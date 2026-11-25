package cli

import (
	"fmt"
	"time"

	"github.com/brunodcdo/open-turkey/internal/db"
)

const blockTimeLayout = "2006-01-02 15:04"

// parseBlockTime resolves clock-only input after the reference time, using
// calendar days so an overnight window keeps its local time across DST.
func parseBlockTime(reference time.Time, flag, value string) (time.Time, error) {
	if date, err := time.ParseInLocation(blockTimeLayout, value, reference.Location()); err == nil && date.Format(blockTimeLayout) == value {
		if !date.After(reference) {
			return time.Time{}, fmt.Errorf("--%s time %q must be after %s", flag, value, reference.Format(blockTimeLayout))
		}
		return date, nil
	}

	clock, err := time.Parse("15:04", value)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid --%s time %q (want HH:MM or YYYY-MM-DD HH:MM)", flag, value)
	}
	day := reference
	at := func(day time.Time) time.Time {
		return time.Date(day.Year(), day.Month(), day.Day(), clock.Hour(), clock.Minute(), 0, 0, day.Location())
	}
	t := at(day)
	if !t.After(reference) {
		day = day.AddDate(0, 0, 1)
		t = at(day)
	}
	if t.Hour() != clock.Hour() || t.Minute() != clock.Minute() {
		return time.Time{}, fmt.Errorf("--%s time %q does not exist on %s in the local time zone", flag, value, day.Format(time.DateOnly))
	}
	return t, nil
}

func scheduleOneTime(name, from, until string, now time.Time) error {
	if until == "" {
		return fmt.Errorf("--from requires --until")
	}
	start, err := parseBlockTime(now, "from", from)
	if err != nil {
		return err
	}
	end, err := parseBlockTime(start, "until", until)
	if err != nil {
		return err
	}

	database, err := openDB()
	if err != nil {
		return err
	}
	defer database.Close()

	block, err := database.GetBlock(name)
	if err != nil {
		return err
	}
	if isEmpty(len(block.Sites), len(block.Apps), len(block.Patterns)) {
		return fmt.Errorf("block '%s' has nothing to enforce. Add sites, apps or patterns first", name)
	}
	if err := database.AddOneTimeWindow(name, start, end); err != nil {
		return err
	}
	fmt.Printf("Block '%s' scheduled once: %s\n", name, windowRange(db.OneTimeWindow{Start: start, End: end}))
	return nil
}

func windowRange(w db.OneTimeWindow) string {
	return fmt.Sprintf("%s to %s", w.Start.Format(blockTimeLayout), w.End.Format(blockTimeLayout))
}
