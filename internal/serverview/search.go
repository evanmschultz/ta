package serverview

import (
	"fmt"

	"github.com/evanmschultz/ta/internal/ops"
)

// SearchLoaderResult holds the minimal data needed to render search results:
// the list of result records from an ops.Search call and the committed Track A
// template name for rendering.
//
// The template name is fixed to "search_results.html" as the only committed
// Track A search result rendering template. The Results are search hits returned
// by ops.Search and are passed directly to the template engine.
type SearchLoaderResult struct {
	Results      []ops.SearchHit // search hits from ops.Search
	TemplateName string          // committed Track A template name ("search_results.html")
}

// LoadSearch executes a search query against the live .ta project at projectPath
// and returns the results shaped for rendering with the committed Track A template.
//
// The template name is always "search_results.html" — the only committed
// Track A template for search result rendering.
//
// Returns an error if the project cannot be resolved, the search query fails,
// or the query string is empty.
func LoadSearch(projectPath, query string) (SearchLoaderResult, error) {
	// Validate query is not empty.
	if query == "" {
		return SearchLoaderResult{}, fmt.Errorf("load search: query cannot be empty")
	}

	// Execute the search query. We pass empty scope to search the entire
	// project, empty match to avoid type filtering, and the query as the
	// regex to match against all string fields.
	hits, err := ops.Search(projectPath, "", "", nil, query, "", 0, false)
	if err != nil {
		return SearchLoaderResult{}, fmt.Errorf("search query %q: %w", query, err)
	}

	return SearchLoaderResult{
		Results:      hits,
		TemplateName: "search_results.html",
	}, nil
}
