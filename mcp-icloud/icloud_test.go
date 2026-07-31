package main

import (
	"errors"
	"strings"
	"testing"
)

type fakeMailer struct {
	from string
	to   []string
	msg  []byte
	err  error
}

func (f *fakeMailer) Send(from string, to []string, msg []byte) error {
	f.from, f.to, f.msg = from, to, msg
	return f.err
}

func TestSendEmail_OK(t *testing.T) {
	f := &fakeMailer{}
	err := SendEmail(f, "me@icloud.com", EmailRequest{To: "x@y.co", Subject: "hi", Body: "test"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	s := string(f.msg)
	if !strings.Contains(s, "To: x@y.co") || !strings.Contains(s, "Subject: hi") || !strings.Contains(s, "test") {
		t.Fatalf("malformed message:\n%s", s)
	}
	if f.from != "me@icloud.com" || len(f.to) != 1 {
		t.Fatalf("wrong envelope: from=%s to=%v", f.from, f.to)
	}
}

func TestSendEmail_Validation(t *testing.T) {
	cases := map[string]EmailRequest{
		"invalid to":     {To: "nope", Subject: "s", Body: "b"},
		"empty subject":  {To: "x@y.co", Subject: " ", Body: "b"},
		"injection to":   {To: "x@y.co\r\nBcc: evil@z.co", Subject: "s", Body: "b"},
		"injection subj": {To: "x@y.co", Subject: "s\r\nBcc: evil@z.co", Body: "b"},
	}
	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			if err := SendEmail(&fakeMailer{}, "me@icloud.com", r); err == nil {
				t.Fatal("expected validation failure")
			}
		})
	}
}

func TestSendEmail_PropagatesSMTPError(t *testing.T) {
	f := &fakeMailer{err: errors.New("conn refused")}
	if err := SendEmail(f, "me@icloud.com", EmailRequest{To: "x@y.co", Subject: "s", Body: "b"}); err == nil {
		t.Fatal("expected propagated SMTP error")
	}
}
