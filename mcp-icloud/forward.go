package main

import (
	"fmt"
	"strings"
)

// ForwardRequest is the forward_email tool payload. Mailbox (source) is
// REQUIRED — IMAP UIDs are per-mailbox, so it must name the mailbox the UID
// came from, same as read/mark/move.
type ForwardRequest struct {
	Mailbox string `json:"mailbox" jsonschema:"REQUIRED source mailbox the UID belongs to (UIDs are per-mailbox); pass the same mailbox you searched/listed"`
	UID     uint32 `json:"uid" jsonschema:"UID of the message to forward (from list_emails or search_emails)"`
	To      string `json:"to" jsonschema:"recipient to forward to"`
	Note    string `json:"note,omitempty" jsonschema:"optional note prepended above the forwarded message"`
}

// ForwardEmail reads the original message by UID and re-sends it to a new
// recipient. Reuses ReadEmail (fetch + per-mailbox UID validation) and
// SendEmail (compose + SMTP + recipient/injection validation).
func ForwardEmail(r MailReader, m Mailer, from string, req ForwardRequest) error {
	mailbox := strings.TrimSpace(req.Mailbox)
	if mailbox == "" {
		return fmt.Errorf("mailbox is required")
	}
	to := strings.TrimSpace(req.To)
	if to == "" {
		return fmt.Errorf("to (recipient) is required")
	}
	// Validate recipient up-front so a malformed address fails before the IMAP
	// fetch, not after. SendEmail re-checks — this only saves the round-trip.
	if !emailRe.MatchString(to) {
		return fmt.Errorf("invalid recipient: %q", to)
	}
	orig, err := ReadEmail(r, ReadRequest{Mailbox: mailbox, UID: req.UID})
	if err != nil {
		return err
	}

	subject := orig.Subject
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(subject)), "fwd:") {
		subject = "Fwd: " + subject
	}

	var b strings.Builder
	if note := strings.TrimSpace(req.Note); note != "" {
		b.WriteString(note)
		b.WriteString("\n\n")
	}
	b.WriteString("---------- Forwarded message ----------\n")
	fmt.Fprintf(&b, "From: %s\n", orig.From)
	fmt.Fprintf(&b, "Date: %s\n", orig.Date)
	fmt.Fprintf(&b, "Subject: %s\n", orig.Subject)
	fmt.Fprintf(&b, "To: %s\n\n", orig.To)
	b.WriteString(orig.Body)

	return SendEmail(m, from, EmailRequest{To: to, Subject: subject, Body: b.String()})
}
