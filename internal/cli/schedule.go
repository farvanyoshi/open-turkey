// schedule.go — recurring block schedules.
//
//	open-turkey schedule add <block> --days weekdays --from 09:00 --to 17:00
//	open-turkey schedule list <block>
//	open-turkey schedule remove <block> <id>
//
// While a scheduled window is open, the daemon keeps the block active and the
// CLI refuses to stop/unlock it (see blockHold). "schedule remove" likewise
// refuses to delete a schedule whose window is open or opens within
// schedule.EditFreeze, so removing a schedule can't be used to end the block
// early or stop it from starting. Windows are same-day; express an overnight
// span as two windows.
package cli

import (
	"fmt"
	"strconv"
	"time"

	"github.com/brunodcdo/open-turkey/internal/schedule"
	"github.com/spf13/cobra"
)

// Thin wrappers so other cli files can format schedules without importing the
// schedule package directly.
func scheduleFormatDays(mask int) string { return schedule.FormatDays(mask) }
func scheduleFormatMinutes(m int) string { return schedule.FormatMinutes(m) }

var scheduleCmd = &cobra.Command{
	Use:   "schedule",
	Short: "Manage recurring block schedules",
}

var scheduleAddCmd = &cobra.Command{
	Use:   "add [block]",
	Short: "Add a recurring window to a block",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		daysSpec, _ := cmd.Flags().GetString("days")
		fromSpec, _ := cmd.Flags().GetString("from")
		toSpec, _ := cmd.Flags().GetString("to")

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
		end, err := schedule.ParseTimeOfDay(toSpec)
		if err != nil {
			return err
		}
		if start >= end {
			return fmt.Errorf("--from (%s) must be before --to (%s); for an overnight window add two schedules", fromSpec, toSpec)
		}

		database, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close()

		// Confirm the block exists (GetBlock errors otherwise).
		if _, err := database.GetBlock(name); err != nil {
			return err
		}
		if err := database.AddSchedule(name, days, start, end); err != nil {
			return err
		}
		fmt.Printf("Schedule added to '%s': %s %s-%s\n", name, schedule.FormatDays(days), fromSpec, toSpec)
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
			fmt.Printf("  [%d] %s %s-%s\n", s.ID, schedule.FormatDays(s.DaysMask),
				schedule.FormatMinutes(s.StartMin), schedule.FormatMinutes(s.EndMin))
		}
		return nil
	},
}

var scheduleRemoveCmd = &cobra.Command{
	Use:   "remove [block] [id]",
	Short: "Remove a schedule from a block by its id",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		id, err := strconv.Atoi(args[1])
		if err != nil {
			return fmt.Errorf("invalid schedule id %q", args[1])
		}
		database, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close()

		// Don't let removing a schedule become a way around its own window:
		// deleting the schedule that is holding the block (or is about to)
		// would make the daemon deactivate it, or never activate it. So a
		// schedule whose window is open or opens within schedule.EditFreeze
		// can't be removed; other schedules still can.
		schedules, err := database.ListSchedules(name)
		if err != nil {
			return err
		}
		for _, s := range schedules {
			if s.ID != id {
				continue
			}
			w := schedule.Window{Days: s.DaysMask, Start: s.StartMin, End: s.EndMin}
			if w.ActiveWithin(time.Now(), schedule.EditFreeze) {
				return fmt.Errorf("schedule %d for '%s' is in its window or starts within %s, and can't be removed until the window closes", id, name, freezeLead())
			}
			break
		}

		if err := database.RemoveSchedule(name, id); err != nil {
			return err
		}
		fmt.Printf("Schedule %d removed from '%s'.\n", id, name)
		return nil
	},
}

func init() {
	scheduleAddCmd.Flags().String("days", "daily", "Days: daily, weekdays, weekends, mon-fri, or mon,wed,fri")
	scheduleAddCmd.Flags().String("from", "", "Start time HH:MM (required)")
	scheduleAddCmd.Flags().String("to", "", "End time HH:MM (required)")

	scheduleCmd.AddCommand(scheduleAddCmd)
	scheduleCmd.AddCommand(scheduleListCmd)
	scheduleCmd.AddCommand(scheduleRemoveCmd)
	rootCmd.AddCommand(scheduleCmd)
}
