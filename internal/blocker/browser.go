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

// BrowserPolicy describes what the browser layer should enforce. It is richer
// than a flat domain list so it can express keyword/wildcard patterns and
// allowlist ("block everything except") mode, neither of which the /etc/hosts
// or firewall layers can represent.
type BrowserPolicy struct {
	// BlockDomains are bare hosts to block (blocklist mode / always blocked).
	BlockDomains []string
	// BlockPatterns are extra wildcard/keyword URL filters (browser only).
	BlockPatterns []string
	// AllowMode turns on "block everything except AllowDomains".
	AllowMode bool
	// AllowDomains are the hosts permitted when AllowMode is set.
	AllowDomains []string
}

// empty reports whether the policy would block nothing.
func (p BrowserPolicy) Empty() bool {
	return !p.AllowMode && len(ValidDomains(p.BlockDomains)) == 0 && len(ValidPatterns(p.BlockPatterns)) == 0
}

// =============================================================================
// Public functions
// =============================================================================

// ApplyBrowserPolicies writes the blocking policies for every browser.
//
// Firefox and Chromium use DIFFERENT pattern formats (treating them the same
// once let subdomains through), so we build two sets:
//
//   - Firefox (WebsiteFilter): match patterns like "*://*.domain.com/*", plus a
//     catch-all "*://*/*" with Exceptions in allowlist mode.
//   - Chromium family (URLBlocklist / URLAllowlist): a bare "domain.com" filter
//     (which matches root, subdomains and paths), or "*" plus URLAllowlist in
//     allowlist mode.
//
// With an empty (nothing-to-block) policy, the files are removed instead of
// written empty, so an apps-only block leaves no policy files behind.
func ApplyBrowserPolicies(p BrowserPolicy) error {
	if p.Empty() {
		return RemoveBrowserPolicies()
	}

	if err := applyFirefoxPolicy(firefoxBlockSet(p), firefoxExceptionSet(p)); err != nil {
		return fmt.Errorf("failed to apply Firefox policy: %w", err)
	}

	block := chromiumBlockSet(p)
	allow := chromiumAllowSet(p)
	for _, path := range chromiumPolicyPaths {
		if err := applyChromiumPolicy(path, block, allow); err != nil {
			return fmt.Errorf("failed to apply policy at %s: %w", path, err)
		}
	}

	return nil
}

// BlockDomainsPolicy is a convenience constructor for the common case of a
// plain blocklist of domains (no patterns, no allowlist).
func BlockDomainsPolicy(domains []string) BrowserPolicy {
	return BrowserPolicy{BlockDomains: domains}
}

// firefoxBlockSet builds Firefox's WebsiteFilter.Block array for a policy.
func firefoxBlockSet(p BrowserPolicy) []string {
	if p.AllowMode {
		// Block all http/https URLs; allowed sites go in Exceptions.
		set := []string{"*://*/*"}
		return dedupSort(append(set, ValidPatterns(p.BlockPatterns)...))
	}
	set := firefoxPatterns(ValidDomains(p.BlockDomains))
	return dedupSort(append(set, ValidPatterns(p.BlockPatterns)...))
}

// firefoxExceptionSet builds Firefox's WebsiteFilter.Exceptions array (only
// meaningful in allowlist mode).
func firefoxExceptionSet(p BrowserPolicy) []string {
	if !p.AllowMode {
		return nil
	}
	return firefoxPatterns(ValidDomains(p.AllowDomains))
}

// chromiumBlockSet builds the URLBlocklist array for a policy.
func chromiumBlockSet(p BrowserPolicy) []string {
	if p.AllowMode {
		return dedupSort(append([]string{"*"}, ValidPatterns(p.BlockPatterns)...))
	}
	set := chromiumFilters(ValidDomains(p.BlockDomains))
	return dedupSort(append(set, ValidPatterns(p.BlockPatterns)...))
}

// chromiumAllowSet builds the URLAllowlist array (only meaningful in allowlist
// mode).
func chromiumAllowSet(p BrowserPolicy) []string {
	if !p.AllowMode {
		return nil
	}
	return chromiumFilters(ValidDomains(p.AllowDomains))
}

// dedupSort returns the sorted, deduplicated set of the input.
func dedupSort(in []string) []string {
	set := make(map[string]struct{}, len(in))
	for _, s := range in {
		set[s] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
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
func IsBrowserPoliciesApplied(p BrowserPolicy) bool {
	if p.Empty() {
		return !BrowserPoliciesPresent()
	}

	ffBlock := firefoxBlockSet(p)
	ffExceptions := firefoxExceptionSet(p)
	crBlock := chromiumBlockSet(p)
	crAllow := chromiumAllowSet(p)

	if dirExists(filepath.Dir(firefoxPolicyPath)) {
		if !checkFirefoxPolicy(ffBlock, ffExceptions) {
			return false
		}
	}

	for _, path := range chromiumPolicyPaths {
		if dirExists(filepath.Dir(path)) {
			if !checkChromiumPolicy(path, crBlock, crAllow) {
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
func applyFirefoxPolicy(block, exceptions []string) error {
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
	blocks := make([]string, 0, len(block))
	blocks = append(blocks, block...)
	websiteFilter["Block"] = blocks
	// Exceptions carry the allowlist entries; drop the key when there are none
	// so a switch back to blocklist mode doesn't leave stale exceptions.
	if len(exceptions) > 0 {
		exc := make([]string, 0, len(exceptions))
		exc = append(exc, exceptions...)
		websiteFilter["Exceptions"] = exc
	} else {
		delete(websiteFilter, "Exceptions")
	}
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

// checkFirefoxPolicy verifies WebsiteFilter.Block and .Exceptions are exactly
// the expected sets and that DNS-over-HTTPS is present and disabled.
func checkFirefoxPolicy(expectedBlock, expectedExceptions []string) bool {
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
	if !sameSet(stringsFromInterface(websiteFilter["Block"]), expectedBlock) {
		return false
	}
	if !sameSet(stringsFromInterface(websiteFilter["Exceptions"]), expectedExceptions) {
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
// browser: the URL blocklist (and, in allowlist mode, the allowlist) plus
// DNS-over-HTTPS turned off.
func applyChromiumPolicy(path string, block, allow []string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}
	policy := map[string]interface{}{
		"URLBlocklist":            block,
		"DnsOverHttpsMode":        "off",
		"BuiltInDnsClientEnabled": false,
	}
	if len(allow) > 0 {
		policy["URLAllowlist"] = allow
	}
	return writeJSON(path, policy)
}

// checkChromiumPolicy verifies URLBlocklist and URLAllowlist are exactly the
// expected sets and that DNS-over-HTTPS is off.
func checkChromiumPolicy(path string, expectedBlock, expectedAllow []string) bool {
	content, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var policy map[string]interface{}
	if json.Unmarshal(content, &policy) != nil {
		return false
	}
	if !sameSet(stringsFromInterface(policy["URLBlocklist"]), expectedBlock) {
		return false
	}
	if !sameSet(stringsFromInterface(policy["URLAllowlist"]), expectedAllow) {
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
