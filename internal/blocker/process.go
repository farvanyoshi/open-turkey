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
// DESKTOP APP SCOPES
// ------------------
// Desktop environments run each app launched from a menu or dock in its own
// systemd unit, named per the XDG spec "app-[<launcher>-]<ID>-<random>.scope"
// or "app-[<launcher>-]<ID>[@<random>].service" (e.g.
// "app-gnome-net.lutris.Lutris-4242.scope"). A blocked name matches every
// process in such a unit whose app ID equals it or ends in ".<name>", so
// blocking "lutris" covers "net.lutris.Lutris" and everything it started —
// even processes that were reparented away from it. Flatpak scopes have the
// same shape, so "discord" also covers "com.discordapp.Discord".
//
// CHILD PROCESSES
// ---------------
// Killing only the matched process isn't enough: a launcher's children (a
// game started from Lutris or Steam, say) would keep running. So every
// descendant of a matched process is killed too. The process tree is
// snapshotted before anything is killed, because a killed parent's children
// are immediately reparented and the link to it is lost. When the blocked
// process itself is a shell, session or other infrastructure process
// (noDescend), its children are spared, so blocking e.g. "bash" doesn't take
// down everything started from a terminal.
//
// SIGNAL & SAFETY
// ---------------
// We send SIGKILL (can't be caught or ignored) so a blocked app can't refuse
// to die. We never touch PID 1 (init/systemd), our own process or any of our
// ancestors (so running the CLI from inside a blocked app doesn't kill the CLI
// mid-way; the daemon catches that app on its next pass), and we ignore
// processes that vanish between listing and killing (a normal race).
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

// noDescend lists executables whose children are NOT killed when they are
// themselves blocked: shells, terminal multiplexers and session/system
// infrastructure, whose "children" are really unrelated programs. It only
// applies to a directly-matched process; a shell running inside a blocked app
// (a terminal's tab, say) is killed along with its children.
var noDescend = map[string]bool{
	"systemd": true, "init": true, "sudo": true, "su": true, "doas": true,
	"pkexec": true, "login": true, "sshd": true, "dbus-daemon": true,
	"dbus-broker": true, "gdm": true, "sddm": true, "lightdm": true,
	"gnome-shell": true, "gnome-session-b": true, "plasmashell": true,
	"kwin_wayland": true, "kwin_x11": true, "ksmserver": true, "Xorg": true,
	"Xwayland": true, "bash": true, "zsh": true, "fish": true, "sh": true,
	"dash": true, "tmux": true, "tmux: server": true, "screen": true,
}

// KillBlocked SIGKILLs every process that matches one of processNames (by
// executable name, Flatpak app ID or desktop app scope) together with all of
// its descendants. It returns the number of processes killed.
func KillBlocked(processNames []string) (int, error) {
	if len(processNames) == 0 {
		return 0, nil
	}

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, err
	}

	flatpakIDs := flatpakAppIDs(processNames)

	// Snapshot the tree and find the directly-matched processes.
	parent := map[int]int{}
	children := map[int][]int{}
	var roots []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue // not a PID directory
		}
		ppid, ok := readPPID(pid)
		if !ok {
			continue // already gone
		}
		parent[pid] = ppid
		children[ppid] = append(children[ppid], pid)

		if matchesBlockedProcess(pid, processNames) ||
			matchesProcessScope(pid, processNames, flatpakIDs) {
			roots = append(roots, pid)
		}
	}

	// Never kill init, ourselves or our ancestors.
	protected := map[int]bool{1: true}
	for pid := os.Getpid(); pid > 0 && !protected[pid]; pid = parent[pid] {
		protected[pid] = true
	}

	// Collect roots and their descendants, parents before children.
	var follow []int
	for _, pid := range roots {
		if !noDescend[readComm(pid)] {
			follow = append(follow, pid)
		}
	}
	targets := append(roots, descendants(follow, children)...)

	killed := 0
	for _, pid := range targets {
		if protected[pid] {
			continue
		}
		protected[pid] = true // a root may also be another root's descendant
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
			// Process already exited between listing and killing — fine.
			continue
		}
		killed++
	}

	return killed, nil
}

// descendants returns every descendant of roots in children, breadth-first
// (parents before children), each pid once. roots themselves are not included.
func descendants(roots []int, children map[int][]int) []int {
	seen := map[int]bool{}
	for _, pid := range roots {
		seen[pid] = true
	}
	var out []int
	queue := append([]int(nil), roots...)
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		for _, child := range children[pid] {
			if !seen[child] {
				seen[child] = true
				out = append(out, child)
				queue = append(queue, child)
			}
		}
	}
	return out
}

// readPPID returns the parent PID of pid from /proc/<pid>/stat.
func readPPID(pid int) (int, bool) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, false
	}
	return parsePPID(string(data))
}

// parsePPID extracts the PPID from /proc/<pid>/stat content: "pid (comm) state
// ppid ...". comm may itself contain spaces and parentheses, so fields are
// counted from the LAST ')'.
func parsePPID(stat string) (int, bool) {
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return 0, false
	}
	fields := strings.Fields(stat[i+1:])
	if len(fields) < 2 {
		return 0, false
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, false
	}
	return ppid, true
}

// readComm returns /proc/<pid>/comm, or "" if it can't be read.
func readComm(pid int) string {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
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

// matchesProcessScope reports whether the process at pid runs in a desktop app
// scope (see appScopeIDs) matching one of names, or in a Flatpak whose app ID
// is one of flatpakIDs (case-insensitive).
func matchesProcessScope(pid int, names, flatpakIDs []string) bool {
	pidPath := "/proc/" + strconv.Itoa(pid)

	cg, err := os.ReadFile(pidPath + "/cgroup")
	if err == nil && matchesAppID(appScopeIDs(string(cg)), names) {
		return true
	}
	if len(flatpakIDs) == 0 {
		return false
	}

	id := ""
	if err == nil {
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
	for _, target := range flatpakIDs {
		if strings.EqualFold(id, target) {
			return true
		}
	}
	return false
}

// appScopeIDs returns the candidate app IDs of the desktop app units in
// /proc/<pid>/cgroup content. Units are named
// "app-[<launcher>-]<ID>-<random>.scope" or "app-[<launcher>-]<ID>[@<random>].service";
// since the launcher part is optional, both "<launcher>-<ID>" and "<ID>" are
// returned, with systemd's \xNN escapes decoded.
func appScopeIDs(content string) []string {
	var ids []string
	for _, line := range strings.Split(content, "\n") {
		// Each line is "hierarchy-ID:controllers:path".
		i := strings.LastIndexByte(line, ':')
		if i < 0 {
			continue
		}
		for _, elem := range strings.Split(line[i+1:], "/") {
			if !strings.HasPrefix(elem, "app-") {
				continue
			}
			mid := ""
			switch {
			case strings.HasSuffix(elem, ".scope"):
				mid = strings.TrimSuffix(elem[len("app-"):], ".scope")
				dash := strings.LastIndexByte(mid, '-')
				if dash <= 0 {
					continue
				}
				mid = mid[:dash] // drop "-<random>"
			case strings.HasSuffix(elem, ".service"):
				mid = strings.TrimSuffix(elem[len("app-"):], ".service")
				if at := strings.IndexByte(mid, '@'); at >= 0 {
					mid = mid[:at]
				}
			default:
				continue
			}
			if mid == "" {
				continue
			}
			ids = append(ids, unescapeUnit(mid))
			if dash := strings.IndexByte(mid, '-'); dash > 0 && dash < len(mid)-1 {
				ids = append(ids, unescapeUnit(mid[dash+1:]))
			}
		}
	}
	return ids
}

// matchesAppID reports whether one of ids equals one of names, or ends in
// "." followed by one (case-insensitive): "net.lutris.Lutris" matches "lutris".
func matchesAppID(ids, names []string) bool {
	for _, id := range ids {
		last := id[strings.LastIndexByte(id, '.')+1:]
		for _, name := range names {
			if strings.EqualFold(id, name) || strings.EqualFold(last, name) {
				return true
			}
		}
	}
	return false
}

// unescapeUnit decodes systemd unit-name escapes ("\x2d" → "-").
func unescapeUnit(s string) string {
	if !strings.Contains(s, `\x`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) && s[i+1] == 'x' {
			if v, err := strconv.ParseUint(s[i+2:i+4], 16, 8); err == nil {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
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
