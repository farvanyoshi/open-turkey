// =============================================================================
// Package blocker — Open Turkey's process termination module
// =============================================================================
//
// This file implements the "process killer" — the part of Open Turkey that
// closes blocked programs (like browsers or distracting apps).
// Let's see how this works on Linux:
//
// WHAT IS A PROCESS?
// ------------------
// Every program running on your computer is a "process". When you open
// Firefox, Linux creates a process for it. Each process gets a unique number
// called a PID (Process ID), which is like an ID card — no other active
// process has the same number.
//
// WHAT IS /proc?
// --------------
// Linux has a virtual file system called /proc. It doesn't really exist on
// disk — the kernel (the core of the system) generates this information in
// real time. Each active process has a /proc/[PID]/ folder with various
// information about it. It's as if each process had a "record card" that the
// kernel keeps up to date.
//
// Two files interest us:
//
//   /proc/[PID]/comm    → The process's short name (15 characters max).
//                         Example: "firefox", "chrome", "slack"
//
//   /proc/[PID]/cmdline → The full command line used to start the process.
//                         Example: "/usr/bin/firefox --new-window".
//                         Arguments are separated by null bytes (\0), not by
//                         spaces — that's a Linux quirk.
//
// HOW DO WE "KILL" A PROCESS?
// ---------------------------
// On Linux, processes communicate through "signals". They're like special
// messages that the kernel delivers. There are several types:
//
//   SIGTERM (signal 15) → "Please shut down politely." The process can catch
//                         this signal, save data and exit calmly.
//
//   SIGKILL (signal 9)  → "Die NOW." The process can NOT catch, ignore or
//                         block this signal. The kernel kills it on the spot,
//                         with no chance to do anything. It's the "emergency
//                         button".
//
// We use SIGKILL because we're dealing with programs the user wants BLOCKED.
// It makes no sense to politely ask Firefox to close — the user might have a
// tab open on the blocked site and the browser could simply ignore the request.
//
// WHY DO WE NEED ROOT?
// --------------------
// A normal user can only kill processes that belong to them. But since Open
// Turkey runs as root (it needs that to edit /etc/hosts and iptables), it also
// has permission to kill any process on the system.
//
// SAFETY PRECAUTIONS:
// -------------------
// - We never kill PID 1 (init/systemd) — it's the "parent" of all processes.
//   Killing it would bring down the whole system.
// - We never kill our own process — that would be suicide, the block would stop.
// - Processes can disappear at any time (the user closed them, or the process
//   ended on its own). This causes "race conditions" — we try to read the
//   record of a process that no longer exists. We simply ignore those errors
//   and move on.
// =============================================================================
package blocker

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

// KillBlocked walks every active process on the system and terminates (kills)
// those whose names match the given list.
//
// Parameters:
//   - processNames: list of program names to kill.
//     Examples: ["firefox", "chrome", "slack", "discord"]
//
// Returns:
//   - int:   number of processes that were actually killed.
//   - error: error if the /proc directory can't be read (if that fails,
//            something is very wrong with the system).
//
// Usage example:
//
//	killed, err := blocker.KillBlocked([]string{"firefox", "chrome"})
//	fmt.Printf("Processes terminated: %d\n", killed)
func KillBlocked(processNames []string) (int, error) {
	// If the process list is empty, there's nothing to do.
	// Return 0 killed and no error.
	if len(processNames) == 0 {
		return 0, nil
	}

	// Read the contents of the /proc directory. Each entry can be:
	// - A number (e.g. "1234") → it's a process folder (PID)
	// - A name (e.g. "cpuinfo", "meminfo") → it's a system file, ignore it
	entries, err := os.ReadDir("/proc")
	if err != nil {
		// If we can't read /proc, something is very wrong.
		// The system may not have /proc mounted (very rare on Linux).
		return 0, err
	}

	// Get our own PID so we don't accidentally kill ourselves.
	// os.Getpid() returns the PID of the program running RIGHT NOW (Open Turkey).
	selfPID := os.Getpid()

	// Counter of how many processes we killed successfully.
	killed := 0

	// Walk each /proc entry looking for PIDs.
	for _, entry := range entries {
		// Try converting the entry name to a number.
		// If it works, it's a PID. If not, it's a system file and we ignore it.
		// Example: "1234" becomes the number 1234, but "cpuinfo" would fail.
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			// Not a number → not a PID → skip to the next one.
			continue
		}

		// SAFETY: We never kill PID 1 (init/systemd).
		// PID 1 is the first process the kernel starts. It's the "grandparent"
		// of every other process. Killing it would cause a kernel panic or
		// shut down the system.
		if pid == 1 {
			continue
		}

		// SAFETY: We never kill our own process.
		// If we killed ourselves, Open Turkey would stop working and every
		// block that depends on continuous monitoring would fail.
		if pid == selfPID {
			continue
		}

		// Check whether this process matches any name in the block list.
		// We use two detection strategies (explained below).
		if matchesBlockedProcess(pid, processNames) {
			// Found a blocked process! Send SIGKILL (signal 9).
			//
			// syscall.Kill() asks the kernel to send a signal to a process.
			// SIGKILL is the most aggressive signal — it kills the process
			// instantly and it can NOT intercept or ignore that signal.
			err := syscall.Kill(pid, syscall.SIGKILL)
			if err != nil {
				// If killing failed, the process probably already died on its
				// own between the time we read /proc and the time we tried to
				// kill it. That's a "race condition" — a normal situation on
				// systems with many processes. We simply ignore it and continue.
				continue
			}
			// Process killed successfully! Increment the counter.
			killed++
		}
	}

	// Return how many processes we killed. We don't return an error here
	// because individual errors from processes that disappeared are normal
	// and were already handled above.
	return killed, nil
}

// matchesBlockedProcess checks whether a process (identified by PID) has a
// name that matches any of the names in the block list.
//
// We use TWO detection strategies to maximize the chance of catching the
// right process:
//
//  1. /proc/[pid]/comm — The process's short name (fast and simple).
//     Works well for programs with short names like "firefox" or "chrome".
//     Limitation: 15 characters max. "firefox-esr" shows up as "firefox-esr",
//     but very long names would be truncated.
//
//  2. /proc/[pid]/cmdline — The full command line (more detailed).
//     Contains the full path and every argument. Useful when the comm name
//     doesn't match, but the full executable does.
//     Example: comm may be "python3", but cmdline reveals
//     "/usr/bin/python3 /opt/slack/slack".
func matchesBlockedProcess(pid int, processNames []string) bool {
	// Build the base path for this PID: "/proc/1234"
	pidPath := "/proc/" + strconv.Itoa(pid)

	// --- STRATEGY 1: Check /proc/[pid]/comm ---
	//
	// The comm file contains only the executable's name, with no path and no
	// arguments. It's the fastest way to identify a process.
	// Example content: "firefox\n" (it comes with a trailing newline)
	commBytes, err := os.ReadFile(pidPath + "/comm")
	if err == nil {
		// strings.TrimSpace removes spaces and newlines from the start and end.
		// "firefox\n" becomes "firefox".
		comm := strings.TrimSpace(string(commBytes))

		// Compare with each name in the list using EqualFold, which ignores
		// case. So "Firefox", "FIREFOX" and "firefox" are all recognized.
		for _, name := range processNames {
			if strings.EqualFold(comm, name) {
				return true
			}
		}
	}
	// If reading comm failed, the process may have disappeared.
	// Not a problem — we try the next strategy.

	// --- STRATEGY 2: Check /proc/[pid]/cmdline ---
	//
	// The cmdline file contains the full command line used to start the
	// process. Arguments are separated by null bytes (\0), not by spaces.
	// That's a Linux kernel convention.
	//
	// Example (representing \0 as |):
	//   /usr/bin/firefox|--new-window|https://google.com
	//
	// Each piece between the \0 bytes is an "argument" of the command.
	cmdlineBytes, err := os.ReadFile(pidPath + "/cmdline")
	if err != nil {
		// If we can't read cmdline, the process disappeared.
		// We can neither confirm nor deny — return false (no match).
		return false
	}

	// Convert the bytes to a string and split on the null bytes (\0).
	// Result: ["usr/bin/firefox", "--new-window", "https://google.com"]
	cmdline := string(cmdlineBytes)
	args := strings.Split(cmdline, "\x00")

	// Check each command-line argument against each name in the list.
	// We use Contains instead of EqualFold (equals) because the full path may
	// be "/usr/bin/firefox" and we want to find "firefox" inside it.
	//
	// We convert everything to lowercase so the comparison is case-insensitive.
	for _, arg := range args {
		// Empty arguments may appear (especially the last one, after the
		// trailing \0). We skip them to avoid false positives.
		if arg == "" {
			continue
		}
		argLower := strings.ToLower(arg)
		for _, name := range processNames {
			if strings.Contains(argLower, strings.ToLower(name)) {
				return true
			}
		}
	}

	// Neither strategy found a match — this process isn't on the list.
	return false
}
