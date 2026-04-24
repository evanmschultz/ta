package ops

// ResetDefaultCacheForTest wipes the package-level schema cache so
// each test case starts with a cold cache. Safe to call from test
// code in any package (including cross-package tests under `cmd/`).
//
// Not part of the stable API surface despite the exported name —
// production callers have no reason to invoke it. Lives in a
// regular .go file (not _test.go) because Go's package-scoped
// test-only visibility does not extend to external test packages
// that import this package from a different directory.
func ResetDefaultCacheForTest() {
	defaultCache = newSchemaCache()
}
