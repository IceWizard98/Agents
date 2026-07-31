package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message/mail"
)

const (
	defaultMailbox = "INBOX"
	defaultLimit   = 10
	maxLimit       = 50
)

// MailReader reads mail. Hexagonal port: real adapter uses IMAP, tests a fake.
type MailReader interface {
	List(mailbox string, limit int) ([]MailHeader, error)
	Read(mailbox string, uid uint32) (MailMessage, error)
}

// MailHeader is one row of the list_emails result (no body).
type MailHeader struct {
	UID     uint32 `json:"uid"`
	From    string `json:"from"`
	To      string `json:"to"`
	Subject string `json:"subject"`
	Date    string `json:"date"`
}

// MailMessage is a full message returned by read_email.
type MailMessage struct {
	UID     uint32 `json:"uid"`
	From    string `json:"from"`
	To      string `json:"to"`
	Subject string `json:"subject"`
	Date    string `json:"date"`
	Body    string `json:"body"`
}

// ListRequest is the list_emails tool payload. Mailbox/Limit are optional —
// omitempty keeps them out of the generated JSON schema's "required" list
// (jsonschema-go marks every non-omitempty field required; without this, a
// caller that omits an optional field gets rejected before the tool runs).
type ListRequest struct {
	Mailbox string `json:"mailbox,omitempty" jsonschema:"mailbox name, default INBOX"`
	Limit   int    `json:"limit,omitempty" jsonschema:"max messages to return (1-50, default 10)"`
}

// ReadRequest is the read_email tool payload. UID has no sensible default
// (0 is invalid) so it stays required; Mailbox is optional.
type ReadRequest struct {
	Mailbox string `json:"mailbox,omitempty" jsonschema:"mailbox name, default INBOX"`
	UID     uint32 `json:"uid" jsonschema:"message UID (from list_emails)"`
}

// ListEmails validates the request and reads the most recent headers.
func ListEmails(r MailReader, req ListRequest) ([]MailHeader, error) {
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
	return r.List(mailbox, limit)
}

// ReadEmail validates the request and reads one full message by UID.
func ReadEmail(r MailReader, req ReadRequest) (MailMessage, error) {
	if req.UID == 0 {
		return MailMessage{}, fmt.Errorf("uid is required")
	}
	mailbox := strings.TrimSpace(req.Mailbox)
	if mailbox == "" {
		mailbox = defaultMailbox
	}
	return r.Read(mailbox, req.UID)
}

// imapReader is the real MailReader: iCloud IMAP (imap.mail.me.com:993, TLS).
// Stateless: one connection per call. ponytail: fine for chat-driven low volume;
// pool connections if call rate ever climbs.
type imapReader struct {
	addr, user, pass string // addr = host:port
}

func (r imapReader) dial() (*imapclient.Client, error) {
	var opts *imapclient.Options
	if os.Getenv("ICLOUD_IMAP_DEBUG") != "" {
		opts = &imapclient.Options{DebugWriter: os.Stderr}
	}
	c, err := imapclient.DialTLS(r.addr, opts)
	if err != nil {
		return nil, fmt.Errorf("imap dial: %w", err)
	}
	if err := c.Login(r.user, r.pass).Wait(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("imap login: %w", err)
	}
	return c, nil
}

func (r imapReader) List(mailbox string, limit int) ([]MailHeader, error) {
	c, err := r.dial()
	if err != nil {
		return nil, err
	}
	defer func() { c.Logout().Wait() }()

	mbox, err := c.Select(mailbox, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("select %q: %w", mailbox, err)
	}
	if mbox.NumMessages == 0 {
		return nil, nil
	}

	// Newest `limit` messages = highest sequence numbers.
	from := uint32(1)
	if mbox.NumMessages > uint32(limit) {
		from = mbox.NumMessages - uint32(limit) + 1
	}
	seq := imap.SeqSet{}
	seq.AddRange(from, mbox.NumMessages)

	msgs, err := c.Fetch(seq, &imap.FetchOptions{UID: true, Envelope: true}).Collect()
	if err != nil {
		return nil, fmt.Errorf("fetch headers: %w", err)
	}

	out := make([]MailHeader, 0, len(msgs))
	for _, m := range msgs {
		h := MailHeader{UID: uint32(m.UID)}
		if m.Envelope != nil {
			h.Subject = m.Envelope.Subject
			h.Date = m.Envelope.Date.Format(time.RFC3339)
			h.From = addrString(m.Envelope.From)
			h.To = addrString(m.Envelope.To)
		}
		out = append(out, h)
	}
	// Newest first.
	sort.Slice(out, func(i, j int) bool { return out[i].UID > out[j].UID })
	return out, nil
}

func (r imapReader) Read(mailbox string, uid uint32) (MailMessage, error) {
	c, err := r.dial()
	if err != nil {
		return MailMessage{}, err
	}
	defer func() { c.Logout().Wait() }()

	if _, err := c.Select(mailbox, nil).Wait(); err != nil {
		return MailMessage{}, fmt.Errorf("select %q: %w", mailbox, err)
	}

	uidSet := imap.UIDSet{}
	uidSet.AddNum(imap.UID(uid))
	section := &imap.FetchItemBodySection{}
	opts := &imap.FetchOptions{
		UID:         true,
		Envelope:    true,
		BodySection: []*imap.FetchItemBodySection{section},
	}
	msgs, err := c.Fetch(uidSet, opts).Collect()
	if err != nil {
		return MailMessage{}, fmt.Errorf("fetch message: %w", err)
	}
	if len(msgs) == 0 {
		return MailMessage{}, fmt.Errorf("no message with uid %d in %q", uid, mailbox)
	}
	m := msgs[0]

	msg := MailMessage{UID: uint32(m.UID)}
	if m.Envelope != nil {
		msg.Subject = m.Envelope.Subject
		msg.Date = m.Envelope.Date.Format(time.RFC3339)
		msg.From = addrString(m.Envelope.From)
		msg.To = addrString(m.Envelope.To)
	}
	msg.Body = extractText(m.FindBodySection(section))
	return msg, nil
}

func (r imapReader) Search(mailbox string, q SearchQuery, limit int) ([]MailHeader, error) {
	c, err := r.dial()
	if err != nil {
		return nil, err
	}
	defer func() { c.Logout().Wait() }()

	if _, err := c.Select(mailbox, nil).Wait(); err != nil {
		return nil, fmt.Errorf("select %q: %w", mailbox, err)
	}

	criteria := &imap.SearchCriteria{}
	if q.From != "" {
		criteria.Header = append(criteria.Header, imap.SearchCriteriaHeaderField{Key: "From", Value: q.From})
	}
	if q.Subject != "" {
		criteria.Header = append(criteria.Header, imap.SearchCriteriaHeaderField{Key: "Subject", Value: q.Subject})
	}
	if q.To != "" {
		criteria.Header = append(criteria.Header, imap.SearchCriteriaHeaderField{Key: "To", Value: q.To})
	}
	if !q.Since.IsZero() {
		criteria.Since = q.Since
	}
	if !q.Before.IsZero() {
		criteria.Before = q.Before
	}
	if q.UnreadOnly {
		criteria.NotFlag = append(criteria.NotFlag, imap.FlagSeen)
	}

	data, err := c.UIDSearch(criteria, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	uids := data.AllUIDs()
	if len(uids) == 0 {
		return nil, nil
	}

	// Newest first, capped to limit (search has no server-side sort here).
	sort.Slice(uids, func(i, j int) bool { return uids[i] > uids[j] })
	if len(uids) > limit {
		uids = uids[:limit]
	}

	uidSet := imap.UIDSet{}
	uidSet.AddNum(uids...)
	msgs, err := c.Fetch(uidSet, &imap.FetchOptions{UID: true, Envelope: true}).Collect()
	if err != nil {
		return nil, fmt.Errorf("fetch matched headers: %w", err)
	}

	out := make([]MailHeader, 0, len(msgs))
	for _, m := range msgs {
		h := MailHeader{UID: uint32(m.UID)}
		if m.Envelope != nil {
			h.Subject = m.Envelope.Subject
			h.Date = m.Envelope.Date.Format(time.RFC3339)
			h.From = addrString(m.Envelope.From)
			h.To = addrString(m.Envelope.To)
		}
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UID > out[j].UID })
	return out, nil
}

func (r imapReader) Mark(mailbox string, uid uint32, read bool) error {
	c, err := r.dial()
	if err != nil {
		return err
	}
	defer func() { c.Logout().Wait() }()

	if _, err := c.Select(mailbox, nil).Wait(); err != nil {
		return fmt.Errorf("select %q: %w", mailbox, err)
	}

	op := imap.StoreFlagsDel
	if read {
		op = imap.StoreFlagsAdd
	}
	uidSet := imap.UIDSet{}
	uidSet.AddNum(imap.UID(uid))
	store := &imap.StoreFlags{Op: op, Silent: true, Flags: []imap.Flag{imap.FlagSeen}}
	if err := c.Store(uidSet, store, nil).Close(); err != nil {
		return fmt.Errorf("store flags: %w", err)
	}
	return nil
}

func (r imapReader) Move(mailbox string, uid uint32, to string) error {
	c, err := r.dial()
	if err != nil {
		return err
	}
	defer func() { c.Logout().Wait() }()

	if _, err := c.Select(mailbox, nil).Wait(); err != nil {
		return fmt.Errorf("select %q: %w", mailbox, err)
	}

	uidSet := imap.UIDSet{}
	uidSet.AddNum(imap.UID(uid))
	if _, err := c.Move(uidSet, to).Wait(); err != nil {
		return fmt.Errorf("move to %q: %w", to, err)
	}
	return nil
}

func (r imapReader) Mailboxes() ([]string, error) {
	c, err := r.dial()
	if err != nil {
		return nil, err
	}
	defer func() { c.Logout().Wait() }()

	items, err := c.List("", "*", nil).Collect()
	if err != nil {
		return nil, fmt.Errorf("list mailboxes: %w", err)
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		selectable := true
		for _, a := range it.Attrs {
			if a == imap.MailboxAttrNoSelect {
				selectable = false
				break
			}
		}
		if selectable {
			out = append(out, it.Mailbox)
		}
	}
	sort.Strings(out)
	return out, nil
}

// addrString renders the first address as name <mailbox@host> (or just the addr).
func addrString(as []imap.Address) string {
	if len(as) == 0 {
		return ""
	}
	a := as[0]
	email := a.Mailbox + "@" + a.Host
	if a.Name != "" {
		return fmt.Sprintf("%s <%s>", a.Name, email)
	}
	return email
}

// extractText pulls the text/plain part from a raw RFC822 message. Falls back to
// the raw bytes if the message isn't MIME-parseable.
func extractText(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	mr, err := mail.CreateReader(strings.NewReader(string(raw)))
	if err != nil {
		return string(raw)
	}
	var b strings.Builder
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		if _, ok := p.Header.(*mail.InlineHeader); ok {
			data, _ := io.ReadAll(p.Body)
			b.Write(data)
		}
	}
	if b.Len() == 0 {
		return string(raw)
	}
	return b.String()
}
