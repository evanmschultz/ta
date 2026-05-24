package serverview_test

import (
	"testing"

	"github.com/evanmschultz/ta/internal/serverview"
)

func TestDeriveActiveScope(t *testing.T) {
	tests := []struct {
		name     string
		route    string
		expected serverview.NavScope
	}{
		{
			name:     "root path",
			route:    "/",
			expected: serverview.ScopeCascade,
		},
		{
			name:     "cascade detail route",
			route:    "/cascade/drop_008.drop.planner",
			expected: serverview.ScopeCascade,
		},
		{
			name:     "cascade detail route no slash",
			route:    "cascade/drop_008.drop.planner",
			expected: serverview.ScopeCascade,
		},
		{
			name:     "roadmap route",
			route:    "/roadmap",
			expected: serverview.ScopeRoadmap,
		},
		{
			name:     "schema route",
			route:    "/schema",
			expected: serverview.ScopeSchema,
		},
		{
			name:     "search route with query",
			route:    "/search?q=test",
			expected: serverview.ScopeSearch,
		},
		{
			name:     "empty route defaults to cascade",
			route:    "",
			expected: serverview.ScopeCascade,
		},
		{
			name:     "unknown route defaults to cascade",
			route:    "/unknown/path",
			expected: serverview.ScopeCascade,
		},
		{
			name:     "whitespace-trimmed",
			route:    "  /roadmap  ",
			expected: serverview.ScopeRoadmap,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := serverview.DeriveActiveScope(tt.route)
			if got != tt.expected {
				t.Errorf("DeriveActiveScope(%q) = %q, want %q", tt.route, got, tt.expected)
			}
		})
	}
}

func TestBreadcrumbForRecord(t *testing.T) {
	tests := []struct {
		name     string
		recordID string
		parentID string
		expected []serverview.BreadcrumbItem
	}{
		{
			name:     "record with parent",
			recordID: "drop_008.drop.planner",
			parentID: "drop_008.drop",
			expected: []serverview.BreadcrumbItem{
				{Label: "drop_008.drop", URL: "/cascade/drop_008.drop"},
				{Label: "drop_008.drop.planner", URL: ""},
			},
		},
		{
			name:     "record without parent",
			recordID: "drop_008.drop",
			parentID: "",
			expected: []serverview.BreadcrumbItem{
				{Label: "drop_008.drop", URL: ""},
			},
		},
		{
			name:     "deeply nested record",
			recordID: "drop_008.drop.builder_l2_a_d1_serve_cmd",
			parentID: "drop_008.drop.planner_l2_a_serve_cmd",
			expected: []serverview.BreadcrumbItem{
				{Label: "drop_008.drop.planner_l2_a_serve_cmd", URL: "/cascade/drop_008.drop.planner_l2_a_serve_cmd"},
				{Label: "drop_008.drop.builder_l2_a_d1_serve_cmd", URL: ""},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := serverview.BreadcrumbForRecord(tt.recordID, tt.parentID)
			if !breadcrumbsEqual(got, tt.expected) {
				t.Errorf("BreadcrumbForRecord(%q, %q) = %+v, want %+v", tt.recordID, tt.parentID, got, tt.expected)
			}
		})
	}
}

func TestBreadcrumbForUtilityRoute(t *testing.T) {
	tests := []struct {
		name     string
		scope    serverview.NavScope
		expected []serverview.BreadcrumbItem
	}{
		{
			name:  "roadmap scope",
			scope: serverview.ScopeRoadmap,
			expected: []serverview.BreadcrumbItem{
				{Label: "Cascade", URL: "/"},
				{Label: "roadmap", URL: ""},
			},
		},
		{
			name:  "schema scope",
			scope: serverview.ScopeSchema,
			expected: []serverview.BreadcrumbItem{
				{Label: "Cascade", URL: "/"},
				{Label: "schema", URL: ""},
			},
		},
		{
			name:  "search scope",
			scope: serverview.ScopeSearch,
			expected: []serverview.BreadcrumbItem{
				{Label: "Cascade", URL: "/"},
				{Label: "search", URL: ""},
			},
		},
		{
			name:  "cascade scope",
			scope: serverview.ScopeCascade,
			expected: []serverview.BreadcrumbItem{
				{Label: "Cascade", URL: "/"},
				{Label: "cascade", URL: ""},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := serverview.BreadcrumbForUtilityRoute(tt.scope)
			if !breadcrumbsEqual(got, tt.expected) {
				t.Errorf("BreadcrumbForUtilityRoute(%q) = %+v, want %+v", tt.scope, got, tt.expected)
			}
		})
	}
}

func TestNewPageContext(t *testing.T) {
	tests := []struct {
		name                string
		recordID            string
		pageTitle           string
		parentID            string
		expectedScope       serverview.NavScope
		expectedTitle       string
		expectedItems       int
		expectedBreadcrumbs int
	}{
		{
			name:                "cascade detail with parent",
			recordID:            "drop_008.drop.planner",
			pageTitle:           "L2 planner",
			parentID:            "drop_008.drop",
			expectedScope:       serverview.ScopeCascade,
			expectedTitle:       "L2 planner",
			expectedItems:       4,
			expectedBreadcrumbs: 2,
		},
		{
			name:                "cascade root drop",
			recordID:            "drop_008.drop",
			pageTitle:           "Drop 008",
			parentID:            "",
			expectedScope:       serverview.ScopeCascade,
			expectedTitle:       "Drop 008",
			expectedItems:       4,
			expectedBreadcrumbs: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := serverview.NewPageContext(tt.recordID, tt.pageTitle, tt.parentID)

			if got.ActiveScope != tt.expectedScope {
				t.Errorf("ActiveScope = %q, want %q", got.ActiveScope, tt.expectedScope)
			}
			if got.PageTitle != tt.expectedTitle {
				t.Errorf("PageTitle = %q, want %q", got.PageTitle, tt.expectedTitle)
			}
			if len(got.SidebarItems) != tt.expectedItems {
				t.Errorf("SidebarItems count = %d, want %d", len(got.SidebarItems), tt.expectedItems)
			}
			if len(got.Breadcrumb) != tt.expectedBreadcrumbs {
				t.Errorf("Breadcrumb count = %d, want %d", len(got.Breadcrumb), tt.expectedBreadcrumbs)
			}

			// Verify the cascade item is marked active.
			cascadeActive := false
			for _, item := range got.SidebarItems {
				if item.Scope == serverview.ScopeCascade && item.IsActive {
					cascadeActive = true
				} else if item.Scope != serverview.ScopeCascade && item.IsActive {
					t.Errorf("non-cascade item %s is unexpectedly active", item.Scope)
				}
			}
			if !cascadeActive {
				t.Error("cascade item is not marked active")
			}
		})
	}
}

func TestNewPageContextForRoute(t *testing.T) {
	tests := []struct {
		name                string
		routePath           string
		pageTitle           string
		expectedScope       serverview.NavScope
		expectedTitle       string
		expectedItems       int
		expectedBreadcrumbs int
		expectedActiveLabel string
	}{
		{
			name:                "roadmap route",
			routePath:           "/roadmap",
			pageTitle:           "Project Roadmap",
			expectedScope:       serverview.ScopeRoadmap,
			expectedTitle:       "Project Roadmap",
			expectedItems:       4,
			expectedBreadcrumbs: 2,
			expectedActiveLabel: "roadmap",
		},
		{
			name:                "schema route",
			routePath:           "/schema",
			pageTitle:           "Schema Browser",
			expectedScope:       serverview.ScopeSchema,
			expectedTitle:       "Schema Browser",
			expectedItems:       4,
			expectedBreadcrumbs: 2,
			expectedActiveLabel: "schema",
		},
		{
			name:                "search route",
			routePath:           "/search",
			pageTitle:           "Search Results",
			expectedScope:       serverview.ScopeSearch,
			expectedTitle:       "Search Results",
			expectedItems:       4,
			expectedBreadcrumbs: 2,
			expectedActiveLabel: "search",
		},
		{
			name:                "root route",
			routePath:           "/",
			pageTitle:           "Cascade Browser",
			expectedScope:       serverview.ScopeCascade,
			expectedTitle:       "Cascade Browser",
			expectedItems:       4,
			expectedBreadcrumbs: 2,
			expectedActiveLabel: "cascade",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := serverview.NewPageContextForRoute(tt.routePath, tt.pageTitle)

			if got.ActiveScope != tt.expectedScope {
				t.Errorf("ActiveScope = %q, want %q", got.ActiveScope, tt.expectedScope)
			}
			if got.PageTitle != tt.expectedTitle {
				t.Errorf("PageTitle = %q, want %q", got.PageTitle, tt.expectedTitle)
			}
			if len(got.SidebarItems) != tt.expectedItems {
				t.Errorf("SidebarItems count = %d, want %d", len(got.SidebarItems), tt.expectedItems)
			}
			if len(got.Breadcrumb) != tt.expectedBreadcrumbs {
				t.Errorf("Breadcrumb count = %d, want %d", len(got.Breadcrumb), tt.expectedBreadcrumbs)
			}

			// Verify the expected scope item is marked active.
			activeCount := 0
			for _, item := range got.SidebarItems {
				if item.IsActive {
					activeCount++
					if item.Scope != tt.expectedScope {
						t.Errorf("active item %s does not match expected scope %s", item.Scope, tt.expectedScope)
					}
				}
			}
			if activeCount != 1 {
				t.Errorf("expected 1 active sidebar item, got %d", activeCount)
			}

			// Verify the last breadcrumb matches the expected scope.
			if len(got.Breadcrumb) > 0 {
				lastCrumb := got.Breadcrumb[len(got.Breadcrumb)-1]
				if lastCrumb.Label != tt.expectedActiveLabel {
					t.Errorf("last breadcrumb label = %q, want %q", lastCrumb.Label, tt.expectedActiveLabel)
				}
				if lastCrumb.URL != "" {
					t.Errorf("last breadcrumb URL should be empty for current page, got %q", lastCrumb.URL)
				}
			}
		})
	}
}

func TestDefaultSidebarItems(t *testing.T) {
	items := serverview.DefaultSidebarItems()

	if len(items) != 4 {
		t.Fatalf("DefaultSidebarItems returned %d items, want 4", len(items))
	}

	expectedScopes := []serverview.NavScope{serverview.ScopeCascade, serverview.ScopeRoadmap, serverview.ScopeSchema, serverview.ScopeSearch}
	for i, scope := range expectedScopes {
		if items[i].Scope != scope {
			t.Errorf("items[%d].Scope = %q, want %q", i, items[i].Scope, scope)
		}
		if items[i].IsActive {
			t.Errorf("items[%d].IsActive should be false by default", i)
		}
		if items[i].Route == "" {
			t.Errorf("items[%d].Route is empty", i)
		}
		if items[i].Label == "" {
			t.Errorf("items[%d].Label is empty", i)
		}
	}
}

func TestPageContextSidebarData(t *testing.T) {
	items := serverview.DefaultSidebarItems()
	items[1].IsActive = true // Mark roadmap active

	pc := serverview.PageContext{
		SidebarItems: items,
		ActiveScope:  serverview.ScopeRoadmap,
		PageTitle:    "Test",
	}

	data := pc.SidebarData()

	if data["ActiveScope"] != "roadmap" {
		t.Errorf("ActiveScope = %q, want %q", data["ActiveScope"], "roadmap")
	}

	sidebarItems, ok := data["Items"].([]serverview.NavItem)
	if !ok {
		t.Fatalf("items is not []NavItem")
	}
	if len(sidebarItems) != 4 {
		t.Errorf("items count = %d, want 4", len(sidebarItems))
	}
}

// breadcrumbsEqual compares two breadcrumb slices for equality.
func breadcrumbsEqual(a, b []serverview.BreadcrumbItem) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Label != b[i].Label || a[i].URL != b[i].URL {
			return false
		}
	}
	return true
}
