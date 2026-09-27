// Package db provides the SQLite database access layer for Open Turkey.
//
// We use the "modernc.org/sqlite" driver because it's a pure Go
// implementation, meaning it needs neither CGo nor C libraries installed on
// the system. That makes cross-compiling and distributing the final binary
// easier.
package db

import (
	"database/sql"
	"fmt"
	"strings"

	// Import the pure-Go SQLite driver. The underscore (_) in front means we
	// won't call functions from this package directly — it registers itself
	// as a SQL driver when imported. It's a common pattern in Go.
	_ "modernc.org/sqlite"
)

// DefaultDBPath is the default path where the database is stored.
// We use /var/lib/ because it's the standard Linux directory for persistent
// application data (following the Filesystem Hierarchy Standard — FHS).
const DefaultDBPath = "/var/lib/open-turkey/open-turkey.db"

// --------------------------------------------------------------------------
// Types (structs) — represent the data flowing between the database and the app
// --------------------------------------------------------------------------

// DB wraps the database connection.
// We wrap it instead of exposing *sql.DB directly to control which operations
// are allowed and keep the public interface clean.
type DB struct {
	conn *sql.DB
}

// Block represents a block in the general listing.
// SiteCount and AppCount avoid loading every domain/app just to count them.
type Block struct {
	ID        int
	Name      string
	SiteCount int
	AppCount  int
	CreatedAt string
}

// BlockDetail carries all the information about a specific block,
// including the full lists of blocked sites and apps.
type BlockDetail struct {
	ID        int
	Name      string
	Sites     []string
	Apps      []string
	Active    bool
	Locked    bool
	LockChars int
	CreatedAt string
}

// ActiveBlockDetail represents a block that is currently active.
// The daemon uses it to know what to block.
type ActiveBlockDetail struct {
	BlockName string
	Sites     []string
	Apps      []string
	Locked    bool
	LockChars int
}

// --------------------------------------------------------------------------
// Migrations (database schema)
// --------------------------------------------------------------------------

// migrations contains the SQL that creates the database tables.
// We put everything in a single string because it's simple and direct.
//
// Schema design decisions:
//   - blocks: the central table. The "name" field is UNIQUE to avoid duplicate blocks.
//   - sites: domains associated with a block. ON DELETE CASCADE ensures that
//     when a block is deleted, its sites are removed automatically.
//   - apps: processes associated with a block. Same CASCADE logic.
//   - active_blocks: records which blocks are currently active.
//     The "locked" field indicates whether the user locked the block (can't
//     be easily deactivated).
//     The "lock_chars" field stores how many random characters the user must
//     type to unlock (a "friction" mechanism to prevent impulsive
//     deactivation). block_id is UNIQUE because a block can only be active
//     once.
const migrations = `
CREATE TABLE IF NOT EXISTS blocks (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT UNIQUE NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS sites (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    block_id INTEGER NOT NULL,
    domain   TEXT NOT NULL,
    FOREIGN KEY (block_id) REFERENCES blocks(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS apps (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    block_id     INTEGER NOT NULL,
    process_name TEXT NOT NULL,
    FOREIGN KEY (block_id) REFERENCES blocks(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS active_blocks (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    block_id     INTEGER UNIQUE NOT NULL,
    locked       BOOLEAN NOT NULL DEFAULT 0,
    lock_chars   INTEGER NOT NULL DEFAULT 0,
    activated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (block_id) REFERENCES blocks(id) ON DELETE CASCADE
);
`

// --------------------------------------------------------------------------
// Opening and closing the database
// --------------------------------------------------------------------------

// OpenDB opens (or creates) the SQLite database at the given path.
//
// Important steps that happen here:
//  1. Open the connection using the "sqlite" driver (registered by modernc.org/sqlite).
//  2. Enable foreign keys — by default SQLite does NOT enforce foreign keys!
//     Without this PRAGMA, ON DELETE CASCADE wouldn't work.
//  3. Enable WAL mode (Write-Ahead Logging) — this allows concurrent reads and
//     writes without locking. It's much better than the default (journal) mode
//     for applications that read and write at the same time (e.g. daemon + CLI).
//  4. Run the migrations to ensure the tables exist.
func OpenDB(dbPath string) (*DB, error) {
	// "sqlite" is the name of the driver registered by modernc.org/sqlite.
	// dbPath is the path of the .db file on disk.
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database at '%s': %w", dbPath, err)
	}

	// Check that the connection actually works.
	// sql.Open() doesn't really connect — it only validates the parameters.
	// Ping() forces a real connection.
	if err := conn.Ping(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// Enable foreign keys. Without this, SQLite ignores FOREIGN KEY
	// and CASCADE doesn't work. This is one of SQLite's most common "gotchas".
	if _, err := conn.Exec("PRAGMA foreign_keys = ON"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	// WAL (Write-Ahead Logging) lets reads happen while a write is in
	// progress. In the default (journal) mode, the database locks completely
	// during writes.
	if _, err := conn.Exec("PRAGMA journal_mode = WAL"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to enable WAL mode: %w", err)
	}

	// Run the migrations to create the tables if they don't exist.
	// "IF NOT EXISTS" makes running this several times safe (idempotent).
	if _, err := conn.Exec(migrations); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to create database tables: %w", err)
	}

	return &DB{conn: conn}, nil
}

// Close closes the database connection.
// Always call Close() when you're done using the database (defer db.Close()).
func (d *DB) Close() error {
	if err := d.conn.Close(); err != nil {
		return fmt.Errorf("failed to close database: %w", err)
	}
	return nil
}

// --------------------------------------------------------------------------
// Block CRUD
// --------------------------------------------------------------------------

// CreateBlock creates a new block with the given name.
// The name must be unique — if it already exists, the database returns an error.
func (d *DB) CreateBlock(name string) error {
	_, err := d.conn.Exec("INSERT INTO blocks (name) VALUES (?)", name)
	if err != nil {
		// If it's a UNIQUE constraint error, give a clearer message.
		if strings.Contains(err.Error(), "UNIQUE") {
			return fmt.Errorf("a block named '%s' already exists", name)
		}
		return fmt.Errorf("failed to create block '%s': %w", name, err)
	}
	return nil
}

// AddSites adds a list of domains to an existing block.
//
// We use a transaction (tx) to guarantee that either ALL domains are
// inserted, or NONE are. This avoids inconsistent states — for example, if
// the third domain fails, the first two are undone (rollback).
func (d *DB) AddSites(blockName string, domains []string) error {
	// First, look up the block ID by name.
	blockID, err := d.getBlockID(blockName)
	if err != nil {
		return err
	}

	// Start a transaction.
	tx, err := d.conn.Begin()
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}
	// defer tx.Rollback() is safe even if tx.Commit() was already called.
	// If Commit() succeeded, Rollback() simply does nothing.
	defer tx.Rollback()

	// Prepare the statement once and reuse it for each domain.
	// This is more efficient than running a separate INSERT for each one.
	stmt, err := tx.Prepare("INSERT INTO sites (block_id, domain) VALUES (?, ?)")
	if err != nil {
		return fmt.Errorf("failed to prepare site insert: %w", err)
	}
	defer stmt.Close()

	for _, domain := range domains {
		if _, err := stmt.Exec(blockID, domain); err != nil {
			return fmt.Errorf("failed to add domain '%s' to block '%s': %w", domain, blockName, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to save sites for block '%s': %w", blockName, err)
	}

	return nil
}

// AddApps adds a list of process names to an existing block.
// The logic is identical to AddSites — we use a transaction for the same reason.
func (d *DB) AddApps(blockName string, processNames []string) error {
	blockID, err := d.getBlockID(blockName)
	if err != nil {
		return err
	}

	tx, err := d.conn.Begin()
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare("INSERT INTO apps (block_id, process_name) VALUES (?, ?)")
	if err != nil {
		return fmt.Errorf("failed to prepare app insert: %w", err)
	}
	defer stmt.Close()

	for _, name := range processNames {
		if _, err := stmt.Exec(blockID, name); err != nil {
			return fmt.Errorf("failed to add process '%s' to block '%s': %w", name, blockName, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to save apps for block '%s': %w", blockName, err)
	}

	return nil
}

// RemoveSites removes a list of domains from an existing block.
//
// If any domain doesn't exist in the block, it returns an error and the whole
// removal is rolled back.
func (d *DB) RemoveSites(blockName string, domains []string) error {
	blockID, err := d.getBlockID(blockName)
	if err != nil {
		return err
	}

	tx, err := d.conn.Begin()
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare("DELETE FROM sites WHERE block_id = ? AND domain = ?")
	if err != nil {
		return fmt.Errorf("failed to prepare site removal: %w", err)
	}
	defer stmt.Close()

	for _, domain := range domains {
		result, err := stmt.Exec(blockID, domain)
		if err != nil {
			return fmt.Errorf("failed to remove domain '%s' from block '%s': %w", domain, blockName, err)
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("failed to verify removal of domain '%s': %w", domain, err)
		}
		if rowsAffected == 0 {
			return fmt.Errorf("domain '%s' not found in block '%s'", domain, blockName)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to save site removal for block '%s': %w", blockName, err)
	}

	return nil
}

// RemoveApps removes a list of process names from an existing block.
//
// If any process doesn't exist in the block, it returns an error and the
// whole removal is rolled back.
func (d *DB) RemoveApps(blockName string, processNames []string) error {
	blockID, err := d.getBlockID(blockName)
	if err != nil {
		return err
	}

	tx, err := d.conn.Begin()
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare("DELETE FROM apps WHERE block_id = ? AND process_name = ?")
	if err != nil {
		return fmt.Errorf("failed to prepare app removal: %w", err)
	}
	defer stmt.Close()

	for _, name := range processNames {
		result, err := stmt.Exec(blockID, name)
		if err != nil {
			return fmt.Errorf("failed to remove process '%s' from block '%s': %w", name, blockName, err)
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("failed to verify removal of process '%s': %w", name, err)
		}
		if rowsAffected == 0 {
			return fmt.Errorf("process '%s' not found in block '%s'", name, blockName)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to save app removal for block '%s': %w", blockName, err)
	}

	return nil
}

// ListBlocks returns every registered block with its site and app counts.
//
// We use LEFT JOIN + COUNT(DISTINCT ...) to fetch everything in a single query.
// LEFT JOIN ensures blocks with no sites or apps also appear (with count 0).
// DISTINCT avoids double counting when there are multiple joins.
func (d *DB) ListBlocks() ([]Block, error) {
	query := `
		SELECT
			b.id,
			b.name,
			COUNT(DISTINCT s.id) AS site_count,
			COUNT(DISTINCT a.id) AS app_count,
			b.created_at
		FROM blocks b
		LEFT JOIN sites s ON s.block_id = b.id
		LEFT JOIN apps  a ON a.block_id = b.id
		GROUP BY b.id
		ORDER BY b.name
	`

	rows, err := d.conn.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to list blocks: %w", err)
	}
	// defer rows.Close() is required to release the cursor's resources.
	// Without it, the connection can get "stuck" and cause deadlocks.
	defer rows.Close()

	var blocks []Block
	for rows.Next() {
		var b Block
		if err := rows.Scan(&b.ID, &b.Name, &b.SiteCount, &b.AppCount, &b.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to read block data: %w", err)
		}
		blocks = append(blocks, b)
	}

	// rows.Err() checks whether any error occurred during iteration.
	// It's different from the error returned by rows.Next() — it covers
	// network errors, etc.
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate blocks: %w", err)
	}

	return blocks, nil
}

// GetBlock returns the full details of a block: sites, apps, activation state.
func (d *DB) GetBlock(name string) (*BlockDetail, error) {
	// Fetch the block's basic data + activation info (if any).
	// LEFT JOIN with active_blocks because the block may not be active.
	var detail BlockDetail
	err := d.conn.QueryRow(`
		SELECT
			b.id,
			b.name,
			b.created_at,
			CASE WHEN ab.id IS NOT NULL THEN 1 ELSE 0 END AS active,
			COALESCE(ab.locked, 0) AS locked,
			COALESCE(ab.lock_chars, 0) AS lock_chars
		FROM blocks b
		LEFT JOIN active_blocks ab ON ab.block_id = b.id
		WHERE b.name = ?
	`, name).Scan(
		&detail.ID,
		&detail.Name,
		&detail.CreatedAt,
		&detail.Active,
		&detail.Locked,
		&detail.LockChars,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("block '%s' not found", name)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to fetch block '%s': %w", name, err)
	}

	// Fetch the block's sites.
	detail.Sites, err = d.getBlockSites(detail.ID)
	if err != nil {
		return nil, err
	}

	// Fetch the block's apps.
	detail.Apps, err = d.getBlockApps(detail.ID)
	if err != nil {
		return nil, err
	}

	return &detail, nil
}

// RemoveBlock deletes a block by name.
// If the block is active, removal is prevented — the user must deactivate it
// before removing it. This avoids accidental removal while the block is in
// effect.
func (d *DB) RemoveBlock(name string) error {
	// Check whether the block is active before trying to remove it.
	active, err := d.IsBlockActive(name)
	if err != nil {
		return err
	}
	if active {
		return fmt.Errorf("cannot remove block '%s' because it is active. Deactivate it first", name)
	}

	result, err := d.conn.Exec("DELETE FROM blocks WHERE name = ?", name)
	if err != nil {
		return fmt.Errorf("failed to remove block '%s': %w", name, err)
	}

	// Check whether any row was actually deleted.
	// If RowsAffected() == 0, the block didn't exist.
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to verify removal of block '%s': %w", name, err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("block '%s' not found", name)
	}

	return nil
}

// --------------------------------------------------------------------------
// Block activation and deactivation
// --------------------------------------------------------------------------

// ActivateBlock marks a block as active. This makes the sites and apps in
// that block start being blocked by the daemon.
//
// Parameters:
//   - locked: if true, the block can't be easily deactivated.
//   - lockChars: how many random characters the user must type to unlock.
//     The more characters, the more "friction" to deactivate.
func (d *DB) ActivateBlock(name string, locked bool, lockChars int) error {
	blockID, err := d.getBlockID(name)
	if err != nil {
		return err
	}

	_, err = d.conn.Exec(
		"INSERT INTO active_blocks (block_id, locked, lock_chars) VALUES (?, ?, ?)",
		blockID, locked, lockChars,
	)
	if err != nil {
		// If the block is already active, the UNIQUE block_id will cause an error.
		if strings.Contains(err.Error(), "UNIQUE") {
			return fmt.Errorf("block '%s' is already active", name)
		}
		return fmt.Errorf("failed to activate block '%s': %w", name, err)
	}

	return nil
}

// DeactivateBlock removes the block from the active blocks table.
// If the block isn't active, it returns an error.
func (d *DB) DeactivateBlock(name string) error {
	blockID, err := d.getBlockID(name)
	if err != nil {
		return err
	}

	result, err := d.conn.Exec("DELETE FROM active_blocks WHERE block_id = ?", blockID)
	if err != nil {
		return fmt.Errorf("failed to deactivate block '%s': %w", name, err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to verify deactivation of block '%s': %w", name, err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("block '%s' is not active", name)
	}

	return nil
}

// IsBlockActive checks whether a block is currently active.
func (d *DB) IsBlockActive(name string) (bool, error) {
	var count int
	err := d.conn.QueryRow(`
		SELECT COUNT(*) FROM active_blocks ab
		JOIN blocks b ON b.id = ab.block_id
		WHERE b.name = ?
	`, name).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to check whether block '%s' is active: %w", name, err)
	}
	return count > 0, nil
}

// IsBlockLocked checks whether an active block is locked.
// If the block isn't active, it returns false (not locked).
func (d *DB) IsBlockLocked(name string) (bool, error) {
	var locked bool
	err := d.conn.QueryRow(`
		SELECT COALESCE(ab.locked, 0) FROM active_blocks ab
		JOIN blocks b ON b.id = ab.block_id
		WHERE b.name = ?
	`, name).Scan(&locked)
	if err == sql.ErrNoRows {
		// If it's not active, it's not locked.
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to check whether block '%s' is locked: %w", name, err)
	}
	return locked, nil
}

// --------------------------------------------------------------------------
// Queries for the daemon (active blocks)
// --------------------------------------------------------------------------

// GetActiveBlocks returns every active block with its sites and apps.
// The daemon uses this to know what to block.
func (d *DB) GetActiveBlocks() ([]ActiveBlockDetail, error) {
	rows, err := d.conn.Query(`
		SELECT b.id, b.name, ab.locked, ab.lock_chars
		FROM active_blocks ab
		JOIN blocks b ON b.id = ab.block_id
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch active blocks: %w", err)
	}
	defer rows.Close()

	var activeBlocks []ActiveBlockDetail
	for rows.Next() {
		var blockID int
		var abd ActiveBlockDetail

		if err := rows.Scan(&blockID, &abd.BlockName, &abd.Locked, &abd.LockChars); err != nil {
			return nil, fmt.Errorf("failed to read active block: %w", err)
		}

		// For each active block, fetch its sites and apps.
		abd.Sites, err = d.getBlockSites(blockID)
		if err != nil {
			return nil, err
		}

		abd.Apps, err = d.getBlockApps(blockID)
		if err != nil {
			return nil, err
		}

		activeBlocks = append(activeBlocks, abd)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate active blocks: %w", err)
	}

	return activeBlocks, nil
}

// GetAllBlockedDomains returns every domain from every active block.
// The daemon uses this to update /etc/hosts or the firewall.
// We return a "flat" list because the daemon doesn't need to know which block
// each domain belongs to — it only needs the full list.
func (d *DB) GetAllBlockedDomains() ([]string, error) {
	rows, err := d.conn.Query(`
		SELECT DISTINCT s.domain
		FROM sites s
		JOIN active_blocks ab ON ab.block_id = s.block_id
		ORDER BY s.domain
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch blocked domains: %w", err)
	}
	defer rows.Close()

	var domains []string
	for rows.Next() {
		var domain string
		if err := rows.Scan(&domain); err != nil {
			return nil, fmt.Errorf("failed to read blocked domain: %w", err)
		}
		domains = append(domains, domain)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate blocked domains: %w", err)
	}

	return domains, nil
}

// GetAllBlockedApps returns every process name from every active block.
// The daemon uses this to kill processes the user wants to block.
func (d *DB) GetAllBlockedApps() ([]string, error) {
	rows, err := d.conn.Query(`
		SELECT DISTINCT a.process_name
		FROM apps a
		JOIN active_blocks ab ON ab.block_id = a.block_id
		ORDER BY a.process_name
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch blocked apps: %w", err)
	}
	defer rows.Close()

	var apps []string
	for rows.Next() {
		var app string
		if err := rows.Scan(&app); err != nil {
			return nil, fmt.Errorf("failed to read blocked app: %w", err)
		}
		apps = append(apps, app)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate blocked apps: %w", err)
	}

	return apps, nil
}

// --------------------------------------------------------------------------
// Data maintenance
// --------------------------------------------------------------------------

// SiteRow represents a row of the sites table (ID + domain).
type SiteRow struct {
	ID     int
	Domain string
}

// GetAllSitesRaw returns every site from every block (ID + domain).
// Used for migrating/normalizing existing data.
func (d *DB) GetAllSitesRaw() ([]SiteRow, error) {
	rows, err := d.conn.Query("SELECT id, domain FROM sites")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch sites: %w", err)
	}
	defer rows.Close()

	var sites []SiteRow
	for rows.Next() {
		var s SiteRow
		if err := rows.Scan(&s.ID, &s.Domain); err != nil {
			return nil, fmt.Errorf("failed to read site: %w", err)
		}
		sites = append(sites, s)
	}
	return sites, rows.Err()
}

// UpdateSiteDomain updates a site's domain by ID.
func (d *DB) UpdateSiteDomain(id int, domain string) error {
	_, err := d.conn.Exec("UPDATE sites SET domain = ? WHERE id = ?", domain, id)
	return err
}

// --------------------------------------------------------------------------
// Helper functions (private)
// --------------------------------------------------------------------------

// getBlockID looks up a block's ID by name.
// It's a helper used by several other functions that need to convert the
// name (which the user provides) into the ID (which the database uses
// internally).
func (d *DB) getBlockID(name string) (int, error) {
	var id int
	err := d.conn.QueryRow("SELECT id FROM blocks WHERE name = ?", name).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("block '%s' not found", name)
	}
	if err != nil {
		return 0, fmt.Errorf("failed to fetch block '%s': %w", name, err)
	}
	return id, nil
}

// getBlockSites returns every domain of a block by ID.
func (d *DB) getBlockSites(blockID int) ([]string, error) {
	rows, err := d.conn.Query("SELECT domain FROM sites WHERE block_id = ? ORDER BY domain", blockID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch block sites: %w", err)
	}
	defer rows.Close()

	var sites []string
	for rows.Next() {
		var site string
		if err := rows.Scan(&site); err != nil {
			return nil, fmt.Errorf("failed to read site: %w", err)
		}
		sites = append(sites, site)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate sites: %w", err)
	}

	return sites, nil
}

// getBlockApps returns every process name of a block by ID.
func (d *DB) getBlockApps(blockID int) ([]string, error) {
	rows, err := d.conn.Query("SELECT process_name FROM apps WHERE block_id = ? ORDER BY process_name", blockID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch block apps: %w", err)
	}
	defer rows.Close()

	var apps []string
	for rows.Next() {
		var app string
		if err := rows.Scan(&app); err != nil {
			return nil, fmt.Errorf("failed to read app: %w", err)
		}
		apps = append(apps, app)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate apps: %w", err)
	}

	return apps, nil
}
