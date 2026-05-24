// page_context.go — Shared page-context substrate for template rendering.
// PageContext carries sidebar items, active scope marker, and breadcrumb
// metadata for every served HTML page. Helper functions derive these
// without emitting HTML or CSS.

package serverview

import (
	"strings"
)

// NavScope represents one of the four main navigation scopes in the served UI.
// Each scope maps to a top-level route and appears as one item in the sidebar.
type NavScope string

const (
	ScopeCascade NavScope = "cascade" // /
	ScopeRoadmap NavScope = "roadmap" // /roadmap
	ScopeSchema  NavScope = "schema"  // /schema
	ScopeSearch  NavScope = "search"  // /search
)

// NavItem represents one entry in the sidebar navigation menu.
type NavItem struct {
	Label    string
	Route    string
	Scope    NavScope
	IsActive bool
}

// BreadcrumbItem represents one segment in a breadcrumb trail.
type BreadcrumbItem struct {
	Label string
	URL   string // empty if this is the final/current item
}

// PageContext is the shared context passed to every page template.
// It carries sidebar navigation state, active scope, and breadcrumb metadata.
// Templates include the sidebar partial and use these fields to render
// persistent navigation chrome across every served page.
type PageContext struct {
	// Sidebar navigation items (cascade, roadmap, schema, search).
	SidebarItems []NavItem

	// The active nav scope (which sidebar item is current).
	ActiveScope NavScope

	// Breadcrumb trail for the current page (e.g., cascade record hierarchy).
	Breadcrumb []BreadcrumbItem

	// Page title for the browser tab and header.
	PageTitle string
}

// SidebarData returns a template-friendly representation of sidebar state.
// It is a convenience wrapper for templates that need both the items and
// the active scope in one call.
func (pc PageContext) SidebarData() map[string]any {
	return map[string]any{
		"Items":       pc.SidebarItems,
		"ActiveScope": string(pc.ActiveScope),
	}
}

// DeriveActiveScope determines the active nav scope from a route path.
// Routes:
// - "/" or cascade detail paths (/cascade/*) → ScopeCascade
// - "/roadmap" → ScopeRoadmap
// - "/schema" → ScopeSchema
// - "/search" → ScopeSearch
func DeriveActiveScope(routePath string) NavScope {
	routePath = strings.TrimSpace(routePath)
	if routePath == "" {
		routePath = "/"
	}

	// Normalize path for comparison.
	routePath = strings.TrimPrefix(routePath, "/")

	switch {
	case routePath == "" || strings.HasPrefix(routePath, "cascade"):
		return ScopeCascade
	case strings.HasPrefix(routePath, "roadmap"):
		return ScopeRoadmap
	case strings.HasPrefix(routePath, "schema"):
		return ScopeSchema
	case strings.HasPrefix(routePath, "search"):
		return ScopeSearch
	default:
		// Default to cascade if route is unrecognized.
		return ScopeCascade
	}
}

// BreadcrumbForRecord builds a breadcrumb trail for a cascade record.
// The trail shows the record's id and, if it has a parent_id, includes
// a clickable parent entry.
// Final breadcrumb item has no URL (indicating "current page").
func BreadcrumbForRecord(recordID string, parentID string) []BreadcrumbItem {
	crumbs := []BreadcrumbItem{}

	// If the record has a parent, show it as a breadcrumb link.
	if parentID != "" {
		crumbs = append(crumbs, BreadcrumbItem{
			Label: parentID,
			URL:   "/cascade/" + parentID,
		})
	}

	// Current record (no URL, final crumb).
	crumbs = append(crumbs, BreadcrumbItem{
		Label: recordID,
		URL:   "", // Current page, no URL
	})

	return crumbs
}

// BreadcrumbForUtilityRoute builds a breadcrumb trail for a utility page
// (roadmap, schema, search). The trail shows a link to the cascade index,
// then the current scope label.
func BreadcrumbForUtilityRoute(scope NavScope) []BreadcrumbItem {
	return []BreadcrumbItem{
		{
			Label: "Cascade",
			URL:   "/",
		},
		{
			Label: string(scope),
			URL:   "", // Current page, no URL
		},
	}
}

// DefaultSidebarItems returns the standard sidebar navigation menu
// with all four nav scopes and no active scope selected.
// Callers should update the appropriate item's IsActive flag.
func DefaultSidebarItems() []NavItem {
	return []NavItem{
		{
			Label:    "Cascade",
			Route:    "/",
			Scope:    ScopeCascade,
			IsActive: false,
		},
		{
			Label:    "Roadmap",
			Route:    "/roadmap",
			Scope:    ScopeRoadmap,
			IsActive: false,
		},
		{
			Label:    "Schema",
			Route:    "/schema",
			Scope:    ScopeSchema,
			IsActive: false,
		},
		{
			Label:    "Search",
			Route:    "/search",
			Scope:    ScopeSearch,
			IsActive: false,
		},
	}
}

// NewPageContext creates a PageContext for a cascade detail page.
// It derives the active scope from the route, sets up the sidebar with
// the active item marked, builds a breadcrumb trail for the record,
// and sets the page title.
func NewPageContext(recordID string, pageTitle string, parentID string) PageContext {
	items := DefaultSidebarItems()
	// Mark cascade scope as active for detail pages.
	items[0].IsActive = true

	return PageContext{
		SidebarItems: items,
		ActiveScope:  ScopeCascade,
		Breadcrumb:   BreadcrumbForRecord(recordID, parentID),
		PageTitle:    pageTitle,
	}
}

// NewPageContextForRoute creates a PageContext for a utility route
// (roadmap, schema, search). It derives the active scope from the route,
// marks the appropriate sidebar item, builds a breadcrumb trail,
// and sets the page title.
func NewPageContextForRoute(routePath string, pageTitle string) PageContext {
	scope := DeriveActiveScope(routePath)
	items := DefaultSidebarItems()

	// Mark the corresponding scope as active.
	for i := range items {
		if items[i].Scope == scope {
			items[i].IsActive = true
			break
		}
	}

	return PageContext{
		SidebarItems: items,
		ActiveScope:  scope,
		Breadcrumb:   BreadcrumbForUtilityRoute(scope),
		PageTitle:    pageTitle,
	}
}
