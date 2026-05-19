// Package install applies a resolved installconfig.Config against a dotta.Tree,
// landing files under each substrate's Destination and recording registration
// directives for later settings-file mutation.
//
// This file holds the fsatomic-backed file-copy primitive used by Apply when
// a substrate's on_conflict policy resolves to overwrite (or the destination
// does not yet exist).
package install

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/evanmschultz/ta/internal/fsatomic"
)

// defaultFileMode is the destination permission applied when chmod is empty.
const defaultFileMode os.FileMode = 0o644

// parentDirMode is the permission applied to parent directories created on
// demand by CopyFile when the destination's parent tree does not yet exist.
const parentDirMode os.FileMode = 0o755

// CopyFile copies srcAbs to dstAbs atomically via fsatomic.Write and then
// applies chmod (parsed as an octal string, default 0o644 when chmod=="").
//
// Both paths must be absolute. Parent directories of dstAbs are created at
// 0o755 if they do not already exist. An existing dstAbs is overwritten.
//
// A non-empty chmod that fails to parse as octal returns a descriptive error
// before any filesystem mutation is attempted.
func CopyFile(srcAbs, dstAbs, chmod string) error {
	if srcAbs == "" {
		return fmt.Errorf("install: CopyFile: empty srcAbs")
	}
	if dstAbs == "" {
		return fmt.Errorf("install: CopyFile: empty dstAbs")
	}

	mode, err := parseChmod(chmod)
	if err != nil {
		return fmt.Errorf("install: CopyFile: parse chmod %q: %w", chmod, err)
	}

	data, err := os.ReadFile(srcAbs)
	if err != nil {
		return fmt.Errorf("install: CopyFile: read src %s: %w", srcAbs, err)
	}

	if err := os.MkdirAll(filepath.Dir(dstAbs), parentDirMode); err != nil {
		return fmt.Errorf("install: CopyFile: mkdir parent of %s: %w", dstAbs, err)
	}

	if err := fsatomic.Write(dstAbs, data); err != nil {
		return fmt.Errorf("install: CopyFile: write dst %s: %w", dstAbs, err)
	}

	if err := os.Chmod(dstAbs, mode); err != nil {
		return fmt.Errorf("install: CopyFile: chmod dst %s to %#o: %w", dstAbs, mode, err)
	}

	return nil
}

// parseChmod interprets s as an octal mode string. Empty returns the default
// (0o644). Invalid strings return an error.
func parseChmod(s string) (os.FileMode, error) {
	if s == "" {
		return defaultFileMode, nil
	}
	v, err := strconv.ParseUint(s, 8, 32)
	if err != nil {
		return 0, err
	}
	return os.FileMode(v), nil
}
