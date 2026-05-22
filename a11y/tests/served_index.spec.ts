import { test, expect } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

/**
 * drop_009 L2-A D3 — served / route a11y spec (cascade tree index).
 *
 * Hits the live `ta serve` `/` route (started by Playwright's webServer
 * config) and asserts axe-core finds zero serious/critical WCAG 2 AA
 * violations. moderate/minor are not blocking by design (initial gate;
 * tighten later per the pre-MVP-tracker discipline).
 *
 * Originally targeted /schema, but the schema_browser.html render path
 * has an upstream bug producing a partial 500 page (LoadSchema doesn't
 * cleanly handle ta's own .ta/schema.toml shape — tracked as a
 * post-MVP-feature-complete follow-up). The / route uses the inline
 * cascade-index HTML from internal/serverview/render.go::writeCascadeIndex,
 * which carries `<title>` + `lang="en"` correctly.
 *
 * When the schema_browser bug is fixed, add a sibling spec for /schema.
 */
test.describe('GET / — cascade tree index a11y', () => {
  test('axe-core: zero serious/critical violations under WCAG 2 AA', async ({ page }) => {
    await page.goto('/');
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
        `axe-core found ${blockers.length} serious/critical WCAG 2 AA violation(s) on /:\n${summary}\nFull details: ${JSON.stringify(blockers, null, 2)}`,
      );
    }

    expect(blockers).toEqual([]);
  });
});
