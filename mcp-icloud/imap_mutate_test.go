package main

import (
	"os"
	"strconv"
	"testing"
)

// Opt-in, mutating: round-trips flag + folder changes on one real message so
// the effect is reversed by the end of the test. Run with:
// ICLOUD_IT=1 ICLOUD_IT_UID=<uid> ICLOUD_SMTP_USER=... ICLOUD_SMTP_APP_PASSWORD=... go test -run MarkAndMove
func TestIntegration_IMAP_MarkAndMoveRoundTrip(t *testing.T) {
	if os.Getenv("ICLOUD_IT") == "" {
		t.Skip("set ICLOUD_IT=1 to run")
	}
	uidStr := os.Getenv("ICLOUD_IT_UID")
	if uidStr == "" {
		t.Skip("set ICLOUD_IT_UID=<uid of a disposable test message> to run this mutating test")
	}
	n, err := strconv.ParseUint(uidStr, 10, 32)
	if err != nil {
		t.Fatalf("bad ICLOUD_IT_UID: %v", err)
	}
	uid := uint32(n)
	r := imapReader{
		addr: "imap.mail.me.com:993",
		user: os.Getenv("ICLOUD_SMTP_USER"),
		pass: os.Getenv("ICLOUD_SMTP_APP_PASSWORD"),
	}

	if err := r.Mark("INBOX", uid, false); err != nil {
		t.Fatalf("Mark unread failed: %v", err)
	}
	t.Logf("marked uid=%d unread", uid)
	if err := r.Mark("INBOX", uid, true); err != nil {
		t.Fatalf("Mark read failed: %v", err)
	}
	t.Logf("marked uid=%d read", uid)

	if err := r.Move("INBOX", uid, "Archive"); err != nil {
		t.Fatalf("Move to Archive failed: %v", err)
	}
	t.Logf("moved uid=%d to Archive", uid)
	if err := r.Move("Archive", uid, "INBOX"); err != nil {
		t.Fatalf("Move back to INBOX failed: %v", err)
	}
	t.Logf("moved uid=%d back to INBOX", uid)
}
