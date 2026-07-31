package main

import (
	"errors"
	"testing"
)

type fakeReader struct {
	headers    []MailHeader
	msg        MailMessage
	listErr    error
	readErr    error
	gotMailbox string
	gotLimit   int
	gotUID     uint32
	called     bool
}

func (f *fakeReader) List(mailbox string, limit int) ([]MailHeader, error) {
	f.called = true
	f.gotMailbox, f.gotLimit = mailbox, limit
	return f.headers, f.listErr
}

func (f *fakeReader) Read(mailbox string, uid uint32) (MailMessage, error) {
	f.called = true
	f.gotMailbox, f.gotUID = mailbox, uid
	return f.msg, f.readErr
}

func TestListEmails_Defaults(t *testing.T) {
	f := &fakeReader{}
	if _, err := ListEmails(f, ListRequest{}); err != nil {
		t.Fatalf("err: %v", err)
	}
	if f.gotMailbox != "INBOX" {
		t.Fatalf("default mailbox = %q, want INBOX", f.gotMailbox)
	}
	if f.gotLimit != 10 {
		t.Fatalf("default limit = %d, want 10", f.gotLimit)
	}
}

func TestListEmails_LimitClamp(t *testing.T) {
	cases := map[string]struct{ in, want int }{
		"zero -> default": {0, 10},
		"negative -> default": {-5, 10},
		"over cap -> 50": {999, 50},
		"in range kept": {25, 25},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := &fakeReader{}
			if _, err := ListEmails(f, ListRequest{Limit: c.in}); err != nil {
				t.Fatalf("err: %v", err)
			}
			if f.gotLimit != c.want {
				t.Fatalf("limit %d -> %d, want %d", c.in, f.gotLimit, c.want)
			}
		})
	}
}

func TestListEmails_CustomMailbox(t *testing.T) {
	f := &fakeReader{}
	if _, err := ListEmails(f, ListRequest{Mailbox: "Sent"}); err != nil {
		t.Fatalf("err: %v", err)
	}
	if f.gotMailbox != "Sent" {
		t.Fatalf("mailbox = %q, want Sent", f.gotMailbox)
	}
}

func TestListEmails_PropagatesError(t *testing.T) {
	f := &fakeReader{listErr: errors.New("imap down")}
	if _, err := ListEmails(f, ListRequest{}); err == nil {
		t.Fatal("expected propagated error")
	}
}

func TestReadEmail_OK(t *testing.T) {
	f := &fakeReader{msg: MailMessage{UID: 42, Subject: "hi", Body: "hello"}}
	m, err := ReadEmail(f, ReadRequest{UID: 42})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if m.Subject != "hi" || m.Body != "hello" {
		t.Fatalf("wrong message: %+v", m)
	}
	if f.gotMailbox != "INBOX" || f.gotUID != 42 {
		t.Fatalf("wrong args: mailbox=%s uid=%d", f.gotMailbox, f.gotUID)
	}
}

func TestReadEmail_RejectsZeroUID(t *testing.T) {
	f := &fakeReader{}
	if _, err := ReadEmail(f, ReadRequest{UID: 0}); err == nil {
		t.Fatal("expected error for uid=0")
	}
	if f.called {
		t.Fatal("reader must not be called with invalid uid")
	}
}

func TestReadEmail_PropagatesError(t *testing.T) {
	f := &fakeReader{readErr: errors.New("no such message")}
	if _, err := ReadEmail(f, ReadRequest{UID: 5}); err == nil {
		t.Fatal("expected propagated error")
	}
}
