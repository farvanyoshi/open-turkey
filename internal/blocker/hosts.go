// Package blocker implements site blocking at the operating system level.
//
// HOW BLOCKING VIA /etc/hosts WORKS:
//
// When you type "facebook.com" into the browser, the operating system needs to
// find out the real IP address of that server (e.g. 157.240.1.35). To do that,
// it makes a "DNS query" — basically asking a name server "what's the IP of
// facebook.com?".
//
// BUT, before making that query on the internet, Linux ALWAYS checks the
// /etc/hosts file first. That file works like a "local phone book": if the
// domain is there, the system uses the IP defined in the file and does NOT
// even query external DNS.
//
// The trick is simple: we map the domains we want to block to the address
// 0.0.0.0 (an invalid/nonexistent address). When the browser tries to reach
// the site, it gets this fake IP and the connection fails immediately.
//
// WHY 0.0.0.0 AND NOT 127.0.0.1?
// 127.0.0.1 (localhost) may have a web server running and would show a
// confusing error page. 0.0.0.0 simply doesn't exist as a destination — the
// connection fails instantly, without waiting. It's faster and cleaner.
//
// IMPORTANT: This block affects EVERY program on the computer, not just the
// browser. Apps, email clients, anything that uses DNS will be blocked.
// That's why it's such an effective method — you can't get around it by
// switching browsers.
//
// HOW WE MANAGE OUR SECTION:
// To avoid messing up the original /etc/hosts entries (which may include
// important system settings), we use special markers:
//
//	# BEGIN OPEN-TURKEY
//	0.0.0.0 facebook.com
//	0.0.0.0 www.facebook.com
//	# END OPEN-TURKEY
//
// That way, when we need to remove the block, we know exactly which lines are
// ours and which are the system's. We never touch anything outside the markers.
package blocker

import (
	"fmt"
	"os"
	"strings"
)

// hostsPath is the path of the Linux hosts file.
// This file exists on every Unix/Linux system and is read automatically by
// the operating system before any DNS query.
const hostsPath = "/etc/hosts"

// beginMarker and endMarker delimit the section Open Turkey manages.
// Everything BETWEEN these two lines was written by us and can be safely
// removed. Everything OUTSIDE these lines belongs to the system and must
// NEVER be changed.
const (
	beginMarker = "# BEGIN OPEN-TURKEY"
	endMarker   = "# END OPEN-TURKEY"
)

// ApplyHosts takes a list of domains and blocks all of them in /etc/hosts.
//
// The process is:
//  1. Read the current contents of /etc/hosts (to preserve system entries)
//  2. Remove any previous Open Turkey section (avoids duplicates)
//  3. Build a new section with every domain pointing to 0.0.0.0
//  4. Write the file back with the correct permissions (0644)
//
// For each domain, we add TWO entries:
//   - 0.0.0.0 domain.com       (version without www)
//   - 0.0.0.0 www.domain.com   (version with www)
//
// This is needed because "facebook.com" and "www.facebook.com" are treated as
// DIFFERENT domains by the system. If we only blocked one, the user could get
// in through the other.
func ApplyHosts(domains []string) error {
	// Step 1: Read the current contents of /etc/hosts.
	// We need to preserve everything that already exists (system entries like
	// "127.0.0.1 localhost") and only add/update our section.
	content, err := os.ReadFile(hostsPath)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", hostsPath, err)
	}

	// Step 2: Remove any previous Open Turkey section.
	// This ensures we won't have duplicate entries if the user changes the
	// list of blocked sites.
	cleanContent := removeOpenTurkeySection(string(content))

	// Step 3: Build the new section with the blocked domains.
	// Each domain gets two lines: one with and one without "www.".
	var section strings.Builder
	section.WriteString(beginMarker + "\n")

	for _, domain := range domains {
		// Normalize entries like "https://example.com/" or "example.com/".
		domain = NormalizeDomain(domain)
		if domain == "" {
			continue
		}

		// Always add the domain as given
		section.WriteString(fmt.Sprintf("0.0.0.0 %s\n", domain))

		// Now handle the version with/without "www.":
		// If the domain ALREADY starts with "www.", also add it without "www."
		// If it does NOT start with "www.", add the version with "www."
		// This avoids doubling "www." (e.g. "www.www.facebook.com")
		if strings.HasPrefix(domain, "www.") {
			// The user passed "www.facebook.com", so we add "facebook.com"
			withoutWWW := strings.TrimPrefix(domain, "www.")
			section.WriteString(fmt.Sprintf("0.0.0.0 %s\n", withoutWWW))
		} else {
			// The user passed "facebook.com", so we add "www.facebook.com"
			section.WriteString(fmt.Sprintf("0.0.0.0 www.%s\n", domain))
		}
	}

	section.WriteString(endMarker + "\n")

	// Step 4: Join the original content (without our old section) with the new section.
	// We make sure there's a newline between the existing content and our
	// section to keep the file organized and readable.
	finalContent := cleanContent
	if !strings.HasSuffix(finalContent, "\n") && finalContent != "" {
		finalContent += "\n"
	}
	finalContent += section.String()

	// Step 5: Write the file back.
	// Permission 0644 means:
	//   - Owner (root): can read and write (6 = 4+2)
	//   - Group: can only read (4)
	//   - Others: can only read (4)
	// This is the default /etc/hosts permission on most Linux distributions.
	if err := os.WriteFile(hostsPath, []byte(finalContent), 0644); err != nil {
		return fmt.Errorf("failed to write file %s: %w", hostsPath, err)
	}

	return nil
}

// RemoveHosts removes every Open Turkey block from the /etc/hosts file.
//
// This function is called when the block period ends or when the user
// disables the blocker. It removes ONLY the lines between our markers,
// preserving the rest of the file.
func RemoveHosts() error {
	// Step 1: Read the current contents.
	content, err := os.ReadFile(hostsPath)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", hostsPath, err)
	}

	// Step 2: Remove the Open Turkey section.
	cleanContent := removeOpenTurkeySection(string(content))

	// Step 3: Write the file back without our section.
	if err := os.WriteFile(hostsPath, []byte(cleanContent), 0644); err != nil {
		return fmt.Errorf("failed to write file %s: %w", hostsPath, err)
	}

	return nil
}

// IsHostsApplied checks whether ALL the domains in the list are blocked in /etc/hosts.
//
// Returns true ONLY if:
//  1. Open Turkey's BEGIN/END markers exist in the file
//  2. EVERY expected domain is present in the section (both with and without "www.")
//
// If ANY domain is missing, it returns false. This matters for detecting
// whether someone manually edited /etc/hosts trying to get around the block.
func IsHostsApplied(domains []string) bool {
	// Try to read the file. If it fails (e.g. no permission), we consider the
	// block NOT applied, since we have no way to confirm it.
	content, err := os.ReadFile(hostsPath)
	if err != nil {
		return false
	}

	text := string(content)

	// Check 1: Do the markers exist?
	// If not, Open Turkey never wrote to this file or someone manually
	// removed our section.
	if !strings.Contains(text, beginMarker) || !strings.Contains(text, endMarker) {
		return false
	}

	// Check 2: Extract only the contents of our section.
	// We need to check the domains INSIDE the section, not in the whole file,
	// because the user may have their own entries for these domains outside
	// our section (unlikely, but possible).
	section := extractOpenTurkeySection(text)

	// Check 3: Is every expected domain present?
	for _, domain := range domains {
		domain = NormalizeDomain(domain)
		if domain == "" {
			continue
		}

		// Check whether the line "0.0.0.0 domain" exists in the section.
		// We use the full line to avoid false positives
		// (e.g. "facebook.com.br" matching "facebook.com").
		mainEntry := fmt.Sprintf("0.0.0.0 %s", domain)
		if !strings.Contains(section, mainEntry) {
			return false
		}

		// Also check the alternate version (with or without www.)
		if strings.HasPrefix(domain, "www.") {
			withoutWWW := strings.TrimPrefix(domain, "www.")
			altEntry := fmt.Sprintf("0.0.0.0 %s", withoutWWW)
			if !strings.Contains(section, altEntry) {
				return false
			}
		} else {
			wwwEntry := fmt.Sprintf("0.0.0.0 www.%s", domain)
			if !strings.Contains(section, wwwEntry) {
				return false
			}
		}
	}

	return true
}

// removeOpenTurkeySection removes everything between the BEGIN and END markers (inclusive).
//
// This is a helper used both by ApplyHosts (to clean up before rewriting)
// and by RemoveHosts (to remove the block).
//
// The algorithm walks the file line by line:
//   - While OUTSIDE the markers, keep the line
//   - On finding BEGIN, start skipping lines
//   - On finding END, stop skipping (and skip the END line itself too)
//   - Whatever is left is the system's original content, untouched
func removeOpenTurkeySection(content string) string {
	lines := strings.Split(content, "\n")
	var result []string

	// inSection works like an "on/off switch":
	// false = we're outside the Open Turkey section (keep the line)
	// true = we're inside the section (skip the line)
	inSection := false

	for _, line := range lines {
		// Found the start of our section — from here on, skip everything
		if strings.TrimSpace(line) == beginMarker {
			inSection = true
			continue
		}

		// Found the end of our section — go back to keeping lines
		if strings.TrimSpace(line) == endMarker {
			inSection = false
			continue
		}

		// If we're outside the section, keep the line as is
		if !inSection {
			result = append(result, line)
		}
	}

	// Join the lines back into a single text.
	// Remove extra blank lines from the end to keep the file clean.
	finalText := strings.Join(result, "\n")
	finalText = strings.TrimRight(finalText, "\n")

	// If the original file wasn't empty, make sure it ends with a newline
	// (Unix convention — text files should end with a newline so tools like
	// "cat" work correctly).
	if finalText != "" {
		finalText += "\n"
	}

	return finalText
}

// extractOpenTurkeySection returns only the contents between the markers.
//
// Used by IsHostsApplied to check which domains are blocked without getting
// confused by entries outside our section.
func extractOpenTurkeySection(content string) string {
	lines := strings.Split(content, "\n")
	var section []string
	inSection := false

	for _, line := range lines {
		if strings.TrimSpace(line) == beginMarker {
			inSection = true
			continue
		}
		if strings.TrimSpace(line) == endMarker {
			break // Found the end, no need to continue
		}
		if inSection {
			section = append(section, line)
		}
	}

	return strings.Join(section, "\n")
}
