package blocker

import (
	"fmt"
	"testing"
)

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

func TestIsFlatpakAppID(t *testing.T) {
	for _, s := range []string{"com.discordapp.Discord", "org.vinegarhq.Sober", "org.gnome.Gtk3-demo"} {
		if !isFlatpakAppID(s) {
			t.Errorf("isFlatpakAppID(%q) = false; want true", s)
		}
	}
	for _, s := range []string{"discord", "telegram-desktop", "python3.12", "a..b", ".a.b", "a.b.", "/usr/bin/a.b.c", "a.b c.d"} {
		if isFlatpakAppID(s) {
			t.Errorf("isFlatpakAppID(%q) = true; want false", s)
		}
	}
}

func TestFlatpakAppFromCgroup(t *testing.T) {
	cases := map[string]string{
		// cgroup v2, real layout under a systemd user session.
		"0::/user.slice/user-1000.slice/user@1000.service/app.slice/app-flatpak-org.vinegarhq.Sober-3261111766.scope\n": "org.vinegarhq.Sober",
		// A dash inside the ID must survive; only the numeric instance is stripped.
		"0::/user.slice/app.slice/app-flatpak-org.gnome.Gtk3-demo-42.scope\n": "org.gnome.Gtk3-demo",
		// cgroup v1 style, several lines.
		"12:pids:/user.slice\n1:name=systemd:/user.slice/app.slice/app-flatpak-com.discordapp.Discord-7.scope\n": "com.discordapp.Discord",
		// Not a Flatpak.
		"0::/user.slice/user-1000.slice/user@1000.service/app.slice/app-org.kde.konsole-15233.scope/tab(16862).scope\n": "",
		"0::/system.slice/open-turkey.service\n": "",
		// Malformed instance suffix.
		"0::/app.slice/app-flatpak-com.example.App-abc.scope\n": "",
		"": "",
	}
	for in, want := range cases {
		if got := flatpakAppFromCgroup(in); got != want {
			t.Errorf("flatpakAppFromCgroup(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestFlatpakAppFromInfo(t *testing.T) {
	info := "[Application]\nname=com.discordapp.Discord\nruntime=runtime/org.freedesktop.Platform/x86_64/24.08\n\n[Instance]\nname=ignored\n"
	if got := flatpakAppFromInfo(info); got != "com.discordapp.Discord" {
		t.Errorf("flatpakAppFromInfo = %q; want com.discordapp.Discord", got)
	}
	if got := flatpakAppFromInfo("[Instance]\nname=x.y.z\n"); got != "" {
		t.Errorf("flatpakAppFromInfo without [Application] = %q; want empty", got)
	}
}

func TestParsePPID(t *testing.T) {
	cases := map[string]int{
		"1234 (lutris) S 1000 1234 1234 0 -1 4194560":         1000,
		"77 (tmux: server) S 1 77 77 0 -1":                    1,
		"88 (weird) name)) R 42 88 88 0":                      42, // comm with ')' and spaces
		"99 (Hades2.exe) S 4321 99 99 0 -1 4194304 1234 0 0 ": 4321,
	}
	for in, want := range cases {
		if got, ok := parsePPID(in); !ok || got != want {
			t.Errorf("parsePPID(%q) = %d, %v; want %d", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "12 (x", "12 (x) S", "12 (x) S abc"} {
		if _, ok := parsePPID(bad); ok {
			t.Errorf("parsePPID(%q) ok; want failure", bad)
		}
	}
}

func TestDescendants(t *testing.T) {
	// 10 (lutris) → 11 (lutris-wrapper) → 12 (wine) → 13 (Hades2.exe)
	//            ↘ 14 (python3)
	// 20 (unrelated) → 21
	children := map[int][]int{10: {11, 14}, 11: {12}, 12: {13}, 20: {21}}
	got := descendants([]int{10}, children)
	want := []int{11, 14, 12, 13}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("descendants = %v; want %v", got, want)
	}
	// A root that is also another root's descendant isn't returned twice.
	if got := descendants([]int{10, 11}, children); fmt.Sprint(got) != "[14 12 13]" {
		t.Errorf("descendants with nested roots = %v", got)
	}
}

func TestAppScopeIDs(t *testing.T) {
	cases := map[string][]string{
		// GNOME: launcher prefix, numeric suffix.
		"0::/user.slice/user-1000.slice/user@1000.service/app.slice/app-gnome-net.lutris.Lutris-4242.scope\n": {"gnome-net.lutris.Lutris", "net.lutris.Lutris"},
		// Escaped dash in the ID.
		`0::/user.slice/app.slice/app-gnome-telegram\x2ddesktop-99.scope` + "\n": {"gnome-telegram-desktop", "telegram-desktop"},
		// KDE service form with @random, no launcher.
		"0::/user.slice/app.slice/app-org.kde.dolphin@a1b2c3.service\n": {"org.kde.dolphin"},
		// Nested scope: only the app- element counts.
		"0::/user.slice/app.slice/app-org.kde.konsole-15233.scope/tab(16862).scope\n": {"org.kde.konsole"},
		// Not an app unit.
		"0::/system.slice/open-turkey.service\n":                                    nil,
		"0::/user.slice/user@1000.service/app.slice/app-org.gnome.Terminal.slice\n": nil,
		"": nil,
	}
	for in, want := range cases {
		if got := appScopeIDs(in); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("appScopeIDs(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestMatchesAppID(t *testing.T) {
	ids := []string{"gnome-net.lutris.Lutris", "net.lutris.Lutris"}
	for _, n := range []string{"lutris", "LUTRIS", "net.lutris.Lutris"} {
		if !matchesAppID(ids, []string{n}) {
			t.Errorf("matchesAppID(%v, %q) = false; want true", ids, n)
		}
	}
	for _, n := range []string{"lutri", "net", "net.lutris", "gnome"} {
		if matchesAppID(ids, []string{n}) {
			t.Errorf("matchesAppID(%v, %q) = true; want false", ids, n)
		}
	}
	// The flatpak launcher form covers the full Flatpak app ID.
	if !matchesAppID(appScopeIDs("0::/app.slice/app-flatpak-com.discordapp.Discord-7.scope\n"), []string{"discord"}) {
		t.Error("blocking \"discord\" should match the Discord Flatpak's scope")
	}
}

func TestUnescapeUnit(t *testing.T) {
	cases := map[string]string{
		`telegram\x2ddesktop`: "telegram-desktop",
		`plain`:               "plain",
		`bad\xzz`:             `bad\xzz`,
		`end\x2`:              `end\x2`,
	}
	for in, want := range cases {
		if got := unescapeUnit(in); got != want {
			t.Errorf("unescapeUnit(%q) = %q; want %q", in, got, want)
		}
	}
}
