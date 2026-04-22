package db

import (
	"errors"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/schema"
)

func testRegistry() schema.Registry {
	return schema.Registry{DBs: map[string]schema.DB{
		"readme": {
			Name:   "readme",
			Shape:  schema.ShapeFile,
			Format: schema.FormatMD,
			Path:   "README.md",
			Types: map[string]schema.SectionType{
				"title":   {Name: "title", Heading: 1},
				"section": {Name: "section", Heading: 2},
			},
		},
		"plan_db": {
			Name:   "plan_db",
			Shape:  schema.ShapeDirectory,
			Format: schema.FormatTOML,
			Path:   "workflow",
			Types: map[string]schema.SectionType{
				"build_task": {Name: "build_task"},
				"qa_task":    {Name: "qa_task"},
			},
		},
		"docs": {
			Name:   "docs",
			Shape:  schema.ShapeCollection,
			Format: schema.FormatMD,
			Path:   "docs",
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

func TestParseAddressTooManySegmentsSingleInstance(t *testing.T) {
	r := NewResolver("/proj", testRegistry())

	// Single-instance with 4 segments must fail loudly.
	_, _, err := r.ParseAddress("readme.drop_1.section.installation")
	if err == nil {
		t.Fatal("expected error for 4-segment address on single-instance db")
	}
	if !strings.Contains(err.Error(), "readme") {
		t.Errorf("error should mention db name: %v", err)
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
