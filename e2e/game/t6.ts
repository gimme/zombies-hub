// e2e/game/t6.ts - Black Ops II players for the game tests (bo2/), a way to steer their games, and the steps that the
// tests share.
//
// A player is a container of the image zombies-hub-t6-player (docker/t6-player.sh) beside the test's server, on
// Docker's network: the player's page and their game at once, as one PC has them. The test says for the player what
// their page says to the lobby (T6Player.say), and the container says it, so that the server expects the player's
// game at the container's address, where the game comes from (server/rooms.go: player.addr). The player joins their
// room's Black Ops II games by hand, as a player without the launcher does: their game starts each time their room's
// game is up, and closes once it is no longer up. Its game is the server's own copy of Plutonium (Server.plutonium),
// under Wine.
//
// steer puts steer.gsc in each Black Ops II game that the server starts from then on, which lets the test steer the
// game from its console. The script goes in the game's home before the game loads it.
import { expect } from '@playwright/test';
import { type ChildProcess, spawn } from 'node:child_process';
import { createWriteStream } from 'node:fs';
import { join } from 'node:path';
import { createInterface } from 'node:readline';
import type { Leaderboards, MapBoards, View } from '../../web/src/api';
import { docker, mount, type Server, shaderCache } from './server';

const image = 'zombies-hub-t6-player';

const sleep = (ms: number) => new Promise((done) => setTimeout(done, ms));

export class T6Player {
  /** What the container printed, a line each. */
  readonly lines: string[] = [];
  readonly exited: Promise<number>;
  private exitCode: number | null = null;
  private readonly proc: ChildProcess;

  private constructor(readonly name: string, readonly container: string, out: string, args: string[]) {
    this.proc = spawn('docker', args, { windowsHide: true });
    const log = createWriteStream(join(out, `${name}.log`));
    for (const stream of [this.proc.stdout!, this.proc.stderr!]) {
      createInterface({ input: stream }).on('line', (line) => {
        this.lines.push(line);
        log.write(line + '\n');
      });
    }
    this.exited = new Promise((done) => this.proc.once('exit', (code) => {
      this.exitCode = code ?? 1;
      log.end();
      done(this.exitCode);
    }));
  }

  /**
   * Starts the player, as container <server>-t6-<name>, and waits until they are in the server's lobby. Their logs go
   * to out: the container's, <name>.log, and each game's, <name>-game-<n>.log.
   */
  static async start(server: Server, name: string, out: string): Promise<T6Player> {
    const container = `${server.name}-t6-${name.toLowerCase().replace(/\W/g, '')}`;
    const [, shaders, address] = await Promise.all([docker(['rm', '-f', container]), shaderCache(image),
      server.address()]);
    const player = new T6Player(name, container, out, ['run', '--rm', '--name', container, '--network', 'bridge',
      ...mount(process.env.ZOMBIES_TEST_GAME!, '/game', true), ...mount(server.plutonium, '/plutonium', true),
      ...mount(out, '/out'), ...shaders, image, 'play', '/out', address, name]);
    await player.waitFor(new RegExp(`^t6-player: ${name} is in the lobby$`), 60_000);
    return player;
  }

  /** Waits for a line that the container prints; fails once the container has ended. */
  async waitFor(pattern: RegExp, timeout: number): Promise<string> {
    for (const until = Date.now() + timeout; ;) {
      const line = this.lines.find((l) => pattern.test(l));
      if (line) return line;
      if (this.exitCode !== null) {
        throw new Error(`${this.name}'s container ended (exit code ${this.exitCode}) before a line like ${pattern}`);
      }
      if (Date.now() > until) throw new Error(`${this.name}'s container printed no line like ${pattern} in ` +
        `${timeout / 1000} s`);
      await sleep(500);
    }
  }

  /** Says to the lobby what the player's page says, from the player's container, and returns the lobby's answer. */
  async say(action: string, body: object = {}): Promise<string> {
    const said = await docker(['exec', this.container, 't6-player.sh', 'say', action, JSON.stringify(body)]);
    if (said.code) throw new Error(`${this.name} could not say ${action}: ${said.err}`);
    return said.out;
  }

  /** What the lobby last sent the player. */
  async view(): Promise<View> {
    return JSON.parse((await docker(['exec', this.container, 't6-player.sh', 'view'])).out);
  }

  /** The player's token, which the lobby knows them by. */
  async token(): Promise<string> {
    return (await docker(['exec', this.container, 'cat', '/home/game/player/token'])).out.trim();
  }

  /** A screenshot of the player's screen, <name>-<step>.png in out. */
  async shot(step: string): Promise<void> {
    await docker(['exec', this.container, 'import', '-window', 'root', `/out/${this.name}-${step}.png`]);
  }

  /** Ends the player's container. */
  async stop(): Promise<void> {
    await docker(['rm', '-f', this.container]);
    await Promise.race([this.exited, sleep(10_000)]);
  }
}

/**
 * The first player makes a room on the map, which each of settings changes then (a call of the host's: room), and the
 * others join it. Returns the room's code.
 */
export async function makeRoom(server: Server, players: T6Player[], map: string, ...settings: object[]):
Promise<string> {
  const [host, ...others] = players;
  expect((await host.say('create', { map, private: true })).trim(), `${host.name} makes a room`).toBe('{}');
  for (const setting of settings) {
    expect((await host.say('room', setting)).trim(), `${host.name} sets ${JSON.stringify(setting)}`).toBe('{}');
  }
  const room = await server.poll(`${host.name} made a room`, 10_000, async () => server.roomOf(host.name));
  for (const player of others) {
    expect((await player.say('join', { room })).trim(), `${player.name} joins it`).toBe('{}');
  }
  return room;
}

/**
 * The room's players say that they are ready, and its game starts. Returns once the game has begun, each player's game
 * has settled in it (settled), and steer.gsc reports them all, with check holding for its report (reported): the
 * report, and where in the server's log the game began to start.
 */
export async function playGame(server: Server, room: string, players: T6Player[],
  check?: (rows: SteerRow[]) => boolean): Promise<{ from: number; rows: SteerRow[] }> {
  const from = server.mark();
  for (const player of players) {
    expect((await player.say('ready', { ready: true })).trim(), `${player.name} is ready`).toBe('{}');
  }
  // Plutonium, downloaded the first time, and the map
  await server.waitFor(new RegExp(`^server: room ${room}'s game is up: its players join it$`), 600_000, from);
  // Mesa compiles a shader the first time the game draws it, and the player's game sends nothing meanwhile: the first
  // time, past the minute that the room's game waits for it before it drops it
  await server.console(`${room} set sv_timeout 600`);
  await server.waitFor(new RegExp(`^server: room ${room}'s game has begun$`), 900_000, from);
  const names = players.map((p) => p.name);
  await settled(server, room, names);
  return { from, rows: await reported(server, room, names, check) };
}

/** Waits until the script says that the room's game is over ("over <said>"), and the server has its result. */
export async function gameOver(server: Server, room: string, said: string, from: number): Promise<void> {
  await server.waitFor(new RegExp(`^\\[${room}\\] zombies_lobby: over ${said}$`), 60_000, from);
  await server.waitFor(new RegExp(`^server: room ${room}'s game is over$`), 10_000, from);
}

/**
 * The players in the room's T6 game, as its status lists them, which has a column more than a T5 game's (server.ts:
 * status): "[ROOM]   0   500   0   31   9D21   Ann   2100   127.1.0.7:28960  62039 20000" (num, score, bot, ping,
 * guid, name, lastmsg, address, qport, rate). A player still connecting shows CNCT for their ping, and is left out.
 */
async function t6Status(server: Server, room: string): Promise<{ name: string; ping: number }[]> {
  const rows: { name: string; ping: number }[] = [];
  const row = new RegExp(`^\\[${room}\\]\\s+\\d+\\s+-?\\d+\\s+\\d+\\s+(\\d+)\\s+\\S+\\s+(.*?)\\s+\\d+\\s+` +
    '127\\.\\d+\\.\\d+\\.\\d+:-?\\d+\\s');
  for (const line of (await server.console(`${room} status`)).split('\n')) {
    const m = row.exec(line);
    if (m) rows.push({ ping: +m[1], name: m[2] });
  }
  return rows;
}

/**
 * Waits until each player is in the room's game and has been heard from all through 10 s: past the minutes that
 * their games compile shaders in, the first time.
 */
async function settled(server: Server, room: string, names: string[]): Promise<void> {
  let heardSince = 0;
  await server.poll(`${names.join(', ')} settled in room ${room}'s game`, 900_000, async () => {
    const rows = await t6Status(server, room);
    if (!names.every((name) => rows.some((r) => r.name === name && r.ping < 999))) {
      heardSince = 0;
      return false;
    }
    heardSince ||= Date.now();
    return Date.now() - heardSince >= 10_000;
  });
}

/** A player as steer.gsc reports them (steer_report). */
export interface SteerRow {
  name: string;
  team: string;
  down: boolean;
  zombie: boolean;
  score: number;
}

/**
 * Each player of the room's game, as steer.gsc reports them, and whether the level has started (past its black screen
 * at the start). No one, where the game's level does not run yet, and its script answers nothing.
 */
export async function steerReport(server: Server, room: string): Promise<{ started: boolean; rows: SteerRow[] }> {
  const from = server.mark();
  await server.console(`${room} set steer_report 1`);
  const end = await server.waitFor(new RegExp(`^\\[${room}\\] zombies_steer: reported [0-9]+ players, the level `),
    5000, from).catch(() => '');
  const line = new RegExp(`^\\[${room}\\] zombies_steer: (.+) team (\\S+) (up|down) (human|zombie) score (-?\\d+)$`);
  return {
    started: end.endsWith(' started'),
    rows: end ? server.all(line, from).map((l) => {
      const m = line.exec(l)!;
      return { name: m[1], team: m[2], down: m[3] === 'down', zombie: m[4] === 'zombie', score: +m[5] };
    }) : [],
  };
}

/**
 * Waits until steer.gsc reports each player, in a level that has started, and returns its report. A player's game that
 * has settled (settled) may still be loading the map, which the room's game waits for before its level goes on. Grief
 * watches its teams once the level has started. check, where given, is to hold for the report too.
 */
async function reported(server: Server, room: string, names: string[],
  check: (rows: SteerRow[]) => boolean = () => true): Promise<SteerRow[]> {
  return server.poll(`room ${room}'s game reports ${names.join(', ')}, started`, 300_000, async () => {
    const { started, rows } = await steerReport(server, room);
    return started && names.every((name) => rows.some((r) => r.name === name)) && check(rows) && rows;
  });
}

/** The leaderboards' boards of rounds of Black Ops II, as the player's page reads them. */
export async function t6Boards(server: Server, player: T6Player): Promise<MapBoards[]> {
  // read from here: unlike a page's other calls, the leaderboards' do not move where the server expects their game
  const boards: Leaderboards = await (await fetch(`${server.url}/lobby/leaderboards`,
    { headers: { Authorization: 'Bearer ' + await player.token() } })).json();
  return boards.titles.find((t) => t.id === 't6')!.rounds.maps;
}

/** Checks that the server keeps no records, and has no leaderboards of the maps whose names start with maps. */
export async function noRecords(server: Server, player: T6Player, maps: string): Promise<void> {
  const boards = await t6Boards(server, player);
  expect(boards.filter((m) => m.map.startsWith(maps)), `no leaderboards of ${maps}*`).toEqual([]);
  expect(boards.flatMap((m) => [...m.duo, ...m.squad, ...m.bigSquad]), 'no records').toEqual([]);
}

/**
 * Puts steer.gsc in each Black Ops II game of the server's from now on (above): a shell in the server's container
 * copies it into every game's home that lacks it, or has another, five times a second. A game's home has its scripts
 * from when the server prepares the game, seconds before Plutonium's game loads them.
 */
export async function steer(server: Server): Promise<void> {
  const copied = await docker(['cp', join(import.meta.dirname, 'bo2', 'steer.gsc'), `${server.name}:/tmp/steer.gsc`]);
  if (copied.code) throw new Error(`steer.gsc could not be copied into the server's container: ${copied.err}`);
  const started = await docker(['exec', '-d', '-u', '0', server.name, 'sh', '-c', 'while :; do ' +
    'for d in /config/game/*/storage/t6/scripts/zm; do ' +
    '[ -d "$d" ] && ! cmp -s /tmp/steer.gsc "$d/steer.gsc" && cp /tmp/steer.gsc "$d/steer.gsc"; ' +
    'done; sleep 0.2; done']);
  if (started.code) throw new Error(`steer.gsc's copying did not start: ${started.err}`);
}
