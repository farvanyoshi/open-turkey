package blocker

import "testing"

func TestMatchesName(t *testing.T) {
	names := []string{"discord", "telegram-desktop", "firefox"}

	shouldMatch := []string{
		"discord",         // exact
		"Discord",         // case-insensitive
		"FIREFOX",         // case-insensitive
		"telegram-deskto", // 15-char comm truncation of "telegram-desktop"
	}
	for _, n := range shouldMatch {
		if !matchesName(n, names) {
			t.Errorf("matchesName(%q) = false; want true", n)
		}
	}

	shouldNotMatch := []string{
		"",                // empty
		"discord-updater", // not an exact match (was over-matched before)
		"qrencode",        // contains "code"-like fragments but no exact name
		"telegram",        // prefix but not a truncation match
		"notfirefox",      // substring, must not match
	}
	for _, n := range shouldNotMatch {
		if matchesName(n, names) {
			t.Errorf("matchesName(%q) = true; want false", n)
		}
	}
}

// TestMatchesNameNoSubstringOverreach guards the specific regression: blocking
// "code" must not match unrelated executables that merely contain "code".
func TestMatchesNameNoSubstringOverreach(t *testing.T) {
	names := []string{"code"}
	for _, n := range []string{"vscode", "qrencode", "geocode-helper", "code-server"} {
		if matchesName(n, names) {
			t.Errorf("matchesName(%q, [code]) = true; want false (no substring overreach)", n)
		}
	}
	if !matchesName("code", names) {
		t.Error("matchesName(\"code\", [code]) = false; want true")
	}
}

func TestBaseName(t *testing.T) {
	cases := map[string]string{
		"/usr/bin/firefox": "firefox",
		"firefox":          "firefox",
		"/opt/Slack/slack": "slack",
		"":                 "",
		"/trailing/":       "",
	}
	for in, want := range cases {
		if got := baseName(in); got != want {
			t.Errorf("baseName(%q) = %q; want %q", in, got, want)
		}
	}
}
