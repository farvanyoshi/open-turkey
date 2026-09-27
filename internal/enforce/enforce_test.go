package enforce

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/brunodcdo/open-turkey/internal/db"
)

// TestReconcileSchedules checks the daemon's scheduling core (no layers, so no
// root needed): a block auto-activates inside its window and the daemon
// deactivates it when the window closes — but never touches a manually
// activated block.
func TestReconcileSchedules(t *testing.T) {
	d, err := db.OpenDB(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	if err := d.CreateBlock("s", db.ModeBlocklist); err != nil {
		t.Fatal(err)
	}
	if err := d.AddSites("s", []string{"x.com"}); err != nil {
		t.Fatal(err)
	}
	// Every day, 09:00–17:00.
	if err := d.AddSchedule("s", 0x7f, 9*60, 17*60); err != nil {
		t.Fatal(err)
	}

	inWindow := time.Date(2024, 1, 1, 10, 0, 0, 0, time.Local)  // Mon 10:00
	outWindow := time.Date(2024, 1, 1, 18, 0, 0, 0, time.Local) // Mon 18:00

	// Inside the window → auto-activated as scheduled.
	if err := reconcileSchedules(d, inWindow); err != nil {
		t.Fatal(err)
	}
	if b, _ := d.GetBlock("s"); !b.Active || !b.Scheduled {
		t.Fatalf("expected scheduled activation; got active=%v scheduled=%v", b.Active, b.Scheduled)
	}

	// Outside the window → auto-deactivated.
	if err := reconcileSchedules(d, outWindow); err != nil {
		t.Fatal(err)
	}
	if b, _ := d.GetBlock("s"); b.Active {
		t.Fatal("expected scheduled block to deactivate outside its window")
	}

	// A MANUAL activation must not be auto-deactivated outside a window.
	if err := d.ActivateBlock("s", false, 0, 0, false); err != nil {
		t.Fatal(err)
	}
	if err := reconcileSchedules(d, outWindow); err != nil {
		t.Fatal(err)
	}
	if b, _ := d.GetBlock("s"); !b.Active {
		t.Fatal("manual activation must survive schedule reconciliation")
	}
}

func TestInputsBlocklist(t *testing.T) {
	active := []db.ActiveBlockDetail{
		{BlockName: "a", Mode: db.ModeBlocklist, Sites: []string{"facebook.com"}, Apps: []string{"discord"}, Patterns: []string{"*ads*"}},
		{BlockName: "b", Mode: db.ModeBlocklist, Sites: []string{"reddit.com"}, Apps: []string{"discord", "slack"}},
	}
	hosts, apps, bp := Inputs(active)

	if !reflect.DeepEqual(hosts, []string{"facebook.com", "reddit.com"}) {
		t.Errorf("hosts = %v", hosts)
	}
	if !reflect.DeepEqual(apps, []string{"discord", "slack"}) { // deduped, sorted
		t.Errorf("apps = %v", apps)
	}
	if bp.AllowMode {
		t.Error("no allowlist block, AllowMode should be false")
	}
	if !reflect.DeepEqual(bp.BlockPatterns, []string{"*ads*"}) {
		t.Errorf("patterns = %v", bp.BlockPatterns)
	}
}

func TestInputsAllowlist(t *testing.T) {
	active := []db.ActiveBlockDetail{
		{BlockName: "block", Mode: db.ModeBlocklist, Sites: []string{"facebook.com"}},
		{BlockName: "allow", Mode: db.ModeAllowlist, Sites: []string{"work.com", "docs.com"}},
	}
	hosts, _, bp := Inputs(active)

	// Only blocklist domains go to /etc/hosts and the firewall.
	if !reflect.DeepEqual(hosts, []string{"facebook.com"}) {
		t.Errorf("hosts = %v; want [facebook.com]", hosts)
	}
	if !bp.AllowMode {
		t.Fatal("AllowMode should be true when an allowlist block is active")
	}
	// Allowed domains carried into the browser policy.
	wantAllow := map[string]bool{"work.com": true, "docs.com": true}
	for _, d := range bp.AllowDomains {
		if !wantAllow[d] {
			t.Errorf("unexpected allow domain %q", d)
		}
	}
	if len(bp.AllowDomains) != 2 {
		t.Errorf("AllowDomains = %v; want 2 entries", bp.AllowDomains)
	}
}
