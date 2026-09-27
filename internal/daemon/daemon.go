// Package daemon implements the main loop of Open Turkey's background service.
//
// The daemon runs as a systemd service and has one crucial job: making sure
// every blocking layer always matches what the active blocks require.
//
// Every 5 seconds it checks each layer and only rewrites it when it has drifted
// (someone edited /etc/hosts, flushed iptables, deleted a policy file, etc.).
// It also kills blocked application processes each cycle. Because it only acts
// on drift, an idle system with no changes causes no disk writes.
//
// The daemon handles SIGTERM and SIGINT to shut down cleanly.
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

// Run starts the enforcement loop and blocks until a shutdown signal arrives.
func Run() error {
	database, err := db.OpenDB(db.ResolveDBPath())
	if err != nil {
		return err
	}
	defer database.Close()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	log.Println("daemon: starting Open Turkey enforcement loop")

	// Enforce immediately so blocks are in place the moment the daemon starts,
	// without waiting for the first tick.
	if err := enforce(database); err != nil {
		log.Printf("daemon: error in initial enforcement cycle: %v", err)
	}

	for {
		select {
		case <-ticker.C:
			if err := enforce(database); err != nil {
				log.Printf("daemon: error in enforcement cycle: %v", err)
			}
		case sig := <-sigChan:
			log.Printf("daemon: signal received (%v), shutting down gracefully...", sig)
			return nil
		}
	}
}

// enforce runs one enforcement cycle: it reads the active blocks and makes the
// site layers (hosts, firewall, browser policies) match the union of their
// domains, then kills any blocked processes.
//
// The key property is idempotence with no churn: each layer is only rewritten
// when its own "applied" check reports drift, and cleanup only runs when
// something of ours is actually present. So a steady state — including "blocks
// active but only apps, no domains" and "no blocks at all" — produces zero
// disk writes per tick.
func enforce(database *db.DB) error {
	activeBlocks, err := database.GetActiveBlocks()
	if err != nil {
		return err
	}

	// Union of the domains and apps across all active blocks.
	var domains []string
	var apps []string
	for _, block := range activeBlocks {
		domains = append(domains, block.Sites...)
		apps = append(apps, block.Apps...)
	}
	domains = blocker.ValidDomains(domains)

	if len(domains) > 0 {
		// --- Layer 1: /etc/hosts ---
		if !blocker.IsHostsApplied(domains) {
			log.Println("daemon: /etc/hosts drifted, reapplying")
			if err := blocker.ApplyHosts(domains); err != nil {
				log.Printf("daemon: error applying /etc/hosts: %v", err)
			}
		}

		// --- Layer 2: firewall (iptables / ip6tables) ---
		if !blocker.IsFirewallApplied() {
			log.Println("daemon: firewall drifted, reapplying")
			if err := blocker.ApplyFirewall(domains); err != nil {
				log.Printf("daemon: error applying firewall: %v", err)
			}
		}

		// --- Layer 3: browser policies ---
		if !blocker.IsBrowserPoliciesApplied(domains) {
			log.Println("daemon: browser policies drifted, reapplying")
			if err := blocker.ApplyBrowserPolicies(domains); err != nil {
				log.Printf("daemon: error applying browser policies: %v", err)
			}
		}
	} else {
		// No domains to block (no active blocks, or only apps). Remove each
		// site layer, but only if it is actually present, to avoid churn.
		if blocker.HostsSectionPresent() {
			if err := blocker.RemoveHosts(); err != nil {
				log.Printf("daemon: error removing /etc/hosts section: %v", err)
			}
		}
		if blocker.IsFirewallApplied() {
			if err := blocker.RemoveFirewall(); err != nil {
				log.Printf("daemon: error removing firewall: %v", err)
			}
		}
		if blocker.BrowserPoliciesPresent() {
			if err := blocker.RemoveBrowserPolicies(); err != nil {
				log.Printf("daemon: error removing browser policies: %v", err)
			}
		}
	}

	// --- Layer 4: kill blocked processes ---
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
