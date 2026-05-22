import { defineConfig, devices } from '@playwright/test';

/**
 * ta a11y suite — runs against `ta serve` started by Playwright's
 * webServer config. Tests in tests/ verify WCAG 2 AA via axe-core
 * across the three planned viewports (375, 768, 1280).
 *
 * webServer.cwd is '..' so `./bin/ta serve` resolves to the built
 * binary at <repo-root>/bin/ta, and the implicit project root for
 * ta serve is the repo root itself (which has the .ta/ tree).
 */
export default defineConfig({
  testDir: './tests',
  timeout: 30_000,
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: 0,
  workers: 1,
  reporter: process.env.CI ? 'list' : 'html',
  webServer: {
    command: './bin/ta serve --port 4321',
    cwd: '..',
    url: 'http://127.0.0.1:4321/',
    timeout: 30_000,
    reuseExistingServer: !process.env.CI,
  },
  use: {
    baseURL: 'http://127.0.0.1:4321',
  },
  projects: [
    {
      name: 'mobile-375',
      use: { ...devices['Desktop Chrome'], viewport: { width: 375, height: 667 } },
    },
    {
      name: 'tablet-768',
      use: { ...devices['Desktop Chrome'], viewport: { width: 768, height: 1024 } },
    },
    {
      name: 'desktop-1280',
      use: { ...devices['Desktop Chrome'], viewport: { width: 1280, height: 800 } },
    },
  ],
});
