// schedule.go — recurring block schedules.
//
//	open-turkey schedule add <block> --days weekdays --from 09:00 --to 17:00
//	open-turkey schedule add <block> --allow --days fri,sat --from 18:00 --to 24:00
//	open-turkey schedule list <block>
//	open-turkey schedule remove <block> <number>
//
// A block's schedules are either block windows (the block is on while one is
// open) or allow windows (the block is on at all times except while one is
// open); a block can't mix the two. While its schedule enforces it, the daemon
// keeps the block active and the CLI refuses to stop/unlock it (see blockHold).
//
// Schedule changes that would leave the block off when it would otherwise be on
// (removing a block window, adding an allow window, removing the last allow
// window) are refused if that would take effect now or within
// schedule.EditFreeze, so they can't be used to end the block early or stop it
// from starting. Windows are same-day (the end may be 24:00); express an
// overnight span as two windows.
//
// Schedules are numbered 1..n per block; removing one renumbers the later
// ones down, and a new schedule takes the next (lowest free) number.
package cli

import (
	"fmt"
	"strconv"
	"time"

	"github.com/brunodcdo/open-turkey/internal/db"
	"github.com/brunodcdo/open-turkey/internal/schedule"
	"github.com/spf13/cobra"
)

var scheduleCmd = &cobra.Command{
	Use:   "schedule",
	Short: "Manage recurring block schedules",
}

var scheduleAddCmd = &cobra.Command{
	Use:   "add [block]",
	Short: "Add a recurring block window (or, with --allow, an allow window) to a block",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		daysSpec, _ := cmd.Flags().GetString("days")
		fromSpec, _ := cmd.Flags().GetString("from")
		toSpec, _ := cmd.Flags().GetString("to")
		allow, _ := cmd.Flags().GetBool("allow")

		if fromSpec == "" || toSpec == "" {
			return fmt.Errorf("--from and --to are required (HH:MM)")
		}
		days, err := schedule.ParseDays(daysSpec)
		if err != nil {
			return err
		}
		start, err := schedule.ParseTimeOfDay(fromSpec)
		if err != nil {
			return err
		}
		end, err := schedule.ParseEndTime(toSpec)
		if err != nil {
			return err
		}
		if start >= end {
			return fmt.Errorf("--from (%s) must be before --to (%s); for an overnight window add two schedules", fromSpec, toSpec)
		}
		w := schedule.Window{Days: days, Start: start, End: end, Allow: allow}

		database, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close()

		// ListSchedules also confirms the block exists.
		schedules, err := database.ListSchedules(name)
		if err != nil {
			return err
		}
		// An allow window opens a gap in the block. On a block that already
		// has allow windows that is a loosening edit (queued, or --now with
		// the typing challenge). Otherwise it can't be added if the gap would
		// start now or within schedule.EditFreeze.
		if allow && len(schedules) > 0 && schedules[0].Allow {
			proceed, err := gateEdit(cmd, database, name, false, db.Edit{Action: db.EditAddSchedule, Window: w})
			if err != nil || !proceed {
				return err
			}
		} else {
			from := db.Windows(schedules)
			if schedule.Weakens(from, append(from, w), time.Now(), schedule.EditFreeze) {
				return fmt.Errorf("that allow window would lift '%s' now or within %s; add it at least %s before it opens", name, freezeLead(), freezeLead())
			}
		}

		if err := database.AddSchedule(name, w); err != nil {
			return err
		}
		fmt.Printf("Schedule %d added to '%s': %s\n", len(schedules)+1, name, w)
		if allow && len(schedules) == 0 {
			fmt.Printf("'%s' will now be on at all times except during its allow windows.\n", name)
		}
		return nil
	},
}

var scheduleListCmd = &cobra.Command{
	Use:   "list [block]",
	Short: "List a block's schedules",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		database, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close()

		schedules, err := database.ListSchedules(name)
		if err != nil {
			return err
		}
		if len(schedules) == 0 {
			fmt.Printf("Block '%s' has no schedules.\n", name)
			return nil
		}
		fmt.Printf("Schedules for '%s':\n", name)
		for _, s := range schedules {
			fmt.Printf("  [%d] %s\n", s.Num, s.Window())
		}
		return nil
	},
}

var scheduleRemoveCmd = &cobra.Command{
	Use:   "remove [block] [number]",
	Short: "Remove a schedule from a block by its number (later ones are renumbered)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		num, err := strconv.Atoi(args[1])
		if err != nil {
			return fmt.Errorf("invalid schedule number %q", args[1])
		}
		database, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close()

		// Don't let removing a schedule become a way around it: removing the
		// block window that is holding the block (or is about to), or the
		// last allow window while the block is on, would make the daemon
		// deactivate it, or never activate it. So a removal that would lift
		// the block now or within schedule.EditFreeze is refused; others are
		// fine (removing one of several allow windows only strengthens it).
		schedules, err := database.ListSchedules(name)
		if err != nil {
			return err
		}
		if num < 1 || num > len(schedules) {
			return fmt.Errorf("schedule %d not found in block '%s'", num, name)
		}
		from := db.Windows(schedules)
		to := append(append([]schedule.Window{}, from[:num-1]...), from[num:]...)
		switch {
		case from[num-1].Allow && len(schedules) == 1:
			// Removing the last allow window leaves the block unscheduled:
			// a loosening edit (queued, or --now with the typing challenge).
			proceed, err := gateEdit(cmd, database, name, false, db.Edit{Action: db.EditRemoveSchedule, Window: from[num-1]})
			if err != nil || !proceed {
				return err
			}
		case from[num-1].Allow:
			// Removing one of several allow windows only makes it stricter.
		case schedule.Weakens(from, to, time.Now(), schedule.EditFreeze):
			return fmt.Errorf("schedule %d for '%s' is in its window or starts within %s, and can't be removed until the window closes", num, name, freezeLead())
		}

		if err := database.RemoveSchedule(name, num); err != nil {
			return err
		}
		fmt.Printf("Schedule %d removed from '%s'.\n", num, name)
		return nil
	},
}

func init() {
	scheduleAddCmd.Flags().String("days", "daily", "Days: daily, weekdays, weekends, mon-fri, or mon,wed,fri")
	scheduleAddCmd.Flags().String("from", "", "Start time HH:MM (required)")
	scheduleAddCmd.Flags().String("to", "", "End time HH:MM, or 24:00 for midnight (required)")
	scheduleAddCmd.Flags().Bool("allow", false, "Allow window: keep the block on at all times except during its allow windows")

	addNowFlag(scheduleAddCmd, scheduleRemoveCmd)

	scheduleCmd.AddCommand(scheduleAddCmd)
	scheduleCmd.AddCommand(scheduleListCmd)
	scheduleCmd.AddCommand(scheduleRemoveCmd)
	rootCmd.AddCommand(scheduleCmd)
}
