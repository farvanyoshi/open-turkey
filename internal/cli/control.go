// control.go — Block control commands: start, stop, unlock, status.
//
// This file implements the commands that control a block's LIFECYCLE:
//
//	open-turkey start <block>   → Activates a block (starts blocking sites/apps)
//	open-turkey stop <block>    → Deactivates a block (stops blocking)
//	open-turkey unlock <block>  → Unlocks a locked block via the typing challenge
//	open-turkey status          → Shows the status of every block
//
// HOW DOES ACTIVATING A BLOCK WORK?
// ---------------------------------
// When the user runs "open-turkey start social-media", the system:
//  1. Marks the block as active in the database
//  2. Collects ALL domains from ALL active blocks (not just the new one)
//  3. Applies the 4 blocking layers:
//     - /etc/hosts (blocks local DNS)
//     - iptables firewall (blocks network connections)
//     - Browser policies (blocks in Firefox/Chrome/Chromium)
//     - Kills blocked processes (closes apps like Discord, Slack, etc.)
//
// Why reapply ALL layers and not just the new block's?
// Because the layers are "global" — /etc/hosts, for example, has ONE Open
// Turkey section with ALL blocked domains. You can't add domains
// incrementally without risking inconsistency. It's safer to rebuild
// everything from scratch on every change.
//
// WHAT IS THE "LOCK"?
// -------------------
// When the user activates a block with --lock, they're saying: "I know I'm
// going to try to sabotage myself, so make it hard to disable this block".
// A locked block can only be deactivated via the "unlock" command, which
// requires typing a huge random string (the "typing challenge"). This creates
// enough friction for the "I need to check Instagram" impulse to pass.
package cli

import (
	"fmt"
	"os"

	"github.com/brunodcdo/open-turkey/internal/blocker"
	"github.com/brunodcdo/open-turkey/internal/db"
	"github.com/brunodcdo/open-turkey/internal/lock"
	"github.com/spf13/cobra"
)

// ============================================================================
// startCmd — Activate a block
// ============================================================================

// startCmd activates a block registered in the database.
//
// Usage: open-turkey start <block-name> [--lock] [--lock-chars N]
//
// Examples:
//
//	open-turkey start social-media              → activate without a lock
//	open-turkey start social-media --lock       → activate with a lock (300 chars default)
//	open-turkey start social-media --lock --lock-chars 500  → lock with 500 chars
//
// Flags:
//
//	--lock         Locks the block so it can't be deactivated with "stop".
//	               The user will need to use "unlock" with the typing challenge.
//	--lock-chars   Number of random characters in the unlock challenge.
//	               Default: 300. The more, the harder to unlock.
var startCmd = &cobra.Command{
	Use:   "start [block]",
	Short: "Activate a block",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Extract the block name from the first argument.
		// Cobra already guaranteed we have exactly 1 argument (ExactArgs(1)).
		blockName := args[0]

		// Read the --lock and --lock-chars flags.
		// cmd.Flags().GetBool/GetInt return the flag value and a possible
		// parsing error (unlikely, since Cobra validates types automatically).
		useLock, err := cmd.Flags().GetBool("lock")
		if err != nil {
			return fmt.Errorf("failed to read --lock flag: %w", err)
		}

		unlockChars, err := cmd.Flags().GetInt("lock-chars")
		if err != nil {
			return fmt.Errorf("failed to read --lock-chars flag: %w", err)
		}

		// A lock is only meaningful if its challenge is non-trivial. Reject a
		// zero, negative or tiny value so "--lock --lock-chars 0" can't create
		// a lock that's unlocked by pressing Enter.
		if useLock && unlockChars < lock.MinLockChars {
			return fmt.Errorf("--lock-chars must be at least %d (got %d)", lock.MinLockChars, unlockChars)
		}

		// --- Step 1: Open the database ---
		// openDB() is a helper defined in block.go that opens the SQLite
		// database at the default path (/var/lib/open-turkey/open-turkey.db).
		database, err := openDB()
		if err != nil {
			return err
		}
		// defer ensures the database is closed when the function ends, even
		// if an error occurs along the way. It's the equivalent of "finally"
		// in other languages.
		defer database.Close()

		// --- Step 2: Check that the block exists and has sites/apps ---
		// We need to confirm the block exists before trying to activate it.
		// We also check that it has at least one site or app configured,
		// since activating an empty block makes no sense.
		detail, err := database.GetBlock(blockName)
		if err != nil {
			return err
		}

		if len(detail.Sites) == 0 && len(detail.Apps) == 0 {
			return fmt.Errorf("block '%s' has no sites or apps configured. Add some with 'open-turkey block add-site' or 'open-turkey block add-app'", blockName)
		}

		// --- Step 3: Check whether it's already active ---
		// If the block is already active, activating it again makes no sense.
		// We tell the user to avoid confusion.
		if detail.Active {
			return fmt.Errorf("block '%s' is already active", blockName)
		}

		// --- Step 4: Activate the block in the database ---
		// If --lock wasn't passed, lockChars stays 0 (no lock).
		// If --lock was passed, we use the --lock-chars value (default 300).
		lockChars := 0
		if useLock {
			lockChars = unlockChars
		}

		if err := database.ActivateBlock(blockName, useLock, lockChars); err != nil {
			return err
		}

		// --- Step 5: Collect ALL domains and apps from ALL active blocks ---
		// We need every domain because the blocking layers are global.
		// If the "social-media" block blocks facebook.com and the "games"
		// block blocks steam.com, we need to apply BOTH in /etc/hosts,
		// the firewall, etc.
		domains, err := database.GetAllBlockedDomains()
		if err != nil {
			return fmt.Errorf("failed to fetch blocked domains: %w", err)
		}

		apps, err := database.GetAllBlockedApps()
		if err != nil {
			return fmt.Errorf("failed to fetch blocked apps: %w", err)
		}

		// --- Step 6: Apply the 4 blocking layers ---
		// Each layer is a different blocking mechanism that works
		// independently of the others. Together, they make it very hard to
		// get around.

		// Layer 1: /etc/hosts — redirects domains to 0.0.0.0 (invalid IP).
		// It's the first line of defense and affects EVERY program on the system.
		if err := blocker.ApplyHosts(domains); err != nil {
			return fmt.Errorf("failed to apply /etc/hosts block: %w", err)
		}

		// Layer 2: iptables (firewall) — blocks network packets to the sites' IPs.
		// Works even if the user finds the real IP and tries to access it directly.
		if err := blocker.ApplyFirewall(domains); err != nil {
			return fmt.Errorf("failed to apply firewall block: %w", err)
		}

		// Layer 3: Browser policies — blocks directly in Firefox/Chrome/Chromium.
		// The browser shows a "Blocked by your organization's policy" page.
		if err := blocker.ApplyBrowserPolicies(domains); err != nil {
			return fmt.Errorf("failed to apply browser policies: %w", err)
		}

		// Layer 4: Kill processes — terminates blocked apps that are running.
		// Uses SIGKILL (signal 9) to make sure the process dies immediately.
		if len(apps) > 0 {
			blocker.KillBlocked(apps)
		}

		// --- Step 7: Success message ---
		// Tell the user whether the block was activated with or without a lock.
		if useLock {
			fmt.Printf("Block '%s' activated successfully [LOCKED - %d chars to unlock]\n", blockName, lockChars)
		} else {
			fmt.Printf("Block '%s' activated successfully\n", blockName)
		}

		return nil
	},
}

// ============================================================================
// stopCmd — Deactivate a block
// ============================================================================

// stopCmd deactivates a block that is active.
//
// Usage: open-turkey stop <block-name>
//
// IMPORTANT: If the block is locked (--lock), "stop" is refused.
// The user must use "open-turkey unlock <block>" to unlock it first. This is
// intentional — the lock exists to prevent impulsive deactivation.
//
// After deactivating, the system reapplies the layers with the remaining
// domains (from other active blocks). If no block remains active, ALL
// blocking layers are removed completely.
var stopCmd = &cobra.Command{
	Use:   "stop [block]",
	Short: "Deactivate a block",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		blockName := args[0]

		// --- Step 1: Open the database ---
		database, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close()

		// --- Step 2: Check that the block is active ---
		// Deactivating something that isn't active makes no sense.
		active, err := database.IsBlockActive(blockName)
		if err != nil {
			return err
		}
		if !active {
			return fmt.Errorf("block '%s' is not active", blockName)
		}

		// --- Step 3: Check whether the block is locked ---
		// If it's locked, the user can NOT use "stop". They must use "unlock",
		// which requires typing a huge random string (the typing challenge).
		// This is the "friction" mechanism that prevents impulsive deactivation.
		locked, err := database.IsBlockLocked(blockName)
		if err != nil {
			return err
		}
		if locked {
			return fmt.Errorf("block '%s' is locked. Use 'open-turkey unlock' first", blockName)
		}

		// --- Step 4: Deactivate the block in the database ---
		if err := database.DeactivateBlock(blockName); err != nil {
			return err
		}

		// --- Step 5: Reapply or remove the blocking layers ---
		// After deactivating a block, we need to update the blocking layers.
		// There are two possible scenarios:
		//
		// Scenario A: Other blocks are still active.
		//   → Reapply ALL layers with the remaining domains/apps.
		//
		// Scenario B: No block is active anymore.
		//   → Remove ALL blocking layers completely.
		if err := reapplyOrRemoveLayers(database); err != nil {
			return err
		}

		fmt.Printf("Block '%s' deactivated successfully\n", blockName)
		return nil
	},
}

// ============================================================================
// unlockCmd — Unlock a locked block via the typing challenge
// ============================================================================

// unlockCmd allows deactivating a block that was activated with --lock.
//
// Usage: open-turkey unlock <block-name>
//
// The flow is:
//  1. Check that the block is active and locked
//  2. Generate a random string of N characters (set at activation)
//  3. The user must type the string EXACTLY
//  4. If they get it right: the block is deactivated
//  5. If they get it wrong: the block stays in place
//
// This command is the ONLY way to deactivate a locked block. It was designed
// to be tedious and slow on purpose — the goal is for the urge to unlock to
// pass before the user finishes typing.
var unlockCmd = &cobra.Command{
	Use:   "unlock [block]",
	Short: "Unlock a locked block via the typing challenge",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		blockName := args[0]

		// --- Step 1: Open the database ---
		database, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close()

		// --- Step 2: Check that the block is active ---
		// Unlocking something that isn't active makes no sense.
		active, err := database.IsBlockActive(blockName)
		if err != nil {
			return err
		}
		if !active {
			return fmt.Errorf("block '%s' is not active", blockName)
		}

		// --- Step 3: Check that the block is actually locked ---
		// If it's not locked, the user can simply use "stop".
		locked, err := database.IsBlockLocked(blockName)
		if err != nil {
			return err
		}
		if !locked {
			return fmt.Errorf("block '%s' is not locked. Use 'open-turkey stop' to deactivate it", blockName)
		}

		// --- Step 4: Get the number of challenge characters ---
		// The lock_chars value was set when the block was activated with --lock.
		// It's stored in the database so we know how many characters to generate.
		detail, err := database.GetBlock(blockName)
		if err != nil {
			return err
		}

		// --- Step 5: Run the typing challenge ---
		// RunChallenge generates a random string, shows it to the user,
		// reads what they typed and compares it character by character.
		// Returns true if the challenge was completed successfully.
		success, err := lock.RunChallenge(detail.LockChars)
		if err != nil {
			return fmt.Errorf("failed to run unlock challenge: %w", err)
		}

		// --- Step 6: If it failed, the block stays in place ---
		if !success {
			fmt.Println("Unlock failed. The block remains active and locked.")
			os.Exit(1)
		}

		// --- Step 7: Challenge passed — deactivate the block ---
		if err := database.DeactivateBlock(blockName); err != nil {
			return err
		}

		// --- Step 8: Reapply or remove layers (same logic as stop) ---
		if err := reapplyOrRemoveLayers(database); err != nil {
			return err
		}

		fmt.Printf("Block '%s' unlocked and deactivated successfully\n", blockName)
		return nil
	},
}

// ============================================================================
// statusCmd — Show the status of every block
// ============================================================================

// statusCmd shows a summary of the current state of every registered block.
//
// Usage: open-turkey status
//
// Example output:
//
//	=== Open Turkey Status ===
//
//	Active blocks:
//	  social-media [LOCKED - 300 chars]
//	  games
//
//	Inactive blocks:
//	  work
//
//	Total: 3 blocks (2 active, 1 inactive)
//
// This command is useful for a quick overview without having to inspect
// each block individually.
var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the status of all blocks",
	RunE: func(cmd *cobra.Command, args []string) error {
		// --- Step 1: Open the database ---
		database, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close()

		// --- Step 2: Fetch every registered block ---
		// ListBlocks returns every block with its site/app counts.
		blocks, err := database.ListBlocks()
		if err != nil {
			return err
		}

		// If there are no registered blocks, tell the user.
		if len(blocks) == 0 {
			fmt.Println("No blocks registered. Create one with 'open-turkey block create'.")
			return nil
		}

		// --- Step 3: Fetch activation info for each block ---
		// We need to know which blocks are active and whether they're locked.
		// For that, we call GetBlock for each block, which returns the full
		// status (active, locked, lock_chars).
		//
		// We split the blocks into two lists: active and inactive.
		// Each active block becomes a string formatted with or without "[LOCKED]".
		var activeBlocks []string
		var inactiveBlocks []string

		for _, block := range blocks {
			// GetBlock returns full details including Active, Locked, LockChars.
			detail, err := database.GetBlock(block.Name)
			if err != nil {
				return err
			}

			if detail.Active {
				// If it's active, format it with lock info (if any).
				if detail.Locked {
					activeBlocks = append(activeBlocks, fmt.Sprintf("  %s [LOCKED - %d chars]", detail.Name, detail.LockChars))
				} else {
					activeBlocks = append(activeBlocks, fmt.Sprintf("  %s", detail.Name))
				}
			} else {
				inactiveBlocks = append(inactiveBlocks, fmt.Sprintf("  %s", detail.Name))
			}
		}

		// --- Step 4: Print the formatted report ---
		fmt.Println("=== Open Turkey Status ===")
		fmt.Println()

		// Active blocks section.
		fmt.Println("Active blocks:")
		if len(activeBlocks) == 0 {
			fmt.Println("  (none)")
		} else {
			for _, line := range activeBlocks {
				fmt.Println(line)
			}
		}

		fmt.Println()

		// Inactive blocks section.
		fmt.Println("Inactive blocks:")
		if len(inactiveBlocks) == 0 {
			fmt.Println("  (none)")
		} else {
			for _, line := range inactiveBlocks {
				fmt.Println(line)
			}
		}

		fmt.Println()

		// Totals line.
		totalActive := len(activeBlocks)
		totalInactive := len(inactiveBlocks)
		total := totalActive + totalInactive
		fmt.Printf("Total: %d blocks (%d active, %d inactive)\n", total, totalActive, totalInactive)

		return nil
	},
}

// ============================================================================
// Helper functions
// ============================================================================

// reapplyOrRemoveLayers updates the blocking layers after a change to the
// active blocks.
//
// This function centralizes the logic shared by stopCmd, unlockCmd and the
// block-editing commands: after a change, we need to decide whether to
// reapply the layers with the remaining domains or remove everything.
//
// Two possible scenarios:
//
//  1. Active blocks still exist: reapply all 4 layers with the domains/apps
//     of the blocks that are still active. This ensures the other blocks
//     keep working.
//
//  2. No active blocks remain: remove every layer completely.
//     The system returns to the "free" state — no site or app blocked.
func reapplyOrRemoveLayers(database *db.DB) error {
	// Fetch the remaining domains and apps (from blocks that are still active).
	domains, err := database.GetAllBlockedDomains()
	if err != nil {
		return fmt.Errorf("failed to fetch remaining blocked domains: %w", err)
	}

	apps, err := database.GetAllBlockedApps()
	if err != nil {
		return fmt.Errorf("failed to fetch remaining blocked apps: %w", err)
	}

	// Fetch the active blocks to know whether any remain.
	activeBlocks, err := database.GetActiveBlocks()
	if err != nil {
		return fmt.Errorf("failed to fetch active blocks: %w", err)
	}

	if len(activeBlocks) > 0 {
		// Scenario 1: Blocks are still active — reapply every layer.
		// Rebuild everything from scratch with the remaining domains to
		// guarantee consistency. It's safer than trying to remove domains
		// individually.

		if err := blocker.ApplyHosts(domains); err != nil {
			return fmt.Errorf("failed to reapply /etc/hosts block: %w", err)
		}

		if err := blocker.ApplyFirewall(domains); err != nil {
			return fmt.Errorf("failed to reapply firewall block: %w", err)
		}

		if err := blocker.ApplyBrowserPolicies(domains); err != nil {
			return fmt.Errorf("failed to reapply browser policies: %w", err)
		}

		// Kill processes from the blocks that are still active.
		if len(apps) > 0 {
			blocker.KillBlocked(apps)
		}
	} else {
		// Scenario 2: No active blocks — remove every layer.
		// The system returns to the "clean" state — no blocks.

		if err := blocker.RemoveHosts(); err != nil {
			return fmt.Errorf("failed to remove /etc/hosts block: %w", err)
		}

		if err := blocker.RemoveFirewall(); err != nil {
			return fmt.Errorf("failed to remove firewall block: %w", err)
		}

		if err := blocker.RemoveBrowserPolicies(); err != nil {
			return fmt.Errorf("failed to remove browser policies: %w", err)
		}
	}

	return nil
}

// ============================================================================
// init() — Registers the commands with Cobra
// ============================================================================

// init() is called automatically by Go when the package is imported.
// Here we register our commands as children of rootCmd (the root command),
// making them available as subcommands:
//
//	open-turkey start ...
//	open-turkey stop ...
//	open-turkey unlock ...
//	open-turkey status
//
// We also configure startCmd's flags here, since Cobra requires flags to be
// registered before the command runs.
func init() {
	// Register startCmd's flags.
	// Flags().Bool and Flags().Int create flags with a long name only
	// (no one-letter shorthand).
	startCmd.Flags().Bool("lock", false, "Lock the block (requires a challenge to deactivate)")
	startCmd.Flags().Int("lock-chars", lock.DefaultLockChars, "Number of characters in the unlock challenge")

	// Add every command as a child of the root command.
	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(stopCmd)
	rootCmd.AddCommand(unlockCmd)
	rootCmd.AddCommand(statusCmd)
}
