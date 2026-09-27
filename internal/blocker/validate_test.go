package blocker

import (
	"reflect"
	"testing"
)

func TestNormalizeDomain(t *testing.T) {
	cases := map[string]string{
		"facebook.com":                  "facebook.com",
		"Facebook.COM":                  "facebook.com",
		"  facebook.com  ":              "facebook.com",
		"https://www.facebook.com/feed": "www.facebook.com",
		"example.com/foo/bar":           "example.com",
		"example.com:443":               "example.com",
		"https://example.com:8080/x":    "example.com",
		"example.com.":                  "example.com",
		"user@example.com":              "example.com",
		"":                              "",
		"   ":                           "",
	}
	for in, want := range cases {
		if got := NormalizeDomain(in); got != want {
			t.Errorf("NormalizeDomain(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestIsValidDomain(t *testing.T) {
	valid := []string{
		"example.com",
		"a.b.co",
		"sub.domain.example.com",
		"xn--bcher-kva.com", // punycode
		"my-site.org",
		"uol.com.br",
	}
	for _, d := range valid {
		if !IsValidDomain(d) {
			t.Errorf("IsValidDomain(%q) = false; want true", d)
		}
	}

	invalid := []string{
		"",                           // empty
		"localhost",                  // single label
		"com",                        // TLD only
		"example.c",                  // TLD too short
		"example.123",                // numeric TLD
		"-bad.com",                   // leading hyphen
		"bad-.com",                   // trailing hyphen
		"exa mple.com",               // space
		"bad_domain.com",             // underscore
		"a..b.com",                   // empty label
		"*.example.com",              // wildcard
		"evil.com\n1.2.3.4 bank.com", // newline injection attempt
		"192.168.0.1",                // IPv4 literal (numeric TLD)
	}
	for _, d := range invalid {
		if IsValidDomain(d) {
			t.Errorf("IsValidDomain(%q) = true; want false", d)
		}
	}
}

func TestValidDomains(t *testing.T) {
	in := []string{
		"Facebook.com",
		"https://www.facebook.com/",
		"facebook.com", // duplicate of first once normalized
		"bad_domain.com",
		"evil.com\ninject",
		"  Reddit.COM  ",
	}
	want := []string{"facebook.com", "reddit.com", "www.facebook.com"}
	got := ValidDomains(in)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ValidDomains(%v) = %v; want %v", in, got, want)
	}
}

// TestValidDomainsRejectsInjection is the focused regression test for the
// /etc/hosts injection vector: a crafted "domain" with a newline must never
// survive validation.
func TestValidDomainsRejectsInjection(t *testing.T) {
	payload := "evil.com\n0.0.0.0 bank.example.com"
	if got := ValidDomains([]string{payload}); len(got) != 0 {
		t.Errorf("ValidDomains(%q) = %v; want empty (injection must be rejected)", payload, got)
	}
}
