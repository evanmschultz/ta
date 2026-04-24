package ops_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/ops"
)

// planDBSchema mirrors the ta-v2 dogfood schema shape at the minimum
// fidelity required to validate records of the form
// `plan_db.<instance>.build_task.<id>` and
// `plan_db.<instance>.qa_task.<id>`. Kept self-contained per the §12.10
// dogfood-test rule: "Fixtures should NOT depend on the actual project
// path (use a temp dir + seeded records)."
const planDBSchema = `
[plan_db]
directory = "workflow"
format = "toml"
description = "dogfood planning db"

[plan_db.build_task]
description = "One unit of implementation work."

[plan_db.build_task.fields.id]
type = "string"
required = true

[plan_db.build_task.fields.status]
type = "string"
required = true
enum = ["todo", "doing", "review", "blocked", "done"]

[plan_db.build_task.fields.title]
type = "string"
required = true

[plan_db.build_task.fields.owner]
type = "string"
required = true

[plan_db.build_task.fields.body]
type = "string"
format = "markdown"

[plan_db.qa_task]
description = "QA twin of a build_task."

[plan_db.qa_task.fields.id]
type = "string"
required = true

[plan_db.qa_task.fields.parent_build_task]
type = "string"
required = true

[plan_db.qa_task.fields.kind]
type = "string"
required = true
enum = ["proof", "falsification"]

[plan_db.qa_task.fields.status]
type = "string"
required = true
enum = ["todo", "doing", "passed", "failed"]

[plan_db.qa_task.fields.body]
type = "string"
format = "markdown"
`

// seedDogfoodFixture builds a temp-dir project with a plan_db schema and
// a curated mini-set of build_task + qa_task records that exercise the
// same creation, round-trip, and search paths the real mage dogfood
// target will drive. The fixture is intentionally smaller than the
// 26-record production payload — the point is to prove the shape is
// sound without coupling the unit test to the exact drop lineage.
func seedDogfoodFixture(t *testing.T) string {
	t.Helper()
	t.Cleanup(ops.ResetDefaultCacheForTest)
	ops.ResetDefaultCacheForTest()

	root := t.TempDir()
	taDir := filepath.Join(root, ".ta")
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		t.Fatalf("mkdir .ta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taDir, "schema.toml"), []byte(planDBSchema), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}

	records := []struct {
		section string
		data    map[string]any
	}{
		{
			section: "plan_db.ta-v2.build_task.task_12_1",
			data: map[string]any{
				"id":     "task_12_1",
				"status": "done",
				"title":  "Backend interface extraction",
				"owner":  "evanmschultz",
				"body":   "Sketch summary citing commit 1e636d9.",
			},
		},
		{
			section: "plan_db.ta-v2.build_task.task_12_9",
			data: map[string]any{
				"id":     "task_12_9",
				"status": "doing",
				"title":  "MCP caching",
				"owner":  "evanmschultz",
				"body":   "Sketch summary: schema cascade cache, mtime invalidation, startup pre-warm.",
			},
		},
		{
			section: "plan_db.ta-v2.qa_task.qa_12_1_proof",
			data: map[string]any{
				"id":                "qa_12_1_proof",
				"parent_build_task": "task_12_1",
				"kind":              "proof",
				"status":            "passed",
				"body":              "Sketch Proof twin for §12.1.",
			},
		},
		{
			section: "plan_db.ta-v2.qa_task.qa_12_1_falsification",
			data: map[string]any{
				"id":                "qa_12_1_falsification",
				"parent_build_task": "task_12_1",
				"kind":              "falsification",
				"status":            "passed",
				"body":              "Sketch Falsification twin for §12.1 post-Option-A.",
			},
		},
	}
	for _, r := range records {
		if _, _, err := ops.Create(root, r.section, "", r.data); err != nil {
			t.Fatalf("Create %s: %v", r.section, err)
		}
	}
	return root
}

// TestDogfoodGetRoundtripsBuildTask proves a record written via
// ops.Create comes back identically through ops.Get on the
// dogfood plan_db shape. This is the §12.10 verification contract:
// `ta get <path> plan_db.ta-v2.build_task.task_12_1` must surface the
// record's bytes.
func TestDogfoodGetRoundtripsBuildTask(t *testing.T) {
	root := seedDogfoodFixture(t)
	res, err := ops.Get(root, "plan_db.ta-v2.build_task.task_12_1", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	got := string(res.Bytes)
	for _, want := range []string{
		"[build_task.task_12_1]",
		`id = "task_12_1"`,
		`status = "done"`,
		`title = "Backend interface extraction"`,
		`owner = "evanmschultz"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Get output missing %q\n--- got ---\n%s", want, got)
		}
	}
	if !strings.HasSuffix(res.FilePath, filepath.Join("workflow", "ta-v2", "db.toml")) {
		t.Errorf("unexpected backing file: %s", res.FilePath)
	}
}

// TestDogfoodSearchFindsDoneBuildTasks proves the §12.10 verification
// probe `ta search --scope plan_db.ta-v2 --match '{"status":"done"}'`
// returns only the done build_tasks, not the in-flight ones or the
// QA twins (which have a different `status` enum).
func TestDogfoodSearchFindsDoneBuildTasks(t *testing.T) {
	root := seedDogfoodFixture(t)
	hits, err := ops.Search(root, "plan_db.ta-v2.build_task", map[string]any{"status": "done"}, "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 done build_task, got %d: %+v", len(hits), hits)
	}
	if got, want := hits[0].Section, "plan_db.ta-v2.build_task.task_12_1"; got != want {
		t.Errorf("section = %q; want %q", got, want)
	}
}

// TestDogfoodSearchFindsFalsificationTwins proves the §12.10 probe
// `ta search --scope plan_db.ta-v2.qa_task --match '{"kind":"falsification"}'`
// returns the falsification twins without bleeding over into proof twins
// or build_task records.
func TestDogfoodSearchFindsFalsificationTwins(t *testing.T) {
	root := seedDogfoodFixture(t)
	hits, err := ops.Search(root, "plan_db.ta-v2.qa_task", map[string]any{"kind": "falsification"}, "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 falsification twin, got %d: %+v", len(hits), hits)
	}
	if got, want := hits[0].Section, "plan_db.ta-v2.qa_task.qa_12_1_falsification"; got != want {
		t.Errorf("section = %q; want %q", got, want)
	}
}

// TestDogfoodCreateIsIdempotentPerRecord proves the ErrRecordExists
// guard protects the mage dogfood target from double-writes on re-run
// when the existence check at the file level misses a race. Creating
// the same record twice must fail loudly with ErrRecordExists, never
// silently append or corrupt the db file.
func TestDogfoodCreateIsIdempotentPerRecord(t *testing.T) {
	root := seedDogfoodFixture(t)
	_, _, err := ops.Create(root, "plan_db.ta-v2.build_task.task_12_1", "", map[string]any{
		"id":     "task_12_1",
		"status": "done",
		"title":  "Backend interface extraction",
		"owner":  "evanmschultz",
	})
	if err == nil {
		t.Fatal("re-create: want error, got nil")
	}
	// Use errors.Is via the exported sentinel if callers branch on it.
	if !strings.Contains(err.Error(), "record already exists") {
		t.Errorf("want ErrRecordExists, got %v", err)
	}
}
