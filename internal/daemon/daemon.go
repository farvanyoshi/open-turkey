// Package daemon implements the main loop of Open Turkey's background service.
//
// The daemon runs as a systemd service and has one crucial job: making sure
// every blocking layer always matches what the active blocks require, and that
// scheduled blocks turn on and off at the right times.
//
// Every 5 seconds it runs one enforcement tick (see the enforce package): it
// reconciles schedules, then re-applies only the layers that have drifted
// (someone edited /etc/hosts, flushed iptables, deleted a policy file, etc.).
// Because it only acts on drift, an idle system causes no disk writes.
//
// The daemon handles SIGTERM and SIGINT to shut down cleanly.
package daemon

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/brunodcdo/open-turkey/internal/db"
	"github.com/brunodcdo/open-turkey/internal/enforce"
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

	// Enforce immediately so blocks are in place the moment the daemon starts.
	if err := enforce.Tick(database, time.Now()); err != nil {
		log.Printf("daemon: error in initial enforcement cycle: %v", err)
	}

	for {
		select {
		case <-ticker.C:
			if err := enforce.Tick(database, time.Now()); err != nil {
				log.Printf("daemon: error in enforcement cycle: %v", err)
			}
		case sig := <-sigChan:
			log.Printf("daemon: signal received (%v), shutting down gracefully...", sig)
			return nil
		}
	}
}
