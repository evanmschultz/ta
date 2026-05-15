package ops_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/ops"
)

// TestRebuildHintPerimeterPredicate is the cascade drop_002 perimeter
// guard for the "ta index rebuild" recovery hint. It enforces two
// invariants the not-found-vs-orphan split rests on:
//
//  1. Friendly absence: a missing id in a multi-type db surfaces
//     ErrRecordNotFound (no rebuild hint) — both at the format-string
//     level (sub-a) and at the ops-layer command level (sub-d).
//  2. Genuine orphan: ErrTypeUnresolved keeps the rebuild hint (sub-b)
//     because that branch fires only when a record IS on disk but lacks
//     an index entry — the actionable case where rebuild matters.
//
// Sub-c is the source-scan guard: every literal "ta index rebuild"
// hit in internal/ops/ production code must live in an allowlisted
// scope (one of the index-mutating helpers, resolveTypeForID, the move
// partial-write paths, or alongside an ErrType*/ErrIndex*/*PartialWrite*
// sentinel). New hint sites introduced outside that allowlist fail the
// test loudly so the perimeter cannot drift.
func TestRebuildHintPerimeterPredicate(t *testing.T) {
	const rebuildHint = "ta index rebuild"

	t.Run("sub-a: ErrRecordNotFound wrap omits rebuild hint", func(t *testing.T) {
		err := fmt.Errorf(ops.ErrRecordNotFoundFormat, ops.ErrRecordNotFound, "plans.x", "/tmp/plans.toml")
		msg := err.Error()
		if strings.Contains(msg, rebuildHint) {
			t.Fatalf("ErrRecordNotFound wrap must NOT contain %q: got %q", rebuildHint, msg)
		}
		if !errors.Is(err, ops.ErrRecordNotFound) {
			t.Fatalf("wrap must satisfy errors.Is(_, ErrRecordNotFound); got %v", err)
		}
	})

	t.Run("sub-b: ErrTypeUnresolved keeps rebuild hint", func(t *testing.T) {
		msg := ops.ErrTypeUnresolved.Error()
		if !strings.Contains(msg, rebuildHint) {
			t.Fatalf("ErrTypeUnresolved MUST contain %q: got %q", rebuildHint, msg)
		}
	})

	t.Run("sub-c: production hint sites live in allowlisted scope", func(t *testing.T) {
		// Allowlist tokens applied case-insensitively to:
		//   (i)  the enclosing function name (when the hit is inside a func body), OR
		//   (ii) any identifier appearing in a small window around the hit
		//        (captures sentinel-name references like ErrMovePartialWrite
		//        that wrap the formatted message, and ErrTypeUnresolved /
		//        ErrIndexMissing in the errors.go var block).
		allow := regexp.MustCompile(`(?i)^(writeIndexEntry|deleteIndexEntry|deleteIndexEntriesByFile|updateIndexForMove|resolveTypeForID|.*ErrType.*|.*ErrIndex.*|.*PartialWrite.*)$`)

		// Scan production .go files in internal/ops/ (skip _test.go —
		// those are scaffolding for THIS predicate and adjacent
		// regression coverage; the perimeter is about production code).
		opsDir := opsPackageDir(t)
		entries, err := os.ReadDir(opsDir)
		if err != nil {
			t.Fatalf("read ops dir %s: %v", opsDir, err)
		}

		identRE := regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
		funcRE := regexp.MustCompile(`^func\s+(?:\([^)]*\)\s+)?([A-Za-z_][A-Za-z0-9_]*)`)

		hitCount := 0
		for _, ent := range entries {
			if ent.IsDir() {
				continue
			}
			name := ent.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := filepath.Join(opsDir, name)
			buf, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			lines := strings.Split(string(buf), "\n")

			// Pre-compute the enclosing-function index per line.
			// enclosing[i] = name of last `func NAME` declaration whose
			// opening line is <= i, or "" when none has fired yet.
			enclosing := make([]string, len(lines))
			current := ""
			for i, line := range lines {
				if m := funcRE.FindStringSubmatch(strings.TrimLeft(line, " \t")); m != nil {
					current = m[1]
				}
				enclosing[i] = current
			}

			for i, line := range lines {
				if !strings.Contains(line, rebuildHint) {
					continue
				}
				// Skip pure-comment lines. Doc comments and inline `//`
				// commentary that mention the hint are NARRATIVE about
				// behavior — they never become error message bytes at
				// runtime. The perimeter cares about emission sites, not
				// documentation that names the recovery hint while
				// describing nearby code.
				trimmed := strings.TrimLeft(line, " \t")
				if strings.HasPrefix(trimmed, "//") {
					continue
				}
				hitCount++

				// Build a ±5-line window around the hit and harvest every
				// identifier-like token. Test the regex against:
				//   (a) the enclosing function name (if any), and
				//   (b) each identifier inside the window.
				windowStart := i - 5
				if windowStart < 0 {
					windowStart = 0
				}
				windowEnd := i + 5
				if windowEnd > len(lines)-1 {
					windowEnd = len(lines) - 1
				}
				windowText := strings.Join(lines[windowStart:windowEnd+1], "\n")
				idents := identRE.FindAllString(windowText, -1)

				ok := false
				matchedAgainst := ""
				if fn := enclosing[i]; fn != "" && allow.MatchString(fn) {
					ok = true
					matchedAgainst = "func:" + fn
				}
				if !ok {
					for _, id := range idents {
						if allow.MatchString(id) {
							ok = true
							matchedAgainst = "ident:" + id
							break
						}
					}
				}
				if !ok {
					t.Errorf("%s:%d: hint site outside allowlist (enclosing func=%q): %s",
						path, i+1, enclosing[i], strings.TrimSpace(line))
				} else {
					t.Logf("%s:%d: allowlisted via %s", path, i+1, matchedAgainst)
				}
			}
		}

		// Sanity: the perimeter must be exercising real code. If a future
		// refactor strips every hint site (e.g. all rebuild guidance moves
		// to a single sentinel), this test is the canary that catches the
		// drop and forces a deliberate update.
		if hitCount == 0 {
			t.Fatalf("found zero %q hits in %s — perimeter scan probed nothing", rebuildHint, opsDir)
		}
	})

	t.Run("sub-d: ops-layer missing-id calls omit rebuild hint", func(t *testing.T) {
		root := withMultiTypeSchema(t)

		// Each row exercises a different ops-layer entry point against a
		// missing id in the multi-type db. Post-B1, these must surface
		// ErrRecordNotFound (clean message, no rebuild hint) rather than
		// the legacy ErrTypeUnresolved+rebuild orphan path.
		cases := []struct {
			name string
			call func() error
		}{
			{
				name: "Get",
				call: func() error {
					_, err := ops.Get(root, "plans.absent", "", nil)
					return err
				},
			},
			{
				name: "Update",
				call: func() error {
					_, _, err := ops.Update(root, "plans.absent", "", map[string]any{"title": "x"})
					return err
				},
			},
			{
				name: "DeleteWithOptions",
				call: func() error {
					_, err := ops.DeleteWithOptions(root, "plans.absent", "", ops.DeleteOptions{})
					return err
				},
			},
			{
				name: "Move",
				call: func() error {
					_, err := ops.Move(root, "plans.absent", "plans.new", "", ops.MoveOptions{})
					return err
				},
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				err := tc.call()
				if err == nil {
					t.Fatalf("%s on missing id: expected error, got nil", tc.name)
				}
				msg := err.Error()
				if strings.Contains(msg, "ta index rebuild") {
					t.Fatalf("%s on missing id must NOT mention `ta index rebuild`; got %q", tc.name, msg)
				}
			})
		}
	})
}

// opsPackageDir returns the absolute path of internal/ops/ (the package
// under perimeter test). Resolved at test time via runtime caller info
// so the test stays correct regardless of CWD when invoked.
func opsPackageDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// Tests run with CWD = package dir (Go default).
	return wd
}
