package main

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// maxResponseBytes caps how much of any upstream response body we read into
// memory, so a huge (or hostile) page can't OOM the process. Comfortably above
// any max_chars in bytes; max_chars still truncates the returned content.
const maxResponseBytes = 10 << 20 // 10 MB

// blockedHosts are names that must never be fetched: they name us or the cloud
// metadata endpoint by literal hostname (no dot / not caught by the IP checks).
var blockedHosts = map[string]bool{
	"localhost":                true,
	"metadata.google.internal": true,
}

// validatePublicURL rejects URLs whose host points at a loopback / private /
// link-local / unspecified address, at localhost, or at a bare internal service
// name (e.g. "supermemory"). It blunts SSRF: a prompt-injected agent pointing a
// fetch at internal Coolify services (http://supermemory:6767) or cloud metadata
// (http://169.254.169.254). Fails closed: an unresolvable host is rejected.
//
// ponytail: this is an app-layer check; DNS-rebind can still bypass it (resolve
// public here, private on the real fetch inside FlareSolverr). The real fix is an
// egress network policy on the flaresolverr container — treat this as
// defense-in-depth, not the whole story.
func validatePublicURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse url %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("url scheme must be http or https: %q", raw)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("url has no host: %q", raw)
	}
	if blockedHosts[strings.ToLower(host)] {
		return fmt.Errorf("refusing to fetch internal host %q", host)
	}
	// IP literal: check directly, no DNS.
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedIP(ip) {
			return fmt.Errorf("refusing to fetch non-public address %q", host)
		}
		return nil
	}
	// A bare hostname with no dot is an internal service name, never public.
	if !strings.Contains(host, ".") {
		return fmt.Errorf("refusing to fetch non-public host %q", host)
	}
	// Resolve and reject if ANY address is non-public. Fail closed on lookup error.
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("resolve host %q: %w", host, err)
	}
	for _, ip := range ips {
		if isBlockedIP(ip) {
			return fmt.Errorf("host %q resolves to non-public address %s", host, ip)
		}
	}
	return nil
}

// isBlockedIP reports whether ip is in a range we refuse to fetch. IsPrivate
// covers RFC1918 (10/8, 172.16/12, 192.168/16) and ULA (fc00::/7);
// IsLinkLocalUnicast covers 169.254.0.0/16 (incl. cloud metadata) and fe80::/10.
func isBlockedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}
