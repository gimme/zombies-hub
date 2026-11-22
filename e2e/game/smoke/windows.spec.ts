// A Windows player plays their room's game through the server's UDP port, as Play.bat does, with their key: the
// Windows client under Wine (docker/windows-player.sh, in the image zombies-hub-windows-player). Their client starts
// before they are in a room, and waits: for them to join one, then for the game to start, which they say they are
// ready for, as everyone does; round 1 waits for them. They close their game, which keeps their character but tells
// the lobby they quit, and the game runs on until they end it. They play again, and as game over ends the game their
// client is frozen for a few seconds, as one compiling shaders is: going again, it still hears it was game over, and
// quits saying so.
import { type ChildProcess, spawn } from 'node:child_process';
import { createWriteStream, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { docker, mount } from '../server';
import { expect, type Server, test } from '../fixtures';

const sleep = (ms: number) => new Promise((done) => setTimeout(done, ms));

// Mesa's compiled shaders, kept from one run to the next: without them the client takes minutes longer to join. A
// folder of build/, or on Windows a volume, as Wine can't run the game from a Windows folder.
const shaders = process.platform === 'win32' ? 'zombies-hub-shader-cache'
  : join(import.meta.dirname, '..', '..', '..', 'build', 'shader-cache');

/**
 * Windows client n, as container <server>-windows-<n>, its home (its settings and main/console_mp.log) windows-<n> in
 * the test's results, which joins the game of the room of the player with the key, on the server at its address on
 * Docker's network. Its engine is the one the server unpacked.
 */
class WindowsClient {
  readonly name: string;
  readonly home: string;
  readonly exited: Promise<number>;
  private readonly proc: ChildProcess;

  constructor(readonly n: number, results: string, server: Server, address: string, key: string) {
    this.name = `${server.name}-windows-${n}`;
    this.home = join(results, `windows-${n}`);
    const engine = process.platform === 'win32' ? `${server.name}-engine` : join(server.dir, 'config', 'engine');
    this.proc = spawn('docker', ['run', '--rm', '--name', this.name, '--network', 'bridge',
      ...mount(process.env.ZOMBIES_TEST_GAME!, '/game', true), ...mount(engine, '/engine', true),
      ...mount(results, '/out'), ...mount(shaders, '/shader-cache'), '-e', 'MESA_SHADER_CACHE_DIR=/shader-cache',
      'zombies-hub-windows-player', `/out/windows-${n}`, `${address}:3075`, key], { windowsHide: true });
    const log = createWriteStream(`${this.home}.log`);
    this.proc.stdout!.pipe(log);
    this.proc.stderr!.pipe(log);
    this.exited = new Promise((done) => this.proc.once('exit', (code) => done(code ?? 1)));
  }

  /** The client's log so far. */
  log(): string {
    try {
      return readFileSync(join(this.home, 'main', 'console_mp.log'), 'latin1');
    } catch {
      return '';
    }
  }

  /** Waits until the client's log has a line with the text. */
  async said(text: string, timeout: number): Promise<void> {
    for (const until = Date.now() + timeout; !this.log().includes(text);) {
      if (Date.now() > until) throw new Error(`the Windows client did not say "${text}" in ${timeout / 1000} s`);
      await sleep(1000);
    }
  }

  /** Whether its screen is more than one colour; a screenshot of it, <home>-<step>.png. */
  async sees(step: string): Promise<boolean> {
    const shot = `/out/windows-${this.n}-${step}.png`;
    if ((await docker(['exec', this.name, 'import', '-window', 'root', shot])).code) return false;
    const deviation = await docker(['exec', this.name, 'convert', shot, '-colorspace', 'gray', '-format',
      '%[fx:standard_deviation > 0.02]', 'info:']);
    return deviation.out.trim() === '1';
  }
}

test("a Windows player plays a room's game", async ({ server, api }, testInfo) => {
  const address = (await docker(['container', 'inspect', '-f',
    '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}', server.name])).out.trim();
  if (process.platform === 'win32') {
    // the volume, as Docker makes it, is root's: the client's user writes in it
    await docker(['run', '--rm', '-u', '0', '--entrypoint', 'chown', ...mount(shaders, '/cache'),
      'zombies-hub-windows-player', '1000:1000', '/cache']);
  }
  const win = await api.player('Win');
  const join1 = new WindowsClient(1, testInfo.outputDir, server, address, win.key);
  await join1.said('Waiting for you to join a room on the server\'s page...', 300_000);
  await win.say('create', { private: true });
  const room = await server.poll('Win made a room', 10_000, async () => server.roomOf('Win'));
  await join1.said('Waiting for the game to start...', 30_000);
  expect((await win.say('ready', { ready: true })).trim(), 'Win says they are ready').toBe('{}');
  // no zombies: Win stands still, as their game compiles its shaders, for longer than round 1's zombies take to kill
  // them. Said as the game has loaded, before Win is in it and round 1 begins.
  await server.waitFor(new RegExp(`^\\[${room}\\] ------- Game Initialization -------$`), 120_000);
  await server.noZombies(room);
  await server.poll('the Windows client is in the game', 300_000, () => server.inGame(room, 'Win'));
  await server.waitFor(new RegExp(`^\\[${room}\\] bo1_lobby: start$`), 150_000);
  const at = server.seatOf(room, 'Win')!;
  expect((await server.rowOf(room, 'Win'))?.name, 'the name the game knows the Windows player by').toContain('Win');

  // Mesa compiles each of the game's shaders the first time the game draws it, and the game waits: with none of them
  // in its cache, its first frames in the game take 20 s and more each, and meanwhile it sends nothing. So the game
  // is to have heard from it all through 10 s; its loading screen stays up meanwhile, which sees would take for the
  // game.
  const settled = async (what: string) => {
    let heardSince = 0;
    await server.poll(what, 180_000, async () => {
      if (!(await server.inGame(room, 'Win'))) {
        heardSince = 0;
        return false;
      }
      heardSince ||= Date.now();
      return Date.now() - heardSince >= 10_000;
    });
  };
  await settled('the Windows client compiled the game\'s shaders');
  await server.poll('the Windows client sees the game', 60_000, () => join1.sees('joined'));
  // its Graphics menu has the Frame Rate Limit where Screen Refresh Rate was, at 240 a second, the Windows client's
  // default (bo1-zombies/src/ui/ui_main.cpp: UI_FrameLimit_PatchMenus); Wine's display says no refresh rate of its own
  expect(join1.log(), "the Windows client's Frame Rate Limit").toMatch(new RegExp('^bo1_maxfps: menu ' +
    'options_graphics item [0-9]+ is the Frame Rate Limit, at 240: 30 60 120 144 240 @MPUI_RULES_UNLIMITED_CAPS ' +
    '\\(the display 0 Hz\\), [1-9][0-9]* labels', 'm'));

  // The player closes their game, as its window's close button does (WM_CLOSE): its client says disconnect, and the
  // game keeps Win's character for them, but tells the lobby they quit it. Their route ends then, not after a minute
  // of silence. Their room's game, which no one else is in, runs on, for them to come back to, until they end it.
  let from = server.mark();
  expect((await docker(['exec', join1.name, 'wine', 'taskkill', '/im', 'BO1Zombies.exe'])).code,
    "the Windows client's game window closed").toBe(0);
  const seat = at.replace(/\./g, '\\.');
  await server.waitFor(new RegExp(`^\\[${room}\\] bo1_lobby: quit ${seat}:28960$`), 30_000, from);
  // the game keeps Win's character as they quit, and the relay ends their route
  await server.waitFor(new RegExp(`^\\[${room}\\] Win quit the game: their character waits for them$`), 5000, from);
  await server.waitFor(new RegExp(`^relay: .* \\(Win, room ${room}\\) left \\(they quit the game\\)$`), 5000, from);
  await sleep(3000);
  expect(server.find(new RegExp(`^server: room ${room}'s game ends: `), from), 'the game, which Win quit, ran on')
    .toBeUndefined();
  expect((await win.say('end')).trim(), 'Win, its host, ends the game no one plays').toBe('{}');
  await server.waitFor(new RegExp(`^server: room ${room}'s game ends: Win ended the game\\.$`), 5000, from);
  await Promise.race([join1.exited, sleep(30_000).then(() => { throw new Error('the Windows client did not quit'); })]);

  // Win plays again, in their room, and as game over ends it, their game is busy for a few seconds (frozen: docker
  // pause, once it has compiled the game's shaders), as one compiling shaders is: what comes meanwhile fills its
  // socket's buffer, which drops the newest. Going again, it still hears it was game over, which the game tells its
  // players each second until they have (bo1-zombies/src/server_mp/sv_lobby_mp.cpp: SV_Lobby_TellOver), and quits
  // saying so, rather than a minute later, as one that lost the connection.
  const join2 = new WindowsClient(2, testInfo.outputDir, server, address, win.key);
  await join2.said('Waiting for the game to start...', 60_000);
  from = server.mark();
  expect((await win.say('ready', { ready: true })).trim(), 'Win says they are ready').toBe('{}');
  await server.waitFor(new RegExp(`^\\[${room}\\] ------- Game Initialization -------$`), 120_000, from);
  await server.noZombies(room);
  await server.poll('the Windows client is in the game again', 300_000, () => server.inGame(room, 'Win'));
  await server.waitFor(new RegExp(`^\\[${room}\\] bo1_lobby: start$`), 150_000, from);
  await settled('the Windows client compiled the game\'s shaders');
  expect((await docker(['pause', join2.name])).code, "the Windows client's game froze").toBe(0);
  await sleep(8000);
  from = server.mark();
  await server.console(`${room} fast_restart`);
  await server.waitFor(new RegExp(`^\\[${room}\\] bo1_lobby: over [0-9]+$`), 10_000, from);
  await sleep(4000);
  expect((await docker(['unpause', join2.name])).code, "the Windows client's game went again").toBe(0);
  await Promise.race([join2.exited, sleep(30_000).then(() => {
    throw new Error('the Windows client, going again, did not quit');
  })]);
  expect(join2.log(), 'the Windows client heard it was game over').toMatch(/^The game ended \(over\): /m);
  await server.waitFor(new RegExp(`^server: room ${room}'s game ended: quit$`), 10_000, from);
});
