// =============================================================================
// Package blocker — Open Turkey's browser policy module
// =============================================================================
//
// This file implements site blocking using browser "enterprise policies"
// for Firefox and Chromium/Chrome.
//
// WHAT ARE BROWSER POLICIES?
// --------------------------
// Browsers like Firefox and Chrome are built to be used in large companies,
// where the IT administrator needs to control what employees can and cannot
// do. To support that, they read "policies" — JSON files that live in system
// directories and that the browser loads automatically.
//
// These policies allow, for example:
//   - Blocking access to specific sites (what we care about!)
//   - Disabling extension installation
//   - Forcing proxy settings
//   - Disabling private browsing
//
// WHY IS THIS SO EFFECTIVE?
// -------------------------
// Unlike extensions or browser settings, policies are applied by the
// OPERATING SYSTEM. The user CANNOT disable them from inside the browser. It
// doesn't matter if they go into settings, use private mode, or try any other
// trick — policies are law.
//
// Removing a policy requires root access to the system to edit/delete the
// JSON file. In Open Turkey's context, that means the block can only be
// removed by the program's daemon (which runs as root), not by a user trying
// to get around it.
//
// HOW DOES IT WORK IN PRACTICE?
// -----------------------------
// 1. We create JSON files in the system policy directories
// 2. The browser picks these files up automatically (at startup AND while
//    running — no restart needed!)
// 3. The "WebsiteFilter" (Firefox) or "URLBlocklist" (Chromium) policy
//    blocks the URL patterns we specify
// 4. The browser shows an error page saying the site was blocked by the
//    organization's policy
//
// NO EXTENSION REQUIRED!
// ----------------------
// Many site blockers rely on browser extensions, which the user can simply
// disable. Here we use a mechanism owned by the operating system — far more
// robust.
//
// DIFFERENCES BETWEEN FIREFOX AND CHROMIUM:
// -----------------------------------------
// Firefox:
//   - File: /etc/firefox/policies/policies.json
//   - Format: { "policies": { "WebsiteFilter": { "Block": [...] } } }
//   - The file is SHARED — other policies may exist in the same file. So we
//     need to MERGE our rules with the existing ones, without deleting
//     anything that isn't ours.
//
// Chromium/Chrome:
//   - File: /etc/chromium/policies/managed/open-turkey.json
//   - Format: { "URLBlocklist": [...] }
//   - Chromium reads ALL .json files inside the managed/ directory, so we can
//     create a separate file that's ours alone (open-turkey.json). That's much
//     simpler — no need to worry about merging.
//   - Google Chrome uses a different directory:
//     /etc/opt/chrome/policies/managed/open-turkey.json
//   - Brave, also Chromium-based, uses its own directory:
//     /etc/brave/policies/managed/open-turkey.json
//     (older versions read /etc/chromium/, already covered by the file above).
//
// =============================================================================
package blocker

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"net/url"
	"strings"
)

// Policy file paths for each browser.
//
// Firefox uses a single shared policies.json file.
// Chromium and Chrome use directories where each .json file is a separate
// policy — that's why we create "open-turkey.json", a file that's ours alone.
const (
	// firefoxPolicyPath is Firefox's policy file.
	// Firefox looks for this file automatically at startup.
	// If the file doesn't exist, Firefox simply ignores it and carries on
	// without policies — so we don't break anything by creating/removing it.
	firefoxPolicyPath = "/etc/firefox/policies/policies.json"

	// chromiumPolicyPath is Chromium's policy file.
	// Chromium reads every .json inside /etc/chromium/policies/managed/,
	// so our file coexists peacefully with any others that may exist.
	chromiumPolicyPath = "/etc/chromium/policies/managed/open-turkey.json"

	// chromePolicyPath is Google Chrome's policy file.
	// Chrome uses a different directory from Chromium, even though it's built
	// from the same code. That's because the Chrome install puts its files in
	// /etc/opt/chrome/ instead of /etc/chromium/.
	chromePolicyPath = "/etc/opt/chrome/policies/managed/open-turkey.json"

	// bravePolicyPath is Brave's policy file.
	// Brave is Chromium-based and uses the same format (URLBlocklist), but it
	// moved to its own directory under /etc/brave/. Older Brave versions read
	// /etc/chromium/policies/managed/, which is already covered by the
	// Chromium file above — so adding this path is enough.
	bravePolicyPath = "/etc/brave/policies/managed/open-turkey.json"
)

// =============================================================================
// Public functions — the interface the rest of Open Turkey uses
// =============================================================================

// ApplyBrowserPolicies creates the blocking policies for every browser.
//
// WARNING — Firefox and Chromium use DIFFERENT pattern formats, and treating
// them as the same has already caused a production bug (subdomains like
// folha.uol.com.br got through in Brave/Chrome). So we generate TWO sets:
//
//   - Firefox (WebsiteFilter): match patterns in the style "*://*.domain.com/*".
//     The asterisk in the scheme covers http/https, "*." covers subdomains and
//     "/*" covers any path. See firefoxPatterns.
//
//   - Chromium/Chrome/Brave (URLBlocklist): the format is
//     "[scheme://][.]host[:port][/path][@query]" and it does NOT accept
//     wildcards like "*://*.host/*" — malformed filters are silently dropped.
//     Here a plain "domain.com" filter already matches the root domain AND all
//     subdomains AND all paths. See chromiumFilters.
//     Ref.: https://www.chromium.org/administrators/url-blocklist-filter-format/
func ApplyBrowserPolicies(domains []string) error {
	// Step 1: Build the patterns/filters — one set per format.
	ffPatterns := firefoxPatterns(domains)
	crFilters := chromiumFilters(domains)

	// Step 2: Apply the Firefox policy.
	// Firefox uses its own format with "WebsiteFilter" inside "policies".
	// Since the file may already exist with other policies, we merge.
	if err := applyFirefoxPolicy(ffPatterns); err != nil {
		return fmt.Errorf("failed to apply Firefox policy: %w", err)
	}

	// Step 3: Apply the Chromium policy.
	// Chromium uses "URLBlocklist" and accepts separate files per policy.
	if err := applyChromiumPolicy(chromiumPolicyPath, crFilters); err != nil {
		return fmt.Errorf("failed to apply Chromium policy: %w", err)
	}

	// Step 4: Apply the Google Chrome policy.
	// Chrome uses the same format as Chromium, but in a different directory.
	if err := applyChromiumPolicy(chromePolicyPath, crFilters); err != nil {
		return fmt.Errorf("failed to apply Google Chrome policy: %w", err)
	}

	// Step 5: Apply the Brave policy.
	// Brave also uses the Chromium format, under /etc/brave/.
	if err := applyChromiumPolicy(bravePolicyPath, crFilters); err != nil {
		return fmt.Errorf("failed to apply Brave policy: %w", err)
	}

	return nil
}

// RemoveBrowserPolicies removes the blocking policies from every browser.
//
// The removal strategy differs per browser:
//
// Firefox: The policies.json file may contain OTHER policies besides ours.
// So we CANNOT simply delete the file. Instead:
//   - If the file only has our WebsiteFilter policy: we delete the file
//   - If it has other policies besides ours: we remove only WebsiteFilter
//
// Chromium/Chrome: Since we use a separate file (open-turkey.json), we just
// delete it. Other policy files in the same directory are unaffected.
func RemoveBrowserPolicies() error {
	// Step 1: Remove the Firefox policy (being careful not to delete other
	// policies that may exist in the same file).
	if err := removeFirefoxPolicy(); err != nil {
		return fmt.Errorf("failed to remove Firefox policy: %w", err)
	}

	// Step 2: Remove the Chromium policy.
	// We simply delete our file. If it doesn't exist, that's fine.
	if err := removePolicyFile(chromiumPolicyPath); err != nil {
		return fmt.Errorf("failed to remove Chromium policy: %w", err)
	}

	// Step 3: Remove the Google Chrome policy.
	if err := removePolicyFile(chromePolicyPath); err != nil {
		return fmt.Errorf("failed to remove Google Chrome policy: %w", err)
	}

	// Step 4: Remove the Brave policy.
	if err := removePolicyFile(bravePolicyPath); err != nil {
		return fmt.Errorf("failed to remove Brave policy: %w", err)
	}

	return nil
}

// IsBrowserPoliciesApplied checks whether the blocking policies are active
// for EVERY browser that has a policy directory on the system.
//
// The check is strict: it returns true ONLY if every expected domain is
// present in EVERY applicable policy file. If any installed browser lacks the
// correct policy, it returns false.
//
// This matters for detecting whether someone manually edited the policy
// files trying to get around the block.
func IsBrowserPoliciesApplied(domains []string) bool {
	// Build the expected patterns/filters — one set per format, the same as
	// what ApplyBrowserPolicies writes. Checking Chromium against the Firefox
	// format (or vice versa) would make the daemon think the policy always
	// diverges and reapply it every 5s for no reason.
	ffPatterns := firefoxPatterns(domains)
	crFilters := chromiumFilters(domains)

	if len(ffPatterns) == 0 || len(crFilters) == 0 {
		return false
	}

	// Firefox check:
	// We only check if Firefox's policy directory exists,
	// which indicates that Firefox is installed on the system.
	firefoxDir := filepath.Dir(firefoxPolicyPath)
	if dirExists(firefoxDir) {
		if !checkFirefoxPolicy(ffPatterns) {
			return false
		}
	}

	// Chromium check:
	// Same reasoning — we only check if the parent directory exists.
	chromiumDir := filepath.Dir(chromiumPolicyPath)
	if dirExists(chromiumDir) {
		if !checkChromiumPolicy(chromiumPolicyPath, crFilters) {
			return false
		}
	}

	// Google Chrome check:
	chromeDir := filepath.Dir(chromePolicyPath)
	if dirExists(chromeDir) {
		if !checkChromiumPolicy(chromePolicyPath, crFilters) {
			return false
		}
	}

	// Brave check:
	braveDir := filepath.Dir(bravePolicyPath)
	if dirExists(braveDir) {
		if !checkChromiumPolicy(bravePolicyPath, crFilters) {
			return false
		}
	}

	return true
}

// =============================================================================
// Helper functions — URL pattern generation
// =============================================================================

// stripWWWPrefix removes a leading "www." from a host, but only when what's
// left is a domain with at least two labels. Without this guard, a
// single-label host like "www.com" (a real registrable domain) would become
// "com" — and Chromium's bare filter / Firefox's "*://*.com/*" wildcard would
// then match ALL of ".com".
func stripWWWPrefix(host string) string {
	withoutWWW := strings.TrimPrefix(host, "www.")
	if strings.Contains(withoutWWW, ".") {
		return withoutWWW
	}
	return host
}

// firefoxPatterns converts domains into match patterns for Firefox's
// WebsiteFilter policy.
//
// For each domain, it generates two patterns:
//   - *://*.domain.com/*  → subdomains (www, m, app, etc.)
//   - *://domain.com/*    → root domain
//
// The *://.../* format is what Firefox's WebsiteFilter expects:
//   - *://  → any scheme (http, https, etc.)
//   - *.    → any subdomain
//   - /*    → any path within the site
//
// IMPORTANT: this format does NOT work for the Chromium/Chrome/Brave
// URLBlocklist (which drops these wildcards) — for that family, use
// chromiumFilters.
func firefoxPatterns(domains []string) []string {
	set := make(map[string]bool)

	for _, domain := range domains {
		host := NormalizeDomain(domain)
		if host == "" {
			continue
		}

		// Pattern 1: block the exact host (e.g. *://facebook.com/*)
		set[fmt.Sprintf("*://%s/*", host)] = true

		// Pattern 2: block subdomains. If the host starts with "www.",
		// we build the wildcard on the host without www (avoids "*.www.*").
		base := stripWWWPrefix(host)
		set[fmt.Sprintf("*://*.%s/*", base)] = true
	}

	patterns := make([]string, 0, len(set))
	for p := range set {
		patterns = append(patterns, p)
	}
	sort.Strings(patterns)
	return patterns
}

// chromiumFilters converts domains into URLBlocklist-format filters, used by
// Chromium, Google Chrome and Brave.
//
// URLBlocklist does NOT use match patterns like Firefox. The format is
// "[scheme://][.]host[:port][/path][@query]" and a plain filter like
// "uol.com.br" already matches the root domain AND all subdomains (www, m,
// folha, ...) AND all paths. Wildcards like "*://*.host/*" are INVALID and
// the browser silently drops them — that was the bug that let subdomains
// (e.g. folha.uol.com.br) through even with uol.com.br blocked.
// Ref.: https://www.chromium.org/administrators/url-blocklist-filter-format/
//
// So we generate ONE filter per domain, always the base host: we strip any
// "www." prefix because the bare filter already covers root and subdomains,
// and a leading "." would restrict exactly what we want to catch.
func chromiumFilters(domains []string) []string {
	set := make(map[string]bool)

	for _, domain := range domains {
		host := NormalizeDomain(domain)
		if host == "" {
			continue
		}

		// The bare "domain.com" filter already covers "www.domain.com" and
		// other subdomains, so we normalize by stripping "www." from the input.
		host = stripWWWPrefix(host)

		set[host] = true
	}

	filters := make([]string, 0, len(set))
	for f := range set {
		filters = append(filters, f)
	}
	sort.Strings(filters)
	return filters
}

func NormalizeDomain(input string) string {
	s := strings.TrimSpace(strings.ToLower(input))
	if s == "" {
		return ""
	}

	// If it's a full URL, extract the host.
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err == nil && u != nil && u.Host != "" {
			s = u.Host
		}
	}

	// Remove any path (e.g. "example.com/foo").
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}

	// Remove port (e.g. "example.com:443").
	if i := strings.LastIndexByte(s, ':'); i >= 0 && !strings.Contains(s, "]") {
		s = s[:i]
	}

	s = strings.TrimSpace(strings.TrimSuffix(s, "."))
	if s == "" {
		return ""
	}

	return s
}

// =============================================================================
// Helper functions — Firefox
// =============================================================================

// applyFirefoxPolicy creates or updates Firefox's policy file.
//
// Firefox's policies.json file has a nested structure:
//
//	{
//	    "policies": {
//	        "WebsiteFilter": {
//	            "Block": [
//	                "*://*.facebook.com/*",
//	                "*://facebook.com/*"
//	            ]
//	        },
//	        ... (other policies may exist here)
//	    }
//	}
//
// If the file already exists, we need to MERGE: read the existing JSON, add
// our patterns to the "Block" array (without duplicating), and write it back.
// If the file doesn't exist, we create it from scratch.
func applyFirefoxPolicy(patterns []string) error {
	// Step 1: Create the policy directory if it doesn't exist.
	// os.MkdirAll creates every directory in the path, like "mkdir -p" in the
	// terminal. If they already exist, it does nothing (idempotent).
	// Permission 0755: owner can do everything (7), group and others can read
	// and enter (5).
	dir := filepath.Dir(firefoxPolicyPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	// Step 2: Try to read the existing file.
	// If the file already exists, we load its contents to preserve any other
	// policies that may be configured.
	// If it doesn't exist, we start with an empty map.
	rootPolicy := make(map[string]interface{})

	existingContent, err := os.ReadFile(firefoxPolicyPath)
	if err == nil && len(existingContent) > 0 {
		// The file exists and has content — decode the JSON.
		// json.Unmarshal converts the JSON (text) into a Go map (data structure).
		if err := json.Unmarshal(existingContent, &rootPolicy); err != nil {
			// If the JSON is malformed, we don't want to lose data.
			// But we also need to apply our policy. In that case,
			// we log the error and start from scratch.
			rootPolicy = make(map[string]interface{})
		}
	}

	// Step 3: Navigate to the "policies" level in the map.
	// If "policies" doesn't exist yet, we create an empty map.
	policies, ok := rootPolicy["policies"].(map[string]interface{})
	if !ok {
		// "policies" doesn't exist or isn't a map — create from scratch.
		policies = make(map[string]interface{})
	}

	// Step 4: Navigate to "WebsiteFilter" inside "policies".
	websiteFilter, ok := policies["WebsiteFilter"].(map[string]interface{})
	if !ok {
		websiteFilter = make(map[string]interface{})
	}

	// Step 5: Update the "Block" array to reflect EXACTLY the current state.
	// We don't merge here, because merging prevents removals (Firefox would
	// keep blocking old patterns even after the user removed a domain).
	finalBlocks := make([]string, 0, len(patterns))
	for _, p := range patterns {
		finalBlocks = append(finalBlocks, p)
	}

	// Step 6: Rebuild the JSON structure and write the file.
	websiteFilter["Block"] = finalBlocks
	policies["WebsiteFilter"] = websiteFilter
	rootPolicy["policies"] = policies

	return writeJSON(firefoxPolicyPath, rootPolicy)
}

// removeFirefoxPolicy removes the Firefox blocking policy.
//
// There are two scenarios:
//   - If the policies.json file ONLY contains our WebsiteFilter policy:
//     we delete the whole file (full cleanup).
//   - If it has OTHER policies besides ours: we remove only WebsiteFilter
//     and write the file back with the rest preserved.
//
// This matters so we don't delete settings the system administrator may
// have made independently of Open Turkey.
func removeFirefoxPolicy() error {
	// Step 1: Try to read the existing file.
	// If it doesn't exist, there's nothing to remove — success!
	content, err := os.ReadFile(firefoxPolicyPath)
	if err != nil {
		// If the error is "file not found", all is well.
		// Any other error (permissions, etc.) is a real problem.
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read %s: %w", firefoxPolicyPath, err)
	}

	// Step 2: Decode the existing JSON.
	var rootPolicy map[string]interface{}
	if err := json.Unmarshal(content, &rootPolicy); err != nil {
		// Malformed JSON — we can just delete the file,
		// since it doesn't contain valid data anyway.
		return os.Remove(firefoxPolicyPath)
	}

	// Step 3: Check whether "policies" exists.
	policies, ok := rootPolicy["policies"].(map[string]interface{})
	if !ok {
		// Without "policies", the file doesn't have our rules. Nothing to do.
		return nil
	}

	// Step 4: Remove WebsiteFilter.
	delete(policies, "WebsiteFilter")

	// Step 5: Decide whether to delete the file or write it without WebsiteFilter.
	// If "policies" is empty after removing WebsiteFilter, the file is no
	// longer useful — we can delete it.
	if len(policies) == 0 {
		// The file only had our policy — delete everything.
		if err := os.Remove(firefoxPolicyPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove %s: %w", firefoxPolicyPath, err)
		}
		return nil
	}

	// Other policies still exist — write the file without WebsiteFilter.
	rootPolicy["policies"] = policies
	return writeJSON(firefoxPolicyPath, rootPolicy)
}

// checkFirefoxPolicy checks whether the WebsiteFilter.Block array in the
// Firefox policy file contains EXACTLY the expected patterns.
//
// Since ApplyBrowserPolicies overwrites Block in full, the correct state is
// set equality: no pattern missing, no extra pattern. If there's a mismatch
// in either direction (including an extra/tampered entry), it returns false
// so the daemon reapplies.
func checkFirefoxPolicy(expectedPatterns []string) bool {
	// Read and decode the policy file.
	content, err := os.ReadFile(firefoxPolicyPath)
	if err != nil {
		return false
	}

	var rootPolicy map[string]interface{}
	if err := json.Unmarshal(content, &rootPolicy); err != nil {
		return false
	}

	// Navigate to policies → WebsiteFilter → Block.
	policies, ok := rootPolicy["policies"].(map[string]interface{})
	if !ok {
		return false
	}

	websiteFilter, ok := policies["WebsiteFilter"].(map[string]interface{})
	if !ok {
		return false
	}

	existingBlocks := stringsFromInterface(websiteFilter["Block"])

	return sameSet(existingBlocks, expectedPatterns)
}

// =============================================================================
// Helper functions — Chromium / Google Chrome
// =============================================================================

// applyChromiumPolicy creates the policy file for Chromium, Chrome or Brave
// (same URLBlocklist format, different directories).
//
// The format is simpler than Firefox's — one bare filter per domain, which
// already covers root, subdomains and paths (see chromiumFilters):
//
//	{
//	    "URLBlocklist": [
//	        "facebook.com"
//	    ]
//	}
//
// Since we use a separate file (open-turkey.json), we don't need to worry
// about merging — we simply overwrite the file.
//
// The filePath parameter lets us reuse this function for Chromium, Chrome
// and Brave, which use different directories but the same format.
func applyChromiumPolicy(filePath string, patterns []string) error {
	// Step 1: Create the policy directory if it doesn't exist.
	// If the parent directory doesn't exist, the browser probably isn't
	// installed. We create the directory anyway — if the user installs the
	// browser later, the policy will already be there waiting.
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	// Step 2: Build the policy structure.
	// Chromium reads the "URLBlocklist" key automatically — it doesn't need
	// a nested "policies" structure like Firefox.
	policy := map[string]interface{}{
		"URLBlocklist": patterns,
	}

	return writeJSON(filePath, policy)
}

// checkChromiumPolicy checks whether the URLBlocklist array in the
// Chromium/Chrome/Brave policy file contains EXACTLY the expected filters.
//
// open-turkey.json is a file that's exclusively ours, overwritten in full on
// every ApplyBrowserPolicies. So the correct state is set equality: any
// mismatch (missing filter OR extra/tampered entry) returns false so the
// daemon reapplies.
func checkChromiumPolicy(filePath string, expectedPatterns []string) bool {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return false
	}

	var policy map[string]interface{}
	if err := json.Unmarshal(content, &policy); err != nil {
		return false
	}

	existingBlocks := stringsFromInterface(policy["URLBlocklist"])

	return sameSet(existingBlocks, expectedPatterns)
}

// =============================================================================
// Generic helper functions
// =============================================================================

// removePolicyFile deletes a policy file from the system.
//
// Used to remove the Chromium and Chrome files, which are exclusive to
// Open Turkey (open-turkey.json) and can simply be deleted.
//
// If the file no longer exists, it returns nil (no error) — the function is
// idempotent. "Idempotent" means calling it several times has the same effect
// as calling it once. That's useful for robustness: if the program crashes,
// we can try removing again without problems.
func removePolicyFile(filePath string) error {
	err := os.Remove(filePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove %s: %w", filePath, err)
	}
	return nil
}

// writeJSON serializes a Go map to formatted JSON and writes it to a file.
//
// Uses json.MarshalIndent to produce readable JSON, indented with 2 spaces.
// This matters because:
//   - System administrators may want to inspect the files
//   - Minified JSON (no whitespace) is hard to read and debug
//   - 2 spaces is the most common convention for config files
//
// Permission 0644:
//   - Owner (root): can read and write (6 = 4+2)
//   - Group: can only read (4)
//   - Others: can only read (4)
// Every user needs to read the file (the browser runs as a normal user, not
// as root), but only root can modify it.
func writeJSON(filePath string, data interface{}) error {
	// json.MarshalIndent converts the Go structure into a JSON string.
	// The first empty argument ("") is the prefix for each line.
	// The second argument ("  ") is the indentation — 2 spaces.
	jsonBytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize JSON: %w", err)
	}

	// Add a trailing newline at the end of the file.
	// This is a Unix convention: text files should end with a newline so that
	// tools like "cat" display them correctly.
	jsonBytes = append(jsonBytes, '\n')

	if err := os.WriteFile(filePath, jsonBytes, 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", filePath, err)
	}

	return nil
}

// stringsFromInterface converts an interface{} value (which may be a decoded
// JSON array) into a Go string slice.
//
// When we decode generic JSON with json.Unmarshal into a
// map[string]interface{}, arrays become []interface{} (not []string).
// Each array element is an interface{} that must be converted to a string
// individually.
//
// Example: the JSON ["a", "b"] is decoded to []interface{}{"a", "b"}
// and this function converts it to []string{"a", "b"}.
//
// If the value isn't an array, or if some element isn't a string, we simply
// ignore it (we return whatever we managed to convert).
func stringsFromInterface(value interface{}) []string {
	// Try converting to []interface{} — the type json.Unmarshal uses to
	// represent JSON arrays.
	array, ok := value.([]interface{})
	if !ok {
		return nil
	}

	var result []string
	for _, item := range array {
		// Try converting each item to a string.
		// If it's not a string (e.g. a number), we ignore it.
		if str, ok := item.(string); ok {
			result = append(result, str)
		}
	}

	return result
}

// sameSet returns true if the two slices contain the SAME set of strings,
// ignoring order and duplicates. Since the expected values already come
// deduplicated and sorted from the generators, it's enough to compare the
// size of the existing set to the number of expected values and confirm that
// every expected value is present.
func sameSet(existing, expected []string) bool {
	existingSet := make(map[string]bool, len(existing))
	for _, e := range existing {
		existingSet[e] = true
	}

	if len(existingSet) != len(expected) {
		return false
	}

	for _, e := range expected {
		if !existingSet[e] {
			return false
		}
	}

	return true
}

// dirExists checks whether a directory exists on the file system.
//
// Used to determine whether a browser is "installed" — if the policy
// directory exists, we assume the browser is present.
// This heuristic isn't perfect (the directory may exist without the browser,
// or the browser may be installed without the directory), but it's good
// enough for our purpose.
//
// Important detail for whoever touches this: after the first start, the
// Chromium-family directories (Chromium, Chrome, Brave) exist even without the
// browser installed, because applyChromiumPolicy creates them with
// os.MkdirAll on purpose — so the policy is ready in case the user installs
// the browser later. This does NOT weaken IsBrowserPoliciesApplied: since
// apply also writes the file, the directory's presence matches the presence
// of the correct file, and the only way to have "directory exists but file
// missing/divergent" is tampering — exactly what the daemon should detect and
// reapply.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}
