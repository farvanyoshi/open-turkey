// Package daemon implements the main loop of Open Turkey's background service.
//
// The daemon runs as a systemd service and has a simple but crucial job:
// making sure every blocking layer is always correctly applied.
//
// Every 5 seconds, it checks whether someone (or some program) removed or
// changed the blocking rules — for example, by manually editing /etc/hosts or
// flushing the iptables rules. If it detects any change, it reapplies
// everything.
//
// In addition, the daemon also kills blocked application processes on every
// cycle, preventing the user from simply opening the app while the block is
// active.
//
// The daemon handles SIGTERM and SIGINT to shut down cleanly (graceful
// shutdown), without leaving resources hanging.
package daemon

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/brunodcdo/open-turkey/internal/blocker"
	"github.com/brunodcdo/open-turkey/internal/db"
)

// Run is the daemon's main function. It starts the enforcement loop, which
// runs indefinitely until it receives a shutdown signal.
//
// Overall flow:
//  1. Open the database at the default path
//  2. Set up signal handling (SIGTERM and SIGINT)
//  3. Create a ticker that fires every 5 seconds
//  4. Run an enforcement cycle immediately on startup
//  5. Enter the main loop, alternating between ticks and signals
func Run() error {
	// Open the database where the active blocks are stored.
	// The default path is defined by the db package (usually under /var/lib/open-turkey/).
	database, err := db.OpenDB(db.DefaultDBPath)
	if err != nil {
		return err
	}
	defer database.Close()

	// Set up a channel to receive operating system signals.
	// SIGTERM is sent by systemd when stopping the service.
	// SIGINT is sent when the user presses Ctrl+C (e.g. in manual testing).
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)

	// The ticker fires every 5 seconds. This is the "heart" of the daemon:
	// it ensures enforcement happens periodically, without long pauses that
	// would let the user get around the block.
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	log.Println("daemon: starting Open Turkey enforcement loop")

	// Run one cycle immediately on startup, without waiting for the first
	// 5 seconds. This ensures that when the daemon starts, the rules are
	// applied right away.
	if err := enforce(database); err != nil {
		// We don't stop the daemon because of an error in the cycle.
		// We just log it and move on — resilience is the priority.
		log.Printf("daemon: error in initial enforcement cycle: %v", err)
	}

	// Main daemon loop.
	// We use select to wait for two kinds of event:
	//   - a ticker tick (time to enforce again)
	//   - an OS signal (time to shut down)
	for {
		select {
		case <-ticker.C:
			// Every 5 seconds, run a full enforcement cycle.
			// If something fails, we log the error but keep running.
			if err := enforce(database); err != nil {
				log.Printf("daemon: error in enforcement cycle: %v", err)
			}

		case sig := <-sigChan:
			// We received a shutdown signal. Log it and exit cleanly.
			log.Printf("daemon: signal received (%v), shutting down gracefully...", sig)
			return nil
		}
	}
}

// enforce runs a single enforcement cycle.
//
// This function is the core of the daemon. It checks whether each blocking
// layer is correctly applied and, if not, reapplies it.
//
// The layers checked are:
//   - /etc/hosts: blocks domains by resolving them to 0.0.0.0
//   - Firewall (iptables): blocks network connections to the domains
//   - Browser policies: prevents access via Chrome/Firefox policies
//   - Processes: kills blocked applications that are running
//
// Important: if one layer fails, the others are still checked.
// This ensures maximum resilience — an iptables error shouldn't prevent the
// /etc/hosts block, for example.
func enforce(database *db.DB) error {
	// Fetch every active block from the database.
	activeBlocks, err := database.GetActiveBlocks()
	if err != nil {
		return err
	}

	// If there are no active blocks, make sure every layer is removed (clean
	// state). This prevents orphaned rules from lingering after a block ends.
	if len(activeBlocks) == 0 {
		// Remove each layer individually to guarantee a clean state.
		blocker.RemoveHosts()
		blocker.RemoveFirewall()
		blocker.RemoveBrowserPolicies()
		return nil
	}

	// Collect every domain and application from every active block.
	// Several blocks may be active at the same time (e.g. "social media"
	// and "games"), so we need to merge the lists.
	var domains []string
	var apps []string

	for _, block := range activeBlocks {
		domains = append(domains, block.Sites...)
		apps = append(apps, block.Apps...)
	}

	// --- Layer 1: /etc/hosts ---
	// Check whether the domains are already mapped to 0.0.0.0 in /etc/hosts.
	// If someone manually edited the file to remove the entries, reapply.
	if !blocker.IsHostsApplied(domains) {
		log.Println("daemon: /etc/hosts out of date, reapplying blocks")
		if err := blocker.ApplyHosts(domains); err != nil {
			// Log the error but continue to the next layers.
			log.Printf("daemon: error applying /etc/hosts: %v", err)
		}
	}

	// --- Layer 2: Firewall (iptables) ---
	// Check whether the firewall rules are present.
	// If someone ran "iptables -F" to flush the rules, reapply.
	if !blocker.IsFirewallApplied() {
		log.Println("daemon: firewall rules missing, reapplying")
		if err := blocker.ApplyFirewall(domains); err != nil {
			log.Printf("daemon: error applying firewall: %v", err)
		}
	}

	// --- Layer 3: Browser policies ---
	// Check whether the Chrome/Firefox policies are configured.
	// These policies block access even if the user uses an alternative DNS.
	if !blocker.IsBrowserPoliciesApplied(domains) {
		log.Println("daemon: browser policies out of date, reapplying")
		if err := blocker.ApplyBrowserPolicies(domains); err != nil {
			log.Printf("daemon: error applying browser policies: %v", err)
		}
	}

	// --- Layer 4: Kill blocked processes ---
	// Look for processes of blocked applications and terminate them.
	// This prevents using apps like games or social media on the desktop.
	if len(apps) > 0 {
		killed, err := blocker.KillBlocked(apps)
		if err != nil {
			log.Printf("daemon: error killing blocked processes: %v", err)
		}
		if killed > 0 {
			log.Printf("daemon: %d blocked processes terminated", killed)
		}
	}

	return nil
}
