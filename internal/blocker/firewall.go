// =============================================================================
// Package blocker — Open Turkey's firewall module
// =============================================================================
//
// This file implements site blocking using iptables, the firewall built into
// the Linux kernel. Let's see how it works:
//
// WHAT IS IPTABLES?
// -----------------
// iptables is a tool that controls network traffic on Linux. It works like a
// "doorman" that decides which data packets may enter or leave your computer.
// Think of it as a nightclub bouncer: it has a list and only lets through
// those who are allowed.
//
// HOW DO WE ORGANIZE THE BLOCK?
// -----------------------------
// iptables organizes its rules into "tables" and "chains". We use the default
// table (filter) and create a custom chain called "OPEN-TURKEY". This is like
// creating a separate list just for our app, instead of mixing our rules with
// the system's.
//
// The OUTPUT chain controls everything that LEAVES the computer. When you try
// to access a site, your computer sends packets out — and that's where we
// intercept. We insert a "jump" from the OUTPUT chain to our OPEN-TURKEY
// chain, so that all outgoing traffic goes through our rules first.
//
// WHAT DOES "DROP" MEAN?
// ----------------------
// When a rule has the DROP target, the packet is silently discarded. The
// computer doesn't even tell the server it tried to connect. From the user's
// point of view, the site simply "doesn't load" — it waits until it times
// out. This differs from REJECT, which would reply with an error.
// We use DROP because it's harder to notice and work around.
//
// WHY DO WE BLOCK DNS-OVER-HTTPS (DoH)?
// -------------------------------------
// When we block a site via /etc/hosts (another module), the browser uses the
// system DNS to resolve names. But modern browsers like Chrome and Firefox can
// use DNS-over-HTTPS, which is a way of querying DNS directly over HTTPS,
// bypassing /etc/hosts. To prevent that, we block access to the best-known
// DoH servers (Cloudflare, Google, Quad9) on port 443 (HTTPS).
//
// LIMITATION: IPv4 ONLY
// ---------------------
// This module only uses the "iptables" command, which controls only IPv4
// traffic (addresses like 192.168.1.1). Blocking IPv6 (addresses like
// 2001:db8::1) would require "ip6tables".
// For simplicity, we don't implement ip6tables in this version.
// In practice, most sites still work over IPv4, but an advanced user could
// get around the block using IPv6.
// A future version should add ip6tables support.
//
// =============================================================================
package blocker

import (
	"bytes"
	"fmt"
	"net"
	"os/exec"
	"strings"
)

// chainName is the name of our custom iptables chain.
// We use a unique, descriptive name so that anyone inspecting the iptables
// rules knows these rules belong to Open Turkey. Hyphens are allowed in chain
// names.
const chainName = "OPEN-TURKEY"

// dohServersIPv4 contains the IPv4 addresses of the most widely used
// DNS-over-HTTPS servers. We block these IPs on port 443 (HTTPS) to stop
// browsers from making encrypted DNS queries, which would get around our
// /etc/hosts block.
//
// Why these ones specifically?
// - Cloudflare (1.1.1.1, 1.0.0.1): used by Firefox by default
// - Google (8.8.8.8, 8.8.4.4): used by Chrome and Android
// - Quad9 (9.9.9.9, 149.112.112.112): a popular security-focused alternative
//
// Note: these providers' IPv6 addresses (like 2606:4700:4700::1111) are NOT
// blocked here because we only use iptables (IPv4). See the limitation
// documented in the file header.
var dohServersIPv4 = []string{
	// Cloudflare — the world's fastest DNS provider, Firefox's default
	"1.1.1.1",
	"1.0.0.1",

	// Google — the world's most popular DNS provider
	"8.8.8.8",
	"8.8.4.4",

	// Quad9 — a security- and privacy-focused provider
	"9.9.9.9",
	"149.112.112.112",
}

// =============================================================================
// Public functions — the interface the rest of Open Turkey uses
// =============================================================================

// ApplyFirewall configures the firewall to block the given domains.
//
// Step by step:
//  1. Create the OPEN-TURKEY chain (if it doesn't already exist)
//  2. Flush all previous rules from the chain (to start over from scratch)
//  3. Ensure the OUTPUT chain "jumps" to our chain
//  4. For each domain, resolve its IPs and add DROP rules
//  5. Block the known DoH servers
//
// Why flush and recreate? Because the list of blocked sites can change.
// It's simpler and safer to recreate every rule than to try to compute the
// difference between the current and desired state.
func ApplyFirewall(domains []string) error {
	// --- Step 1: Create the custom chain ---
	// The -N argument creates a new chain. If it already exists, iptables
	// returns an error, but that's fine — we silently ignore that error.
	// It's like trying to create a folder that already exists: no harm done.
	_ = runIptables("-N", chainName)

	// --- Step 2: Flush existing rules ---
	// The -F (flush) argument removes every rule from the chain, but keeps
	// the chain itself. We do this to make sure we start from a clean state
	// before adding the new rules.
	if err := runIptables("-F", chainName); err != nil {
		return fmt.Errorf("failed to flush chain %s: %w", chainName, err)
	}

	// --- Step 3: Insert the jump from OUTPUT to our chain ---
	// First we check whether the jump already exists using -C (check).
	// If not, we insert it with -I (insert at the top, not append).
	// We use -I instead of -A so that our rule is evaluated BEFORE any other
	// rule in the OUTPUT chain. That ensures our block takes priority.
	if err := runIptables("-C", "OUTPUT", "-j", chainName); err != nil {
		// The jump rule doesn't exist yet — create it
		if err := runIptables("-I", "OUTPUT", "-j", chainName); err != nil {
			return fmt.Errorf("failed to insert jump to chain %s in OUTPUT: %w", chainName, err)
		}
	}

	// --- Step 4: Resolve domains and add DROP rules ---
	// For each domain (e.g. "youtube.com"), we need to find out which IP
	// addresses it uses. A domain may have several IPs (e.g. Google has
	// dozens). We block all of them.
	for _, domain := range domains {
		domain = NormalizeDomain(domain)
		if domain == "" {
			continue
		}

		// net.LookupHost performs a DNS query and returns every IP associated
		// with the domain. It's equivalent to running "nslookup" or "dig" in
		// the terminal.
		ips, err := net.LookupHost(domain)
		if err != nil {
			// If we can't resolve the domain, it's not a fatal error.
			// The domain may be temporarily down, or it may be an invalid
			// domain. We just skip it and continue with the others.
			// In a production app, it would be good to log this for debugging.
			continue
		}

		for _, ip := range ips {
			// Filter to IPv4 addresses only. IPv6 addresses contain ":"
			// (e.g. "2607:f8b0:4004::64") while IPv4 ones don't (e.g. "142.250.80.46").
			// Since we only use iptables (not ip6tables), we ignore IPv6.
			if strings.Contains(ip, ":") {
				continue
			}

			// -A appends the rule to the end of the chain
			// -d specifies the destination IP
			// -j DROP means "silently discard the packet"
			//
			// In plain language: "every packet leaving the computer destined
			// for IP X must be discarded"
			if err := runIptables("-A", chainName, "-d", ip, "-j", "DROP"); err != nil {
				return fmt.Errorf("failed to block IP %s for domain %s: %w", ip, domain, err)
			}
		}
	}

	// --- Step 5: Block DNS-over-HTTPS servers ---
	if err := BlockDoH(); err != nil {
		return fmt.Errorf("failed to block DoH servers: %w", err)
	}

	return nil
}

// RemoveFirewall completely undoes the firewall block.
//
// Order matters here! We need to:
//  1. First remove the jump from OUTPUT (otherwise OUTPUT points to a nonexistent chain)
//  2. Then flush the rules in our chain
//  3. Finally, delete the chain itself
//
// iptables doesn't allow deleting a chain that still has rules or that is
// referenced by another chain. That's why the order is crucial.
//
// This function is idempotent: it can be called several times without error,
// even if the firewall was already removed. That's important for robustness —
// if the program crashes, we can call RemoveFirewall on the next run without
// worrying about the previous state.
func RemoveFirewall() error {
	// --- Step 1: Remove the jump from OUTPUT to our chain ---
	// -D (delete) removes the specified rule. If the rule doesn't exist,
	// iptables returns an error, but we ignore it — the firewall may have
	// already been removed before.
	_ = runIptables("-D", "OUTPUT", "-j", chainName)

	// --- Step 2: Flush every rule in the chain ---
	// We need to do this BEFORE deleting the chain, because iptables refuses
	// to delete a chain that still contains rules.
	_ = runIptables("-F", chainName)

	// --- Step 3: Delete the chain ---
	// -X deletes a custom chain. Built-in chains (INPUT, OUTPUT, FORWARD)
	// can't be deleted, but our OPEN-TURKEY chain can.
	_ = runIptables("-X", chainName)

	// We don't return errors because any failure here most likely means the
	// chain no longer existed, which is the desired state.
	return nil
}

// IsFirewallApplied checks whether Open Turkey's firewall is active.
//
// For the firewall to count as "applied", two conditions must hold:
//  1. The OPEN-TURKEY chain must exist
//  2. The chain must contain at least one rule (not be empty)
//
// An empty chain would mean we created the structure but blocked nothing —
// that doesn't count as an active firewall.
func IsFirewallApplied() bool {
	// -L lists the rules of a specific chain. If the chain doesn't exist,
	// the command fails. We use -n to avoid resolving IPs to names (faster).
	output, err := runIptablesOutput("-L", chainName, "-n")
	if err != nil {
		// On error, the chain probably doesn't exist
		return false
	}

	// The output of "iptables -L CHAIN" always starts with 2 header lines:
	//   Chain OPEN-TURKEY (1 references)
	//   target     prot opt source               destination
	//
	// If there are rules, they appear from the 3rd line onward.
	// So if we have more than 2 lines, the chain has rules.
	lines := strings.Split(strings.TrimSpace(output), "\n")
	return len(lines) > 2
}

// BlockDoH blocks access to the best-known DNS-over-HTTPS servers.
//
// DNS-over-HTTPS (DoH) is a protocol that allows DNS queries over HTTPS
// (port 443). Modern browsers use it to "escape" the operating system's DNS,
// which would defeat our /etc/hosts block.
//
// Here we specifically block port 443 (HTTPS) on these servers.
// We don't block ALL traffic to these IPs because that would break normal DNS
// resolution (port 53), which is still needed for the sites that are NOT
// blocked to work.
//
// WARNING: Blocking Google's (8.8.8.8) and Cloudflare's (1.1.1.1) DNS may
// cause side effects if the user uses them as their default DNS.
// However, since we only block port 443 (HTTPS), normal DNS resolution on
// port 53 (UDP/TCP) keeps working.
func BlockDoH() error {
	for _, ip := range dohServersIPv4 {
		// -A OPEN-TURKEY: append to our chain
		// -d <ip>: destination is the DoH server
		// -p tcp: TCP protocol (HTTPS uses TCP)
		// --dport 443: destination port 443 (HTTPS)
		// -j DROP: silently discard
		//
		// In plain language: "if the computer tries to reach this IP on
		// port 443 over TCP, discard the packet"
		err := runIptables("-A", chainName, "-d", ip, "-p", "tcp", "--dport", "443", "-j", "DROP")
		if err != nil {
			return fmt.Errorf("failed to block DoH server %s: %w", ip, err)
		}
	}

	return nil
}

// =============================================================================
// Helper functions — internal to the package
// =============================================================================

// runIptables runs an iptables command with the given arguments.
//
// Why wrap this in a separate function?
//  1. Avoids code repetition (DRY - Don't Repeat Yourself)
//  2. Centralizes error handling
//  3. Captures the error output (stderr) for more useful messages
//
// iptables requires root (superuser) permissions to work.
// If Open Turkey isn't running as root, every command will fail. The Open
// Turkey daemon must be started with sudo.
func runIptables(args ...string) error {
	cmd := exec.Command("iptables", args...)

	// We capture stderr because that's where iptables writes its error
	// messages. stdout is usually empty for commands that modify rules.
	// Without capturing stderr, we'd only know that something failed, not why.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// Build an error message that includes:
		// - The full command that was run (for debugging)
		// - iptables' error message (stderr)
		// - Go's error (exit code, etc.)
		stderrStr := strings.TrimSpace(stderr.String())
		if stderrStr != "" {
			return fmt.Errorf(
				"failed to run iptables %s: %s (error: %w)",
				strings.Join(args, " "),
				stderrStr,
				err,
			)
		}
		return fmt.Errorf("failed to run iptables %s: %w", strings.Join(args, " "), err)
	}

	return nil
}

// runIptablesOutput runs an iptables command and returns its standard output (stdout).
//
// This variant is used when we need to READ information from iptables, such
// as listing existing rules. The difference from runIptables is that here we
// capture and return stdout instead of just checking for an error.
func runIptablesOutput(args ...string) (string, error) {
	cmd := exec.Command("iptables", args...)

	// CombinedOutput captures stdout and stderr together.
	// We use it because on error we want the stderr message, and on success
	// we want stdout with the rule listing.
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf(
			"failed to run iptables %s: %s (error: %w)",
			strings.Join(args, " "),
			strings.TrimSpace(string(output)),
			err,
		)
	}

	return string(output), nil
}
