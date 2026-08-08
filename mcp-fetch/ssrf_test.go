package main

import (
	"context"
	"net/url"
	"testing"
)

// validatePublicURL must reject anything that points at us / the internal
// Coolify network / cloud metadata, and accept public IP literals. Cases that
// use IP literals or bare names need no DNS, keeping the test hermetic.
func TestValidatePublicURL(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"metadata ip", "http://169.254.169.254/latest/meta-data/", true},
		{"loopback ipv4", "http://127.0.0.1:6767/x", true},
		{"loopback ipv6", "http://[::1]/x", true},
		{"private 10", "http://10.0.0.5/x", true},
		{"private 192.168", "http://192.168.1.1/x", true},
		{"private 172.16", "http://172.16.0.1/x", true},
		{"ula ipv6", "http://[fd00::1]/x", true},
		{"unspecified", "http://0.0.0.0/x", true},
		{"localhost name", "http://localhost:8191/x", true},
		{"internal service name", "http://supermemory:6767/x", true},
		{"metadata hostname", "http://metadata.google.internal/x", true},
		{"non-http scheme", "file:///etc/passwd", true},
		{"empty host", "http:///path", true},
		{"public ip literal", "https://8.8.8.8/x", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validatePublicURL(c.url)
			if c.wantErr && err == nil {
				t.Errorf("validatePublicURL(%q) = nil, want error", c.url)
			}
			if !c.wantErr && err != nil {
				t.Errorf("validatePublicURL(%q) = %v, want nil", c.url, err)
			}
		})
	}
}

func TestFetch_BlocksInternalURL(t *testing.T) {
	fs := &fakeSolver{res: okResult("should not be reached")}
	_, err := Fetch(context.Background(), fs, FetchRequest{URL: "http://127.0.0.1:6767/admin"})
	if err == nil {
		t.Fatal("expected Fetch to reject an internal URL")
	}
	if fs.got.URL != "" {
		t.Errorf("solver must not be called for a blocked URL, got %q", fs.got.URL)
	}
}

func TestYouTubeID_MultiSegmentShortURL(t *testing.T) {
	// youtu.be/<id>/<extra> must yield only the id, not "id/extra".
	u := mustParse(t, "https://youtu.be/abc123XYZ01/foo?t=5")
	if got := youtubeID(u); got != "abc123XYZ01" {
		t.Errorf("youtubeID = %q, want %q", got, "abc123XYZ01")
	}
}

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u
}
