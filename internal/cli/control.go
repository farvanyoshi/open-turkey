// control.go — Block control commands: start, stop, unlock, status.
//
// Lifecycle:
//
//	open-turkey start <block>   → Activate a block (start blocking)
//	open-turkey stop <block>    → Deactivate a block (stop blocking)
//	open-turkey unlock <block>  → Unlock a char-locked block via the challenge
//	open-turkey status          → Show the status of every block
//
// WAYS A BLOCK CAN BE "HELD" (not freely removable)
// -------------------------------------------------
//   - Character lock (--lock): removable only by completing the typing
//     challenge via "unlock".
//   - Time lock (--for / --until): cannot be removed AT ALL until the time
//     passes — not even with the challenge. The strongest lock.
//   - Schedule window: while a scheduled window is open, the block is held; the
//     daemon turns it off when the window closes. For edits (not stop/unlock)
//     the hold starts schedule.EditFreeze (30 minutes) before the window
//     opens, so a block can't be emptied just before it is due to start.
//
// All of these are enforced centrally by blockHold so start/stop/unlock/edit and
// uninstall agree on when a block may be removed.
package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/brunodcdo/open-turkey/internal/db"
	"github.com/brunodcdo/open-turkey/internal/enforce"
	"github.com/brunodcdo/open-turkey/internal/lock"
	"github.com/spf13/cobra"
)

// ============================================================================
// startCmd — Activate a block
// ============================================================================

var startCmd = &cobra.Command{
	Use:   "start [block]",
	Short: "Activate a block",
	Long: `Activate a block, applying every blocking layer.

Locks (optional, and combinable):
  --lock            Require the typing challenge ('unlock') to deactivate.
  --lock-chars N    Challenge length in characters (default 600, minimum 30).
  --for DURATION    Time lock: cannot be removed for this long (e.g. 2h, 90m, 1h30m).
  --until HH:MM     Time lock until a clock time today (or tomorrow if already past).

A time lock cannot be lifted early by anyone short of root — that is the point.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		blockName := args[0]

		useLock, err := cmd.Flags().GetBool("lock")
		if err != nil {
			return err
		}
		lockChars, err := cmd.Flags().GetInt("lock-chars")
		if err != nil {
			return err
		}
		forDur, err := cmd.Flags().GetString("for")
		if err != nil {
			return err
		}
		untilClock, err := cmd.Flags().GetString("until")
		if err != nil {
			return err
		}

		if useLock && lockChars < lock.MinLockChars {
			return fmt.Errorf("--lock-chars must be at least %d (got %d)", lock.MinLockChars, lockChars)
		}
		if forDur != "" && untilClock != "" {
			return fmt.Errorf("use only one of --for or --until")
		}

		// Compute the time-lock expiry (0 = none).
		var lockedUntil int64
		now := time.Now()
		switch {
		case forDur != "":
			d, err := time.ParseDuration(forDur)
			if err != nil {
				return fmt.Errorf("invalid --for duration %q: %w", forDur, err)
			}
			if d <= 0 {
				return fmt.Errorf("--for duration must be positive")
			}
			lockedUntil = now.Add(d).Unix()
		case untilClock != "":
			t, err := untilToday(now, untilClock)
			if err != nil {
				return err
			}
			lockedUntil = t.Unix()
		}

		database, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close()

		detail, err := database.GetBlock(blockName)
		if err != nil {
			return err
		}
		if len(detail.Sites) == 0 && len(detail.Apps) == 0 && len(detail.Patterns) == 0 {
			return fmt.Errorf("block '%s' has nothing to enforce. Add sites, apps or patterns first", blockName)
		}
		if detail.Active {
			return fmt.Errorf("block '%s' is already active", blockName)
		}

		chars := 0
		if useLock {
			chars = lockChars
		}
		if err := database.ActivateBlock(blockName, useLock, chars, lockedUntil, false); err != nil {
			return err
		}

		if err := enforce.ApplyFromDB(database); err != nil {
			return fmt.Errorf("failed to apply blocking layers: %w", err)
		}

		msg := fmt.Sprintf("Block '%s' activated successfully", blockName)
		if useLock {
			msg += fmt.Sprintf(" [LOCKED - %d chars]", chars)
		}
		if lockedUntil > 0 {
			msg += fmt.Sprintf(" [TIME-LOCKED until %s]", time.Unix(lockedUntil, 0).Format("2006-01-02 15:04"))
		}
		fmt.Println(msg)
		notify("Open Turkey", fmt.Sprintf("Block '%s' is now active", blockName))
		return nil
	},
}

// ============================================================================
// stopCmd — Deactivate a block
// ============================================================================

var stopCmd = &cobra.Command{
	Use:   "stop [block]",
	Short: "Deactivate a block",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		blockName := args[0]

		database, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close()

		hold, err := blockHold(database, blockName, time.Now())
		if err != nil {
			return err
		}
		if !hold.active {
			return fmt.Errorf("block '%s' is not active", blockName)
		}
		if hold.scheduledNow {
			return fmt.Errorf("block '%s' is in a scheduled window and can't be stopped until it closes", blockName)
		}
		if hold.timeLocked() {
			return fmt.Errorf("block '%s' is time-locked until %s and can't be stopped before then", blockName, hold.until())
		}
		if hold.charLocked {
			return fmt.Errorf("block '%s' is locked. Use 'open-turkey unlock %s' first", blockName, blockName)
		}

		if err := database.DeactivateBlock(blockName); err != nil {
			return err
		}
		if err := enforce.ApplyFromDB(database); err != nil {
			return err
		}
		fmt.Printf("Block '%s' deactivated successfully\n", blockName)
		notify("Open Turkey", fmt.Sprintf("Block '%s' deactivated", blockName))
		return nil
	},
}

// ============================================================================
// unlockCmd — Unlock a char-locked block via the typing challenge
// ============================================================================

var unlockCmd = &cobra.Command{
	Use:   "unlock [block]",
	Short: "Unlock a locked block via the typing challenge",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		blockName := args[0]

		database, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close()

		hold, err := blockHold(database, blockName, time.Now())
		if err != nil {
			return err
		}
		if !hold.active {
			return fmt.Errorf("block '%s' is not active", blockName)
		}
		if hold.scheduledNow {
			return fmt.Errorf("block '%s' is in a scheduled window; it will lift automatically when the window closes", blockName)
		}
		if hold.timeLocked() {
			return fmt.Errorf("block '%s' is time-locked until %s; it cannot be unlocked before then", blockName, hold.until())
		}
		if !hold.charLocked {
			return fmt.Errorf("block '%s' is not locked. Use 'open-turkey stop' to deactivate it", blockName)
		}

		success, err := lock.RunChallenge(hold.lockChars)
		if err != nil {
			return fmt.Errorf("failed to run unlock challenge: %w", err)
		}
		if !success {
			fmt.Println("Unlock failed. The block remains active and locked.")
			os.Exit(1)
		}

		if err := database.DeactivateBlock(blockName); err != nil {
			return err
		}
		if err := enforce.ApplyFromDB(database); err != nil {
			return err
		}
		fmt.Printf("Block '%s' unlocked and deactivated successfully\n", blockName)
		return nil
	},
}

// ============================================================================
// statusCmd — Show the status of every block
// ============================================================================

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the status of all blocks",
	RunE: func(cmd *cobra.Command, args []string) error {
		database, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close()

		blocks, err := database.ListBlocks()
		if err != nil {
			return err
		}
		if len(blocks) == 0 {
			fmt.Println("No blocks registered. Create one with 'open-turkey block create'.")
			return nil
		}

		now := time.Now()
		var active, inactive []string
		for _, block := range blocks {
			detail, err := database.GetBlock(block.Name)
			if err != nil {
				return err
			}
			tags := ""
			if detail.Mode == db.ModeAllowlist {
				tags += " [ALLOWLIST]"
			}
			if detail.Active {
				if detail.Scheduled {
					tags += " [SCHEDULED]"
				}
				if detail.Locked {
					tags += fmt.Sprintf(" [LOCKED - %d chars]", detail.LockChars)
				}
				if detail.LockedUntil > now.Unix() {
					tags += fmt.Sprintf(" [TIME-LOCKED until %s]", time.Unix(detail.LockedUntil, 0).Format("2006-01-02 15:04"))
				}
				active = append(active, fmt.Sprintf("  %s%s", detail.Name, tags))
			} else {
				inactive = append(inactive, fmt.Sprintf("  %s%s", detail.Name, tags))
			}
		}

		fmt.Println("=== Open Turkey Status ===")
		fmt.Println()
		fmt.Println("Active blocks:")
		printLinesOrNone(active)
		fmt.Println()
		fmt.Println("Inactive blocks:")
		printLinesOrNone(inactive)
		fmt.Println()
		fmt.Printf("Total: %d blocks (%d active, %d inactive)\n", len(active)+len(inactive), len(active), len(inactive))
		return nil
	},
}

func printLinesOrNone(lines []string) {
	if len(lines) == 0 {
		fmt.Println("  (none)")
		return
	}
	for _, l := range lines {
		fmt.Println(l)
	}
}

// ============================================================================
// Hold state — the single source of truth for "can this block be removed now?"
// ============================================================================

type holdState struct {
	active       bool
	charLocked   bool
	lockChars    int
	lockedUntil  int64
	scheduledNow bool
	// scheduleFrozen: a schedule window is open now or opens within
	// schedule.EditFreeze, whether or not the block is active yet.
	scheduleFrozen bool
	now            time.Time
}

func (h holdState) timeLocked() bool { return h.lockedUntil > h.now.Unix() }
func (h holdState) until() string {
	return time.Unix(h.lockedUntil, 0).Format("2006-01-02 15:04")
}

// held reports whether the block currently can't be freely removed/edited.
func (h holdState) held() bool {
	return h.scheduleFrozen || h.active && (h.charLocked || h.timeLocked() || h.scheduledNow)
}

// blockHold gathers everything that determines whether a block may be removed
// or edited right now.
func blockHold(database *db.DB, name string, now time.Time) (holdState, error) {
	detail, err := database.GetBlock(name)
	if err != nil {
		return holdState{}, err
	}
	h := holdState{
		active:      detail.Active,
		charLocked:  detail.Locked,
		lockChars:   detail.LockChars,
		lockedUntil: detail.LockedUntil,
		now:         now,
	}
	if detail.Active {
		sched, err := enforce.ScheduleActiveNow(database, name, now)
		if err != nil {
			return holdState{}, err
		}
		h.scheduledNow = sched
	}
	frozen, err := enforce.ScheduleFrozen(database, name, now)
	if err != nil {
		return holdState{}, err
	}
	h.scheduleFrozen = frozen
	return h, nil
}

// untilToday parses "HH:MM" into the next time it occurs from now (today, or
// tomorrow if the time has already passed today).
func untilToday(now time.Time, clock string) (time.Time, error) {
	var h, m int
	if _, err := fmt.Sscanf(clock, "%d:%d", &h, &m); err != nil {
		return time.Time{}, fmt.Errorf("invalid --until time %q (want HH:MM)", clock)
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return time.Time{}, fmt.Errorf("invalid --until time %q", clock)
	}
	t := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
	if !t.After(now) {
		t = t.Add(24 * time.Hour)
	}
	return t, nil
}

func init() {
	startCmd.Flags().Bool("lock", false, "Lock the block (requires a challenge to deactivate)")
	startCmd.Flags().Int("lock-chars", lock.DefaultLockChars, "Number of characters in the unlock challenge")
	startCmd.Flags().String("for", "", "Time lock: keep the block un-removable for this duration (e.g. 2h, 90m)")
	startCmd.Flags().String("until", "", "Time lock: keep the block un-removable until this clock time (HH:MM)")

	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(stopCmd)
	rootCmd.AddCommand(unlockCmd)
	rootCmd.AddCommand(statusCmd)
}
