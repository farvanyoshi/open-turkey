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
//  1. Normalize and validate the domains (invalid entries are dropped)
//  2. Read the current contents of /etc/hosts (to preserve system entries)
//  3. Remove any previous Open Turkey section (avoids duplicates)
//  4. Build a new section with every domain pointing to 0.0.0.0
//  5. Write the file back atomically with the correct permissions (0644)
//
// If no valid domains remain, ApplyHosts removes our section entirely (there
// is nothing to block), which keeps callers from writing an empty marker block.
//
// For each domain, we add TWO entries:
//   - 0.0.0.0 domain.com       (version without www)
//   - 0.0.0.0 www.domain.com   (version with www)
//
// This is needed because "facebook.com" and "www.facebook.com" are treated as
// DIFFERENT domains by the system. If we only blocked one, the user could get
// in through the other.
func ApplyHosts(domains []string) error {
	valid := ValidDomains(domains)
	if len(valid) == 0 {
		// Nothing to block — make sure our section is gone rather than
		// writing an empty marker block.
		return RemoveHosts()
	}

	// Read the current contents of /etc/hosts. We need to preserve everything
	// that already exists (system entries like "127.0.0.1 localhost") and only
	// add/update our section.
	content, err := os.ReadFile(hostsPath)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", hostsPath, err)
	}

	// Remove any previous Open Turkey section so we don't leave duplicates
	// when the list of blocked sites changes.
	cleanContent := removeOpenTurkeySection(string(content))

	// Build the new section with the blocked domains.
	var section strings.Builder
	section.WriteString(beginMarker + "\n")

	for _, domain := range valid {
		// Always add the domain as given.
		section.WriteString(fmt.Sprintf("0.0.0.0 %s\n", domain))

		// Add the version with/without "www." so both forms are blocked,
		// without doubling "www." (e.g. "www.www.facebook.com").
		if strings.HasPrefix(domain, "www.") {
			withoutWWW := strings.TrimPrefix(domain, "www.")
			section.WriteString(fmt.Sprintf("0.0.0.0 %s\n", withoutWWW))
		} else {
			section.WriteString(fmt.Sprintf("0.0.0.0 www.%s\n", domain))
		}
	}

	section.WriteString(endMarker + "\n")

	// Join the original content (without our old section) with the new one,
	// keeping a newline between them.
	finalContent := cleanContent
	if !strings.HasSuffix(finalContent, "\n") && finalContent != "" {
		finalContent += "\n"
	}
	finalContent += section.String()

	// Write atomically (temp file + rename) so a crash can never leave
	// /etc/hosts truncated or half-written. Permission 0644 matches the
	// distribution default.
	if err := atomicWriteFile(hostsPath, []byte(finalContent), 0644); err != nil {
		return fmt.Errorf("failed to write file %s: %w", hostsPath, err)
	}

	return nil
}

// RemoveHosts removes every Open Turkey block from the /etc/hosts file.
//
// It is a no-op when our section isn't present, so the daemon can call it on
// every idle tick without rewriting the file.
func RemoveHosts() error {
	content, err := os.ReadFile(hostsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read file %s: %w", hostsPath, err)
	}

	text := string(content)
	// Nothing of ours in the file — don't rewrite it.
	if !strings.Contains(text, beginMarker) && !strings.Contains(text, endMarker) {
		return nil
	}

	cleanContent := removeOpenTurkeySection(text)

	if err := atomicWriteFile(hostsPath, []byte(cleanContent), 0644); err != nil {
		return fmt.Errorf("failed to write file %s: %w", hostsPath, err)
	}

	return nil
}

// HostsSectionPresent reports whether our BEGIN/END markers exist in
// /etc/hosts. The daemon uses it to decide whether an idle cleanup is needed.
func HostsSectionPresent() bool {
	content, err := os.ReadFile(hostsPath)
	if err != nil {
		return false
	}
	text := string(content)
	return strings.Contains(text, beginMarker) || strings.Contains(text, endMarker)
}

// IsHostsApplied checks whether ALL the given domains are blocked in /etc/hosts.
//
// Returns true ONLY if:
//  1. Open Turkey's BEGIN/END markers exist in the file
//  2. EVERY expected domain is present in the section (both with and without "www.")
//     with the exact "0.0.0.0 <domain>" mapping
//
// If ANY domain is missing, or its address was changed (e.g. someone edited
// "0.0.0.0 facebook.com" to point at a real IP), it returns false so the
// daemon reapplies.
func IsHostsApplied(domains []string) bool {
	valid := ValidDomains(domains)
	if len(valid) == 0 {
		// No domains to enforce: "applied" means our section is absent.
		return !HostsSectionPresent()
	}

	content, err := os.ReadFile(hostsPath)
	if err != nil {
		return false
	}

	text := string(content)

	// The markers must exist.
	if !strings.Contains(text, beginMarker) || !strings.Contains(text, endMarker) {
		return false
	}

	// Check the domains INSIDE our section only.
	section := extractOpenTurkeySection(text)

	for _, domain := range valid {
		// The exact "0.0.0.0 domain" line must be present. Using the full
		// line avoids false positives (e.g. "facebook.com.br" matching
		// "facebook.com") and catches an address that was tampered with.
		mainEntry := fmt.Sprintf("0.0.0.0 %s", domain)
		if !containsLine(section, mainEntry) {
			return false
		}

		if strings.HasPrefix(domain, "www.") {
			withoutWWW := strings.TrimPrefix(domain, "www.")
			if !containsLine(section, fmt.Sprintf("0.0.0.0 %s", withoutWWW)) {
				return false
			}
		} else {
			if !containsLine(section, fmt.Sprintf("0.0.0.0 www.%s", domain)) {
				return false
			}
		}
	}

	return true
}

// containsLine reports whether text has a line equal to want (ignoring
// surrounding whitespace). This is stricter than strings.Contains: it won't be
// fooled by "0.0.0.0 facebook.com.evil.test" when looking for
// "0.0.0.0 facebook.com".
func containsLine(text, want string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}

// removeOpenTurkeySection removes everything between the BEGIN and END markers (inclusive).
//
// This is a helper used both by ApplyHosts (to clean up before rewriting)
// and by RemoveHosts (to remove the block).
func removeOpenTurkeySection(content string) string {
	lines := strings.Split(content, "\n")
	var result []string

	inSection := false
	for _, line := range lines {
		if strings.TrimSpace(line) == beginMarker {
			inSection = true
			continue
		}
		if strings.TrimSpace(line) == endMarker {
			inSection = false
			continue
		}
		if !inSection {
			result = append(result, line)
		}
	}

	finalText := strings.Join(result, "\n")
	finalText = strings.TrimRight(finalText, "\n")
	if finalText != "" {
		finalText += "\n"
	}
	return finalText
}

// extractOpenTurkeySection returns only the contents between the markers.
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
			break
		}
		if inSection {
			section = append(section, line)
		}
	}

	return strings.Join(section, "\n")
}
