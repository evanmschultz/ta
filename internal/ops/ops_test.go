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
file = "plans.toml"
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
