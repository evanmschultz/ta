package db

import (
	"errors"
	"testing"

	"github.com/evanmschultz/ta/internal/schema"
)

// testRegistry builds a Phase 9.1-shaped registry that exercises all
// three legacy-shape branches via the IsSingleFile / IsLegacyDirectory /
// IsLegacyCollection heuristics:
//
//   - readme: Paths=["README.md"] → IsSingleFile (single .md entry).
//   - plan_db: Paths=["workflow"] → IsLegacyDirectory (no ext, no
//     trailing slash, no glob).
//   - docs: Paths=["docs/"] → IsLegacyCollection (trailing slash).
//
// Phase 9.2 rewrites every consumer of this registry against the new
// paths-glob grammar; this fixture is transitional.
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
			Paths:  []string{"workflow"},
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

func TestParseAddressSingleInstance(t *testing.T) {
	r := NewResolver("/proj", testRegistry())

	addr, db, err := r.ParseAddress("readme.section.installation")
	if err != nil {
		t.Fatalf("ParseAddress: %v", err)
	}
	if db.Name != "readme" {
		t.Errorf("db.Name = %q, want readme", db.Name)
	}
	if addr.DB != "readme" || addr.Instance != "" || addr.Type != "section" || addr.ID != "installation" {
		t.Errorf("addr = %+v", addr)
	}
}

func TestParseAddressDirPerInstance(t *testing.T) {
	r := NewResolver("/proj", testRegistry())

	addr, db, err := r.ParseAddress("plan_db.drop_1.build_task.task_001")
	if err != nil {
		t.Fatalf("ParseAddress: %v", err)
	}
	if db.Name != "plan_db" {
		t.Errorf("db.Name = %q, want plan_db", db.Name)
	}
	if addr.DB != "plan_db" || addr.Instance != "drop_1" || addr.Type != "build_task" || addr.ID != "task_001" {
		t.Errorf("addr = %+v", addr)
	}
}

func TestParseAddressFilePerInstance(t *testing.T) {
	r := NewResolver("/proj", testRegistry())

	addr, _, err := r.ParseAddress("docs.reference-api.section.endpoints")
	if err != nil {
		t.Fatalf("ParseAddress: %v", err)
	}
	if addr.Instance != "reference-api" || addr.Type != "section" || addr.ID != "endpoints" {
		t.Errorf("addr = %+v", addr)
	}
}

func TestParseAddressTooFewSegments(t *testing.T) {
	r := NewResolver("/proj", testRegistry())

	// Single-instance: need <db>.<type>.<id> (3 segments). Just 2 errs.
	if _, _, err := r.ParseAddress("readme.section"); err == nil {
		t.Error("expected error for readme.section (too few)")
	}
	// Multi-instance dir: need <db>.<instance>.<type>.<id> (4). 3 errs.
	if _, _, err := r.ParseAddress("plan_db.drop_1.build_task"); err == nil {
		t.Error("expected error for plan_db.drop_1.build_task (too few)")
	}
	// Multi-instance collection: same.
	if _, _, err := r.ParseAddress("docs.reference-api.section"); err == nil {
		t.Error("expected error for docs.reference-api.section (too few)")
	}
}

// TestParseAddressUniformDottedIDsAccepted locks in the V2-PLAN §2.9 /
// §11.D uniform grammar: <id-path> is 1+ dot-separated segments, joined
// with '.' into addr.ID. Single-instance accepts any 3+; multi-instance
// accepts any 4+. No "too many segments" reject on single-instance.
func TestParseAddressUniformDottedIDsAccepted(t *testing.T) {
	r := NewResolver("/proj", testRegistry())

	cases := []struct {
		section  string
		wantInst string
		wantType string
		wantID   string
	}{
		// Single-instance with dotted IDs.
		{"readme.section.install", "", "section", "install"},
		{"readme.section.install.sub", "", "section", "install.sub"},
		{"readme.section.a.b.c.d", "", "section", "a.b.c.d"},
		// Multi-instance with dotted IDs.
		{"plan_db.drop_1.build_task.task_001", "drop_1", "build_task", "task_001"},
		{"plan_db.drop_1.build_task.t1.subtask", "drop_1", "build_task", "t1.subtask"},
		{"plan_db.drop_1.build_task.a.b.c", "drop_1", "build_task", "a.b.c"},
		// File-per-instance with dotted IDs.
		{"docs.reference-api.section.endpoints", "reference-api", "section", "endpoints"},
		{"docs.reference-api.section.endpoints.sub", "reference-api", "section", "endpoints.sub"},
	}
	for _, tc := range cases {
		addr, _, err := r.ParseAddress(tc.section)
		if err != nil {
			t.Errorf("ParseAddress(%q): unexpected error %v", tc.section, err)
			continue
		}
		if addr.Instance != tc.wantInst || addr.Type != tc.wantType || addr.ID != tc.wantID {
			t.Errorf("ParseAddress(%q) = %+v, want Instance=%q Type=%q ID=%q",
				tc.section, addr, tc.wantInst, tc.wantType, tc.wantID)
		}
		// Round-trip through Canonical.
		if got := addr.Canonical(); got != tc.section {
			t.Errorf("Canonical(%+v) = %q, want %q", addr, got, tc.section)
		}
	}
}

func TestParseAddressWrongShapeMultiInstance(t *testing.T) {
	r := NewResolver("/proj", testRegistry())

	// Multi-instance dir with only 3 segments (looks like single-instance).
	_, _, err := r.ParseAddress("plan_db.build_task.task_001")
	if err == nil {
		t.Fatal("expected error for 3-segment address on multi-instance db")
	}
}

// TestParseAddressRejectsEmptySegments guards the "no leading/trailing
// or interior empty segment" rule that came in with the uniform grammar:
// strings.Split(".foo.bar", ".") returns ["", "foo", "bar"] and we must
// not silently accept that as the db "". Same for "a..b" and "a.b.".
func TestParseAddressRejectsEmptySegments(t *testing.T) {
	r := NewResolver("/proj", testRegistry())

	cases := []string{
		".readme.section.install",  // leading dot
		"readme.section.install.",  // trailing dot
		"readme..section.install",  // double dot
		"plan_db.drop_1..task_001", // double dot mid-address
		"plan_db..build_task.task_001",
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
	r := NewResolver("/proj", testRegistry())

	_, _, err := r.ParseAddress("nope.section.x")
	if err == nil {
		t.Fatal("expected error for unknown db")
	}
	if !errors.Is(err, ErrUnknownDB) {
		t.Errorf("expected ErrUnknownDB, got %v", err)
	}
}

func TestParseAddressUnknownType(t *testing.T) {
	r := NewResolver("/proj", testRegistry())

	_, _, err := r.ParseAddress("readme.nosuchtype.x")
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

func TestAddressCanonical(t *testing.T) {
	cases := []struct {
		addr Address
		want string
	}{
		{Address{DB: "readme", Type: "section", ID: "installation"}, "readme.section.installation"},
		{Address{DB: "plan_db", Instance: "drop_1", Type: "build_task", ID: "task_001"}, "plan_db.drop_1.build_task.task_001"},
		{Address{DB: "docs", Instance: "ref-api", Type: "section", ID: "endpoints"}, "docs.ref-api.section.endpoints"},
	}
	for _, tc := range cases {
		if got := tc.addr.Canonical(); got != tc.want {
			t.Errorf("Canonical() = %q, want %q", got, tc.want)
		}
	}
}
