package main

import (
	"strings"
	"testing"
)

// buildMIME assembles a minimal multipart/mixed RFC5322 message with CRLF
// line endings (required by go-message/mail for reliable parsing). parts
// is the pre-built body of each MIME part (headers + blank line + content).
func buildMIME(boundary string, parts ...string) []byte {
	var b strings.Builder
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: multipart/mixed; boundary=\"" + boundary + "\"\r\n")
	b.WriteString("\r\n")
	for _, p := range parts {
		b.WriteString("--" + boundary + "\r\n")
		b.WriteString(p)
		b.WriteString("\r\n")
	}
	b.WriteString("--" + boundary + "--\r\n")
	return []byte(b.String())
}

func TestExtractAttachments_EmptyOnTextOnlyMessage(t *testing.T) {
	raw := []byte("Content-Type: text/plain\r\n\r\nhello world, no MIME structure at all")
	if atts := extractAttachments(raw); len(atts) != 0 {
		t.Fatalf("expected no attachments, got %d: %+v", len(atts), atts)
	}
}

func TestExtractAttachments_EmptyOnMultipartWithNoAttachment(t *testing.T) {
	raw := buildMIME("BOUND1",
		"Content-Type: text/plain\r\n\r\nJust an inline body, no attachment.\r\n",
	)
	atts := extractAttachments(raw)
	if len(atts) != 0 {
		t.Fatalf("expected no attachments, got %d: %+v", len(atts), atts)
	}
}

func TestExtractAttachments_ParsesAttachmentPart(t *testing.T) {
	raw := buildMIME("BOUND2",
		"Content-Type: text/plain\r\n\r\nSee attached.\r\n",
		"Content-Type: text/plain; name=\"hello.txt\"\r\n"+
			"Content-Disposition: attachment; filename=\"hello.txt\"\r\n\r\n"+
			"hello\r\n",
	)
	atts := extractAttachments(raw)
	if len(atts) != 1 {
		t.Fatalf("expected 1 attachment, got %d: %+v", len(atts), atts)
	}
	a := atts[0]
	if a.Filename != "hello.txt" {
		t.Fatalf("filename = %q, want hello.txt", a.Filename)
	}
	if a.Skipped {
		t.Fatalf("small attachment should not be skipped: %+v", a)
	}
	if a.Data == "" {
		t.Fatalf("expected base64 data, got empty")
	}
	if a.Size <= 0 {
		t.Fatalf("size = %d, want > 0", a.Size)
	}
}

func TestExtractAttachments_MissingContentTypeDefaults(t *testing.T) {
	// A part with no Content-Type header at all: RFC 2045 says text/plain, but
	// Content-Disposition: attachment already says "file, not body text", so an
	// undeclared attachment is reported as application/octet-stream.
	raw := buildMIME("BOUND3",
		"Content-Disposition: attachment; filename=\"blob.bin\"\r\n\r\n"+
			"binarydata\r\n",
	)
	atts := extractAttachments(raw)
	if len(atts) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(atts))
	}
	if atts[0].ContentType != "application/octet-stream" {
		t.Fatalf("content_type = %q, want application/octet-stream", atts[0].ContentType)
	}
	if atts[0].Filename != "blob.bin" {
		t.Fatalf("filename = %q, want blob.bin", atts[0].Filename)
	}
}

// go-message has no charset decoders registered for legacy charsets unless the
// charset package is imported, and it reports that as an error alongside a
// perfectly usable part. Treating it as fatal made every iso-8859-1 mail look
// like it had no attachments.
func TestExtractAttachments_UnknownCharsetBodyStillYieldsAttachment(t *testing.T) {
	raw := buildMIME("BOUND6",
		"Content-Type: text/plain; charset=iso-8859-1\r\n\r\nCiao pero\xf2.\r\n",
		"Content-Type: application/pdf\r\n"+
			"Content-Disposition: attachment; filename=\"a.pdf\"\r\n\r\n"+
			"pdfbytes\r\n",
	)
	atts := extractAttachments(raw)
	if len(atts) != 1 {
		t.Fatalf("expected 1 attachment past the iso-8859-1 body part, got %d: %+v", len(atts), atts)
	}
	if atts[0].Filename != "a.pdf" {
		t.Fatalf("filename = %q, want a.pdf", atts[0].Filename)
	}
}

func TestExtractAttachments_UnknownCharsetOnAttachmentPart(t *testing.T) {
	raw := buildMIME("BOUND7",
		"Content-Type: text/plain\r\n\r\nBody.\r\n",
		"Content-Type: text/csv; charset=windows-1252\r\n"+
			"Content-Disposition: attachment; filename=\"rows.csv\"\r\n\r\n"+
			"a;b\r\n",
	)
	atts := extractAttachments(raw)
	if len(atts) != 1 {
		t.Fatalf("expected 1 attachment, got %d: %+v", len(atts), atts)
	}
	if atts[0].Filename != "rows.csv" {
		t.Fatalf("filename = %q, want rows.csv", atts[0].Filename)
	}
}

// A charset label no decoder can resolve is what actually reaches the
// IsUnknownCharset branch — the registered legacy charsets never error.
func TestExtractAttachments_UnresolvableCharsetStillYieldsAttachment(t *testing.T) {
	raw := buildMIME("BOUND10",
		"Content-Type: text/plain; charset=\"x-nonexistent-42\"\r\n\r\nbody\r\n",
		"Content-Type: application/pdf\r\n"+
			"Content-Disposition: attachment; filename=\"a.pdf\"\r\n\r\n"+
			"pdfbytes\r\n",
	)
	atts := extractAttachments(raw)
	if len(atts) != 1 || atts[0].Filename != "a.pdf" {
		t.Fatalf("expected a.pdf past an unresolvable charset, got %+v", atts)
	}
}

// A malformed Content-Type must not make a healthy attachment look failed:
// reason is the "you did not get the bytes" channel.
func TestExtractAttachments_MalformedContentTypeKeepsCleanReason(t *testing.T) {
	raw := buildMIME("BOUND11",
		"Content-Type: application/pdf; name=\r\n\r\npdfbytes\r\n",
	)
	atts := extractAttachments(raw)
	if len(atts) != 1 {
		t.Fatalf("expected 1 attachment, got %d: %+v", len(atts), atts)
	}
	if atts[0].Reason != "" || atts[0].Skipped {
		t.Fatalf("healthy attachment must carry no failure reason: %+v", atts[0])
	}
	if atts[0].Data == "" {
		t.Fatalf("expected data for a readable attachment")
	}
}

func TestExtractAttachments_AttachmentWithoutFilename(t *testing.T) {
	raw := buildMIME("BOUND8",
		"Content-Type: application/pdf\r\n"+
			"Content-Disposition: attachment\r\n\r\n"+
			"pdfbytes\r\n",
	)
	atts := extractAttachments(raw)
	if len(atts) != 1 {
		t.Fatalf("expected 1 attachment, got %d: %+v", len(atts), atts)
	}
	if atts[0].Filename != "" {
		t.Fatalf("filename = %q, want empty", atts[0].Filename)
	}
	if atts[0].Data == "" {
		t.Fatalf("expected data for a nameless attachment")
	}
}

// An undecodable body must be reported, not dropped: a silently missing
// attachment is indistinguishable from an email that never had one.
func TestExtractAttachments_UnreadablePartIsReportedAsSkipped(t *testing.T) {
	raw := buildMIME("BOUND9",
		"Content-Type: application/pdf\r\n"+
			"Content-Transfer-Encoding: base64\r\n"+
			"Content-Disposition: attachment; filename=\"broken.pdf\"\r\n\r\n"+
			"!!!!not base64!!!!\r\n",
	)
	atts := extractAttachments(raw)
	if len(atts) != 1 {
		t.Fatalf("expected the unreadable part to be reported, got %d: %+v", len(atts), atts)
	}
	if !atts[0].Skipped {
		t.Fatalf("expected skipped=true for an unreadable part: %+v", atts[0])
	}
	if atts[0].Reason == "" {
		t.Fatalf("expected a reason for the skipped part")
	}
	if atts[0].Data != "" {
		t.Fatalf("expected no data for a skipped part")
	}
}

func TestExtractAttachments_MultipleAttachments(t *testing.T) {
	raw := buildMIME("BOUND4",
		"Content-Type: text/plain\r\n\r\nBody text.\r\n",
		"Content-Type: application/pdf\r\n"+
			"Content-Disposition: attachment; filename=\"a.pdf\"\r\n\r\n"+
			"pdfbytes\r\n",
		"Content-Type: image/png\r\n"+
			"Content-Disposition: attachment; filename=\"b.png\"\r\n\r\n"+
			"pngbytes\r\n",
	)
	atts := extractAttachments(raw)
	if len(atts) != 2 {
		t.Fatalf("expected 2 attachments, got %d: %+v", len(atts), atts)
	}
	names := map[string]bool{atts[0].Filename: true, atts[1].Filename: true}
	if !names["a.pdf"] || !names["b.png"] {
		t.Fatalf("unexpected filenames: %+v", atts)
	}
}

func TestExtractAttachments_MalformedMIMEReturnsNilNotPanic(t *testing.T) {
	raw := []byte("not a valid mime message at all \x00\x01\x02")
	atts := extractAttachments(raw)
	if atts != nil {
		t.Fatalf("expected nil for unparseable input, got %+v", atts)
	}
}

func TestExtractAttachments_EmptyInput(t *testing.T) {
	if atts := extractAttachments(nil); atts != nil {
		t.Fatalf("expected nil for empty input, got %+v", atts)
	}
	if atts := extractAttachments([]byte{}); atts != nil {
		t.Fatalf("expected nil for empty input, got %+v", atts)
	}
}

func TestBuildAttachment_SmallEncodesData(t *testing.T) {
	a := buildAttachment("f.txt", "text/plain", []byte("hello"))
	if a.Skipped {
		t.Fatalf("small attachment should not be skipped")
	}
	if a.Data == "" {
		t.Fatalf("expected data to be populated")
	}
	if a.Size != 5 {
		t.Fatalf("size = %d, want 5", a.Size)
	}
}

func TestBuildAttachment_SkipsOverSizeLimit(t *testing.T) {
	data := make([]byte, maxAttachmentSize+1)
	a := buildAttachment("big.bin", "application/octet-stream", data)
	if !a.Skipped {
		t.Fatalf("expected attachment over %d bytes to be skipped", maxAttachmentSize)
	}
	if a.Reason != "too large" {
		t.Fatalf("reason = %q, want %q", a.Reason, "too large")
	}
	if a.Data != "" {
		t.Fatalf("expected no data for a skipped attachment, got %d bytes of base64", len(a.Data))
	}
	if a.Size != maxAttachmentSize+1 {
		t.Fatalf("size = %d, want %d", a.Size, maxAttachmentSize+1)
	}
}

func TestBuildAttachment_ExactlyAtLimitIsNotSkipped(t *testing.T) {
	data := make([]byte, maxAttachmentSize)
	a := buildAttachment("edge.bin", "application/octet-stream", data)
	if a.Skipped {
		t.Fatalf("attachment exactly at the limit must not be skipped")
	}
	if a.Data == "" {
		t.Fatalf("expected data for an at-limit attachment")
	}
}

func TestSanitizeFilename_StripsPathTraversal(t *testing.T) {
	cases := []string{
		"../../etc/passwd",
		"a/b/c.txt",
		"..\\..\\windows",
		"normal.pdf",
		"",
	}
	for _, in := range cases {
		got := sanitizeFilename(in)
		if strings.Contains(got, "..") {
			t.Fatalf("sanitizeFilename(%q) = %q still contains ..", in, got)
		}
		if strings.ContainsAny(got, "/\\") {
			t.Fatalf("sanitizeFilename(%q) = %q still contains a path separator", in, got)
		}
	}
}

func TestSanitizeFilename_StripsControlCharacters(t *testing.T) {
	got := sanitizeFilename("bad\x00name\x1f.txt")
	if strings.ContainsAny(got, "\x00\x1f") {
		t.Fatalf("sanitizeFilename left control characters: %q", got)
	}
}

// Control characters must go before the ".." pass, otherwise ".\x00." loses the
// NUL after the check and reconstitutes the traversal sequence.
func TestSanitizeFilename_InterleavedControlCharsCannotReformTraversal(t *testing.T) {
	for _, in := range []string{".\x00.", "..\x01/etc", ".\x1f./.\x1f./passwd"} {
		if got := sanitizeFilename(in); strings.Contains(got, "..") {
			t.Errorf("sanitizeFilename(%q) = %q still contains ..", in, got)
		}
	}
}

func TestSanitizeFilename_EmptyStaysEmpty(t *testing.T) {
	if got := sanitizeFilename(""); got != "" {
		t.Fatalf("sanitizeFilename(\"\") = %q, want empty", got)
	}
}
