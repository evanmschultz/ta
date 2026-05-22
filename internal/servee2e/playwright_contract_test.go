package servee2e

import "testing"

// playwright_contract_test.go preserves the recorded Go test names that
// drop_008's L2-D-D4 acceptance reserved. Actual coverage lives in the
// TypeScript Playwright + axe suite shipped by drop_009 under a11y/.
//
// See docs/a11y-playwright-ci.md and docs/playwright-live-backend-pattern.md.

// TestServe_AsHTTP_CascadeTreeOnGet_Playwright is a contract stub. The
// recorded acceptance required this test name; actual coverage lives in
// a11y/tests/served_index.spec.ts.
func TestServe_AsHTTP_CascadeTreeOnGet_Playwright(t *testing.T) {
	t.Skip("contract migrated to a11y/tests/served_index.spec.ts via drop_009's TypeScript Playwright + axe suite; see docs/a11y-playwright-ci.md")
}

// TestServe_AsHTTP_SearchFlow_PlaywrightAxe is a contract stub. The
// cascade-tree axe coverage lives in a11y/tests/served_index.spec.ts;
// a dedicated search-flow Playwright spec is a follow-up.
func TestServe_AsHTTP_SearchFlow_PlaywrightAxe(t *testing.T) {
	t.Skip("search-flow Playwright spec is a follow-up; cascade-tree axe coverage lives in a11y/tests/served_index.spec.ts")
}
