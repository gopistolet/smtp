package mta

import (
	"bytes"
	"log"
	"testing"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/gopistolet/gopistolet/smtp"
)

type testProtocol struct {
	t       *testing.T
	cmds    []smtp.Cmd
	answers []smtp.Answer
}

func getMailWithoutError(a string) *smtp.MailAddress {
	addr, _ := smtp.ParseAddress(a)
	return &addr
}

func (p *testProtocol) Send(cmd smtp.Cmd) {
	if len(p.answers) <= 0 {
		p.t.Errorf("Did not expect an answer got: %v", cmd)
		return
	}

	log.Printf("%#v\n", cmd)

	cmdA, ok := cmd.(smtp.Answer)
	if !ok {
		p.t.Errorf("Expected cmd.Answer got %t", cmd)
		return
	}

	answer := p.answers[0]
	p.answers = p.answers[1:]

	if answer.Status != cmdA.Status {
		p.t.Errorf("Expected answer %d, got %d", answer.Status, cmdA.Status)
		return
	}
}

func (p *testProtocol) GetCmd() (*smtp.Cmd, bool) {
	if len(p.cmds) <= 0 {
		p.t.Errorf("Did not expect to send a cmd")
		return nil, false
	}

	cmd := p.cmds[0]
	p.cmds = p.cmds[1:]

	if cmd == nil {
		return nil, false
	}

	return &cmd, true
}

func (p *testProtocol) Close() {
	if len(p.cmds) > 0 {
		p.t.Errorf("Did not expect connection to be closed, got more commands: %v", p.cmds)
		return
	}

	if len(p.answers) > 0 {
		p.t.Errorf("Did not expect connection to be closed, need more answers: %v", p.answers)
		return
	}
}

// Tests answers for HELO and QUIT
func TestAnswersHeloQuit(t *testing.T) {
	
	Convey("Testing answers for HELO and QUIT", t, func() {
	
		cfg := Config{
			Hostname: "home.sweet.home",
		}
	
		mta := New(cfg)
		if mta == nil {
			t.Fatal("Could not create mta server")
		}
	
		// Test connection with HELO and QUIT
		proto := &testProtocol{
			t: t,
			cmds: []smtp.Cmd{
				smtp.HeloCmd{
					Domain: "some.sender",
				},
				smtp.QuitCmd{},
			},
			answers: []smtp.Answer{
				smtp.Answer{
					Status:  smtp.Ready,
					Message: cfg.Hostname + " Service Ready",
				},
				smtp.Answer{
					Status:  smtp.Ok,
					Message: cfg.Hostname,
				},
				smtp.Answer{
					Status:  smtp.Closing,
					Message: "Bye!",
				},
			},
		}
		mta.HandleClient(proto)
	
		// Test connection with HELO followed by closing the connection
		proto = &testProtocol{
			t: t,
			cmds: []smtp.Cmd{
				smtp.HeloCmd{
					Domain: "some.sender",
				},
				nil,
			},
			answers: []smtp.Answer{
				smtp.Answer{
					Status:  smtp.Ready,
					Message: cfg.Hostname + " Service Ready",
				},
				smtp.Answer{
					Status:  smtp.Ok,
					Message: cfg.Hostname,
				},
			},
		}
		mta.HandleClient(proto)
		
	})
}

// Test answers if we are giving a correct sequence of MAIL,RCPT,DATA commands.
func TestMailAnswersCorrectSequence(t *testing.T) {
	
	Convey("Testing answers for correct sequence of MAIL,RCPT,DATA commands.", t, func() {
	
		cfg := Config{
			Hostname: "home.sweet.home",
		}
	
		mta := New(cfg)
		if mta == nil {
			t.Fatal("Could not create mta server")
		}
	
		proto := &testProtocol{
			t: t,
			cmds: []smtp.Cmd{
				smtp.HeloCmd{
					Domain: "some.sender",
				},
				smtp.MailCmd{
					From: getMailWithoutError("someone@somewhere.test"),
				},
				smtp.RcptCmd{
					To: getMailWithoutError("guy1@somewhere.test"),
				},
				smtp.RcptCmd{
					To: getMailWithoutError("guy2@somewhere.test"),
				},
				smtp.DataCmd{
					R: *smtp.NewDataReader(bytes.NewReader([]byte("Some test email\n.\n"))),
				},
				smtp.QuitCmd{},
			},
			answers: []smtp.Answer{
				smtp.Answer{
					Status:  smtp.Ready,
					Message: cfg.Hostname + " Service Ready",
				},
				smtp.Answer{
					Status:  smtp.Ok,
					Message: cfg.Hostname,
				},
				smtp.Answer{
					Status:  smtp.Ok,
					Message: "OK",
				},
				smtp.Answer{
					Status:  smtp.Ok,
					Message: "OK",
				},
				smtp.Answer{
					Status:  smtp.Ok,
					Message: "OK",
				},
				smtp.Answer{
					Status:  smtp.StartData,
					Message: "OK",
				},
				smtp.Answer{
					Status:  smtp.Closing,
					Message: "Bye!",
				},
			},
		}
		mta.HandleClient(proto)
		
	})
}

// Tests answers if we are giving a wrong sequence of MAIL,RCPT,DATA commands.
func TestMailAnswersWrongSequence(t *testing.T) {
	
	Convey("Testing wrong sequence of MAIL,RCPT,DATA commands.", t, func() {
	
		cfg := Config{
			Hostname: "home.sweet.home",
		}
	
		mta := New(cfg)
		if mta == nil {
			t.Fatal("Could not create mta server")
		}
	
		// RCPT before MAIl
		proto := &testProtocol{
			t: t,
			cmds: []smtp.Cmd{
				smtp.HeloCmd{
					Domain: "some.sender",
				},
				smtp.RcptCmd{
					To: getMailWithoutError("guy1@somewhere.test"),
				},
				smtp.QuitCmd{},
			},
			answers: []smtp.Answer{
				smtp.Answer{
					Status:  smtp.Ready,
					Message: cfg.Hostname + " Service Ready",
				},
				smtp.Answer{
					Status:  smtp.Ok,
					Message: cfg.Hostname,
				},
				smtp.Answer{
					Status:  smtp.BadSequence,
					Message: "Need mail before RCPT",
				},
				smtp.Answer{
					Status:  smtp.Closing,
					Message: "Bye!",
				},
			},
		}
		mta.HandleClient(proto)
	
		// DATA before MAIL
		proto = &testProtocol{
			t: t,
			cmds: []smtp.Cmd{
				smtp.HeloCmd{
					Domain: "some.sender",
				},
				smtp.DataCmd{},
				smtp.QuitCmd{},
			},
			answers: []smtp.Answer{
				smtp.Answer{
					Status:  smtp.Ready,
					Message: cfg.Hostname + " Service Ready",
				},
				smtp.Answer{
					Status:  smtp.Ok,
					Message: cfg.Hostname,
				},
				smtp.Answer{
					Status:  smtp.BadSequence,
					Message: "Need mail before DATA",
				},
				smtp.Answer{
					Status:  smtp.Closing,
					Message: "Bye!",
				},
			},
		}
		mta.HandleClient(proto)
	
		// DATA before RCPT
		proto = &testProtocol{
			t: t,
			cmds: []smtp.Cmd{
				smtp.HeloCmd{
					Domain: "some.sender",
				},
				smtp.MailCmd{
					From: getMailWithoutError("guy@somewhere.test"),
				},
				smtp.DataCmd{},
				smtp.QuitCmd{},
			},
			answers: []smtp.Answer{
				smtp.Answer{
					Status:  smtp.Ready,
					Message: cfg.Hostname + " Service Ready",
				},
				smtp.Answer{
					Status:  smtp.Ok,
					Message: cfg.Hostname,
				},
				smtp.Answer{
					Status:  smtp.Ok,
					Message: "OK",
				},
				smtp.Answer{
					Status:  smtp.BadSequence,
					Message: "Need RCPT before DATA",
				},
				smtp.Answer{
					Status:  smtp.Closing,
					Message: "Bye!",
				},
			},
		}
		mta.HandleClient(proto)
	
		// Multiple MAIL
		proto = &testProtocol{
			t: t,
			cmds: []smtp.Cmd{
				smtp.HeloCmd{
					Domain: "some.sender",
				},
				smtp.MailCmd{
					From: getMailWithoutError("guy@somewhere.test"),
				},
				smtp.RcptCmd{
					To: getMailWithoutError("someone@somewhere.test"),
				},
				smtp.MailCmd{
					From: getMailWithoutError("someguy@somewhere.test"),
				},
				smtp.QuitCmd{},
			},
			answers: []smtp.Answer{
				smtp.Answer{
					Status:  smtp.Ready,
					Message: cfg.Hostname + " Service Ready",
				},
				smtp.Answer{
					Status:  smtp.Ok,
					Message: cfg.Hostname,
				},
				smtp.Answer{
					Status:  smtp.Ok,
					Message: "OK",
				},
				smtp.Answer{
					Status:  smtp.Ok,
					Message: "OK",
				},
				smtp.Answer{
					Status:  smtp.BadSequence,
					Message: "Sender already specified",
				},
				smtp.Answer{
					Status:  smtp.Closing,
					Message: "Bye!",
				},
			},
		}
		mta.HandleClient(proto)
		
	})
}

// Tests if our state gets reset correctly.
func TestReset(t *testing.T) {
	
	Convey("Testing reset", t, func() {
	
		cfg := Config{
			Hostname: "home.sweet.home",
		}
	
		mta := New(cfg)
		if mta == nil {
			t.Fatal("Could not create mta server")
		}
	
		// Test if state gets reset after sending email
		proto := &testProtocol{
			t: t,
			cmds: []smtp.Cmd{
				smtp.HeloCmd{
					Domain: "some.sender",
				},
				smtp.MailCmd{
					From: getMailWithoutError("someone@somewhere.test"),
				},
				smtp.RcptCmd{
					To: getMailWithoutError("guy1@somewhere.test"),
				},
				smtp.DataCmd{
					R: *smtp.NewDataReader(bytes.NewReader([]byte("Some email content\n.\n"))),
				},
				smtp.RcptCmd{
					To: getMailWithoutError("someguy@somewhere.test"),
				},
				smtp.QuitCmd{},
			},
			answers: []smtp.Answer{
				smtp.Answer{
					Status:  smtp.Ready,
					Message: cfg.Hostname + " Service Ready",
				},
				smtp.Answer{
					Status:  smtp.Ok,
					Message: cfg.Hostname,
				},
				smtp.Answer{
					Status:  smtp.Ok,
					Message: "OK",
				},
				smtp.Answer{
					Status:  smtp.Ok,
					Message: "OK",
				},
				smtp.Answer{
					Status:  smtp.StartData,
					Message: "OK",
				},
				smtp.Answer{
					Status:  smtp.BadSequence,
					Message: "Need mail before RCPT",
				},
				smtp.Answer{
					Status:  smtp.Closing,
					Message: "Bye!",
				},
			},
		}
		mta.HandleClient(proto)
	
		// Test if we can reset state ourselves.
		proto = &testProtocol{
			t: t,
			cmds: []smtp.Cmd{
				smtp.HeloCmd{
					Domain: "some.sender",
				},
				smtp.MailCmd{
					From: getMailWithoutError("someone@somewhere.test"),
				},
				smtp.RcptCmd{
					To: getMailWithoutError("guy1@somewhere.test"),
				},
				smtp.RsetCmd{},
				smtp.MailCmd{
					From: getMailWithoutError("someone@somewhere.test"),
				},
				smtp.RcptCmd{
					To: getMailWithoutError("guy1@somewhere.test"),
				},
				smtp.DataCmd{
					R: *smtp.NewDataReader(bytes.NewReader([]byte("Some email\n.\n"))),
				},
				smtp.QuitCmd{},
			},
			answers: []smtp.Answer{
				smtp.Answer{
					Status:  smtp.Ready,
					Message: cfg.Hostname + " Service Ready",
				},
				smtp.Answer{
					Status:  smtp.Ok,
					Message: cfg.Hostname,
				},
				smtp.Answer{
					Status:  smtp.Ok,
					Message: "OK",
				},
				smtp.Answer{
					Status:  smtp.Ok,
					Message: "OK",
				},
				smtp.Answer{
					Status:  smtp.Ok,
					Message: "OK",
				},
				smtp.Answer{
					Status:  smtp.Ok,
					Message: "OK",
				},
				smtp.Answer{
					Status:  smtp.Ok,
					Message: "OK",
				},
				smtp.Answer{
					Status:  smtp.StartData,
					Message: "OK",
				},
				smtp.Answer{
					Status:  smtp.Closing,
					Message: "Bye!",
				},
			},
		}
		mta.HandleClient(proto)
		
	})
}
