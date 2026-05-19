package index

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/evanmschultz/ta/internal/backend/md"
	tomlbackend "github.com/evanmschultz/ta/internal/backend/toml"
	"github.com/evanmschultz/ta/internal/config"
	"github.com/evanmschultz/ta/internal/db"
	"github.com/evanmschultz/ta/internal/record"
	"github.com/evanmschultz/ta/internal/schema"
)

// RebuildResult summarizes the on-disk walk performed by Rebuild.
//
// PreservedCount counts entries whose Created timestamp was carried
// over from the prior on-disk index (best-effort load). FreshCount
// counts entries whose Created was stamped at rebuild time because no
// prior entry existed for that canonical id (or the prior index was
// missing / corrupt). PreservedCount + FreshCount == RecordsIndexed.
type RebuildResult struct {
	RecordsIndexed int
	PreservedCount int
	FreshCount     int
	IndexPath      string
	Index          *Index
}

// Rebuild walks every declared db's paths via the project resolver,
// opens each backing file, enumerates its declared records via the
// per-format backend, and regenerates `.ta/index.toml` from on-disk
// truth. Every entry's Updated is stamped with the rebuild timestamp.
// Created is preserved when a prior on-disk index entry exists for the
// same canonical id (best-effort load); otherwise Created is stamped
// with the rebuild timestamp.
//
// Per F10 (PLAN §12.17.9), the index format_version is 2 and every
// bracket key on disk IS the id (no type segment). The walker derives
// the type from the on-disk bracket prefix (which equals the type for
// records of any given db) and indexes the bracket-as-id verbatim.
//
// F14 Created-preserve semantics: the prior `.ta/index.toml` is loaded
// best-effort BEFORE the walk. Benign load failures (file missing,
// format_version unknown, TOML decode failure, shape errors from
// flattenInto) yield an empty prior map — the rebuild proceeds with
// all-fresh Created stamps. Permission / I/O errors propagate so a
// transient filesystem fault cannot silently erase historical Created
// timestamps.
func Rebuild(projectRoot string) (*RebuildResult, error) {
	if projectRoot == "" {
		return nil, fmt.Errorf("index: rebuild: empty project root")
	}

	resolution, err := config.Resolve(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("index: rebuild: resolve schema: %w", err)
	}

	prior, err := bestEffortPriorIndex(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("index: rebuild: load prior index: %w", err)
	}

	idx := &Index{
		FormatVersion: FormatVersion,
		Records:       map[string]Entry{},
	}
	now := time.Now().UTC()

	resolver := db.NewResolver(projectRoot, resolution.Registry)

	dbNames := make([]string, 0, len(resolution.Registry.DBs))
	for name := range resolution.Registry.DBs {
		dbNames = append(dbNames, name)
	}
	sort.Strings(dbNames)

	preserved := 0
	fresh := 0

	for _, dbName := range dbNames {
		dbDecl := resolution.Registry.DBs[dbName]
		instances, err := resolver.Instances(dbName)
		if err != nil {
			return nil, fmt.Errorf("index: rebuild: db %q: %w", dbName, err)
		}
		for _, inst := range instances {
			p, f, err := indexInstance(idx, dbName, dbDecl, inst, now, prior)
			if err != nil {
				return nil, fmt.Errorf("index: rebuild: db %q file %s: %w",
					dbName, inst.FilePath, err)
			}
			preserved += p
			fresh += f
		}
	}

	if err := idx.Save(projectRoot); err != nil {
		return nil, fmt.Errorf("index: rebuild: save: %w", err)
	}
	return &RebuildResult{
		RecordsIndexed: len(idx.Records),
		PreservedCount: preserved,
		FreshCount:     fresh,
		IndexPath:      Path(projectRoot),
		Index:          idx,
	}, nil
}

// bestEffortPriorIndex loads the prior on-disk index at projectRoot for
// Rebuild's Created-preserve pass. The error-class allowlist is
// load-bearing for F14:
//
//   - fs.ErrNotExist (no prior index) → empty map, nil.
//   - ErrUnknownFormatVersion (legacy / future on-disk schema) → empty
//     map, nil. Rebuild's job IS to refresh stale format_versions.
//   - *toml.DecodeError (malformed TOML) → empty map, nil. A corrupt
//     index file must not block recovery.
//   - parseBytes shape errors (flattenInto rejections, missing
//     format_version scalar, malformed entry tables) → empty map, nil.
//     Same reasoning: a structurally broken prior index is recovered
//     by overwriting it.
//
// Permission and I/O errors from os.ReadFile (anything that is NOT
// fs.ErrNotExist) PROPAGATE. A transient EACCES must not silently
// erase historical Created timestamps; the caller surfaces it as a
// rebuild failure and the operator addresses the filesystem fault.
func bestEffortPriorIndex(projectRoot string) (map[string]Entry, error) {
	path := Path(projectRoot)
	buf, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]Entry{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	idx, err := parseBytes(buf, path)
	if err != nil {
		if isBenignPriorIndexErr(err) {
			return map[string]Entry{}, nil
		}
		// Defensive: any class not on the allowlist propagates rather
		// than being silently swallowed. parseBytes is not expected to
		// return anything outside the allowlist today; this branch
		// protects against future drift.
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return idx.Records, nil
}

// isBenignPriorIndexErr reports whether err matches the F14 best-effort
// load allowlist (ErrUnknownFormatVersion, *toml.DecodeError, or any
// parseBytes shape error). Permission / I/O errors are checked in
// bestEffortPriorIndex BEFORE parseBytes is invoked; this helper only
// sees parse-time failures.
func isBenignPriorIndexErr(err error) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, ErrUnknownFormatVersion) {
		return true
	}
	var decErr *toml.DecodeError
	if errors.As(err, &decErr) {
		return true
	}
	// parseBytes returns fmt.Errorf-wrapped messages with the "index: "
	// prefix for every other shape failure (missing format_version
	// scalar, non-integer format_version, malformed entry tables,
	// unexpected scalars at intermediate nodes). All are recoverable
	// by overwriting the index — treat as benign.
	if strings.HasPrefix(err.Error(), "index: ") {
		return true
	}
	return false
}

// indexInstance opens one backing file, enumerates its declared
// records, and inserts each into idx under the canonical id key.
// Returns (preservedCount, freshCount, error) where preserved counts
// entries whose Created was carried over from prior and fresh counts
// entries stamped with the rebuild timestamp. dbName is threaded so
// every rebuilt Entry carries the authoritative DBName field
// (F38d-2.14c fast-path consumer at internal/ops/helpers.go).
func indexInstance(idx *Index, dbName string, dbDecl schema.DB, inst db.Instance, stamp time.Time, prior map[string]Entry) (int, int, error) {
	buf, err := os.ReadFile(inst.FilePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("read: %w", err)
	}

	switch dbDecl.Format {
	case schema.FormatTOML:
		return indexTOMLBuf(idx, dbName, dbDecl, inst, buf, stamp, prior)
	case schema.FormatMD:
		return indexMDBuf(idx, dbName, dbDecl, inst, buf, stamp, prior)
	default:
		return 0, 0, fmt.Errorf("unsupported format %q", dbDecl.Format)
	}
}

// stampedEntry returns the Entry to Put for canonical, plus a bool that
// is true when the Created timestamp was preserved from prior (false
// when stamped fresh). Centralizes the Created-preserve policy so every
// indexer call-site applies identical semantics. dbName populates
// Entry.DBName so resolveIDWithIndexHint's F38d-2.14c fast path keeps
// working post-rebuild (prior bug: rebuild emitted DBName="" and
// silently neutered the fast-path, surfacing the ambiguous-id failure
// mode F38d-2.14c was designed to prevent).
func stampedEntry(canonical, dbName, typeName string, stamp time.Time, prior map[string]Entry) (Entry, bool) {
	created := stamp
	preserved := false
	if p, ok := prior[canonical]; ok && !p.Created.IsZero() {
		created = p.Created
		preserved = true
	}
	return Entry{DBName: dbName, Type: typeName, Created: created, Updated: stamp}, preserved
}

// mdDeclaredTypes builds the MD backend's declared-type slice — bare
// type names plus their heading levels.
func mdDeclaredTypes(dbDecl schema.DB) []record.DeclaredType {
	names := make([]string, 0, len(dbDecl.Types))
	for n := range dbDecl.Types {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]record.DeclaredType, 0, len(names))
	for _, n := range names {
		t := dbDecl.Types[n]
		out = append(out, record.DeclaredType{Name: n, Heading: t.Heading})
	}
	return out
}

// indexTOMLBuf enumerates every declared bracket in buf and adds an
// entry for each. The canonical id is computed from the instance slug
// (file-relpath) and the bracket path.
//
// Per F10 the on-disk bracket IS the id verbatim. For single-file dbs
// the bracket already begins with the file-relpath (which equals the
// db name in the common case). For multi-file dbs the bracket is the
// bracket-key alone; the walker prepends the file-relpath to form the
// full id.
//
// Rebuild infers the type from the FIRST segment of the bracket-key
// that names a declared type on the db. This is recovery-only: writes
// route through ops.Create which records the authoritative type in
// the index directly.
func indexTOMLBuf(idx *Index, dbName string, dbDecl schema.DB, inst db.Instance, buf []byte, stamp time.Time, prior map[string]Entry) (int, int, error) {
	// Use per-instance SingleFileMount (post L3-G7-D4 fix) so multi-literal-path
	// mounts route through the correct branch per the file-relpath actually
	// present on disk. schema.IsSingleFileDB(dbDecl) is per-DB and incorrectly
	// fails for paths=[literal1, literal2] even when each instance is single-file.
	singleFile := inst.SingleFileMount
	// We'd like to enumerate every bracket; the TOML backend's List
	// requires a declared-prefix list. We construct one prefix per
	// declared type and union-list across all of them.
	allPaths := make(map[string]string) // bracket → typeName
	for typeName := range dbDecl.Types {
		var prefix string
		if singleFile {
			// Single-file: brackets begin with `<file-relpath>.<id-tail>`.
			// The id-tail's first segment may be the type, or may be a
			// user-chosen segment. We can't infer type from on-disk
			// alone; rebuild scans for brackets prefixed with the
			// file-relpath only. For typed-record disambiguation we
			// rely on having only one declared type, or on the index
			// being authoritatively repopulated by the next write.
			prefix = inst.Slug
		} else {
			prefix = typeName
		}
		paths, err := listAtPrefix(buf, dbDecl, []record.DeclaredType{{Name: prefix}})
		if err != nil {
			return 0, 0, err
		}
		for _, p := range paths {
			// Type assignment is the LAST type that scans this bracket.
			// For multi-file dbs prefix == typeName so it's exact.
			// For single-file dbs we'd like type-inference; we record
			// the first declared type we encounter as a placeholder.
			if _, exists := allPaths[p]; !exists {
				allPaths[p] = typeName
			}
		}
	}

	// Stable iteration order for deterministic output.
	keys := make([]string, 0, len(allPaths))
	for k := range allPaths {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	preserved := 0
	fresh := 0
	for _, p := range keys {
		typeName := allPaths[p]
		canonical := canonicalForBracket(inst.Slug, p, singleFile)
		entry, wasPreserved := stampedEntry(canonical, dbName, typeName, stamp, prior)
		idx.Put(canonical, entry)
		if wasPreserved {
			preserved++
		} else {
			fresh++
		}
	}
	return preserved, fresh, nil
}

// listAtPrefix is a thin wrapper around the TOML backend's List so the
// indexer can pass any declared-type prefix without re-deriving the
// scanner state.
func listAtPrefix(buf []byte, dbDecl schema.DB, types []record.DeclaredType) ([]string, error) {
	_ = dbDecl
	be := tomlbackend.NewBackend(types)
	return be.List(buf, "")
}

// indexMDBuf enumerates every declared MD heading-section in buf and
// adds an entry. MD addresses returned by List are `<type>.<chain>`;
// the canonical id prepends the instance slug.
//
// File-as-record dbs (F31) emit ONE record per file with the
// file-relpath itself as the canonical id (no chain, no bracket-key);
// dispatch to FileRecordBackend so heading=0 does not trip
// md.NewBackend's [1, 6] validator (F38b).
func indexMDBuf(idx *Index, dbName string, dbDecl schema.DB, inst db.Instance, buf []byte, stamp time.Time, prior map[string]Entry) (int, int, error) {
	if schema.DBHasFileAsRecord(dbDecl) {
		return indexFileRecordBuf(idx, dbName, dbDecl, inst, buf, stamp, prior)
	}
	types := mdDeclaredTypes(dbDecl)
	be, err := md.NewBackend(types)
	if err != nil {
		return 0, 0, fmt.Errorf("md backend: %w", err)
	}
	addresses, err := be.List(buf, "")
	if err != nil {
		return 0, 0, err
	}
	preserved := 0
	fresh := 0
	for _, addr := range addresses {
		typeName, ok := mdTypeFromAddress(addr, dbDecl)
		if !ok {
			return preserved, fresh, fmt.Errorf("md address %q: cannot resolve declared type", addr)
		}
		canonical := joinSlugAddr(inst.Slug, addr)
		entry, wasPreserved := stampedEntry(canonical, dbName, typeName, stamp, prior)
		idx.Put(canonical, entry)
		if wasPreserved {
			preserved++
		} else {
			fresh++
		}
	}
	return preserved, fresh, nil
}

// indexFileRecordBuf indexes a single file-as-record buffer (F31). Per
// the F38b locked decision the file IS the record: the canonical id is
// the instance slug (file-relpath) verbatim with no type segment, and
// the record's type is the one declared file-as-record type on the db
// (mixed-mode is rejected at schema load).
//
// An empty buffer yields no entry — `FileRecordBackend.List` returns
// no addresses for an empty buf, so the caller sees an unbacked id and
// the index correctly omits the record.
func indexFileRecordBuf(idx *Index, dbName string, dbDecl schema.DB, inst db.Instance, buf []byte, stamp time.Time, prior map[string]Entry) (int, int, error) {
	fileType, st, ok := singleFileRecordType(dbDecl)
	if !ok {
		return 0, 0, fmt.Errorf("index: db %q has file-as-record types but none resolved", dbDecl.Name)
	}
	types := []record.DeclaredType{{Name: fileType}}
	be, err := md.NewFileRecordBackend(types, fileType, st.BodyField)
	if err != nil {
		return 0, 0, fmt.Errorf("md file-as-record backend: %w", err)
	}
	addresses, err := be.List(buf, "")
	if err != nil {
		return 0, 0, err
	}
	// File-as-record yields at most ONE logical record per file even
	// though the backend's List loop may emit multiple equivalent
	// addresses. Count preserved/fresh exactly once on the first
	// iteration; subsequent Puts are idempotent (Index.Put preserves
	// the in-place Created automatically).
	preserved := 0
	fresh := 0
	for i := range addresses {
		entry, wasPreserved := stampedEntry(inst.Slug, dbName, fileType, stamp, prior)
		idx.Put(inst.Slug, entry)
		if i == 0 {
			if wasPreserved {
				preserved++
			} else {
				fresh++
			}
		}
	}
	return preserved, fresh, nil
}

// singleFileRecordType is the index-package mirror of
// ops.singleFileRecordType. Per F31's mixed-mode prohibition at most
// one file-as-record type can exist per db. Duplicated here rather
// than exported from schema (revisit when a fourth caller emerges; see
// F38b locked decisions).
func singleFileRecordType(dbDecl schema.DB) (string, schema.SectionType, bool) {
	for name, t := range dbDecl.Types {
		if t.IsFileRecord() {
			return name, t, true
		}
	}
	return "", schema.SectionType{}, false
}

// canonicalForBracket joins the instance slug with a TOML bracket path
// to produce the canonical id `<file-relpath>.<bracket-key>`.
//
// Per F10 the on-disk bracket IS the id verbatim:
//
//   - Single-file dbs: the bracket already begins with the file-relpath
//     (`[plans.demo-1]`). The id is the bracket as-is.
//   - Multi-file dbs (glob): the bracket is the bracket-key alone
//     (`[note-001]`). The id is `<file-relpath>.<bracket>`.
func canonicalForBracket(slug, bracket string, singleFile bool) string {
	if singleFile {
		// Bracket already includes the file-relpath; it IS the id.
		return bracket
	}
	if slug == "" {
		return bracket
	}
	return slug + "." + bracket
}

// joinSlugAddr prepends the slug to addr with a "." separator unless
// slug is empty.
func joinSlugAddr(slug, addr string) string {
	if slug == "" {
		return addr
	}
	return slug + "." + addr
}

// mdTypeFromAddress extracts the declared type name from an MD address
// returned by md.Backend.List. Addresses are `<type>.<chain>`, so the
// first segment is the type.
func mdTypeFromAddress(address string, dbDecl schema.DB) (string, bool) {
	typeSeg, _, _ := strings.Cut(address, ".")
	if typeSeg == "" {
		typeSeg = address
	}
	if typeSeg == "" {
		return "", false
	}
	if _, ok := dbDecl.Types[typeSeg]; !ok {
		return "", false
	}
	return typeSeg, true
}
