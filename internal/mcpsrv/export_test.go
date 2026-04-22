package mcpsrv

import (
	"github.com/evanmschultz/ta/internal/config"
	"github.com/mark3labs/mcp-go/server"
)

// MCPServer exposes the underlying mcp-go server for in-process test clients.
// Test-only; do not call from non-test code.
func (s *Server) MCPServer() *server.MCPServer { return s.srv }

// ResetDefaultCacheForTest wipes the package-level schema cache so each
// test case starts with a cold cache. Call from t.Cleanup() after
// constructing a test fixture that depends on the cache state.
func ResetDefaultCacheForTest() {
	defaultCache = newSchemaCache()
}

// DefaultCacheLoadCountForTest exposes the package cache's load counter
// so tests can prove cache-hit vs cache-miss behavior without racy
// timing assertions.
func DefaultCacheLoadCountForTest() uint64 {
	return defaultCache.loadCountForTest()
}

// SwapDefaultCacheLoaderForTest replaces the defaultCache with a fresh
// cache whose underlying loader is the caller-supplied loader. Returns
// a restore function that reinstates a cold production cache. Test-only
// indirection that lets cache_test.go count loader invocations while
// still exercising the package-level code path real MCP traffic uses.
func SwapDefaultCacheLoaderForTest(loader func(string) (config.Resolution, error)) func() {
	prev := defaultCache
	defaultCache = newSchemaCacheWithLoader(loader)
	return func() { defaultCache = prev }
}

// DefaultResolveUncachedForTest exposes the production loader so tests
// can wrap it in counting indirection while still exercising the real
// sentinel-path dance.
func DefaultResolveUncachedForTest(projectPath string) (config.Resolution, error) {
	return resolveFromProjectDirUncached(projectPath)
}
