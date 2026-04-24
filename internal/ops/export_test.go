package ops

import (
	"github.com/evanmschultz/ta/internal/config"
)

// ResetDefaultCacheForTest is declared in testing.go (regular file) so
// external test packages under cmd/ can reset the cache between tests.
// See testing.go for the implementation.

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
// project-local resolve path.
func DefaultResolveUncachedForTest(projectPath string) (config.Resolution, error) {
	return resolveFromProjectDirUncached(projectPath)
}
