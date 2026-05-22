# Playwright + axe-core a11y gate — pattern reference

This document captures the canonical pattern ta uses to gate every PR on Playwright + axe-core accessibility checks. The pattern is designed for: (1) hard CI enforcement on every PR, (2) zero local-dev friction for contributors who haven't installed the Node/Playwright toolchain. Copy this pattern to any project where a11y compliance must not regress.

## TL;DR

- **CI runs Playwright + axe on every PR as a required check** — fails on `serious`/`critical` WCAG 2 AA violations.
- **Local `mage check` warn-skips** when `a11y/node_modules` is absent — Go-only contributors aren't blocked.
- **Local devs who install the toolchain get the full run** — `cd a11y && pnpm install && pnpm exec playwright install chromium` opts in.
- **Five files** + one mage target carry the whole pattern. Copyable to any project in ~15 min.

## Why this design

The naive options are both bad:

- **Skip CI a11y** — relies on devs remembering to run Playwright locally. a11y rots silently.
- **Hard-fail `mage check` everywhere** — every dev needs Playwright + browsers installed before `mage check` passes. Hostile to drive-by contributors and Go-only sub-team work.

The env-split pattern fixes both:

| Environment | pnpm/Playwright present? | Behavior |
| --- | --- | --- |
| CI (`CI=true`) | yes (workflow installs) | hard-fail on missing toolchain or violations |
| Local | no (`a11y/node_modules` absent) | warn-skip, exit 0, print install command |
| Local | yes | full run, hard-fail on violations |

CI is the single enforcement point. Local skipping is intentional dev-friction relief.

## Files (5 + 1 mage target)

### 1. `a11y/package.json`

```json
{
  "name": "@<project>/a11y",
  "version": "0.0.0",
  "private": true,
  "type": "module",
  "scripts": {
    "test": "playwright test",
    "test:report": "playwright test --reporter=html"
  },
  "devDependencies": {
    "@axe-core/playwright": "4.10.1",
    "@playwright/test": "1.49.0",
    "axe-core": "4.10.2"
  }
}
```

### 2. `a11y/playwright.config.ts`

```typescript
import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
  testDir: './tests',
  timeout: 30_000,
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: 0,
  workers: 1,
  reporter: process.env.CI ? 'list' : 'html',
  webServer: {
    command: './bin/<project> serve --port 4321',  // or your server's start command
    cwd: '..',
    url: 'http://127.0.0.1:4321/',
    timeout: 30_000,
    reuseExistingServer: !process.env.CI,
  },
  use: {
    baseURL: 'http://127.0.0.1:4321',
  },
  projects: [
    { name: 'mobile-375',  use: { ...devices['Desktop Chrome'], viewport: { width: 375,  height: 667  } } },
    { name: 'tablet-768',  use: { ...devices['Desktop Chrome'], viewport: { width: 768,  height: 1024 } } },
    { name: 'desktop-1280', use: { ...devices['Desktop Chrome'], viewport: { width: 1280, height: 800 } } },
  ],
});
```

The `webServer` block tells Playwright to start your HTTP server before the suite and tear it down after. `cwd: '..'` so commands resolve relative to the repo root (where `bin/<project>` lives). `reuseExistingServer: !process.env.CI` lets local devs run against a server they already started; CI always boots fresh.

### 3. `a11y/tests/<route>.spec.ts`

Per-route axe-core spec. One file per route or feature surface:

```typescript
import { test, expect } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

test.describe('GET /<route> — served route a11y', () => {
  test('axe-core: zero serious/critical violations under WCAG 2 AA', async ({ page }) => {
    await page.goto('/<route>');
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
        `axe-core found ${blockers.length} serious/critical WCAG 2 AA violation(s) on /<route>:\n${summary}`,
      );
    }

    expect(blockers).toEqual([]);
  });
});
```

**Severity policy**: `serious` + `critical` block; `moderate` + `minor` are reported in the Playwright artifact but don't fail the build. Tighten over time (raise the floor to `moderate` once the codebase is clean of serious/critical).

### 4. `a11y/.gitignore`

```
node_modules/
playwright-report/
test-results/
.playwright/
```

### 5. `.github/workflows/ci.yml` — `a11y` job

Add this job alongside your existing `check` (or equivalent):

```yaml
  a11y:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v6
      - uses: actions/setup-go@v6
        with:
          go-version: '1.26.x'
          cache: true
      - uses: actions/setup-node@v4
        with:
          node-version: '20'
      - uses: pnpm/action-setup@v4
        with:
          version: 9
      - name: Cache Playwright browsers
        uses: actions/cache@v4
        id: playwright-cache
        with:
          path: ~/.cache/ms-playwright
          key: playwright-${{ runner.os }}-${{ hashFiles('a11y/pnpm-lock.yaml', 'a11y/package.json') }}
      - name: Install mage
        run: go install github.com/magefile/mage@latest
      - name: Install a11y deps
        run: cd a11y && pnpm install --no-frozen-lockfile
      - name: Install Playwright browsers (cache miss only)
        if: steps.playwright-cache.outputs.cache-hit != 'true'
        run: cd a11y && pnpm exec playwright install --with-deps chromium
      - name: Install Playwright system deps (cache hit)
        if: steps.playwright-cache.outputs.cache-hit == 'true'
        run: cd a11y && pnpm exec playwright install-deps chromium
      - name: Run a11y suite (mage TemplatesA11y)
        run: mage TemplatesA11y
      - name: Upload Playwright report on failure
        if: failure()
        uses: actions/upload-artifact@v4
        with:
          name: playwright-report
          path: a11y/playwright-report/
          retention-days: 14
```

The cache key is tied to `a11y/pnpm-lock.yaml` + `a11y/package.json`. Browser binaries are ~300-500 MB; the cache cuts install from ~60s (cold) to ~15s (warm). The `if: failure()` artifact upload lets devs download the full Playwright report from a failed PR without re-running CI locally.

Make `a11y` a required check via GitHub branch protection settings (Settings → Branches → Branch protection rules → `main` → Require status checks → check `a11y`).

### 6. `magefile.go` — `TemplatesA11y` target

```go
// TemplatesA11y runs the Playwright + axe-core a11y suite under a11y/.
// Environment-split: CI hard-fails on missing toolchain or violations;
// local warn-skips when a11y/node_modules is absent (opt in by installing).
func TemplatesA11y() error {
    inCI := os.Getenv("CI") == "true"
    if !inCI {
        if _, err := os.Stat("a11y/node_modules"); err != nil {
            fmt.Fprintln(os.Stderr,
                "WARN: a11y/node_modules absent; skipping TemplatesA11y locally. "+
                    "CI enforces a11y on every PR. Install with "+
                    "`cd a11y && pnpm install && pnpm exec playwright install chromium` "+
                    "to enable locally.")
            return nil
        }
    }
    pnpm, err := exec.LookPath("pnpm")
    if err != nil {
        return fmt.Errorf("templatesA11y: pnpm not on PATH: %w", err)
    }
    if err := Build(); err != nil {  // or whatever builds your project's binary
        return fmt.Errorf("templatesA11y: build binary: %w", err)
    }
    cmd := exec.Command(pnpm, "test")
    cmd.Dir = "a11y"
    cmd.Stdout = os.Stdout
    cmd.Stderr = os.Stderr
    if err := cmd.Run(); err != nil {
        return fmt.Errorf("templatesA11y: pnpm test in a11y/: %w", err)
    }
    return nil
}
```

Wire into your composite `Check` so `mage check` covers a11y (warn-skipped locally, enforced in CI):

```go
func Check() error {
    for _, step := range []func() error{FmtCheck, Vet, Test, Tidy, TemplatesA11y} {
        if err := step(); err != nil { return err }
    }
    return nil
}
```

## Operating model

### When CI fails the a11y gate

1. The PR check `a11y` reports failure with the violating route, axe rule id, and impacted node count in the job log.
2. Download the full Playwright report via the PR's "Artifacts" link (uploaded by the `if: failure()` step). Open `playwright-report/index.html` for screenshots + DOM snapshots of each failing assertion.
3. Fix the markup/CSS until axe reports zero serious/critical violations. Common fixes: `aria-label` / `alt` text, semantic landmarks (`<main>`, `<nav>`, `<header>`), color contrast, focus order, form label association.
4. Push the fix. CI re-runs automatically.

### When you add a new served route

1. Write the route handler + template + serve test as usual.
2. Add `a11y/tests/<route>.spec.ts` mirroring the template in §3. One new spec file per route.
3. Push. CI picks up the new spec automatically (Playwright globs `tests/**/*.spec.ts`).

### When you want to tighten the severity gate

Edit the filter in your spec(s):

```typescript
// Current: serious + critical block
const blockers = results.violations.filter((v) => v.impact === 'serious' || v.impact === 'critical');

// Tighter: moderate also blocks
const blockers = results.violations.filter((v) => ['serious', 'critical', 'moderate'].includes(v.impact));
```

Best practice: tighten gradually. Once the codebase is clean of serious/critical for a release cycle, add moderate. Then minor. Do not start with all severities blocking — early-stage codebases drown in noise.

### When a route legitimately can't pass axe

Some patterns (e.g. canvas charts, decorative iframes) genuinely cannot pass automated axe checks. Use `.disableRules()` per-test with a comment explaining WHY:

```typescript
const results = await new AxeBuilder({ page })
  .disableRules(['color-contrast'])  // chart uses brand-required low-contrast palette per design system v3
  .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'])
  .analyze();
```

Track each disabled rule in your project's a11y backlog. The disable should be temporary or come with documented manual-review coverage.

## Coverage honesty

Automated axe-core catches ~57% of real WCAG violations (Deque internal study). Keyboard navigation, screen reader UX, cognitive accessibility, and color-contrast edge cases need manual review. This gate is the **floor**, not the ceiling. Schedule manual a11y reviews for major UI changes; do not assume "green a11y job" means "WCAG compliant."

## Cost in CI minutes

- Cold run (no cache, fresh checkout): ~90-150s (browser install + deps + test).
- Warm run (cache hit on browsers + Go cache hit): ~30-45s.
- Per-PR cost on GitHub Actions free tier (public repo): 0 (unlimited). Private repo on free Personal plan: 2000 min/month — a 45s a11y job per PR supports ~2600 PRs/mo, well above realistic volume.

## Copying this pattern to another project

1. Create `a11y/` directory + the 4 files (package.json, playwright.config.ts, tests/<route>.spec.ts, .gitignore). Adjust the `command` in `webServer` to start that project's HTTP server.
2. Add the `TemplatesA11y` mage target (or equivalent in your build runner) with the env-split logic.
3. Wire it into your composite check target (`mage check`, `make check`, `pnpm check`, etc.).
4. Add the `a11y` job to your CI workflow.
5. Configure branch protection to require the `a11y` status check on `main`.
6. Initial run: write at least one spec for a known-good route. Watch CI go green. Then expand.

The whole pattern is ~150 LOC of substrate per project. Highly portable.

## Open questions / follow-ups (TA-specific)

- Add specs for `/`, `/cascade/<id>`, `/roadmap`, `/search?q=...` routes (currently only `/schema`). Each follows the same template.
- Add a Track A fixture spec that loads `internal/templates_html_basic/templates/<name>.html` directly via `file://` — proves a11y at the template level without going through ta serve. (drop_009 L2-A D3 originally specified both; second spec deferred to follow-up.)
- Consider moving the cache key to a Playwright version stamp so version bumps invalidate cleanly.

## References

- [Playwright CI docs](https://playwright.dev/docs/ci)
- [@axe-core/playwright npm](https://www.npmjs.com/package/@axe-core/playwright)
- [axe-core rules + impact levels](https://github.com/dequelabs/axe-core/blob/develop/doc/rule-descriptions.md)
- [GitHub Actions caching for Playwright](https://devactivity.com/posts/development-integrations/turbocharge-your-ci-mastering-github-actions-caching-for-npm-playwright-and-pre-commit/)
- [Deque automated coverage study](https://www.deque.com/axe/axe-core/) — the ~57% number
