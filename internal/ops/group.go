// Package-level note: this file delivers the group-prefix resolution
// primitives consumed by L3-C2 (CLI dispatch) and L3-C3 (MCP dispatch).
// See drop_004.drop.l3_c1_ops_group_aggregate for the planner record.
//
// Symbol cites in surrounding L2/L3 records may name approximate line
// numbers from a prior revision of ops.go (e.g. 1095 / 1072 for
// IsScopeAddress / ScopeRecord). Locate by name via Grep / LSP; line
// numbers drift across edits.

package ops

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/evanmschultz/ta/internal/index"
)

// ErrNoGroup is returned by GetGroup when id resolves to zero children
// under the separator-strict prefix `id + "."`. The id may itself be
// a fully-qualified record id (route through Get instead), a
// file-as-record id (route through Get), or simply absent from the
// index. Callers MUST first guard with IsGroupPrefix or accept the
// error and fall back to Get; GetGroup never auto-falls-back.
var ErrNoGroup = errors.New("ops: id is not a group prefix (no child records)")

// IsGroupPrefix reports whether id names a group of records — that is,
// whether at least one canonical record id in idx begins with the
// separator-strict prefix `id + "."`.
//
// Separator strictness matters: `drop_002` is a prefix-string of
// `drop_002_v2` but not a group prefix of it, because the segments
// diverge before the next `.`. The trailing dot in the scan prefix
// enforces this. The pattern mirrors index.DeleteByFile /
// index.CountByFile, which use `fileRelPath + "."` for the same reason.
//
// F31 (file-as-record) handling: when a record IS the file, its index
// key is the file-relpath verbatim (no `.bracket` suffix; see
// index/rebuild.go::indexFileRecordBuf). The separator-strict scan
// therefore naturally returns false for a file-as-record id — its own
// index key does not match the `id + "."` prefix, and no sibling keys
// share that prefix. No additional schema lookup is required; the test
// suite pins this property as _FileAsRecordReturnsFalse.
//
// Returns false for an empty id, an empty / nil index, and any id that
// has zero children under it.
func IsGroupPrefix(idx *index.Index, id string) bool {
	_, ok := resolveGroupPrefix(idx, id)
	return ok
}

// resolveGroupPrefix returns the sorted list of canonical child ids
// under id and a flag reporting whether at least one child exists.
// Sort order matches the rest of the index package (sort.Strings on
// canonical ids; see index.Walk in index.go).
//
// The function is intentionally pure and operates on a live *Index
// snapshot — callers that need a fresh view should Load the index
// first. A nil idx, an empty idx.Records, or an empty id all return
// (nil, false).
func resolveGroupPrefix(idx *index.Index, id string) ([]string, bool) {
	if idx == nil || idx.Records == nil || id == "" {
		return nil, false
	}
	prefix := id + "."
	var children []string
	for k := range idx.Records {
		if strings.HasPrefix(k, prefix) {
			children = append(children, k)
		}
	}
	if len(children) == 0 {
		return nil, false
	}
	sort.Strings(children)
	return children, true
}

// GetGroup aggregates every child record under the group-prefix id.
//
// Semantics:
//
//   - id MUST name a group prefix (at least one canonical id in the
//     index begins with `id + "."`). A non-group id returns
//     ErrNoGroup; callers must route fully-qualified record ids
//     through Get instead. The grammar is db-agnostic — children may
//     span multiple declared types under the same prefix (the
//     cascade.drop_NNN dogfood case where drop / planner / qa_proof
//     records all live under one parent prefix).
//
//   - fields slices each child record's returned field map to the
//     named subset (pass-through to ops.Get's `fields` parameter).
//     An empty / nil fields slice returns every field per Get's
//     default contract.
//
//   - limit caps the child slice length; limit <= 0 means "no cap".
//     all=true overrides limit entirely (mirrors GetScope / Search).
//     The cap applies AFTER canonical sort, so the returned subset is
//     deterministic.
//
//   - The on-disk read happens per-child via ops.Get rather than via
//     search.Run — bypassing the search walker keeps GetGroup aligned
//     with the index's authoritative type resolution and avoids the
//     glob-mount type-filter quirks that drove F38d-2.16. Per-child
//     errors abort with the underlying error wrapped so the caller
//     can disambiguate ErrRecordNotFound (index drift) from
//     ErrFileNotFound, etc.
//
// A missing `.ta/index.toml` surfaces ErrIndexMissing (loud failure;
// recovery via `ta index rebuild`).
func GetGroup(path, id string, fields []string, limit int, all bool) ([]ScopeRecord, error) {
	idx, err := loadIndexOrSentinel(path)
	if err != nil {
		return nil, err
	}
	children, ok := resolveGroupPrefix(idx, id)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrNoGroup, id)
	}
	if !all && limit > 0 && len(children) > limit {
		children = children[:limit]
	}
	out := make([]ScopeRecord, 0, len(children))
	for _, childID := range children {
		res, err := Get(path, childID, "", fields)
		if err != nil {
			return nil, fmt.Errorf("group %q: child %q: %w", id, childID, err)
		}
		out = append(out, ScopeRecord{
			ID:     childID,
			Bytes:  res.Bytes,
			Fields: res.Fields,
		})
	}
	return out, nil
}
