// Temporary program to normalize existing domains in the database.
// Fixes entries like "https://polymarket.com/" → "polymarket.com".
package main

import (
	"fmt"
	"os"

	"github.com/brunodcdo/open-turkey/internal/blocker"
	"github.com/brunodcdo/open-turkey/internal/db"
)

func main() {
	database, err := db.OpenDB(db.DefaultDBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer database.Close()

	sites, err := database.GetAllSitesRaw()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to fetch sites: %v\n", err)
		os.Exit(1)
	}

	for _, s := range sites {
		normalized := blocker.NormalizeDomain(s.Domain)
		if normalized != s.Domain {
			fmt.Printf("Normalizing: '%s' → '%s'\n", s.Domain, normalized)
			if err := database.UpdateSiteDomain(s.ID, normalized); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to update site %d: %v\n", s.ID, err)
				os.Exit(1)
			}
		}
	}

	fmt.Println("Domains normalized successfully.")
}
