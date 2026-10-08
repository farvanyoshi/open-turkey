// pending.go — queued edits that loosen a block.
//
// A block with allow windows can't be loosened by a plain edit at any time
// (otherwise its own allow window would be the moment to dismantle it). The
// CLI instead queues the edit here with a time to apply it, and the daemon
// applies it once that time comes (see enforce.Tick). Queued edits can be
// listed and cancelled; they are deleted with their block.
package db

import (
	"encoding/json"
	"fmt"

	"github.com/brunodcdo/open-turkey/internal/schedule"
)

// Edit actions that can be queued.
const (
	EditAddSites       = "add-site"
	EditRemoveSites    = "remove-site"
	EditRemoveApps     = "remove-app"
	EditRemovePatterns = "remove-pattern"
	EditSetMode        = "set-mode"
	EditRemoveBlock    = "remove-block"
	EditAddSchedule    = "add-schedule"
	EditRemoveSchedule = "remove-schedule"
)

// Edit is one change to a block. Values holds the sites, apps or patterns (or
// the mode, for EditSetMode); Window the schedule, for the schedule actions.
type Edit struct {
	Action string          `json:"-"`
	Values []string        `json:"values,omitempty"`
	Window schedule.Window `json:"window"`
}

// String describes the edit for listings, e.g. "remove-app steam".
func (e Edit) String() string {
	switch e.Action {
	case EditRemoveBlock:
		return "remove the block"
	case EditAddSchedule, EditRemoveSchedule:
		return fmt.Sprintf("%s %s", e.Action, e.Window)
	}
	s := e.Action
	for _, v := range e.Values {
		s += " " + v
	}
	return s
}

// PendingEdit is a queued edit. Num is its 1-based position in the queue
// (ordered by when it applies), as shown by "pending list"; ApplyAt is unix
// seconds.
type PendingEdit struct {
	ID        int
	Num       int
	BlockName string
	Edit      Edit
	ApplyAt   int64
}

// QueueEdit queues an edit to a block, to be applied at applyAt (unix seconds).
func (d *DB) QueueEdit(blockName string, e Edit, applyAt int64) error {
	blockID, err := d.getBlockID(blockName)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("failed to encode edit: %w", err)
	}
	_, err = d.conn.Exec(
		"INSERT INTO pending_edits (block_id, action, payload, apply_at) VALUES (?, ?, ?, ?)",
		blockID, e.Action, string(payload), applyAt,
	)
	if err != nil {
		return fmt.Errorf("failed to queue edit to block '%s': %w", blockName, err)
	}
	return nil
}

// ListPendingEdits returns every queued edit, soonest first.
func (d *DB) ListPendingEdits() ([]PendingEdit, error) {
	rows, err := d.conn.Query(`
		SELECT p.id, b.name, p.action, p.payload, p.apply_at
		FROM pending_edits p
		JOIN blocks b ON b.id = p.block_id
		ORDER BY p.apply_at, p.id
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to list pending edits: %w", err)
	}
	defer rows.Close()

	var out []PendingEdit
	for rows.Next() {
		p := PendingEdit{Num: len(out) + 1}
		var payload string
		if err := rows.Scan(&p.ID, &p.BlockName, &p.Edit.Action, &payload, &p.ApplyAt); err != nil {
			return nil, fmt.Errorf("failed to read pending edit: %w", err)
		}
		if err := json.Unmarshal([]byte(payload), &p.Edit); err != nil {
			return nil, fmt.Errorf("failed to decode pending edit %d: %w", p.ID, err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeletePendingEdit removes a queued edit by its row ID.
func (d *DB) DeletePendingEdit(id int) error {
	if _, err := d.conn.Exec("DELETE FROM pending_edits WHERE id = ?", id); err != nil {
		return fmt.Errorf("failed to delete pending edit: %w", err)
	}
	return nil
}

// ApplyEdit performs an edit on a block. Removing a block deactivates it
// first; any other edit that leaves an active block with nothing to enforce
// deactivates it, as the CLI does.
func (d *DB) ApplyEdit(blockName string, e Edit) error {
	var err error
	switch e.Action {
	case EditAddSites:
		err = d.AddSites(blockName, e.Values)
	case EditRemoveSites:
		err = d.RemoveSites(blockName, e.Values)
	case EditRemoveApps:
		err = d.RemoveApps(blockName, e.Values)
	case EditRemovePatterns:
		err = d.RemovePatterns(blockName, e.Values)
	case EditSetMode:
		if len(e.Values) != 1 {
			return fmt.Errorf("set-mode needs exactly one mode")
		}
		err = d.SetBlockMode(blockName, e.Values[0])
	case EditAddSchedule:
		err = d.AddSchedule(blockName, e.Window)
	case EditRemoveSchedule:
		err = d.removeScheduleWindow(blockName, e.Window)
	case EditRemoveBlock:
		active, err := d.IsBlockActive(blockName)
		if err != nil {
			return err
		}
		if active {
			if err := d.DeactivateBlock(blockName); err != nil {
				return err
			}
		}
		return d.RemoveBlock(blockName)
	default:
		return fmt.Errorf("unknown edit action %q", e.Action)
	}
	if err != nil {
		return err
	}

	detail, err := d.GetBlock(blockName)
	if err != nil {
		return err
	}
	if detail.Active && len(detail.Sites) == 0 && len(detail.Apps) == 0 && len(detail.Patterns) == 0 {
		return d.DeactivateBlock(blockName)
	}
	return nil
}
