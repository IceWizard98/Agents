package main

import (
	"fmt"
	"net/smtp"
	"regexp"
	"strings"
)

// Mailer sends a mail. Hexagonal port: real adapter uses net/smtp, tests a fake.
type Mailer interface {
	Send(from string, to []string, msg []byte) error
}

// smtpMailer sends via iCloud (smtp.mail.me.com:587, STARTTLS + PLAIN auth).
type smtpMailer struct {
	host, port, user, pass string
}

func (m smtpMailer) Send(from string, to []string, msg []byte) error {
	auth := smtp.PlainAuth("", m.user, m.pass, m.host)
	return smtp.SendMail(m.host+":"+m.port, auth, from, to, msg)
}

var emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// EmailRequest is the send_email tool payload.
type EmailRequest struct {
	To      string `json:"to" jsonschema:"recipient"`
	Subject string `json:"subject" jsonschema:"subject"`
	Body    string `json:"body" jsonschema:"message body"`
}

// buildMessage composes a minimal RFC 5322 message (CRLF headers).
func buildMessage(from string, r EmailRequest) ([]byte, error) {
	r.To = strings.TrimSpace(r.To)
	if !emailRe.MatchString(r.To) {
		return nil, fmt.Errorf("invalid recipient: %q", r.To)
	}
	if strings.TrimSpace(r.Subject) == "" {
		return nil, fmt.Errorf("empty subject")
	}
	// no header injection: reject newlines in subject/to
	if strings.ContainsAny(r.Subject, "\r\n") || strings.ContainsAny(r.To, "\r\n") {
		return nil, fmt.Errorf("header injection detected")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", r.To)
	fmt.Fprintf(&b, "Subject: %s\r\n", r.Subject)
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
	b.WriteString(r.Body)
	return []byte(b.String()), nil
}

// SendEmail validates and sends. from = ICLOUD_SMTP_USER.
func SendEmail(m Mailer, from string, r EmailRequest) error {
	msg, err := buildMessage(from, r)
	if err != nil {
		return err
	}
	if err := m.Send(from, []string{r.To}, msg); err != nil {
		return fmt.Errorf("smtp send: %w", err)
	}
	return nil
}
