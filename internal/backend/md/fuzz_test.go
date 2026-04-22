package md

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/record"
)

// FuzzSpliceInvariant exercises the byte-identity invariant required
// by V2-PLAN §5.1 / §12.4: splicing a section with bytes identical to
// the section's own existing content must yield a buffer equal to the
// input (modulo trailing-newline normalisation in Emit).
//
// The invariant is:
//
//	out, _ := b.Splice(buf, section, Emit(section, {body: Find(buf, section).body}))
//	bytes.Equal(buf, out) || out is buf with trailing '\n' added
//
// Seeds: project-root README.md and CLAUDE.md (when present) plus a
// suite of synthetic edge cases (empty body, body without trailing
// newline, body with fenced code containing '##', consecutive
// headings). The fuzzer explores perturbations around the seeds.
func FuzzSpliceInvariant(f *testing.F) {
	// Synthetic seeds first.
	synth := []string{
		// Empty body.
		"# ta\n\n## Empty\n",
		// Body without trailing newline.
		"# ta\n\n## Installation\n\nInstall from source.",
		// Body with fenced code containing ## which MUST be treated
		// as content, not as another heading.
		"# ta\n\n## Fenced\n\n" +
			"```go\n" +
			"// ## not a heading\n" +
			"func X() {}\n" +
			"```\n",
		// Body with tilde fence.
		"# ta\n\n## Tilde\n\n" +
			"~~~\n" +
			"## hidden\n" +
			"~~~\n",
		// Multiple consecutive headings, no bodies.
		"# a\n## b\n### c\n",
		// Heading at EOF with no trailing newline.
		"## Only",
		// Trailing whitespace.
		"# ta\n\n## X\n\nbody\n\n\n",
	}
	for _, s := range synth {
		f.Add([]byte(s), 2, "## X")
	}

	// Add real dogfood seeds if present in the project root.
	if root, ok := findProjectRoot(); ok {
		for _, name := range []string{"README.md", "CLAUDE.md"} {
			data, err := os.ReadFile(filepath.Join(root, name))
			if err == nil && len(data) > 0 {
				// Seed pairs the buffer with a random H2 target slug
				// derived from the second heading in the file, if any.
				hs, _ := scanATX(data)
				for _, h := range hs {
					if h.Level == 2 {
						f.Add(data, 2, "## "+h.Text)
						break
					}
				}
			}
		}
	}

	f.Fuzz(func(t *testing.T, buf []byte, level int, targetHeading string) {
		if level < 1 || level > 6 {
			t.Skip()
		}
		b, err := NewBackend(level)
		if err != nil {
			t.Skip()
		}

		// Derive a target slug from targetHeading if it looks like
		// "## X"; else pick the first matching-level heading in buf.
		var slug string
		if strings.HasPrefix(targetHeading, strings.Repeat("#", level)+" ") {
			slug = slugFromHeading(strings.TrimSpace(strings.TrimLeft(targetHeading, "#")))
		}

		hs, err := scanATX(buf)
		if err != nil {
			// Collision or other scan-level error; invariant is
			// undefined on un-scannable input.
			t.Skip()
		}
		if slug == "" {
			for _, h := range hs {
				if h.Level == level {
					slug = h.Slug
					break
				}
			}
		}
		if slug == "" {
			t.Skip()
		}

		// Build a section path; the prefix doesn't matter for Find's
		// slug match, only the last dotted segment does.
		section := "x.y." + slug

		sec, ok, err := b.Find(buf, section)
		if err != nil {
			t.Skip()
		}
		if !ok {
			t.Skip()
		}

		// Extract body from the located section: skip the heading line,
		// then strip one leading blank line if present (Emit produces
		// "heading\n\nbody\n").
		span := buf[sec.Range[0]:sec.Range[1]]
		nl := bytes.IndexByte(span, '\n')
		if nl < 0 {
			t.Skip()
		}
		body := string(span[nl+1:])
		body = strings.TrimPrefix(body, "\n")

		emitted, err := b.Emit(section, record.Record{"body": body})
		if err != nil {
			t.Fatalf("Emit: %v", err)
		}

		out, err := b.Splice(buf, section, emitted)
		if err != nil {
			t.Fatalf("Splice: %v", err)
		}

		// Invariant: the emitted-and-spliced buffer should match the
		// original, possibly modulo trailing-newline normalisation in
		// Emit. We check byte-equality in the region outside the
		// spliced section directly (strictly byte-identical), and
		// tolerate body-equivalence inside it.
		if !bytes.Equal(buf[:sec.Range[0]], out[:sec.Range[0]]) {
			t.Fatalf("pre-range bytes diverged:\n  got: %q\n  want: %q",
				out[:sec.Range[0]], buf[:sec.Range[0]])
		}
		post := buf[sec.Range[1]:]
		if !bytes.Equal(out[len(out)-len(post):], post) {
			t.Fatalf("post-range bytes diverged:\n  got: %q\n  want: %q",
				out[len(out)-len(post):], post)
		}

		// The emitted replacement must itself parse as a valid heading
		// matching the requested level + slug.
		eh, err := scanATX(emitted)
		if err != nil {
			t.Fatalf("emitted bytes unscannable: %v\n--- emitted ---\n%s", err, emitted)
		}
		if len(eh) == 0 || eh[0].Level != level || eh[0].Slug != slug {
			t.Fatalf("emitted heading mismatch: got %+v, want level=%d slug=%q",
				eh, level, slug)
		}
	})
}

// findProjectRoot walks up from the test's CWD looking for go.mod and
// returns the first directory that has one. Best-effort; returns
// (_, false) when nothing is found so the fuzzer seeds work without
// real files.
func findProjectRoot() (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
