// daemon.go — Command to start the Open Turkey daemon.
//
// WHAT IS A DAEMON?
// -----------------
// A daemon is a program that runs in the background on Linux, without direct
// interaction with the user. Think of it as a "watchman" that stays awake all
// the time, even when nobody is looking.
//
// Well-known examples of daemons on Linux:
//   - sshd: the SSH daemon (allows remote connections)
//   - nginx: the web server daemon
//   - systemd: the "manager" of every other daemon
//
// WHY DOES OPEN TURKEY NEED A DAEMON?
// -----------------------------------
// Without a daemon, the block would be easy to get around. The user could:
//   - Manually edit /etc/hosts to remove our blocks
//   - Flush the iptables rules with "iptables -F"
//   - Delete the browser policy files
//   - Simply reboot the computer to wipe everything
//
// The daemon continuously monitors all these points and reapplies the blocks
// if it detects that someone (or some system update) removed them.
// It's like a security guard doing rounds 24/7 and relocking any door someone
// tries to open.
//
// HOW DO I RUN IT?
// ----------------
// This command is normally NOT called directly by the user.
// It's run by systemd (the Linux service manager) through a unit file in
// /etc/systemd/system/.
//
// Manual usage example (for debugging):
//   sudo open-turkey daemon
//
// Usage via systemd (the right way):
//   sudo systemctl start open-turkey
//   sudo systemctl enable open-turkey   # starts automatically at boot
package cli

import (
	"github.com/brunodcdo/open-turkey/internal/daemon"
	"github.com/spf13/cobra"
)

// daemonCmd starts the Open Turkey daemon.
//
// The daemon runs in an infinite loop, monitoring and reapplying the blocks.
// It only stops when it receives a shutdown signal (SIGTERM/SIGINT)
// or when the process is killed externally.
//
// All the daemon logic lives in the "internal/daemon" package, keeping this
// file lean. This command's role is just to act as a "bridge" between Cobra
// (the CLI framework) and the daemon package.
var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Start the Open Turkey daemon (use via systemd)",
	RunE: func(cmd *cobra.Command, args []string) error {
		// daemon.Run() starts the daemon's main loop.
		// This call is BLOCKING — it only returns when the daemon shuts down
		// (by signal or fatal error). While the daemon is running, execution
		// stays "stuck" in here.
		return daemon.Run()
	},
}

// init() registers the daemon command as a child of rootCmd.
//
// After this registration, the user can run:
//   open-turkey daemon
//
// Go calls init() automatically when the package is loaded.
// Since every file in the "cli" package is loaded together,
// each file can have its own init() and all of them will run.
func init() {
	rootCmd.AddCommand(daemonCmd)
}
