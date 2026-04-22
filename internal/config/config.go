package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/evanmschultz/ta/internal/schema"
)

// ErrNoSchema is returned when no .ta/schema.toml is found at the home
// directory nor at any ancestor of the target file path.
var ErrNoSchema = errors.New("no .ta/schema.toml found in project tree or home directory")

// SchemaFileName is the on-disk name of the schema file, relative to
// a .ta/ directory.
const SchemaFileName = "schema.toml"

// SchemaDirName is the directory name that holds the schema file.
const SchemaDirName = ".ta"

// Resolution is the cascade-merged schema for a target file. Sources lists
// every .ta/schema.toml that contributed, in merge order: home first (when
// not already on the ancestor chain), then ancestors from filesystem root
// toward the target file. Registry is the merged result; section types
// defined closer to the target file override same-named types from further
// out, while types unique to any level are preserved.
type Resolution struct {
	Sources  []string
	Registry schema.Registry
}

// Resolve builds the cascade-merged schema Resolution for filePath. It
// collects every candidate .ta/schema.toml along the cascade, loads the
// ones that exist, and folds them into a single Registry with closer
// schemas overriding further-out ones per section type.
//
// If no candidate exists, Resolve returns ErrNoSchema. Malformed schema
// files surface their parse error wrapped in context, never ErrNoSchema.
func Resolve(filePath string) (Resolution, error) {
	abs, err := filepath.Abs(filePath)
	if err != nil {
		return Resolution{}, fmt.Errorf("config: absolute path for %q: %w", filePath, err)
	}

	candidates, err := candidatePaths(abs)
	if err != nil {
		return Resolution{}, err
	}

	var sources []string
	merged := schema.Registry{}
	for _, path := range candidates {
		reg, ok, err := loadIfExists(path)
		if err != nil {
			return Resolution{}, err
		}
		if !ok {
			continue
		}
		sources = append(sources, path)
		merged = merged.Override(reg)
	}

	if len(sources) == 0 {
		return Resolution{}, ErrNoSchema
	}
	return Resolution{Sources: sources, Registry: merged}, nil
}

// candidatePaths returns schema config paths in cascade order. The home
// config (if it exists as a concept — i.e. os.UserHomeDir resolves) comes
// first unless it coincides with an ancestor of the target file, in which
// case the ancestor chain places it naturally. Ancestors follow in
// CandidatePaths returns the same ordered list of schema file paths
// that Resolve would consider when called on filePath — the home-slot
// plus every ancestor's .ta/schema.toml, in root-to-file order. The
// existence of each returned path is NOT checked here; callers that
// want the existence-gated subset should stat each path themselves.
//
// This is the cheap "what files might contribute right now?" probe
// used by the mcpsrv cache to detect a cascade layer that appeared
// mid-session — the bare mtime check on captured sources misses new
// files by construction. Returning just the candidate list (no parse)
// keeps the probe O(ancestors) + 1 stat per call.
func CandidatePaths(filePath string) ([]string, error) {
	abs, err := filepath.Abs(filePath)
	if err != nil {
		return nil, fmt.Errorf("config: abs path: %w", err)
	}
	return candidatePaths(abs)
}

// filesystem-root-to-file order so that `merged.Override(next)` gives
// closer-to-file precedence.
func candidatePaths(absFilePath string) ([]string, error) {
	var ancestors []string
	dir := filepath.Dir(absFilePath)
	for {
		ancestors = append(ancestors, filepath.Join(dir, SchemaDirName, SchemaFileName))
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	// Reverse: we collected file-to-root; merge needs root-to-file.
	for i, j := 0, len(ancestors)-1; i < j; i, j = i+1, j-1 {
		ancestors[i], ancestors[j] = ancestors[j], ancestors[i]
	}

	var out []string
	if home, err := os.UserHomeDir(); err == nil {
		homePath := filepath.Join(home, SchemaDirName, SchemaFileName)
		if !slices.Contains(ancestors, homePath) {
			out = append(out, homePath)
		}
	}
	out = append(out, ancestors...)
	return out, nil
}

func loadIfExists(path string) (schema.Registry, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return schema.Registry{}, false, nil
		}
		return schema.Registry{}, false, fmt.Errorf("config: open %s: %w", path, err)
	}
	defer f.Close()

	reg, err := schema.Load(f)
	if err != nil {
		return schema.Registry{}, false, fmt.Errorf("config: %s: %w", path, err)
	}
	return reg, true, nil
}
