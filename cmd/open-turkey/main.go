// Package main is the entry point of Open Turkey — a command-line
// productivity blocker for Linux.
//
// Why is main.go so lean?
// -----------------------
// We follow the principle of separation of concerns. main.go's only job is
// to initialize the application. All the command, flag and execution logic
// lives inside the internal packages (internal/), which has a few advantages:
//
//   - Testability: internal packages can be tested in isolation,
//     without having to run the whole binary.
//   - Organization: each package handles a single responsibility.
//   - Encapsulation: the internal/ directory ensures other external modules
//     don't import our internal logic by accident.
//
// The main() function just delegates execution to cli.Execute(), which is
// responsible for configuring and running the root command using the Cobra
// pattern.
package main

import (
	"github.com/brunodcdo/open-turkey/internal/cli"
)

// main is the application's entry point. We call cli.Execute(), which
// initializes Cobra's root command and processes the command-line arguments.
// If there's an error, Cobra itself takes care of showing the error message
// and exiting the process with the appropriate exit code.
func main() {
	cli.Execute()
}
