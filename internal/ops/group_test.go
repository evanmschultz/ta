package ops_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/index"
	"github.com/evanmschultz/ta/internal/ops"
)

// makeIdx builds an in-memory *index.Index with the given canonical
// keys. Entry payloads are zero-value beyond Type since IsGroupPrefix
// only consults map keys.
func makeIdx(keys map[string]string) *index.Index {
	records := make(map[string]index.Entry, len(keys))
	for k, typ := range keys {
		records[k] = index.Entry{Type: typ}
	}
	return &index.Index{Records: records}
}

// TestOps_IsGroupPrefix_BasicPrefix — an id with at least one child
// canonical key under it is a group prefix.
func TestOps_IsGroupPrefix_BasicPrefix(t *testing.T) {
	idx := makeIdx(map[string]string{
		"cascade.drop_001.builder":          "builder",
		"cascade.drop_001.builder-qa-proof": "qa_proof",
		"cascade.drop_002.builder":          "builder",
	})
	if !ops.IsGroupPrefix(idx, "cascade.drop_001") {
		t.Fatalf("IsGroupPrefix(%q) = false, want true", "cascade.drop_001")
	}
}

// TestOps_IsGroupPrefix_PrefixCollisionGuard — separator-strict scan
// must reject id strings that are textual prefixes of an existing key
// without a `.` boundary between them. `drop_002` is a *string* prefix
// of `drop_002_v2` but NOT a group prefix.
func TestOps_IsGroupPrefix_PrefixCollisionGuard(t *testing.T) {
	idx := makeIdx(map[string]string{
		"cascade.drop_002_v2.builder": "builder",
	})
	if ops.IsGroupPrefix(idx, "cascade.drop_002") {
		t.Fatalf("IsGroupPrefix(%q) = true, want false (separator-strict guard)",
			"cascade.drop_002")
	}
}

// TestOps_IsGroupPrefix_FileAsRecordReturnsFalse — F31 file-as-record
// entries land in the index with key == file-relpath (no `.bracket`
// suffix; see index/rebuild.go::indexFileRecordBuf). The id itself is
// present in the index but has no children under `id + "."`, so
// IsGroupPrefix returns false. R1 audit + cascade-methodology rule:
// file-as-record ids route through Get, never GetGroup.
func TestOps_IsGroupPrefix_FileAsRecordReturnsFalse(t *testing.T) {
	idx := makeIdx(map[string]string{
		"claude_agents/writer.md": "agent",
		"claude_agents/editor.md": "agent",
	})
	if ops.IsGroupPrefix(idx, "claude_agents/writer.md") {
		t.Fatalf("IsGroupPrefix on file-as-record id = true, want false")
	}
}

// TestOps_IsGroupPrefix_UnknownID — an id that does not appear in the
// index at all (neither exact nor as a prefix) returns false.
func TestOps_IsGroupPrefix_UnknownID(t *testing.T) {
	idx := makeIdx(map[string]string{
		"cascade.drop_001.builder": "builder",
	})
	if ops.IsGroupPrefix(idx, "cascade.does_not_exist") {
		t.Fatalf("IsGroupPrefix on unknown id = true, want false")
	}
}

// TestOps_IsGroupPrefix_EmptyGroup — an empty index, a nil index, and
// an empty id string all yield false. These are the conservative
// no-children short-circuits the helper documents.
func TestOps_IsGroupPrefix_EmptyGroup(t *testing.T) {
	cases := []struct {
		name string
		idx  *index.Index
		id   string
	}{
		{name: "nil index", idx: nil, id: "cascade.drop_001"},
		{name: "empty Records", idx: &index.Index{Records: map[string]index.Entry{}}, id: "cascade.drop_001"},
		{name: "empty id", idx: makeIdx(map[string]string{"cascade.drop_001.builder": "builder"}), id: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if ops.IsGroupPrefix(tc.idx, tc.id) {
				t.Fatalf("IsGroupPrefix(%s, %q) = true, want false", tc.name, tc.id)
			}
		})
	}
}

// TestOps_IsGroupPrefix_MultiDB — the actual cascade dogfood case: a
// single group prefix spans children from multiple db types
// (cascade.drop + cascade.planner under the same parent prefix). The
// prefix scan is db-agnostic; any canonical id begins with the
// file-relpath segment, so cross-type aggregation works for free.
// RESTORED per L3-C1 AMEND (was contracted away in the original L2-C
// 10-test list; both plan-QA halves flagged it as critical).
func TestOps_IsGroupPrefix_MultiDB(t *testing.T) {
	idx := makeIdx(map[string]string{
		"cascade.drop_001.drop":       "drop",
		"cascade.drop_001.planner_l2": "planner",
		"cascade.drop_001.builder_a":  "builder",
	})
	if !ops.IsGroupPrefix(idx, "cascade.drop_001") {
		t.Fatalf("IsGroupPrefix on multi-type group = false, want true")
	}
}

// createDrop is a tiny convenience that materialises one cascade.drop
// record under the shared `cascadeDropSchema` (defined in ops_test.go).
// All GetGroup disk-based tests share this helper to keep the bracket
// arithmetic uniform.
func createDrop(t *testing.T, root, id string, dropNumber int) {
	t.Helper()
	if _, _, err := ops.Create(root, id, "cascade.drop", map[string]any{
		"structural_type": "drop",
		"drop_number":     dropNumber,
	}); err != nil {
		t.Fatalf("Create %q: %v", id, err)
	}
}

// TestOps_GetGroup_AggregatesChildren — the happy path. Three children
// under one group-prefix parent all surface via GetGroup, each with
// non-empty bytes and the expected canonical id. Sort order is
// asserted in the dedicated CanonicalSortOrder test below; here we
// just pin set-membership and the parent-prefix grammar.
func TestOps_GetGroup_AggregatesChildren(t *testing.T) {
	root := t.TempDir()
	writeSchema(t, root, cascadeDropSchema)

	createDrop(t, root, "drop_001.drop.alpha", 1)
	createDrop(t, root, "drop_001.drop.beta", 2)
	createDrop(t, root, "drop_001.drop.gamma", 3)

	got, err := ops.GetGroup(root, "drop_001.drop", nil, 0, true)
	if err != nil {
		t.Fatalf("GetGroup: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("GetGroup returned %d records, want 3: %+v", len(got), got)
	}
	ids := make(map[string]bool, len(got))
	for _, rec := range got {
		ids[rec.ID] = true
		if len(rec.Bytes) == 0 {
			t.Errorf("record %q: empty Bytes; want non-empty record body", rec.ID)
		}
	}
	for _, want := range []string{
		"drop_001.drop.alpha",
		"drop_001.drop.beta",
		"drop_001.drop.gamma",
	} {
		if !ids[want] {
			t.Errorf("GetGroup result missing id %q; got ids %v", want, ids)
		}
	}
}

// TestOps_GetGroup_CanonicalSortOrder — children come back in
// sort.Strings order on the canonical id, mirroring resolveGroupPrefix
// + index.Walk (sort.Strings on map keys). Insertion order is
// deliberately reversed to prove the function is not just emitting
// map iteration order.
func TestOps_GetGroup_CanonicalSortOrder(t *testing.T) {
	root := t.TempDir()
	writeSchema(t, root, cascadeDropSchema)

	createDrop(t, root, "drop_001.drop.gamma", 3)
	createDrop(t, root, "drop_001.drop.alpha", 1)
	createDrop(t, root, "drop_001.drop.beta", 2)

	got, err := ops.GetGroup(root, "drop_001.drop", nil, 0, true)
	if err != nil {
		t.Fatalf("GetGroup: %v", err)
	}
	want := []string{
		"drop_001.drop.alpha",
		"drop_001.drop.beta",
		"drop_001.drop.gamma",
	}
	if len(got) != len(want) {
		t.Fatalf("GetGroup returned %d records, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].ID != w {
			t.Errorf("GetGroup[%d].ID = %q, want %q (full order: %v)",
				i, got[i].ID, w, idsOf(got))
		}
	}
}

// idsOf is a tiny helper used by the sort-order assertion error path.
func idsOf(recs []ops.ScopeRecord) []string {
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = r.ID
	}
	return out
}

// TestOps_GetGroup_MultiDB — RESTORED per L3-C1 AMEND. Both plan-QA
// halves flagged this as critical: a single group prefix in the
// dogfood cascade naturally spans multiple declared types
// (cascade.drop, cascade.planner, etc.) under one shared parent
// prefix. GetGroup must surface all child types — the prefix scan is
// db-agnostic. Uses cascadeMultiTypeSchema (defined in ops_test.go)
// which declares both `drop` and `planner` under one glob-TOML mount.
func TestOps_GetGroup_MultiDB(t *testing.T) {
	root := t.TempDir()
	writeSchema(t, root, cascadeMultiTypeSchema)

	if _, _, err := ops.Create(root, "drop_001.drop.theroot", "cascade.drop", map[string]any{
		"structural_type": "drop",
		"drop_number":     1,
		"title":           "root drop",
	}); err != nil {
		t.Fatalf("Create drop: %v", err)
	}
	if _, _, err := ops.Create(root, "drop_001.drop.planner_l2", "cascade.planner", map[string]any{
		"title": "L2 planner",
	}); err != nil {
		t.Fatalf("Create planner: %v", err)
	}

	got, err := ops.GetGroup(root, "drop_001.drop", nil, 0, true)
	if err != nil {
		t.Fatalf("GetGroup: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("GetGroup returned %d records, want 2 (drop + planner): %v",
			len(got), idsOf(got))
	}
	gotIDs := idsOf(got)
	want := []string{
		"drop_001.drop.planner_l2",
		"drop_001.drop.theroot",
	}
	for i, w := range want {
		if gotIDs[i] != w {
			t.Errorf("GetGroup[%d].ID = %q, want %q (full: %v)",
				i, gotIDs[i], w, gotIDs)
		}
	}
}

// TestOps_GetGroup_FieldsFilter — RESTORED per L3-C1 AMEND. The
// `fields []string` parameter is pass-through to per-child ops.Get;
// the returned ScopeRecord.Fields map carries exactly the requested
// subset. Pinning this surface in a test prevents future "should we
// also return all fields?" drift.
func TestOps_GetGroup_FieldsFilter(t *testing.T) {
	root := t.TempDir()
	writeSchema(t, root, cascadeDropSchema)

	createDrop(t, root, "drop_001.drop.alpha", 1)
	createDrop(t, root, "drop_001.drop.beta", 2)

	got, err := ops.GetGroup(root, "drop_001.drop", []string{"drop_number"}, 0, true)
	if err != nil {
		t.Fatalf("GetGroup: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("GetGroup returned %d records, want 2", len(got))
	}
	for _, rec := range got {
		if rec.Fields == nil {
			t.Errorf("record %q: Fields == nil; want filtered map with drop_number", rec.ID)
			continue
		}
		if _, has := rec.Fields["drop_number"]; !has {
			t.Errorf("record %q: Fields missing drop_number; got %v", rec.ID, rec.Fields)
		}
		if _, has := rec.Fields["structural_type"]; has {
			t.Errorf("record %q: Fields includes unrequested structural_type; got %v",
				rec.ID, rec.Fields)
		}
	}
}

// TestOps_GetGroup_LimitAll — RESTORED per L3-C1 AMEND. limit > 0 caps
// the canonical-sorted child slice; all=true overrides limit entirely.
// limit <= 0 with all=false also means "no cap" (mirrors GetScope's
// resolveLimit-less-than-zero precedent — GetGroup's contract is the
// simpler "0 means uncapped" since per-child Get is unbounded by
// nature).
func TestOps_GetGroup_LimitAll(t *testing.T) {
	root := t.TempDir()
	writeSchema(t, root, cascadeDropSchema)

	createDrop(t, root, "drop_001.drop.alpha", 1)
	createDrop(t, root, "drop_001.drop.beta", 2)
	createDrop(t, root, "drop_001.drop.gamma", 3)

	// limit=2, all=false → first 2 in canonical order.
	got, err := ops.GetGroup(root, "drop_001.drop", nil, 2, false)
	if err != nil {
		t.Fatalf("GetGroup(limit=2): %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("GetGroup(limit=2) = %d records, want 2", len(got))
	}
	want := []string{"drop_001.drop.alpha", "drop_001.drop.beta"}
	for i, w := range want {
		if got[i].ID != w {
			t.Errorf("limit cap [%d]: ID = %q, want %q", i, got[i].ID, w)
		}
	}

	// limit=2, all=true → limit ignored, all 3 returned.
	got, err = ops.GetGroup(root, "drop_001.drop", nil, 2, true)
	if err != nil {
		t.Fatalf("GetGroup(limit=2, all=true): %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("GetGroup(limit=2, all=true) = %d records, want 3 (all overrides limit)",
			len(got))
	}
}

// TestOps_GetGroup_IndexMissing — RESTORED per L3-C1 AMEND. A fresh
// project with no records (and therefore no `.ta/index.toml`) must
// surface ErrIndexMissing loudly rather than silently returning an
// empty slice. Mirrors the rest-of-ops "missing index is a recovery
// event, not a normal state" discipline (errors.go ErrIndexMissing).
func TestOps_GetGroup_IndexMissing(t *testing.T) {
	root := t.TempDir()
	writeSchema(t, root, cascadeDropSchema)
	// No Create calls → no index.toml on disk.

	_, err := ops.GetGroup(root, "drop_001.drop", nil, 0, true)
	if err == nil {
		t.Fatal("GetGroup with missing index: err = nil, want ErrIndexMissing")
	}
	if !errors.Is(err, ops.ErrIndexMissing) {
		t.Errorf("GetGroup error = %v, want ErrIndexMissing", err)
	}
	if !strings.Contains(err.Error(), "index missing") {
		t.Errorf("GetGroup error message = %q, want substring `index missing`", err.Error())
	}
}
