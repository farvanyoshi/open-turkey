package blocker

import (
	"reflect"
	"testing"
)

// TestNormalizeDomain covers normalizing varied inputs into the canonical host
// that feeds blocking-pattern generation. These are pure-logic cases — no I/O
// or root — that apply to every browser, including Brave, since they all
// derive from the same patterns.
func TestNormalizeDomain(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple domain", "facebook.com", "facebook.com"},
		{"uppercase becomes lowercase", "Facebook.COM", "facebook.com"},
		{"surrounding spaces", "  facebook.com  ", "facebook.com"},
		{"full url extracts the host", "https://www.facebook.com/feed", "www.facebook.com"},
		{"path without scheme is discarded", "example.com/foo/bar", "example.com"},
		{"port is removed", "example.com:443", "example.com"},
		{"url with port extracts host without the port", "https://example.com:8080/x", "example.com"},
		{"trailing dot is removed", "example.com.", "example.com"},
		{"empty string", "", ""},
		{"only spaces", "   ", ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NormalizeDomain(c.input)
			if got != c.expected {
				t.Errorf("NormalizeDomain(%q) = %q; expected %q", c.input, got, c.expected)
			}
		})
	}
}

// TestFirefoxPatterns validates the *://.../* match patterns for Firefox's
// WebsiteFilter: the two patterns per domain (root and subdomain wildcard),
// collapsing the "www." prefix, deduplication and sorted output.
func TestFirefoxPatterns(t *testing.T) {
	cases := []struct {
		name     string
		domains  []string
		expected []string
	}{
		{
			name:     "root domain generates both patterns",
			domains:  []string{"facebook.com"},
			expected: []string{"*://*.facebook.com/*", "*://facebook.com/*"},
		},
		{
			name:     "www prefix collapses into wildcard without www",
			domains:  []string{"www.facebook.com"},
			expected: []string{"*://*.facebook.com/*", "*://www.facebook.com/*"},
		},
		{
			name:     "root and www share and deduplicate the wildcard",
			domains:  []string{"facebook.com", "www.facebook.com"},
			expected: []string{"*://*.facebook.com/*", "*://facebook.com/*", "*://www.facebook.com/*"},
		},
		{
			name:     "repeated domain does not duplicate patterns",
			domains:  []string{"facebook.com", "facebook.com"},
			expected: []string{"*://*.facebook.com/*", "*://facebook.com/*"},
		},
		{
			name:     "www on single-label domain does not become a TLD wildcard",
			domains:  []string{"www.com"},
			expected: []string{"*://*.www.com/*", "*://www.com/*"},
		},
		{
			name:     "empty entries are ignored",
			domains:  []string{"", "   "},
			expected: []string{},
		},
		{
			name:     "no domains results in empty list",
			domains:  nil,
			expected: []string{},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := firefoxPatterns(c.domains)
			if !reflect.DeepEqual(got, c.expected) {
				t.Errorf("firefoxPatterns(%v) = %v; expected %v", c.domains, got, c.expected)
			}
		})
	}
}

// TestChromiumFilters validates the URLBlocklist filters (Chromium/Chrome/
// Brave): one bare filter per domain, "www." prefix removed, deduplication and
// sorting. The "uol.com.br" case is the regression test for the bug that let
// Folha (folha.uol.com.br, a subdomain) through — the bare "uol.com.br" filter
// covers every subdomain in the browser, no wildcard needed.
func TestChromiumFilters(t *testing.T) {
	cases := []struct {
		name     string
		domains  []string
		expected []string
	}{
		{
			name:     "root domain becomes bare filter",
			domains:  []string{"facebook.com"},
			expected: []string{"facebook.com"},
		},
		{
			name:     "www prefix is removed (bare covers subdomains)",
			domains:  []string{"www.facebook.com"},
			expected: []string{"facebook.com"},
		},
		{
			name:     "root and www deduplicate into the same bare filter",
			domains:  []string{"facebook.com", "www.facebook.com"},
			expected: []string{"facebook.com"},
		},
		{
			name:     "folha regression: blocking uol.com.br covers the subdomain",
			domains:  []string{"uol.com.br"},
			expected: []string{"uol.com.br"},
		},
		{
			name:     "full url is normalized to the bare host",
			domains:  []string{"https://www.uol.com.br/folha"},
			expected: []string{"uol.com.br"},
		},
		{
			name:     "www on single-label domain does not become the whole TLD",
			domains:  []string{"www.com"},
			expected: []string{"www.com"},
		},
		{
			name:     "multiple domains come out sorted",
			domains:  []string{"reddit.com", "globo.com"},
			expected: []string{"globo.com", "reddit.com"},
		},
		{
			name:     "empty entries are ignored",
			domains:  []string{"", "   "},
			expected: []string{},
		},
		{
			name:     "no domains results in empty list",
			domains:  nil,
			expected: []string{},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := chromiumFilters(c.domains)
			if !reflect.DeepEqual(got, c.expected) {
				t.Errorf("chromiumFilters(%v) = %v; expected %v", c.domains, got, c.expected)
			}
		})
	}
}
