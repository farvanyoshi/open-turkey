package db

import (
	"path/filepath"
	"testing"
)

// TestFeaturesRoundTrip exercises the newer schema (modes, patterns, schedules,
// time locks, scheduled flag) end to end against a temporary database. Opening
// the DB also runs migrations + upgradeSchema, so this covers that path too.
func TestFeaturesRoundTrip(t *testing.T) {
	d, err := OpenDB(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer d.Close()

	if err := d.CreateBlock("focus", ModeAllowlist); err != nil {
		t.Fatalf("CreateBlock: %v", err)
	}
	if err := d.AddSites("focus", []string{"work.com"}); err != nil {
		t.Fatal(err)
	}
	if err := d.AddApps("focus", []string{"discord"}); err != nil {
		t.Fatal(err)
	}
	if err := d.AddPatterns("focus", []string{"*ads*", "*casino*"}); err != nil {
		t.Fatal(err)
	}
	if err := d.AddSchedule("focus", 0x3e /*weekdays*/, 540, 1020); err != nil {
		t.Fatal(err)
	}

	// Activate with a char lock, a time lock and the scheduled flag.
	if err := d.ActivateBlock("focus", true, 300, 9999999999, true); err != nil {
		t.Fatal(err)
	}

	got, err := d.GetBlock("focus")
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != ModeAllowlist {
		t.Errorf("mode = %q; want allowlist", got.Mode)
	}
	if !got.Active || !got.Locked || got.LockChars != 300 || got.LockedUntil != 9999999999 || !got.Scheduled {
		t.Errorf("activation state wrong: %+v", got)
	}
	if len(got.Patterns) != 2 {
		t.Errorf("patterns = %v; want 2", got.Patterns)
	}

	// Schedules visible per-block and globally.
	if s, _ := d.ListSchedules("focus"); len(s) != 1 {
		t.Errorf("ListSchedules = %d; want 1", len(s))
	}
	all, _ := d.AllSchedules()
	if len(all) != 1 || all[0].BlockName != "focus" {
		t.Errorf("AllSchedules = %+v", all)
	}

	// Active blocks carry mode, patterns and the time lock.
	active, err := d.GetActiveBlocks()
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 {
		t.Fatalf("active = %d; want 1", len(active))
	}
	if active[0].Mode != ModeAllowlist || active[0].LockedUntil != 9999999999 || len(active[0].Patterns) != 2 {
		t.Errorf("active detail wrong: %+v", active[0])
	}

	// Remove schedule by id.
	if err := d.RemoveSchedule("focus", all[0].ID); err != nil {
		t.Fatal(err)
	}
	if s, _ := d.ListSchedules("focus"); len(s) != 0 {
		t.Errorf("after remove, schedules = %d; want 0", len(s))
	}
}

// TestCreateBlockRejectsBadMode guards the mode validation.
func TestCreateBlockRejectsBadMode(t *testing.T) {
	d, err := OpenDB(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	if err := d.CreateBlock("x", "sideways"); err == nil {
		t.Error("CreateBlock with invalid mode should error")
	}
	// Empty mode defaults to blocklist.
	if err := d.CreateBlock("y", ""); err != nil {
		t.Fatalf("CreateBlock empty mode: %v", err)
	}
	b, _ := d.GetBlock("y")
	if b.Mode != ModeBlocklist {
		t.Errorf("default mode = %q; want blocklist", b.Mode)
	}
}
