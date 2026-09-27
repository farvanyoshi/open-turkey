// Package cli defines Open Turkey's command-line commands using Cobra.
//
// Cobra is a popular Go library for building CLIs. It organizes the
// application into "commands" that can have subcommands, flags and arguments.
// For example: "open-turkey block create social-media" has:
//   - "open-turkey" as the root command
//   - "block" as a subcommand
//   - "create" as a sub-subcommand
//   - "social-media" as an argument
//
// This file defines the root command and the Execute() function that starts
// everything.
package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// rootCmd is the application's root command. Every other command (block,
// start, etc.) is added as a child of this command. When the user types
// "open-turkey" with no arguments, Cobra shows this command's help
// automatically.
var rootCmd = &cobra.Command{
	Use:   "open-turkey",
	Short: "A command-line productivity blocker for Linux",
}

// Execute initializes and runs Cobra's root command.
// This function is called by main.go — it's the CLI's entry point.
// If any command returns an error, we print the message to stderr
// and exit the process with exit code 1.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
