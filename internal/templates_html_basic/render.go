package templates_html_basic

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
)

// Render executes the Track A template identified by templateName
// against data and returns the rendered HTML bytes. The template name
// is an fs.FS-relative path rooted at the EmbeddedBasicHTML() filesystem
// (i.e. "sample/cascade_drop.html"), not the on-disk source path under
// internal/templates_html_basic/templates/.
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

	fsys := EmbeddedBasicHTML()

	body, err := fs.ReadFile(fsys, templateName)
	if err != nil {
		// fs.ReadFile wraps the underlying error; surface it with the
		// template name so callers see WHICH lookup failed without
		// having to inspect the wrapped fs.PathError manually.
		return nil, fmt.Errorf("templates_html_basic: Render: read template %q: %w", templateName, err)
	}

	tmpl, err := template.New(templateName).Parse(string(body))
	if err != nil {
		return nil, fmt.Errorf("templates_html_basic: Render: parse template %q: %w", templateName, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("templates_html_basic: Render: execute template %q: %w", templateName, err)
	}

	return buf.Bytes(), nil
}
