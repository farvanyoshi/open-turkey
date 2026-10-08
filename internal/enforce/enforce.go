// Package enforce turns the set of active blocks into concrete layer actions.
//
// It is the single place that knows how to combine several active blocks (with
// different modes, patterns and schedules) into the inputs each blocking layer
// needs, so the CLI (on state changes) and the daemon (on every tick) behave
// identically.
//
//   - Apply forces every layer to match the active blocks right now. The CLI
//     uses it after start/stop/edit, when the desired set has just changed.
//   - Tick is the daemon's per-cycle routine: it applies queued edits whose
//     delay has passed, reconciles schedules (auto-activating/deactivating
//     scheduled blocks), then re-applies only the layers that have drifted, so
//     a steady state does no work.
package enforce

import (
	"log"
	"sort"
	"time"

	"github.com/brunodcdo/open-turkey/internal/blocker"
	"github.com/brunodcdo/open-turkey/internal/db"
	"github.com/brunodcdo/open-turkey/internal/schedule"
)

// Inputs derives the layer inputs from the active blocks:
//   - hostsDomains: blocklist-mode domains for /etc/hosts and the firewall
//     (allowlist mode can't be represented there, so only blocklist domains go
//     to those layers).
//   - apps: the union of process names to kill.
//   - bp: the full browser policy (blocklist domains, patterns, and allowlist
//     mode/domains if any active block is an allowlist).
//
// Everything is unioned across active blocks, including allowlists: with two
// allowlist blocks active, a site allowed by either one is reachable.
func Inputs(active []db.ActiveBlockDetail) (hostsDomains, apps []string, bp blocker.BrowserPolicy) {
	var blockDomains, allowDomains, patterns, appList []string
	allowMode := false

	for _, b := range active {
		appList = append(appList, b.Apps...)
		patterns = append(patterns, b.Patterns...)
		if b.Mode == db.ModeAllowlist {
			allowMode = true
			allowDomains = append(allowDomains, b.Sites...)
		} else {
			blockDomains = append(blockDomains, b.Sites...)
		}
	}

	bp = blocker.BrowserPolicy{
		BlockDomains:  blockDomains,
		BlockPatterns: patterns,
		AllowMode:     allowMode,
		AllowDomains:  allowDomains,
	}
	return blocker.ValidDomains(blockDomains), dedup(appList), bp
}

// Apply forces every layer to match the active blocks. Used by the CLI right
// after the desired state changes (start, stop, unlock, edit), where the domain
// set may have changed and we want the effect immediately.
func Apply(active []db.ActiveBlockDetail) error {
	hosts, apps, bp := Inputs(active)

	var firstErr error
	note := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	note(blocker.ApplyHosts(hosts))        // empty list => removes our section
	note(blocker.ApplyFirewall(hosts))     // empty list => removes the chain
	note(blocker.ApplyBrowserPolicies(bp)) // empty policy => removes files
	if len(apps) > 0 {
		blocker.KillBlocked(apps)
	}
	return firstErr
}

// ApplyFromDB reads the active blocks and applies them. Convenience for the CLI.
func ApplyFromDB(d *db.DB) error {
	active, err := d.GetActiveBlocks()
	if err != nil {
		return err
	}
	return Apply(active)
}

// Tick runs one daemon cycle: apply due queued edits, reconcile schedules,
// then re-apply drifted layers.
func Tick(d *db.DB, now time.Time) error {
	if err := applyDueEdits(d, now); err != nil {
		log.Printf("daemon: pending edits error: %v", err)
	}
	if err := reconcileSchedules(d, now); err != nil {
		log.Printf("daemon: schedule reconciliation error: %v", err)
	}

	active, err := d.GetActiveBlocks()
	if err != nil {
		return err
	}
	hosts, apps, bp := Inputs(active)

	// Site layers, re-applied only on drift (so an idle system does no writes).
	if len(hosts) > 0 {
		if !blocker.IsHostsApplied(hosts) {
			log.Println("daemon: /etc/hosts drifted, reapplying")
			if err := blocker.ApplyHosts(hosts); err != nil {
				log.Printf("daemon: error applying /etc/hosts: %v", err)
			}
		}
		if !blocker.IsFirewallApplied() {
			log.Println("daemon: firewall drifted, reapplying")
			if err := blocker.ApplyFirewall(hosts); err != nil {
				log.Printf("daemon: error applying firewall: %v", err)
			}
		}
	} else {
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
	}

	// Browser policies.
	if !bp.Empty() {
		if !blocker.IsBrowserPoliciesApplied(bp) {
			log.Println("daemon: browser policies drifted, reapplying")
			if err := blocker.ApplyBrowserPolicies(bp); err != nil {
				log.Printf("daemon: error applying browser policies: %v", err)
			}
		}
	} else if blocker.BrowserPoliciesPresent() {
		if err := blocker.RemoveBrowserPolicies(); err != nil {
			log.Printf("daemon: error removing browser policies: %v", err)
		}
	}

	// Kill blocked processes.
	if len(apps) > 0 {
		if killed, err := blocker.KillBlocked(apps); err != nil {
			log.Printf("daemon: error killing blocked processes: %v", err)
		} else if killed > 0 {
			log.Printf("daemon: %d blocked processes terminated", killed)
		}
	}

	return nil
}

// applyDueEdits applies the queued edits (see db/pending.go) whose time has
// come. An edit to a block that is char-locked or time-locked stays queued
// until the lock lifts, so the queue can't get past a manual lock. An edit
// that fails (e.g. the app was already removed) is logged and dropped.
func applyDueEdits(d *db.DB, now time.Time) error {
	pending, err := d.ListPendingEdits()
	if err != nil {
		return err
	}
	for _, p := range pending {
		if p.ApplyAt > now.Unix() {
			break // soonest first
		}
		detail, err := d.GetBlock(p.BlockName)
		if err != nil {
			log.Printf("daemon: pending edit: %v", err)
			continue
		}
		if detail.Active && (detail.Locked || detail.LockedUntil > now.Unix()) {
			continue
		}
		if err := d.ApplyEdit(p.BlockName, p.Edit); err != nil {
			log.Printf("daemon: pending edit to %s (%s) failed, dropping it: %v", p.BlockName, p.Edit, err)
		} else {
			log.Printf("daemon: applied queued edit to %s: %s", p.BlockName, p.Edit)
		}
		if err := d.DeletePendingEdit(p.ID); err != nil {
			return err
		}
	}
	return nil
}

// reconcileSchedules activates blocks their schedule enforces right now (see
// schedule.Enforced: a block window is open, or no allow window is) and
// deactivates the scheduled ones it no longer enforces. Manually-activated
// blocks are never auto-deactivated, nor are scheduled ones that were locked
// while active ("start --lock/--for/--until" on them): a time-locked block is
// deactivated on the first tick after its time lock expires, and a
// char-locked one stays on until it is unlocked. A scheduled block whose
// schedules have all been removed is deactivated like one whose window closed.
func reconcileSchedules(d *db.DB, now time.Time) error {
	all, err := d.AllSchedules()
	if err != nil {
		return err
	}
	active, err := d.GetActiveBlocks()
	if err != nil {
		return err
	}

	// Group windows by block; scheduled blocks with no schedules left get an
	// empty list, which schedule.Enforced treats as "off".
	windows := map[string][]schedule.Window{}
	for _, s := range all {
		windows[s.BlockName] = append(windows[s.BlockName], s.Window())
	}
	for _, b := range active {
		if _, ok := windows[b.BlockName]; !ok && b.Scheduled {
			windows[b.BlockName] = nil
		}
	}

	for name, ws := range windows {
		within := schedule.Enforced(ws, now)

		detail, err := d.GetBlock(name)
		if err != nil {
			log.Printf("daemon: schedule: %v", err)
			continue
		}

		switch {
		case within && !detail.Active:
			// Activate as a scheduled block (not char-locked, but the CLI
			// refuses to stop it while the schedule enforces it).
			if err := d.ActivateBlock(name, false, 0, 0, true); err != nil {
				log.Printf("daemon: schedule: failed to activate %s: %v", name, err)
			} else {
				log.Printf("daemon: schedule: activated %s", name)
			}
		case !within && detail.Active && detail.Scheduled && !detail.Locked && detail.LockedUntil <= now.Unix():
			if err := d.DeactivateBlock(name); err != nil {
				log.Printf("daemon: schedule: failed to deactivate %s: %v", name, err)
			} else {
				log.Printf("daemon: schedule: deactivated %s", name)
			}
		}
	}
	return nil
}

// ScheduleActiveNow reports whether a block's schedule enforces it now (see
// schedule.Enforced). The CLI uses it to refuse stopping/unlocking a scheduled
// block while that holds.
func ScheduleActiveNow(d *db.DB, blockName string, now time.Time) (bool, error) {
	schedules, err := d.ListSchedules(blockName)
	if err != nil {
		return false, err
	}
	return schedule.Enforced(db.Windows(schedules), now), nil
}

// ScheduleFrozen reports whether a block's schedule enforces it now or at any
// point within schedule.EditFreeze. The CLI refuses edits that could weaken the
// block while this holds, whether or not the block is active yet.
func ScheduleFrozen(d *db.DB, blockName string, now time.Time) (bool, error) {
	schedules, err := d.ListSchedules(blockName)
	if err != nil {
		return false, err
	}
	return schedule.EnforcedWithin(db.Windows(schedules), now, schedule.EditFreeze), nil
}

func dedup(in []string) []string {
	set := make(map[string]struct{}, len(in))
	for _, s := range in {
		set[s] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
