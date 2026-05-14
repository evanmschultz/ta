package ops

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/evanmschultz/ta/internal/db"
	"github.com/evanmschultz/ta/internal/index"
	"github.com/evanmschultz/ta/internal/schema"
)

// spliceOut returns buf with the bytes in rng removed.
func spliceOut(buf []byte, rng [2]int) []byte {
	out := make([]byte, 0, len(buf)-(rng[1]-rng[0]))
	out = append(out, buf[:rng[0]]...)
	out = append(out, buf[rng[1]:]...)
	return out
}

// readFileIfExists returns the file bytes or nil if the file does not
// exist. Any other error is returned as-is.
func readFileIfExists(path string) ([]byte, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return buf, nil
}

// validationPath rebuilds the "<db>.<type>.<id>" form schema.Validate
// expects, given a resolved id and its bare type name. The validation
// form is INTERNAL and independent of the user-facing id grammar — it
// is the legacy address shape used by Validate's two-segment lookup.
func validationPath(resolved db.Resolved, bareType string) string {
	parts := []string{resolved.DBName, bareType}
	if resolved.BracketKey != "" {
		parts = append(parts, resolved.BracketKey)
	}
	return joinDot(parts)
}

// tomlRelPathForFields returns the backend-relative record path for
// use by extractTOMLFields. Per F10 the on-disk bracket IS the id, so
// the relative path is the bracket-key alone for multi-file dbs and
// the file-relpath-prefixed form for single-file dbs (where the
// file-relpath sits inside the bracket).
func tomlRelPathForFields(resolved db.Resolved) string {
	if resolved.SingleFileMount {
		// Single-file: bracket is `<file-relpath>.<bracket-key>`; the
		// pelletier-decoded TOML root needs the full path to descend
		// from `root[<file-relpath>][<bracket-key>...]`.
		return resolved.Canonical()
	}
	return resolved.BracketKey
}

// joinDot joins non-empty segments with '.'.
func joinDot(parts []string) string {
	out := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		if out == "" {
			out = p
			continue
		}
		out += "." + p
	}
	return out
}

// resolveTypeForID resolves the authoritative type for a given id. Per
// F10 (PLAN §12.17.9 §2 truth table):
//
//	typeName     | requireType | Index hit? | Behavior
//	"" (empty)   | true        | n/a        | ErrTypeMismatch (Create needs --type)
//	"" (empty)   | false       | yes        | Return index entry's type
//	"" (empty)   | false       | no         | ErrTypeUnresolved
//	<db>.<type>  | true        | n/a        | Validate against schema; return type
//	<db>.<type>  | false       | yes (match)| Return matching type
//	<db>.<type>  | false       | yes (mis)  | ErrTypeMismatch
//	<db>.<type>  | false       | no         | Return typeName as authoritative
//	<bare>       | any         | n/a        | ErrTypeNotQualified
//
// On success returns the BARE type name (for downstream schema
// lookup); ErrIndexMissing wraps a missing-file index load failure.
func resolveTypeForID(resolved db.Resolved, typeName string, requireType bool, projectRoot string, declaredTypes map[string]struct{}) (string, error) {
	if typeName == "" {
		if requireType {
			return "", fmt.Errorf("%w: create requires --type (db-qualified, e.g. `plans.task`)", ErrTypeMismatch)
		}
		// Look up the index entry for the canonical id. Missing-index
		// or missing-entry falls through to a best-effort "first
		// declared type" resolution — F10 keeps the loud-fail
		// discipline only for multi-type dbs where the choice is
		// genuinely ambiguous.
		if idx, err := tryLoadIndex(projectRoot); err == nil && idx != nil {
			if entry, ok := idx.Get(resolved.Canonical()); ok {
				return entry.Type, nil
			}
		}
		// No index entry. Pick the first declared type if there is
		// only one; otherwise fail loudly per F10.
		if len(declaredTypes) == 1 {
			for n := range declaredTypes {
				return n, nil
			}
		}
		return "", fmt.Errorf("%w: id %q has no index entry and db has multiple declared types",
			ErrTypeUnresolved, resolved.Canonical())
	}
	// typeName must be db-qualified (`<db>.<type>`).
	dot := strings.Index(typeName, ".")
	if dot < 0 {
		return "", fmt.Errorf("%w: got %q, want `%s.<type>`", ErrTypeNotQualified, typeName, resolved.DBName)
	}
	dbPart := typeName[:dot]
	bareType := typeName[dot+1:]
	if dbPart != resolved.DBName {
		return "", fmt.Errorf("%w: type db %q does not match resolved db %q",
			ErrTypeMismatch, dbPart, resolved.DBName)
	}
	if bareType == "" {
		return "", fmt.Errorf("%w: empty type after %q.", ErrTypeNotQualified, dbPart)
	}
	if _, declared := declaredTypes[bareType]; !declared {
		return "", fmt.Errorf("%w: type %q not declared on db %q",
			ErrTypeMismatch, bareType, resolved.DBName)
	}
	if requireType {
		return bareType, nil
	}
	// Optional path: cross-check against index when present; tolerate
	// missing index (no entry means caller-supplied is authoritative).
	if idx, err := tryLoadIndex(projectRoot); err == nil && idx != nil {
		if entry, ok := idx.Get(resolved.Canonical()); ok {
			if entry.Type != bareType {
				return "", fmt.Errorf(
					"%w: index records type %q for %q but caller supplied %q (run `ta index rebuild`)",
					ErrTypeMismatch, entry.Type, resolved.Canonical(), bareType,
				)
			}
		}
	}
	return bareType, nil
}

// tryLoadIndex loads the project's index, returning nil (no error)
// when the index file is absent so callers can fall back to a
// best-effort path. Other errors propagate wrapped.
func tryLoadIndex(projectRoot string) (*index.Index, error) {
	path := index.Path(projectRoot)
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("ops: stat index: %w", err)
	}
	idx, err := index.Load(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("ops: load index: %w", err)
	}
	return idx, nil
}

// resolveIDWithIndexHint is the F38d-2.14 disambiguation entry point
// for read paths (Get, GetAllFields) and optional-type mutation paths
// (Update). When the index carries an entry for id, it uses the indexed
// bare type to constrain ResolveID to the correct db — preventing an
// alphabetically-earlier db with a looser mount shape from swallowing
// the id (the canonical failure mode when two dbs both accept the same
// id namespace, e.g. claude_agents glob `agents/*/*.md` and plans
// single-file `.ta/cascade/plans.toml`).
//
// Algorithm:
//  1. Load the index. If absent or load fails, fall through.
//  2. Get the entry for id. If missing, fall through.
//  3. Scan registry dbs in stable order. For each db that declares the
//     indexed bare type, try ResolveIDInDB. First success wins.
//  4. Fallback: plain ResolveID (index-orphan and no-index recovery).
func resolveIDWithIndexHint(resolver *db.Resolver, reg schema.Registry, projectRoot, id string) (db.Resolved, schema.DB, error) {
	if idx, err := tryLoadIndex(projectRoot); err == nil && idx != nil {
		if entry, ok := idx.Get(id); ok && entry.Type != "" {
			dbNames := make([]string, 0, len(reg.DBs))
			for name := range reg.DBs {
				dbNames = append(dbNames, name)
			}
			sort.Strings(dbNames)
			for _, dbName := range dbNames {
				dbDecl := reg.DBs[dbName]
				if _, hasType := dbDecl.Types[entry.Type]; !hasType {
					continue
				}
				if res, decl, resolveErr := resolver.ResolveIDInDB(id, dbName); resolveErr == nil {
					return res, decl, nil
				}
			}
		}
	}
	return resolver.ResolveID(id)
}

// loadIndexOrSentinel is the strict variant: a missing index file
// surfaces ErrIndexMissing. Used by callers that want loud failure.
func loadIndexOrSentinel(projectRoot string) (*index.Index, error) {
	idx, err := tryLoadIndex(projectRoot)
	if err != nil {
		return nil, err
	}
	if idx == nil {
		return nil, fmt.Errorf("%w: %s", ErrIndexMissing, index.Path(projectRoot))
	}
	return idx, nil
}

// writeIndexEntry upserts the canonical id into `.ta/index.toml`
// after a successful Create / Update.
func writeIndexEntry(projectRoot string, resolved db.Resolved, typeName string) error {
	// On write paths, missing index is OK; we'll create it.
	idx, err := index.Load(projectRoot)
	if err != nil {
		return fmt.Errorf("ops: load index: %w (record on disk; run `ta index rebuild`)", err)
	}
	now := time.Now().UTC()
	idx.Put(resolved.Canonical(), index.Entry{
		Type:    typeName,
		Created: now,
		Updated: now,
	})
	if err := idx.Save(projectRoot); err != nil {
		return fmt.Errorf("ops: save index: %w (record on disk; run `ta index rebuild`)", err)
	}
	return nil
}

// deleteIndexEntry removes the canonical id from `.ta/index.toml`
// after a successful Delete. A missing entry is a no-op.
//
// Per F10 (PLAN §12.17.9), the legacy ErrUnknownFormatVersion
// tolerance retires — any load failure including a stale format
// version surfaces loudly. The disk delete already succeeded; the
// caller wraps the error so the user sees both facts.
func deleteIndexEntry(projectRoot string, resolved db.Resolved) error {
	idx, err := index.Load(projectRoot)
	if err != nil {
		return fmt.Errorf("ops: load index: %w (record removed from disk; run `ta index rebuild`)", err)
	}
	idx.Delete(resolved.Canonical())
	if err := idx.Save(projectRoot); err != nil {
		return fmt.Errorf("ops: save index: %w (record removed from disk; run `ta index rebuild`)", err)
	}
	return nil
}

// deleteIndexEntriesByFile removes every index entry whose canonical
// id begins with `<fileRelPath>.` after a successful file-level delete
// (F19). Per the locked non-atomic semantics: the disk file is removed
// first; an index-load or index-save failure here surfaces with the
// "file removed; run `ta index rebuild`" hint so the operator can
// reconcile.
func deleteIndexEntriesByFile(projectRoot, fileRelPath string) error {
	idx, err := index.Load(projectRoot)
	if err != nil {
		return fmt.Errorf("ops: load index: %w (file removed from disk; run `ta index rebuild`)", err)
	}
	idx.DeleteByFile(fileRelPath)
	if err := idx.Save(projectRoot); err != nil {
		return fmt.Errorf("ops: save index: %w (file removed from disk; run `ta index rebuild`)", err)
	}
	return nil
}

// countRecordsInFile returns the number of index entries whose
// canonical id begins with `<fileRelPath>.`. Used by DeleteWithOptions
// to populate DeleteResult.RemainingInFile per F20 (the verbose-flag
// remaining-count is file-scoped). Returns 0 (and a nil error) when
// the index file is absent.
func countRecordsInFile(projectRoot, fileRelPath string) (int, error) {
	idx, err := tryLoadIndex(projectRoot)
	if err != nil {
		return 0, err
	}
	if idx == nil {
		return 0, nil
	}
	return idx.CountByFile(fileRelPath), nil
}
