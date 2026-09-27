// =============================================================================
// Package blocker — Open Turkey's browser policy module
// =============================================================================
//
// This file implements site blocking using browser "enterprise policies" for
// Firefox and the Chromium family (Chromium, Chrome, Brave, Edge, Opera,
// Vivaldi).
//
// WHAT ARE BROWSER POLICIES?
// --------------------------
// Browsers meant for managed/enterprise environments read "policy" files that
// the OS administrator controls. Because the OS enforces them, the user cannot
// disable them from inside the browser — not through settings, not in private
// mode, not by removing an extension. Removing a policy requires root, which
// (in Open Turkey's model) only the daemon has.
//
// WHAT WE SET
// -----------
//   - A URL blocklist (Chromium: "URLBlocklist"; Firefox: "WebsiteFilter") for
//     the blocked domains.
//   - DNS-over-HTTPS turned off (Chromium: "DnsOverHttpsMode":"off" +
//     "BuiltInDnsClientEnabled":false; Firefox: "DNSOverHTTPS" disabled and
//     locked). Without this, the browser could resolve names over HTTPS and
//     skip /etc/hosts.
//
// FILE LAYOUT
// -----------
// Firefox uses a single shared policies.json, so we MERGE our keys and remove
// only our keys on cleanup. Each Chromium-family browser reads every .json in
// its managed/ directory, so we drop our own open-turkey.json per browser and
// simply delete it on cleanup.
//
// =============================================================================
package blocker

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// firefoxPolicyPath is Firefox's shared policy file.
const firefoxPolicyPath = "/etc/firefox/policies/policies.json"

// chromiumPolicyPaths lists the managed-policy file we write for each
// Chromium-family browser. They share the same URLBlocklist format but each
// reads a different directory. Writing a file whose directory doesn't exist
// yet is fine — os.MkdirAll creates it, so the policy is ready if the user
// installs that browser later.
var chromiumPolicyPaths = []string{
	"/etc/chromium/policies/managed/open-turkey.json",   // Chromium
	"/etc/opt/chrome/policies/managed/open-turkey.json", // Google Chrome
	"/etc/brave/policies/managed/open-turkey.json",      // Brave
	"/etc/opt/edge/policies/managed/open-turkey.json",   // Microsoft Edge
	"/etc/opt/opera/policies/managed/open-turkey.json",  // Opera
	"/etc/vivaldi/policies/managed/open-turkey.json",    // Vivaldi
}

// =============================================================================
// Public functions
// =============================================================================

// ApplyBrowserPolicies writes the blocking policies for every browser.
//
// Firefox and Chromium use DIFFERENT pattern formats (treating them the same
// once let subdomains through), so we build two sets:
//
//   - Firefox (WebsiteFilter): match patterns like "*://*.domain.com/*".
//   - Chromium/Chrome/Brave/Edge/Opera/Vivaldi (URLBlocklist): a bare
//     "domain.com" filter, which already matches the root, all subdomains and
//     all paths. Wildcards are invalid there and are silently dropped.
//     Ref.: https://www.chromium.org/administrators/url-blocklist-filter-format/
//
// With no valid domains, the policies are removed instead of written empty, so
// an apps-only block leaves no policy files behind.
func ApplyBrowserPolicies(domains []string) error {
	valid := ValidDomains(domains)
	if len(valid) == 0 {
		return RemoveBrowserPolicies()
	}

	ffPatterns := firefoxPatterns(valid)
	crFilters := chromiumFilters(valid)

	if err := applyFirefoxPolicy(ffPatterns); err != nil {
		return fmt.Errorf("failed to apply Firefox policy: %w", err)
	}

	for _, path := range chromiumPolicyPaths {
		if err := applyChromiumPolicy(path, crFilters); err != nil {
			return fmt.Errorf("failed to apply policy at %s: %w", path, err)
		}
	}

	return nil
}

// RemoveBrowserPolicies removes our blocking policies from every browser.
//
// Firefox's file may hold other policies, so we strip only our keys (and
// delete the file if nothing else remains). Each Chromium file is ours alone,
// so we delete it.
func RemoveBrowserPolicies() error {
	if err := removeFirefoxPolicy(); err != nil {
		return fmt.Errorf("failed to remove Firefox policy: %w", err)
	}
	for _, path := range chromiumPolicyPaths {
		if err := removePolicyFile(path); err != nil {
			return fmt.Errorf("failed to remove policy at %s: %w", path, err)
		}
	}
	return nil
}

// IsBrowserPoliciesApplied reports whether the policies are correct for every
// browser whose policy directory exists. It is strict: any missing filter, any
// tampered blocklist, or a missing DoH-off setting makes it return false so the
// daemon reapplies. With no domains, "applied" means our policies are absent.
func IsBrowserPoliciesApplied(domains []string) bool {
	valid := ValidDomains(domains)
	if len(valid) == 0 {
		return !BrowserPoliciesPresent()
	}

	ffPatterns := firefoxPatterns(valid)
	crFilters := chromiumFilters(valid)

	if dirExists(filepath.Dir(firefoxPolicyPath)) {
		if !checkFirefoxPolicy(ffPatterns) {
			return false
		}
	}

	for _, path := range chromiumPolicyPaths {
		if dirExists(filepath.Dir(path)) {
			if !checkChromiumPolicy(path, crFilters) {
				return false
			}
		}
	}

	return true
}

// BrowserPoliciesPresent reports whether any of our policy artifacts still
// exist (any Chromium file, or Firefox's WebsiteFilter key). The daemon uses it
// to decide whether an idle cleanup is needed.
func BrowserPoliciesPresent() bool {
	for _, path := range chromiumPolicyPaths {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}

	content, err := os.ReadFile(firefoxPolicyPath)
	if err != nil {
		return false
	}
	var root map[string]interface{}
	if json.Unmarshal(content, &root) != nil {
		return false
	}
	policies, ok := root["policies"].(map[string]interface{})
	if !ok {
		return false
	}
	_, hasFilter := policies["WebsiteFilter"]
	_, hasDoH := policies["DNSOverHTTPS"]
	return hasFilter || hasDoH
}

// =============================================================================
// URL pattern generation
// =============================================================================

// stripWWWPrefix removes a leading "www." from a host, but only when what's
// left still has at least two labels, so a single-label registrable domain
// like "www.com" is never reduced to the whole ".com" TLD.
func stripWWWPrefix(host string) string {
	if len(host) > 4 && host[:4] == "www." {
		rest := host[4:]
		for i := 0; i < len(rest); i++ {
			if rest[i] == '.' {
				return rest
			}
		}
	}
	return host
}

// firefoxPatterns builds Firefox WebsiteFilter match patterns: for each domain,
// "*://domain/*" (root) and "*://*.base/*" (subdomains). Deduplicated, sorted.
func firefoxPatterns(domains []string) []string {
	set := make(map[string]bool)
	for _, host := range domains {
		if host == "" {
			continue
		}
		set[fmt.Sprintf("*://%s/*", host)] = true
		set[fmt.Sprintf("*://*.%s/*", stripWWWPrefix(host))] = true
	}
	patterns := make([]string, 0, len(set))
	for p := range set {
		patterns = append(patterns, p)
	}
	sort.Strings(patterns)
	return patterns
}

// chromiumFilters builds URLBlocklist filters: one bare host per domain (which
// covers root, subdomains and paths). Deduplicated, sorted.
func chromiumFilters(domains []string) []string {
	set := make(map[string]bool)
	for _, host := range domains {
		if host == "" {
			continue
		}
		set[stripWWWPrefix(host)] = true
	}
	filters := make([]string, 0, len(set))
	for f := range set {
		filters = append(filters, f)
	}
	sort.Strings(filters)
	return filters
}

// =============================================================================
// Firefox
// =============================================================================

// applyFirefoxPolicy writes our keys into Firefox's policies.json, preserving
// any unrelated policies already present. We overwrite (not merge) our own
// WebsiteFilter.Block so that removing a domain actually unblocks it, and we
// force DNS-over-HTTPS off and locked.
func applyFirefoxPolicy(patterns []string) error {
	dir := filepath.Dir(firefoxPolicyPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	root := make(map[string]interface{})
	if content, err := os.ReadFile(firefoxPolicyPath); err == nil && len(content) > 0 {
		if json.Unmarshal(content, &root) != nil {
			root = make(map[string]interface{})
		}
	}

	policies, ok := root["policies"].(map[string]interface{})
	if !ok {
		policies = make(map[string]interface{})
	}

	websiteFilter, ok := policies["WebsiteFilter"].(map[string]interface{})
	if !ok {
		websiteFilter = make(map[string]interface{})
	}
	blocks := make([]string, 0, len(patterns))
	blocks = append(blocks, patterns...)
	websiteFilter["Block"] = blocks
	policies["WebsiteFilter"] = websiteFilter

	// Turn DNS-over-HTTPS off and lock it so it can't be re-enabled to bypass
	// /etc/hosts.
	policies["DNSOverHTTPS"] = map[string]interface{}{
		"Enabled": false,
		"Locked":  true,
	}

	root["policies"] = policies
	return writeJSON(firefoxPolicyPath, root)
}

// removeFirefoxPolicy strips only our keys (WebsiteFilter, DNSOverHTTPS) from
// Firefox's policies.json, deleting the file if nothing else remains.
func removeFirefoxPolicy() error {
	content, err := os.ReadFile(firefoxPolicyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read %s: %w", firefoxPolicyPath, err)
	}

	var root map[string]interface{}
	if json.Unmarshal(content, &root) != nil {
		return os.Remove(firefoxPolicyPath)
	}

	policies, ok := root["policies"].(map[string]interface{})
	if !ok {
		return nil
	}

	delete(policies, "WebsiteFilter")
	delete(policies, "DNSOverHTTPS")

	if len(policies) == 0 {
		if err := os.Remove(firefoxPolicyPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove %s: %w", firefoxPolicyPath, err)
		}
		return nil
	}

	root["policies"] = policies
	return writeJSON(firefoxPolicyPath, root)
}

// checkFirefoxPolicy verifies WebsiteFilter.Block is exactly the expected set
// and that DNS-over-HTTPS is present and disabled.
func checkFirefoxPolicy(expected []string) bool {
	content, err := os.ReadFile(firefoxPolicyPath)
	if err != nil {
		return false
	}
	var root map[string]interface{}
	if json.Unmarshal(content, &root) != nil {
		return false
	}
	policies, ok := root["policies"].(map[string]interface{})
	if !ok {
		return false
	}
	websiteFilter, ok := policies["WebsiteFilter"].(map[string]interface{})
	if !ok {
		return false
	}
	if !sameSet(stringsFromInterface(websiteFilter["Block"]), expected) {
		return false
	}

	doh, ok := policies["DNSOverHTTPS"].(map[string]interface{})
	if !ok {
		return false
	}
	enabled, ok := doh["Enabled"].(bool)
	return ok && !enabled
}

// =============================================================================
// Chromium family
// =============================================================================

// applyChromiumPolicy writes our managed-policy file for one Chromium-family
// browser: the URL blocklist plus DNS-over-HTTPS turned off.
func applyChromiumPolicy(path string, filters []string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}
	policy := map[string]interface{}{
		"URLBlocklist":            filters,
		"DnsOverHttpsMode":        "off",
		"BuiltInDnsClientEnabled": false,
	}
	return writeJSON(path, policy)
}

// checkChromiumPolicy verifies URLBlocklist is exactly the expected set and
// that DNS-over-HTTPS is off.
func checkChromiumPolicy(path string, expected []string) bool {
	content, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var policy map[string]interface{}
	if json.Unmarshal(content, &policy) != nil {
		return false
	}
	if !sameSet(stringsFromInterface(policy["URLBlocklist"]), expected) {
		return false
	}
	mode, ok := policy["DnsOverHttpsMode"].(string)
	return ok && mode == "off"
}

// =============================================================================
// Generic helpers
// =============================================================================

// removePolicyFile deletes a policy file, treating "already gone" as success.
func removePolicyFile(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove %s: %w", path, err)
	}
	return nil
}

// writeJSON marshals data to indented JSON and writes it atomically with mode
// 0644 (browsers run as an ordinary user and must be able to read it; only
// root may modify it).
func writeJSON(path string, data interface{}) error {
	jsonBytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize JSON: %w", err)
	}
	jsonBytes = append(jsonBytes, '\n')
	if err := atomicWriteFile(path, jsonBytes, 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	return nil
}

// stringsFromInterface converts a decoded JSON array (as []interface{}) into a
// []string, skipping non-string elements.
func stringsFromInterface(value interface{}) []string {
	array, ok := value.([]interface{})
	if !ok {
		return nil
	}
	var result []string
	for _, item := range array {
		if str, ok := item.(string); ok {
			result = append(result, str)
		}
	}
	return result
}

// sameSet reports whether two slices hold the same set of strings, ignoring
// order and duplicates.
func sameSet(existing, expected []string) bool {
	set := make(map[string]bool, len(existing))
	for _, e := range existing {
		set[e] = true
	}
	if len(set) != len(expected) {
		return false
	}
	for _, e := range expected {
		if !set[e] {
			return false
		}
	}
	return true
}

// dirExists reports whether path exists and is a directory (our heuristic for
// "this browser is installed").
func dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}
