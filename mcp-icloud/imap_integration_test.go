package main

import (
	"os"
	"testing"
	"time"
)

// Opt-in integration test: hits the real iCloud IMAP server.
// Run with: ICLOUD_IT=1 ICLOUD_SMTP_USER=... ICLOUD_SMTP_APP_PASSWORD=... go test -run Integration
func TestIntegration_IMAP_ListAndRead(t *testing.T) {
	if os.Getenv("ICLOUD_IT") == "" {
		t.Skip("set ICLOUD_IT=1 to run the live iCloud IMAP test")
	}
	user := os.Getenv("ICLOUD_SMTP_USER")
	pass := os.Getenv("ICLOUD_SMTP_APP_PASSWORD")
	if user == "" || pass == "" {
		t.Fatal("ICLOUD_SMTP_USER / ICLOUD_SMTP_APP_PASSWORD required")
	}
	r := imapReader{addr: "imap.mail.me.com:993", user: user, pass: pass}

	headers, err := r.List("INBOX", 3)
	if err != nil {
		t.Fatalf("List failed (auth/connectivity?): %v", err)
	}
	t.Logf("INBOX returned %d headers", len(headers))
	for _, h := range headers {
		t.Logf("  uid=%d from=%q subject=%q date=%s", h.UID, h.From, h.Subject, h.Date)
	}
	if len(headers) == 0 {
		return // empty inbox is a valid result; auth still succeeded
	}

	msg, err := r.Read("INBOX", headers[0].UID)
	if err != nil {
		t.Fatalf("Read uid=%d failed: %v", headers[0].UID, err)
	}
	t.Logf("read uid=%d subject=%q body=%d bytes", msg.UID, msg.Subject, len(msg.Body))
}

func TestIntegration_IMAP_Mailboxes(t *testing.T) {
	if os.Getenv("ICLOUD_IT") == "" {
		t.Skip("set ICLOUD_IT=1 to run the live iCloud IMAP test")
	}
	r := imapReader{
		addr: "imap.mail.me.com:993",
		user: os.Getenv("ICLOUD_SMTP_USER"),
		pass: os.Getenv("ICLOUD_SMTP_APP_PASSWORD"),
	}
	boxes, err := r.Mailboxes()
	if err != nil {
		t.Fatalf("Mailboxes failed: %v", err)
	}
	t.Logf("mailboxes: %v", boxes)
	if len(boxes) == 0 {
		t.Fatal("expected at least one selectable mailbox")
	}
}

func TestIntegration_IMAP_Search(t *testing.T) {
	if os.Getenv("ICLOUD_IT") == "" {
		t.Skip("set ICLOUD_IT=1 to run the live iCloud IMAP test")
	}
	r := imapReader{
		addr: "imap.mail.me.com:993",
		user: os.Getenv("ICLOUD_SMTP_USER"),
		pass: os.Getenv("ICLOUD_SMTP_APP_PASSWORD"),
	}
	// Broad since-filter (last ~2 years) so this passes regardless of inbox
	// contents; the point is exercising the real SEARCH command end to end.
	since, err := time.Parse(dateLayout, "2024-01-01")
	if err != nil {
		t.Fatalf("bad test fixture date: %v", err)
	}
	headers, err := r.Search("INBOX", SearchQuery{Since: since}, 5)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	t.Logf("search returned %d headers", len(headers))
	for _, h := range headers {
		t.Logf("  uid=%d from=%q subject=%q date=%s", h.UID, h.From, h.Subject, h.Date)
	}
}

// mark_email / move_email mutate real mailbox state — not covered by the
// default opt-in test to avoid surprising side effects on the live account.
// Verified manually against a disposable test message during development.
