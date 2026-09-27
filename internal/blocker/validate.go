// This file holds domain normalization and validation shared by every
// blocking layer.
//
// WHY VALIDATION MATTERS FOR SECURITY
// -----------------------------------
// The blocking layers write domains into sensitive places: lines in
// /etc/hosts, arguments to iptables, and entries in browser policy JSON. If a
// "domain" were allowed to contain a newline or a space, a crafted entry like
//
//	evil.com\n1.2.3.4 bank.com
//
// would inject an extra "1.2.3.4 bank.com" line into /etc/hosts, redirecting
// bank.com for every program on the machine. To make that impossible, every
// domain is validated against a strict hostname grammar before it is ever
// written anywhere, both when the user adds it and again defensively at write
// time.
package blocker

import (
	"net/url"
	"sort"
	"strings"
)

// maxDomainLength is the maximum length of a DNS name (RFC 1035).
const maxDomainLength = 253

// NormalizeDomain reduces a user-supplied string to a bare, lowercase host.
//
// It strips a scheme, a path, a port and a trailing dot, and lowercases the
// result. It does NOT guarantee the result is a valid domain — call
// IsValidDomain (or ValidDomains) for that. Keeping normalization and
// validation separate keeps both easy to test.
func NormalizeDomain(input string) string {
	s := strings.TrimSpace(strings.ToLower(input))
	if s == "" {
		return ""
	}

	// If it's a full URL, extract the host.
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err == nil && u != nil && u.Host != "" {
			s = u.Host
		}
	}

	// Remove any path (e.g. "example.com/foo").
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}

	// Remove userinfo (e.g. "user@example.com").
	if i := strings.LastIndexByte(s, '@'); i >= 0 {
		s = s[i+1:]
	}

	// Remove port (e.g. "example.com:443"), but leave IPv6 literals in
	// brackets alone (they contain colons legitimately).
	if i := strings.LastIndexByte(s, ':'); i >= 0 && !strings.Contains(s, "]") {
		s = s[:i]
	}

	s = strings.TrimSpace(strings.TrimSuffix(s, "."))
	return s
}

// IsValidDomain reports whether s is a syntactically valid, already-normalized
// domain: one or more labels separated by dots, each label 1-63 characters of
// ASCII letters/digits/hyphens (not starting or ending with a hyphen), and a
// letters-only top-level label of at least two characters.
//
// Crucially, it rejects anything containing whitespace, control characters or
// other bytes that could break out of a hosts line, an iptables argument or a
// JSON string. Wildcards ("*") are rejected here on purpose — keyword/wildcard
// patterns take a separate, browser-only path (see keyword handling) so they
// can never reach /etc/hosts or iptables.
func IsValidDomain(s string) bool {
	if len(s) == 0 || len(s) > maxDomainLength {
		return false
	}

	labels := strings.Split(s, ".")
	if len(labels) < 2 {
		// A bare TLD like "localhost" is not a blockable public domain.
		return false
	}

	for i, label := range labels {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}

		lastLabel := i == len(labels)-1
		// The top-level label must be at least two characters (e.g. "co",
		// "com") — no single-letter TLDs.
		if lastLabel && len(label) < 2 {
			return false
		}
		letterSeen := false
		for j := 0; j < len(label); j++ {
			c := label[j]
			switch {
			case c >= 'a' && c <= 'z':
				letterSeen = true
			case c >= '0' && c <= '9':
				// digits allowed in any label
			case c == '-':
				// hyphen allowed in the interior (checked above)
			default:
				// any other byte (space, newline, slash, '*', etc.) is invalid
				return false
			}
		}

		// The top-level label must be all letters (e.g. "com", "org", "co").
		if lastLabel && !letterSeen {
			return false
		}
	}

	return true
}

// ValidDomains normalizes, validates, deduplicates and sorts a list of
// user-supplied domains, discarding anything invalid. Every layer funnels its
// input through this so behavior is identical everywhere and no invalid string
// can reach the system.
func ValidDomains(domains []string) []string {
	set := make(map[string]struct{}, len(domains))
	for _, d := range domains {
		host := NormalizeDomain(d)
		if IsValidDomain(host) {
			set[host] = struct{}{}
		}
	}

	out := make([]string, 0, len(set))
	for d := range set {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}
