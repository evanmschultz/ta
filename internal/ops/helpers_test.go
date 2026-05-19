package ops_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/index"
	"github.com/evanmschultz/ta/internal/ops"
)

// F38d-2.14c per-Entry DBName disambiguation tests. These exercise the
// resolveIDWithIndexHint fast path (DBName wins over alphabetical scan)
// + the two falsification carve-outs (CE4 Load coercion already
// exercised in internal/index tests; CE5 graceful degradation when the
// indexed db has been dropped from the registry).
//
// The fixture re-uses withAmbiguousIDSchema from ops_test.go (both dbs
// `claude_agents` glob + `plans` single-file accept the same id
// namespace; alphabetical scan picks the wrong db pre-fix).

// TestResolveIDWithIndexHint_DBNameWinsOverAlphabetical pins the fast
// path: when the index entry carries DBName="plans", the resolver
// must read from the plans db's file even though `claude_agents`
// sorts alphabetically earlier.
func TestResolveIDWithIndexHint_DBNameWinsOverAlphabetical(t *testing.T) {
	root := withAmbiguousIDSchema(t)
	// Create through the normal path so writeIndexEntry stamps DBName.
	if _, _, err := ops.Create(root, "plans.dogfood-d2", "plans.plan", map[string]any{
		"title": "F38d-2.14c DBName disambiguation",
		"state": "in_progress",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Verify the index entry carries DBName="plans".
	idx, err := index.Load(root)
	if err != nil {
		t.Fatalf("index.Load: %v", err)
	}
	entry, ok := idx.Get("plans.dogfood-d2")
	if !ok {
		t.Fatal("missing index entry")
	}
	if entry.DBName != "plans" {
		t.Errorf("entry.DBName = %q, want %q", entry.DBName, "plans")
	}

	// The fast path resolves to plans/.ta/cascade/plans.toml.
	res, err := ops.Get(root, "plans.dogfood-d2", "", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	wantPath := filepath.Join(root, ".ta", "cascade", "plans.toml")
	if res.FilePath != wantPath {
		t.Errorf("FilePath = %q, want %q (DBName fast path should pick plans)",
			res.FilePath, wantPath)
	}
	if !strings.Contains(string(res.Bytes), "[plans.dogfood-d2]") {
		t.Errorf("bytes missing [plans.dogfood-d2] bracket; got:\n%s", res.Bytes)
	}
}

// TestResolveIDWithIndexHint_LegacyFallback covers the v=2 read shim
// path: an index entry written without DBName (legacy) still resolves
// via the type-based alphabetical scan. Pre-fix the unconstrained
// ResolveID would pick claude_agents (alphabetically earlier) and
// fail with file-not-found; post-fix the indexed bare type "plan"
// constrains resolution to the plans db.
func TestResolveIDWithIndexHint_LegacyFallback(t *testing.T) {
	root := withAmbiguousIDSchema(t)

	// Materialize the record on disk WITHOUT going through Create so we
	// can hand-author a legacy-shape index file (no per-entry db_name).
	planFile := filepath.Join(root, ".ta", "cascade", "plans.toml")
	if err := os.MkdirAll(filepath.Dir(planFile), 0o755); err != nil {
		t.Fatalf("mkdir plans dir: %v", err)
	}
	planBody := "[plans.legacy-d2]\ntitle = \"Legacy v=2 entry\"\nstate = \"todo\"\n"
	if err := os.WriteFile(planFile, []byte(planBody), 0o644); err != nil {
		t.Fatalf("write plans.toml: %v", err)
	}

	// Hand-author a legacy v=2 index. No db_name key — Load shim
	// accepts this shape and coerces FormatVersion to current. The
	// in-memory Entry.DBName="" forces resolveIDWithIndexHint to fall
	// through to the type-scan branch.
	idxFile := filepath.Join(root, ".ta", "index.toml")
	legacyIdx := "format_version = 2\n\n[plans.legacy-d2]\ntype = \"plan\"\ncreated = 2026-04-01T10:00:00Z\nupdated = 2026-04-01T10:00:00Z\n"
	if err := os.WriteFile(idxFile, []byte(legacyIdx), 0o644); err != nil {
		t.Fatalf("write legacy index: %v", err)
	}

	res, err := ops.Get(root, "plans.legacy-d2", "", nil)
	if err != nil {
		t.Fatalf("Get (legacy DBName=''): %v", err)
	}
	wantPath := planFile
	if res.FilePath != wantPath {
		t.Errorf("FilePath = %q, want %q (legacy scan should still pick plans)",
			res.FilePath, wantPath)
	}
	if !strings.Contains(string(res.Bytes), "[plans.legacy-d2]") {
		t.Errorf("bytes missing [plans.legacy-d2] bracket; got:\n%s", res.Bytes)
	}
}

// TestResolveIDWithIndexHint_FallsBackWhenIndexedDBNoLongerExists pins
// the CE5 graceful-degradation carve-out: an index entry whose DBName
// references a db that has been removed from the registry must NOT
// fail the lookup; it falls through to the type-based alphabetical
// scan (which still finds the record via the surviving plans db).
//
// Scenario: write an entry under DBName="planset" (a name that was
// never declared / has been retired); the registry only knows
// `claude_agents` + `plans`. The scan finds `plans` via the indexed
// type "plan" and the record resolves cleanly.
func TestResolveIDWithIndexHint_FallsBackWhenIndexedDBNoLongerExists(t *testing.T) {
	root := withAmbiguousIDSchema(t)

	planFile := filepath.Join(root, ".ta", "cascade", "plans.toml")
	if err := os.MkdirAll(filepath.Dir(planFile), 0o755); err != nil {
		t.Fatalf("mkdir plans dir: %v", err)
	}
	planBody := "[plans.ce5-d2]\ntitle = \"Dropped-DB fallback\"\nstate = \"todo\"\n"
	if err := os.WriteFile(planFile, []byte(planBody), 0o644); err != nil {
		t.Fatalf("write plans.toml: %v", err)
	}

	// Hand-author a v=3 index whose entry references a dropped db.
	idxFile := filepath.Join(root, ".ta", "index.toml")
	body := "format_version = 3\n\n[plans.ce5-d2]\ntype = \"plan\"\ndb_name = \"planset\"\ncreated = 2026-04-01T10:00:00Z\nupdated = 2026-04-01T10:00:00Z\n"
	if err := os.WriteFile(idxFile, []byte(body), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	res, err := ops.Get(root, "plans.ce5-d2", "", nil)
	if err != nil {
		t.Fatalf("Get (dropped DBName=planset): %v", err)
	}
	if res.FilePath != planFile {
		t.Errorf("FilePath = %q, want %q (CE5 fallback should still pick plans)",
			res.FilePath, planFile)
	}
	if !strings.Contains(string(res.Bytes), "[plans.ce5-d2]") {
		t.Errorf("bytes missing [plans.ce5-d2] bracket; got:\n%s", res.Bytes)
	}
}
