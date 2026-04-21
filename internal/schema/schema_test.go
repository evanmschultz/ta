package schema

import "testing"

func TestRegistryLookup(t *testing.T) {
	reg := Registry{DBs: map[string]DB{
		"plans": {
			Name:   "plans",
			Types:  map[string]SectionType{"task": {Name: "task"}},
			Shape:  ShapeFile,
			Format: FormatTOML,
			Path:   "plans.toml",
		},
	}}

	cases := []struct {
		name    string
		path    string
		wantOK  bool
		wantKey string
	}{
		{"bare db", "plans", false, ""},
		{"db.type", "plans.task", true, "task"},
		{"db.type.id", "plans.task.task_001", true, "task"},
		{"unknown db", "note.standup", false, ""},
		{"unknown type", "plans.missing", false, ""},
		{"empty path", "", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := reg.Lookup(tc.path)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got.Name != tc.wantKey {
				t.Fatalf("name = %q, want %q", got.Name, tc.wantKey)
			}
		})
	}
}

func TestRegistryLookupZeroValue(t *testing.T) {
	var reg Registry
	if _, ok := reg.Lookup("plans.task.task_001"); ok {
		t.Fatal("zero-value Registry must return ok=false")
	}
}

func TestRegistryLookupDB(t *testing.T) {
	reg := Registry{DBs: map[string]DB{
		"plans": {Name: "plans"},
	}}
	if _, ok := reg.LookupDB("plans.task.task_001"); !ok {
		t.Fatal("expected plans lookup via first segment")
	}
	if _, ok := reg.LookupDB("missing.x"); ok {
		t.Fatal("missing db should not resolve")
	}
}

func TestRegistryOverrideReplaceSameName(t *testing.T) {
	base := Registry{DBs: map[string]DB{
		"plans": {Name: "plans", Description: "base"},
		"notes": {Name: "notes", Description: "base"},
	}}
	overlay := Registry{DBs: map[string]DB{
		"plans": {Name: "plans", Description: "overlay"},
	}}
	got := base.Override(overlay)
	if got.DBs["plans"].Description != "overlay" {
		t.Errorf("plans.description = %q, want overlay", got.DBs["plans"].Description)
	}
	if got.DBs["notes"].Description != "base" {
		t.Errorf("notes retention broken; got = %q", got.DBs["notes"].Description)
	}
}

func TestSplitFirstTwo(t *testing.T) {
	cases := []struct {
		in   string
		a, b string
		rest string
	}{
		{"", "", "", ""},
		{"db", "db", "", ""},
		{"db.type", "db", "type", ""},
		{"db.type.id", "db", "type", "id"},
		{"db.type.id.more", "db", "type", "id.more"},
	}
	for _, c := range cases {
		a, b, r := splitFirstTwo(c.in)
		if a != c.a || b != c.b || r != c.rest {
			t.Errorf("splitFirstTwo(%q) = (%q,%q,%q), want (%q,%q,%q)",
				c.in, a, b, r, c.a, c.b, c.rest)
		}
	}
}
