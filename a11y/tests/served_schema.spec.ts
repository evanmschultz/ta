import { test, expect } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

/**
 * drop_009 L2-A D3 — served /schema route a11y spec.
 *
 * Hits the live `ta serve` /schema route (started by Playwright's
 * webServer config) and asserts axe-core finds zero serious/critical
 * WCAG 2 AA violations. moderate / minor are not blocking by design
 * (initial gate; tighten later per the pre-MVP-tracker discipline).
 *
 * The schema_browser.html template uses --optional-bg / --optional-fg
 * tokens swapped to AA contrast in drop_009 L2-B D1 (commit 4882365);
 * D2 locked the contrast ratio via Go unit test (pill_contrast_test.go).
 * This Playwright spec is the integrated regression-gate at the
 * served-route level.
 */
test.describe('GET /schema — served route a11y', () => {
  test('axe-core: zero serious/critical violations under WCAG 2 AA', async ({ page }) => {
    await page.goto('/schema');
    await page.waitForLoadState('networkidle');

    const results = await new AxeBuilder({ page })
      .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'])
      .analyze();

    const blockers = results.violations.filter(
      (v) => v.impact === 'serious' || v.impact === 'critical',
    );

    if (blockers.length > 0) {
      const summary = blockers
        .map((v) => `- [${v.impact}] ${v.id}: ${v.help} (${v.nodes.length} node(s))`)
        .join('\n');
      throw new Error(
        `axe-core found ${blockers.length} serious/critical WCAG 2 AA violation(s) on /schema:\n${summary}\nFull details: ${JSON.stringify(blockers, null, 2)}`,
      );
    }

    expect(blockers).toEqual([]);
  });
});
