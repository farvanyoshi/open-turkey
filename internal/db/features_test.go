package db

import (
	"path/filepath"
	"reflect"

	"github.com/brunodcdo/open-turkey/internal/schedule"
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
	if err := d.AddSchedule("focus", schedule.Window{Days: 0x3e, Start: 540, End: 1020}); err != nil {
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
	if s, _ := d.ListSchedules("focus"); len(s) != 1 || s[0].Allow {
		t.Errorf("ListSchedules = %+v; want 1 block window", s)
	}
	if err := d.AddSchedule("focus", schedule.Window{Days: 0x60, Start: 1080, End: 1440, Allow: true}); err != nil {
		t.Fatal(err)
	}
	if s, _ := d.ListSchedules("focus"); len(s) != 2 || !s[1].Allow || s[1].EndMin != 1440 {
		t.Errorf("allow window not stored: %+v", s)
	}
	if err := d.RemoveSchedule("focus", 2); err != nil {
		t.Fatal(err)
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

	// Remove schedule by number.
	if err := d.RemoveSchedule("focus", 1); err != nil {
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

// TestScheduleNumbering checks that schedules are numbered 1..n per block, that
// removing one renumbers the later ones down, and that a new one takes the
// lowest free number.
func TestScheduleNumbering(t *testing.T) {
	d, err := OpenDB(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	for _, name := range []string{"a", "b"} {
		if err := d.CreateBlock(name, ""); err != nil {
			t.Fatal(err)
		}
	}
	// Interleave blocks so row IDs aren't contiguous per block.
	for _, start := range []int{60, 120, 180} {
		if err := d.AddSchedule("a", schedule.Window{Days: 0x7f, Start: start, End: start + 30}); err != nil {
			t.Fatal(err)
		}
		if err := d.AddSchedule("b", schedule.Window{Days: 0x7f, Start: start, End: start + 30}); err != nil {
			t.Fatal(err)
		}
	}

	starts := func(name string) (nums, mins []int) {
		t.Helper()
		ss, err := d.ListSchedules(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range ss {
			nums = append(nums, s.Num)
			mins = append(mins, s.StartMin)
		}
		return
	}

	if nums, _ := starts("b"); !reflect.DeepEqual(nums, []int{1, 2, 3}) {
		t.Fatalf("block b numbers = %v; want [1 2 3]", nums)
	}

	if err := d.RemoveSchedule("a", 2); err != nil {
		t.Fatal(err)
	}
	if nums, mins := starts("a"); !reflect.DeepEqual(nums, []int{1, 2}) || !reflect.DeepEqual(mins, []int{60, 180}) {
		t.Fatalf("after removing 2: nums=%v mins=%v; want [1 2] [60 180]", nums, mins)
	}
	if err := d.AddSchedule("a", schedule.Window{Days: 0x7f, Start: 300, End: 330}); err != nil {
		t.Fatal(err)
	}
	if nums, mins := starts("a"); !reflect.DeepEqual(nums, []int{1, 2, 3}) || mins[2] != 300 {
		t.Fatalf("after add: nums=%v mins=%v; want new schedule as 3", nums, mins)
	}
	if err := d.RemoveSchedule("a", 4); err == nil {
		t.Error("removing a nonexistent number should error")
	}
	if nums, _ := starts("b"); len(nums) != 3 {
		t.Errorf("block b affected by edits to a: %v", nums)
	}
}
