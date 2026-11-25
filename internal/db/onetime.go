package db

import (
	"fmt"
	"time"
)

// AddOneTimeWindow saves a non-recurring blocking window.
func (d *DB) AddOneTimeWindow(name string, start, end time.Time) error {
	id, err := d.getBlockID(name)
	if err != nil {
		return err
	}
	_, err = d.conn.Exec(`
		INSERT INTO one_time_windows (block_id, starts_at, ends_at) VALUES (?, ?, ?)
		ON CONFLICT (block_id, starts_at, ends_at) DO NOTHING
	`, id, start.Unix(), end.Unix())
	if err != nil {
		return fmt.Errorf("failed to schedule block '%s': %w", name, err)
	}
	return nil
}

// OneTimeWindows lists a block's windows that have yet to end. The name must
// refer to an existing block.
func (d *DB) OneTimeWindows(name string, now time.Time) ([]OneTimeWindow, error) {
	rows, err := d.conn.Query(`
		SELECT w.starts_at, w.ends_at FROM one_time_windows w
		JOIN blocks b ON b.id = w.block_id
		WHERE b.name = ? AND w.ends_at > ?
		ORDER BY w.starts_at, w.ends_at
	`, name, now.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var windows []OneTimeWindow
	for rows.Next() {
		var start, end int64
		if err := rows.Scan(&start, &end); err != nil {
			return nil, err
		}
		windows = append(windows, OneTimeWindow{Start: time.Unix(start, 0), End: time.Unix(end, 0)})
	}
	return windows, rows.Err()
}

type OneTimeWindow struct {
	Start time.Time
	End   time.Time
}

func (w OneTimeWindow) Includes(now time.Time) bool {
	return !now.Before(w.Start) && now.Before(w.End)
}

// OneTimeBlocks returns the names of blocks with a window open at now.
func (d *DB) OneTimeBlocks(now time.Time) ([]string, error) {
	rows, err := d.conn.Query(`
		SELECT DISTINCT b.name FROM one_time_windows w
		JOIN blocks b ON b.id = w.block_id
		WHERE w.starts_at <= ? AND w.ends_at > ?
	`, now.Unix(), now.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// PruneOneTimeWindows drops completed windows. It leaves future windows intact.
func (d *DB) PruneOneTimeWindows(now time.Time) error {
	_, err := d.conn.Exec("DELETE FROM one_time_windows WHERE ends_at <= ?", now.Unix())
	return err
}
