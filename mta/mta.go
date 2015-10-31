package mta

import (
	"crypto/tls"
	"fmt"
	"io/ioutil"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/gopistolet/gopistolet/log"
	"github.com/gopistolet/gopistolet/smtp"
)

type Config struct {
	Hostname string
	Port     uint32
	TlsCert  string
	TlsKey   string
}

// Session id
type Id struct {
	Timestamp int64
	Counter   uint32
}

func (id *Id) String() string {
	return strconv.FormatInt(id.Timestamp, 16) + strconv.FormatInt(int64(id.Counter), 16)
}

var globalCounter uint32 = 0
var globalCounterLock = &sync.Mutex{}

func generateSessionId() Id {
	globalCounterLock.Lock()
	defer globalCounterLock.Unlock()
	globalCounter++
	return Id{Timestamp: time.Now().Unix(), Counter: globalCounter}

}

// State contains all the state for a single client
type State struct {
	From         *smtp.MailAddress
	To           []*smtp.MailAddress
	Data         []byte
	EightBitMIME bool
	Secure       bool
	SessionId    Id
	Ip           string
}

// Handler is the interface that will be used when a mail was received.
type Handler interface {
	HandleMail(*State)
}

// HandlerFunc is a wrapper to allow normal functions to be used as a handler.
type HandlerFunc func(*State)

func (h HandlerFunc) HandleMail(state *State) {
	h(state)
}

// reset the state
func (s *State) reset() {
	s.From = nil
	s.To = []*smtp.MailAddress{}
	s.Data = []byte{}
	s.EightBitMIME = false
}

// Checks the state if the client can send a MAIL command.
func (s *State) canReceiveMail() (bool, string) {
	if s.From != nil {
		return false, "Sender already specified"
	}

	return true, ""
}

// Checks the state if the client can send a RCPT command.
func (s *State) canReceiveRcpt() (bool, string) {
	if s.From == nil {
		return false, "Need mail before RCPT"
	}

	return true, ""
}

// Checks the state if the client can send a DATA command.
func (s *State) canReceiveData() (bool, string) {
	if s.From == nil {
		return false, "Need mail before DATA"
	}

	if len(s.To) == 0 {
		return false, "Need RCPT before DATA"
	}

	return true, ""
}

// Mta Represents an MTA server
type Mta struct {
	config Config
	// The handler to be called when a mail is received.
	MailHandler Handler
	// The config for tls connection. Nil if not supported.
	TlsConfig *tls.Config
	// When shutting down this channel is closed, no new connections should be handled then.
	// But existing connections can continue untill quitC is closed.
	shutDownC chan bool
	// When this is closed existing connections should stop.
	quitC chan bool
	wg    sync.WaitGroup
}

// New Create a new MTA server that doesn't handle the protocol.
func New(c Config, h Handler) *Mta {
	mta := &Mta{
		config:      c,
		MailHandler: h,
		quitC:       make(chan bool),
		shutDownC:   make(chan bool),
	}

	if c.TlsCert != "" && c.TlsKey != "" {
		cert, err := tls.LoadX509KeyPair(c.TlsCert, c.TlsKey)
		if err != nil {
			log.Warnf("Could not load keypair: %v", err)
		} else {
			mta.TlsConfig = &tls.Config{
				Certificates: []tls.Certificate{cert},
			}
		}
	}

	return mta
}

func (s *Mta) Stop() {
	log.Printf("Received stop command. Sending shutdown event...")
	close(s.shutDownC)
	// Give existing connections some time to finish.
	t := time.Duration(10)
	log.Printf("Waiting for a maximum of %d seconds...", t)
	time.Sleep(t * time.Second)
	log.Printf("Sending force quit event...")
	close(s.quitC)
}

func (s *Mta) hasTls() bool {
	return s.TlsConfig != nil
}

// Same as the Mta struct but has methods for handling socket connections.
type DefaultMta struct {
	mta *Mta
}

// NewDefault Create a new MTA server with a
// socket protocol implementation.
func NewDefault(c Config, h Handler) *DefaultMta {
	mta := &DefaultMta{
		mta: New(c, h),
	}
	if mta == nil {
		return nil
	}

	return mta
}

func (s *DefaultMta) Stop() {
	s.mta.Stop()
}

func (s *DefaultMta) ListenAndServe() error {
	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", s.mta.config.Hostname, s.mta.config.Port))
	if err != nil {
		log.Errorln("Could not start listening: %v", err)
		return err
	}

	// Close the listener so that listen well return from ln.Accept().
	go func() {
		_, ok := <-s.mta.shutDownC
		if !ok {
			ln.Close()
		}
	}()

	err = s.listen(ln)
	log.Printf("Waiting for connections to close...")
	s.mta.wg.Wait()
	return err
}

func (s *DefaultMta) listen(ln net.Listener) error {
	defer ln.Close()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				log.Printf("Accept error: %v", err)
				continue
			}
			// Assume this means listener was closed.
			if noe, ok := err.(*net.OpError); ok && !noe.Temporary() {
				log.Printf("Listener is closed, stopping listen loop...")
				return nil
			}
			return err
		}

		s.mta.wg.Add(1)
		go s.serve(c)
	}

	// Dead code
	panic("Can't get here")
}

func (s *DefaultMta) serve(c net.Conn) {
	defer s.mta.wg.Done()

	proto := smtp.NewMtaProtocol(c)
	if proto == nil {
		log.Errorf("Could not create Mta protocol")
		c.Close()
		return
	}
	ip, _, _ := net.SplitHostPort(c.RemoteAddr().String())
	s.mta.HandleClient(proto, ip)
}

// HandleClient Start communicating with a client
func (s *Mta) HandleClient(proto smtp.Protocol, ip string) {
	//log.Printf("Received connection")

	// Hold state for this client connection
	state := State{}
	state.reset()
	state.SessionId = generateSessionId()
	state.Ip = ip

	log.WithFields(log.Fields{
		"SessionId": state.SessionId.String(),
		"Ip":        state.Ip,
	}).Debug("Received connection")

	// Start with welcome message
	proto.Send(smtp.Answer{
		Status:  smtp.Ready,
		Message: s.config.Hostname + " Service Ready",
	})

	var c *smtp.Cmd
	var err error

	quit := false
	cmdC := make(chan bool)

	nextCmd := func() bool {
		go func() {
			for {
				c, err = proto.GetCmd()

				if err != nil {
					if err == smtp.ErrLtl {
						proto.Send(smtp.Answer{
							Status:  smtp.SyntaxError,
							Message: "Line too long.",
						})
					} else {
						// Not a line too long error. What to do?
						cmdC <- true
						return
					}
				} else {
					break
				}
			}
			cmdC <- false
		}()

		select {
		case _, ok := <-s.quitC:
			if !ok {
				proto.Send(smtp.Answer{
					Status:  smtp.ShuttingDown,
					Message: "Server is going down.",
				})
				return true
			}
		case q := <-cmdC:
			return q

		}

		return false
	}

	quit = nextCmd()

	for quit == false {

		//log.Printf("Received cmd: %#v", *c)

		switch cmd := (*c).(type) {
		case smtp.HeloCmd:
			proto.Send(smtp.Answer{
				Status:  smtp.Ok,
				Message: s.config.Hostname,
			})

		case smtp.EhloCmd:
			state.reset()

			messages := []string{s.config.Hostname, "8BITMIME"}
			if s.hasTls() && !state.Secure {
				messages = append(messages, "STARTTLS")
			}

			messages = append(messages, "OK")

			proto.Send(smtp.MultiAnswer{
				Status:   smtp.Ok,
				Messages: messages,
			})

		case smtp.QuitCmd:
			proto.Send(smtp.Answer{
				Status:  smtp.Closing,
				Message: "Bye!",
			})
			quit = true

		case smtp.MailCmd:
			if ok, reason := state.canReceiveMail(); !ok {
				proto.Send(smtp.Answer{
					Status:  smtp.BadSequence,
					Message: reason,
				})
				break
			}

			state.From = cmd.From
			state.EightBitMIME = cmd.EightBitMIME
			message := "Sender"
			if state.EightBitMIME {
				message += " and 8BITMIME"
			}
			message += " ok"

			proto.Send(smtp.Answer{
				Status:  smtp.Ok,
				Message: message,
			})

		case smtp.RcptCmd:
			if ok, reason := state.canReceiveRcpt(); !ok {
				proto.Send(smtp.Answer{
					Status:  smtp.BadSequence,
					Message: reason,
				})
				break
			}

			state.To = append(state.To, cmd.To)

			proto.Send(smtp.Answer{
				Status:  smtp.Ok,
				Message: "OK",
			})

		case smtp.DataCmd:
			if ok, reason := state.canReceiveData(); !ok {
				/*
					RFC 5321 3.3

					If there was no MAIL, or no RCPT, command, or all such commands were
					rejected, the server MAY return a "command out of sequence" (503) or
					"no valid recipients" (554) reply in response to the DATA command.
					If one of those replies (or any other 5yz reply) is received, the
					client MUST NOT send the message data; more generally, message data
					MUST NOT be sent unless a 354 reply is received.
				*/
				proto.Send(smtp.Answer{
					Status:  smtp.BadSequence,
					Message: reason,
				})
				break
			}

			message := "Start"
			if state.EightBitMIME {
				message += " 8BITMIME"
			}
			message += " mail input; end with <CRLF>.<CRLF>"
			proto.Send(smtp.Answer{
				Status:  smtp.StartData,
				Message: message,
			})

		tryAgain:
			tmpData, err := ioutil.ReadAll(&cmd.R)
			state.Data = append(state.Data, tmpData...)
			if err == smtp.ErrLtl {
				proto.Send(smtp.Answer{
					// SyntaxError or 552 error? or something else?
					Status:  smtp.SyntaxError,
					Message: "Line too long",
				})
				goto tryAgain
			} else if err == smtp.ErrIncomplete {
				// I think this can only happen on a socket if it gets closed before receiving the full data.
				proto.Send(smtp.Answer{
					Status:  smtp.SyntaxError,
					Message: "Could not parse mail data",
				})
				state.reset()
				break

			} else if err != nil {
				//panic(err)
				log.WithFields(log.Fields{
					"SessionId": state.SessionId.String(),
				}).Panic(err)
			}

			s.MailHandler.HandleMail(&state)

			proto.Send(smtp.Answer{
				Status:  smtp.Ok,
				Message: "Mail delivered",
			})

			// Reset state after mail was handled so we can start from a clean slate.
			state.reset()

		case smtp.RsetCmd:
			state.reset()
			proto.Send(smtp.Answer{
				Status:  smtp.Ok,
				Message: "OK",
			})

		case smtp.StartTlsCmd:
			if !s.hasTls() {
				proto.Send(smtp.Answer{
					Status:  smtp.NotImplemented,
					Message: "STARTTLS is not implemented",
				})
				break
			}

			if state.Secure {
				proto.Send(smtp.Answer{
					Status:  smtp.NotImplemented,
					Message: "Already in TLS mode",
				})
				break
			}

			proto.Send(smtp.Answer{
				Status:  smtp.Ready,
				Message: "Ready for TLS handshake",
			})

			err := proto.StartTls(s.TlsConfig)
			if err != nil {
				//log.Println("Could not enable TLS mode")
				log.WithFields(log.Fields{
					"SessionId": state.SessionId.String(),
				}).Info("Yay, we are using TLS now")
				// --> TODO: what log level should this be?
				break
			}

			//log.Println("Yay, we are using TLS now")
			log.WithFields(log.Fields{
				"SessionId": state.SessionId.String(),
			}).Debug("Yay, we are using TLS now")
			state.reset()
			state.Secure = true

		case smtp.NoopCmd:
			proto.Send(smtp.Answer{
				Status:  smtp.Ok,
				Message: "OK",
			})

		case smtp.VrfyCmd, smtp.ExpnCmd, smtp.SendCmd, smtp.SomlCmd, smtp.SamlCmd:
			proto.Send(smtp.Answer{
				Status:  smtp.NotImplemented,
				Message: "Command not implemented",
			})

		case smtp.InvalidCmd:
			// TODO: Is this correct? An InvalidCmd is a known command with
			// invalid arguments. So we should send smtp.SyntaxErrorParam?
			// Is InvalidCmd a good name for this kind of error?
			proto.Send(smtp.Answer{
				Status:  smtp.SyntaxErrorParam,
				Message: cmd.Info,
			})

		case smtp.UnknownCmd:
			proto.Send(smtp.Answer{
				Status:  smtp.SyntaxError,
				Message: "Command not recognized",
			})

		default:
			// TODO: We get here if the switch does not handle all Cmd's defined
			// in protocol.go. That means we forgot to add it here. This should ideally
			// be checked at compile time. But if we get here anyway we probably shouldn't
			// crash...
			log.Fatalf("Command not implemented: %#v", cmd)
		}

		if quit {
			break
		}

		quit = nextCmd()
	}

	proto.Close()
	//log.Printf("Closed connection")
}
