// This file defines the CLI commands for managing blocks in Open Turkey.
//
// A "block" is a named group of sites and applications that the user wants to
// block together. For example, a "social-media" block might contain the
// domains facebook.com, instagram.com and the processes discord, telegram.
//
// The commands here are the interface between the user and the database.
// Each command follows the same pattern:
//  1. Open the database (openDB)
//  2. Call the matching function in the db package
//  3. Print the result or the error message
//
// Every subcommand lives under "open-turkey block":
//   - open-turkey block create [name]
//   - open-turkey block add-site [block] [domains...]
//   - open-turkey block remove-site [block] [domains...]
//   - open-turkey block add-app [block] [processes...]
//   - open-turkey block remove-app [block] [processes...]
//   - open-turkey block list
//   - open-turkey block info [name]
//   - open-turkey block remove [name]
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/brunodcdo/open-turkey/internal/blocker"
	"github.com/brunodcdo/open-turkey/internal/db"
	"github.com/brunodcdo/open-turkey/internal/enforce"
	"github.com/spf13/cobra"
)

// --------------------------------------------------------------------------
// Helper function to open the database
// --------------------------------------------------------------------------

// openDB opens the connection to the SQLite database.
// Before opening, we create the parent directory if it doesn't exist
// (os.MkdirAll). This is needed because SQLite doesn't create directories
// automatically — it only creates the .db file, and needs the directory to
// already exist.
//
// We use db.DefaultDBPath to keep the path centralized in the db package.
// If in the future we want to allow a custom path (via a flag), we just
// replace this constant with a configurable variable.
func openDB() (*db.DB, error) {
	path := db.ResolveDBPath()

	// filepath.Dir() extracts the parent directory from the full path.
	// Example: "/var/lib/open-turkey/open-turkey.db" → "/var/lib/open-turkey"
	dir := filepath.Dir(path)

	// os.MkdirAll creates the directory and every required parent.
	// If the directory already exists, it does nothing (idempotent).
	// Permission 0755: owner can read/write/execute, others can read/execute.
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory '%s': %w", dir, err)
	}

	return db.OpenDB(path)
}

// ensureEditable refuses edits to a block that is active and locked.
//
// A locked block is meant to be immovable until it is unlocked (via the typing
// challenge, or a time lock's expiry). Allowing its site/app lists to be edited
// would be a trivial bypass: emptying a block deactivates it, and even trimming
// entries weakens a lock the user set precisely so they couldn't weaken it on
// impulse. So all four edit commands go through this guard.
func ensureEditable(database *db.DB, name string) error {
	hold, err := blockHold(database, name, time.Now())
	if err != nil {
		return err
	}
	if hold.held() {
		return fmt.Errorf("block '%s' is currently held (locked, time-locked or in a scheduled window) and cannot be edited. Remove the hold first", name)
	}
	return nil
}

// normalizeDomains normalizes each raw domain and rejects any that aren't valid
// hostnames, returning a clear error naming the offending input. This is the
// user-facing gate; the blocking layers validate again defensively.
func normalizeDomains(raw []string) ([]string, error) {
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		host := blocker.NormalizeDomain(r)
		if !blocker.IsValidDomain(host) {
			return nil, fmt.Errorf("invalid domain %q (expected something like 'example.com')", r)
		}
		out = append(out, host)
	}
	return out, nil
}

// --------------------------------------------------------------------------
// Parent command: block
// --------------------------------------------------------------------------

// blockCmd is the parent command of every block subcommand.
// It does nothing on its own — it only serves as a grouping.
// When the user types "open-turkey block" without a subcommand,
// Cobra shows the help automatically.
var blockCmd = &cobra.Command{
	Use:   "block",
	Short: "Manage blocks of sites and applications",
}

// --------------------------------------------------------------------------
// Subcommand: block create
// --------------------------------------------------------------------------

// blockCreateCmd creates a new block with the given name.
// The name must be unique — if a block with the same name already exists,
// the database returns an error.
//
// Usage:
//
//	open-turkey block create social-media
//	open-turkey block create focus --template social-media
//	open-turkey block create work --allowlist
var blockCreateCmd = &cobra.Command{
	Use:   "create [name]",
	Short: "Create a new block",
	Long: `Create a new block.

  --allowlist         Make it an allowlist block (block everything EXCEPT its
                      sites; enforced at the browser layer only).
  --template NAME     Seed it from a built-in list (social-media, news, video,
                      shopping).`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]

		allowlist, _ := cmd.Flags().GetBool("allowlist")
		template, _ := cmd.Flags().GetString("template")

		mode := db.ModeBlocklist
		if allowlist {
			mode = db.ModeAllowlist
		}

		// Resolve template seeds (validated) before touching the database, so a
		// bad template name fails cleanly with nothing created.
		var seeds []string
		if template != "" {
			raw, err := templateDomains(template)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			seeds, err = normalizeDomains(raw)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		}

		database, err := openDB()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		defer database.Close()

		if err := database.CreateBlock(name, mode); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		if len(seeds) > 0 {
			if err := database.AddSites(name, seeds); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		}

		fmt.Printf("Block '%s' created successfully (%s).\n", name, mode)
		if len(seeds) > 0 {
			fmt.Printf("Seeded %d sites from template '%s'.\n", len(seeds), template)
		}
	},
}

// --------------------------------------------------------------------------
// Subcommand: block add-site
// --------------------------------------------------------------------------

// blockAddSiteCmd adds one or more domains (sites) to an existing block.
// The first argument is the block name, and the rest are the domains.
//
// Usage: open-turkey block add-site social-media facebook.com instagram.com
var blockAddSiteCmd = &cobra.Command{
	Use:   "add-site [block] [domains...]",
	Short: "Add sites to a block",
	// cobra.MinimumNArgs(2) requires at least 2 arguments:
	// the block name + at least 1 domain.
	Args: cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		// The first argument is the block name.
		name := args[0]

		// Normalize and validate each domain. Rejecting invalid input here
		// (and again in the blocking layers) prevents crafted strings — e.g.
		// one containing a newline — from being injected into /etc/hosts.
		domains, err := normalizeDomains(args[1:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		database, err := openDB()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		defer database.Close()

		if err := ensureEditable(database, name); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		if err := database.AddSites(name, domains); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		active, err := database.IsBlockActive(name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if active {
			if err := enforce.ApplyFromDB(database); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		}

		fmt.Printf("Sites added to block '%s' successfully.\n", name)
	},
}

// --------------------------------------------------------------------------
// Subcommand: block remove-site
// --------------------------------------------------------------------------

// blockRemoveSiteCmd removes one or more domains (sites) from an existing block.
//
// Usage: open-turkey block remove-site social-media facebook.com instagram.com
var blockRemoveSiteCmd = &cobra.Command{
	Use:   "remove-site [block] [domains...]",
	Short: "Remove sites from a block",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		// Normalize each domain to guarantee a match with what's in the database.
		domains, err := normalizeDomains(args[1:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		database, err := openDB()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		defer database.Close()

		if err := ensureEditable(database, name); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		if err := database.RemoveSites(name, domains); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		active, err := database.IsBlockActive(name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if active {
			// If the block became empty, deactivate it to avoid an "active with
			// nothing" state (and to ensure the layers are removed if nothing
			// active remains).
			detail, err := database.GetBlock(name)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}

			if len(detail.Sites) == 0 && len(detail.Apps) == 0 {
				if err := database.DeactivateBlock(name); err != nil {
					fmt.Fprintf(os.Stderr, "Error: %v\n", err)
					os.Exit(1)
				}
			}

			if err := enforce.ApplyFromDB(database); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		}

		fmt.Printf("Sites removed from block '%s' successfully.\n", name)
	},
}

// --------------------------------------------------------------------------
// Subcommand: block add-app
// --------------------------------------------------------------------------

// blockAddAppCmd adds one or more process names (apps) to an existing block.
// The first argument is the block name, and the rest are the process names.
//
// Usage: open-turkey block add-app social-media discord telegram
var blockAddAppCmd = &cobra.Command{
	Use:   "add-app [block] [processes...]",
	Short: "Add applications to a block",
	// Same logic as add-site: needs the block name + at least 1 process.
	Args: cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		// The names of the processes to block (e.g. "discord", "telegram").
		processNames := args[1:]

		database, err := openDB()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		defer database.Close()

		if err := ensureEditable(database, name); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		if err := database.AddApps(name, processNames); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		active, err := database.IsBlockActive(name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if active {
			if err := enforce.ApplyFromDB(database); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		}

		fmt.Printf("Applications added to block '%s' successfully.\n", name)
	},
}

// --------------------------------------------------------------------------
// Subcommand: block remove-app
// --------------------------------------------------------------------------

// blockRemoveAppCmd removes one or more process names (apps) from an existing block.
//
// Usage: open-turkey block remove-app social-media discord telegram
var blockRemoveAppCmd = &cobra.Command{
	Use:   "remove-app [block] [processes...]",
	Short: "Remove applications from a block",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		processNames := args[1:]

		database, err := openDB()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		defer database.Close()

		if err := ensureEditable(database, name); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		if err := database.RemoveApps(name, processNames); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		active, err := database.IsBlockActive(name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if active {
			detail, err := database.GetBlock(name)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}

			if len(detail.Sites) == 0 && len(detail.Apps) == 0 {
				if err := database.DeactivateBlock(name); err != nil {
					fmt.Fprintf(os.Stderr, "Error: %v\n", err)
					os.Exit(1)
				}
			}

			if err := enforce.ApplyFromDB(database); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		}

		fmt.Printf("Applications removed from block '%s' successfully.\n", name)
	},
}

// --------------------------------------------------------------------------
// Subcommand: block list
// --------------------------------------------------------------------------

// blockListCmd lists every block registered in the database.
// Shows a formatted table with the block name, number of sites,
// number of apps and the creation date.
//
// Usage: open-turkey block list
var blockListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all blocks",
	Run: func(cmd *cobra.Command, args []string) {
		database, err := openDB()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		defer database.Close()

		// ListBlocks returns a slice (list) of blocks with counts.
		blocks, err := database.ListBlocks()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		// If there are no registered blocks, show a friendly message.
		if len(blocks) == 0 {
			fmt.Println("No blocks registered.")
			return
		}

		// Print the table header.
		// %-20s = left-aligned string, 20 characters wide.
		// %-6s  = left-aligned string, 6 characters wide.
		// This format keeps the columns visually aligned.
		fmt.Printf("%-20s %-6s %-6s %s\n", "BLOCK", "SITES", "APPS", "CREATED AT")

		// Iterate over each block and print a table row.
		for _, b := range blocks {
			// %-20s for the block name (left-aligned).
			// %-6d for the numbers (left-aligned, integer format).
			// %s for the creation date.
			fmt.Printf("%-20s %-6d %-6d %s\n", b.Name, b.SiteCount, b.AppCount, b.CreatedAt)
		}
	},
}

// --------------------------------------------------------------------------
// Subcommand: block info
// --------------------------------------------------------------------------

// blockInfoCmd shows the full details of a specific block.
// Shows the name, creation date, activation status, and the lists
// of blocked sites and applications.
//
// Usage: open-turkey block info social-media
var blockInfoCmd = &cobra.Command{
	Use:   "info [name]",
	Short: "Show details of a block",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]

		database, err := openDB()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		defer database.Close()

		// GetBlock returns a pointer to a BlockDetail with all the information.
		block, err := database.GetBlock(name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		// Show the block's basic information.
		fmt.Printf("Block: %s\n", block.Name)
		fmt.Printf("Mode: %s\n", block.Mode)
		fmt.Printf("Created at: %s\n", block.CreatedAt)

		// Status line: inactive, or active with any holds.
		if block.Active {
			status := "Active"
			if block.Scheduled {
				status += " (scheduled)"
			}
			if block.Locked {
				status += fmt.Sprintf(" [Locked - %d characters]", block.LockChars)
			}
			if block.LockedUntil > time.Now().Unix() {
				status += fmt.Sprintf(" [Time-locked until %s]", time.Unix(block.LockedUntil, 0).Format("2006-01-02 15:04"))
			}
			fmt.Printf("Status: %s\n", status)
		} else {
			fmt.Printf("Status: Inactive\n")
		}

		// Show the list of blocked sites.
		// Separate with a blank line to improve readability.
		fmt.Println()
		if len(block.Sites) > 0 {
			fmt.Println("Blocked sites:")
			for _, site := range block.Sites {
				fmt.Printf("  - %s\n", site)
			}
		} else {
			fmt.Println("Blocked sites: none")
		}

		// Show the list of blocked applications.
		fmt.Println()
		if len(block.Apps) > 0 {
			fmt.Println("Blocked applications:")
			for _, app := range block.Apps {
				fmt.Printf("  - %s\n", app)
			}
		} else {
			fmt.Println("Blocked applications: none")
		}

		// Show browser patterns, if any.
		if len(block.Patterns) > 0 {
			fmt.Println()
			fmt.Println("Browser patterns:")
			for _, p := range block.Patterns {
				fmt.Printf("  - %s\n", p)
			}
		}

		// Show schedules, if any.
		if schedules, err := database.ListSchedules(name); err == nil && len(schedules) > 0 {
			fmt.Println()
			fmt.Println("Schedules:")
			for _, s := range schedules {
				fmt.Printf("  [%d] %s %s-%s\n", s.ID, scheduleFormatDays(s.DaysMask),
					scheduleFormatMinutes(s.StartMin), scheduleFormatMinutes(s.EndMin))
			}
		}
	},
}

// --------------------------------------------------------------------------
// Subcommand: block remove
// --------------------------------------------------------------------------

// blockRemoveCmd removes a block by name.
// If the block is active, the database prevents the removal —
// the user must deactivate the block before removing it.
//
// Usage: open-turkey block remove social-media
var blockRemoveCmd = &cobra.Command{
	Use:   "remove [name]",
	Short: "Remove a block",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]

		database, err := openDB()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		defer database.Close()

		if err := database.RemoveBlock(name); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Block '%s' removed successfully.\n", name)
	},
}

// --------------------------------------------------------------------------
// Command registration (init)
// --------------------------------------------------------------------------

// init() is a special Go function that runs automatically when the package
// is imported. We use it to register the subcommands in Cobra's command tree.
// Registration order doesn't matter — Cobra organizes everything internally.
//
// Resulting hierarchy:
//
//	open-turkey (rootCmd)
//	└── block (blockCmd)
//	    ├── create   (blockCreateCmd)
//	    ├── add-site (blockAddSiteCmd)
//	    ├── remove-site (blockRemoveSiteCmd)
//	    ├── add-app  (blockAddAppCmd)
//	    ├── remove-app  (blockRemoveAppCmd)
//	    ├── list     (blockListCmd)
//	    ├── info     (blockInfoCmd)
//	    └── remove   (blockRemoveCmd)
func init() {
	// Add the "block" command as a child of the root command.
	rootCmd.AddCommand(blockCmd)

	// Flags for "block create".
	blockCreateCmd.Flags().Bool("allowlist", false, "Create an allowlist block (block everything except its sites)")
	blockCreateCmd.Flags().String("template", "", "Seed the block from a built-in list (social-media, news, video, shopping)")

	// Add every subcommand as a child of "block".
	blockCmd.AddCommand(blockCreateCmd)
	blockCmd.AddCommand(blockAddSiteCmd)
	blockCmd.AddCommand(blockRemoveSiteCmd)
	blockCmd.AddCommand(blockAddAppCmd)
	blockCmd.AddCommand(blockRemoveAppCmd)
	blockCmd.AddCommand(blockListCmd)
	blockCmd.AddCommand(blockInfoCmd)
	blockCmd.AddCommand(blockRemoveCmd)
}
