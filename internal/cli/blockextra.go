// blockextra.go — additional block subcommands: browser patterns, mode
// switching, and JSON export/import.
//
//	open-turkey block add-pattern <block> '*casino*' '*://*/*ads*'
//	open-turkey block remove-pattern <block> '*casino*'
//	open-turkey block set-mode <block> allowlist|blocklist
//	open-turkey block export <block>            # prints JSON to stdout
//	open-turkey block import <file>             # creates a block from JSON
//
// Patterns are wildcard/keyword URL filters enforced ONLY at the browser layer
// (they can't be expressed in /etc/hosts or iptables).
package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/brunodcdo/open-turkey/internal/blocker"
	"github.com/brunodcdo/open-turkey/internal/db"
	"github.com/brunodcdo/open-turkey/internal/enforce"
	"github.com/spf13/cobra"
)

// blockExport is the JSON shape for export/import.
type blockExport struct {
	Name     string   `json:"name"`
	Mode     string   `json:"mode"`
	Sites    []string `json:"sites"`
	Apps     []string `json:"apps"`
	Patterns []string `json:"patterns"`
}

// reapplyIfActive re-applies all layers when the named block is currently
// active, so an edit takes effect immediately.
func reapplyIfActive(database *db.DB, name string) error {
	active, err := database.IsBlockActive(name)
	if err != nil {
		return err
	}
	if active {
		return enforce.ApplyFromDB(database)
	}
	return nil
}

var blockAddPatternCmd = &cobra.Command{
	Use:   "add-pattern [block] [patterns...]",
	Short: "Add browser URL patterns (wildcards/keywords) to a block",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		patterns := args[1:]
		for _, p := range patterns {
			if !blocker.IsValidPattern(p) {
				fmt.Fprintf(os.Stderr, "Error: invalid pattern %q\n", p)
				os.Exit(1)
			}
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
		if err := database.AddPatterns(name, patterns); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if err := reapplyIfActive(database, name); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Patterns added to block '%s'.\n", name)
	},
}

var blockRemovePatternCmd = &cobra.Command{
	Use:   "remove-pattern [block] [patterns...]",
	Short: "Remove browser URL patterns from a block",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		patterns := args[1:]

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
		if err := database.RemovePatterns(name, patterns); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if err := reapplyIfActive(database, name); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Patterns removed from block '%s'.\n", name)
	},
}

var blockSetModeCmd = &cobra.Command{
	Use:   "set-mode [block] [blocklist|allowlist]",
	Short: "Change a block's mode",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		name, mode := args[0], args[1]

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
		if err := database.SetBlockMode(name, mode); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if err := reapplyIfActive(database, name); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Block '%s' mode set to %s.\n", name, mode)
	},
}

var blockExportCmd = &cobra.Command{
	Use:   "export [block]",
	Short: "Print a block as JSON (redirect to a file to save it)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close()

		detail, err := database.GetBlock(args[0])
		if err != nil {
			return err
		}
		out := blockExport{
			Name:     detail.Name,
			Mode:     detail.Mode,
			Sites:    detail.Sites,
			Apps:     detail.Apps,
			Patterns: detail.Patterns,
		}
		data, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	},
}

var blockImportCmd = &cobra.Command{
	Use:   "import [file]",
	Short: "Create a block from a JSON file (from 'block export')",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		data, err := os.ReadFile(args[0])
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", args[0], err)
		}
		var in blockExport
		if err := json.Unmarshal(data, &in); err != nil {
			return fmt.Errorf("failed to parse %s: %w", args[0], err)
		}
		if in.Name == "" {
			return fmt.Errorf("import file has no block name")
		}
		if in.Mode == "" {
			in.Mode = db.ModeBlocklist
		}

		// Validate before writing anything.
		sites, err := normalizeDomains(in.Sites)
		if err != nil {
			return err
		}
		for _, p := range in.Patterns {
			if !blocker.IsValidPattern(p) {
				return fmt.Errorf("invalid pattern %q in import", p)
			}
		}

		database, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close()

		if err := database.CreateBlock(in.Name, in.Mode); err != nil {
			return err
		}
		if len(sites) > 0 {
			if err := database.AddSites(in.Name, sites); err != nil {
				return err
			}
		}
		if len(in.Apps) > 0 {
			if err := database.AddApps(in.Name, in.Apps); err != nil {
				return err
			}
		}
		if len(in.Patterns) > 0 {
			if err := database.AddPatterns(in.Name, in.Patterns); err != nil {
				return err
			}
		}
		fmt.Printf("Imported block '%s' (%d sites, %d apps, %d patterns).\n",
			in.Name, len(sites), len(in.Apps), len(in.Patterns))
		return nil
	},
}

func init() {
	blockCmd.AddCommand(blockAddPatternCmd)
	blockCmd.AddCommand(blockRemovePatternCmd)
	blockCmd.AddCommand(blockSetModeCmd)
	blockCmd.AddCommand(blockExportCmd)
	blockCmd.AddCommand(blockImportCmd)
}
