package main

import (
	"fmt"
	"strings"
	"time"
)

const dateLayout = "2006-01-02"

// SearchQuery is a mailbox-agnostic search filter — a domain type, not the
// imap package's SearchCriteria, so the port stays decoupled from the wire
// format the adapter happens to use.
type SearchQuery struct {
	From       string
	To         string
	Subject    string
	Since      time.Time // zero = no lower bound
	Before     time.Time // zero = no upper bound
	UnreadOnly bool
}

// SearchRequest is the search_emails tool payload. All fields are optional
// filters (SearchEmails rejects the request if none are set) — omitempty
// keeps them out of the generated schema's "required" list.
type SearchRequest struct {
	Mailbox    string `json:"mailbox,omitempty" jsonschema:"mailbox name, default INBOX"`
	From       string `json:"from,omitempty" jsonschema:"filter: sender contains this text"`
	To         string `json:"to,omitempty" jsonschema:"filter: recipient contains this text (useful for the Sent mailbox)"`
	Subject    string `json:"subject,omitempty" jsonschema:"filter: subject contains this text"`
	Since      string `json:"since,omitempty" jsonschema:"filter: on or after this date, YYYY-MM-DD"`
	Before     string `json:"before,omitempty" jsonschema:"filter: before this date, YYYY-MM-DD"`
	UnreadOnly bool   `json:"unread_only,omitempty" jsonschema:"filter: only unread messages"`
	Limit      int    `json:"limit,omitempty" jsonschema:"max results (1-50, default 10)"`
}

// MarkRequest is the mark_email tool payload. Mailbox is required — UIDs are
// per-mailbox, so it must name the mailbox the UID came from (not defaulted).
type MarkRequest struct {
	Mailbox string `json:"mailbox" jsonschema:"REQUIRED mailbox the UID belongs to (UIDs are per-mailbox); pass the same mailbox you searched/listed"`
	UID     uint32 `json:"uid" jsonschema:"message UID (from list_emails or search_emails)"`
	Read    bool   `json:"read" jsonschema:"true = mark read, false = mark unread"`
}

// MoveRequest is the move_email tool payload. Mailbox (source) is required —
// UIDs are per-mailbox, so it must name the mailbox the UID came from.
type MoveRequest struct {
	Mailbox string `json:"mailbox" jsonschema:"REQUIRED source mailbox the UID belongs to (UIDs are per-mailbox); pass the same mailbox you searched/listed"`
	UID     uint32 `json:"uid" jsonschema:"message UID (from list_emails or search_emails)"`
	To      string `json:"to" jsonschema:"destination mailbox (use list_mailboxes to see names; move to the Trash/Deleted folder to delete)"`
}

// MailSearcher searches one mailbox. Hexagonal port: real adapter uses IMAP
// SEARCH, tests a fake.
type MailSearcher interface {
	Search(mailbox string, q SearchQuery, limit int) ([]MailHeader, error)
}

// MailFlagger sets/clears the \Seen flag on a message.
type MailFlagger interface {
	Mark(mailbox string, uid uint32, read bool) error
}

// MailMover moves a message between mailboxes (IMAP MOVE).
type MailMover interface {
	Move(mailbox string, uid uint32, to string) error
}

// MailboxLister lists selectable mailboxes.
type MailboxLister interface {
	Mailboxes() ([]string, error)
}

// SearchEmails validates the request and runs a filtered search. Requires at
// least one filter — use list_emails for an unfiltered dump of the newest
// messages.
func SearchEmails(s MailSearcher, req SearchRequest) ([]MailHeader, error) {
	q := SearchQuery{
		From:       strings.TrimSpace(req.From),
		To:         strings.TrimSpace(req.To),
		Subject:    strings.TrimSpace(req.Subject),
		UnreadOnly: req.UnreadOnly,
	}
	if req.Since != "" {
		t, err := time.Parse(dateLayout, req.Since)
		if err != nil {
			return nil, fmt.Errorf("invalid since date %q (want YYYY-MM-DD): %w", req.Since, err)
		}
		q.Since = t
	}
	if req.Before != "" {
		t, err := time.Parse(dateLayout, req.Before)
		if err != nil {
			return nil, fmt.Errorf("invalid before date %q (want YYYY-MM-DD): %w", req.Before, err)
		}
		q.Before = t
	}
	if q.From == "" && q.To == "" && q.Subject == "" && q.Since.IsZero() && q.Before.IsZero() && !q.UnreadOnly {
		return nil, fmt.Errorf("at least one filter is required (from, to, subject, since, before, or unread_only)")
	}

	mailbox := strings.TrimSpace(req.Mailbox)
	if mailbox == "" {
		mailbox = defaultMailbox
	}
	limit := req.Limit
	if limit < 1 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	return s.Search(mailbox, q, limit)
}

// MarkEmail validates the request and sets/clears the read flag. mailbox is
// REQUIRED: IMAP UIDs are per-mailbox, so defaulting to INBOX would act on a
// different message than the one the UID was retrieved from.
func MarkEmail(f MailFlagger, req MarkRequest) error {
	if req.UID == 0 {
		return fmt.Errorf("uid is required")
	}
	mailbox := strings.TrimSpace(req.Mailbox)
	if mailbox == "" {
		return fmt.Errorf("mailbox is required")
	}
	return f.Mark(mailbox, req.UID, req.Read)
}

// MoveEmail validates the request and moves a message to another mailbox.
// mailbox (source) is REQUIRED: IMAP UIDs are per-mailbox, so defaulting to
// INBOX would move a different message — moving the wrong one to Trash is
// silent data loss.
func MoveEmail(m MailMover, req MoveRequest) error {
	if req.UID == 0 {
		return fmt.Errorf("uid is required")
	}
	to := strings.TrimSpace(req.To)
	if to == "" {
		return fmt.Errorf("to (destination mailbox) is required")
	}
	mailbox := strings.TrimSpace(req.Mailbox)
	if mailbox == "" {
		return fmt.Errorf("mailbox is required")
	}
	return m.Move(mailbox, req.UID, to)
}
