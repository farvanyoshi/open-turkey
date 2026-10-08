package enforce

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/brunodcdo/open-turkey/internal/db"
	"github.com/brunodcdo/open-turkey/internal/schedule"
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
	if err := d.AddSchedule("s", schedule.Window{Days: 0x7f, Start: 9 * 60, End: 17 * 60}); err != nil {
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

// TestReconcileAllowSchedule checks allow windows: the block is on outside
// them and off inside them.
func TestReconcileAllowSchedule(t *testing.T) {
	d, err := db.OpenDB(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	if err := d.CreateBlock("games", db.ModeBlocklist); err != nil {
		t.Fatal(err)
	}
	// Allowed Friday and Saturday 18:00–24:00.
	if err := d.AddSchedule("games", schedule.Window{Days: schedule.Friday | schedule.Saturday, Start: 18 * 60, End: 24 * 60, Allow: true}); err != nil {
		t.Fatal(err)
	}

	fri := func(h, m int) time.Time { return time.Date(2024, 1, 5, h, m, 0, 0, time.Local) } // a Friday
	steps := []struct {
		t    time.Time
		want bool
	}{
		{fri(12, 0), true},                   // Friday afternoon: blocked
		{fri(18, 0), false},                  // allow window open
		{fri(23, 59), false},                 // still open up to midnight
		{fri(12, 0).AddDate(0, 0, 2), true},  // Sunday: blocked
		{fri(20, 0).AddDate(0, 0, 1), false}, // Saturday evening
		{fri(20, 0).AddDate(0, 0, 3), true},  // Monday evening: blocked
	}
	for _, st := range steps {
		if err := reconcileSchedules(d, st.t); err != nil {
			t.Fatal(err)
		}
		b, _ := d.GetBlock("games")
		if b.Active != st.want {
			t.Errorf("%s: active = %v; want %v", st.t.Format("Mon 15:04"), b.Active, st.want)
		}
		if on, _ := ScheduleActiveNow(d, "games", st.t); on != st.want {
			t.Errorf("%s: ScheduleActiveNow = %v; want %v", st.t.Format("Mon 15:04"), on, st.want)
		}
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
