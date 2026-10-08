package cli

import (
	"testing"

	"github.com/brunodcdo/open-turkey/internal/db"
)

func TestSiteEditStrengthens(t *testing.T) {
	blocklist := &db.BlockDetail{Mode: db.ModeBlocklist, Sites: []string{"a.com", "b.com"}}
	allowlist := &db.BlockDetail{Mode: db.ModeAllowlist, Sites: []string{"a.com", "b.com"}}
	allowWithApp := &db.BlockDetail{Mode: db.ModeAllowlist, Sites: []string{"a.com"}, Apps: []string{"steam"}}

	cases := []struct {
		name    string
		detail  *db.BlockDetail
		domains []string
		adding  bool
		want    bool
	}{
		{"add to blocklist", blocklist, []string{"c.com"}, true, true},
		{"remove from blocklist", blocklist, []string{"a.com"}, false, false},
		{"add to allowlist", allowlist, []string{"c.com"}, true, false},
		{"remove one from allowlist", allowlist, []string{"a.com"}, false, true},
		{"empty the allowlist", allowlist, []string{"a.com", "b.com"}, false, false},
		{"empty sites, apps remain", allowWithApp, []string{"a.com"}, false, true},
	}
	for _, c := range cases {
		if got := siteEditStrengthens(c.detail, c.domains, c.adding); got != c.want {
			t.Errorf("%s: got %v; want %v", c.name, got, c.want)
		}
	}
}
