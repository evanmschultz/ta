// Package templates manages the global template library — the
// per-user `~/.ta/` directory plus the binary-embedded `examples/`
// tree injected via SetBinarySource. The library carries four kinds
// of items: schema fragments (TOML dbs aggregated into
// `~/.ta/schema.toml`), agents (markdown grouped under user-chosen
// subdirs at `~/.ta/agents/<group>/<name>.md`), configs
// (`~/.ta/configs/<canonical>`), and docs templates
// (`~/.ta/docs-templates/<canonical>`). The library is a pure
// template store — never read at runtime by the MCP server or data
// tools. Only `ta init` and `ta template *` touch it.
//
// Per V2-PLAN §14.2 the firewall is strict: templates imports stdlib
// + internal/schema + internal/fsatomic only. It does NOT import
// internal/config/Resolve, internal/ops, or internal/mcpsrv. Runtime
// consumers never import this package.
//
// Post-F15 the home schema is one machine-managed file
// (`~/.ta/schema.toml`) that aggregates every db the user has saved.
// Same-name dbs across the file are not possible — TOML's
// single-instance rule forbids it. The save / merge flow guarantees
// the post-write file passes `schema.LoadBytes` (cross-db invariants
// like overlapping paths and id collisions), so `ta init` reading the
// file gets a registry it can pick from.
//
// Public API — schema (F15):
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
// Public API — multi-category (F24):
//
//   - SetBinarySource(fsys)                  — inject the embed.FS the binary ships with.
//   - ListItems(kind)                        — provenance-tagged items for one Kind.
//   - ListAll()                              — provenance-tagged items across every Kind.
//   - ShowItem(item)                         — raw bytes for one item.
//   - SaveAgent / SaveConfig / SaveDocsTemplate — promote a project file to the home library.
//   - DeleteAgent / DeleteConfig / DeleteDocsTemplate — remove a home-library item; binary items error read-only.
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

// ---- Multi-category surface (F24) -----------------------------------

// Kind classifies a template item. The four kinds correspond to the
// four `examples/` subdirectories: schemas (TOML dbs), agents
// (markdown files grouped under user-defined subdirs), configs
// (canonical-named JSON / TOML / line files), and docs-templates
// (canonical-named markdown skeletons).
type Kind string

const (
	// KindSchema names a single db inside a schema fragment file.
	// Schemas are special: home-side they aggregate into one
	// `~/.ta/schema.toml`; binary-side each `examples/schemas/<file>.toml`
	// can declare one or more dbs that aggregate at picker time.
	KindSchema Kind = "schema"
	// KindAgent names one markdown file under a `<group>/` subdir.
	// `<group>` is whatever the user chose at save time — ta does
	// not enforce any taxonomy. Agents saved without a group land
	// flat under `~/.ta/agents/` and surface as the `(ungrouped)`
	// group at picker time.
	KindAgent Kind = "agent"
	// KindConfig names one canonical config file (e.g.
	// `claude-settings.json`). The picker writes each selected
	// config to its canonical destination at apply time.
	KindConfig Kind = "config"
	// KindDocsTemplate names one canonical docs template (e.g.
	// `CLAUDE.md`). The picker writes each selected docs template
	// to the target's project root at apply time.
	KindDocsTemplate Kind = "docs-template"
)

// Provenance tags an Item by source.
type Provenance string

const (
	// ProvenanceBinary tags items that ship inside the ta binary
	// via `examples/`. Read-only: SaveX / DeleteX refuse to mutate
	// these.
	ProvenanceBinary Provenance = "ta"
	// ProvenanceHome tags items that live under `~/.ta/`.
	// SaveX / DeleteX target this side.
	ProvenanceHome Provenance = "home"
)

// Item is one template entry surfaced to pickers, MCP previews, and
// CLI listings.
//
// Fields:
//   - Kind: KindSchema | KindAgent | KindConfig | KindDocsTemplate.
//   - Name: db name for schemas; filename stem for agents and docs;
//     canonical filename (with extension) for configs.
//   - Group: agent subdir (e.g. "go", "fe", "" for ungrouped). Empty
//     for non-agents.
//   - Provenance: ProvenanceBinary or ProvenanceHome.
//   - Description: short blurb. Schemas pull from `[<db>].description`;
//     other kinds leave it empty (callers may show a filename
//     fallback).
//
// Items are equality-keyed by (Kind, Group, Name, Provenance).
type Item struct {
	Kind        Kind
	Name        string
	Group       string
	Provenance  Provenance
	Description string
}

// agentsDir / configsDir / docsDir are the home-library sibling
// directories at `~/.ta/agents/` etc. Schemas go in the
// canonical `~/.ta/schema.toml` (handled by the F15 surface).
const (
	agentsDir  = "agents"
	configsDir = "configs"
	docsDir    = "docs-templates"

	// schemasDir is the binary-side directory inside `examples/`
	// that holds schema fragments. Home-side schemas live in the
	// flat `~/.ta/schema.toml` file, not under a subdir — this
	// constant is binary-side only.
	schemasDir = "schemas"

	// examplesRoot is the prefix every binary-source path carries
	// inside the embed.FS. Always rooted at "examples".
	examplesRoot = "examples"

	// keepSentinel is the empty-dir placeholder name used to keep
	// otherwise-empty directories present in the embed.FS. Filtered
	// out at enumeration time so callers do not see it as a
	// template item.
	keepSentinel = ".keep"
)

// binarySrc is the embed.FS the binary ships with, injected via
// SetBinarySource at process startup. nil means "no binary source"
// — ListItems / ListAll silently skip the binary side, which is the
// correct behavior for tests and library callers that don't depend
// on the binary tree.
var binarySrc fs.FS

// SetBinarySource injects the binary-embedded examples FS. Called
// once from `cmd/ta/main.go` init via the root `embed.go`. Tests
// may swap in an `fstest.MapFS` to drive the binary side
// independently of the real `examples/` tree. Passing nil resets to
// the no-binary state — useful in tests that want to assert the
// home-only path.
func SetBinarySource(fsys fs.FS) { binarySrc = fsys }

// ErrReadOnly is returned by SaveX / DeleteX when called on a
// binary-provenance item. Binary items are immutable from the
// templates surface — the user must copy to home first.
var ErrReadOnly = errors.New("templates: binary item is read-only")

// ErrItemNotFound is returned by ShowItem / DeleteX when the
// addressed item does not exist on the named provenance.
var ErrItemNotFound = errors.New("templates: item not found")

// ListItems returns provenance-tagged items for one Kind, sorted by
// (Group, Name, Provenance). Both binary and home are queried; both
// missing yields nil.
func ListItems(kind Kind) ([]Item, error) {
	switch kind {
	case KindSchema:
		return listSchemaItems()
	case KindAgent:
		return listAgentItems()
	case KindConfig:
		return listConfigItems()
	case KindDocsTemplate:
		return listDocsTemplateItems()
	default:
		return nil, fmt.Errorf("templates: unknown kind %q", kind)
	}
}

// ListAll concatenates ListItems output across every Kind. Items are
// sorted by (Kind, Group, Name, Provenance) so the result is stable
// across invocations.
func ListAll() ([]Item, error) {
	var out []Item
	for _, k := range []Kind{KindSchema, KindAgent, KindConfig, KindDocsTemplate} {
		items, err := ListItems(k)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	return out, nil
}

// ShowItem returns the raw bytes for one item. Schemas come back as
// a single-db TOML body (same shape as ShowDB); agents / configs /
// docs-templates come back as the underlying file bytes verbatim.
func ShowItem(item Item) ([]byte, error) {
	switch item.Kind {
	case KindSchema:
		return showSchemaItem(item)
	case KindAgent:
		return showAgentItem(item)
	case KindConfig:
		return showConfigItem(item)
	case KindDocsTemplate:
		return showDocsTemplateItem(item)
	default:
		return nil, fmt.Errorf("templates: unknown kind %q", item.Kind)
	}
}

// listSchemaItems enumerates db items from both provenances.
//
// Binary side: every `examples/schemas/<file>.toml` is parsed, and
// each top-level db it declares becomes one Item. The fragment
// filename does not appear in the picker — only db names.
//
// Home side: ListDBs reads `~/.ta/schema.toml`.
func listSchemaItems() ([]Item, error) {
	var out []Item

	// Binary side — walk examples/schemas/*.toml.
	if binarySrc != nil {
		entries, err := fs.ReadDir(binarySrc, examplesRoot+"/"+schemasDir)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("templates: read binary schemas: %w", err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
				continue
			}
			data, err := fs.ReadFile(binarySrc, examplesRoot+"/"+schemasDir+"/"+e.Name())
			if err != nil {
				return nil, fmt.Errorf("templates: read %s: %w", e.Name(), err)
			}
			bodies, err := unmarshalDBBodies(data)
			if err != nil {
				return nil, fmt.Errorf("templates: parse %s: %w", e.Name(), err)
			}
			for name, body := range bodies {
				out = append(out, Item{
					Kind:        KindSchema,
					Name:        name,
					Provenance:  ProvenanceBinary,
					Description: dbDescription(body),
				})
			}
		}
	}

	// Home side — reuse the F15 surface.
	homeNames, err := ListDBs()
	if err != nil {
		return nil, err
	}
	if len(homeNames) > 0 {
		_, raw, err := LoadHome()
		if err != nil {
			return nil, err
		}
		bodies, err := unmarshalDBBodies(raw)
		if err != nil {
			return nil, err
		}
		for _, name := range homeNames {
			out = append(out, Item{
				Kind:        KindSchema,
				Name:        name,
				Provenance:  ProvenanceHome,
				Description: dbDescription(bodies[name]),
			})
		}
	}

	sortItems(out)
	return out, nil
}

// listAgentItems enumerates agent .md files across both provenances,
// preserving group subdirs as Item.Group. Flat files (no group) get
// Group="".
func listAgentItems() ([]Item, error) {
	var out []Item
	if binarySrc != nil {
		items, err := walkAgentTree(binarySrc, examplesRoot+"/"+agentsDir, ProvenanceBinary)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	homeRoot, err := Root()
	if err != nil {
		return nil, err
	}
	homeAgents := filepath.Join(homeRoot, agentsDir)
	if items, err := walkAgentTree(os.DirFS(homeAgents), ".", ProvenanceHome); err != nil {
		// Missing home agents dir is not an error — just no items.
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	} else {
		out = append(out, items...)
	}
	sortItems(out)
	return out, nil
}

// walkAgentTree returns Item entries for every .md file under root,
// recording the parent dir as Group (or "" for files at the root).
// .keep sentinels and non-.md files are filtered out.
func walkAgentTree(fsys fs.FS, root string, prov Provenance) ([]Item, error) {
	if fsys == nil {
		return nil, nil
	}
	if _, err := fs.Stat(fsys, root); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("templates: stat agents root: %w", err)
	}
	var out []Item
	err := fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		base := d.Name()
		if base == keepSentinel || !strings.HasSuffix(base, ".md") {
			return nil
		}
		// Determine group from the immediate parent dir.
		rel := p
		if root != "." && root != "" {
			rel = strings.TrimPrefix(p, root+"/")
		}
		group := filepath.Dir(rel)
		if group == "." {
			group = ""
		}
		// Reject nested groups (`agents/go/foo/bar.md`) — group is
		// exactly one level deep. Anything deeper is a layout error
		// the user can correct by reorganizing.
		if strings.Contains(group, "/") {
			return fmt.Errorf("templates: nested agent group %q not supported (one level deep only)", rel)
		}
		out = append(out, Item{
			Kind:       KindAgent,
			Name:       strings.TrimSuffix(base, ".md"),
			Group:      group,
			Provenance: prov,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// listConfigItems enumerates config files at both provenances.
// Configs are flat — no group dimension.
func listConfigItems() ([]Item, error) {
	return listFlatItems(KindConfig, configsDir, "")
}

// listDocsTemplateItems enumerates docs-template files at both
// provenances. Like configs, docs templates are flat.
func listDocsTemplateItems() ([]Item, error) {
	return listFlatItems(KindDocsTemplate, docsDir, ".md")
}

// listFlatItems is the shared shape for non-grouped kinds. extFilter
// is applied iff non-empty; an empty filter means "any extension"
// (configs ship with mixed extensions: .json / .toml / no
// extension).
func listFlatItems(kind Kind, subdir, extFilter string) ([]Item, error) {
	var out []Item
	if binarySrc != nil {
		entries, err := fs.ReadDir(binarySrc, examplesRoot+"/"+subdir)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("templates: read binary %s: %w", subdir, err)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if e.Name() == keepSentinel {
				continue
			}
			if extFilter != "" && !strings.HasSuffix(e.Name(), extFilter) {
				continue
			}
			out = append(out, Item{
				Kind:       kind,
				Name:       canonicalName(kind, e.Name()),
				Provenance: ProvenanceBinary,
			})
		}
	}
	homeRoot, err := Root()
	if err != nil {
		return nil, err
	}
	homeSub := filepath.Join(homeRoot, subdir)
	entries, err := os.ReadDir(homeSub)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("templates: read home %s: %w", subdir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if e.Name() == keepSentinel {
			continue
		}
		if extFilter != "" && !strings.HasSuffix(e.Name(), extFilter) {
			continue
		}
		out = append(out, Item{
			Kind:       kind,
			Name:       canonicalName(kind, e.Name()),
			Provenance: ProvenanceHome,
		})
	}
	sortItems(out)
	return out, nil
}

// canonicalName trims the extension for docs templates (canonical
// names omit the `.md`) and leaves config filenames intact (configs
// keep their extensions because the destination is filename-driven).
func canonicalName(kind Kind, filename string) string {
	if kind == KindDocsTemplate {
		return strings.TrimSuffix(filename, ".md")
	}
	return filename
}

// showSchemaItem returns the raw bytes for one schema item. Binary
// schemas slice the named db out of the parent fragment file; home
// schemas reuse ShowDB.
func showSchemaItem(item Item) ([]byte, error) {
	switch item.Provenance {
	case ProvenanceHome:
		return ShowDB(item.Name)
	case ProvenanceBinary:
		return showBinarySchemaDB(item.Name)
	default:
		return nil, fmt.Errorf("templates: unknown provenance %q", item.Provenance)
	}
}

// showBinarySchemaDB locates the named db inside any
// `examples/schemas/*.toml` fragment and returns its slice. Returns
// ErrItemNotFound when no fragment declares the name.
func showBinarySchemaDB(name string) ([]byte, error) {
	if binarySrc == nil {
		return nil, fmt.Errorf("%w: binary source not registered", ErrItemNotFound)
	}
	entries, err := fs.ReadDir(binarySrc, examplesRoot+"/"+schemasDir)
	if err != nil {
		return nil, fmt.Errorf("templates: read binary schemas: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		data, err := fs.ReadFile(binarySrc, examplesRoot+"/"+schemasDir+"/"+e.Name())
		if err != nil {
			return nil, fmt.Errorf("templates: read %s: %w", e.Name(), err)
		}
		bodies, err := unmarshalDBBodies(data)
		if err != nil {
			return nil, fmt.Errorf("templates: parse %s: %w", e.Name(), err)
		}
		body, ok := bodies[name]
		if !ok {
			continue
		}
		out, err := marshalDBSubset(map[string]map[string]any{name: body})
		if err != nil {
			return nil, fmt.Errorf("templates: marshal %s: %w", name, err)
		}
		return out, nil
	}
	return nil, fmt.Errorf("%w: schema %q in binary source", ErrItemNotFound, name)
}

// showAgentItem returns raw .md bytes for one agent item.
func showAgentItem(item Item) ([]byte, error) {
	rel := agentRelPath(item.Group, item.Name)
	switch item.Provenance {
	case ProvenanceHome:
		root, err := Root()
		if err != nil {
			return nil, err
		}
		path := filepath.Join(root, agentsDir, rel)
		data, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil, fmt.Errorf("%w: agent %q (group=%q) in home", ErrItemNotFound, item.Name, item.Group)
			}
			return nil, fmt.Errorf("templates: read %s: %w", path, err)
		}
		return data, nil
	case ProvenanceBinary:
		if binarySrc == nil {
			return nil, fmt.Errorf("%w: binary source not registered", ErrItemNotFound)
		}
		full := examplesRoot + "/" + agentsDir + "/" + rel
		data, err := fs.ReadFile(binarySrc, full)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil, fmt.Errorf("%w: agent %q (group=%q) in binary", ErrItemNotFound, item.Name, item.Group)
			}
			return nil, fmt.Errorf("templates: read %s: %w", full, err)
		}
		return data, nil
	default:
		return nil, fmt.Errorf("templates: unknown provenance %q", item.Provenance)
	}
}

// agentRelPath joins group + name into the relative path under
// `agents/`. Group="" yields a flat path (`<name>.md`); non-empty
// group yields `<group>/<name>.md`.
func agentRelPath(group, name string) string {
	if group == "" {
		return name + ".md"
	}
	return group + "/" + name + ".md"
}

// showConfigItem returns raw bytes for one config item.
func showConfigItem(item Item) ([]byte, error) {
	return showFlatItem(item, configsDir)
}

// showDocsTemplateItem returns raw bytes for one docs-template item.
func showDocsTemplateItem(item Item) ([]byte, error) {
	// Docs templates trim `.md` for canonical naming; reattach for
	// the on-disk read.
	withExt := item.Name + ".md"
	clone := item
	clone.Name = withExt
	return showFlatItem(clone, docsDir)
}

func showFlatItem(item Item, subdir string) ([]byte, error) {
	switch item.Provenance {
	case ProvenanceHome:
		root, err := Root()
		if err != nil {
			return nil, err
		}
		path := filepath.Join(root, subdir, item.Name)
		data, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil, fmt.Errorf("%w: %s %q in home", ErrItemNotFound, item.Kind, item.Name)
			}
			return nil, fmt.Errorf("templates: read %s: %w", path, err)
		}
		return data, nil
	case ProvenanceBinary:
		if binarySrc == nil {
			return nil, fmt.Errorf("%w: binary source not registered", ErrItemNotFound)
		}
		full := examplesRoot + "/" + subdir + "/" + item.Name
		data, err := fs.ReadFile(binarySrc, full)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil, fmt.Errorf("%w: %s %q in binary", ErrItemNotFound, item.Kind, item.Name)
			}
			return nil, fmt.Errorf("templates: read %s: %w", full, err)
		}
		return data, nil
	default:
		return nil, fmt.Errorf("templates: unknown provenance %q", item.Provenance)
	}
}

// SaveAgent copies body bytes into `~/.ta/agents/<group>/<name>.md`.
// Empty group → flat layout. force=false errors when the destination
// exists; force=true overwrites atomically. Validates name + group
// shape before any disk write.
func SaveAgent(name, group string, body []byte, force bool) error {
	if err := validateName(name); err != nil {
		return err
	}
	if group != "" {
		if err := validateName(group); err != nil {
			return fmt.Errorf("%w: group %q", ErrInvalidName, group)
		}
	}
	root, err := Root()
	if err != nil {
		return err
	}
	dir := filepath.Join(root, agentsDir)
	if group != "" {
		dir = filepath.Join(dir, group)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("templates: create %s: %w", dir, err)
	}
	dst := filepath.Join(dir, name+".md")
	if !force {
		if _, err := os.Stat(dst); err == nil {
			return fmt.Errorf("templates: %s already exists; pass force=true to overwrite", dst)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("templates: stat %s: %w", dst, err)
		}
	}
	return fsatomic.Write(dst, body)
}

// SaveConfig copies body bytes into `~/.ta/configs/<canonical>`.
// `canonical` is the destination filename (e.g.
// `claude-settings.json`). force=false errors on conflict.
func SaveConfig(canonical string, body []byte, force bool) error {
	return saveFlatItem(canonical, body, force, configsDir, "")
}

// SaveDocsTemplate copies body bytes into
// `~/.ta/docs-templates/<canonical>.md`. `canonical` is the docs
// template name without `.md` extension; the extension is appended.
// force=false errors on conflict.
func SaveDocsTemplate(canonical string, body []byte, force bool) error {
	return saveFlatItem(canonical, body, force, docsDir, ".md")
}

// saveFlatItem handles the shared write path for configs and docs
// templates. extSuffix is appended to the filename iff non-empty
// (docs add `.md`; configs leave the canonical filename intact).
func saveFlatItem(canonical string, body []byte, force bool, subdir, extSuffix string) error {
	if canonical == "" {
		return fmt.Errorf("%w: empty canonical name", ErrInvalidName)
	}
	if strings.ContainsAny(canonical, `/\`) {
		return fmt.Errorf("%w: %q contains a path separator", ErrInvalidName, canonical)
	}
	if strings.HasPrefix(canonical, ".") {
		return fmt.Errorf("%w: %q starts with a dot", ErrInvalidName, canonical)
	}
	if canonical != filepath.Clean(canonical) {
		return fmt.Errorf("%w: %q is not in canonical form", ErrInvalidName, canonical)
	}
	root, err := Root()
	if err != nil {
		return err
	}
	dir := filepath.Join(root, subdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("templates: create %s: %w", dir, err)
	}
	dst := filepath.Join(dir, canonical+extSuffix)
	if !force {
		if _, err := os.Stat(dst); err == nil {
			return fmt.Errorf("templates: %s already exists; pass force=true to overwrite", dst)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("templates: stat %s: %w", dst, err)
		}
	}
	return fsatomic.Write(dst, body)
}

// DeleteAgent removes a home-library agent. Binary agents always
// error with ErrReadOnly. Missing files surface ErrItemNotFound.
func DeleteAgent(name, group string) error {
	if err := validateName(name); err != nil {
		return err
	}
	if group != "" {
		if err := validateName(group); err != nil {
			return fmt.Errorf("%w: group %q", ErrInvalidName, group)
		}
	}
	root, err := Root()
	if err != nil {
		return err
	}
	dst := filepath.Join(root, agentsDir, agentRelPath(group, name))
	if err := os.Remove(dst); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: agent %q (group=%q)", ErrItemNotFound, name, group)
		}
		return fmt.Errorf("templates: delete %s: %w", dst, err)
	}
	return nil
}

// DeleteConfig removes one home-library config.
func DeleteConfig(canonical string) error {
	return deleteFlatItem(canonical, configsDir, "", KindConfig)
}

// DeleteDocsTemplate removes one home-library docs template.
func DeleteDocsTemplate(canonical string) error {
	return deleteFlatItem(canonical, docsDir, ".md", KindDocsTemplate)
}

func deleteFlatItem(canonical, subdir, extSuffix string, kind Kind) error {
	if canonical == "" {
		return fmt.Errorf("%w: empty canonical name", ErrInvalidName)
	}
	root, err := Root()
	if err != nil {
		return err
	}
	dst := filepath.Join(root, subdir, canonical+extSuffix)
	if err := os.Remove(dst); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: %s %q", ErrItemNotFound, kind, canonical)
		}
		return fmt.Errorf("templates: delete %s: %w", dst, err)
	}
	return nil
}

// dbDescription extracts the optional [<db>].description field from a
// raw db body, returning "" when absent or non-string.
func dbDescription(body map[string]any) string {
	if body == nil {
		return ""
	}
	d, ok := body["description"].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(d)
}

// sortItems orders a slice in (Kind, Group, Name, Provenance) order.
// Stable across calls so picker output is deterministic.
func sortItems(items []Item) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Group != b.Group {
			return a.Group < b.Group
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Provenance < b.Provenance
	})
}

// ---- internal --------------------------------------------------------

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
