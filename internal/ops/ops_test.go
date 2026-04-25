package ops_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/ops"
)

// limitAllSchema declares a single-instance TOML db with one type so
// the limit/all tests can seed N records cheaply via a flat file.
const limitAllSchema = `
[plans]
paths = ["plans.toml"]
format = "toml"
description = "Endpoint limit/all test fixture."

[plans.task]
description = "A unit of work."

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.status]
type = "string"
required = true
`

// seedNTasks writes `n` [plans.task.tNN] records to plans.toml so the
// endpoint-level cap tests see more-than-default data in scope. Returns
// the project root.
func seedNTasks(t *testing.T, n int) string {
	t.Helper()
	t.Cleanup(ops.ResetDefaultCacheForTest)
	ops.ResetDefaultCacheForTest()

	root := t.TempDir()
	taDir := filepath.Join(root, ".ta")
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		t.Fatalf("mkdir .ta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taDir, "schema.toml"), []byte(limitAllSchema), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	var body strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&body, "[plans.task.t%02d]\nid = \"T%02d\"\nstatus = \"todo\"\n\n", i, i)
	}
	if err := os.WriteFile(filepath.Join(root, "plans.toml"), []byte(body.String()), 0o644); err != nil {
		t.Fatalf("seed plans.toml: %v", err)
	}
	return root
}

// TestListSectionsDefaultLimit proves the endpoint substitutes the
// default cap (10) when the caller passes limit <= 0 && all == false.
// This is the behavior change that makes MCP's uncapped list_sections
// match the CLI contract (§6a.1 decoupling / §12.17.5 [A2.1]).
func TestListSectionsDefaultLimit(t *testing.T) {
	root := seedNTasks(t, 15)
	got, err := ops.ListSections(root, "", 0, false)
	if err != nil {
		t.Fatalf("ListSections: %v", err)
	}
	if len(got) != 10 {
		t.Errorf("default limit should cap at 10, got %d: %v", len(got), got)
	}
}

// TestListSectionsExplicitLimit proves a non-zero limit is honored.
func TestListSectionsExplicitLimit(t *testing.T) {
	root := seedNTasks(t, 15)
	got, err := ops.ListSections(root, "", 5, false)
	if err != nil {
		t.Fatalf("ListSections: %v", err)
	}
	if len(got) != 5 {
		t.Errorf("limit=5 should cap at 5, got %d: %v", len(got), got)
	}
}

// TestListSectionsAll proves all=true returns every record, ignoring
// the default and any explicit limit.
func TestListSectionsAll(t *testing.T) {
	root := seedNTasks(t, 15)
	got, err := ops.ListSections(root, "", 0, true)
	if err != nil {
		t.Fatalf("ListSections: %v", err)
	}
	if len(got) != 15 {
		t.Errorf("all=true should return every record, got %d: %v", len(got), got)
	}
}

// TestListSectionsAllBeatsLimit proves that if both all=true and a
// non-zero limit arrive at the endpoint, all wins — the adapter-level
// mutex is a UX guard; the endpoint is permissive so library callers
// see deterministic precedence.
func TestListSectionsAllBeatsLimit(t *testing.T) {
	root := seedNTasks(t, 12)
	got, err := ops.ListSections(root, "", 3, true)
	if err != nil {
		t.Fatalf("ListSections: %v", err)
	}
	if len(got) != 12 {
		t.Errorf("all=true must beat limit=3 at endpoint, got %d", len(got))
	}
}

// TestListSectionsEarlyExitWalkOrder proves the early-exit preserves
// source order: the first 3 records written are the first 3 returned.
// Combined with the cap-cross check this locks in the plan's "don't
// materialize then slice" requirement — at the file-boundary level the
// walk stops once the cap is met.
func TestListSectionsEarlyExitWalkOrder(t *testing.T) {
	root := seedNTasks(t, 15)
	got, err := ops.ListSections(root, "", 3, false)
	if err != nil {
		t.Fatalf("ListSections: %v", err)
	}
	want := []string{
		"plans.task.t01",
		"plans.task.t02",
		"plans.task.t03",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d]=%q, want %q", i, got[i], want[i])
		}
	}
}

// TestSearchDefaultLimit mirrors TestListSectionsDefaultLimit on the
// search endpoint. Ten hits out of a fifteen-hit scope. §12.17.5 [A2.2].
func TestSearchDefaultLimit(t *testing.T) {
	root := seedNTasks(t, 15)
	hits, err := ops.Search(root, "plans.task", map[string]any{"status": "todo"}, "", "", 0, false)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 10 {
		t.Errorf("default limit should cap at 10, got %d", len(hits))
	}
}

// TestSearchExplicitLimit proves a non-zero limit is honored.
func TestSearchExplicitLimit(t *testing.T) {
	root := seedNTasks(t, 15)
	hits, err := ops.Search(root, "plans.task", map[string]any{"status": "todo"}, "", "", 4, false)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 4 {
		t.Errorf("limit=4 should cap at 4, got %d", len(hits))
	}
}

// TestSearchAll proves all=true returns every hit.
func TestSearchAll(t *testing.T) {
	root := seedNTasks(t, 15)
	hits, err := ops.Search(root, "plans.task", map[string]any{"status": "todo"}, "", "", 0, true)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 15 {
		t.Errorf("all=true should return every hit, got %d", len(hits))
	}
}

// TestSearchAllBeatsLimit parity with the ListSections endpoint —
// all wins at the endpoint even when limit is also non-zero.
func TestSearchAllBeatsLimit(t *testing.T) {
	root := seedNTasks(t, 12)
	hits, err := ops.Search(root, "plans.task", map[string]any{"status": "todo"}, "", "", 3, true)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 12 {
		t.Errorf("all=true must beat limit=3, got %d", len(hits))
	}
}

// ---- §12.17.5 [B2] GetScope / IsScopeAddress ------------------------

// multiInstanceOpsSchema declares a glob-mount TOML db so the
// scope-vs-record-address cases exercise the multi-file branch under
// the Phase 9.2 file-relpath grammar (PLAN §12.17.9).
const multiInstanceOpsSchema = `
[plan_db]
paths = ["workflow/*/db"]
format = "toml"
description = "Multi-file planning db."

[plan_db.build_task]
description = "A build task."

[plan_db.build_task.fields.id]
type = "string"
required = true

[plan_db.build_task.fields.status]
type = "string"
required = true
`

// seedMultiInstancePlans stands up a dir-per-instance project with two
// drops carrying 3 and 2 build_tasks. Returns the project root.
func seedMultiInstancePlans(t *testing.T) string {
	t.Helper()
	t.Cleanup(ops.ResetDefaultCacheForTest)
	ops.ResetDefaultCacheForTest()

	root := t.TempDir()
	taDir := filepath.Join(root, ".ta")
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		t.Fatalf("mkdir .ta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taDir, "schema.toml"), []byte(multiInstanceOpsSchema), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	dropA := filepath.Join(root, "workflow", "drop_a")
	if err := os.MkdirAll(dropA, 0o755); err != nil {
		t.Fatalf("mkdir drop_a: %v", err)
	}
	dropB := filepath.Join(root, "workflow", "drop_b")
	if err := os.MkdirAll(dropB, 0o755); err != nil {
		t.Fatalf("mkdir drop_b: %v", err)
	}
	bodyA := "[build_task.task_1]\nid = \"A1\"\nstatus = \"todo\"\n\n" +
		"[build_task.task_2]\nid = \"A2\"\nstatus = \"doing\"\n\n" +
		"[build_task.task_3]\nid = \"A3\"\nstatus = \"done\"\n"
	if err := os.WriteFile(filepath.Join(dropA, "db.toml"), []byte(bodyA), 0o644); err != nil {
		t.Fatalf("seed drop_a: %v", err)
	}
	bodyB := "[build_task.task_1]\nid = \"B1\"\nstatus = \"todo\"\n\n" +
		"[build_task.task_2]\nid = \"B2\"\nstatus = \"todo\"\n"
	if err := os.WriteFile(filepath.Join(dropB, "db.toml"), []byte(bodyB), 0o644); err != nil {
		t.Fatalf("seed drop_b: %v", err)
	}
	return root
}

// TestIsScopeAddressSingleFile enumerates scope-vs-record cases under
// the Phase 9.2 grammar. <file-relpath> alone and <file-relpath>.<type>
// are scopes; <file-relpath>.<type>.<id> is a single record.
func TestIsScopeAddressSingleFile(t *testing.T) {
	root := seedNTasks(t, 1)
	cases := []struct {
		section string
		want    bool
	}{
		{"plans", true},           // <file-relpath>
		{"plans.task", true},      // <file-relpath>.<type>
		{"plans.task.t01", false}, // full record
		{"plans.task.deep.id", false},
	}
	for _, tc := range cases {
		got, err := ops.IsScopeAddress(root, tc.section)
		if err != nil {
			t.Fatalf("IsScopeAddress(%q): %v", tc.section, err)
		}
		if got != tc.want {
			t.Errorf("IsScopeAddress(%q) = %v, want %v", tc.section, got, tc.want)
		}
	}
}

// TestIsScopeAddressGlobMount enumerates scope-vs-record cases on a
// glob-mounted db (file-relpath has multi-segment shape `<drop>.db`).
func TestIsScopeAddressGlobMount(t *testing.T) {
	root := seedMultiInstancePlans(t)
	cases := []struct {
		section string
		want    bool
	}{
		{"drop_a.db", true},                    // <file-relpath>
		{"drop_a.db.build_task", true},         // <file-relpath>.<type>
		{"drop_a.db.build_task.task_1", false}, // full record
	}
	for _, tc := range cases {
		got, err := ops.IsScopeAddress(root, tc.section)
		if err != nil {
			t.Fatalf("IsScopeAddress(%q): %v", tc.section, err)
		}
		if got != tc.want {
			t.Errorf("IsScopeAddress(%q) = %v, want %v", tc.section, got, tc.want)
		}
	}
}

// TestIsScopeAddressUnknownDBErrors proves a typo in the file-relpath
// segment fails loudly rather than falling back to "well, it's a scope".
func TestIsScopeAddressUnknownDBErrors(t *testing.T) {
	root := seedNTasks(t, 1)
	if _, err := ops.IsScopeAddress(root, "nope"); err == nil {
		t.Fatal("expected unknown-db error, got nil")
	}
}

// TestIsScopeAddressEmptySectionErrors proves the empty-string guard
// fires before any I/O.
func TestIsScopeAddressEmptySectionErrors(t *testing.T) {
	root := seedNTasks(t, 1)
	if _, err := ops.IsScopeAddress(root, ""); err == nil {
		t.Fatal("expected empty-section error, got nil")
	}
}

// TestGetScopeDB returns every record under <db>.
func TestGetScopeDB(t *testing.T) {
	root := seedNTasks(t, 5)
	records, err := ops.GetScope(root, "plans", nil, 0, true)
	if err != nil {
		t.Fatalf("GetScope: %v", err)
	}
	if len(records) != 5 {
		t.Fatalf("GetScope(plans) len = %d, want 5", len(records))
	}
	want := []string{
		"plans.task.t01", "plans.task.t02", "plans.task.t03",
		"plans.task.t04", "plans.task.t05",
	}
	for i, w := range want {
		if records[i].Section != w {
			t.Errorf("records[%d].Section = %q, want %q", i, records[i].Section, w)
		}
	}
}

// TestGetScopeDBType returns every record of a given type across
// single-instance (db.type collapses to "every record of that type").
func TestGetScopeDBType(t *testing.T) {
	root := seedNTasks(t, 3)
	records, err := ops.GetScope(root, "plans.task", nil, 0, true)
	if err != nil {
		t.Fatalf("GetScope: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("GetScope(plans.task) len = %d, want 3", len(records))
	}
}

// TestGetScopeDBInstance returns every record in one file of a
// glob-mounted db.
func TestGetScopeDBInstance(t *testing.T) {
	root := seedMultiInstancePlans(t)
	records, err := ops.GetScope(root, "drop_a.db", nil, 0, true)
	if err != nil {
		t.Fatalf("GetScope: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("GetScope(drop_a.db) len = %d, want 3: %+v", len(records), records)
	}
	for _, r := range records {
		if !strings.HasPrefix(r.Section, "drop_a.db.") {
			t.Errorf("leaked record from another file: %q", r.Section)
		}
	}
}

// TestGetScopeDBInstanceType returns every record in one instance-type
// pair.
func TestGetScopeDBInstanceType(t *testing.T) {
	root := seedMultiInstancePlans(t)
	records, err := ops.GetScope(root, "drop_b.db.build_task", nil, 0, true)
	if err != nil {
		t.Fatalf("GetScope: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("GetScope(drop_b.db.build_task) len = %d, want 2", len(records))
	}
}

// TestGetScopeDefaultLimit proves the endpoint default-10 applies when
// the caller passes limit <= 0 && all == false (§6a.1 / §12.17.5 [B2]).
func TestGetScopeDefaultLimit(t *testing.T) {
	root := seedNTasks(t, 15)
	records, err := ops.GetScope(root, "plans.task", nil, 0, false)
	if err != nil {
		t.Fatalf("GetScope: %v", err)
	}
	if len(records) != 10 {
		t.Errorf("default limit should cap at 10, got %d", len(records))
	}
}

// TestGetScopeExplicitLimit proves a non-zero limit caps at N.
func TestGetScopeExplicitLimit(t *testing.T) {
	root := seedNTasks(t, 15)
	records, err := ops.GetScope(root, "plans.task", nil, 4, false)
	if err != nil {
		t.Fatalf("GetScope: %v", err)
	}
	if len(records) != 4 {
		t.Errorf("limit=4 should cap at 4, got %d", len(records))
	}
}

// TestGetScopeAll proves all=true returns every record.
func TestGetScopeAll(t *testing.T) {
	root := seedNTasks(t, 15)
	records, err := ops.GetScope(root, "plans.task", nil, 0, true)
	if err != nil {
		t.Fatalf("GetScope: %v", err)
	}
	if len(records) != 15 {
		t.Errorf("all=true should return every record, got %d", len(records))
	}
}

// TestGetScopeAllBeatsLimit parity with the other scope endpoints —
// all wins at the endpoint even when limit is also non-zero.
func TestGetScopeAllBeatsLimit(t *testing.T) {
	root := seedNTasks(t, 12)
	records, err := ops.GetScope(root, "plans.task", nil, 3, true)
	if err != nil {
		t.Fatalf("GetScope: %v", err)
	}
	if len(records) != 12 {
		t.Errorf("all=true must beat limit=3, got %d", len(records))
	}
}

// TestGetScopeFieldsFilter proves the fields parameter narrows the
// returned Fields map. Names not declared on the type are silently
// omitted (mirrors search's optional-field contract).
func TestGetScopeFieldsFilter(t *testing.T) {
	root := seedNTasks(t, 2)
	records, err := ops.GetScope(root, "plans.task", []string{"status"}, 0, true)
	if err != nil {
		t.Fatalf("GetScope: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("len = %d, want 2", len(records))
	}
	for _, r := range records {
		if _, ok := r.Fields["status"]; !ok {
			t.Errorf("record %q missing status: %+v", r.Section, r.Fields)
		}
		if _, ok := r.Fields["id"]; ok {
			t.Errorf("record %q should not carry id under fields=[status]: %+v", r.Section, r.Fields)
		}
	}
}

// TestGetSingleRecordUnchanged locks in the §12.17.5 [B2]
// backwards-compat guarantee: ops.Get on a fully-qualified address
// returns the pre-B2 GetResult shape byte-for-byte (raw bytes mode).
// Test is framed at the endpoint so both the CLI and MCP adapters
// inherit the byte-equivalence proof.
func TestGetSingleRecordUnchanged(t *testing.T) {
	root := seedNTasks(t, 1)
	res, err := ops.Get(root, "plans.task.t01", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	got := string(res.Bytes)
	// seedNTasks terminates every record with "\n\n" so the trailing
	// blank line is part of the record's byte range.
	want := "[plans.task.t01]\nid = \"T01\"\nstatus = \"todo\"\n\n"
	if got != want {
		t.Errorf("single-record bytes drifted:\ngot  %q\nwant %q", got, want)
	}
	if res.Fields != nil {
		t.Errorf("Fields should be nil when fields=nil: %+v", res.Fields)
	}
}
