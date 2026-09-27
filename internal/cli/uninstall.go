// uninstall.go — the "prepare-uninstall" guard command.
//
// WHY THIS EXISTS
// ---------------
// Two problems with a naive uninstall:
//
//  1. It was an easy escape hatch. "sudo make uninstall" would remove the
//     daemon while a locked block was active, sidestepping the typing
//     challenge entirely.
//
//  2. It left the system broken. Uninstalling removed the binary but never
//     cleaned the blocking layers, so /etc/hosts entries, firewall rules and
//     browser policies stayed in place with no tool left to remove them.
//
// The Makefile's uninstall target runs this command FIRST. It:
//   - refuses (non-zero exit) if any locked block is active, so uninstalling
//     can't bypass a lock; and
//   - otherwise deactivates all blocks and removes every blocking layer, so the
//     system is left clean.
//
// It's hidden because users don't call it directly — "make uninstall" does.
package cli

import (
	"fmt"
	"os"

	"github.com/brunodcdo/open-turkey/internal/blocker"
	"github.com/spf13/cobra"
)

var prepareUninstallCmd = &cobra.Command{
	Use:    "prepare-uninstall",
	Short:  "Internal: refuse if locked, otherwise clean all layers before uninstall",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		database, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close()

		active, err := database.GetActiveBlocks()
		if err != nil {
			return err
		}

		// Refuse while any block is locked — uninstalling must not be a way
		// around the lock.
		var locked []string
		for _, b := range active {
			if b.Locked {
				locked = append(locked, b.BlockName)
			}
		}
		if len(locked) > 0 {
			return fmt.Errorf("cannot uninstall: locked block(s) still active: %v. Unlock them first with 'open-turkey unlock <block>'", locked)
		}

		// Deactivate everything so the daemon (and this command) leave nothing
		// enforced.
		for _, b := range active {
			if err := database.DeactivateBlock(b.BlockName); err != nil {
				return fmt.Errorf("failed to deactivate block '%s': %w", b.BlockName, err)
			}
		}

		// Remove every blocking layer so the system is left clean.
		if err := blocker.RemoveHosts(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to clean /etc/hosts: %v\n", err)
		}
		if err := blocker.RemoveFirewall(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to clean firewall: %v\n", err)
		}
		if err := blocker.RemoveBrowserPolicies(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to clean browser policies: %v\n", err)
		}

		fmt.Println("Open Turkey blocking layers removed; ready to uninstall.")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(prepareUninstallCmd)
}
