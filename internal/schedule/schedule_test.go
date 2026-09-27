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
