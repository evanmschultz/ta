package db

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/evanmschultz/ta/internal/schema"
)

// testRegistry builds a Phase 9.2-shaped registry exercising all three
// new mount shapes per the locked design (PLAN §12.17.9):
//
//   - readme: Paths=["README"]             → single-file mount.
//   - plan_db: Paths=["workflow/*/db"]     → glob (one file per drop).
//   - docs:    Paths=["docs/"]             → collection root.
//
// Address grammar is `<file-relpath>.<type>.<id-tail>` — no leading
// db-name segment. The mount-static-prefix is stripped from the on-disk
// path; the residual is dot-joined to produce file-relpath.
func testRegistry() schema.Registry {
	return schema.Registry{DBs: map[string]schema.DB{
		"readme": {
			Name:   "readme",
			Paths:  []string{"README"},
			Format: schema.FormatMD,
			Types: map[string]schema.SectionType{
				"title":   {Name: "title", Heading: 1},
				"section": {Name: "section", Heading: 2},
			},
		},
		"plan_db": {
			Name:   "plan_db",
			Paths:  []string{"workflow/*/db"},
			Format: schema.FormatTOML,
			Types: map[string]schema.SectionType{
				"build_task": {Name: "build_task"},
				"qa_task":    {Name: "qa_task"},
			},
		},
		"docs": {
			Name:   "docs",
			Paths:  []string{"docs/"},
			Format: schema.FormatMD,
			Types: map[string]schema.SectionType{
				"title":   {Name: "title", Heading: 1},
				"section": {Name: "section", Heading: 2},
			},
		},
	}}
}

func TestParseAddressSingleFile(t *testing.T) {
	r := NewResolver("/proj", testRegistry())

	addr, db, err := r.ParseAddress("README.section.installation")
	if err != nil {
		t.Fatalf("ParseAddress: %v", err)
	}
	if db.Name != "readme" {
		t.Errorf("db.Name = %q, want readme", db.Name)
	}
	if addr.DBName != "readme" {
		t.Errorf("addr.DBName = %q, want readme", addr.DBName)
	}
	if addr.FileRelPath != "README" {
		t.Errorf("addr.FileRelPath = %q, want README", addr.FileRelPath)
	}
	if addr.Type != "section" || addr.ID != "installation" {
		t.Errorf("addr = %+v", addr)
	}
	if !addr.SingleFileMount {
		t.Errorf("addr.SingleFileMount = false; want true for ['README']")
	}
	if want := filepath.Join("/proj", "README.md"); addr.FilePath != want {
		t.Errorf("addr.FilePath = %q, want %q", addr.FilePath, want)
	}
}

func TestParseAddressGlobMount(t *testing.T) {
	r := NewResolver("/proj", testRegistry())

	addr, db, err := r.ParseAddress("ta.db.build_task.task_001")
	if err != nil {
		t.Fatalf("ParseAddress: %v", err)
	}
	if db.Name != "plan_db" {
		t.Errorf("db.Name = %q, want plan_db", db.Name)
	}
	if addr.DBName != "plan_db" {
		t.Errorf("addr.DBName = %q", addr.DBName)
	}
	if addr.FileRelPath != "ta.db" || addr.Type != "build_task" || addr.ID != "task_001" {
		t.Errorf("addr = %+v", addr)
	}
	if addr.SingleFileMount {
		t.Errorf("addr.SingleFileMount = true; want false for glob mount")
	}
	if want := filepath.Join("/proj", "workflow", "ta", "db.toml"); addr.FilePath != want {
		t.Errorf("addr.FilePath = %q, want %q", addr.FilePath, want)
	}
}

func TestParseAddressCollectionMount(t *testing.T) {
	r := NewResolver("/proj", testRegistry())

	addr, db, err := r.ParseAddress("install.prereqs.section.title")
	if err != nil {
		t.Fatalf("ParseAddress: %v", err)
	}
	if db.Name != "docs" {
		t.Errorf("db.Name = %q, want docs", db.Name)
	}
	if addr.FileRelPath != "install.prereqs" || addr.Type != "section" || addr.ID != "title" {
		t.Errorf("addr = %+v", addr)
	}
	if want := filepath.Join("/proj", "docs", "install", "prereqs.md"); addr.FilePath != want {
		t.Errorf("addr.FilePath = %q, want %q", addr.FilePath, want)
	}
}

func TestParseAddressDottedIDsAccepted(t *testing.T) {
	r := NewResolver("/proj", testRegistry())

	cases := []struct {
		section     string
		wantRelPath string
		wantType    string
		wantID      string
	}{
		{"README.section.install", "README", "section", "install"},
		{"README.section.install.sub", "README", "section", "install.sub"},
		{"README.section.a.b.c.d", "README", "section", "a.b.c.d"},
		{"ta.db.build_task.task_001", "ta.db", "build_task", "task_001"},
		{"ta.db.build_task.t1.subtask", "ta.db", "build_task", "t1.subtask"},
		{"ta.db.build_task.a.b.c", "ta.db", "build_task", "a.b.c"},
		{"install.prereqs.section.title", "install.prereqs", "section", "title"},
		{"install.prereqs.section.title.sub", "install.prereqs", "section", "title.sub"},
	}
	for _, tc := range cases {
		addr, _, err := r.ParseAddress(tc.section)
		if err != nil {
			t.Errorf("ParseAddress(%q): unexpected error %v", tc.section, err)
			continue
		}
		if addr.FileRelPath != tc.wantRelPath || addr.Type != tc.wantType || addr.ID != tc.wantID {
			t.Errorf("ParseAddress(%q) = %+v, want FileRelPath=%q Type=%q ID=%q",
				tc.section, addr, tc.wantRelPath, tc.wantType, tc.wantID)
		}
		if got := addr.Canonical(); got != tc.section {
			t.Errorf("Canonical(%+v) = %q, want %q", addr, got, tc.section)
		}
	}
}

func TestParseAddressTooFewSegments(t *testing.T) {
	r := NewResolver("/proj", testRegistry())

	// Single-file: need <file-relpath>.<type>.<id> (3 segments). Just 2 errs.
	if _, _, err := r.ParseAddress("README.section"); err == nil {
		t.Error("expected error for README.section (too few)")
	}
	// Glob: need <file-relpath...>.<type>.<id> (4+ for ta.db.X.Y).
	if _, _, err := r.ParseAddress("ta.db.build_task"); err == nil {
		t.Error("expected error for ta.db.build_task (no id)")
	}
}

func TestParseAddressRejectsEmptySegments(t *testing.T) {
	r := NewResolver("/proj", testRegistry())

	cases := []string{
		".README.section.install",
		"README.section.install.",
		"README..section.install",
		"ta.db..build_task.task_001",
		"ta..db.build_task.task_001",
	}
	for _, s := range cases {
		if _, _, err := r.ParseAddress(s); err == nil {
			t.Errorf("ParseAddress(%q): expected error, got nil", s)
		} else if !errors.Is(err, ErrBadAddress) {
			t.Errorf("ParseAddress(%q): expected ErrBadAddress, got %v", s, err)
		}
	}
}

func TestParseAddressUnknownDB(t *testing.T) {
	// Use a registry that has only a non-collection mount so "unknown
	// file-relpath" actually surfaces. With a collection mount in the
	// registry, almost any well-formed address resolves under the
	// catch-all rule (Phase 9.2 PLAN §12.17.9).
	reg := schema.Registry{DBs: map[string]schema.DB{
		"plan_db": {
			Name:   "plan_db",
			Paths:  []string{"workflow/*/db"},
			Format: schema.FormatTOML,
			Types: map[string]schema.SectionType{
				"build_task": {Name: "build_task"},
			},
		},
	}}
	r := NewResolver("/proj", reg)

	_, _, err := r.ParseAddress("nope.section.x")
	if err == nil {
		t.Fatal("expected error for unknown file-relpath")
	}
	if !errors.Is(err, ErrUnknownDB) {
		t.Errorf("expected ErrUnknownDB, got %v", err)
	}
}

func TestParseAddressUnknownType(t *testing.T) {
	r := NewResolver("/proj", testRegistry())

	// README mount matches; but `nosuchtype` is not declared on readme.
	_, _, err := r.ParseAddress("README.nosuchtype.x")
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
	if !errors.Is(err, ErrUnknownType) {
		t.Errorf("expected ErrUnknownType, got %v", err)
	}
}

func TestParseAddressEmpty(t *testing.T) {
	r := NewResolver("/proj", testRegistry())
	if _, _, err := r.ParseAddress(""); err == nil {
		t.Fatal("expected error for empty address")
	}
}

func TestParseAddressHomeRelativeMount(t *testing.T) {
	// PLAN §12.17.9 Phase 9.2 lets a mount declare a `~/...` prefix.
	// resolver.expandMount expands `~/` against the user's home dir
	// before walking; the address parser must do the same so the
	// FilePath returned by ParseAddress (and consumed by ResolveRead /
	// ResolveWrite) lands on disk under $HOME instead of under the
	// project root with a literal `~` segment.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("os.UserHomeDir: %v", err)
	}

	reg := schema.Registry{DBs: map[string]schema.DB{
		"home_db": {
			Name:   "home_db",
			Paths:  []string{"~/.ta/projects/foo/db"},
			Format: schema.FormatTOML,
			Types: map[string]schema.SectionType{
				"task": {Name: "task"},
			},
		},
	}}
	r := NewResolver("/proj", reg)

	// Address grammar for non-collection mounts: file-relpath equals
	// the residual segs after the static prefix. splitMountSegments on
	// `~/.ta/projects/foo/db` (post home-expansion) yields static-prefix
	// `.ta/projects/foo/` and residual `[db]`, so the address is
	// `db.task.t1` — same shape as for the bare `["plans"]` single-file
	// mount.
	addr, db, err := r.ParseAddress("db.task.t1")
	if err != nil {
		t.Fatalf("ParseAddress: %v", err)
	}
	if db.Name != "home_db" {
		t.Errorf("db.Name = %q, want home_db", db.Name)
	}
	if addr.Type != "task" || addr.ID != "t1" {
		t.Errorf("addr = %+v", addr)
	}
	want := filepath.Join(home, ".ta", "projects", "foo", "db.toml")
	if addr.FilePath != want {
		t.Errorf("addr.FilePath = %q, want %q (must expand ~/ to $HOME, "+
			"not literal %q segment under project root)",
			addr.FilePath, want, "~")
	}
	// Also confirm the path does NOT contain a literal `~` segment
	// joined under /proj — that would corrupt the on-disk tree.
	if got := filepath.Join("/proj", "~", ".ta", "projects", "foo", "db.toml"); addr.FilePath == got {
		t.Errorf("addr.FilePath = %q is the unexpanded form (project-root/~/...)", addr.FilePath)
	}
}

func TestAddressCanonical(t *testing.T) {
	cases := []struct {
		addr Address
		want string
	}{
		{Address{FileRelPath: "README", Type: "section", ID: "installation"}, "README.section.installation"},
		{Address{FileRelPath: "ta.db", Type: "build_task", ID: "task_001"}, "ta.db.build_task.task_001"},
		{Address{FileRelPath: "install.prereqs", Type: "section", ID: "title"}, "install.prereqs.section.title"},
	}
	for _, tc := range cases {
		if got := tc.addr.Canonical(); got != tc.want {
			t.Errorf("Canonical() = %q, want %q", got, tc.want)
		}
	}
}
