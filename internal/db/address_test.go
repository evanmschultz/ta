package db

import (
	"errors"
	"os"
	"path/filepath"
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
