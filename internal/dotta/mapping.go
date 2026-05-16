// mapping.toml is a RESERVED filename within any subtree under the
// root walked by Walk. User data MUST NOT use this filename for
// content records — the enumerator and apply layers treat it as
// per-subtree projection metadata, not as deliverable content.
package dotta

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// MappingFilename is the reserved per-subtree metadata filename. It is
// exported so callers (the enumerator, tests, doc generators) can refer
// to a single source of truth instead of repeating the literal.
const MappingFilename = "mapping.toml"

// LoadMapping reads and decodes the per-subtree mapping.toml that lives
// at filepath.Join(subtreeDir, MappingFilename).
//
// An absent file is not an error: it returns a zero-value Mapping and a
// nil error so callers can apply package-level defaults. Read errors,
// parse errors, and validation errors are all wrapped with a "dotta:"
// prefix so they survive errors.Is / errors.As inspection while still
// being self-describing in log output.
//
// OnConflict is validated against the OnConflict* constants declared in
// tree.go. An empty OnConflict is accepted; the caller is expected to
// substitute a package-level default downstream.
func LoadMapping(subtreeDir string) (Mapping, error) {
	path := filepath.Join(subtreeDir, MappingFilename)

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Mapping{}, nil
		}
		return Mapping{}, fmt.Errorf("dotta: read mapping at %s: %w", path, err)
	}

	var m Mapping
	if err := toml.Unmarshal(data, &m); err != nil {
		return Mapping{}, fmt.Errorf("dotta: parse mapping at %s: %w", path, err)
	}

	if m.OnConflict != "" && !isValidOnConflict(m.OnConflict) {
		return Mapping{}, fmt.Errorf(
			"dotta: invalid on_conflict %q at %s (want skip|overwrite|merge|prompt)",
			m.OnConflict, path,
		)
	}

	return m, nil
}

// isValidOnConflict reports whether s matches one of the four
// OnConflict* constants. Empty strings are NOT accepted here — the
// caller in LoadMapping handles the empty-is-default case explicitly so
// this helper stays a pure membership check.
func isValidOnConflict(s string) bool {
	switch s {
	case OnConflictSkip, OnConflictOverwrite, OnConflictMerge, OnConflictPrompt:
		return true
	default:
		return false
	}
}
