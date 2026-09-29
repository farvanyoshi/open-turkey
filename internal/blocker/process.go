// =============================================================================
// Package blocker — Open Turkey's process termination module
// =============================================================================
//
// This file kills blocked application processes. It runs as root (needed for
// /etc/hosts and iptables), so it can terminate any process — which makes
// precise matching essential.
//
// MATCHING — WHY EXACT, NOT SUBSTRING
// -----------------------------------
// An earlier version matched a blocked name as a SUBSTRING of any command-line
// argument. Running as root, that was dangerous: blocking "code" would also
// kill "vscode-helper", "qrencode", or any process with "code" anywhere in its
// arguments, including other users' and system processes.
//
// Now we match the process's EXECUTABLE NAME exactly (case-insensitive):
//   - /proc/<pid>/comm  — the kernel's short name (truncated to 15 chars), or
//   - the basename of argv[0] from /proc/<pid>/cmdline.
//
// So "discord" matches a process named "discord" or launched from
// "/usr/bin/discord", but not "discord-updater" or a browser tab whose URL
// merely contains "discord". A 15-char comm truncation is handled specially so
// long names like "telegram-desktop" still match.
//
// FLATPAK APP IDS
// ---------------
// A blocked name that looks like a Flatpak app ID (reverse-DNS with at least
// two dots, e.g. "com.discordapp.Discord") also matches every process of that
// Flatpak, whatever its executable is called. Flatpak runs each app in a
// systemd scope named "app-flatpak-<ID>-<instance>.scope", visible in
// /proc/<pid>/cgroup; when there is no such scope (no systemd user session) we
// fall back to the "name=" key of the sandbox's /.flatpak-info. This catches
// apps whose executable is a generic interpreter (python3, java, electron)
// without killing every other program with that name.
//
// SIGNAL & SAFETY
// ---------------
// We send SIGKILL (can't be caught or ignored) so a blocked app can't refuse
// to die. We never touch PID 1 (init/systemd) or our own process, and we
// ignore processes that vanish between listing and killing (a normal race).
// =============================================================================
package blocker

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

// commMaxLen is the kernel's fixed width for /proc/<pid>/comm (TASK_COMM_LEN-1).
const commMaxLen = 15

// KillBlocked walks every process and SIGKILLs those whose executable name
// matches one of processNames. It returns the number of processes killed.
func KillBlocked(processNames []string) (int, error) {
	if len(processNames) == 0 {
		return 0, nil
	}

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, err
	}

	selfPID := os.Getpid()
	flatpakIDs := flatpakAppIDs(processNames)
	killed := 0

	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue // not a PID directory
		}
		if pid == 1 || pid == selfPID {
			continue // never kill init or ourselves
		}

		if matchesBlockedProcess(pid, processNames) ||
			(len(flatpakIDs) > 0 && matchesFlatpakApp(pid, flatpakIDs)) {
			if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
				// Process already exited between listing and killing — fine.
				continue
			}
			killed++
		}
	}

	return killed, nil
}

// matchesBlockedProcess reports whether the process at pid has an executable
// name that exactly matches one of processNames (case-insensitive).
func matchesBlockedProcess(pid int, processNames []string) bool {
	pidPath := "/proc/" + strconv.Itoa(pid)

	// Strategy 1: /proc/<pid>/comm — the short executable name.
	if commBytes, err := os.ReadFile(pidPath + "/comm"); err == nil {
		comm := strings.TrimSpace(string(commBytes))
		if matchesName(comm, processNames) {
			return true
		}
	}

	// Strategy 2: basename of argv[0] from /proc/<pid>/cmdline.
	cmdlineBytes, err := os.ReadFile(pidPath + "/cmdline")
	if err != nil {
		return false
	}
	// argv entries are NUL-separated; argv[0] is the executable path.
	argv0 := cmdlineBytes
	if i := indexByte(cmdlineBytes, 0); i >= 0 {
		argv0 = cmdlineBytes[:i]
	}
	base := baseName(string(argv0))
	return matchesName(base, processNames)
}

// flatpakAppIDs returns the entries of names that look like Flatpak app IDs.
func flatpakAppIDs(names []string) []string {
	var ids []string
	for _, n := range names {
		if isFlatpakAppID(n) {
			ids = append(ids, n)
		}
	}
	return ids
}

// isFlatpakAppID reports whether s has the shape of a Flatpak app ID: at least
// three non-empty dot-separated elements (Flatpak's own minimum) and no path
// separators or whitespace.
func isFlatpakAppID(s string) bool {
	if strings.ContainsAny(s, "/ \t\n") {
		return false
	}
	parts := strings.Split(s, ".")
	if len(parts) < 3 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
	}
	return true
}

// matchesFlatpakApp reports whether the process at pid belongs to a Flatpak
// whose app ID is one of ids (case-insensitive).
func matchesFlatpakApp(pid int, ids []string) bool {
	pidPath := "/proc/" + strconv.Itoa(pid)

	id := ""
	if cg, err := os.ReadFile(pidPath + "/cgroup"); err == nil {
		id = flatpakAppFromCgroup(string(cg))
	}
	if id == "" {
		if info, err := os.ReadFile(pidPath + "/root/.flatpak-info"); err == nil {
			id = flatpakAppFromInfo(string(info))
		}
	}
	if id == "" {
		return false
	}
	for _, target := range ids {
		if strings.EqualFold(id, target) {
			return true
		}
	}
	return false
}

// flatpakAppFromCgroup extracts the app ID from /proc/<pid>/cgroup content,
// looking for a path element "app-flatpak-<ID>-<instance>.scope". It returns
// "" when the process isn't in a Flatpak scope.
func flatpakAppFromCgroup(content string) string {
	const prefix, suffix = "app-flatpak-", ".scope"
	for _, line := range strings.Split(content, "\n") {
		// Each line is "hierarchy-ID:controllers:path".
		i := strings.LastIndexByte(line, ':')
		if i < 0 {
			continue
		}
		for _, elem := range strings.Split(line[i+1:], "/") {
			if !strings.HasPrefix(elem, prefix) || !strings.HasSuffix(elem, suffix) {
				continue
			}
			mid := elem[len(prefix) : len(elem)-len(suffix)]
			// Strip the trailing "-<instance>" (all digits).
			dash := strings.LastIndexByte(mid, '-')
			if dash <= 0 || !allDigits(mid[dash+1:]) {
				continue
			}
			return mid[:dash]
		}
	}
	return ""
}

// flatpakAppFromInfo extracts the app ID ("name=" under [Application]) from a
// sandbox's /.flatpak-info keyfile. It returns "" if there is none.
func flatpakAppFromInfo(content string) string {
	inApplication := false
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			inApplication = line == "[Application]"
			continue
		}
		if inApplication && strings.HasPrefix(line, "name=") {
			return strings.TrimSpace(strings.TrimPrefix(line, "name="))
		}
	}
	return ""
}

// allDigits reports whether s is a non-empty string of ASCII digits.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// matchesName reports whether name equals any of names (case-insensitive). It
// also matches when name is a 15-character comm truncation of a longer target
// (e.g. comm "telegram-deskto" matches target "telegram-desktop").
func matchesName(name string, names []string) bool {
	if name == "" {
		return false
	}
	for _, target := range names {
		if strings.EqualFold(name, target) {
			return true
		}
		if len(name) == commMaxLen && len(target) > commMaxLen &&
			strings.EqualFold(name, target[:commMaxLen]) {
			return true
		}
	}
	return false
}

// baseName returns the final path element of p (like filepath.Base but without
// treating a trailing slash specially — process argv[0] rarely has one).
func baseName(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// indexByte returns the index of the first occurrence of b in s, or -1.
func indexByte(s []byte, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
