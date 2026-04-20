package tomlfile

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteAtomic writes data to path via a same-directory temp file plus rename.
// This avoids leaving a partial file if the process dies mid-write.
//
// The temp file is created in filepath.Dir(path) so rename is a cheap
// same-filesystem operation. If the target directory does not exist,
// WriteAtomic returns the underlying error unchanged.
func WriteAtomic(path string, data []byte) error {
	if path == "" {
		return fmt.Errorf("tomlfile: WriteAtomic: empty path")
	}
	dir := filepath.Dir(path)
	base := filepath.Base(path)

	tmp, err := os.CreateTemp(dir, "."+base+".*.tmp")
	if err != nil {
		return fmt.Errorf("tomlfile: create temp in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("tomlfile: write temp %s: %w", tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("tomlfile: sync temp %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("tomlfile: close temp %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("tomlfile: rename %s -> %s: %w", tmpPath, path, err)
	}
	return nil
}
