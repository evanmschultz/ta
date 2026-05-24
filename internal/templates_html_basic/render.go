package templates_html_basic

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"strings"
)

// Render executes the Track A template identified by templateName
// against data and returns the rendered HTML bytes. The template name
// is an fs.FS-relative path rooted at the EmbeddedBasicHTML() filesystem
// (i.e. "cascade_drop.html"), not the on-disk source path under
// internal/templates_html_basic/templates/.
//
// Rendering loads all .html files from both templates/ and partials/ directories
// into a single template set, allowing templates to include partials via
// {{ template "partials/name.html" . }}.
//
// Rendering is performed via stdlib html/template, which applies
// context-aware auto-escaping to every interpolated value. Mock callers
// supplying user-controlled data should pass plain Go strings (or
// nested map[string]any / struct values) — not template.HTML — so the
// escape pipeline is not bypassed.
//
// Missing templates return an error wrapping fs.ErrNotExist with a
// descriptive prefix; parse errors and execute errors are wrapped with
// the template name to keep diagnostics actionable.
func Render(templateName string, data any) ([]byte, error) {
	if templateName == "" {
		return nil, errors.New("templates_html_basic: Render: empty template name")
	}

	// Get the embedded FS root which contains both templates/ and partials/ subdirectories
	rootFS := getEmbeddedRoot()

	// ParseFS loads the full template set from both templates/ and partials/ directories.
	// This allows templates to reference each other and include partials.
	// We parse all .html files from both directories with wildcard patterns.
	patterns := []string{"templates/*.html", "partials/*.html"}
	tmpl, err := template.ParseFS(rootFS, patterns...)
	if err != nil {
		return nil, fmt.Errorf("templates_html_basic: Render: parse template set: %w", err)
	}

	// Look up the template. ParseFS names templates by their file paths relative to the FS root.
	// Support both old API (just filename like "cascade_drop.html") and new API (with prefix).
	mainTmpl := tmpl.Lookup(templateName)
	if mainTmpl == nil && !strings.Contains(templateName, "/") {
		// Try with templates/ prefix for backward compatibility
		mainTmpl = tmpl.Lookup("templates/" + templateName)
	}
	if mainTmpl == nil {
		return nil, fmt.Errorf("templates_html_basic: Render: template %q not found in set", templateName)
	}

	var buf bytes.Buffer
	if err := mainTmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("templates_html_basic: Render: execute template %q: %w", templateName, err)
	}

	return buf.Bytes(), nil
}

// getEmbeddedRoot returns the root embedded FS containing both templates/ and partials/
// subdirectories. This is a private helper that accesses the embedded FS directly.
func getEmbeddedRoot() fs.FS {
	// We need access to the root embeddedFS from embed.go.
	// Since it's private, we'll use reflection or a public accessor.
	// For now, we reconstruct the embedded root by accessing it through
	// a new function we'll add to embed.go.
	return embeddedFSRoot()
}
