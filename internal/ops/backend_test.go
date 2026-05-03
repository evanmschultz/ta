package ops

import (
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/db"
	"github.com/evanmschultz/ta/internal/schema"
)

// TestBackendSectionPath_MDIncludesType locks in the F30 contract
// that backendSectionPath inserts the bareType BETWEEN file-relpath
// and bracket-key for FormatMD dbs. The md backend's relativeAddress
// strips qualifier segments left-to-right and anchors on the first
// segment matching a declared type-name (per V2-PLAN §5.3.2
// hierarchical addressing). Post-F10 the canonical id no longer
// carries the type, so ops must restore the type-anchored shape
// before dispatching to md.Backend Find / Emit / Splice. TOML keeps
// the bracket-as-id shape unchanged.
func TestBackendSectionPath_MDIncludesType(t *testing.T) {
	cases := []struct {
		name     string
		dbDecl   schema.DB
		resolved db.Resolved
		bareType string
		want     string
	}{
		{
			name:   "md inserts bareType between file-relpath and bracket-key",
			dbDecl: schema.DB{Format: schema.FormatMD},
			resolved: db.Resolved{
				FileRelPath: "foo.bar",
				BracketKey:  "baz",
			},
			bareType: "agent",
			want:     "foo.bar.agent.baz",
		},
		{
			name:   "md single-file mount also threads bareType",
			dbDecl: schema.DB{Format: schema.FormatMD},
			resolved: db.Resolved{
				FileRelPath:     "README",
				BracketKey:      "installation",
				SingleFileMount: true,
			},
			bareType: "section",
			want:     "README.section.installation",
		},
		{
			name:   "toml ignores bareType, returns bracket only for multi-file",
			dbDecl: schema.DB{Format: schema.FormatTOML},
			resolved: db.Resolved{
				FileRelPath: "ta.db",
				BracketKey:  "task_001",
			},
			bareType: "build_task",
			want:     "task_001",
		},
		{
			name:   "toml single-file returns canonical (bracket = id)",
			dbDecl: schema.DB{Format: schema.FormatTOML},
			resolved: db.Resolved{
				FileRelPath:     "plans",
				BracketKey:      "demo-1",
				SingleFileMount: true,
			},
			bareType: "task",
			want:     "plans.demo-1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := backendSectionPath(tc.dbDecl, tc.resolved, tc.bareType)
			if got != tc.want {
				t.Errorf("backendSectionPath(%s) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

// TestBackendSectionPath_MDEmptyBareType covers the degenerate case
// where ops fails to resolve a type before calling. The function
// returns just the file-relpath + bracket-key (no type segment) —
// md.Backend.relativeAddress will reject this with
// ErrNotDeclaredType, surfacing the missing-type problem at the
// backend boundary rather than producing a malformed path with a
// stray dot.
func TestBackendSectionPath_MDEmptyBareType(t *testing.T) {
	got := backendSectionPath(
		schema.DB{Format: schema.FormatMD},
		db.Resolved{FileRelPath: "foo.bar", BracketKey: "baz"},
		"",
	)
	if got != "foo.bar.baz" {
		t.Errorf("got %q, want %q", got, "foo.bar.baz")
	}
	// Sanity: no leading or trailing dot.
	if strings.HasPrefix(got, ".") || strings.HasSuffix(got, ".") {
		t.Errorf("section path leaks dot: %q", got)
	}
}
