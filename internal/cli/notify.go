// notify.go — best-effort desktop notifications.
//
// The CLI runs as root (via the sudo wrapper), so reaching the logged-in user's
// notification daemon takes a little work: we drop back to the invoking user
// (SUDO_USER) and point at their session bus. Everything here is best-effort —
// any failure is silently ignored and never affects blocking. If notify-send
// isn't installed or no graphical session is found, notifications just don't
// appear.
package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
)

// notify tries to show a desktop notification. It never returns an error and
// never blocks the caller meaningfully (the send is quick and failures are
// swallowed).
func notify(title, body string) {
	path, err := exec.LookPath("notify-send")
	if err != nil {
		return // not installed; nothing to do
	}

	// Determine the target user. When run via sudo, SUDO_USER is the human.
	user := os.Getenv("SUDO_USER")
	if user == "" || user == "root" {
		// Not run through sudo (or already the user): try a direct send.
		_ = exec.Command(path, title, body).Start()
		return
	}

	uid := os.Getenv("SUDO_UID")
	if uid == "" {
		if u := lookupUID(user); u != "" {
			uid = u
		}
	}
	if uid == "" {
		return
	}

	// Run notify-send as the user, pointing at their session bus.
	cmd := exec.Command("sudo", "-u", user, "notify-send", title, body)
	cmd.Env = append(os.Environ(),
		"DISPLAY=:0",
		fmt.Sprintf("DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/%s/bus", uid),
	)
	_ = cmd.Start()
}

// lookupUID returns the numeric uid for a username by reading /etc/passwd
// (avoids importing os/user, which needs cgo in some builds).
func lookupUID(user string) string {
	data, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return ""
	}
	for _, line := range splitLines(string(data)) {
		fields := splitColon(line)
		if len(fields) >= 3 && fields[0] == user {
			if _, err := strconv.Atoi(fields[2]); err == nil {
				return fields[2]
			}
		}
	}
	return ""
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func splitColon(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}
