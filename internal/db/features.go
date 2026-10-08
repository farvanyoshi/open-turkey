// features.go — database support for the newer features: block modes,
// browser patterns, time locks and recurring schedules.
//
// The schema in db.go's `migrations` already declares the newer columns and
// tables for fresh databases. upgradeSchema here brings OLDER databases (made
// before those columns existed) up to date with idempotent ALTERs, so an
// existing install keeps working after an upgrade.
package db

import (
	"database/sql"
	"fmt"
)

// upgradeSchema adds any columns that older databases are missing. New tables
// (patterns, schedules) are handled by CREATE TABLE IF NOT EXISTS already.
func upgradeSchema(conn *sql.DB) error {
	adds := []struct{ table, col, def string }{
		{"blocks", "mode", "TEXT NOT NULL DEFAULT 'blocklist'"},
		{"active_blocks", "locked_until", "INTEGER NOT NULL DEFAULT 0"},
		{"active_blocks", "scheduled", "BOOLEAN NOT NULL DEFAULT 0"},
	}
	for _, a := range adds {
		if err := addColumnIfMissing(conn, a.table, a.col, a.def); err != nil {
			return err
		}
	}
	return nil
}

// addColumnIfMissing runs "ALTER TABLE ... ADD COLUMN" only when the column
// isn't already present, so it's safe to call on every open.
func addColumnIfMissing(conn *sql.DB, table, column, definition string) error {
	rows, err := conn.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return fmt.Errorf("failed to inspect table %s: %w", table, err)
	}
	defer rows.Close()

	found := false
	for rows.Next() {
		var (
			cid, notnull, pk int
			name, ctype      string
			dflt             sql.NullString
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return fmt.Errorf("failed to read columns of %s: %w", table, err)
		}
		if name == column {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if found {
		return nil
	}

	if _, err := conn.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, definition)); err != nil {
		return fmt.Errorf("failed to add column %s.%s: %w", table, column, err)
	}
	return nil
}

// --------------------------------------------------------------------------
// Block mode
// --------------------------------------------------------------------------

// SetBlockMode changes a block's mode (ModeBlocklist or ModeAllowlist).
func (d *DB) SetBlockMode(name, mode string) error {
	if mode != ModeBlocklist && mode != ModeAllowlist {
		return fmt.Errorf("invalid block mode '%s' (use '%s' or '%s')", mode, ModeBlocklist, ModeAllowlist)
	}
	result, err := d.conn.Exec("UPDATE blocks SET mode = ? WHERE name = ?", mode, name)
	if err != nil {
		return fmt.Errorf("failed to set mode for block '%s': %w", name, err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("block '%s' not found", name)
	}
	return nil
}

// --------------------------------------------------------------------------
// Browser patterns (wildcard / keyword filters, enforced at the browser layer)
// --------------------------------------------------------------------------

// getBlockPatterns returns a block's browser patterns by ID.
func (d *DB) getBlockPatterns(blockID int) ([]string, error) {
	rows, err := d.conn.Query("SELECT pattern FROM patterns WHERE block_id = ? ORDER BY pattern", blockID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch block patterns: %w", err)
	}
	defer rows.Close()

	var patterns []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("failed to read pattern: %w", err)
		}
		patterns = append(patterns, p)
	}
	return patterns, rows.Err()
}

// AddPatterns adds browser patterns to a block (transactional).
func (d *DB) AddPatterns(blockName string, patterns []string) error {
	blockID, err := d.getBlockID(blockName)
	if err != nil {
		return err
	}
	tx, err := d.conn.Begin()
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare("INSERT INTO patterns (block_id, pattern) VALUES (?, ?)")
	if err != nil {
		return fmt.Errorf("failed to prepare pattern insert: %w", err)
	}
	defer stmt.Close()

	for _, p := range patterns {
		if _, err := stmt.Exec(blockID, p); err != nil {
			return fmt.Errorf("failed to add pattern '%s' to block '%s': %w", p, blockName, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to save patterns for block '%s': %w", blockName, err)
	}
	return nil
}

// RemovePatterns removes browser patterns from a block, erroring if one isn't
// present (transactional, so a bad name leaves the block unchanged).
func (d *DB) RemovePatterns(blockName string, patterns []string) error {
	blockID, err := d.getBlockID(blockName)
	if err != nil {
		return err
	}
	tx, err := d.conn.Begin()
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare("DELETE FROM patterns WHERE block_id = ? AND pattern = ?")
	if err != nil {
		return fmt.Errorf("failed to prepare pattern removal: %w", err)
	}
	defer stmt.Close()

	for _, p := range patterns {
		result, err := stmt.Exec(blockID, p)
		if err != nil {
			return fmt.Errorf("failed to remove pattern '%s' from block '%s': %w", p, blockName, err)
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return fmt.Errorf("pattern '%s' not found in block '%s'", p, blockName)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to save pattern removal for block '%s': %w", blockName, err)
	}
	return nil
}

// --------------------------------------------------------------------------
// Schedules
// --------------------------------------------------------------------------

// Schedule is one recurring window attached to a block.
//
// Num is the number shown to the user: the schedule's 1-based position among
// its block's schedules (in creation order). It is derived, not stored, so the
// numbers are always 1..n with no gaps: a new schedule takes n+1, the lowest
// free number, and removing one shifts every later one down by one. ID is the
// internal row ID and is never shown.
type Schedule struct {
	ID        int
	Num       int
	BlockName string
	DaysMask  int
	StartMin  int
	EndMin    int
}

// AddSchedule attaches a recurring window to a block.
func (d *DB) AddSchedule(blockName string, daysMask, startMin, endMin int) error {
	blockID, err := d.getBlockID(blockName)
	if err != nil {
		return err
	}
	_, err = d.conn.Exec(
		"INSERT INTO schedules (block_id, days_mask, start_min, end_min) VALUES (?, ?, ?, ?)",
		blockID, daysMask, startMin, endMin,
	)
	if err != nil {
		return fmt.Errorf("failed to add schedule to block '%s': %w", blockName, err)
	}
	return nil
}

// ListSchedules returns the schedules for one block.
func (d *DB) ListSchedules(blockName string) ([]Schedule, error) {
	blockID, err := d.getBlockID(blockName)
	if err != nil {
		return nil, err
	}
	rows, err := d.conn.Query(
		"SELECT id, days_mask, start_min, end_min FROM schedules WHERE block_id = ? ORDER BY id",
		blockID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list schedules for '%s': %w", blockName, err)
	}
	defer rows.Close()

	var out []Schedule
	for rows.Next() {
		s := Schedule{BlockName: blockName, Num: len(out) + 1}
		if err := rows.Scan(&s.ID, &s.DaysMask, &s.StartMin, &s.EndMin); err != nil {
			return nil, fmt.Errorf("failed to read schedule: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// AllSchedules returns every schedule across all blocks. The daemon uses it to
// decide which blocks to auto-activate or auto-deactivate.
func (d *DB) AllSchedules() ([]Schedule, error) {
	rows, err := d.conn.Query(`
		SELECT s.id, b.name, s.days_mask, s.start_min, s.end_min
		FROM schedules s
		JOIN blocks b ON b.id = s.block_id
		ORDER BY b.name, s.id
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch all schedules: %w", err)
	}
	defer rows.Close()

	var out []Schedule
	for rows.Next() {
		var s Schedule
		if err := rows.Scan(&s.ID, &s.BlockName, &s.DaysMask, &s.StartMin, &s.EndMin); err != nil {
			return nil, fmt.Errorf("failed to read schedule: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// GetSchedule returns a block's schedule by its number (Num, as shown in
// "schedule list").
func (d *DB) GetSchedule(blockName string, num int) (Schedule, error) {
	schedules, err := d.ListSchedules(blockName)
	if err != nil {
		return Schedule{}, err
	}
	if num < 1 || num > len(schedules) {
		return Schedule{}, fmt.Errorf("schedule %d not found in block '%s'", num, blockName)
	}
	return schedules[num-1], nil
}

// RemoveSchedule deletes one schedule from a block by its number (Num, as
// shown in "schedule list"). Later schedules move down one number.
func (d *DB) RemoveSchedule(blockName string, num int) error {
	s, err := d.GetSchedule(blockName, num)
	if err != nil {
		return err
	}
	if _, err := d.conn.Exec("DELETE FROM schedules WHERE id = ?", s.ID); err != nil {
		return fmt.Errorf("failed to remove schedule %d from block '%s': %w", num, blockName, err)
	}
	return nil
}
