// Package templates manages the global schema template library at
// `~/.ta/schema.toml`. The directory is a pure template store — never
// read at runtime by the MCP server or data tools. Only `ta init` and
// `ta template *` touch it.
//
// Per V2-PLAN §14.2 the firewall is strict: templates imports stdlib +
// internal/schema + internal/fsatomic only. It does NOT import
// internal/config/Resolve, internal/ops, or internal/mcpsrv. Runtime
// consumers never import this package.
//
// Post-F15 the home library is one machine-managed file
// (`~/.ta/schema.toml`) that aggregates every db the user has saved.
// Same-name dbs across the file are not possible — TOML's
// single-instance rule forbids it. The save / merge flow guarantees
// the post-write file passes `schema.LoadBytes` (cross-db invariants
// like overlapping paths and id collisions), so `ta init` reading the
// file gets a registry it can pick from.
//
// Public API:
//
//   - Root()                                 — resolves $HOME/.ta.
//   - SchemaPath()                           — resolves $HOME/.ta/schema.toml.
//   - LoadHome()                             — (registry, raw bytes, err); missing file → (zero, nil, nil).
//   - ListDBs()                              — sorted db names from LoadHome.
//   - ShowDB(name)                           — raw TOML body for one db; ErrDBNotFound when absent.
//   - SaveDBs(projectBytes, names, opts)     — merge selected dbs from a project schema into the home schema.
//   - DeleteDB(name)                         — remove one db; ErrDBNotFound when absent.
//   - LegacyTemplateFiles()                  — enumerate sibling `~/.ta/<name>.toml` files (warn-only carry-over).
//
// The pre-F15 names `List / Load / Save / Delete` are intentionally
// dropped so callers compile-break loudly rather than silently inherit
// new semantics.
package templates

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/evanmschultz/ta/internal/fsatomic"
	"github.com/evanmschultz/ta/internal/schema"
)

// schemaFile is the single canonical filename inside the home library.
// One file per .ta/ directory per memory rule
// `feedback_ta_one_schema_file_per_dir.md`.
const schemaFile = "schema.toml"

// rootFn resolves the default template root. Exposed as a package
// variable so tests can inject a tmpdir via SetRootForTest; production
// call sites go through Root() which calls this.
var rootFn = defaultRoot

// Root returns the template library directory, conventionally
// $HOME/.ta. Does not create the directory — callers decide whether a
// missing root is an error (LoadHome treats it as empty; SaveDBs creates
// it).
func Root() (string, error) { return rootFn() }

func defaultRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("templates: resolve home: %w", err)
	}
	return filepath.Join(home, ".ta"), nil
}

// SchemaPath returns the absolute path to `~/.ta/schema.toml`. Callers
// (tests, error messages) use this when they need the load-bearing
// path string rather than just the parent directory.
func SchemaPath() (string, error) {
	root, err := Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, schemaFile), nil
}

// SetRootForTest swaps the root-resolver for the duration of a test
// and returns a restore function. Only tests should call this.
func SetRootForTest(dir string) (restore func()) {
	prev := rootFn
	rootFn = func() (string, error) { return dir, nil }
	return func() { rootFn = prev }
}

// ErrDBNotFound is returned by ShowDB and DeleteDB when the named db
// is not present in `~/.ta/schema.toml` (or the file itself is
// missing).
var ErrDBNotFound = errors.New("templates: db not found in ~/.ta/schema.toml")

// ErrInvalidName is returned when a db name would escape the file or
// name a hidden/non-canonical key. Db names are TOML table keys; the
// file-escape rules from the pre-F15 file-per-template world still
// apply because db names are also surfaced in error messages and
// (post-F15) interpreted by `ta init --template <db>`.
var ErrInvalidName = errors.New("templates: invalid db name")

// validateName rejects db names that the picker / cli would otherwise
// echo back unsafely. Identical contract to the pre-F15
// per-file-template validation: rejects empty, separators, leading
// dots, and non-canonical forms.
func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: empty name", ErrInvalidName)
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("%w: %q contains a path separator", ErrInvalidName, name)
	}
	if strings.HasPrefix(name, ".") {
		return fmt.Errorf("%w: %q starts with a dot", ErrInvalidName, name)
	}
	if name != filepath.Clean(name) {
		return fmt.Errorf("%w: %q is not in canonical form", ErrInvalidName, name)
	}
	return nil
}

// LoadHome reads `~/.ta/schema.toml` and returns the parsed registry
// plus the raw bytes. A missing file returns (zero registry, nil
// bytes, nil error) — callers decide whether emptiness is an error.
func LoadHome() (schema.Registry, []byte, error) {
	path, err := SchemaPath()
	if err != nil {
		return schema.Registry{}, nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return schema.Registry{}, nil, nil
		}
		return schema.Registry{}, nil, fmt.Errorf("templates: read %s: %w", path, err)
	}
	reg, err := schema.LoadBytes(data)
	if err != nil {
		return schema.Registry{}, nil, fmt.Errorf("templates: validate %s: %w", path, err)
	}
	return reg, data, nil
}

// ListDBs returns the sorted db names declared in `~/.ta/schema.toml`.
// A missing file yields nil; an empty registry yields an empty slice.
func ListDBs() ([]string, error) {
	reg, _, err := LoadHome()
	if err != nil {
		return nil, err
	}
	if reg.DBs == nil {
		return nil, nil
	}
	out := make([]string, 0, len(reg.DBs))
	for n := range reg.DBs {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}

// ShowDB returns the raw TOML body for one db, sliced from the home
// schema and re-marshalled. Pelletier/go-toml/v2 has no
// whitespace-preserving AST so the round-trip cost is minor
// reformatting — acceptable per the F15 "comment preservation drop"
// decision (home schema is machine-managed). Returns ErrDBNotFound
// when the db is absent or the home file is missing.
func ShowDB(name string) ([]byte, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}
	_, raw, err := LoadHome()
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, fmt.Errorf("%w: %q", ErrDBNotFound, name)
	}
	bodies, err := unmarshalDBBodies(raw)
	if err != nil {
		return nil, err
	}
	body, ok := bodies[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrDBNotFound, name)
	}
	out, err := marshalDBSubset(map[string]map[string]any{name: body})
	if err != nil {
		return nil, fmt.Errorf("templates: marshal db %q: %w", name, err)
	}
	return out, nil
}

// SaveOptions controls SaveDBs conflict semantics.
type SaveOptions struct {
	// Overwrite, when true, replaces home dbs whose names collide with
	// project dbs. When false, conflicts are skipped and reported.
	Overwrite bool
}

// SaveResult records the outcome of one SaveDBs invocation. Callers
// (the cli) surface the three buckets to the user; the zero value is
// the no-op outcome.
type SaveResult struct {
	// Written lists db names whose body was written or replaced.
	Written []string
	// Skipped lists db names skipped because home already has them and
	// Overwrite is false.
	Skipped []string
	// Conflicts lists db names where home and project both declare it.
	// Overlaps with either Written (Overwrite=true) or Skipped
	// (Overwrite=false), so callers can size confirm prompts before
	// they decide which bucket the conflicts land in.
	Conflicts []string
}

// SaveDBs merges selected dbs from a project schema into
// `~/.ta/schema.toml`. When names is empty every db declared in the
// project schema is selected.
//
// Conflict resolution: SaveOptions.Overwrite=true replaces same-named
// home dbs; Overwrite=false skips them. The returned SaveResult lists
// every conflict regardless of the resolution so callers can produce
// accurate reports.
//
// Validation: after the merge, the result is run through
// schema.LoadBytes to enforce cross-db invariants (overlapping paths,
// id collisions across types). A failure here returns an error
// WITHOUT writing — `~/.ta/schema.toml` is left byte-identical.
//
// Atomicity: the write goes through fsatomic.Write so a crash mid-write
// cannot leave a truncated home schema.
func SaveDBs(projectSchemaBytes []byte, names []string, opts SaveOptions) (SaveResult, error) {
	// Parse project bytes first — bad project bytes should error
	// before we touch home at all.
	if _, err := schema.LoadBytes(projectSchemaBytes); err != nil {
		return SaveResult{}, fmt.Errorf("templates: validate project schema: %w", err)
	}
	projectBodies, err := unmarshalDBBodies(projectSchemaBytes)
	if err != nil {
		return SaveResult{}, fmt.Errorf("templates: parse project schema: %w", err)
	}
	if len(projectBodies) == 0 {
		return SaveResult{}, errors.New("templates: project schema declares no dbs")
	}

	// Normalize the requested names. Empty / nil = all project dbs.
	requested := make([]string, 0, len(projectBodies))
	if len(names) == 0 {
		for n := range projectBodies {
			requested = append(requested, n)
		}
	} else {
		seen := make(map[string]struct{}, len(names))
		for _, n := range names {
			if err := validateName(n); err != nil {
				return SaveResult{}, err
			}
			if _, dup := seen[n]; dup {
				continue
			}
			seen[n] = struct{}{}
			if _, ok := projectBodies[n]; !ok {
				return SaveResult{}, fmt.Errorf("templates: project schema has no db %q", n)
			}
			requested = append(requested, n)
		}
	}
	sort.Strings(requested)

	// Validate every requested name (defensive — names from project
	// bytes were never validated, only the explicit args were).
	for _, n := range requested {
		if err := validateName(n); err != nil {
			return SaveResult{}, err
		}
	}

	// Load existing home (may be missing).
	_, homeBytes, err := LoadHome()
	if err != nil {
		return SaveResult{}, err
	}
	homeBodies := make(map[string]map[string]any)
	if homeBytes != nil {
		homeBodies, err = unmarshalDBBodies(homeBytes)
		if err != nil {
			return SaveResult{}, err
		}
	}

	// Detect conflicts and build the merged body map.
	merged := make(map[string]map[string]any, len(homeBodies)+len(requested))
	maps.Copy(merged, homeBodies)
	var written, skipped, conflicts []string
	for _, n := range requested {
		_, conflict := homeBodies[n]
		if conflict {
			conflicts = append(conflicts, n)
			if !opts.Overwrite {
				skipped = append(skipped, n)
				continue
			}
		}
		merged[n] = projectBodies[n]
		written = append(written, n)
	}

	// Nothing to write? Return early with the bookkeeping but no disk
	// touch. The home file is left byte-identical (or absent if it
	// never existed).
	if len(written) == 0 {
		return SaveResult{
			Written:   nil,
			Skipped:   skipped,
			Conflicts: conflicts,
		}, nil
	}

	// Re-marshal the merged set and re-validate before writing.
	mergedBytes, err := marshalDBSubset(merged)
	if err != nil {
		return SaveResult{}, fmt.Errorf("templates: marshal merged schema: %w", err)
	}
	if _, err := schema.LoadBytes(mergedBytes); err != nil {
		return SaveResult{}, fmt.Errorf("templates: merged schema invalid: %w", err)
	}

	root, err := Root()
	if err != nil {
		return SaveResult{}, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return SaveResult{}, fmt.Errorf("templates: create root %s: %w", root, err)
	}
	path := filepath.Join(root, schemaFile)
	if err := fsatomic.Write(path, mergedBytes); err != nil {
		return SaveResult{}, fmt.Errorf("templates: write %s: %w", path, err)
	}

	return SaveResult{
		Written:   written,
		Skipped:   skipped,
		Conflicts: conflicts,
	}, nil
}

// DeleteDB removes one db from `~/.ta/schema.toml`. Returns
// ErrDBNotFound when the db is absent or the home file is missing.
// The remaining dbs are re-emitted via the same marshal-round-trip
// path SaveDBs uses, so the file format stays consistent. Removing
// the last db leaves a comment-only file documenting how to repopulate
// it (parallels init's empty-schema branch).
func DeleteDB(name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	_, raw, err := LoadHome()
	if err != nil {
		return err
	}
	if raw == nil {
		return fmt.Errorf("%w: %q", ErrDBNotFound, name)
	}
	bodies, err := unmarshalDBBodies(raw)
	if err != nil {
		return err
	}
	if _, ok := bodies[name]; !ok {
		return fmt.Errorf("%w: %q", ErrDBNotFound, name)
	}
	delete(bodies, name)

	root, err := Root()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("templates: create root %s: %w", root, err)
	}
	path := filepath.Join(root, schemaFile)

	if len(bodies) == 0 {
		// Last-db delete: leave a comment-only file so the next
		// `ta init` / `ta template list` has something to read and
		// the user has a remediation pointer in the file itself.
		empty := []byte(emptyHomeSchemaHeader)
		if err := fsatomic.Write(path, empty); err != nil {
			return fmt.Errorf("templates: write %s: %w", path, err)
		}
		return nil
	}

	out, err := marshalDBSubset(bodies)
	if err != nil {
		return fmt.Errorf("templates: marshal remaining dbs: %w", err)
	}
	// Defense in depth: re-validate. If the remaining dbs somehow
	// fail (shouldn't — they parsed cleanly via LoadHome above) we
	// refuse to write and leave the file as-is.
	if _, err := schema.LoadBytes(out); err != nil {
		return fmt.Errorf("templates: remaining schema invalid after delete: %w", err)
	}
	if err := fsatomic.Write(path, out); err != nil {
		return fmt.Errorf("templates: write %s: %w", path, err)
	}
	return nil
}

// emptyHomeSchemaHeader is the comment-only body written when the
// last db is deleted. Mirrors the project-side empty-schema header
// shape — the cascade resolver tolerates a registry with no dbs.
const emptyHomeSchemaHeader = "# Home template library — no dbs declared yet.\n" +
	"# Build a schema in a project (`ta schema --action=create --kind=db`)\n" +
	"# then promote it via `ta template save`.\n"

// LegacyTemplateFiles returns the absolute paths of any
// `~/.ta/<name>.toml` files OTHER than `schema.toml`. Pre-F15 the home
// library was a directory of per-template files; post-F15 there is one
// canonical file. Legacy files are warn-only — `ta init` and
// `ta template list` surface a one-line notice when any are present
// (per F15 design decision: no auto-migration). A missing root yields
// nil.
func LegacyTemplateFiles() ([]string, error) {
	root, err := Root()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("templates: list %s: %w", root, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == schemaFile {
			continue
		}
		if !strings.HasSuffix(name, ".toml") {
			continue
		}
		if strings.HasPrefix(name, ".") {
			continue
		}
		out = append(out, filepath.Join(root, name))
	}
	sort.Strings(out)
	return out, nil
}

// unmarshalDBBodies parses TOML bytes into the {dbName → body} map
// shape SaveDBs / ShowDB / DeleteDB use. Defensive — top-level
// non-table entries are skipped (LoadBytes already rejected them).
func unmarshalDBBodies(buf []byte) (map[string]map[string]any, error) {
	var raw map[string]any
	if err := toml.Unmarshal(buf, &raw); err != nil {
		return nil, fmt.Errorf("templates: parse: %w", err)
	}
	out := make(map[string]map[string]any, len(raw))
	for k, v := range raw {
		body, ok := v.(map[string]any)
		if !ok {
			continue
		}
		out[k] = body
	}
	return out, nil
}

// marshalDBSubset emits TOML bytes for the supplied {dbName → body}
// map. Iterates in sorted-name order so repeat invocations over the
// same input produce byte-identical output (pelletier/go-toml/v2
// marshals map keys in natural map-iteration order; sorting up front
// keeps the trace explicit).
func marshalDBSubset(bodies map[string]map[string]any) ([]byte, error) {
	if len(bodies) == 0 {
		return []byte(emptyHomeSchemaHeader), nil
	}
	names := make([]string, 0, len(bodies))
	for n := range bodies {
		names = append(names, n)
	}
	sort.Strings(names)
	subset := make(map[string]any, len(names))
	for _, n := range names {
		subset[n] = bodies[n]
	}
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	if err := enc.Encode(subset); err != nil {
		return nil, fmt.Errorf("templates: marshal: %w", err)
	}
	return buf.Bytes(), nil
}
