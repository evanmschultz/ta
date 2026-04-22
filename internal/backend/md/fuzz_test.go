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
// by V2-PLAN §5.1 / §5.3.2 / §12.4: splicing a declared section with
// bytes identical to the section's own existing content must yield a
// buffer whose bytes outside the declared range are byte-identical to
// the input.
//
// Seeds: project-root README.md and CLAUDE.md (when present) plus a
// suite of synthetic edge cases (empty body, body without trailing
// newline, body with fenced code containing `##`, consecutive
// headings). One seed exercises the §5.3.2 worked example — H3
// subheadings inside an H2 body that must survive the splice as
// absorbed content.
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
		// §5.3.2 worked example — H3 inside H2's body that must
		// survive splice-of-H2-body per the schema-driven rule. When
		// the caller replaces "## Installation" with an equivalent
		// body (including the H3s absorbed intact), the bytes outside
		// the H2's [header, next-declared-start) range must match the
		// input.
		"# ta\n\n" +
			"## Installation\n\n" +
			"Install from source:\n\n" +
			"    mage install\n\n" +
			"### Prerequisites\n\n" +
			"A Go toolchain.\n\n" +
			"### Troubleshooting\n\n" +
			"If mage install fails, ...\n\n" +
			"## MCP config\n\n" +
			"cfg\n",
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
				hs, _ := scanATX(data, map[int]struct{}{1: {}, 2: {}})
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
		// Build a Backend that declares H1 "title" and H<level>
		// "section" (or just H<level> when level == 1). This mirrors
		// the §5.3.2 model: at least one level declared, others are
		// content. If level == 1 the sole declared type is title.
		var types []record.DeclaredType
		if level == 1 {
			types = []record.DeclaredType{{Name: "title", Heading: 1}}
		} else {
			types = []record.DeclaredType{
				{Name: "title", Heading: 1},
				{Name: "section", Heading: level},
			}
		}
		b, err := NewBackend(types)
		if err != nil {
			t.Skip()
		}

		declaredLevels := map[int]struct{}{}
		for _, tp := range types {
			declaredLevels[tp.Heading] = struct{}{}
		}

		// Derive a target slug from targetHeading if it looks like
		// "## X"; else pick the first matching-level declared heading
		// in buf.
		var slug string
		if strings.HasPrefix(targetHeading, strings.Repeat("#", level)+" ") {
			slug = slugFromHeading(strings.TrimSpace(strings.TrimLeft(targetHeading, "#")))
		}

		hs, err := scanATX(buf, declaredLevels)
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

		// Build a section path. For level == 1 we target title; for
		// others section. The address type-name must match a declared
		// type so Emit can resolve the level.
		var section string
		if level == 1 {
			section = "x.title." + slug
		} else {
			section = "x.section." + slug
		}

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
		// original on bytes outside the spliced section.
		if !bytes.Equal(buf[:sec.Range[0]], out[:sec.Range[0]]) {
			t.Fatalf("pre-range bytes diverged:\n  got: %q\n  want: %q",
				out[:sec.Range[0]], buf[:sec.Range[0]])
		}
		post := buf[sec.Range[1]:]
		if !bytes.Equal(out[len(out)-len(post):], post) {
			t.Fatalf("post-range bytes diverged:\n  got: %q\n  want: %q",
				out[len(out)-len(post):], post)
		}

		// The emitted replacement must itself parse as a valid
		// declared heading matching the requested level + slug.
		eh, err := scanATX(emitted, declaredLevels)
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
