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
	// Per RFC 2045 a MIME part without an explicit Content-Type defaults to
	// text/plain. The fallback to application/octet-stream in
	// extractAttachments only triggers when ContentType() returns an error or
	// an empty string; here we only assert the part is still parsed and a
	// (non-empty) content type is reported.
	raw := buildMIME("BOUND3",
		"Content-Disposition: attachment; filename=\"blob.bin\"\r\n\r\n"+
			"binarydata\r\n",
	)
	atts := extractAttachments(raw)
	if len(atts) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(atts))
	}
	if atts[0].ContentType == "" {
		t.Fatalf("expected a non-empty content_type, got empty")
	}
	if atts[0].Filename != "blob.bin" {
		t.Fatalf("filename = %q, want blob.bin", atts[0].Filename)
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

func TestSanitizeFilename_EmptyStaysEmpty(t *testing.T) {
	if got := sanitizeFilename(""); got != "" {
		t.Fatalf("sanitizeFilename(\"\") = %q, want empty", got)
	}
}
