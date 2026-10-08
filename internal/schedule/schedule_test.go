package schedule

import (
	"testing"
	"time"
)

func TestParseDays(t *testing.T) {
	cases := map[string]int{
		"daily":       AllDays,
		"everyday":    AllDays,
		"weekdays":    Weekdays,
		"weekends":    Weekend,
		"mon-fri":     Weekdays,
		"mon,wed,fri": Monday | Wednesday | Friday,
		"sat,sun":     Weekend,
		"fri-mon":     Friday | Saturday | Sunday | Monday, // wraps the week
		"mon-thu,sun": Monday | Tuesday | Wednesday | Thursday | Sunday,
	}
	for spec, want := range cases {
		got, err := ParseDays(spec)
		if err != nil {
			t.Errorf("ParseDays(%q) error: %v", spec, err)
			continue
		}
		if got != want {
			t.Errorf("ParseDays(%q) = %b; want %b", spec, got, want)
		}
	}

	for _, bad := range []string{"", "funday", "mon-funday", "13"} {
		if _, err := ParseDays(bad); err == nil {
			t.Errorf("ParseDays(%q) = nil error; want error", bad)
		}
	}
}

func TestParseTimeOfDay(t *testing.T) {
	cases := map[string]int{
		"00:00": 0,
		"09:00": 540,
		"17:30": 1050,
		"23:59": 1439,
	}
	for s, want := range cases {
		got, err := ParseTimeOfDay(s)
		if err != nil {
			t.Errorf("ParseTimeOfDay(%q) error: %v", s, err)
			continue
		}
		if got != want {
			t.Errorf("ParseTimeOfDay(%q) = %d; want %d", s, got, want)
		}
	}
	for _, bad := range []string{"", "9", "24:00", "12:60", "aa:bb", "-1:00"} {
		if _, err := ParseTimeOfDay(bad); err == nil {
			t.Errorf("ParseTimeOfDay(%q) = nil error; want error", bad)
		}
	}
}

func TestWindowActiveAt(t *testing.T) {
	// Weekdays 09:00–17:00.
	w := Window{Days: Weekdays, Start: 540, End: 1020}

	// 2024-01-01 is a Monday.
	monday := time.Date(2024, 1, 1, 0, 0, 0, 0, time.Local)

	mustBe := func(t2 time.Time, want bool) {
		t.Helper()
		if got := w.ActiveAt(t2); got != want {
			t.Errorf("ActiveAt(%s) = %v; want %v", t2.Format("Mon 15:04"), got, want)
		}
	}

	mustBe(monday.Add(10*time.Hour), true)                   // Mon 10:00
	mustBe(monday.Add(9*time.Hour), true)                    // Mon 09:00 (inclusive)
	mustBe(monday.Add(17*time.Hour), false)                  // Mon 17:00 (exclusive)
	mustBe(monday.Add(8*time.Hour+59*time.Minute), false)    // Mon 08:59
	mustBe(monday.AddDate(0, 0, 5).Add(10*time.Hour), false) // Saturday 10:00
}

func TestFormatRoundTrip(t *testing.T) {
	for _, spec := range []string{"daily", "weekdays", "weekends", "mon,wed,fri"} {
		mask, err := ParseDays(spec)
		if err != nil {
			t.Fatal(err)
		}
		if got := FormatDays(mask); got != spec {
			t.Errorf("FormatDays(ParseDays(%q)) = %q; want %q", spec, got, spec)
		}
	}
	if FormatMinutes(540) != "09:00" {
		t.Errorf("FormatMinutes(540) = %q; want 09:00", FormatMinutes(540))
	}
}

func TestActiveWithin(t *testing.T) {
	// Monday 09:00–17:00.
	w := Window{Days: Monday, Start: 9 * 60, End: 17 * 60}
	mon := func(h, m, s int) time.Time { return time.Date(2026, 9, 28, h, m, s, 0, time.Local) } // a Monday
	cases := []struct {
		t    time.Time
		lead time.Duration
		want bool
	}{
		{mon(8, 29, 0), EditFreeze, false}, // 31 minutes out
		{mon(8, 30, 0), EditFreeze, true},  // exactly 30 minutes out
		{mon(8, 59, 59), EditFreeze, true},
		{mon(12, 0, 0), EditFreeze, true}, // inside the window
		{mon(16, 59, 0), 0, true},
		{mon(17, 0, 0), EditFreeze, false},                  // closed
		{mon(12, 0, 0).AddDate(0, 0, 1), EditFreeze, false}, // Tuesday
	}
	for _, c := range cases {
		if got := w.ActiveWithin(c.t, c.lead); got != c.want {
			t.Errorf("ActiveWithin(%s, %s) = %v; want %v", c.t.Format("Mon 15:04:05"), c.lead, got, c.want)
		}
	}

	// A window starting at midnight is reached from the previous evening.
	tue := Window{Days: Tuesday, Start: 0, End: 60}
	if !tue.ActiveWithin(mon(23, 45, 0), EditFreeze) {
		t.Error("Tuesday 00:00 window should be within 30 minutes of Monday 23:45")
	}
}

func TestEnforced(t *testing.T) {
	fri := func(h, m int) time.Time { return time.Date(2024, 1, 5, h, m, 0, 0, time.Local) } // a Friday
	block := Window{Days: Friday, Start: 9 * 60, End: 17 * 60}
	allow := Window{Days: Friday | Saturday, Start: 18 * 60, End: 24 * 60, Allow: true}

	if Enforced(nil, fri(12, 0)) {
		t.Error("no windows should never be enforced")
	}
	if !Enforced([]Window{block}, fri(12, 0)) || Enforced([]Window{block}, fri(18, 0)) {
		t.Error("block window: on inside, off outside")
	}
	if !Enforced([]Window{allow}, fri(12, 0)) || Enforced([]Window{allow}, fri(23, 59)) {
		t.Error("allow window: on outside, off inside (up to 24:00)")
	}
	if !Enforced([]Window{allow}, fri(0, 0).AddDate(0, 0, 1)) {
		t.Error("allow window: Saturday 00:00 is outside it")
	}
}

func TestWeakens(t *testing.T) {
	fri := func(h, m int) time.Time { return time.Date(2024, 1, 5, h, m, 0, 0, time.Local) } // a Friday
	evening := Window{Days: Friday, Start: 18 * 60, End: 24 * 60, Allow: true}
	morning := Window{Days: Friday, Start: 8 * 60, End: 9 * 60, Allow: true}

	// Adding an allow window that opens now or within the freeze weakens.
	if !Weakens([]Window{evening}, []Window{evening, morning}, fri(7, 45), EditFreeze) {
		t.Error("adding an allow window opening in 15 minutes should weaken")
	}
	if Weakens([]Window{evening}, []Window{evening, morning}, fri(7, 0), EditFreeze) {
		t.Error("adding an allow window opening in an hour shouldn't count")
	}
	// The first allow window turns an unscheduled block on: never weaker.
	if Weakens(nil, []Window{evening}, fri(12, 0), EditFreeze) {
		t.Error("adding the first allow window shouldn't weaken")
	}
	// Removing one of several allow windows only strengthens.
	if Weakens([]Window{evening, morning}, []Window{evening}, fri(8, 30), EditFreeze) {
		t.Error("removing an allow window shouldn't weaken")
	}
	// Removing the last allow window while the block is on weakens.
	if !Weakens([]Window{evening}, nil, fri(12, 0), EditFreeze) {
		t.Error("removing the last allow window while blocked should weaken")
	}
	if Weakens([]Window{evening}, nil, fri(19, 0), EditFreeze) {
		t.Error("removing the last allow window inside it, well before it closes, is fine")
	}
}

func TestParseEndTime(t *testing.T) {
	if m, err := ParseEndTime("24:00"); err != nil || m != 1440 {
		t.Errorf("ParseEndTime(24:00) = %d, %v", m, err)
	}
	if m, err := ParseEndTime("17:30"); err != nil || m != 1050 {
		t.Errorf("ParseEndTime(17:30) = %d, %v", m, err)
	}
	if _, err := ParseEndTime("24:01"); err == nil {
		t.Error("ParseEndTime(24:01) should error")
	}
	if _, err := ParseTimeOfDay("24:00"); err == nil {
		t.Error("ParseTimeOfDay(24:00) should still error (start times)")
	}
	if got := (Window{Days: Friday | Saturday, Start: 1080, End: 1440, Allow: true}).String(); got != "allow fri,sat 18:00-24:00" {
		t.Errorf("String() = %q", got)
	}
}
