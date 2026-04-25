package index

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/evanmschultz/ta/internal/config"
	"github.com/evanmschultz/ta/internal/fsatomic"
)

// IndexFileName is the on-disk basename for the runtime index, sibling
// to `schema.toml` inside the project's `.ta/` directory.
const IndexFileName = "index.toml"

// FormatVersion is the only `format_version` value this package writes
// or reads. Future format changes bump this constant and gain a Loader
// migration; the loader rejects every other value loudly so stale on-
// disk files cannot silently masquerade as the current shape.
const FormatVersion = 1

// formatVersionKey is the literal TOML key for the top-level scalar.
const formatVersionKey = "format_version"

// ErrUnknownFormatVersion is returned by Load when the on-disk file
// declares a `format_version` value this package does not understand.
// Wrapping callers can match this sentinel to nudge the user toward
// `ta index rebuild`.
var ErrUnknownFormatVersion = errors.New("index: unknown format_version")

// Entry is one record's index data. Type is the declared record type
// name (the second segment of the full canonical address); Created and
// Updated are RFC3339 timestamps preserved across Save/Load round-trips.
type Entry struct {
	Type    string
	Created time.Time
	Updated time.Time
}

// Index is the in-memory representation of `.ta/index.toml`. The zero
// value is NOT directly usable for Save — callers should construct via
// Load (which seeds FormatVersion when the file is missing) or set
// FormatVersion explicitly.
//
// Records is keyed by the canonical address (the FULL dotted form,
// `<file-relpath>.<type>.<id-tail>`). Save expands each key back into
// nested TOML tables so the on-disk shape stays human-readable.
type Index struct {
	FormatVersion int
	Records       map[string]Entry
}

// Path returns the on-disk path of the index for projectRoot.
func Path(projectRoot string) string {
	return filepath.Join(projectRoot, config.SchemaDirName, IndexFileName)
}

// Load reads and parses `.ta/index.toml`. A missing file is NOT an
// error — Load returns an empty Index seeded with the current
// FormatVersion so callers can Put + Save without an explicit
// initialization step.
//
// Parse failures, unknown `format_version` values, and malformed entry
// tables surface wrapped errors. The Trust-and-fail-loud doctrine
// applies: no silent recovery, no auto-rebuild.
func Load(projectRoot string) (*Index, error) {
	path := Path(projectRoot)
	buf, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &Index{
				FormatVersion: FormatVersion,
				Records:       map[string]Entry{},
			}, nil
		}
		return nil, fmt.Errorf("index: read %s: %w", path, err)
	}
	return parseBytes(buf, path)
}

// parseBytes is the byte-slice variant of Load. Split out so tests can
// exercise the parser without touching the filesystem.
func parseBytes(buf []byte, sourcePath string) (*Index, error) {
	var raw map[string]any
	if err := toml.Unmarshal(buf, &raw); err != nil {
		return nil, fmt.Errorf("index: parse %s: %w", sourcePath, err)
	}

	idx := &Index{Records: map[string]Entry{}}

	// format_version is the only top-level scalar; pull it out so the
	// recursive walker only sees record-bearing tables.
	rawVersion, ok := raw[formatVersionKey]
	if !ok {
		return nil, fmt.Errorf("index: %s: missing %s scalar", sourcePath, formatVersionKey)
	}
	delete(raw, formatVersionKey)

	switch v := rawVersion.(type) {
	case int64:
		idx.FormatVersion = int(v)
	case int:
		idx.FormatVersion = v
	default:
		return nil, fmt.Errorf("index: %s: %s must be an integer, got %T",
			sourcePath, formatVersionKey, rawVersion)
	}
	if idx.FormatVersion != FormatVersion {
		return nil, fmt.Errorf(
			"%w: %s declares %d, this build supports %d (run `ta index rebuild`)",
			ErrUnknownFormatVersion, sourcePath, idx.FormatVersion, FormatVersion)
	}

	// Walk the remaining tree; flatten each leaf entry into a dotted
	// canonical address. Leaves are detected by the presence of a "type"
	// string at that level (Entry has type/created/updated and never
	// nests further).
	if err := flattenInto(raw, nil, idx.Records, sourcePath); err != nil {
		return nil, err
	}
	return idx, nil
}

// flattenInto walks node depth-first. Tables containing a "type" string
// key are treated as Entry leaves and added to records under their
// dotted-path key. Tables without "type" are intermediate nodes — their
// sub-tables are recursed into.
func flattenInto(node map[string]any, prefix []string, records map[string]Entry, sourcePath string) error {
	if isEntryNode(node) {
		entry, err := decodeEntry(node, prefix, sourcePath)
		if err != nil {
			return err
		}
		key := strings.Join(prefix, ".")
		records[key] = entry
		return nil
	}

	// Stable iteration so error messages reference the same key on
	// re-runs of the same input.
	keys := make([]string, 0, len(node))
	for k := range node {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		child, ok := node[k].(map[string]any)
		if !ok {
			canonical := strings.Join(append(append([]string{}, prefix...), k), ".")
			return fmt.Errorf(
				"index: %s: unexpected scalar at %q (expected nested table or Entry)",
				sourcePath, canonical)
		}
		if err := flattenInto(child, append(prefix, k), records, sourcePath); err != nil {
			return err
		}
	}
	return nil
}

// isEntryNode reports whether node looks like an Entry leaf — i.e.
// contains a "type" string key. The type key is the cheapest
// distinguishing marker and is always present on Entry tables.
func isEntryNode(node map[string]any) bool {
	v, ok := node["type"]
	if !ok {
		return false
	}
	_, isString := v.(string)
	return isString
}

// decodeEntry pulls type/created/updated out of an Entry-shaped node.
// Missing or wrong-typed fields produce wrapped errors that name the
// canonical address.
func decodeEntry(node map[string]any, prefix []string, sourcePath string) (Entry, error) {
	canonical := strings.Join(prefix, ".")

	typ, ok := node["type"].(string)
	if !ok {
		return Entry{}, fmt.Errorf("index: %s: %q: type must be a string", sourcePath, canonical)
	}

	created, err := decodeTime(node, "created", canonical, sourcePath)
	if err != nil {
		return Entry{}, err
	}
	updated, err := decodeTime(node, "updated", canonical, sourcePath)
	if err != nil {
		return Entry{}, err
	}
	return Entry{Type: typ, Created: created, Updated: updated}, nil
}

// decodeTime reads a time-typed field. go-toml/v2 decodes RFC3339
// datetime values as time.Time directly; defensive fallback parses an
// RFC3339 string for hand-edited files.
func decodeTime(node map[string]any, key, canonical, sourcePath string) (time.Time, error) {
	raw, ok := node[key]
	if !ok {
		return time.Time{}, fmt.Errorf(
			"index: %s: %q: missing %s timestamp", sourcePath, canonical, key)
	}
	switch v := raw.(type) {
	case time.Time:
		return v, nil
	case string:
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return time.Time{}, fmt.Errorf(
				"index: %s: %q: parse %s timestamp %q: %w",
				sourcePath, canonical, key, v, err)
		}
		return t, nil
	default:
		return time.Time{}, fmt.Errorf(
			"index: %s: %q: %s must be a datetime, got %T",
			sourcePath, canonical, key, raw)
	}
}

// Save atomically writes idx to `<projectRoot>/.ta/index.toml`. The
// `.ta/` directory is created when missing; entries are serialized in
// canonical-address order so consecutive Saves of the same Index produce
// byte-identical output (modulo content changes).
func (idx *Index) Save(projectRoot string) error {
	if idx == nil {
		return fmt.Errorf("index: Save: nil receiver")
	}
	if idx.FormatVersion == 0 {
		// Tolerate the zero value for first-time callers; default to
		// the current FormatVersion rather than silently writing a 0.
		idx.FormatVersion = FormatVersion
	}
	if idx.FormatVersion != FormatVersion {
		return fmt.Errorf(
			"%w: cannot Save Index with FormatVersion=%d (this build supports %d)",
			ErrUnknownFormatVersion, idx.FormatVersion, FormatVersion)
	}

	dir := filepath.Join(projectRoot, config.SchemaDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("index: mkdir %s: %w", dir, err)
	}

	buf, err := idx.encode()
	if err != nil {
		return err
	}
	return fsatomic.Write(Path(projectRoot), buf)
}

// encode serializes idx to TOML bytes. The format_version scalar is
// emitted first; record entries are nested into their canonical-address
// path so go-toml/v2 produces `[phase_1.db.t1]` headers naturally.
func (idx *Index) encode() ([]byte, error) {
	root := map[string]any{
		formatVersionKey: int64(idx.FormatVersion),
	}

	keys := make([]string, 0, len(idx.Records))
	for k := range idx.Records {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, canonical := range keys {
		entry := idx.Records[canonical]
		if err := nestEntry(root, canonical, entry); err != nil {
			return nil, err
		}
	}

	buf, err := toml.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("index: marshal: %w", err)
	}
	return buf, nil
}

// nestEntry expands canonical (a dotted address) into nested maps under
// root and writes the entry's leaf table at the deepest position.
// Collisions with an intermediate table return an error rather than
// silently overwriting.
func nestEntry(root map[string]any, canonical string, entry Entry) error {
	if canonical == "" {
		return fmt.Errorf("index: cannot nest empty canonical address")
	}
	if canonical == formatVersionKey {
		return fmt.Errorf("index: canonical address %q collides with reserved scalar", canonical)
	}
	segs := strings.Split(canonical, ".")
	for _, s := range segs {
		if s == "" {
			return fmt.Errorf("index: canonical address %q has empty segment", canonical)
		}
	}

	cur := root
	for i, seg := range segs {
		last := i == len(segs)-1
		if last {
			if existing, ok := cur[seg]; ok {
				return fmt.Errorf(
					"index: canonical address %q collides with existing node (type %T)",
					canonical, existing)
			}
			cur[seg] = entryToMap(entry)
			return nil
		}
		next, ok := cur[seg]
		if !ok {
			fresh := map[string]any{}
			cur[seg] = fresh
			cur = fresh
			continue
		}
		nextMap, ok := next.(map[string]any)
		if !ok {
			return fmt.Errorf(
				"index: canonical address %q traverses non-table at segment %q",
				canonical, seg)
		}
		cur = nextMap
	}
	return nil
}

// entryToMap converts an Entry to its TOML-marshalable map form.
func entryToMap(e Entry) map[string]any {
	return map[string]any{
		"type":    e.Type,
		"created": e.Created.UTC(),
		"updated": e.Updated.UTC(),
	}
}

// Get returns the Entry for canonical. The bool is false when the entry
// is not present.
func (idx *Index) Get(canonical string) (Entry, bool) {
	if idx == nil || idx.Records == nil {
		return Entry{}, false
	}
	e, ok := idx.Records[canonical]
	return e, ok
}

// Put inserts or updates an entry under canonical.
//
// On insert (no prior entry), Created defaults to time.Now().UTC() when
// the caller passed the zero value; Updated likewise.
//
// On update (prior entry exists), Created is preserved from the prior
// entry — callers cannot retroactively rewrite the creation timestamp
// even if they pass a non-zero value. Updated defaults to
// time.Now().UTC() when the caller passed the zero value.
func (idx *Index) Put(canonical string, e Entry) {
	if idx.Records == nil {
		idx.Records = map[string]Entry{}
	}
	now := time.Now().UTC()
	prior, exists := idx.Records[canonical]
	if exists {
		// Preserve original creation timestamp regardless of what the
		// caller passed — Created is monotone over a record's lifetime.
		e.Created = prior.Created
	} else if e.Created.IsZero() {
		e.Created = now
	}
	if e.Updated.IsZero() {
		e.Updated = now
	}
	// Normalize to UTC for byte-identical Save output.
	e.Created = e.Created.UTC()
	e.Updated = e.Updated.UTC()
	idx.Records[canonical] = e
}

// Delete removes the entry under canonical. No-op when not present.
func (idx *Index) Delete(canonical string) {
	if idx == nil || idx.Records == nil {
		return
	}
	delete(idx.Records, canonical)
}

// Walk yields every entry in canonical-address order. fn returns true
// to continue iteration, false to stop.
func (idx *Index) Walk(fn func(canonical string, e Entry) bool) {
	if idx == nil || idx.Records == nil {
		return
	}
	keys := make([]string, 0, len(idx.Records))
	for k := range idx.Records {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !fn(k, idx.Records[k]) {
			return
		}
	}
}
