package blocker

import (
	"reflect"
	"testing"
)

func TestBrowserPolicyEmpty(t *testing.T) {
	if !(BrowserPolicy{}).Empty() {
		t.Error("zero BrowserPolicy should be Empty")
	}
	if (BrowserPolicy{BlockDomains: []string{"x.com"}}).Empty() {
		t.Error("policy with block domains should not be Empty")
	}
	if (BrowserPolicy{BlockPatterns: []string{"*ads*"}}).Empty() {
		t.Error("policy with patterns should not be Empty")
	}
	if (BrowserPolicy{AllowMode: true}).Empty() {
		t.Error("allowlist policy should not be Empty (it blocks everything)")
	}
	// Invalid-only inputs collapse to empty.
	if !(BrowserPolicy{BlockDomains: []string{"bad_domain"}}).Empty() {
		t.Error("policy with only invalid domains should be Empty")
	}
}

func TestBlocklistSets(t *testing.T) {
	p := BrowserPolicy{
		BlockDomains:  []string{"facebook.com", "www.reddit.com"},
		BlockPatterns: []string{"*casino*"},
	}
	cr := chromiumBlockSet(p)
	want := []string{"*casino*", "facebook.com", "reddit.com"}
	if !reflect.DeepEqual(cr, want) {
		t.Errorf("chromiumBlockSet = %v; want %v", cr, want)
	}
	if len(chromiumAllowSet(p)) != 0 {
		t.Error("blocklist mode should have no allow set")
	}

	ff := firefoxBlockSet(p)
	// Must contain the pattern and both facebook match-patterns.
	assertContains(t, ff, "*casino*")
	assertContains(t, ff, "*://facebook.com/*")
	assertContains(t, ff, "*://*.facebook.com/*")
	if len(firefoxExceptionSet(p)) != 0 {
		t.Error("blocklist mode should have no exceptions")
	}
}

func TestAllowlistSets(t *testing.T) {
	p := BrowserPolicy{
		AllowMode:     true,
		AllowDomains:  []string{"work.example.com"},
		BlockPatterns: []string{"*ads*"},
	}
	cr := chromiumBlockSet(p)
	assertContains(t, cr, "*")
	assertContains(t, cr, "*ads*")
	allow := chromiumAllowSet(p)
	if !reflect.DeepEqual(allow, []string{"work.example.com"}) {
		t.Errorf("chromiumAllowSet = %v; want [work.example.com]", allow)
	}

	ff := firefoxBlockSet(p)
	assertContains(t, ff, "*://*/*")
	exc := firefoxExceptionSet(p)
	assertContains(t, exc, "*://work.example.com/*")
}

func TestIsValidPattern(t *testing.T) {
	for _, p := range []string{"*casino*", "*://*/*ads*", "example.com/tracking"} {
		if !IsValidPattern(p) {
			t.Errorf("IsValidPattern(%q) = false; want true", p)
		}
	}
	for _, p := range []string{"", "has space", "tab\there", "new\nline"} {
		if IsValidPattern(p) {
			t.Errorf("IsValidPattern(%q) = true; want false", p)
		}
	}
}

func assertContains(t *testing.T, set []string, want string) {
	t.Helper()
	for _, s := range set {
		if s == want {
			return
		}
	}
	t.Errorf("expected %q in %v", want, set)
}
