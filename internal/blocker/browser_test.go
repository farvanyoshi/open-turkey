package blocker

import (
	"reflect"
	"testing"
)

// firefoxPatterns and chromiumFilters now take already-normalized, validated
// hosts (callers funnel input through ValidDomains first), so these tests use
// normalized inputs. Normalization/validation itself is covered in
// validate_test.go.

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
			name:     "www on single-label domain does not become a TLD wildcard",
			domains:  []string{"www.com"},
			expected: []string{"*://*.www.com/*", "*://www.com/*"},
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

// TestChromiumFilters validates the URLBlocklist filters (Chromium family): one
// bare filter per domain, "www." prefix removed, deduplication and sorting. The
// "uol.com.br" case is the regression for the bug that let Folha
// (folha.uol.com.br, a subdomain) through — the bare "uol.com.br" filter covers
// every subdomain in the browser, no wildcard needed.
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
			name:     "folha regression: uol.com.br as a bare filter covers subdomains",
			domains:  []string{"uol.com.br"},
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

// TestStripWWWPrefix checks that "www." is only stripped when a multi-label
// domain remains, so a single-label registrable domain like "www.com" is never
// reduced to the bare TLD.
func TestStripWWWPrefix(t *testing.T) {
	cases := map[string]string{
		"www.facebook.com": "facebook.com",
		"facebook.com":     "facebook.com",
		"www.com":          "www.com", // must NOT become "com"
		"www.co.uk":        "co.uk",
		"wwwsomething.com": "wwwsomething.com",
	}
	for in, want := range cases {
		if got := stripWWWPrefix(in); got != want {
			t.Errorf("stripWWWPrefix(%q) = %q; want %q", in, got, want)
		}
	}
}
