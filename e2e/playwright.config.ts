// e2e/playwright.config.ts - the page tests. They need the server and the stand-in game built for Linux into
// build/e2e, and the page built into web/dist: docker/test-e2e.sh builds them and runs the tests in Docker, and the
// e2e job of .github/workflows/test.yml does the same without Docker.
//
// The tests run one at a time. Each test's server gives the players in its games the same loopback addresses
// (server/relay.go: acquire), so two servers on one machine can't both have a game.
import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: 'tests',
  globalSetup: './global-setup.ts',
  outputDir: '../build/e2e/results',
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: 0,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  reporter: process.env.CI ? [['list'], ['github']] : 'list',
  use: {
    browserName: 'chromium',
    // The page's service worker would answer the page's requests itself, where the tests don't see them, and the
    // tests serve the stand-in host in place of /player.js.
    serviceWorkers: 'block',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
});
