// e2e/game.config.ts - the game tests (game/): real games against the server image, with the game files, played in
// this PC's Chrome on its GPU. docker/test-games.sh builds the images and runs them.
//
// Each test has a server of its own, in a container of its own, so the tests run four at a time. They are in four
// groups, each a project: smoke (what a deploy needs to work: a room's game to game over, playing alone offline and
// from the player's own install, downloads, the Windows client), engine (what the engine does in the page and as a
// room's game), long (minutes each: a revive, a door, six players, one at a time) and bo2 (Black Ops II's games, with
// Plutonium's game under Wine for each player: co-op, Grief and Turned, minutes each, one at a time).
// docker/test-games.sh runs smoke and engine unless told what to run.
import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: 'game',
  globalSetup: './game/setup.ts',
  outputDir: '../build/test/results',
  fullyParallel: true,
  workers: 4,
  retries: 0,
  // The waits in the tests have their own limits, which say what took too long.
  timeout: 30 * 60_000,
  expect: { timeout: 30_000 },
  // a click, a hover or a read of the page that waits longer has found nothing to act on
  use: { actionTimeout: 30_000, navigationTimeout: 60_000 },
  reporter: [['list', { printSteps: true }]],
  projects: [
    { name: 'smoke', testMatch: 'smoke/*.spec.ts' },
    { name: 'engine', testMatch: 'engine/*.spec.ts' },
    { name: 'long', testMatch: 'long/*.spec.ts' },
    { name: 'bo2', testMatch: 'bo2/*.spec.ts' },
  ],
});
