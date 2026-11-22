// e2e/fixtures.ts - what the page tests run on.
//
// server      a server of the test's own: zombies-server from build/e2e, with a data folder of its own, and the game
//             files, the engine and Plutonium's updater that global-setup.ts made. Its wine is the stand-in game
//             (build/e2e/standin, e2e/standin), and it serves the built page (web/dist). serverArgs adds to its
//             command line: test.use({ serverArgs: ['-max-players', '6'] }). gameFiles('game-t5') is the folder of
//             the game files of Black Ops alone, for -game-dir.
// player      opens the page as a new player: a browser context of their own, so a token of their own, with the
//             stand-in host (host.js) in place of the engine's. Or another tab of a player's (tab).
//
// The steps that many tests take are functions here too: makeRoom, joinRoom, playRoomGame. A failed test has the
// server's log and each tab's console attached.
import { type BrowserContext, expect, type Page, test as base, type TestInfo } from '@playwright/test';
import { type ChildProcessWithoutNullStreams, spawn } from 'node:child_process';
import { createSocket } from 'node:dgram';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:net';
import { join } from 'node:path';
import { createInterface } from 'node:readline';

const root = join(import.meta.dirname, '..');

export interface Server {
  /** The page's address. */
  url: string;
  /** The server's port: TCP for the page, UDP for the games. */
  port: number;
  /** The server's data folder (-data-dir). The engine is unpacked in its folder engine. */
  dataDir: string;
  /** Sends a line to the server's console: "<room code> <line>" goes to the room's game. */
  command(line: string): void;
  /** Waits for a line of the server's log that matches, from the start of the log, and returns it. */
  waitFor(pattern: RegExp, timeout?: number): Promise<string>;
  /** The server's log so far. */
  log(): string;
}

/** How a player's page opens. */
export interface Opening {
  /** More of the page's address, after ?name=: 'room=KXQM', 'autostart=1'. */
  query?: string;
  /** What the stand-in host's store has of a map, by map, where it is not all of it (host.js: makeStore). */
  maps?: Record<string, object>;
  /** Opens another tab of this page's player instead of a new player. */
  tab?: Page;
  /** Waits for the lobby to show, as it does unless false. */
  wait?: boolean;
}

// Whether the port is free for TCP, or for UDP.
const tcpFree = (port: number) => new Promise<boolean>((resolve) => {
  const s = createServer().once('error', () => resolve(false)).listen(port, () => s.close(() => resolve(true)));
});
const udpFree = (port: number) => new Promise<boolean>((resolve) => {
  const s = createSocket('udp4').once('error', () => resolve(false)).bind(port, () => s.close(() => resolve(true)));
});

// A port for the server, free for TCP and UDP, with four free UDP ports above it for its games.
async function freePort(): Promise<number> {
  for (;;) {
    const port = 20000 + 5 * Math.floor(Math.random() * 8000);
    const free = await Promise.all([tcpFree(port), ...[0, 1, 2, 3, 4].map((n) => udpFree(port + n))]);
    if (free.every(Boolean)) return port;
  }
}

async function startServer(dataDir: string, args: string[]):
  Promise<{ server: Server; proc: ChildProcessWithoutNullStreams }> {
  const files = process.env.ZOMBIES_E2E_FILES;
  if (!files) throw new Error('no ZOMBIES_E2E_FILES: global-setup.ts did not run');
  const port = await freePort();
  const proc = spawn(join(root, 'build', 'e2e', 'zombies-server'), [
    '-port', String(port), '-game-port', String(port + 1), '-max-games', '4', '-console', '',
    '-game-dir', join(files, 'game'), '-data-dir', dataDir, '-engine-file', join(files, 'engine.tar.gz'),
    '-web-page', join(root, 'web', 'dist'), '-wine', join(root, 'build', 'e2e', 'standin'), ...args,
  ], { env: { ...process.env, ZOMBIES_PLUTONIUM_URL: process.env.ZOMBIES_E2E_PLUTONIUM } });
  const lines: string[] = [];
  const waiting = new Set<() => void>();
  const ready = new Promise<void>((resolve, reject) => {
    for (const out of [proc.stdout, proc.stderr]) {
      createInterface({ input: out }).on('line', (line) => {
        lines.push(line);
        if (line.startsWith('server: players join on port')) resolve();
        for (const heard of waiting) heard();
      });
    }
    proc.once('exit', (code) => reject(new Error(`the server exited (${code}):\n${lines.join('\n')}`)));
  });
  await ready;
  const waitFor = (pattern: RegExp, timeout = 10_000) => new Promise<string>((resolve, reject) => {
    const look = () => {
      const line = lines.find((l) => pattern.test(l));
      if (line === undefined) return false;
      waiting.delete(look);
      clearTimeout(timer);
      resolve(line);
      return true;
    };
    const timer = setTimeout(() => {
      waiting.delete(look);
      reject(new Error(`the server's log has no line like ${pattern} after ${timeout} ms`));
    }, timeout);
    if (!look()) waiting.add(look);
  });
  return {
    proc,
    server: {
      url: `http://127.0.0.1:${port}`,
      port,
      dataDir,
      command: (line) => proc.stdin.write(line + '\n'),
      waitFor,
      log: () => lines.join('\n'),
    },
  };
}

// Stops the server the way docker stop does, and kills it if it takes too long.
async function stopServer(proc: ChildProcessWithoutNullStreams): Promise<void> {
  if (proc.exitCode !== null) return;
  const exited = new Promise((resolve) => proc.once('exit', resolve));
  proc.kill('SIGINT');
  const timer = setTimeout(() => proc.kill('SIGKILL'), 10_000);
  await exited;
  clearTimeout(timer);
}

// Keeps a log of a failed test in its results' folder, with its screenshots and trace, and attaches it.
async function keep(testInfo: TestInfo, name: string, text: string): Promise<void> {
  const file = testInfo.outputPath(name);
  writeFileSync(file, text + '\n');
  await testInfo.attach(name, { path: file, contentType: 'text/plain' });
}

// Each tab's console, as it came.
const consoles = new WeakMap<Page, string[]>();

/** What the page has said on its console so far. */
export function said(page: Page): string[] {
  return consoles.get(page) || [];
}

export const test = base.extend<{
  serverArgs: string[];
  server: Server;
  player: (name: string, opening?: Opening) => Promise<Page>;
}>({
  serverArgs: [[], { option: true }],

  server: async ({ serverArgs }, use, testInfo) => {
    const dataDir = mkdtempSync(join(process.env.ZOMBIES_E2E_FILES || '', 'server-'));
    const { server, proc } = await startServer(dataDir, serverArgs);
    await use(server);
    await stopServer(proc);
    if (testInfo.status !== testInfo.expectedStatus) await keep(testInfo, 'server.log', server.log());
    rmSync(dataDir, { recursive: true, force: true });
  },

  player: async ({ browser, server }, use, testInfo) => {
    const contexts: BrowserContext[] = [];
    const tabs: { name: string; page: Page }[] = [];
    await use(async (name, opening = {}) => {
      let context = opening.tab?.context();
      if (!context) {
        context = await browser.newContext();
        contexts.push(context);
        await context.route('**/player.js', (route) => route.fulfill({ path: join(import.meta.dirname, 'host.js'),
          contentType: 'text/javascript' }));
        if (opening.maps) await context.addInitScript((maps) => { globalThis.standinMaps = maps; }, opening.maps);
      }
      const page = await context.newPage();
      const log: string[] = [];
      consoles.set(page, log);
      page.on('console', (m) => log.push(m.text()));
      page.on('pageerror', (e) => log.push('error: ' + e.message));
      tabs.push({ name: tabs.some((t) => t.name === name) ? `${name} (tab ${tabs.length + 1})` : name, page });
      await page.goto(`${server.url}/?name=${encodeURIComponent(name)}${opening.query ? '&' + opening.query : ''}`);
      const lobby = page.locator('#outside:not(.hidden), #inside:not(.hidden)').first();
      if (opening.wait !== false) await expect(lobby).toBeVisible();
      return page;
    });
    if (testInfo.status !== testInfo.expectedStatus) {
      for (const t of tabs) await keep(testInfo, `${t.name}.log`, (consoles.get(t.page) || []).join('\n'));
    }
    for (const context of contexts) await context.close();
  },
});

declare global {
  // eslint-disable-next-line no-var
  var standinMaps: Record<string, object> | undefined;
}

/** A folder of the game files that global-setup.ts made: 'game', of both games, or 'game-t5', of Black Ops alone. */
export function gameFiles(folder: string): string {
  return join(process.env.ZOMBIES_E2E_FILES || '', folder);
}

/** Makes a room and returns its code. */
export async function makeRoom(page: Page): Promise<string> {
  await page.locator('#make').click();
  await expect(page.locator('#roomcode')).toHaveText(/^[A-Z]{4}$/);
  return (await page.locator('#roomcode').textContent()) || '';
}

/** Joins the room by its code. */
export async function joinRoom(page: Page, code: string): Promise<void> {
  await page.locator('#code').fill(code);
  await page.locator('#joincode').click();
  await expect(page.locator('#roomcode')).toHaveText(code);
}

/** Has the players say they are ready, and waits until each page is in the room's game. */
export async function playRoomGame(...pages: Page[]): Promise<void> {
  for (const page of pages) await page.locator('#ready').click();
  for (const page of pages) await expect(page.locator('#ready')).toHaveText('In game');
}

export { expect };

/** A player who has no page: the test says for them what a page says to the server's lobby (server/rooms.go). */
export interface ApiPlayer {
  token: string;
  /** Their key, which their Windows client joins with (Play.bat <host> <key>). */
  key: string;
  /** Says an action to the lobby, as the page does: 'create', 'ready', ... The lobby's answer. */
  say(action: string, body?: object): Promise<string>;
  /** Closes their event stream, as a page that closes does. */
  close(): void;
}

/**
 * A player the test plays through the lobby's API. Their event stream stays open, as a page's does, so that the lobby
 * takes them as there.
 */
export async function apiPlayer(server: { url: string }, name: string): Promise<ApiPlayer> {
  const lobby = server.url + '/lobby/';
  const post = async (action: string, body: object, token?: string) => (await fetch(lobby + action, {
    method: 'POST', body: JSON.stringify(body),
    headers: { 'Content-Type': 'application/json', ...(token && { Authorization: 'Bearer ' + token }) },
  })).text();
  const me = JSON.parse(await post('hello', { name }));
  const stop = new AbortController();
  const events = await fetch(lobby + 'events',
    { headers: { Authorization: 'Bearer ' + me.token }, signal: stop.signal });
  const reader = events.body!.getReader();
  await reader.read(); // its first event
  (async () => { while (!(await reader.read().catch(() => ({ done: true }))).done); })();
  return { token: me.token, key: me.key, say: (action, body = {}) => post(action, body, me.token),
    close: () => stop.abort() };
}
