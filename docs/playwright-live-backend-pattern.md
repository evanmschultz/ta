# Playwright lives at the live backend — never the standalone frontend dev server

This is a cross-project reference. The rule it documents was discovered while planning a different project's Wails+Astro Playwright integration; ta already follows the equivalent pattern by accident because ta is HTTP-server-only. Other projects in this org with both a web build AND a desktop (Wails) build should validate which version of the rule applies to them — see §5 for the per-project questions.

## TL;DR

- **Always point Playwright at the live backend** (the runtime your users actually hit), not the standalone frontend dev server.
- For HTTP-server projects: that's `<your-server> serve` running on its real port.
- For Wails projects: that's `wails dev` (default port 34115), NOT `astro dev`/`vite dev`/`pnpm dev` alone (typically 4321/5173/51428/etc.). `wails dev` is what injects `window.go` — the IPC bridge — into the page. Without it, every `window.go.<Service>.<Method>` call in your FE is a no-op and your tests pass against a frontend that can never have worked.
- For component-level tests (Vitest etc.): mock `window.go` explicitly; that's a different layer with different semantics.
- macOS visual fidelity (WKWebView ≠ Chromium) is an accepted gap; layer manual visual smoke on top of Playwright; no automatable WKWebView driver exists as of 2026.

## 1. The principle

Frontend dev servers (Astro, Vite, Next dev, etc.) serve a frontend in isolation. They have no backend, no IPC bridge, no real data layer — they ARE the bare frontend, designed for FE-only iteration.

A Playwright test running against a standalone frontend dev server can only verify:

- HTML/CSS/markup correctness
- Static interactivity (vanilla JS, framework-managed state)
- Mocked-data rendering

It cannot verify:

- Real backend calls succeed
- IPC bridges (window.go, window.electron, native shims) are present and wired
- Real data shapes match what the FE assumes
- Auth/session/storage flows that touch the backend

If your project IS a frontend + backend system (HTTP server with templated pages, Wails app with Go backend, Electron with native bindings), Playwright against the standalone FE dev server is a false-confidence gate — green tests, broken production.

## 2. The fix per project shape

### 2.1 HTTP-server with server-rendered HTML (ta pattern)

The frontend has no separate dev server — the Go HTTP server renders templates and returns HTML. Playwright auto-starts the real binary via the `webServer` config in `playwright.config.ts`:

```typescript
webServer: {
  command: './bin/<project> serve --port 4321',
  cwd: '..',
  url: 'http://127.0.0.1:4321/',
  timeout: 30_000,
  reuseExistingServer: !process.env.CI,
},
```

Playwright runs the real binary, with the real backend, against the real port. No FE dev server in the picture. This is what ta does (see `a11y/playwright.config.ts`). This pattern is the floor — if your project is HTTP-only, this is the right shape.

### 2.2 Wails desktop with web-build option

`wails dev` is the canonical Playwright target. Default port: `34115`. `wails dev` runs:

- Your Vite/Astro/Next dev server (so HMR works) AS the frontend
- Wraps it with a thin Go HTTP shim
- Injects `window.go` — the IPC bridge — into every served page
- Forwards FE → `window.go.<Service>.<Method>` calls to the live Go backend

Playwright config:

```typescript
webServer: {
  command: 'wails dev',
  cwd: '..',          // or wherever wails.json lives
  url: 'http://127.0.0.1:34115/',
  timeout: 60_000,    // wails dev startup is slower than astro dev alone
  reuseExistingServer: !process.env.CI,
},
```

DO NOT use `astro dev` / `vite dev` / `pnpm dev` standalone as the Playwright target in a Wails project — those serve the FE without `window.go`. Tests will pass against a bridgeless mock and silently miss real IPC regressions.

When your Wails project ALSO ships a web-only build (no Wails wrapper), test that build separately via the §2.1 HTTP-server pattern (start the web server, point Playwright there). Two configs, one per deployment target.

### 2.3 Electron / Tauri / other native-wrapper apps

Same principle: target the wrapper's dev server (`electron .` + Playwright Electron driver, `tauri dev` + tauri-driver, etc.), not the bare frontend. Each has its own driver story; check the framework's official Playwright integration guide.

### 2.4 Component / unit-level FE tests (Vitest, Jest)

Different layer, different rule: explicitly stub the backend (`stubGlobal('window.go', { ... })` in Vitest) so component tests stay fast and don't need a backend running. This is per-component contract testing, NOT integration testing. Use BOTH layers — component tests for fast TDD, Playwright integration tests against the live backend for confidence.

## 3. Common anti-patterns to reject

- **Shim/fixture pattern**: writing fake `window.go` HTML files served by `astro dev` to "let Playwright test the FE in isolation". Rejected — duplicates the IPC contract in test fixtures (drifts from real Go signatures), and the resulting test passes don't prove the real backend works. Just use `wails dev`.
- **"Mock everything in Playwright"**: Playwright tests against mocked backend stubs collapse into glorified component tests. If you want component tests, use Vitest. If you want integration tests, use the live backend.
- **"Run Playwright only in CI, not locally"**: defensible if local environment is hostile to the toolchain, but the project's `mage TemplatesA11y` / `pnpm test:e2e` should have a clear env-split (CI hard-fail, local warn-skip when toolchain missing) so devs CAN run locally when they want. See ta's `magefile.go::TemplatesA11y` for the pattern.

## 4. macOS WKWebView fidelity gap (accepted)

Wails on macOS uses WKWebView; Playwright drives Chromium. The two engines have different CSS, JS, layout, and font-rendering behavior. Playwright passing in Chromium does NOT guarantee the same page renders correctly in WKWebView. As of 2026, no automatable WKWebView driver exists — Apple's WebDriver protocol support for WKWebView is incomplete.

Mitigations (layered defense, not perfect):

- Manual macOS visual smoke before each release tag — open the built app, click through the critical flows.
- Visual regression on a Chromium reference build (Argos CI, Chromatic, Playwright snapshots) — catches FE-only regressions, doesn't catch engine-specific drift.
- Pin WebKit-specific feature usage to features known to work in WKWebView (avoid bleeding-edge CSS).

If your project is web-only (no Wails / Electron / Tauri), this gap doesn't apply — Chromium is your production engine too.

## 5. Per-project questions (cross-project reference)

When you adopt this doc in your project, answer these in your project's CLAUDE.md / a11y substrate / cascade record:

1. **Does this project ship a web-only build, a desktop (Wails / Electron / Tauri) build, or both?**
2. **If web-only**: are your Playwright tests pointing at the live HTTP server (e.g. `webServer.command` runs your real backend binary)? If yes, you're already following §2.1. If you're pointing at a standalone FE dev server (astro/vite/next dev alone), that's the anti-pattern — switch.
3. **If desktop (Wails)**: are your Playwright tests pointing at `wails dev` (port 34115) or at the standalone FE dev server (4321/5173)? If the latter, switch — your tests are missing the `window.go` IPC bridge and will silently pass against a bridgeless mock.
4. **If both web AND desktop**: do you have TWO Playwright configs, one per deployment target? Or are you only testing one and assuming the other works? Recommended: two configs, both CI-required.
5. **Component tests**: do you have a separate Vitest layer with `stubGlobal('window.go', ...)` mocks? Optional, but the right place for fast component-contract iteration. Playwright is for integration confidence; Vitest is for component contract.
6. **macOS visual smoke**: how are you covering the WKWebView fidelity gap? Manual click-through, hosted visual regression, or accepted gap with no coverage? Document the choice explicitly.

If your project doesn't have a frontend at all (pure CLI / MCP server / backend service), this doc doesn't apply — skip.

## 6. Update your project's FE personas

After adopting this doc, edit your FE agent personas (`.claude/agents/<project>-fe-*.md` or equivalent) so dispatched agents know the rule. Minimum updates:

- **FE builder persona** (e.g. `ta-fe-builder.md`): under "Accessibility baseline" / "FE Quality Rules", add a one-liner: *"a11y gate (`mage TemplatesA11y` or equivalent) runs Playwright + axe-core against the LIVE BACKEND — never the standalone frontend dev server. See `docs/playwright-live-backend-pattern.md`."* Optionally add a dedicated "Playwright + Live Backend" subsection citing the doc.
- **FE QA Falsification persona** (e.g. `ta-fe-qa-falsification.md`): under "FE Falsification Attacks", add a bullet: *"Playwright pointing at the wrong dev server — webServer.command must start the real backend (HTTP server binary or `wails dev`), not standalone Astro/Vite/Next dev. False-confidence gate; flag loudly."*
- **FE planning + FE QA proof personas**: optional add — they don't directly dispatch Playwright work, but pointing at the doc helps the agents reason about FE test scope when constructing plans / verifying claims.

ta's updates (commit ref noted in this doc's commit message) are the reference. Other projects: mirror that shape, adapted to your persona file naming.

## 7. Reference: how ta got this right by accident

ta is HTTP-server-only (no Wails, no Electron). Its Playwright integration started by needing to test the `ta serve` HTTP cascade browser. The `webServer.command: './bin/ta serve --port 4321'` config in `a11y/playwright.config.ts` runs the real binary against the real port. There was never a standalone frontend dev server to fall into the anti-pattern with — ta's templates are server-rendered Go html/template strings, not a separate Astro/Vite app.

So ta is the §2.1 reference implementation. The Wails-specific guidance (§2.2) is what other projects with desktop builds need to adopt.

See `docs/a11y-playwright-ci.md` for ta's full a11y pattern (Playwright config, axe-core integration, mage target, CI workflow).

## References

- [Wails official docs — wails dev](https://wails.io/docs/reference/cli/#dev)
- [Wails community Playwright integration discussions](https://github.com/wailsapp/wails/discussions)
- [Playwright webServer config](https://playwright.dev/docs/test-webserver)
- [Vitest stubGlobal for window.go mocks](https://vitest.dev/api/vi.html#vi-stubglobal)
- [WKWebView WebDriver support status (incomplete)](https://webkit.org/blog/13443/webdriver-bidi-prototype-in-webkit/)
