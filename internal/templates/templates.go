// Package templates manages the global schema template library at
// ~/.ta/. The directory is a pure template store — never read at
// runtime by the MCP server or data tools. Only `ta init` and
// `ta template *` touch it.
//
// Per V2-PLAN §14.2 the firewall is strict: templates imports stdlib +
// internal/schema + internal/fsatomic only. It does NOT import
// internal/config/Resolve or any internal/mcpsrv/* package. Runtime
// consumers never import this package.
//
// Public API:
//
//   - Root()           — resolves $HOME/.ta.
//   - List(root)       — sorted basenames of *.toml in root; missing root returns nil.
//   - Load(root, name) — returns raw bytes; validates via schema.LoadBytes and
//     surfaces parse errors with the file path.
//   - Save(root, name, bytes) — validates BEFORE write; atomic write on success.
//   - Delete(root, name) — removes a template; errors if missing.
//
// Callers pass root explicitly so tests can use t.TempDir(); production
// call sites use templates.Root().
package templates

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/evanmschultz/ta/internal/fsatomic"
	"github.com/evanmschultz/ta/internal/schema"
)

const templateExt = ".toml"

// rootFn resolves the default template root. Exposed as a package
// variable so tests can inject a tmpdir via SetRootForTest; production
// call sites go through Root() which calls this.
var rootFn = defaultRoot

// Root returns the template library directory, conventionally
// $HOME/.ta. Does not create the directory — callers decide whether a
// missing root is an error (List treats it as empty; Save creates it).
func Root() (string, error) { return rootFn() }

func defaultRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("templates: resolve home: %w", err)
	}
	return filepath.Join(home, ".ta"), nil
}

// SetRootForTest swaps the root-resolver for the duration of a test
// and returns a restore function. Only tests should call this.
func SetRootForTest(dir string) (restore func()) {
	prev := rootFn
	rootFn = func() (string, error) { return dir, nil }
	return func() { rootFn = prev }
}

// List returns the sorted template names under root. A template is any
// *.toml file whose basename does not start with a dot. Missing root
// returns (nil, nil) — not an error — so fresh installs of `ta` with
// no templates yet still answer `ta template list` quietly.
func List(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("templates: list %s: %w", root, err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		base, ok := strings.CutSuffix(name, templateExt)
		if !ok {
			continue
		}
		out = append(out, base)
	}
	sort.Strings(out)
	return out, nil
}

// ErrInvalidName is returned when a template name would escape the
// library root or name a hidden file. Covers empty strings, names with
// path separators, names containing `..` segments, leading-dot names
// (which List would hide), and any name whose `filepath.Clean` form
// differs from the input.
var ErrInvalidName = errors.New("templates: invalid name")

// validateName rejects template names that would escape the library
// root or collide with hidden-file semantics. Closes the QA
// falsification §12.16 HIGH finding: without this guard,
// `Save("foo", "../escape", data)` would resolve to `foo/../escape.toml`
// which filepath.Clean normalizes to a sibling of root, outside the
// library. Applied identically to Load, Save, Delete so every entry
// point shares the same contract.
func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: empty name", ErrInvalidName)
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("%w: %q contains a path separator", ErrInvalidName, name)
	}
	if strings.HasPrefix(name, ".") {
		// Covers ".", "..", ".hidden", and any dotfile-style name.
		// Separators are already rejected above, so a bare "." here
		// really is a dot-prefixed plain name (or `..`), not a
		// walked-path segment.
		return fmt.Errorf("%w: %q starts with a dot", ErrInvalidName, name)
	}
	if name != filepath.Clean(name) {
		// Belt-and-braces: catches anything stdlib considers
		// non-canonical that the other rules did not reject.
		return fmt.Errorf("%w: %q is not in canonical form", ErrInvalidName, name)
	}
	return nil
}

// Load reads the named template's raw bytes and validates them through
// schema.LoadBytes. Per V2-PLAN §14.6 a malformed template on disk must
// break loudly BEFORE any downstream consumer (`ta init`, preview) uses
// it — so the parse error is wrapped with the absolute file path.
func Load(root, name string) ([]byte, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}
	path := filepath.Join(root, name+templateExt)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("templates: read %s: %w", path, err)
	}
	if _, err := schema.LoadBytes(data); err != nil {
		return nil, fmt.Errorf("templates: validate %s: %w", path, err)
	}
	return data, nil
}

// Save validates bytes through schema.LoadBytes and only then writes
// them atomically to <root>/<name>.toml. Creates root if missing. Per
// V2-PLAN §14.6 the validation gate runs BEFORE the write; a malformed
// payload never touches disk and never clobbers a pre-existing valid
// template on the same name.
func Save(root, name string, data []byte) error {
	if err := validateName(name); err != nil {
		return err
	}
	if _, err := schema.LoadBytes(data); err != nil {
		return fmt.Errorf("templates: validate %q: %w", name, err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("templates: create root %s: %w", root, err)
	}
	path := filepath.Join(root, name+templateExt)
	if err := fsatomic.Write(path, data); err != nil {
		return fmt.Errorf("templates: write %s: %w", path, err)
	}
	return nil
}

// Delete removes the named template. A missing file is an error; the
// caller is expected to know which templates exist (via List) before
// issuing a delete.
func Delete(root, name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	path := filepath.Join(root, name+templateExt)
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("templates: remove %s: %w", path, err)
	}
	return nil
}
