package main

import (
	"errors"
	"testing"
	"time"
)

// ---- SearchEmails ----

type fakeSearcher struct {
	headers  []MailHeader
	err      error
	gotBox   string
	gotQ     SearchQuery
	gotLimit int
}

func (f *fakeSearcher) Search(mailbox string, q SearchQuery, limit int) ([]MailHeader, error) {
	f.gotBox, f.gotQ, f.gotLimit = mailbox, q, limit
	return f.headers, f.err
}

func TestSearchEmails_RequiresAtLeastOneFilter(t *testing.T) {
	f := &fakeSearcher{}
	if _, err := SearchEmails(f, SearchRequest{}); err == nil {
		t.Fatal("expected error for empty filter set")
	}
}

func TestSearchEmails_ByFrom(t *testing.T) {
	f := &fakeSearcher{}
	if _, err := SearchEmails(f, SearchRequest{From: "boss@work.com"}); err != nil {
		t.Fatalf("err: %v", err)
	}
	if f.gotQ.From != "boss@work.com" {
		t.Fatalf("From = %q", f.gotQ.From)
	}
	if f.gotBox != "INBOX" {
		t.Fatalf("mailbox = %q, want INBOX default", f.gotBox)
	}
	if f.gotLimit != 10 {
		t.Fatalf("limit = %d, want default 10", f.gotLimit)
	}
}

func TestSearchEmails_ByTo(t *testing.T) {
	f := &fakeSearcher{}
	if _, err := SearchEmails(f, SearchRequest{To: "antonio@example.com"}); err != nil {
		t.Fatalf("err: %v", err)
	}
	if f.gotQ.To != "antonio@example.com" {
		t.Fatalf("To = %q", f.gotQ.To)
	}
}

func TestSearchEmails_ByDateRange(t *testing.T) {
	f := &fakeSearcher{}
	_, err := SearchEmails(f, SearchRequest{Since: "2026-01-01", Before: "2026-02-01"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	wantSince := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	wantBefore := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	if !f.gotQ.Since.Equal(wantSince) {
		t.Fatalf("Since = %v, want %v", f.gotQ.Since, wantSince)
	}
	if !f.gotQ.Before.Equal(wantBefore) {
		t.Fatalf("Before = %v, want %v", f.gotQ.Before, wantBefore)
	}
}

func TestSearchEmails_InvalidDate(t *testing.T) {
	f := &fakeSearcher{}
	if _, err := SearchEmails(f, SearchRequest{Since: "not-a-date"}); err == nil {
		t.Fatal("expected error for invalid since date")
	}
	if _, err := SearchEmails(f, SearchRequest{Before: "2026/01/01"}); err == nil {
		t.Fatal("expected error for wrong-format before date")
	}
}

func TestSearchEmails_UnreadOnly(t *testing.T) {
	f := &fakeSearcher{}
	if _, err := SearchEmails(f, SearchRequest{UnreadOnly: true}); err != nil {
		t.Fatalf("err: %v", err)
	}
	if !f.gotQ.UnreadOnly {
		t.Fatal("UnreadOnly not propagated")
	}
}

func TestSearchEmails_LimitClamp(t *testing.T) {
	f := &fakeSearcher{}
	if _, err := SearchEmails(f, SearchRequest{From: "x", Limit: 999}); err != nil {
		t.Fatalf("err: %v", err)
	}
	if f.gotLimit != 50 {
		t.Fatalf("limit = %d, want capped 50", f.gotLimit)
	}
}

func TestSearchEmails_PropagatesError(t *testing.T) {
	f := &fakeSearcher{err: errors.New("search failed")}
	if _, err := SearchEmails(f, SearchRequest{Subject: "x"}); err == nil {
		t.Fatal("expected propagated error")
	}
}

// ---- MarkEmail ----

type fakeFlagger struct {
	err      error
	gotBox   string
	gotUID   uint32
	gotRead  bool
	wasCalled bool
}

func (f *fakeFlagger) Mark(mailbox string, uid uint32, read bool) error {
	f.wasCalled = true
	f.gotBox, f.gotUID, f.gotRead = mailbox, uid, read
	return f.err
}

func TestMarkEmail_OK(t *testing.T) {
	f := &fakeFlagger{}
	if err := MarkEmail(f, MarkRequest{UID: 7, Read: true}); err != nil {
		t.Fatalf("err: %v", err)
	}
	if f.gotBox != "INBOX" || f.gotUID != 7 || !f.gotRead {
		t.Fatalf("wrong args: box=%s uid=%d read=%v", f.gotBox, f.gotUID, f.gotRead)
	}
}

func TestMarkEmail_RejectsZeroUID(t *testing.T) {
	f := &fakeFlagger{}
	if err := MarkEmail(f, MarkRequest{UID: 0}); err == nil {
		t.Fatal("expected error for uid=0")
	}
	if f.wasCalled {
		t.Fatal("flagger must not be called with invalid uid")
	}
}

func TestMarkEmail_PropagatesError(t *testing.T) {
	f := &fakeFlagger{err: errors.New("store failed")}
	if err := MarkEmail(f, MarkRequest{UID: 1}); err == nil {
		t.Fatal("expected propagated error")
	}
}

// ---- MoveEmail ----

type fakeMover struct {
	err    error
	gotBox string
	gotUID uint32
	gotTo  string
	wasCalled bool
}

func (f *fakeMover) Move(mailbox string, uid uint32, to string) error {
	f.wasCalled = true
	f.gotBox, f.gotUID, f.gotTo = mailbox, uid, to
	return f.err
}

func TestMoveEmail_OK(t *testing.T) {
	f := &fakeMover{}
	if err := MoveEmail(f, MoveRequest{UID: 3, To: "Archive"}); err != nil {
		t.Fatalf("err: %v", err)
	}
	if f.gotBox != "INBOX" || f.gotUID != 3 || f.gotTo != "Archive" {
		t.Fatalf("wrong args: box=%s uid=%d to=%s", f.gotBox, f.gotUID, f.gotTo)
	}
}

func TestMoveEmail_RejectsZeroUID(t *testing.T) {
	f := &fakeMover{}
	if err := MoveEmail(f, MoveRequest{To: "Archive"}); err == nil {
		t.Fatal("expected error for uid=0")
	}
	if f.wasCalled {
		t.Fatal("mover must not be called with invalid uid")
	}
}

func TestMoveEmail_RequiresDestination(t *testing.T) {
	f := &fakeMover{}
	if err := MoveEmail(f, MoveRequest{UID: 1, To: "  "}); err == nil {
		t.Fatal("expected error for empty destination")
	}
	if f.wasCalled {
		t.Fatal("mover must not be called without a destination")
	}
}

func TestMoveEmail_PropagatesError(t *testing.T) {
	f := &fakeMover{err: errors.New("move failed")}
	if err := MoveEmail(f, MoveRequest{UID: 1, To: "Trash"}); err == nil {
		t.Fatal("expected propagated error")
	}
}
