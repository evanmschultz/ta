package schema

import "testing"

func TestRegistryLookup(t *testing.T) {
	reg := Registry{Types: map[string]SectionType{
		"task": {Name: "task"},
	}}

	cases := []struct {
		name    string
		path    string
		wantOK  bool
		wantKey string
	}{
		{"bare segment", "task", true, "task"},
		{"nested path", "task.task_001", true, "task"},
		{"deep path", "task.group.item", true, "task"},
		{"unknown type", "note.standup", false, ""},
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
	if _, ok := reg.Lookup("task.task_001"); ok {
		t.Fatal("zero-value Registry must return ok=false")
	}
}
