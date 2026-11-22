// e2e/game/fixtures.ts - what the game tests run on.
//
// server      a server of the test's own (server.ts), in the container zombies-test-<n>, n the worker's slot: started
//             with the test, with serverOptions (test.use({ serverOptions: { env: { ZOMBIES_MAX_PLAYERS: '6' } } })),
//             and stopped as it ends, which fails the test unless the server stopped cleanly (Server.stop).
// servers     starts another server, zombies-test-<n>-<suffix>, which stops as the test ends.
// players     opens a player's page (player.ts) on the test's server: players.open('Ann'), players.open('Bob',
//             { query: 'room=KXQM' }), or on another's (players.openOn). Each player's Chrome closes as the test ends,
//             in the order they opened, one still opening once it has opened, and none opens after. Their logs are in
//             the test's results, with a screenshot of each page where the test failed, and a failed assert in a
//             page's engine fails the test.
// api         players who have no page: the test says for them what a page says to the lobby (api.player), and makes
//             a room whose game starts at once and waits for a player who never comes (api.room).
// leftovers   the worker's own, at its end: each player's Chrome of its tests that is still open closes, and each
//             server still running stops. A test does that itself as it ends; a run that is stopped (stop.ts) gives up
//             on what a test was doing, even a fixture's teardown, which goes on by itself. A Chrome still open as its
//             worker ends is ended with it, by force (Playwright's exit handlers, and on Windows the job Node puts each
//             process it starts in), which can leave the GPU's driver hung (chrome.ts).
import { test as base, expect } from '@playwright/test';
import { randomBytes } from 'node:crypto';
import { rmSync } from 'node:fs';
import { join } from 'node:path';
import { apiPlayer, type ApiPlayer } from '../fixtures';
import { profiles } from './chrome';
import { Player, type PlayerOptions } from './player';
import { map, Server, type ServerOptions } from './server';

export { expect, map, Player, Server };
export type { PlayerState } from './player';

export interface Players {
  /** Opens the page as a player of that name, on the test's server. */
  open(name: string, options?: PlayerOptions): Promise<Player>;
  /** Opens the page as a player of that name, on another server. */
  openOn(server: Server, name: string, options?: PlayerOptions): Promise<Player>;
}

export interface Servers {
  /** Starts another server, as container zombies-test-<n>-<suffix>. */
  start(suffix: string, options?: ServerOptions): Promise<Server>;
}

export interface Api {
  /** A player with no page, whose event stream stays open as a page's does. */
  player(name: string, server?: Server): Promise<ApiPlayer>;
  /**
   * A room of a player with no page, who says they have its map and are ready, as a page does: its game starts, and
   * waits for them. Returns the player and the room's code.
   */
  room(name: string): Promise<{ player: ApiPlayer; room: string }>;
}

export const test = base.extend<{ serverOptions: ServerOptions; server: Server; servers: Servers; players: Players;
  api: Api }, { leftovers: void }>({
  serverOptions: [{}, { option: true }],

  leftovers: [async ({}, use) => {
    await use();
    await Player.closeAll();
    await Server.stopAll();
  }, { scope: 'worker', auto: true }],

  server: async ({ serverOptions }, use, testInfo) => {
    const server = await Server.start(`zombies-test-${testInfo.parallelIndex}`, testInfo.outputPath('server'),
      serverOptions);
    await use(server);
    try {
      await server.stop();
    } finally {
      await server.release();
    }
  },

  servers: async ({}, use, testInfo) => {
    const started: Server[] = [];
    await use({
      start: async (suffix, options) => {
        const server = await Server.start(`zombies-test-${testInfo.parallelIndex}-${suffix}`,
          testInfo.outputPath(`server-${suffix}`), options);
        started.push(server);
        return server;
      },
    });
    for (const server of started) {
      await server.kill();
      await server.release();
    }
  },

  players: async ({ server }, use, testInfo) => {
    // the players' opens, as they began: an open under way as the test ends is waited for (a stopped test's goes on)
    const opening: Promise<Player | null>[] = [];
    let ended = false;
    const openOn = (on: Server, name: string, options: PlayerOptions = {}) => {
      if (ended) return Promise.reject(new Error(`${name} opens no page: the test has ended`));
      const id = `${testInfo.parallelIndex}-${name.replace(/\W/g, '')}-${randomBytes(4).toString('hex')}`;
      const profile = join(profiles, id);
      const log = testInfo.outputPath(`${name.replace(/\W/g, '')}.log`);
      const open = Player.open(name, options.proxy ? on.proxyUrl : on.url, profile, log, options,
        options.proxy ? { port: on.proxyPort, spki: on.spki } : undefined);
      opening.push(open.catch(() => null));
      return open;
    };
    await use({ open: (name, options) => openOn(server, name, options), openOn });
    ended = true;
    const failed = testInfo.status !== testInfo.expectedStatus;
    const problems: string[] = [];
    for (const open of opening) {
      const player = await open;
      if (!player) continue; // its open failed, and closed its Chrome
      if (failed || player.failure) await player.shot('failed');
      try {
        await player.close();
      } catch (e) {
        problems.push((e as Error).message);
      }
      rmSync(player.profile, { recursive: true, force: true, maxRetries: 3 });
      if (player.failure && !failed) problems.push(player.failure.message);
      const asserts = [...new Set(player.lines.filter((l) => l.startsWith('[bo1] assert ')))];
      if (asserts.length) problems.push(`${player.name}'s engine failed ${asserts.length} asserts: ` +
        `${asserts.slice(0, 5).join('; ')}`);
    }
    if (problems.length) throw new Error(problems.join('\n'));
  },

  api: async ({ server }, use) => {
    const made: ApiPlayer[] = [];
    await use({
      player: async (name, on = server) => {
        const player = await apiPlayer(on, name);
        made.push(player);
        return player;
      },
      room: async (name) => {
        const player = await apiPlayer(server, name);
        made.push(player);
        await player.say('create', { private: true });
        await player.say('files', { map, done: true }); // as a page that has the room's map in its store says
        const ready = await player.say('ready', { ready: true });
        if (ready.trim() !== '{}') throw new Error(`${name} could not say they were ready: ${ready}`);
        const room = await server.poll(`${name}'s room is in the log`, 10_000, async () => server.roomOf(name));
        return { player, room };
      },
    });
    for (const player of made) player.close();
  },
});
