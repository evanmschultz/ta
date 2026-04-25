package index

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/evanmschultz/ta/internal/backend/md"
	tomlbackend "github.com/evanmschultz/ta/internal/backend/toml"
	"github.com/evanmschultz/ta/internal/config"
	"github.com/evanmschultz/ta/internal/db"
	"github.com/evanmschultz/ta/internal/record"
	"github.com/evanmschultz/ta/internal/schema"
)

// RebuildResult summarizes the on-disk walk performed by Rebuild. It is
// returned even on success so the CLI can surface concrete facts (record
// count, output path) in its laslig notice or `--json` payload.
type RebuildResult struct {
	// RecordsIndexed is the total number of entries written to the index.
	RecordsIndexed int
	// IndexPath is the absolute path of the regenerated `.ta/index.toml`.
	IndexPath string
	// Index is the freshly-built (and saved) Index, useful to callers
	// that want to inspect entries without re-loading from disk.
	Index *Index
}

// Rebuild walks every declared db's paths via the project resolver,
// opens each backing file, enumerates its declared records via the
// per-format backend, and regenerates `.ta/index.toml` from on-disk
// truth. Every entry's Created and Updated are stamped with the rebuild
// timestamp — historical timestamps are NOT recoverable from disk.
//
// Missing files / directories are skipped silently: rebuild reflects
// what is actually on disk, and an empty mount that has not been
// populated yet is a normal first-run state. Hard errors (parse
// failures inside an existing file, schema-load failure, write failure)
// surface wrapped.
func Rebuild(projectRoot string) (*RebuildResult, error) {
	if projectRoot == "" {
		return nil, fmt.Errorf("index: rebuild: empty project root")
	}

	resolution, err := config.Resolve(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("index: rebuild: resolve schema: %w", err)
	}

	idx := &Index{
		FormatVersion: FormatVersion,
		Records:       map[string]Entry{},
	}
	now := time.Now().UTC()

	resolver := db.NewResolver(projectRoot, resolution.Registry)

	// Iterate dbs in stable name order so two consecutive rebuilds
	// produce identical index files.
	dbNames := make([]string, 0, len(resolution.Registry.DBs))
	for name := range resolution.Registry.DBs {
		dbNames = append(dbNames, name)
	}
	sort.Strings(dbNames)

	for _, dbName := range dbNames {
		dbDecl := resolution.Registry.DBs[dbName]
		instances, err := resolver.Instances(dbName)
		if err != nil {
			return nil, fmt.Errorf("index: rebuild: db %q: %w", dbName, err)
		}
		for _, inst := range instances {
			if err := indexInstance(idx, dbDecl, inst, now); err != nil {
				return nil, fmt.Errorf("index: rebuild: db %q file %s: %w",
					dbName, inst.FilePath, err)
			}
		}
	}

	if err := idx.Save(projectRoot); err != nil {
		return nil, fmt.Errorf("index: rebuild: save: %w", err)
	}
	return &RebuildResult{
		RecordsIndexed: len(idx.Records),
		IndexPath:      Path(projectRoot),
		Index:          idx,
	}, nil
}

// indexInstance opens one backing file, enumerates its declared
// records, and inserts each into idx under the canonical-address key.
// A missing file is skipped silently — the mount may declare a path
// that has not been materialized yet.
func indexInstance(idx *Index, dbDecl schema.DB, inst db.Instance, stamp time.Time) error {
	buf, err := os.ReadFile(inst.FilePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read: %w", err)
	}

	switch dbDecl.Format {
	case schema.FormatTOML:
		return indexTOMLBuf(idx, dbDecl, inst, buf, stamp)
	case schema.FormatMD:
		return indexMDBuf(idx, dbDecl, inst, buf, stamp)
	default:
		return fmt.Errorf("unsupported format %q", dbDecl.Format)
	}
}

// tomlDeclaredTypes mirrors internal/ops.buildBackend's TOML branch
// (PLAN §12.17.9 Phase 9.2): single-file mounts prefix every declared
// type with the db name (`plans.task`) because brackets on disk carry
// the db prefix; multi-file mounts use bare type names. We replicate the
// rule here rather than importing internal/ops to avoid a dependency
// cycle (ops will import this package in Phase 9.4).
func tomlDeclaredTypes(dbDecl schema.DB) []record.DeclaredType {
	singleFile := schema.IsSingleFileDB(dbDecl)
	names := make([]string, 0, len(dbDecl.Types))
	for n := range dbDecl.Types {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]record.DeclaredType, 0, len(names))
	for _, n := range names {
		prefix := n
		if singleFile {
			prefix = dbDecl.Name + "." + n
		}
		out = append(out, record.DeclaredType{Name: prefix})
	}
	return out
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
// entry for each. The canonical address is computed from the instance
// slug (file-relpath) and the bracket path: when the bracket already
// starts with `<slug>.` (single-file db convention), the bracket IS the
// canonical address; otherwise the slug is prepended.
func indexTOMLBuf(idx *Index, dbDecl schema.DB, inst db.Instance, buf []byte, stamp time.Time) error {
	types := tomlDeclaredTypes(dbDecl)
	be := tomlbackend.NewBackend(types)
	paths, err := be.List(buf, "")
	if err != nil {
		return err
	}
	singleFile := schema.IsSingleFileDB(dbDecl)
	for _, p := range paths {
		typeName, ok := tomlTypeFromBracket(p, dbDecl, singleFile)
		if !ok {
			// Unrecognized type in a declared bracket should not
			// happen — declared brackets are filtered against the
			// declared-type prefix list at scan time. Fail loudly so a
			// bug here shows up rather than producing a silent miss.
			return fmt.Errorf("bracket %q: cannot resolve declared type", p)
		}
		canonical := canonicalForBracket(inst.Slug, p, dbDecl.Name, singleFile)
		idx.Put(canonical, Entry{Type: typeName, Created: stamp, Updated: stamp})
	}
	return nil
}

// indexMDBuf enumerates every declared MD heading-section in buf and
// adds an entry. MD addresses returned by List are `<type>.<chain>`;
// the canonical address prepends the instance slug.
func indexMDBuf(idx *Index, dbDecl schema.DB, inst db.Instance, buf []byte, stamp time.Time) error {
	types := mdDeclaredTypes(dbDecl)
	be, err := md.NewBackend(types)
	if err != nil {
		return fmt.Errorf("md backend: %w", err)
	}
	addresses, err := be.List(buf, "")
	if err != nil {
		return err
	}
	for _, addr := range addresses {
		typeName, ok := mdTypeFromAddress(addr, dbDecl)
		if !ok {
			return fmt.Errorf("md address %q: cannot resolve declared type", addr)
		}
		canonical := joinSlugAddr(inst.Slug, addr)
		idx.Put(canonical, Entry{Type: typeName, Created: stamp, Updated: stamp})
	}
	return nil
}

// canonicalForBracket joins the instance slug with a TOML bracket path
// to produce the canonical address `<file-relpath>.<type>.<id-tail>`.
// Multi-file dbs emit bare `<type>.<id>` brackets; the canonical is
// `<slug>.<bracket>`. Single-file dbs emit db-prefixed `<db>.<type>.<id>`
// brackets; the db prefix is stripped and the slug (file-relpath) is
// prepended so the canonical form aligns with db.Address.Canonical()
// across both shapes (slug == db name in the common single-file case
// but not required to be).
func canonicalForBracket(slug, bracket, dbName string, singleFile bool) string {
	rest := bracket
	if singleFile {
		dbPrefix := dbName + "."
		if strings.HasPrefix(bracket, dbPrefix) {
			rest = bracket[len(dbPrefix):]
		}
	}
	if slug == "" {
		return rest
	}
	return slug + "." + rest
}

// joinSlugAddr prepends the slug to addr with a "." separator unless
// slug is empty.
func joinSlugAddr(slug, addr string) string {
	if slug == "" {
		return addr
	}
	return slug + "." + addr
}

// tomlTypeFromBracket maps a declared-bracket path back to its declared
// type name on dbDecl. For single-file dbs the bracket starts with
// `<db>.<type>...`; the type is the second segment. For multi-file dbs
// the bracket starts with `<type>...`; the type is the first segment.
// The bool is false when the segment is not a declared type — a guard
// against scanner-vs-schema drift.
func tomlTypeFromBracket(bracket string, dbDecl schema.DB, singleFile bool) (string, bool) {
	rest := bracket
	if singleFile {
		dbPrefix := dbDecl.Name + "."
		if !strings.HasPrefix(bracket, dbPrefix) {
			return "", false
		}
		rest = bracket[len(dbPrefix):]
	}
	typeSeg, _, _ := strings.Cut(rest, ".")
	if typeSeg == "" {
		typeSeg = rest
	}
	if typeSeg == "" {
		return "", false
	}
	if _, ok := dbDecl.Types[typeSeg]; !ok {
		return "", false
	}
	return typeSeg, true
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
