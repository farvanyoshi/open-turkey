// This file holds a small helper for writing files atomically.
//
// WHY ATOMIC WRITES?
// ------------------
// The daemon rewrites /etc/hosts and the browser policy JSON files whenever it
// detects tampering. If the process were killed midway through an ordinary
// os.WriteFile (which truncates then writes), it could leave /etc/hosts
// truncated or half-written — a corrupt hosts file can break name resolution
// for the whole system. Writing to a temporary file in the same directory and
// then renaming it into place makes the replacement atomic: readers always see
// either the complete old file or the complete new one, never a partial write.
package blocker

import (
	"fmt"
	"os"
	"path/filepath"
)

// atomicWriteFile writes data to path atomically by creating a temporary file
// in the same directory and renaming it over the target. The rename is atomic
// on POSIX filesystems as long as source and destination share a directory.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)

	// Create the temp file in the SAME directory so the rename stays on one
	// filesystem (cross-filesystem renames are not atomic and would fail).
	tmp, err := os.CreateTemp(dir, ".open-turkey-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()

	// If anything below fails, don't leave the temp file lying around.
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to write temp file %s: %w", tmpName, err)
	}
	// Flush to disk before the rename so a crash can't leave an empty file
	// under the real name.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to sync temp file %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close temp file %s: %w", tmpName, err)
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return fmt.Errorf("failed to set permissions on %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("failed to move %s into place at %s: %w", tmpName, path, err)
	}

	cleanup = false
	return nil
}
