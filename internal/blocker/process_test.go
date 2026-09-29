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
