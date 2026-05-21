package db

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/schema"
)

// testRegistry builds an F10-shaped registry exercising single-file
// and glob mounts. Collection mounts (trailing-slash, `.`) are
// rejected at schema-load time per F10 (PLAN §12.17.9).
//
// Per F10 the id grammar is `<file-relpath>.<bracket-key>` — type is
// not in the id, it lives in the runtime index.
func testRegistry() schema.Registry {
	return schema.Registry{DBs: map[string]schema.DB{
		"readme": {
			Name:   "readme",
			Paths:  []string{"README.md"},
			Format: schema.FormatMD,
			Types: map[string]schema.SectionType{
				"title":   {Name: "title", Heading: 1},
				"section": {Name: "section", Heading: 2},
			},
		},
		"plan_db": {
			Name:   "plan_db",
			Paths:  []string{"workflow/*/db.toml"},
			Format: schema.FormatTOML,
			Types: map[string]schema.SectionType{
				"build_task": {Name: "build_task"},
				"qa_task":    {Name: "qa_task"},
			},
		},
	}}
}

func TestResolveIDSingleFile(t *testing.T) {
	r := NewResolver("/proj", testRegistry())

	res, db, err := r.ResolveID("README.installation")
	if err != nil {
		t.Fatalf("ResolveID: %v", err)
	}
	if db.Name != "readme" {
		t.Errorf("db.Name = %q, want readme", db.Name)
	}
	if res.DBName != "readme" {
		t.Errorf("res.DBName = %q, want readme", res.DBName)
	}
	if res.FileRelPath != "README" {
		t.Errorf("res.FileRelPath = %q, want README", res.FileRelPath)
	}
	if res.BracketKey != "installation" {
		t.Errorf("res.BracketKey = %q, want installation", res.BracketKey)
	}
	if !res.SingleFileMount {
		t.Errorf("res.SingleFileMount = false; want true")
	}
	if want := filepath.Join("/proj", "README.md"); res.FilePath != want {
		t.Errorf("res.FilePath = %q, want %q", res.FilePath, want)
	}
}

func TestResolveIDGlobMount(t *testing.T) {
	r := NewResolver("/proj", testRegistry())

	// Mount `workflow/*/db.toml` has static-prefix `workflow/`; the id
	// starts AFTER the static prefix, so id = `<glob-segment>.<db>.<bracket-key>`.
	res, db, err := r.ResolveID("ta.db.task_001")
	if err != nil {
		t.Fatalf("ResolveID: %v", err)
	}
	if db.Name != "plan_db" {
		t.Errorf("db.Name = %q, want plan_db", db.Name)
	}
	if res.DBName != "plan_db" {
		t.Errorf("res.DBName = %q", res.DBName)
	}
	if res.FileRelPath != "ta.db" || res.BracketKey != "task_001" {
		t.Errorf("res = %+v", res)
	}
	if res.SingleFileMount {
		t.Errorf("res.SingleFileMount = true; want false for glob mount")
	}
	if want := filepath.Join("/proj", "workflow", "ta", "db.toml"); res.FilePath != want {
		t.Errorf("res.FilePath = %q, want %q", res.FilePath, want)
	}
}

func TestResolveIDDottedKeysAccepted(t *testing.T) {
	r := NewResolver("/proj", testRegistry())

	cases := []struct {
		id          string
		wantRelPath string
		wantBracket string
	}{
		{"README.install", "README", "install"},
		{"README.install.sub", "README", "install.sub"},
		{"README.a.b.c.d", "README", "a.b.c.d"},
		{"ta.db.task_001", "ta.db", "task_001"},
		{"ta.db.t1.subtask", "ta.db", "t1.subtask"},
	}
	for _, tc := range cases {
		res, _, err := r.ResolveID(tc.id)
		if err != nil {
			t.Errorf("ResolveID(%q): unexpected error %v", tc.id, err)
			continue
		}
		if res.FileRelPath != tc.wantRelPath || res.BracketKey != tc.wantBracket {
			t.Errorf("ResolveID(%q) = %+v, want FileRelPath=%q BracketKey=%q",
				tc.id, res, tc.wantRelPath, tc.wantBracket)
		}
		if got := res.Canonical(); got != tc.id {
			t.Errorf("Canonical(%+v) = %q, want %q", res, got, tc.id)
		}
	}
}

func TestResolveIDTooFewSegments(t *testing.T) {
	r := NewResolver("/proj", testRegistry())

	// Single-file: need <file-relpath>.<bracket-key> (2+ segments). README alone errs.
	if _, _, err := r.ResolveID("README"); err == nil {
		t.Error("expected error for README (no bracket-key)")
	}
	// Glob: need <glob>.<db>.<bracket-key> (3+).
	if _, _, err := r.ResolveID("ta.db"); err == nil {
		t.Error("expected error for ta.db (no bracket-key)")
	}
}

func TestResolveIDRejectsEmptySegments(t *testing.T) {
	r := NewResolver("/proj", testRegistry())

	cases := []string{
		".README.install",
		"README.install.",
		"README..install",
		"ta..db.task_001",
		".ta.db.task_001",
	}
	for _, s := range cases {
		if _, _, err := r.ResolveID(s); err == nil {
			t.Errorf("ResolveID(%q): expected error, got nil", s)
		} else if !errors.Is(err, ErrBadID) {
			t.Errorf("ResolveID(%q): expected ErrBadID, got %v", s, err)
		}
	}
}

func TestResolveIDDoesNotMatchAnyDB(t *testing.T) {
	reg := schema.Registry{DBs: map[string]schema.DB{
		"plan_db": {
			Name:   "plan_db",
			Paths:  []string{"workflow/*/db.toml"},
			Format: schema.FormatTOML,
			Types: map[string]schema.SectionType{
				"build_task": {Name: "build_task"},
			},
		},
	}}
	r := NewResolver("/proj", reg)

	_, _, err := r.ResolveID("nope.x")
	if err == nil {
		t.Fatal("expected error for unknown file-relpath")
	}
	if !errors.Is(err, ErrIDDoesNotMatchAnyDB) {
		t.Errorf("expected ErrIDDoesNotMatchAnyDB, got %v", err)
	}
}

func TestResolveIDEmpty(t *testing.T) {
	r := NewResolver("/proj", testRegistry())
	if _, _, err := r.ResolveID(""); err == nil {
		t.Fatal("expected error for empty id")
	}
}

func TestResolveIDHomeRelativeMount(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("os.UserHomeDir: %v", err)
	}

	reg := schema.Registry{DBs: map[string]schema.DB{
		"home_db": {
			Name:   "home_db",
			Paths:  []string{"~/.ta/projects/foo/db.toml"},
			Format: schema.FormatTOML,
			Types: map[string]schema.SectionType{
				"task": {Name: "task"},
			},
		},
	}}
	r := NewResolver("/proj", reg)

	res, db, err := r.ResolveID("db.t1")
	if err != nil {
		t.Fatalf("ResolveID: %v", err)
	}
	if db.Name != "home_db" {
		t.Errorf("db.Name = %q, want home_db", db.Name)
	}
	if res.BracketKey != "t1" {
		t.Errorf("res.BracketKey = %q, want t1", res.BracketKey)
	}
	want := filepath.Join(home, ".ta", "projects", "foo", "db.toml")
	if res.FilePath != want {
		t.Errorf("res.FilePath = %q, want %q", res.FilePath, want)
	}
}

// TestResolveIDInDBHappyPath: a 3-segment id under a glob mount
// resolves to the named db without falling through to any other db
// in the registry. This is the F29 base case — the named db is the
// authoritative anchor when --type is supplied.
func TestResolveIDInDBHappyPath(t *testing.T) {
	r := NewResolver("/proj", testRegistry())
	res, dbDecl, err := r.ResolveIDInDB("ta.db.task_001", "plan_db")
	if err != nil {
		t.Fatalf("ResolveIDInDB: %v", err)
	}
	if dbDecl.Name != "plan_db" {
		t.Errorf("dbDecl.Name = %q, want plan_db", dbDecl.Name)
	}
	if res.DBName != "plan_db" {
		t.Errorf("res.DBName = %q, want plan_db", res.DBName)
	}
	if res.FileRelPath != "ta.db" || res.BracketKey != "task_001" {
		t.Errorf("res = %+v", res)
	}
}

// TestResolveIDInDBRejectsWithExpectedShape: a 2-segment id against a
// db whose mount needs 3 segments errors with a message that names
// the expected shape and segment count. The id `g.n` would resolve
// successfully against `readme` (single-file mount, 2 segments) under
// plain ResolveID — but type-aware lookup constrained to `plan_db`
// must reject it loudly.
func TestResolveIDInDBRejectsWithExpectedShape(t *testing.T) {
	r := NewResolver("/proj", testRegistry())
	_, _, err := r.ResolveIDInDB("g.n", "plan_db")
	if err == nil {
		t.Fatal("expected error for 2-segment id under glob mount")
	}
	if !errors.Is(err, ErrIDDoesNotMatchAnyDB) {
		t.Errorf("expected ErrIDDoesNotMatchAnyDB, got %v", err)
	}
	msg := err.Error()
	for _, want := range []string{
		`db "plan_db"`, `does not accept id "g.n"`, "expected shape", "<bracket-key>", "got 2 segments",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
}

// TestResolveIDInDBUnknownDB: a db name not declared in the registry
// surfaces ErrIDDoesNotMatchAnyDB with a message naming the db. F29
// constrains iteration to the named db, so the missing-db case has
// to surface here rather than silently falling through to plain
// ResolveID semantics.
func TestResolveIDInDBUnknownDB(t *testing.T) {
	r := NewResolver("/proj", testRegistry())
	_, _, err := r.ResolveIDInDB("ta.db.task_001", "nope")
	if err == nil {
		t.Fatal("expected error for unknown db name")
	}
	if !errors.Is(err, ErrIDDoesNotMatchAnyDB) {
		t.Errorf("expected ErrIDDoesNotMatchAnyDB, got %v", err)
	}
	if !strings.Contains(err.Error(), `"nope"`) {
		t.Errorf("error %q should name the unknown db", err.Error())
	}
}

// fileRecordRegistry builds an F31 registry mixing a file-as-record
// db (`agents/*/*.md`) with a regular section-mode db (README.md).
// Used for the F31 id-grammar relaxation tests.
func fileRecordRegistry() schema.Registry {
	return schema.Registry{DBs: map[string]schema.DB{
		"agents": {
			Name:   "agents",
			Paths:  []string{"agents/*/*.md"},
			Format: schema.FormatMD,
			Types: map[string]schema.SectionType{
				"agent": {
					Name:      "agent",
					RecordPer: schema.RecordPerFile,
					BodyField: "prompt",
				},
			},
		},
		"readme": {
			Name:   "readme",
			Paths:  []string{"README.md"},
			Format: schema.FormatMD,
			Types: map[string]schema.SectionType{
				"section": {Name: "section", Heading: 2},
			},
		},
	}}
}

// TestResolveID_FileAsRecord_NoBracketKey: per F31, an id whose
// segment count exactly matches a file-as-record db's file-relpath
// length resolves with empty BracketKey. The whole file is the
// record.
func TestResolveID_FileAsRecord_NoBracketKey(t *testing.T) {
	r := NewResolver("/proj", fileRecordRegistry())

	// Mount `agents/*/*.md` has static prefix `agents/`; the id's
	// segments AFTER the static prefix form the file-relpath. With
	// no bracket-key tail (file-as-record), `<group>.<name>` (2 segs)
	// is sufficient.
	res, dbDecl, err := r.ResolveID("ta.writer")
	if err != nil {
		t.Fatalf("ResolveID: %v", err)
	}
	if dbDecl.Name != "agents" {
		t.Errorf("dbDecl.Name = %q, want agents", dbDecl.Name)
	}
	if res.BracketKey != "" {
		t.Errorf("res.BracketKey = %q, want empty (file-as-record)", res.BracketKey)
	}
	if res.FileRelPath != "ta.writer" {
		t.Errorf("res.FileRelPath = %q, want ta.writer", res.FileRelPath)
	}
	want := filepath.Join("/proj", "agents", "ta", "writer.md")
	if res.FilePath != want {
		t.Errorf("res.FilePath = %q, want %q", res.FilePath, want)
	}
}

// TestResolveID_FileAsRecord_RespectsMountWildcards: a 3-segment id
// against a file-as-record glob mount with 2 wildcard segments still
// resolves cleanly; the trailing segment is treated as bracket-key
// even though the type is file-as-record (callers may want sub-record
// addressing later — for now BracketKey just survives).
//
// More importantly: a 1-segment id against the same mount fails — the
// relaxation only allows the EXACT file-relpath length, not anything
// shorter.
func TestResolveID_FileAsRecord_RespectsMountWildcards(t *testing.T) {
	r := NewResolver("/proj", fileRecordRegistry())

	// 1-segment id is below the file-relpath length (mount needs 2
	// glob segments). Must fail.
	if _, _, err := r.ResolveID("just-one"); err == nil {
		t.Error("expected error for 1-segment id against agents/*/*.md")
	}
}

// TestResolveID_SectionOnlyDB_StillRequiresBracketKey: F31 relaxation
// must NOT bleed into section-only dbs. README.md (section-mode) keeps
// the F10 strict rule — id needs file-relpath + bracket-key.
func TestResolveID_SectionOnlyDB_StillRequiresBracketKey(t *testing.T) {
	r := NewResolver("/proj", fileRecordRegistry())

	// Bare README is the file-relpath; without a bracket-key the
	// section-only db rejects it. (The file-as-record agents db
	// accepts a 2-segment id like `ta.writer` but `README` is only
	// 1 segment under the README.md single-file mount, so neither
	// db accepts.)
	if _, _, err := r.ResolveID("README"); err == nil {
		t.Error("expected error for README without bracket-key (section-only db)")
	}
}

// claudeAgentsRegistry mirrors the F35 consolidated single-db
// multi-mount shape: one `claude_agents` db with a 2-segment glob mount
// (home library `agents/<kind>/<name>.md`) AND a 1-segment glob mount
// (project install `.claude/agents/<flat-name>.md`). Both produce
// file-as-record `agent` records.
func claudeAgentsRegistry() schema.Registry {
	return schema.Registry{DBs: map[string]schema.DB{
		"claude_agents": {
			Name:   "claude_agents",
			Paths:  []string{"agents/*/*.md", ".claude/agents/*.md"},
			Format: schema.FormatMD,
			Types: map[string]schema.SectionType{
				"agent": {
					Name:      "agent",
					RecordPer: schema.RecordPerFile,
					BodyField: "prompt",
				},
			},
		},
	}}
}

// TestResolveID_ClaudeAgents_MultiMount_ResolveHome locks the F35
// single-db multi-mount happy path on the home library mount: a
// 2-segment id like `go.builder` resolves to
// `<root>/agents/go/builder.md` under the `agents/*/*.md` mount.
func TestResolveID_ClaudeAgents_MultiMount_ResolveHome(t *testing.T) {
	r := NewResolver("/proj", claudeAgentsRegistry())

	res, dbDecl, err := r.ResolveID("go.builder")
	if err != nil {
		t.Fatalf("ResolveID(go.builder): %v", err)
	}
	if dbDecl.Name != "claude_agents" {
		t.Errorf("dbDecl.Name = %q, want claude_agents", dbDecl.Name)
	}
	if res.FileRelPath != "go.builder" {
		t.Errorf("res.FileRelPath = %q, want go.builder", res.FileRelPath)
	}
	if res.BracketKey != "" {
		t.Errorf("res.BracketKey = %q, want empty (file-as-record)", res.BracketKey)
	}
	if want := filepath.Join("/proj", "agents", "go", "builder.md"); res.FilePath != want {
		t.Errorf("res.FilePath = %q, want %q", res.FilePath, want)
	}
}

// TestResolveID_ClaudeAgents_MultiMount_ResolveProject locks the F35
// single-db multi-mount happy path on the project install mount: a
// 1-segment id like `go-builder` resolves to
// `<root>/.claude/agents/go-builder.md` under the
// `.claude/agents/*.md` mount.
func TestResolveID_ClaudeAgents_MultiMount_ResolveProject(t *testing.T) {
	r := NewResolver("/proj", claudeAgentsRegistry())

	res, dbDecl, err := r.ResolveID("go-builder")
	if err != nil {
		t.Fatalf("ResolveID(go-builder): %v", err)
	}
	if dbDecl.Name != "claude_agents" {
		t.Errorf("dbDecl.Name = %q, want claude_agents", dbDecl.Name)
	}
	if res.FileRelPath != "go-builder" {
		t.Errorf("res.FileRelPath = %q, want go-builder", res.FileRelPath)
	}
	if res.BracketKey != "" {
		t.Errorf("res.BracketKey = %q, want empty (file-as-record)", res.BracketKey)
	}
	if want := filepath.Join("/proj", ".claude", "agents", "go-builder.md"); res.FilePath != want {
		t.Errorf("res.FilePath = %q, want %q", res.FilePath, want)
	}
}

// TestResolveID_ClaudeAgents_MultiMount_OvershootSkipsToNextMount is
// the F35 P0 regression lock for the resolver upper-bound fix. The
// home mount `agents/*/*.md` expects file-relpath length 2; the project
// mount `.claude/agents/*.md` expects file-relpath length 1. With ONLY
// the upper-bound check in place, an id with length 2 does not silently
// match the project mount with a stray bracket-key tail (file-as-record
// dbs have no bracket-key) — the project mount skips, the home mount
// matches, and the id resolves under the home mount.
//
// Pre-fix, with the project mount declared FIRST (alphabetical / iter
// order isn't guaranteed), the file-as-record id `go.builder` could be
// silently absorbed by the project mount with FileRelPath=`go` and
// BracketKey=`builder` — wrong db, wrong on-disk file. The test
// declares the project mount first to exercise the skip path.
func TestResolveID_ClaudeAgents_MultiMount_OvershootSkipsToNextMount(t *testing.T) {
	reg := schema.Registry{DBs: map[string]schema.DB{
		"claude_agents": {
			Name:   "claude_agents",
			Paths:  []string{".claude/agents/*.md", "agents/*/*.md"},
			Format: schema.FormatMD,
			Types: map[string]schema.SectionType{
				"agent": {
					Name:      "agent",
					RecordPer: schema.RecordPerFile,
					BodyField: "prompt",
				},
			},
		},
	}}
	r := NewResolver("/proj", reg)

	res, _, err := r.ResolveID("go.builder")
	if err != nil {
		t.Fatalf("ResolveID(go.builder): %v", err)
	}
	if res.BracketKey != "" {
		t.Errorf("res.BracketKey = %q, want empty — overshoot must skip the 1-segment project mount, not match it with a stray bracket-key", res.BracketKey)
	}
	if res.FileRelPath != "go.builder" {
		t.Errorf("res.FileRelPath = %q, want go.builder (matched against the 2-segment home mount)", res.FileRelPath)
	}
	if want := filepath.Join("/proj", "agents", "go", "builder.md"); res.FilePath != want {
		t.Errorf("res.FilePath = %q, want %q (home mount), got the wrong mount", res.FilePath, want)
	}
}

// TestResolveID_PrefixGlobMountSegment_Accepted locks in F38d-2.11
// Bug 2: a mount segment like `drop_*` is a prefix-glob, not a
// literal directory name. An id whose corresponding segment matches
// the prefix pattern (e.g. `drop_001` against `drop_*`) MUST resolve
// cleanly. Pre-fix the parser only handled bare `*` segments and
// rejected the id silently with "need 3 got 3 segments".
func TestResolveID_PrefixGlobMountSegment_Accepted(t *testing.T) {
	reg := schema.Registry{DBs: map[string]schema.DB{
		"cascade": {
			Name:   "cascade",
			Paths:  []string{".ta/cascade/drops/drop_*/drop.toml"},
			Format: schema.FormatTOML,
			Types: map[string]schema.SectionType{
				"drop": {Name: "drop"},
			},
		},
	}}
	r := NewResolver("/proj", reg)

	res, dbDecl, err := r.ResolveID("drop_001.drop.dogfood_smoke")
	if err != nil {
		t.Fatalf("ResolveID(drop_001.drop.dogfood_smoke): %v", err)
	}
	if dbDecl.Name != "cascade" {
		t.Errorf("dbDecl.Name = %q, want cascade", dbDecl.Name)
	}
	if res.FileRelPath != "drop_001.drop" {
		t.Errorf("res.FileRelPath = %q, want drop_001.drop", res.FileRelPath)
	}
	if res.BracketKey != "dogfood_smoke" {
		t.Errorf("res.BracketKey = %q, want dogfood_smoke", res.BracketKey)
	}
	want := filepath.Join("/proj", ".ta", "cascade", "drops", "drop_001", "drop.toml")
	if res.FilePath != want {
		t.Errorf("res.FilePath = %q, want %q", res.FilePath, want)
	}
}

// TestResolveID_PrefixGlobMountSegment_NonMatchingPrefixRejected
// confirms the prefix-glob matcher is not a free pass: a leading
// segment that doesn't match the literal prefix part of `drop_*`
// (e.g. `wrongprefix_001`) still fails to resolve.
func TestResolveID_PrefixGlobMountSegment_NonMatchingPrefixRejected(t *testing.T) {
	reg := schema.Registry{DBs: map[string]schema.DB{
		"cascade": {
			Name:   "cascade",
			Paths:  []string{".ta/cascade/drops/drop_*/drop.toml"},
			Format: schema.FormatTOML,
			Types: map[string]schema.SectionType{
				"drop": {Name: "drop"},
			},
		},
	}}
	r := NewResolver("/proj", reg)

	_, _, err := r.ResolveID("wrongprefix_001.drop.x")
	if err == nil {
		t.Fatal("expected error for id whose first segment does not match prefix-glob")
	}
	if !errors.Is(err, ErrIDDoesNotMatchAnyDB) {
		t.Errorf("expected ErrIDDoesNotMatchAnyDB, got %v", err)
	}
}

// TestResolveIDInDB_ErrorMessageHasNoDuplicateExpectedShape locks in
// F38d-2.11 Bug 1: the resolver-miss error MUST contain "expected
// shape:" EXACTLY ONCE. Pre-fix, mountExpectedShape and
// expectedShapeForDB each prepended the prefix, producing
// "expected shape: expected shape: ...".
func TestResolveIDInDB_ErrorMessageHasNoDuplicateExpectedShape(t *testing.T) {
	reg := schema.Registry{DBs: map[string]schema.DB{
		"cascade": {
			Name:   "cascade",
			Paths:  []string{".ta/cascade/drops/drop_*/drop.toml"},
			Format: schema.FormatTOML,
			Types: map[string]schema.SectionType{
				"drop": {Name: "drop"},
			},
		},
	}}
	r := NewResolver("/proj", reg)

	// 2-segment id fails the segment-count check; "got 2" branch
	// surfaces the expected-shape rendering through expectedShapeForDB.
	_, _, err := r.ResolveIDInDB("drop_001.drop", "cascade")
	if err == nil {
		t.Fatal("expected error for 2-segment id against cascade")
	}
	msg := err.Error()
	if got := strings.Count(msg, "expected shape:"); got != 1 {
		t.Errorf(`error contains %d copies of "expected shape:", want 1; full message: %q`, got, msg)
	}
}

// TestF10_AddressGrammarIsBracketEqualsID is the regression anchor for
// the F10 closure (E2E_FIXES.md:136, RESOLVED in commit c467803
// "feat(id): drop type from id; align bracket=id; index v2;
// format-from-extension"). The id grammar is `<file-relpath>.<bracket-key>`
// with NO db prefix and NO type segment — type lives in the runtime
// index, never in the id. On disk, the bracket header IS the id.
//
// This test pins TWO load-bearing invariants of that closure:
//
//  1. The `Resolved` struct does NOT carry a `Type` field. A future
//     edit that re-introduces a `Type` field on Resolved (the historical
//     F10-violating shape) MUST surface here as a loud failure.
//  2. Constructive `ResolveID` calls produce a Resolved value whose
//     `BracketKey` IS the bracket-tail of the id — there is no
//     out-of-band type lookup synthesizing it. The test names this
//     property "bracket-equals-id".
//
// The TestF10_-prefix on this function (and any sibling F10 anchors
// added later) is the regression-discoverability anchor — grepping or
// running Hylla refs-find on `TestF10_` lands the F10 closure
// attestation regardless of which package re-asserts it.
func TestF10_AddressGrammarIsBracketEqualsID(t *testing.T) {
	// Invariant 1: the Resolved struct must have no `Type` field.
	// Compile-time would catch a test that references res.Type, but
	// asserting the absence at runtime via reflect lets THIS test be
	// the durable record of the contract instead of the absence-of-
	// other-tests.
	if f, ok := reflect.TypeOf(Resolved{}).FieldByName("Type"); ok {
		t.Fatalf(
			"F10 violation: Resolved struct has a Type field (kind=%s) — "+
				"type must live in the runtime index, never in the id. "+
				"See E2E_FIXES.md:136 + commit c467803.",
			f.Type.Kind(),
		)
	}

	// Invariant 2: BracketKey equals the bracket-tail of the id
	// across every mount shape, with no type indirection.
	t.Run("section-mode_single_file", func(t *testing.T) {
		r := NewResolver("/proj", testRegistry())
		res, _, err := r.ResolveID("README.installation")
		if err != nil {
			t.Fatalf("ResolveID: %v", err)
		}
		if res.BracketKey != "installation" {
			t.Errorf("BracketKey = %q, want %q (bracket-equals-id)",
				res.BracketKey, "installation")
		}
		if res.FileRelPath != "README" {
			t.Errorf("FileRelPath = %q, want %q", res.FileRelPath, "README")
		}
		// Canonical() reassembles file-relpath + bracket-key with no
		// type segment — round-trip proves the id has NO type slot.
		if got := res.Canonical(); got != "README.installation" {
			t.Errorf("Canonical() = %q, want %q (no type segment in id)",
				got, "README.installation")
		}
	})

	t.Run("section-mode_glob_mount", func(t *testing.T) {
		r := NewResolver("/proj", testRegistry())
		res, _, err := r.ResolveID("ta.db.task_001")
		if err != nil {
			t.Fatalf("ResolveID: %v", err)
		}
		if res.BracketKey != "task_001" {
			t.Errorf("BracketKey = %q, want %q (bracket-equals-id)",
				res.BracketKey, "task_001")
		}
		if res.FileRelPath != "ta.db" {
			t.Errorf("FileRelPath = %q, want %q", res.FileRelPath, "ta.db")
		}
		if got := res.Canonical(); got != "ta.db.task_001" {
			t.Errorf("Canonical() = %q, want %q (no type segment in id)",
				got, "ta.db.task_001")
		}
	})

	t.Run("file_as_record_mount", func(t *testing.T) {
		// File-as-record dbs have NO bracket-tail — the whole file IS
		// the record. BracketKey="" is the degenerate bracket-equals-id
		// case: the id resolves to the file itself with no inner anchor
		// and, crucially, no type segment substituting for one.
		r := NewResolver("/proj", fileRecordRegistry())
		res, _, err := r.ResolveID("ta.writer")
		if err != nil {
			t.Fatalf("ResolveID: %v", err)
		}
		if res.BracketKey != "" {
			t.Errorf("BracketKey = %q, want empty (file-as-record bracket-equals-id)",
				res.BracketKey)
		}
		if res.FileRelPath != "ta.writer" {
			t.Errorf("FileRelPath = %q, want %q", res.FileRelPath, "ta.writer")
		}
		if got := res.Canonical(); got != "ta.writer" {
			t.Errorf("Canonical() = %q, want %q (no type segment in id)",
				got, "ta.writer")
		}
	})
}

func TestResolvedCanonical(t *testing.T) {
	cases := []struct {
		res  Resolved
		want string
	}{
		{Resolved{FileRelPath: "README", BracketKey: "installation"}, "README.installation"},
		{Resolved{FileRelPath: "ta.db", BracketKey: "task_001"}, "ta.db.task_001"},
		{Resolved{FileRelPath: "plans", BracketKey: "demo-1"}, "plans.demo-1"},
		{Resolved{FileRelPath: "plans", BracketKey: ""}, "plans"},
	}
	for _, tc := range cases {
		if got := tc.res.Canonical(); got != tc.want {
			t.Errorf("Canonical() = %q, want %q", got, tc.want)
		}
	}
}

// TestResolveID_AgentsMDSectionRoundTrip verifies that ResolveID
// correctly resolves markdown section-mode ids like
// "CLAUDE.section.project-specific-docs" where:
// - agents_md declares section-mode types at heading=2
// - CLAUDE.md contains multiple H2 sections
// - the id includes the type name as part of the bracket key
// - no index seeding is required (no-index branch)
//
// This is the resolver-layer regression lock for agents_md.section
// per-id resolution, complementing D1's ops-layer test.
func TestResolveID_AgentsMDSectionRoundTrip(t *testing.T) {
	// Build a registry that mirrors the agents_md shape from the
	// project's .ta/schema.toml: single-file markdown db with
	// section-mode types at heading=2.
	reg := schema.Registry{DBs: map[string]schema.DB{
		"agents_md": {
			Name:   "agents_md",
			Paths:  []string{"CLAUDE.md", "AGENTS.md"},
			Format: schema.FormatMD,
			Types: map[string]schema.SectionType{
				"section": {Name: "section", Heading: 2},
			},
		},
	}}

	r := NewResolver("/proj", reg)

	// Test case 1: resolve CLAUDE.section.project-specific-docs
	res, dbDecl, err := r.ResolveID("CLAUDE.section.project-specific-docs")
	if err != nil {
		t.Fatalf("ResolveID(CLAUDE.section.project-specific-docs): %v", err)
	}
	if dbDecl.Name != "agents_md" {
		t.Errorf("dbDecl.Name = %q, want agents_md", dbDecl.Name)
	}
	if res.DBName != "agents_md" {
		t.Errorf("res.DBName = %q, want agents_md", res.DBName)
	}
	if res.FileRelPath != "CLAUDE" {
		t.Errorf("res.FileRelPath = %q, want CLAUDE", res.FileRelPath)
	}
	if res.BracketKey != "section.project-specific-docs" {
		t.Errorf("res.BracketKey = %q, want section.project-specific-docs", res.BracketKey)
	}
	if !res.SingleFileMount {
		t.Errorf("res.SingleFileMount = false; want true for single-file mount")
	}
	want := filepath.Join("/proj", "CLAUDE.md")
	if res.FilePath != want {
		t.Errorf("res.FilePath = %q, want %q", res.FilePath, want)
	}
	if got := res.Canonical(); got != "CLAUDE.section.project-specific-docs" {
		t.Errorf("Canonical() = %q, want CLAUDE.section.project-specific-docs", got)
	}

	// Test case 2: resolve AGENTS.section.ta-cli-usage
	res2, dbDecl2, err := r.ResolveID("AGENTS.section.ta-cli-usage")
	if err != nil {
		t.Fatalf("ResolveID(AGENTS.section.ta-cli-usage): %v", err)
	}
	if dbDecl2.Name != "agents_md" {
		t.Errorf("dbDecl.Name = %q, want agents_md", dbDecl2.Name)
	}
	if res2.FileRelPath != "AGENTS" {
		t.Errorf("res.FileRelPath = %q, want AGENTS", res2.FileRelPath)
	}
	if res2.BracketKey != "section.ta-cli-usage" {
		t.Errorf("res.BracketKey = %q, want section.ta-cli-usage", res2.BracketKey)
	}
	want2 := filepath.Join("/proj", "AGENTS.md")
	if res2.FilePath != want2 {
		t.Errorf("res.FilePath = %q, want %q", res2.FilePath, want2)
	}
}
