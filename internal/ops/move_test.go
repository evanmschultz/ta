package ops_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/index"
	"github.com/evanmschultz/ta/internal/ops"
)

// withMoveCrossDBSchema sets up two TOML dbs both declaring a `task`
// type so cross-db moves with type-defaulting work. Used by the
// cross-db tests below.
func withMoveCrossDBSchema(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeSchema(t, root, `
[plans]
paths = ["plans.toml"]

[plans.task]
description = "A plan task"

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.title]
type = "string"
required = true

[plans.task.fields.status]
type = "string"
required = true
enum = ["todo", "doing", "done"]

[cascade]
paths = ["workflow/*/db.toml"]

[cascade.drop]
description = "A cascade drop"

[cascade.drop.fields.id]
type = "string"
required = true

[cascade.drop.fields.title]
type = "string"
required = true

[cascade.drop.fields.status]
type = "string"
required = true
enum = ["todo", "doing", "done"]

[cascade.task]
description = "A cascade task"

[cascade.task.fields.id]
type = "string"
required = true

[cascade.task.fields.title]
type = "string"
required = true

[cascade.task.fields.status]
type = "string"
required = true
enum = ["todo", "doing", "done"]
`)
	return root
}

// withMoveDisjointTypeSchema declares two dbs whose type names do NOT
// overlap, so cross-db moves without --type fall into the
// "specify --type" guidance branch.
func withMoveDisjointTypeSchema(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeSchema(t, root, `
[plans]
paths = ["plans.toml"]

[plans.task]
description = "A plan task"

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.title]
type = "string"
required = true

[other]
paths = ["other.toml"]

[other.note]
description = "A note"

[other.note.fields.id]
type = "string"
required = true

[other.note.fields.title]
type = "string"
required = true
`)
	return root
}

// withMoveMixedFormatSchema declares one TOML db and one MD section-mode
// db whose mounts have non-overlapping segment grammars so a 2-segment
// id resolves only to the TOML db. Format-mismatch tests use this
// shape to assert loud-fail without crossing the resolver-fallthrough
// minefield where two dbs accept the same id.
func withMoveMixedFormatSchema(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeSchema(t, root, `
[plans]
paths = ["plans.toml"]

[plans.task]
description = "A plan task"

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.body]
type = "string"

[notes]
paths = ["docs/notes.md"]

[notes.note]
description = "A note"
heading = 1

[notes.note.fields.body]
type = "string"
`)
	return root
}

// withMoveModeMismatchSchema pairs an md section-mode db with an md
// file-as-record db whose mount shapes do NOT overlap, so a 2-seg id
// resolves only to the section-mode db and a 1-seg id resolves only to
// the file-as-record db. Forces the file-record vs section-mode loud-
// fail to trigger without also tripping the format gate.
func withMoveModeMismatchSchema(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeSchema(t, root, `
[notes]
paths = ["notes.md"]

[notes.note]
description = "A section-mode note"
heading = 1

[notes.note.fields.body]
type = "string"

[agents]
paths = ["agents/*.md"]

[agents.agent]
description = "A file-as-record agent"
record_per = "file"
body_field = "prompt"

[agents.agent.fields.name]
type = "string"
required = true

[agents.agent.fields.prompt]
type = "string"
required = true
format = "markdown"
`)
	return root
}

// TestMove_WithinSameDB_RecordLevel renames `plans.foo` → `plans.bar`
// within plans.toml. Source spliced out, destination present, byte-
// identical body fields preserved.
func TestMove_WithinSameDB_RecordLevel(t *testing.T) {
	root := withSingleFileSchema(t)
	if _, _, err := ops.Create(root, "plans.foo", "plans.task", map[string]any{
		"id": "foo", "title": "first", "status": "todo",
	}); err != nil {
		t.Fatalf("seed Create: %v", err)
	}
	res, err := ops.Move(root, "plans.foo", "plans.bar", "", ops.MoveOptions{})
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	if res.Action != "move" {
		t.Errorf("Action = %q, want move", res.Action)
	}
	body, err := os.ReadFile(filepath.Join(root, "plans.toml"))
	if err != nil {
		t.Fatalf("read plans.toml: %v", err)
	}
	if strings.Contains(string(body), "[plans.foo]") {
		t.Errorf("plans.toml still carries [plans.foo]; body:\n%s", body)
	}
	if !strings.Contains(string(body), "[plans.bar]") {
		t.Errorf("plans.toml missing [plans.bar]; body:\n%s", body)
	}
	got, _, err := ops.GetAllFields(root, "plans.bar", "")
	if err != nil {
		t.Fatalf("GetAllFields dst: %v", err)
	}
	if got.Fields["title"] != "first" {
		t.Errorf("title = %v, want first (preserved across move)", got.Fields["title"])
	}
}

// TestMove_AcrossDBs_PlansToCascade moves a plans task into a cascade
// glob-rooted db. Validates dst against the new type, src spliced,
// index reflects swap.
func TestMove_AcrossDBs_PlansToCascade(t *testing.T) {
	root := withMoveCrossDBSchema(t)
	if _, _, err := ops.Create(root, "plans.task-007", "plans.task", map[string]any{
		"id": "task-007", "title": "moveable", "status": "todo",
	}); err != nil {
		t.Fatalf("seed Create: %v", err)
	}
	if _, err := ops.Move(root, "plans.task-007", "drop_3.db.task-007", "cascade.task", ops.MoveOptions{}); err != nil {
		t.Fatalf("Move: %v", err)
	}
	plansBody, err := os.ReadFile(filepath.Join(root, "plans.toml"))
	if err != nil {
		t.Fatalf("read plans.toml: %v", err)
	}
	if strings.Contains(string(plansBody), "[plans.task-007]") {
		t.Errorf("src still in plans.toml after move; body:\n%s", plansBody)
	}
	cascadeBody, err := os.ReadFile(filepath.Join(root, "workflow", "drop_3", "db.toml"))
	if err != nil {
		t.Fatalf("read cascade db: %v", err)
	}
	// Multi-file (glob-mount) TOML db: bracket on disk is the
	// bracket-key alone (not the canonical id). Per F10's bracket-form
	// rules, single-file dbs emit `[<file-relpath>.<bracket-key>]`
	// while multi-file dbs emit just `[<bracket-key>]`.
	if !strings.Contains(string(cascadeBody), "[task-007]") {
		t.Errorf("cascade db missing dst bracket; body:\n%s", cascadeBody)
	}
	idx, _ := index.Load(root)
	if _, ok := idx.Get("plans.task-007"); ok {
		t.Errorf("index still carries src after move")
	}
	if entry, ok := idx.Get("drop_3.db.task-007"); !ok {
		t.Errorf("index missing dst after move")
	} else if entry.Type != "task" {
		t.Errorf("index dst type = %q, want task", entry.Type)
	}
}

// TestMove_Copy_PreservesSource — Copy=true leaves src on disk; both
// records readable via Get.
func TestMove_Copy_PreservesSource(t *testing.T) {
	root := withSingleFileSchema(t)
	if _, _, err := ops.Create(root, "plans.foo", "plans.task", map[string]any{
		"id": "foo", "title": "first", "status": "todo",
	}); err != nil {
		t.Fatalf("seed Create: %v", err)
	}
	res, err := ops.Move(root, "plans.foo", "plans.bar", "", ops.MoveOptions{Copy: true})
	if err != nil {
		t.Fatalf("Move (copy): %v", err)
	}
	if res.Action != "copy" {
		t.Errorf("Action = %q, want copy", res.Action)
	}
	if _, _, err := ops.GetAllFields(root, "plans.foo", ""); err != nil {
		t.Errorf("src no longer readable after copy: %v", err)
	}
	if _, _, err := ops.GetAllFields(root, "plans.bar", ""); err != nil {
		t.Errorf("dst not readable after copy: %v", err)
	}
}

// TestMove_AtomicOnDstValidationFailure — a dst-side validation
// failure leaves src bytes untouched, no dst file mutation, no index
// change.
func TestMove_AtomicOnDstValidationFailure(t *testing.T) {
	root := withSingleFileSchema(t)
	if _, _, err := ops.Create(root, "plans.foo", "plans.task", map[string]any{
		"id": "foo", "title": "first", "status": "todo",
	}); err != nil {
		t.Fatalf("seed Create: %v", err)
	}
	// Tamper plans.toml so the src record carries a status outside the
	// declared enum. The dst-side validate will reject and the move
	// must abort cleanly.
	tamperedID := "plans.foo"
	plansPath := filepath.Join(root, "plans.toml")
	body, err := os.ReadFile(plansPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	mutated := strings.Replace(string(body), `status = "todo"`, `status = "rogue"`, 1)
	if mutated == string(body) {
		t.Fatalf("expected to mutate status; body:\n%s", body)
	}
	if err := os.WriteFile(plansPath, []byte(mutated), 0o644); err != nil {
		t.Fatalf("write tampered: %v", err)
	}

	before, err := os.ReadFile(plansPath)
	if err != nil {
		t.Fatalf("read pre-move: %v", err)
	}
	idxBefore, _ := index.Load(root)
	srcEntryBefore, _ := idxBefore.Get(tamperedID)

	_, err = ops.Move(root, tamperedID, "plans.bar", "", ops.MoveOptions{})
	if err == nil {
		t.Fatal("expected validation error on dst, got nil")
	}

	after, err := os.ReadFile(plansPath)
	if err != nil {
		t.Fatalf("read post-move: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("plans.toml mutated despite validation failure;\nbefore:\n%s\nafter:\n%s", before, after)
	}
	idxAfter, _ := index.Load(root)
	if _, ok := idxAfter.Get("plans.bar"); ok {
		t.Errorf("index gained dst entry despite validation failure")
	}
	if entry, ok := idxAfter.Get(tamperedID); !ok || entry.Type != srcEntryBefore.Type {
		t.Errorf("index src entry mutated/lost despite validation failure")
	}
}

// TestMove_AtomicOnSrcSpliceFailure_LeavesDuplicate_NoDataLoss —
// induce a splice-write failure by chmod'ing the SRC FILE'S directory
// read-only AFTER pre-creating the dst directory tree. The dst write
// lands successfully (its subdir is writeable) but the src cleanup
// rewrite of plans.toml fails because its parent dir cannot host the
// fsatomic temp file. Verifies ErrMovePartialWrite with both ids +
// recovery hint, dst on disk, src still present.
func TestMove_AtomicOnSrcSpliceFailure_LeavesDuplicate_NoDataLoss(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; chmod-based read-only does not block writes")
	}
	root := withMoveCrossDBSchema(t)
	if _, _, err := ops.Create(root, "plans.foo", "plans.task", map[string]any{
		"id": "foo", "title": "first", "status": "todo",
	}); err != nil {
		t.Fatalf("seed plans Create: %v", err)
	}
	// Pre-create the dst directory so the move's mkdir is a no-op and
	// the dst write can land before the src cleanup runs.
	dstDir := filepath.Join(root, "workflow", "drop_1")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatalf("mkdir dst dir: %v", err)
	}
	// Drop the .ta dir's write bit so index.Save fails... no, we want
	// the SRC SPLICE to fail. The src splice rewrites plans.toml in
	// `root`. fsatomic.Write creates a same-dir temp file. Drop write
	// perms on `root` itself; the dst dir (`workflow/drop_1`) is a
	// subdir that retains its own perms.
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatalf("chmod ro: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })

	_, err := ops.Move(root, "plans.foo", "drop_1.db.foo", "cascade.task", ops.MoveOptions{})

	// Restore perms before ANY assertion that reads disk so we can
	// observe state cleanly.
	if cerr := os.Chmod(root, 0o755); cerr != nil {
		t.Fatalf("restore chmod: %v", cerr)
	}

	if err == nil {
		t.Fatal("expected ErrMovePartialWrite, got nil")
	}
	if !errors.Is(err, ops.ErrMovePartialWrite) {
		t.Errorf("err = %v, want ErrMovePartialWrite", err)
	}
	// Recovery hint must include both ids + a `ta delete` snippet so
	// the operator can reconcile manually. Hint must also point at
	// `ta index rebuild` because the dst is on disk but unindexed
	// (index update happens AFTER src cleanup; partial-write returns
	// before that step).
	msg := err.Error()
	for _, want := range []string{"plans.foo", "drop_1.db.foo", "ta delete", "ta index rebuild"} {
		if !strings.Contains(msg, want) {
			t.Errorf("partial-write error missing %q in message: %s", want, msg)
		}
	}
	// Dst MUST be on disk (no auto-rollback).
	cascadeBody, rerr := os.ReadFile(filepath.Join(root, "workflow", "drop_1", "db.toml"))
	if rerr != nil {
		t.Fatalf("read cascade db: %v", rerr)
	}
	if !strings.Contains(string(cascadeBody), "[task-007]") && !strings.Contains(string(cascadeBody), "[foo]") {
		t.Errorf("dst missing on disk despite no-rollback rule; body:\n%s", cascadeBody)
	}
	// Src MUST still exist (cleanup failed).
	plansBody, rerr := os.ReadFile(filepath.Join(root, "plans.toml"))
	if rerr != nil {
		t.Fatalf("read plans.toml: %v", rerr)
	}
	if !strings.Contains(string(plansBody), "[plans.foo]") {
		t.Errorf("src missing despite cleanup-failure rule; body:\n%s", plansBody)
	}
}

// TestMove_FileRecord_FrontmatterPreserved — file-as-record agent
// moved within same db; YAML frontmatter survives.
func TestMove_FileRecord_FrontmatterPreserved(t *testing.T) {
	root := withFileAsRecordSchema(t)
	if _, _, err := ops.Create(root, "ta.writer", "agents.agent", map[string]any{
		"name":   "writer",
		"tools":  []any{"grep", "edit"},
		"prompt": "you are a writer.",
	}); err != nil {
		t.Fatalf("seed Create: %v", err)
	}
	if _, err := ops.Move(root, "ta.writer", "ta.editor", "", ops.MoveOptions{}); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "agents", "ta", "writer.md")); !os.IsNotExist(statErr) {
		t.Errorf("src writer.md still exists after move: %v", statErr)
	}
	dstBody, err := os.ReadFile(filepath.Join(root, "agents", "ta", "editor.md"))
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	body := string(dstBody)
	if !strings.Contains(body, "---") {
		t.Errorf("dst missing frontmatter delimiters; body:\n%s", body)
	}
	if !strings.Contains(body, "you are a writer.") {
		t.Errorf("dst missing prompt body; body:\n%s", body)
	}
}

// TestMove_SectionRecord_BodyPreserved — bracket-record (TOML) moved;
// section content preserved.
func TestMove_SectionRecord_BodyPreserved(t *testing.T) {
	root := withSingleFileSchema(t)
	if _, _, err := ops.Create(root, "plans.foo", "plans.task", map[string]any{
		"id": "foo", "title": "preserved", "status": "doing",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := ops.Move(root, "plans.foo", "plans.bar", "", ops.MoveOptions{}); err != nil {
		t.Fatalf("Move: %v", err)
	}
	got, _, err := ops.GetAllFields(root, "plans.bar", "")
	if err != nil {
		t.Fatalf("GetAllFields: %v", err)
	}
	if got.Fields["title"] != "preserved" {
		t.Errorf("title = %v, want preserved", got.Fields["title"])
	}
	if got.Fields["status"] != "doing" {
		t.Errorf("status = %v, want doing", got.Fields["status"])
	}
}

// TestMove_ModeMismatch_FileRecordToSection_Errors — file-as-record
// src + section-mode dst → ErrMoveModeMismatch.
func TestMove_ModeMismatch_FileRecordToSection_Errors(t *testing.T) {
	root := withMoveModeMismatchSchema(t)
	if _, _, err := ops.Create(root, "writer", "agents.agent", map[string]any{
		"name":   "writer",
		"prompt": "body text",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := ops.Move(root, "writer", "notes.heading-1", "notes.note", ops.MoveOptions{})
	if err == nil {
		t.Fatal("expected ErrMoveModeMismatch, got nil")
	}
	if !errors.Is(err, ops.ErrMoveModeMismatch) {
		t.Errorf("err = %v, want ErrMoveModeMismatch", err)
	}
}

// TestMove_ModeMismatch_SectionToFileRecord_Errors — mirror.
func TestMove_ModeMismatch_SectionToFileRecord_Errors(t *testing.T) {
	root := withMoveModeMismatchSchema(t)
	// Seed a section-mode note record at notes.md. The MD scanner
	// anchors at the declared `note` heading; the bracket key matches
	// the heading slug.
	if err := os.WriteFile(filepath.Join(root, "notes.md"),
		[]byte("# heading-1\n\nbody text\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ops.Move(root, "notes.heading-1", "editor", "agents.agent", ops.MoveOptions{})
	if err == nil {
		t.Fatal("expected ErrMoveModeMismatch, got nil")
	}
	if !errors.Is(err, ops.ErrMoveModeMismatch) {
		t.Errorf("err = %v, want ErrMoveModeMismatch", err)
	}
}

// TestMove_FormatMismatch_TOMLToMD_Errors — moving across Format
// boundary errors loud.
func TestMove_FormatMismatch_TOMLToMD_Errors(t *testing.T) {
	root := withMoveMixedFormatSchema(t)
	if _, _, err := ops.Create(root, "plans.foo", "plans.task", map[string]any{
		"id": "foo", "body": "x",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := ops.Move(root, "plans.foo", "notes.heading-1", "notes.note", ops.MoveOptions{})
	if err == nil {
		t.Fatal("expected ErrMoveFormatMismatch, got nil")
	}
	if !errors.Is(err, ops.ErrMoveFormatMismatch) {
		t.Errorf("err = %v, want ErrMoveFormatMismatch", err)
	}
}

// TestMove_DestExists_NoForce_Errors — ErrMoveDestExists when dst
// already present.
func TestMove_DestExists_NoForce_Errors(t *testing.T) {
	root := withSingleFileSchema(t)
	for _, id := range []string{"plans.foo", "plans.bar"} {
		if _, _, err := ops.Create(root, id, "plans.task", map[string]any{
			"id": id, "title": "x", "status": "todo",
		}); err != nil {
			t.Fatalf("seed %q: %v", id, err)
		}
	}
	_, err := ops.Move(root, "plans.foo", "plans.bar", "", ops.MoveOptions{})
	if err == nil {
		t.Fatal("expected ErrMoveDestExists")
	}
	if !errors.Is(err, ops.ErrMoveDestExists) {
		t.Errorf("err = %v, want ErrMoveDestExists", err)
	}
}

// TestMove_DestExists_ForceFlag_Overwrites — Force=true overwrites
// dst, src spliced.
func TestMove_DestExists_ForceFlag_Overwrites(t *testing.T) {
	root := withSingleFileSchema(t)
	if _, _, err := ops.Create(root, "plans.foo", "plans.task", map[string]any{
		"id": "foo", "title": "from-foo", "status": "todo",
	}); err != nil {
		t.Fatalf("seed foo: %v", err)
	}
	if _, _, err := ops.Create(root, "plans.bar", "plans.task", map[string]any{
		"id": "bar", "title": "from-bar", "status": "doing",
	}); err != nil {
		t.Fatalf("seed bar: %v", err)
	}
	if _, err := ops.Move(root, "plans.foo", "plans.bar", "", ops.MoveOptions{Force: true}); err != nil {
		t.Fatalf("Move (force): %v", err)
	}
	got, _, err := ops.GetAllFields(root, "plans.bar", "")
	if err != nil {
		t.Fatalf("GetAllFields dst: %v", err)
	}
	if got.Fields["title"] != "from-foo" {
		t.Errorf("title = %v, want from-foo (overwritten)", got.Fields["title"])
	}
	if _, _, err := ops.GetAllFields(root, "plans.foo", ""); err == nil {
		t.Errorf("src still exists after force-overwrite move")
	}
}

// TestMove_SelfCopy_Errors — Copy=true with src==dst → ErrMoveSelfCopy.
func TestMove_SelfCopy_Errors(t *testing.T) {
	root := withSingleFileSchema(t)
	if _, _, err := ops.Create(root, "plans.foo", "plans.task", map[string]any{
		"id": "foo", "title": "x", "status": "todo",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := ops.Move(root, "plans.foo", "plans.foo", "", ops.MoveOptions{Copy: true})
	if err == nil {
		t.Fatal("expected ErrMoveSelfCopy")
	}
	if !errors.Is(err, ops.ErrMoveSelfCopy) {
		t.Errorf("err = %v, want ErrMoveSelfCopy", err)
	}
}

// TestMove_SelfMove_Errors — Copy=false with src==dst → ErrMoveSelfMove.
func TestMove_SelfMove_Errors(t *testing.T) {
	root := withSingleFileSchema(t)
	if _, _, err := ops.Create(root, "plans.foo", "plans.task", map[string]any{
		"id": "foo", "title": "x", "status": "todo",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := ops.Move(root, "plans.foo", "plans.foo", "", ops.MoveOptions{})
	if err == nil {
		t.Fatal("expected ErrMoveSelfMove")
	}
	if !errors.Is(err, ops.ErrMoveSelfMove) {
		t.Errorf("err = %v, want ErrMoveSelfMove", err)
	}
}

// TestMove_TypeDefaulting_SharedTypeName — both dbs have `task` type;
// --type omitted; defaults to src's bareType.
func TestMove_TypeDefaulting_SharedTypeName(t *testing.T) {
	root := withMoveCrossDBSchema(t)
	if _, _, err := ops.Create(root, "plans.task-007", "plans.task", map[string]any{
		"id": "task-007", "title": "x", "status": "todo",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := ops.Move(root, "plans.task-007", "drop_3.db.task-007", "", ops.MoveOptions{}); err != nil {
		t.Fatalf("Move with type defaulting: %v", err)
	}
	idx, _ := index.Load(root)
	entry, ok := idx.Get("drop_3.db.task-007")
	if !ok {
		t.Fatal("index missing dst")
	}
	if entry.Type != "task" {
		t.Errorf("dst type = %q, want task (defaulted from src)", entry.Type)
	}
}

// TestMove_TypeDefaulting_NoSharedTypeName_Errors — dbs share NO type
// names; --type omitted; errors with "specify --type" guidance.
func TestMove_TypeDefaulting_NoSharedTypeName_Errors(t *testing.T) {
	root := withMoveDisjointTypeSchema(t)
	if _, _, err := ops.Create(root, "plans.foo", "plans.task", map[string]any{
		"id": "foo", "title": "x",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := ops.Move(root, "plans.foo", "other.bar", "", ops.MoveOptions{})
	if err == nil {
		t.Fatal("expected error for missing --type across disjoint dbs")
	}
	if !strings.Contains(err.Error(), "specify --type") {
		t.Errorf("error %q missing 'specify --type' guidance", err.Error())
	}
}

// TestMove_TypeOverride_DBQualified — --type=cascade.drop accepted.
func TestMove_TypeOverride_DBQualified(t *testing.T) {
	root := withMoveCrossDBSchema(t)
	if _, _, err := ops.Create(root, "plans.task-007", "plans.task", map[string]any{
		"id": "task-007", "title": "x", "status": "todo",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := ops.Move(root, "plans.task-007", "drop_3.db.t1", "cascade.drop", ops.MoveOptions{}); err != nil {
		t.Fatalf("Move with explicit cross-db type: %v", err)
	}
	idx, _ := index.Load(root)
	entry, ok := idx.Get("drop_3.db.t1")
	if !ok {
		t.Fatal("index missing dst")
	}
	if entry.Type != "drop" {
		t.Errorf("dst type = %q, want drop", entry.Type)
	}
}

// TestMove_TypeOverride_BareRejected — bare --type=drop (no db prefix)
// errors with ErrTypeNotQualified.
func TestMove_TypeOverride_BareRejected(t *testing.T) {
	root := withMoveCrossDBSchema(t)
	if _, _, err := ops.Create(root, "plans.task-007", "plans.task", map[string]any{
		"id": "task-007", "title": "x", "status": "todo",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := ops.Move(root, "plans.task-007", "drop_3.db.task-007", "drop", ops.MoveOptions{})
	if err == nil {
		t.Fatal("expected ErrTypeNotQualified for bare --type")
	}
	if !errors.Is(err, ops.ErrTypeNotQualified) {
		t.Errorf("err = %v, want ErrTypeNotQualified", err)
	}
}

// TestMove_IndexConsistency_AfterMove — read index after move; src
// absent, dst present, no stale entries.
func TestMove_IndexConsistency_AfterMove(t *testing.T) {
	root := withSingleFileSchema(t)
	if _, _, err := ops.Create(root, "plans.foo", "plans.task", map[string]any{
		"id": "foo", "title": "x", "status": "todo",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := ops.Move(root, "plans.foo", "plans.bar", "", ops.MoveOptions{}); err != nil {
		t.Fatalf("Move: %v", err)
	}
	idx, err := index.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := idx.Get("plans.foo"); ok {
		t.Errorf("index still carries src after move")
	}
	if _, ok := idx.Get("plans.bar"); !ok {
		t.Errorf("index missing dst after move")
	}
}
