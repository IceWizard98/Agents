package main

import (
	"errors"
	"strings"
	"testing"
)

func orig() MailMessage {
	return MailMessage{
		UID: 42, From: "boss <boss@work.com>", To: "me@icloud.com",
		Subject: "Q3 numbers", Date: "2026-08-01T10:00:00Z", Body: "here they are",
	}
}

func TestForwardEmail_OK(t *testing.T) {
	r := &fakeReader{msg: orig()}
	m := &fakeMailer{}
	err := ForwardEmail(r, m, "me@icloud.com", ForwardRequest{
		Mailbox: "INBOX", UID: 42, To: "peer@x.co", Note: "fyi",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if r.gotMailbox != "INBOX" || r.gotUID != 42 {
		t.Fatalf("read wrong message: box=%s uid=%d", r.gotMailbox, r.gotUID)
	}
	s := string(m.msg)
	if !strings.Contains(s, "Subject: Fwd: Q3 numbers") {
		t.Fatalf("missing Fwd subject:\n%s", s)
	}
	for _, want := range []string{"fyi", "boss@work.com", "Q3 numbers", "here they are", "Forwarded message"} {
		if !strings.Contains(s, want) {
			t.Fatalf("body missing %q:\n%s", want, s)
		}
	}
	if len(m.to) != 1 || m.to[0] != "peer@x.co" {
		t.Fatalf("wrong envelope to: %v", m.to)
	}
}

func TestForwardEmail_KeepsExistingFwdPrefix(t *testing.T) {
	o := orig()
	o.Subject = "Fwd: already"
	r := &fakeReader{msg: o}
	m := &fakeMailer{}
	if err := ForwardEmail(r, m, "me@icloud.com", ForwardRequest{Mailbox: "INBOX", UID: 1, To: "x@y.co"}); err != nil {
		t.Fatalf("err: %v", err)
	}
	if strings.Contains(string(m.msg), "Fwd: Fwd:") {
		t.Fatalf("double-prefixed subject:\n%s", string(m.msg))
	}
}

func TestForwardEmail_RequiresMailbox(t *testing.T) {
	r := &fakeReader{msg: orig()}
	if err := ForwardEmail(r, &fakeMailer{}, "me@icloud.com", ForwardRequest{UID: 42, To: "x@y.co"}); err == nil {
		t.Fatal("expected error for omitted mailbox")
	}
	if r.called {
		t.Fatal("must not read without a mailbox")
	}
}

func TestForwardEmail_RejectsZeroUID(t *testing.T) {
	r := &fakeReader{msg: orig()}
	if err := ForwardEmail(r, &fakeMailer{}, "me@icloud.com", ForwardRequest{Mailbox: "INBOX", To: "x@y.co"}); err == nil {
		t.Fatal("expected error for uid=0")
	}
	if r.called {
		t.Fatal("must not read with invalid uid")
	}
}

func TestForwardEmail_RequiresRecipient(t *testing.T) {
	r := &fakeReader{msg: orig()}
	if err := ForwardEmail(r, &fakeMailer{}, "me@icloud.com", ForwardRequest{Mailbox: "INBOX", UID: 42, To: "  "}); err == nil {
		t.Fatal("expected error for empty recipient")
	}
}

func TestForwardEmail_RejectsBadRecipientBeforeFetch(t *testing.T) {
	r := &fakeReader{msg: orig()}
	if err := ForwardEmail(r, &fakeMailer{}, "me@icloud.com", ForwardRequest{Mailbox: "INBOX", UID: 42, To: "nope"}); err == nil {
		t.Fatal("expected error for malformed recipient")
	}
	if r.called {
		t.Fatal("must not fetch the message when recipient is malformed")
	}
}

func TestForwardEmail_PropagatesReadError(t *testing.T) {
	r := &fakeReader{readErr: errors.New("fetch failed")}
	if err := ForwardEmail(r, &fakeMailer{}, "me@icloud.com", ForwardRequest{Mailbox: "INBOX", UID: 42, To: "x@y.co"}); err == nil {
		t.Fatal("expected propagated read error")
	}
}

func TestForwardEmail_PropagatesSendError(t *testing.T) {
	r := &fakeReader{msg: orig()}
	m := &fakeMailer{err: errors.New("conn refused")}
	if err := ForwardEmail(r, m, "me@icloud.com", ForwardRequest{Mailbox: "INBOX", UID: 42, To: "x@y.co"}); err == nil {
		t.Fatal("expected propagated send error")
	}
}
