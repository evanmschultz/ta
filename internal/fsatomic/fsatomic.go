// Package fsatomic provides atomic file-write helpers used across ta.
//
// Writes go through a same-directory temp file + rename so partial writes
// cannot leave a truncated target on disk if the process dies mid-write.
// Per V2-PLAN §6 package layout: a dedicated package so lang-agnostic
// consumers (templates, future bootstrap helpers) can write atomically
// without importing a backend package.
package fsatomic

import (
	"fmt"
	"os"
	"path/filepath"
)

// Write writes data to path atomically via a same-directory temp file
// plus rename. If the target directory does not exist the underlying
// error is returned unchanged — callers own directory creation.
func Write(path string, data []byte) error {
	if path == "" {
		return fmt.Errorf("fsatomic: Write: empty path")
	}
	dir := filepath.Dir(path)
	base := filepath.Base(path)

	tmp, err := os.CreateTemp(dir, "."+base+".*.tmp")
	if err != nil {
		return fmt.Errorf("fsatomic: create temp in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("fsatomic: write temp %s: %w", tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("fsatomic: sync temp %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("fsatomic: close temp %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("fsatomic: rename %s -> %s: %w", tmpPath, path, err)
	}
	return nil
}
