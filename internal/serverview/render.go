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
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strings"

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
// index page loads the full cascade graph (nodes + edges), renders it as
// an SVG visualization, and displays it through the cascade_index.html template
// with sidebar navigation and breadcrumb metadata.
func (r *Renderer) RenderCascadeTree(_ context.Context, w http.ResponseWriter, req *http.Request) error {
	graph, err := LoadCascadeGraph(r.projectPath)
	if err != nil {
		return fmt.Errorf("render cascade tree: load graph: %w", err)
	}

	// The index SVG shows drops + their direct planner children so the
	// hierarchy contract (drop -> planner edges) is visible without
	// dragging in the full ~150-node graph (droplets + QA twins live
	// behind each drop's detail page). With one planner per drop the
	// width stays bounded and the depth-2 tree is scannable.
	indexNodeIDs := make(map[string]struct{}, 32)
	for _, n := range graph.Nodes {
		if n.Type == "drop" {
			indexNodeIDs[n.ID] = struct{}{}
		}
	}
	for _, n := range graph.Nodes {
		if !strings.HasPrefix(n.ID, "drop_") {
			continue
		}
		if !strings.Contains(n.ID, ".drop.planner_l1_") && !strings.Contains(n.ID, ".drop.planner_l1.") {
			continue
		}
		// Skip QA twin records (their ids carry the parent planner prefix
		// plus a -plan-qa-*/-qa-* suffix, so the planner_l1_ substring
		// match also pulls them in unless filtered explicitly).
		if strings.HasSuffix(n.ID, "-plan-qa-proof") ||
			strings.HasSuffix(n.ID, "-plan-qa-falsification") ||
			strings.HasSuffix(n.ID, "-qa-proof") ||
			strings.HasSuffix(n.ID, "-qa-falsification") {
			continue
		}
		indexNodeIDs[n.ID] = struct{}{}
	}
	indexGraph := CascadeGraph{
		Nodes: make([]CascadeNode, 0, len(indexNodeIDs)),
		Edges: make([]Edge, 0, len(indexNodeIDs)),
	}
	for _, n := range graph.Nodes {
		if _, ok := indexNodeIDs[n.ID]; ok {
			indexGraph.Nodes = append(indexGraph.Nodes, n)
		}
	}
	for _, e := range graph.Edges {
		_, srcOk := indexNodeIDs[e.SourceID]
		_, dstOk := indexNodeIDs[e.TargetID]
		if srcOk && dstOk {
			indexGraph.Edges = append(indexGraph.Edges, e)
		}
	}

	svg, err := RenderCascadeSVG(indexGraph)
	if err != nil {
		return fmt.Errorf("render cascade tree: render svg: %w", err)
	}

	pageContext := NewPageContextForRoute(req.URL.Path, "Cascade browser")

	data := map[string]any{
		"PageContext": pageContext,
		"SVG":         svg,
		"Nodes":       indexGraph.Nodes,
	}

	out, err := templates_html_basic.Render("cascade_index.html", data)
	if err != nil {
		return fmt.Errorf("render cascade tree: render template: %w", err)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err = w.Write(out)
	return err
}

// RenderCascadeDetail implements server.CascadeDetailRenderer. The
// {id} path parameter is resolved to a single record via LoadDetail,
// then rendered through the matching Track A template with shared chrome
// (PageContext + sidebar navigation). Missing records surface as
// server.NotFoundError so the HTTP handler returns 404.
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

	// Extract the record's title for use in page context.
	// Fall back to a generic title if the record has no title field.
	recordTitle := "Cascade detail"
	if title, ok := res.Fields["title"].(string); ok && title != "" {
		recordTitle = title
	}

	// Build PageContext for the cascade detail page.
	pageContext := NewPageContextForRoute(req.URL.Path, recordTitle)

	// Merge the cascade record's fields with PageContext for template rendering.
	// The detail templates now expect both the record fields AND the PageContext.
	data := map[string]any{
		"PageContext": pageContext,
	}
	// Copy all record fields into the data map for template consumption.
	for k, v := range res.Fields {
		data[k] = v
	}

	out, err := templates_html_basic.Render(res.TemplateName, data)
	if err != nil {
		return fmt.Errorf("render cascade detail %q: %w", id, err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err = w.Write(out)
	return err
}

// RenderRoadmap implements server.DocsRenderer. Lists every
// roadmap.version record under the project, sorts by id, and renders
// each as an HTML fragment through the roadmap_version.html template.
// Output is a single shared-chrome document with each version rendered
// as a fragment inside roadmap.html, with sidebar navigation and breadcrumb.
func (r *Renderer) RenderRoadmap(_ context.Context, w http.ResponseWriter, req *http.Request) error {
	// Load all roadmap.version record ids.
	ids, err := ops.ListSections(r.projectPath, "roadmap.version", 0, true)
	if err != nil {
		return fmt.Errorf("render roadmap: %w", err)
	}
	sort.Strings(ids)

	// Pre-render each version as a fragment. RenderedFragment is typed
	// template.HTML so the outer roadmap.html template embeds the bytes
	// as markup instead of HTML-escaping them to plaintext.
	type VersionFragment struct {
		RenderedFragment template.HTML
	}
	fragments := []VersionFragment{}
	for _, id := range ids {
		res, loadErr := LoadRoadmapVersion(r.projectPath, id)
		if loadErr != nil {
			continue
		}
		fragmentOut, renderErr := templates_html_basic.Render(res.TemplateName, res.Fields)
		if renderErr != nil {
			return fmt.Errorf("render roadmap version %q: %w", id, renderErr)
		}
		fragments = append(fragments, VersionFragment{
			RenderedFragment: template.HTML(fragmentOut),
		})
	}

	// Build PageContext for the roadmap route.
	pageContext := NewPageContextForRoute(req.URL.Path, "Roadmap")

	// Prepare template data with page context and version fragments.
	data := map[string]any{
		"PageContext": pageContext,
		"Versions":    fragments,
	}

	out, err := templates_html_basic.Render("roadmap.html", data)
	if err != nil {
		return fmt.Errorf("render roadmap: render template: %w", err)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err = w.Write(out)
	return err
}

// RenderSchema implements server.DocsRenderer. Loads the schema view
// from .ta/schema.toml via LoadSchema and renders it through
// schema_browser.html with shared chrome (PageContext + sidebar).
//
// The schema_browser.html template accesses template fields via lowercase
// keys ({{ .scopes }}) and injects PageContext for shared sidebar navigation.
// RenderSchema converts the typed result to a map[string]any with lowercase
// keys matching the template's expectations.
func (r *Renderer) RenderSchema(_ context.Context, w http.ResponseWriter, req *http.Request) error {
	res, err := LoadSchema(r.projectPath)
	if err != nil {
		return fmt.Errorf("render schema: %w", err)
	}

	// Build PageContext for the schema route.
	pageContext := NewPageContextForRoute(req.URL.Path, "Schema browser")

	// Prepare template data with page context and scopes.
	data := map[string]any{
		"PageContext": pageContext,
		"scopes":      scopeViewsToMaps(res.Scopes),
	}
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

// RenderSearch implements server.SearchRenderer. Empty query renders an
// empty-state notice via search_results.html with shared chrome and HTTP 400
// status. Non-empty queries run through LoadSearch and render results.
func (r *Renderer) RenderSearch(_ context.Context, w http.ResponseWriter, req *http.Request, query string) error {
	// Build PageContext for the search route.
	pageContext := NewPageContextForRoute(req.URL.Path, "Search")

	// If query is empty, render an empty-state notice with 400 status.
	if query == "" {
		data := map[string]any{
			"PageContext": pageContext,
			"Query":       "",
			"Results":     nil,
		}
		out, err := templates_html_basic.Render("search_results.html", data)
		if err != nil {
			return fmt.Errorf("render search (empty): %w", err)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		_, err = w.Write(out)
		return err
	}

	// Non-empty query: load and render results.
	res, err := LoadSearch(r.projectPath, query)
	if err != nil {
		return fmt.Errorf("render search %q: %w", query, err)
	}

	// Inject PageContext into the search results data.
	data := map[string]any{
		"PageContext": pageContext,
		"Query":       query,
		"Results":     res.Results,
	}
	out, err := templates_html_basic.Render("search_results.html", data)
	if err != nil {
		return fmt.Errorf("render search %q: %w", query, err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err = w.Write(out)
	return err
}

// RenderNotFound renders a 404 page for a missing cascade record via the
// shared not_found.html template with PageContext and the missing ID.
// The response is styled with shared chrome (sidebar, base.css) and sets
// HTTP 404 status code.
func (r *Renderer) RenderNotFound(w http.ResponseWriter, req *http.Request, missingID string) error {
	// Build PageContext for the not-found page (cascade scope).
	pageContext := NewPageContextForRoute(req.URL.Path, "Not Found")

	// Prepare template data with page context and missing ID.
	data := map[string]any{
		"PageContext": pageContext,
		"MissingID":   missingID,
	}

	out, err := templates_html_basic.Render("not_found.html", data)
	if err != nil {
		return fmt.Errorf("render not found: %w", err)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, err = w.Write(out)
	return err
}

// isRecordNotFound is a best-effort check that the loader's error came
// from a missing record. Catches both record-level (ops.ErrRecordNotFound,
// from the index) and file-level (ops.ErrFileNotFound, from the resolver
// trying a backend's file pattern that does not exist) sentinels — both
// surface for unknown ids depending on which lookup path fails first.
func isRecordNotFound(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ops.ErrRecordNotFound) || errors.Is(err, ops.ErrFileNotFound)
}

// RenderFlow implements server.FlowRenderer. The /flow page is the shared
// chrome shell plus a flowchart-container div that a small vanilla JS
// island wires up to /api/cascade/graph.json. The flowchart layout and
// pan/zoom/expand interactions live entirely in the client; this method
// only serves the page shell.
func (r *Renderer) RenderFlow(_ context.Context, w http.ResponseWriter, req *http.Request) error {
	pageContext := NewPageContextForRoute(req.URL.Path, "Flow")
	data := map[string]any{"PageContext": pageContext}
	out, err := templates_html_basic.Render("flow.html", data)
	if err != nil {
		return fmt.Errorf("render flow: %w", err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err = w.Write(out)
	return err
}

// KanbanColumn is one of the four state columns on the /kanban view.
type KanbanColumn struct {
	State string
	Label string
	Cards []KanbanCard
}

// KanbanCard is a single record's card in a kanban column.
type KanbanCard struct {
	ID    string
	Title string
	Role  string
	Type  string
}

// RenderKanban implements server.KanbanRenderer. The /kanban page groups
// every cascade record by its `state` field into 4 columns. Card content
// links to the record's detail page. Pure server-rendered, zero JS.
func (r *Renderer) RenderKanban(_ context.Context, w http.ResponseWriter, req *http.Request) error {
	graph, err := LoadCascadeGraph(r.projectPath)
	if err != nil {
		return fmt.Errorf("render kanban: load graph: %w", err)
	}

	columns := []KanbanColumn{
		{State: "todo", Label: "Todo"},
		{State: "in_progress", Label: "In Progress"},
		{State: "complete", Label: "Complete"},
		{State: "failed", Label: "Failed"},
	}
	columnIndex := map[string]int{
		"todo":        0,
		"in_progress": 1,
		"complete":    2,
		"failed":      3,
	}
	for _, n := range graph.Nodes {
		idx, ok := columnIndex[n.State]
		if !ok {
			continue
		}
		columns[idx].Cards = append(columns[idx].Cards, KanbanCard{
			ID:    n.ID,
			Title: n.Title,
			Role:  n.Role,
			Type:  n.Type,
		})
	}

	pageContext := NewPageContextForRoute(req.URL.Path, "Kanban")
	data := map[string]any{
		"PageContext": pageContext,
		"Columns":     columns,
	}
	out, err := templates_html_basic.Render("kanban.html", data)
	if err != nil {
		return fmt.Errorf("render kanban: %w", err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err = w.Write(out)
	return err
}

// RenderGraphAPI implements server.GraphAPIRenderer. Returns the full
// cascade graph (nodes + edges) as JSON. Consumed by the /flow page's
// client-side flowchart renderer.
func (r *Renderer) RenderGraphAPI(_ context.Context, w http.ResponseWriter, _ *http.Request) error {
	graph, err := LoadCascadeGraph(r.projectPath)
	if err != nil {
		return fmt.Errorf("render graph API: load graph: %w", err)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(graph); err != nil {
		return fmt.Errorf("render graph API: encode: %w", err)
	}
	return nil
}
