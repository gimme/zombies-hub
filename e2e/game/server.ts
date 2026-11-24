// e2e/game/server.ts - a server of the image (zombies-hub:test, which docker/test-games.sh builds), in a container of
// its own, run as Unraid runs it: PUID 99, PGID 100, the game files at /game, and its data at /config, which is the
// folder config in the test's results.
//
// Its port, TCP and UDP, is published at a free port of this PC's loopback, where the players' Chrome reaches it, and
// the page tells them that port (ZOMBIES_PUBLIC_PORT). Its settings are the tests': cheats (a test turns the zombies
// off, or moves a player), rooms' games that wait 150 s for their players, the game files sent at any rate, and rooms
// whose game starts with one player.
//
// ZOMBIES_TEST_GAME is what is mounted at /game: the game files' folder, or in Git Bash the volume docker/test-games.sh
// copied them into, as Wine can't run a game from a Windows folder (docker/host.sh: for_wine). The engine is the
// pinned build, which the server downloads, or ZOMBIES_ENGINE_FILE's, or ZOMBIES_ENGINE_URL's. On Windows the server
// unpacks it into the volume <container>-engine, for the same reason.
import { type ChildProcess, execFile, spawn } from 'node:child_process';
import { createHash, createPublicKey } from 'node:crypto';
import { createSocket } from 'node:dgram';
import { createWriteStream, mkdirSync, readFileSync, type WriteStream, writeFileSync } from 'node:fs';
import { type AddressInfo, createServer } from 'node:net';
import { isAbsolute, join, resolve } from 'node:path';
import { createInterface } from 'node:readline';
import { stopAsked } from './stop';

export const image = 'zombies-hub:test';

// The servers of this process that have yet to stop (stopAll), and whether stopAll has begun, after which none starts.
const running = new Set<Server>();
let allStopping = false;

/** The rooms' map, and the one a player plays alone (ZOMBIES_TEST_MAP, default Kino). */
export const map = process.env.ZOMBIES_TEST_MAP || 'zombie_theater';

/** The user that containers which write to the test's folders run as: this one, or the image's on Windows. */
export const user = process.getuid && process.getgid ? `${process.getuid()}:${process.getgid()}` : '1000:1000';

/** Runs docker: its exit code and output. */
export function docker(args: string[]): Promise<{ code: number; out: string; err: string }> {
  return new Promise((done) => {
    execFile('docker', args, { maxBuffer: 64 << 20, windowsHide: true }, (error, out, err) =>
      done({ code: error ? (typeof error.code === 'number' ? error.code : 1) : 0, out, err }));
  });
}

/** docker run's arguments that mount a folder of this PC's, or a volume, at a path in the container. */
export function mount(source: string, target: string, readonly = false): string[] {
  const folder = isAbsolute(source) || /^[A-Za-z]:[\\/]/.test(source);
  return ['--mount', `type=${folder ? 'bind' : 'volume'},src=${source},dst=${target}${readonly ? ',readonly' : ''}`];
}

/** docker run's arguments for the server's engine build (above). */
export function engineArgs(container: string): string[] {
  const args: string[] = [];
  if (process.env.ZOMBIES_ENGINE_URL) args.push('-e', `ZOMBIES_ENGINE_URL=${process.env.ZOMBIES_ENGINE_URL}`);
  const file = process.env.ZOMBIES_ENGINE_FILE;
  if (file) args.push(...mount(resolve(file), '/engine.tar.gz', true), '-e', 'ZOMBIES_ENGINE_FILE=/engine.tar.gz');
  if (process.platform === 'win32') args.push(...mount(`${container}-engine`, '/config/engine'));
  return args;
}

/** n ports of this PC's loopback that nothing listens on, TCP or UDP, each once. */
export async function freePorts(n: number): Promise<number[]> {
  const held: { close(): unknown }[] = [];
  const one = (): Promise<number> => new Promise((done, failed) => {
    const tcp = createServer().once('error', failed).listen(0, '127.0.0.1', () => {
      const port = (tcp.address() as AddressInfo).port;
      const udp = createSocket('udp4');
      held.push(tcp);
      udp.once('error', () => done(one())).bind(port, '127.0.0.1', () => {
        held.push(udp);
        done(port);
      });
    });
  });
  const ports: number[] = [];
  while (ports.length < n) ports.push(await one());
  for (const h of held) h.close();
  return ports;
}

const sleep = (ms: number) => new Promise((done) => setTimeout(done, ms));
const quote = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

// Mesa's compiled shaders, kept from one run to the next: without them a player's game under Wine takes minutes longer
// to join. A folder of build/, or on Windows a volume, as Wine can't run a game from a Windows folder.
const shaders = process.platform === 'win32' ? 'zombies-hub-shader-cache'
  : join(import.meta.dirname, '..', '..', 'build', 'shader-cache');

/**
 * docker run's arguments that give a player's game under Wine the shader cache (above), at /shader-cache. On Windows
 * the volume, as Docker makes it, is root's: the players' user (the image's, 1000) is given it.
 */
export async function shaderCache(playerImage: string): Promise<string[]> {
  if (process.platform === 'win32') {
    await docker(['run', '--rm', '-u', '0', '--entrypoint', 'chown', ...mount(shaders, '/cache'), playerImage,
      '1000:1000', '/cache']);
  }
  return [...mount(shaders, '/shader-cache'), '-e', 'MESA_SHADER_CACHE_DIR=/shader-cache'];
}

/** How a server starts. */
export interface ServerOptions {
  /** Settings over the tests' own: { ZOMBIES_MAX_PLAYERS: '6' }. An empty one is set empty. */
  env?: Record<string, string>;
  /** Without NET_ADMIN, which the image's firewall needs: its games then reach the network. */
  noFirewall?: boolean;
  /**
   * For Black Ops II games: on Windows, the server's copy of Plutonium and its games' homes are volumes,
   * <container>-plutonium and <container>-homes, as in docker/server.sh. From a Windows folder, Wine runs no game of
   * Plutonium's. The copy is downloaded once, into the volume, which stays for the next run.
   */
  t6?: boolean;
}

/** A row of a room's game's status: a player, with their address (127.1.x.y:port), points and ping. */
export interface StatusRow {
  address: string;
  points: number;
  ping: number;
  name: string;
}

export class Server {
  /** The server's log, a line each. */
  readonly lines: string[] = [];
  /** The page's address. */
  readonly url: string;
  /** The page's address through the proxy (startProxy): https://zombies.test:<port>. */
  readonly proxyUrl: string;
  /** The key of the proxy's certificate (SHA-256 of its SubjectPublicKeyInfo, base64), which players trust. */
  spki = '';
  private readonly waiters = new Set<() => void>();
  private readonly out: WriteStream;
  private exitCode: number | null = null;
  private readonly exited: Promise<number>;

  private constructor(readonly name: string, readonly dir: string, readonly port: number, readonly proxyPort: number,
    proc: ChildProcess) {
    this.url = `http://127.0.0.1:${port}`;
    this.proxyUrl = `https://zombies.test:${proxyPort}`;
    this.out = createWriteStream(join(dir, 'server.log'));
    for (const stream of [proc.stdout!, proc.stderr!]) {
      createInterface({ input: stream }).on('line', (line) => {
        this.lines.push(line);
        this.out.write(line + '\n');
        for (const heard of [...this.waiters]) heard();
      });
    }
    this.exited = new Promise((done) => proc.once('exit', (code) => {
      this.exitCode = code ?? 1;
      for (const heard of [...this.waiters]) heard();
      done(this.exitCode);
    }));
  }

  /**
   * Starts a server as container name, its log and data in dir, and waits until Docker finds it healthy and it has
   * listed what the map reads, which a browser downloads before it plays the map.
   */
  static async start(name: string, dir: string, options: ServerOptions = {}): Promise<Server> {
    const game = process.env.ZOMBIES_TEST_GAME;
    if (!game) throw new Error('no ZOMBIES_TEST_GAME: run the game tests with docker/test-games.sh');
    await docker(['rm', '-f', name, `${name}-proxy`]);
    mkdirSync(join(dir, 'config'), { recursive: true });
    const [port, proxyPort] = await freePorts(2);
    const env: Record<string, string> = {
      PUID: '99', PGID: '100', ZOMBIES_MAP: map, ZOMBIES_CHEATS: '1', ZOMBIES_LOAD_TIMEOUT: '150s',
      ZOMBIES_DOWNLOAD_RATE: 'unlimited', ZOMBIES_MIN_PLAYERS: '1', ZOMBIES_MAX_PLAYERS: '4',
      ZOMBIES_PUBLIC_PORT: String(port), ...options.env,
    };
    const t6: string[] = [];
    if (options.t6 && process.platform === 'win32') {
      t6.push(...mount(`${name}-plutonium`, '/plutonium'), '-e', 'ZOMBIES_PLUTONIUM_DIR=/plutonium',
        ...mount(`${name}-homes`, '/config/game'));
      // the volumes, as Docker makes them, are root's: the server's user (PUID:PGID) is given them
      await docker(['run', '--rm', '-u', '0', '--entrypoint', 'chown', ...t6, image, '99:100', '/plutonium',
        '/config/game']);
    }
    if (allStopping || stopAsked()) throw new Error(`no server is started as ${name}: the run is stopping`);
    const proc = spawn('docker', ['run', '--rm', '--name', name, '--network', 'bridge',
      '-p', `127.0.0.1:${port}:3075/tcp`, '-p', `127.0.0.1:${port}:3075/udp`, '-p', `127.0.0.1:${proxyPort}:443/tcp`,
      ...(options.noFirewall ? [] : ['--cap-add', 'NET_ADMIN']),
      ...Object.entries(env).flatMap(([k, v]) => ['-e', `${k}=${v}`]),
      ...mount(game, '/game', true), ...engineArgs(name), ...mount(join(dir, 'config'), '/config'), ...t6, image],
    { windowsHide: true });
    const server = new Server(name, dir, port, proxyPort, proc);
    running.add(server);
    await server.poll('Docker found the server healthy', 180_000, async () =>
      (await docker(['container', 'inspect', '-f', '{{.State.Health.Status}}', name])).out.trim() === 'healthy');
    await server.waitFor(new RegExp(`^files: ${map} reads [0-9]+ MB of the install: [0-9]+ files, 0 of its names ` +
      'not in the install'), 120_000);
    return server;
  }

  /** Where the log is now: waitFor and find from there look at what comes after. */
  mark(): number {
    return this.lines.length;
  }

  /** The first line of the log from line from on that matches. */
  find(pattern: RegExp, from = 0): string | undefined {
    for (let i = from; i < this.lines.length; i++) if (pattern.test(this.lines[i])) return this.lines[i];
    return undefined;
  }

  /** The lines of the log from line from on that match. */
  all(pattern: RegExp, from = 0): string[] {
    return this.lines.slice(from).filter((l) => pattern.test(l));
  }

  /** Waits for a line of the log from line from on that matches, and returns it; fails once the server has ended. */
  waitFor(pattern: RegExp, timeout = 60_000, from = 0): Promise<string> {
    return new Promise((done, failed) => {
      const look = () => {
        const line = this.find(pattern, from);
        if (line !== undefined || this.exitCode !== null) {
          this.waiters.delete(look);
          clearTimeout(timer);
          if (line !== undefined) done(line);
          else failed(new Error(`the server ended (exit code ${this.exitCode}) before a line like ${pattern}`));
        }
      };
      const timer = setTimeout(() => {
        this.waiters.delete(look);
        failed(new Error(`the server's log had no line like ${pattern} in ${timeout / 1000} s`));
      }, timeout);
      this.waiters.add(look);
      look();
    });
  }

  /** Runs check once a second until it gives something, and returns that; fails after timeout ms. */
  async poll<T>(what: string, timeout: number, check: () => Promise<T | false | null | undefined>): Promise<T> {
    for (const until = Date.now() + timeout; ;) {
      if (this.exitCode !== null) throw new Error(`the server ended (exit code ${this.exitCode}) before ${what}`);
      const got = await check();
      if (got) return got;
      if (Date.now() > until) throw new Error(`${what} took longer than ${timeout / 1000} s`);
      await sleep(1000);
    }
  }

  /** Runs a line on the server's console, as Unraid's Console button does: "<room> <line>" goes to the room's game. */
  async console(line: string): Promise<string> {
    return (await docker(['exec', this.name, 'console', line])).out;
  }

  /** The server's address on Docker's network, where players' games in containers of their own reach it. */
  async address(): Promise<string> {
    return (await docker(['container', 'inspect', '-f', '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}',
      this.name])).out.trim();
  }

  /** Runs a command in the server's container, as the user given: its exit code and output. */
  exec(command: string[], as = ''): Promise<{ code: number; out: string; err: string }> {
    return docker(['exec', ...(as ? ['-u', as] : []), this.name, ...command]);
  }

  /** The code of the room the player last made, as the log says. */
  roomOf(player: string): string | undefined {
    const made = this.all(new RegExp(`^server: ${quote(player)} made room [A-Z]+`)).at(-1);
    return made && /made room ([A-Z]+)/.exec(made)![1];
  }

  /** The address of the player's seat in the room's game (127.1.x.y), as the relay's log says. */
  seatOf(room: string, player: string): string | undefined {
    const joined = this.all(new RegExp(`^relay: .* \\(${quote(player)}, room ${room}\\) joined( over UDP)?, as ` +
      '(127\\.1\\.[0-9.]+):28960$')).at(-1);
    return joined && /, as (127\.1\.[0-9.]+):28960$/.exec(joined)![1];
  }

  /**
   * The players in the room's game, as its status lists them: "[ROOM]   0   500   50      0 Ann   9550
   * 127.1.0.1:28960   25397 25000" (num, score, ping, guid, name, lastmsg, address, qport, rate). A port above 32767
   * shows as a negative number, and the ping of a player who is loading is 999.
   */
  async status(room: string): Promise<StatusRow[]> {
    const rows: StatusRow[] = [];
    for (const line of (await this.console(`${room} status`)).split('\n')) {
      const m = /^(?:\[[A-Z]+\] )?\s*\d+\s+(-?\d+)\s+(\d+)\s+-?\d+\s+(.*?)\s+\d+\s+(127\.\d+\.\d+\.\d+):-?\d+\s/
        .exec(line);
      if (m) rows.push({ points: +m[1], ping: +m[2], name: m[3], address: m[4] });
    }
    return rows;
  }

  /** The player's row in the room's game's status, at their seat's address. */
  async rowOf(room: string, player: string): Promise<StatusRow | undefined> {
    const at = this.seatOf(room, player);
    return at ? (await this.status(room)).find((r) => r.address === at) : undefined;
  }

  /** Whether the room's game has the player in it, past loading (a ping under 999). */
  async inGame(room: string, player: string): Promise<boolean> {
    const row = await this.rowOf(room, player);
    return !!row && row.ping < 999;
  }

  /** Whether the room's game has the dvar at the value, as its console says. */
  async gameHas(room: string, dvar: string, value: string): Promise<boolean> {
    return (await this.console(`${room} ${dvar}`)).includes(`"${dvar}" is: "${value}"`);
  }

  /** Has the room's game spawn no zombies (ai_disableSpawn, a cheat), and waits until it does. */
  async noZombies(room: string): Promise<void> {
    await this.poll(`room ${room}'s game spawns no zombies`, 30_000, async () => {
      if (await this.gameHas(room, 'ai_disableSpawn', '1')) return true;
      if ((await this.console(`${room} ai_disableSpawn`)).includes('"ai_disableSpawn" is:')) {
        await this.console(`${room} set ai_disableSpawn 1`);
      }
      return false;
    });
  }

  /**
   * Nginx in front of the server, as a reverse proxy on its host adds HTTPS: at proxyUrl, with a certificate for
   * zombies.test whose key (spki) the players trust.
   */
  async startProxy(): Promise<void> {
    const dir = join(this.dir, 'proxy');
    mkdirSync(dir, { recursive: true });
    const made = await docker(['run', '--rm', '-u', user, ...mount(dir, '/proxy'), '-w', '/proxy', '--entrypoint', 'sh',
      image, '-c', 'openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -keyout key.pem ' +
      '-out cert.pem -days 1 -subj /CN=zombies.test -addext subjectAltName=DNS:zombies.test 2>/dev/null']);
    if (made.code) throw new Error(`the proxy's certificate could not be made: ${made.err}`);
    const key = createPublicKey(readFileSync(join(dir, 'cert.pem'))).export({ type: 'spki', format: 'der' });
    this.spki = createHash('sha256').update(key).digest('base64');
    writeFileSync(join(dir, 'default.conf'), `server {
    listen 443 ssl;
    ssl_certificate /proxy/cert.pem;
    ssl_certificate_key /proxy/key.pem;
    location / {
        proxy_pass http://127.0.0.1:3075;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_http_version 1.1;
    }
}
`);
    const started = await docker(['run', '-d', '--rm', '--name', `${this.name}-proxy`, '--network',
      `container:${this.name}`, ...mount(dir, '/proxy', true),
      ...mount(join(dir, 'default.conf'), '/etc/nginx/conf.d/default.conf', true), 'nginx:1.28.0-alpine']);
    if (started.code) throw new Error(`the proxy did not start: ${started.err}`);
  }

  /** Stops the proxy: the players that reach the server through it reach it no more. */
  async stopProxy(): Promise<void> {
    await docker(['stop', `${this.name}-proxy`]);
  }

  /**
   * Stops the server, as Docker stops a container, and fails unless it stopped cleanly, and its log has no packet of
   * a player's that the relay dropped, and no failed assert of a ' +
     'game's (games go on past one, as the shipped game did).
   * Stopping it again gives the same.
   */
  stop(): Promise<void> {
    this.stopped ??= this.stopNow().finally(() => {
      if (this.exitCode !== null) running.delete(this);
    });
    return this.stopped;
  }

  private stopped: Promise<void> | null = null;

  /**
   * Ends every server of this process that has yet to stop, each once a stop under way has ended, and starts none
   * after: for the end of a worker, whose tests a stopped run gave up on (fixtures.ts: leftovers).
   */
  static async stopAll(): Promise<void> {
    allStopping = true;
    for (const server of [...running]) {
      await server.stopped?.catch(() => {});
      await server.kill();
      await server.release();
    }
  }

  private async stopNow(): Promise<void> {
    if (this.exitCode !== null) throw new Error(`the server ended before the test did (exit code ${this.exitCode})`);
    const from = this.mark();
    await docker(['rm', '-f', `${this.name}-proxy`]);
    await docker(['stop', this.name]);
    const code = await Promise.race([this.exited, sleep(60_000).then(() => -1)]);
    await new Promise((done) => this.out.end(done));
    const problems: string[] = [];
    if (code !== 0 || !this.find(/^server: stopped$/, from)) {
      problems.push(`the server did not stop cleanly (exit code ${code})`);
    }
    const split = this.find(/ datagrams, not one \(/);
    if (split) problems.push(split);
    const dropped = this.find(/^relay: .*: (dropped a packet no client sends|sent more than [0-9]+ packets a second)/);
    if (dropped) problems.push(dropped);
    const asserts = [...new Set(this.all(/BO1_HEADLESS ASSERT: /))];
    if (asserts.length) problems.push(`${asserts.length} failed asserts in the games: ` +
      `${asserts.slice(0, 5).join('; ')}`);
    if (problems.length) throw new Error(problems.join('\n'));
  }

  /**
   * Ends the server at once. Its container is removed again while its docker run goes on, up to 3 times, 10 s apart:
   * a container docker run has yet to make when it is first removed is made after.
   */
  async kill(): Promise<void> {
    for (let i = 0; i < 3; i++) {
      await docker(['rm', '-f', `${this.name}-proxy`, this.name]);
      await Promise.race([this.exited, sleep(10_000)]);
      if (this.exitCode !== null) break;
    }
    if (this.exitCode !== null) running.delete(this);
    if (!this.out.closed) await new Promise((done) => this.out.end(done));
  }

  /** Gives what the server wrote to this user, once it has stopped: on Linux it is PUID's. */
  async release(): Promise<void> {
    if (process.platform === 'win32') return;
    await docker(['run', '--rm', '--entrypoint', 'chown', ...mount(this.dir, '/dir'), image, '-R', user, '/dir']);
  }
}
