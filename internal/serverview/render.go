// render.go — Renderer adapts the serverview loaders to the
// consumer-side render interfaces defined in internal/server. One
// Renderer satisfies all four (ViewRenderer, CascadeDetailRenderer,
// DocsRenderer, SearchRenderer). The adapter loads live `.ta/` state
// via the matching Load* function and renders the result through
// internal/templates_html_basic, writing HTML to the passed-in
// http.ResponseWriter.

package serverview

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"

	"github.com/evanmschultz/ta/internal/ops"
	"github.com/evanmschultz/ta/internal/server"
	"github.com/evanmschultz/ta/internal/templates_html_basic"
)

// Renderer wires the four server-side render interfaces to the
// project-rooted serverview loaders. Construct once per project at
// boot time via NewRenderer(projectPath); pass it to each
// server.With*Renderer setter.
type Renderer struct {
	projectPath string
}

// NewRenderer returns a Renderer bound to projectPath. All Render*
// methods read live `.ta/` state under that path.
func NewRenderer(projectPath string) *Renderer {
	return &Renderer{projectPath: projectPath}
}

// RenderCascadeTree implements server.ViewRenderer. The cascade tree
// index page has no dedicated Track A template; it renders inline as a
// minimal list of records linking to per-record detail pages.
func (r *Renderer) RenderCascadeTree(_ context.Context, w http.ResponseWriter, _ *http.Request) error {
	nodes, err := LoadCascadeTree(r.projectPath)
	if err != nil {
		return fmt.Errorf("render cascade tree: %w", err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return writeCascadeIndex(w, nodes)
}

// RenderCascadeDetail implements server.CascadeDetailRenderer. The
// {id} path parameter is resolved to a single record via LoadDetail,
// then rendered through the matching Track A template. Missing records
// surface as server.NotFoundError so the HTTP handler returns 404.
func (r *Renderer) RenderCascadeDetail(_ context.Context, w http.ResponseWriter, req *http.Request) error {
	id := req.PathValue("id")
	if id == "" {
		return &server.NotFoundError{Message: "cascade detail: missing id path parameter"}
	}
	res, err := LoadDetail(r.projectPath, id)
	if err != nil {
		if isRecordNotFound(err) {
			return &server.NotFoundError{Message: err.Error()}
		}
		return fmt.Errorf("render cascade detail %q: %w", id, err)
	}
	out, err := templates_html_basic.Render(res.TemplateName, res.Fields)
	if err != nil {
		return fmt.Errorf("render cascade detail %q: %w", id, err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err = w.Write(out)
	return err
}

// RenderRoadmap implements server.DocsRenderer. Lists every
// roadmap.version record under the project, sorts by id, and renders
// each through the roadmap_version.html template. Output is a single
// concatenated HTML document with each version rendered in turn.
func (r *Renderer) RenderRoadmap(_ context.Context, w http.ResponseWriter, _ *http.Request) error {
	ids, err := ops.ListSections(r.projectPath, "roadmap.version", 0, true)
	if err != nil {
		return fmt.Errorf("render roadmap: %w", err)
	}
	sort.Strings(ids)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := io.WriteString(w, `<!DOCTYPE html><html><body>`); err != nil {
		return err
	}
	for _, id := range ids {
		res, loadErr := LoadRoadmapVersion(r.projectPath, id)
		if loadErr != nil {
			continue
		}
		out, renderErr := templates_html_basic.Render(res.TemplateName, res.Fields)
		if renderErr != nil {
			return fmt.Errorf("render roadmap version %q: %w", id, renderErr)
		}
		if _, err := w.Write(out); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(w, `</body></html>`); err != nil {
		return err
	}
	return nil
}

// RenderSchema implements server.DocsRenderer. Loads the schema view
// from .ta/schema.toml via LoadSchema and renders it through
// schema_browser.html.
//
// The schema_browser.html template accesses template fields via lowercase
// keys ({{ .scopes }}, {{ .types }}, {{ .fields }}) because Go html/template
// uses exact field-name match for structs. SchemaLoaderResult's typed
// Scopes/Types/Fields fields therefore do not resolve. RenderSchema
// converts the typed result to a map[string]any with lowercase keys
// matching the template's expectations.
func (r *Renderer) RenderSchema(_ context.Context, w http.ResponseWriter, _ *http.Request) error {
	res, err := LoadSchema(r.projectPath)
	if err != nil {
		return fmt.Errorf("render schema: %w", err)
	}
	data := map[string]any{"scopes": scopeViewsToMaps(res.Scopes)}
	out, err := templates_html_basic.Render(res.TemplateName, data)
	if err != nil {
		return fmt.Errorf("render schema: %w", err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err = w.Write(out)
	return err
}

func scopeViewsToMaps(scopes []ScopeView) []map[string]any {
	out := make([]map[string]any, len(scopes))
	for i, s := range scopes {
		out[i] = map[string]any{
			"name":        s.Name,
			"description": s.Description,
			"types":       typeViewsToMaps(s.Types),
		}
	}
	return out
}

func typeViewsToMaps(types []TypeView) []map[string]any {
	out := make([]map[string]any, len(types))
	for i, t := range types {
		out[i] = map[string]any{
			"name":        t.Name,
			"extends":     t.Extends,
			"description": t.Description,
			"fields":      fieldViewsToMaps(t.Fields),
		}
	}
	return out
}

func fieldViewsToMaps(fields []FieldView) []map[string]any {
	out := make([]map[string]any, len(fields))
	for i, f := range fields {
		out[i] = map[string]any{
			"name":        f.Name,
			"type":        f.Type,
			"required":    f.Required,
			"default":     f.Default,
			"enum":        f.Enum,
			"description": f.Description,
		}
	}
	return out
}

// RenderSearch implements server.SearchRenderer. Empty query surfaces a
// 400 Bad Request inline (without bubbling up an error so the route
// handler does not also write a 500). Non-empty queries run through
// LoadSearch and render via search_results.html.
func (r *Renderer) RenderSearch(_ context.Context, w http.ResponseWriter, _ *http.Request, query string) error {
	if query == "" {
		http.Error(w, "missing required query parameter q", http.StatusBadRequest)
		return nil
	}
	res, err := LoadSearch(r.projectPath, query)
	if err != nil {
		return fmt.Errorf("render search %q: %w", query, err)
	}
	out, err := templates_html_basic.Render(res.TemplateName, res)
	if err != nil {
		return fmt.Errorf("render search %q: %w", query, err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err = w.Write(out)
	return err
}

// writeCascadeIndex writes a minimal inline cascade tree index page.
// Each node renders as one anchored <li> linking to /cascade/<id>.
func writeCascadeIndex(w io.Writer, nodes []CascadeNode) error {
	if _, err := io.WriteString(w, `<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><title>Cascade browser</title></head><body><h1>Cascade browser</h1><ul>`); err != nil {
		return err
	}
	for _, n := range nodes {
		line := fmt.Sprintf(`<li><a href="/cascade/%s">%s</a> — role:%s state:%s</li>`, n.ID, n.Title, n.Role, n.State)
		if _, err := io.WriteString(w, line); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(w, `</ul></body></html>`); err != nil {
		return err
	}
	return nil
}

// isRecordNotFound is a best-effort check that the loader's error came
// from ops.Get on a missing record. The underlying ops sentinel is
// ops.ErrRecordNotFound (wrapped); LoadDetail wraps it further with the
// id and other context, so errors.Is still unwraps to the sentinel.
func isRecordNotFound(err error) bool {
	return err != nil && errors.Is(err, ops.ErrRecordNotFound)
}
