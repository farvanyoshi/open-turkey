// Package schedule holds the pure time logic for recurring block windows,
// shared by the CLI (parsing/formatting user input) and the daemon (deciding
// whether a window is active right now).
//
// A window is a set of weekdays plus a start and end time-of-day. Windows are
// same-day only (start must be before end); an overnight span like 22:00–06:00
// is expressed as two windows. Keeping it same-day avoids ambiguous
// "which day does the mask refer to" logic and keeps the daemon's decision
// trivial to reason about.
package schedule

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Day bits. These match time.Weekday (Sunday = 0) shifted into a bitmask.
const (
	Sunday    = 1 << time.Sunday
	Monday    = 1 << time.Monday
	Tuesday   = 1 << time.Tuesday
	Wednesday = 1 << time.Wednesday
	Thursday  = 1 << time.Thursday
	Friday    = 1 << time.Friday
	Saturday  = 1 << time.Saturday

	Weekdays = Monday | Tuesday | Wednesday | Thursday | Friday
	Weekend  = Saturday | Sunday
	AllDays  = Weekdays | Weekend
)

// Window is a recurring block window: the days it applies and the minute-of-day
// range [Start, End) it covers.
type Window struct {
	Days  int // bitmask of day constants
	Start int // minutes since midnight, inclusive
	End   int // minutes since midnight, exclusive
}

// ActiveAt reports whether the window covers the instant t.
func (w Window) ActiveAt(t time.Time) bool {
	if w.Days&(1<<uint(t.Weekday())) == 0 {
		return false
	}
	m := t.Hour()*60 + t.Minute()
	return m >= w.Start && m < w.End
}

// dayNames maps the abbreviations users type to day bits.
var dayNames = map[string]int{
	"sun": Sunday, "mon": Monday, "tue": Tuesday, "wed": Wednesday,
	"thu": Thursday, "fri": Friday, "sat": Saturday,
}

// orderedDays is used when formatting a mask back to text.
var orderedDays = []struct {
	name string
	bit  int
}{
	{"mon", Monday}, {"tue", Tuesday}, {"wed", Wednesday}, {"thu", Thursday},
	{"fri", Friday}, {"sat", Saturday}, {"sun", Sunday},
}

// ParseDays turns a day specification into a bitmask. Accepted forms:
//
//	"daily" / "everyday" / "all"     → every day
//	"weekdays"                       → Mon–Fri
//	"weekends"                       → Sat, Sun
//	"mon-fri"                        → a range
//	"mon,wed,fri"                    → a list
//
// Ranges and lists may be combined with commas (e.g. "mon-thu,sun").
func ParseDays(spec string) (int, error) {
	spec = strings.ToLower(strings.TrimSpace(spec))
	if spec == "" {
		return 0, fmt.Errorf("empty day specification")
	}
	switch spec {
	case "daily", "everyday", "all":
		return AllDays, nil
	case "weekdays", "weekday":
		return Weekdays, nil
	case "weekends", "weekend":
		return Weekend, nil
	}

	mask := 0
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.Contains(part, "-") {
			ends := strings.SplitN(part, "-", 2)
			from, ok1 := dayNames[strings.TrimSpace(ends[0])]
			to, ok2 := dayNames[strings.TrimSpace(ends[1])]
			if !ok1 || !ok2 {
				return 0, fmt.Errorf("invalid day range %q", part)
			}
			mask |= dayRange(from, to)
		} else {
			bit, ok := dayNames[part]
			if !ok {
				return 0, fmt.Errorf("invalid day %q (use mon,tue,...,sun)", part)
			}
			mask |= bit
		}
	}
	if mask == 0 {
		return 0, fmt.Errorf("no valid days in %q", spec)
	}
	return mask, nil
}

// dayRange returns the bitmask covering from..to inclusive, walking the week
// forward from `from` (so "fri-mon" wraps across the weekend).
func dayRange(from, to int) int {
	// Convert bits back to weekday indices.
	start := bitIndex(from)
	end := bitIndex(to)
	mask := 0
	for i := 0; i < 7; i++ {
		d := (start + i) % 7
		mask |= 1 << uint(d)
		if d == end {
			break
		}
	}
	return mask
}

func bitIndex(bit int) int {
	for i := 0; i < 7; i++ {
		if bit == 1<<uint(i) {
			return i
		}
	}
	return 0
}

// FormatDays renders a mask back to a compact, human-readable string.
func FormatDays(mask int) string {
	switch mask {
	case AllDays:
		return "daily"
	case Weekdays:
		return "weekdays"
	case Weekend:
		return "weekends"
	}
	var parts []string
	for _, d := range orderedDays {
		if mask&d.bit != 0 {
			parts = append(parts, d.name)
		}
	}
	return strings.Join(parts, ",")
}

// ParseTimeOfDay parses "HH:MM" (24-hour) into minutes since midnight.
func ParseTimeOfDay(s string) (int, error) {
	s = strings.TrimSpace(s)
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid time %q (want HH:MM)", s)
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil || h < 0 || h > 23 {
		return 0, fmt.Errorf("invalid hour in %q", s)
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 0 || m > 59 {
		return 0, fmt.Errorf("invalid minute in %q", s)
	}
	return h*60 + m, nil
}

// FormatMinutes renders minutes-since-midnight as "HH:MM".
func FormatMinutes(m int) string {
	return fmt.Sprintf("%02d:%02d", m/60, m%60)
}
