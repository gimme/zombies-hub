// e2e/game/player.ts - a player on the server's page, in a Chrome of their own (chrome.ts), playing with the real
// engine.
//
// The player's log (<name>.log in the test's results) has the page's console, a line each, with "test: " lines for
// what the test has them do, and "page: status: " and "page: error: " for what the page's overlay shows. An error
// there (the engine stopped or crashed), a graphics error, a crash of the page and a Chrome that went away fail what
// the player waits for from then on (failure). The engine's failed asserts, which the game goes on past, fail the test
// at its end (fixtures.ts).
//
// What a test reads of the game, it asks the engine on its console, through Module.bo1Command: bo1_player (ask) says
// where the player is, what they hold, and what the aim assist sees; a dvar's name says its value (dvar).
//
// Chrome draws the game here at up to 30 frames a second (ZOMBIES_TEST_FPS; 0 for the display's rate, which keeps a
// core or two, and the GPU, busy with a game no one watches), and without anti-aliasing unless the test says.
import { type Browser, type BrowserContext, chromium, type ConsoleMessage, type Page } from '@playwright/test';
import { type ChildProcess, execFileSync, spawn } from 'node:child_process';
import { createWriteStream, existsSync, type WriteStream, writeFileSync } from 'node:fs';
import { chrome, chromeExecutable, chromeLeft, stuckNote } from './chrome';

const sleep = (ms: number) => new Promise((done) => setTimeout(done, ms));

/** How a player's page opens. */
export interface PlayerOptions {
  /** More of the page's address, after ?name=<name>: 'room=KXQM', 'autostart=1'. */
  query?: string;
  /** More of the engine's command line (the page's args=): '+set bo1_web_movieAtLoad 1'. */
  args?: string;
  /** Through the server's proxy (Server.startProxy), at https://zombies.test/, as from another machine. */
  proxy?: boolean;
  /** The game draws with the anti-aliasing its settings pick (a new player's 4x), as a player's does. */
  antiAliasing?: boolean;
  /** A gamepad, a simulated one (PAD), which the page sees once a button of it is pressed. */
  gamepad?: boolean;
  /** The player's game folder, which the page's Import button picks: a folder of this PC's. */
  importFrom?: string;
  /**
   * A page behind another tab is hidden, and Chrome throttles it, as a player's does (cover). Chrome is then started
   * by hand, and joined without Playwright's overrides: Playwright's switches keep a page behind another tab running
   * as one in front, and its focus emulation keeps every page shown.
   */
  throttled?: boolean;
  /** An error on the page's overlay is the test's to check, and fails nothing by itself. */
  errors?: boolean;
}

/** The player in the game, as bo1_player says (ask). */
export interface PlayerState {
  /** 0 playing, 6 down (last stand), 7 down and being revived. */
  pmType: number;
  origin: [number, number, number];
  pitch: number;
  yaw: number;
  weapon: string;
  clip: number;
  stock: number;
  /** The client holds forward (W) now. */
  forward: boolean;
  /** The aim assist's targets on screen, and the nearest the crosshair: where (-1 to 1, right and down), how far. */
  aimTargets: number;
  nearest: { x: number; y: number; distance: number };
  aimRange: number;
  autoAimRange: number;
  /** The view slows over a target. */
  slowdown: boolean;
  /** The auto aim's target, and the lock-on's (-1: none). */
  autoAim: number;
  lockOn: number;
  /** The target closest to the player. */
  closest: { x: number; y: number; distance: number };
  /** The commands that asked for a knife's lunge, and the last one's distance; those that pressed melee. */
  lunges: number;
  lungeDist: number;
  melees: number;
  /** The arms of the player's character (viewmodel_...). */
  arms: string;
  /** The scoreboard shows, or fades out. */
  scores: boolean;
  /** The zombies' anim commands the client lost. */
  animLost: number;
}

/** The simulated gamepad's buttons and axes, in the standard mapping (an Xbox controller's). */
export const PAD = { A: 0, B: 1, X: 2, Y: 3, LB: 4, RB: 5, LT: 6, RT: 7, BACK: 8, START: 9, LS: 10, RS: 11, UP: 12,
  DOWN: 13, LEFT: 14, RIGHT: 15, LX: 0, LY: 1, RX: 2, RY: 3 } as const;

/** The browser's memory: its ' +
  'pages' processes and its GPU process (their working sets), in GB, and the engine's threads. */
export interface Memory {
  renderer: number;
  gpu: number;
  workers: number;
}

interface Listener {
  pattern: RegExp;
  done: (line: string | false) => void;
  failed: (error: Error) => void;
}

declare global {
  interface Window {
    bo1PlayerStatus(id: string, error: boolean, text: string): Promise<void>;
    bo1SoundStarted(index: number, count: number): Promise<void>;
    bo1ChromeLetsGo(): void;
    bo1Pad: { rumbles: { at: number; duration?: number; strongMagnitude?: number; weakMagnitude?: number }[];
      plug(on: boolean): void; button(b: number, value: number): void; axis(a: number, value: number): void };
    bo1MusicGain?: GainNode;
    bo1AnimationFrames: number;
    Module: {
      bo1Command(command: string): void;
      ccall(name: string, ret: string, types: string[], args: unknown[]): number;
    };
  }
  interface BaseAudioContext {
    bo1Buffers?: AudioBuffer[];
  }
  interface AudioNode {
    bo1Fed?: boolean;
  }
}

export class Player {
  /** The player's log, a line each. */
  readonly lines: string[] = [];
  /** What failed the player: what they wait for fails with it. */
  failure: Error | null = null;
  page!: Page;
  context!: BrowserContext;
  /** The room the page last said it is in. */
  room = '';
  /** The page's loads: the first, and one each time it reloads. A game's end leaves it loaded (inPlace). */
  loads = 0;
  /** The games' GPU devices the browser collected, as the page says (inPlace). */
  devicesGone = 0;
  /** The times the page asked, in Chrome's dialog, before it left its game (web/src/screen.ts: beforeunload). */
  askedToLeave = 0;
  /** The anti-aliasing the game's back buffer has, as the renderer says: "4x anti-aliasing". */
  antiAliasing = '';
  /** The game's menu and lobby sounds the page started, by alias, in order (web/src/sounds.ts). */
  readonly soundsStarted: string[] = [];
  /** The browser's memory in the lobby before the first game, which inPlace compares with. */
  lobbyMemory: Memory | null = null;
  /** The page's requests for the game files, and whether the engine ran by then (checkReads). */
  readonly reads: { path: string; pack: boolean; byGame: boolean }[] = [];
  private engineRuns = false;
  private soundNames: string[] = [];
  private readonly listeners = new Set<Listener>();
  private readonly answers = new Map<Page, (line: string) => void>();
  private readonly heard = new Set<string>();
  private readonly out: WriteStream;
  private closing = false;
  // a Chrome started by hand (throttled), and Playwright's connection to it
  private browser: Browser | null = null;
  private chromeProcess: ChildProcess | null = null;

  private constructor(readonly name: string, readonly profile: string, logFile: string) {
    this.out = createWriteStream(logFile);
  }

  /** Opens the page at url (the server's, with ?name= and the options' query) in a Chrome of the player's own. */
  static async open(name: string, base: string, profile: string, logFile: string, options: PlayerOptions = {},
    proxy?: { port: number; spki: string }): Promise<Player> {
    if (existsSync(stuckNote)) {
      throw new Error(`no Chrome is started: the GPU's driver holds an earlier Chrome's processes (${stuckNote}): ` +
        'restart the machine');
    }
    const p = new Player(name, profile, logFile);
    const url = new URL(base);
    url.search = `name=${encodeURIComponent(name)}${options.query ? '&' + options.query : ''}`;
    const fps = Number(process.env.ZOMBIES_TEST_FPS ?? 30);
    const args = [options.args, options.antiAliasing ? '' : '+set r_aaSamples 1', fps ? `+set com_maxfps ${fps}` : '']
      .filter(Boolean).join(' ');
    if (args) url.searchParams.set('args', args);
    const switches = ['--autoplay-policy=no-user-gesture-required',
      ...(proxy ? [`--ignore-certificate-errors-spki-list=${proxy.spki}`,
        '--host-resolver-rules=MAP zombies.test 127.0.0.1'] : [])];
    if (options.throttled) {
      await p.startByHand(switches);
    } else {
      p.context = await chromium.launchPersistentContext(profile, {
        ...chrome(), headless: true, viewport: { width: 1280, height: 720 }, args: switches });
    }
    p.context.on('close', () => {
      if (!p.closing) p.fail(new Error(`${name}'s Chrome went away`));
    });
    await p.instrument(options);
    // a tab of its own, not the one Chrome opens with, which is not the active one: the page could not capture the
    // mouse in it ("The root document of this element is not valid for pointer lock"), nor go full screen
    const first = p.context.pages();
    p.page = await p.context.newPage();
    for (const page of first) await page.close();
    if (p.browser) await p.page.setViewportSize({ width: 1280, height: 720 });
    p.watch(p.page, '');
    p.page.on('load', () => p.loads++);
    p.page.on('crash', () => p.fail(new Error(`${name}'s page crashed`)));
    p.page.on('request', (request) => {
      const u = new URL(request.url());
      const pack = /^\/maps\/[^/]+\/pack$/.test(u.pathname);
      if (!pack && (!u.pathname.startsWith('/game/') || u.pathname.endsWith('/manifest.txt'))) return;
      p.reads.push({ path: u.pathname, pack, byGame: p.engineRuns });
    });
    // Chrome's dialog as the page leaves its game, which the player accepts, as one who means to leave does; the
    // page asks nothing else in a dialog of Chrome's, but in its own (web/src/question.ts: askInPage)
    p.page.on('dialog', (dialog) => {
      if (dialog.type() !== 'beforeunload') {
        p.note(`the page asked in Chrome's ${dialog.type()} dialog, not its own: ${dialog.message()}`);
        return dialog.dismiss();
      }
      p.askedToLeave++;
      p.note('the page asked before it left the game');
      return dialog.accept();
    });
    await p.page.goto(url.toString());
    return p;
  }

  // Starts Chrome by hand, headless, without Chrome's own work in the background, as Playwright starts it, and joins
  // it on the address its log says (throttled).
  private async startByHand(switches: string[]): Promise<void> {
    const quiet = ['--disable-background-networking', '--disable-component-update',
      '--disable-client-side-phishing-detection', '--disable-default-apps', '--disable-sync',
      '--metrics-recording-only',
      '--disable-features=OptimizationGuideOnDeviceModel,OptimizationGuideModelExecution,' +
      'OptimizationGuideModelDownloading,OptimizationHints'];
    const proc = spawn(chromeExecutable(), ['--headless', '--no-first-run', '--no-default-browser-check', ...quiet,
      '--mute-audio', `--user-data-dir=${this.profile}`, '--remote-debugging-port=0', ...switches, 'about:blank'],
    { stdio: ['ignore', 'ignore', 'pipe'], windowsHide: true });
    this.chromeProcess = proc;
    const endpoint = await new Promise<string>((done, failed) => {
      let text = '';
      const read = (data: Buffer) => {
        text += data;
        const m = /DevTools listening on (ws:\S+)/.exec(text);
        if (!m) return;
        proc.stderr!.off('data', read).resume();
        done(m[1]);
      };
      proc.stderr!.on('data', read);
      proc.once('exit', (code) => failed(new Error(`${this.name}'s Chrome ended (${code}): ${text.slice(-500)}`)));
    });
    // noDefaults is Playwright's, though its types lack it: no focus emulation, which keeps every page shown
    const noDefaults = { noDefaults: true } as {};
    this.browser = await chromium.connectOverCDP(endpoint, noDefaults);
    this.context = this.browser.contexts()[0];
  }

  /**
   * Puts another tab in front of the page, which hides it, as a player who switches tabs does (throttled), and returns
   * what puts the page in front again.
   */
  async cover(): Promise<() => Promise<void>> {
    if (!this.browser) throw new Error(`${this.name}'s pages always show: Playwright started their Chrome (throttled)`);
    const tabs = await this.browser.newBrowserCDPSession();
    const own = await this.context.newCDPSession(this.page);
    const { targetInfo } = await own.send('Target.getTargetInfo');
    await own.detach();
    const { targetId } = await tabs.send('Target.createTarget', { url: 'about:blank' });
    await tabs.send('Target.activateTarget', { targetId });
    return async () => {
      await tabs.send('Target.activateTarget', { targetId: targetInfo.targetId });
      await tabs.send('Target.closeTarget', { targetId });
      await tabs.detach();
    };
  }

  // What every page of the player's has: Chrome's rule for the mouse, the overlay's status, the sounds it plays, and
  // the options' gamepad and game folder.
  private async instrument(options: PlayerOptions): Promise<void> {
    // The overlay's status line (web/src/screen.ts: setStatus): each change goes to the log, and an error fails the
    // player. A status the page shows just as it reloads can miss the binding, so the page keeps each in its
    // sessionStorage until the binding has it, and the page it loads next sends again what is left.
    await this.context.exposeFunction('bo1PlayerStatus', (id: string, error: boolean, text: string) => {
      if (this.heard.has(id)) return;
      this.heard.add(id);
      if (!text) return;
      this.log(`page: ${error ? 'error' : 'status'}: ${text}`);
      if (error && !options.errors) this.fail(new Error(`${this.name}'s page shows an error: ${text}`));
    });
    await this.context.addInitScript(() => {
      const key = 'zombies-player-status';
      const unheard = (): [string, boolean, string][] => {
        try {
          return JSON.parse(sessionStorage.getItem(key) || '[]');
        } catch {
          return [];
        }
      };
      const keep = (statuses: [string, boolean, string][]) => {
        try {
          sessionStorage.setItem(key, JSON.stringify(statuses));
        } catch {
          // no storage: a status shown as the page reloads may be missed
        }
      };
      const send = (status: [string, boolean, string]) => window.bo1PlayerStatus(...status)
        .then(() => keep(unheard().filter((s) => s[0] !== status[0])), () => {});
      addEventListener('DOMContentLoaded', () => {
        for (const status of unheard()) send(status);
        const el = document.getElementById('status');
        if (!el) return;
        let last = '';
        const report = () => {
          const error = el.classList.contains('error');
          const now = (error ? 'error ' : '') + el.textContent;
          if (now === last) return;
          last = now;
          const status: [string, boolean, string] = [`${Date.now()}-${Math.random()}`, error, el.textContent || ''];
          keep([...unheard(), status]);
          send(status);
        };
        new MutationObserver(report).observe(el, { attributes: true, childList: true, characterData: true,
          subtree: true });
        addEventListener('pagehide', report);
        report();
      });
    });
    // Headless Chrome lets a page capture the mouse whenever it asks; Chrome with a window, only just after a click
    // (or a key but Esc: within 5 s), in full screen, or when the page let it go itself (exitPointerLock) last. The
    // player holds the page to that rule, so that it plays as in Chrome: a game it joins has the mouse once it is
    // clicked. It goes by the input itself, as Playwright's evaluate counts as a click (navigator.userActivation). It
    // takes Esc in a window as Chrome does, which headless Chrome leaves to the page: with the mouse captured, Chrome
    // lets it go, and the page has neither that Esc's down nor its up; the up of one it left the page it takes too,
    // if the mouse is captured by then. bo1ChromeLetsGo() lets the mouse go as Chrome does on Esc in a window.
    await this.context.addInitScript(() => {
      const request = Element.prototype.requestPointerLock;
      const exit = Document.prototype.exitPointerLock;
      let letGo = false;
      let input = -Infinity;
      let escTaken = false;
      addEventListener('mousedown', () => {
        input = performance.now();
      }, true);
      addEventListener('keydown', (e) => {
        if (e.key !== 'Escape') input = performance.now();
        else if (!document.fullscreenElement && document.pointerLockElement) {
          escTaken = true;
          e.stopImmediatePropagation();
          exit.call(document);
        }
      }, true);
      addEventListener('keyup', (e) => {
        if (e.key !== 'Escape' || document.fullscreenElement || !(escTaken || document.pointerLockElement)) return;
        escTaken = false;
        e.stopImmediatePropagation();
        if (document.pointerLockElement) exit.call(document);
      }, true);
      Document.prototype.exitPointerLock = function (this: Document) {
        if (this.pointerLockElement) letGo = true;
        return exit.call(this);
      };
      document.addEventListener('pointerlockchange', () => {
        if (document.pointerLockElement) letGo = false;
      });
      type LockArgs = Parameters<Element['requestPointerLock']>;
      Element.prototype.requestPointerLock = function (this: Element, ...args: LockArgs) {
        if (performance.now() - input < 5000 || document.fullscreenElement || letGo) return request.apply(this, args);
        document.dispatchEvent(new Event('pointerlockerror'));
        return Promise.reject(new DOMException('Chrome lets a page capture the mouse only just after a click',
          'NotAllowedError'));
      };
      window.bo1ChromeLetsGo = () => exit.call(document);
    });
    // The game's menu and lobby sounds the page starts, by alias. The page decodes them as it starts
    // (web/src/sounds.ts: soundsInit), each into a buffer of one audio context, in the order its console then names
    // them ("lobby: sounds, <aliases>"): which buffer of its context a sound plays, and how many buffers the context
    // has, say which sound it is. The music's gain is kept as bo1MusicGain: the gain that an audio context's output
    // takes before any sound has gone through it (web/src/music.ts).
    await this.context.exposeFunction('bo1SoundStarted', (index: number, count: number) => {
      if (count === this.soundNames.length && this.soundNames[index]) this.soundsStarted.push(this.soundNames[index]);
    });
    await this.context.addInitScript(() => {
      const createBuffer = BaseAudioContext.prototype.createBuffer;
      BaseAudioContext.prototype.createBuffer = function (this: BaseAudioContext, ...args) {
        const buffer = createBuffer.apply(this, args);
        (this.bo1Buffers ||= []).push(buffer);
        return buffer;
      };
      const start = AudioBufferSourceNode.prototype.start;
      AudioBufferSourceNode.prototype.start = function (this: AudioBufferSourceNode, ...args) {
        const buffers = this.context.bo1Buffers || [];
        const index = this.buffer ? buffers.indexOf(this.buffer) : -1;
        if (index >= 0 && !this.loop) window.bo1SoundStarted(index, buffers.length);
        return start.apply(this, args);
      };
      const connect = AudioNode.prototype.connect as (this: AudioNode, to: AudioNode, ...rest: number[]) => AudioNode;
      AudioNode.prototype.connect = function (this: AudioNode, to: AudioNode, ...rest: number[]) {
        if (to instanceof GainNode) to.bo1Fed = true;
        if (this instanceof GainNode && to === this.context.destination && !this.bo1Fed && !window.bo1MusicGain) {
          window.bo1MusicGain = this;
        }
        return connect.call(this, to, ...rest);
      } as AudioNode['connect'];
    });
    // the page picks the player's game folder with a file input, which Playwright sets, rather than the folder picker
    if (options.importFrom) {
      await this.context.addInitScript(() => {
        (window as { showDirectoryPicker?: unknown }).showDirectoryPicker = undefined;
      });
    }
    // The player's gamepad, as Chrome's Gamepad API gives it: the standard mapping, as an Xbox controller has it,
    // shown to the page once one of its buttons is pressed, as Chrome shows it. Chrome numbers it 1, after a device
    // with no standard mapping (a wheel), which the page leaves alone. bo1Pad.button(b, value) and bo1Pad.axis(a,
    // value) move it, bo1Pad.plug(false) unplugs it and bo1Pad.plug(true) plugs it in again, and bo1Pad.rumbles has
    // the effects the page played on it (its vibrationActuator: Chrome's dual rumble), each with when (at: the
    // page's clock).
    if (options.gamepad) {
      await this.context.addInitScript(() => {
        const button = () => ({ pressed: false, touched: false, value: 0 });
        const rumbles: object[] = [];
        const pad = { id: 'Xbox 360 Controller (XInput STANDARD GAMEPAD)', index: 1, connected: true,
          mapping: 'standard', timestamp: 0, axes: [0, 0, 0, 0], buttons: Array.from({ length: 17 }, button),
          vibrationActuator: { type: 'dual-rumble', effects: ['dual-rumble'],
            playEffect: (type: string, params: object) => {
              rumbles.push({ type, ...params, at: performance.now() });
              return Promise.resolve('complete');
            },
            reset: () => Promise.resolve('complete') } };
        const wheel = { id: 'Wheel (Vendor: 046d Product: c24f)', index: 0, connected: true, mapping: '',
          timestamp: 0, axes: [0, 0, 0, 0, 0, 0], buttons: Array.from({ length: 12 }, button) };
        let shown = false;
        Object.defineProperty(navigator, 'getGamepads', {
          value: () => (shown ? [wheel, pad.connected ? pad : null, null, null] : [null, null, null, null]) });
        const connected = (p: object) =>
          window.dispatchEvent(Object.assign(new Event('gamepadconnected'), { gamepad: p }));
        window.bo1Pad = {
          rumbles: rumbles as Window['bo1Pad']['rumbles'],
          plug(on) {
            if (pad.connected === on) return;
            pad.connected = on;
            window.dispatchEvent(Object.assign(new Event(on ? 'gamepadconnected' : 'gamepaddisconnected'),
              { gamepad: pad }));
          },
          button(b, value) {
            pad.buttons[b] = { pressed: value > 0, touched: value > 0, value };
            pad.timestamp = performance.now();
            if (shown || !value) return;
            shown = true;
            connected(wheel);
            connected(pad);
          },
          axis(a, value) {
            pad.axes[a] = value;
            pad.timestamp = performance.now();
          },
        };
      });
    }
  }

  /** Has a tab's console go to the player's log, its lines after the prefix: 'tab 2: '. */
  watch(page: Page, prefix: string): void {
    page.on('console', (message: ConsoleMessage) => this.heardLine(page, message.text(), prefix));
    page.on('pageerror', (error) => this.log(`${prefix}page: uncaught ${error.message}`));
  }

  private heardLine(page: Page, text: string, prefix: string): void {
    this.log(prefix + text);
    if (text.startsWith('bo1_player: ')) this.answers.get(page)?.(text);
    if (prefix) return;
    const names = /^lobby: sounds, (.*)$/.exec(text);
    if (names) this.soundNames = names[1].split(', ');
    const inRoom = /^lobby: in room (\w+)$/.exec(text);
    if (inRoom) this.room = inRoom[1];
    if (text.startsWith('[bo1] gpu error: ')) this.fail(new Error(`${this.name}'s page had a graphics error: ${text}`));
    const back = /^\[bo1\] webgpu: the back buffer is \d+x\d+, (.*)$/.exec(text);
    if (back) this.antiAliasing = back[1];
    if (/^\[bo1\] command line: /.test(text)) this.engineRuns = true;
    if (text === "lobby: the last game's GPU device is gone") this.devicesGone++;
  }

  /** A line in the player's log, and the listeners it answers. */
  private log(line: string): void {
    this.lines.push(line);
    this.out.write(line + '\n');
    for (const l of [...this.listeners]) {
      if (!l.pattern.test(line)) continue;
      this.listeners.delete(l);
      l.done(line);
    }
  }

  /** A line of the test's in the player's log: what it had them do, or what it saw. */
  note(text: string): void {
    this.log(`test: ${text}`);
  }

  /** Fails the player: what they wait for fails with the error. */
  fail(error: Error): void {
    if (this.failure || this.closing) return;
    this.failure = error;
    this.log(`test: FAILED: ${error.message}`);
    for (const l of [...this.listeners]) l.failed(error);
    this.listeners.clear();
  }

  /** Runs a check of the test's beside it: one that fails fails the player (failure), and so the test. */
  background(check: () => Promise<unknown>): void {
    check().catch((e: Error) => this.fail(e));
  }

  /** Where the log is now: find and waitFor from there look at what comes after. */
  mark(): number {
    return this.lines.length;
  }

  /** The first line of the log from line from on that matches. */
  find(pattern: RegExp, from = 0): string | undefined {
    for (let i = from; i < this.lines.length; i++) if (pattern.test(this.lines[i])) return this.lines[i];
    return undefined;
  }

  /** The next line of the log that matches, from now on, within ms; false when none does. */
  next(pattern: RegExp, ms: number): Promise<string | false> {
    return new Promise((done, failed) => {
      if (this.failure) return failed(this.failure);
      if (this.closing) return done(false);
      const timer = setTimeout(() => {
        this.listeners.delete(l);
        done(false);
      }, ms);
      const l: Listener = { pattern, done: (line) => {
        clearTimeout(timer);
        done(line);
      }, failed: (error) => {
        clearTimeout(timer);
        failed(error);
      } };
      this.listeners.add(l);
    });
  }

  /** Waits for a line of the log from line from on that matches, and returns it; fails after timeout ms. */
  async waitFor(pattern: RegExp, timeout = 60_000, from = 0): Promise<string> {
    if (this.failure) throw this.failure;
    const found = this.find(pattern, from);
    if (found !== undefined) return found;
    const line = await this.next(pattern, timeout);
    if (line === false) throw new Error(`${this.name}'s log had no line like ${pattern} in ${timeout / 1000} s`);
    return line;
  }

  /** A screenshot of the page, to <name>-<step>.png beside the log. */
  async shot(step: string): Promise<void> {
    const file = (this.out.path as string).replace(/\.log$/, `-${step}.png`);
    await this.page.screenshot({ path: file }).catch(() => {});
  }

  /** The colour the page shows at the middle of the window, [r, g, b]. */
  async middleColor(): Promise<number[]> {
    const png = await this.page.screenshot({ clip: { x: 640, y: 360, width: 1, height: 1 } });
    return this.page.evaluate(async (b64) => {
      const img = new Image();
      img.src = 'data:image/png;base64,' + b64;
      await img.decode();
      const c = document.createElement('canvas');
      c.width = c.height = 1;
      const g = c.getContext('2d')!;
      g.drawImage(img, 0, 0);
      return [...g.getImageData(0, 0, 1, 1).data.slice(0, 3)];
    }, png.toString('base64'));
  }

  // ---- the lobby

  /** Makes a room, and returns its code. */
  async makeRoom(): Promise<string> {
    const made = this.next(/^lobby: in room \w+$/, 60_000);
    await this.page.click('#make');
    const line = await made;
    if (!line) throw new Error('making a room put the page in none');
    this.note(`made room ${this.room}`);
    return this.room;
  }

  /** The players in the page's room. */
  members(): Promise<number> {
    return this.page.locator('#members li:not(.empty)').count();
  }

  /** The map the page shows: its room's, or the one picked to play alone (web/src/maps.ts: mapPicture). */
  shownMap(): Promise<string> {
    return this.page.evaluate(() => {
      const inRoom = !document.getElementById('inside')!.classList.contains('hidden');
      const src = document.getElementById(inRoom ? 'mapimg' : 'soloimg')!.getAttribute('src') || '';
      const name = /^\/maps\/([^/]+)\//.exec(src);
      return name ? decodeURIComponent(name[1]) : '';
    });
  }

  /**
   * Gets the map of a button (#ready: the room's, #playalone: the one picked to play alone) into the browser's store:
   * the button says Download (or Import, from the player's game folder) until the store has it, which the player
   * presses; then it says what it is for (Ready, Start, Play). Returns the map, and what the button counted to and
   * offered, in MB.
   */
  async getTheMap(button: '#ready' | '#playalone', importFrom = ''): Promise<{ map: string; counted: number;
    offered: number }> {
    let counted = 0;
    let offered = 0;
    if (button === '#playalone' && !(await this.page.isVisible(button))) await this.page.click('#soloopen');
    const line = button === '#ready' ? '#roomnote' : '#solonote'; // what went wrong (web/src/files.ts: showNote)
    const says = () => this.page.evaluate(([sel, under]) => {
      const b = document.querySelector(sel)!;
      // (off, the page's main buttons are aria-disabled, which keeps the focus on them: web/src/controls.ts)
      return { text: b.textContent || '', disabled: b.getAttribute('aria-disabled') === 'true',
        line: document.querySelector(under)!.textContent || '' };
    }, [button, line]).catch(() => ({ text: '', disabled: true, line: '' }));
    let missing = 0;
    for (let waited = 0; ; waited++) {
      if (this.failure) throw this.failure;
      const b = await says();
      if (/^[\d,]+ \/ [\d,]+ MB$/.test(b.text) && !counted) {
        counted = +b.text.split(' / ')[1].replace(/\D/g, '');
        this.note(`the map's button says ${b.text}`);
      }
      if (/^(Download|Import) · /.test(b.text) && !b.disabled) {
        offered = Math.round(parseFloat(b.text.replace(/^\w+ · /, '')) * (/GB$/.test(b.text) ? 1000 : 1));
        if (/^Import/.test(b.text)) {
          if (!importFrom) {
            throw new Error(`the page offers to import the map, not to download it: ${b.text}, ${b.line}`);
          }
          // the folder, as the page asks for it (a page that has the input's from before asks for none)
          const chooser = this.page.waitForEvent('filechooser', { timeout: 10_000 }).catch(() => null);
          await this.page.click(button);
          const asked = await chooser;
          if (asked) await asked.setFiles(importFrom);
          this.note(`importing the map from ${importFrom}: ${b.text} (${b.line})`);
        } else {
          await this.page.click(button);
          this.note(`downloading the map: ${b.text}`);
        }
      } else if (/^(Ready|Start|Play)$/.test(b.text) && !b.disabled) {
        break;
      } else if (b.text === 'Retry') {
        throw new Error(`the page's download of the map failed: ${b.line}`);
      } else if (b.text === 'Missing map' && ++missing > 10) {
        throw new Error('the page says the map is missing, and offers no download');
      }
      if (waited >= 600) throw new Error(`the page did not get the map in 600 s: its button says ${b.text}`);
      await sleep(1000);
    }
    const map = await this.shownMap();
    this.note(`${map} is in this browser`);
    return { map, counted, offered };
  }

  /**
   * In a room: gets its map, waits until it has members players (if any), and says the player is ready to play its
   * next game in this page.
   */
  async getReady(members = 0): Promise<void> {
    if (!this.room) await this.waitFor(/^lobby: in room \w+$/);
    await this.getTheMap('#ready');
    for (let waited = 0; members && (await this.members()) < members; waited++) {
      if (waited >= 300) throw new Error(`${members} players were not in the room within 300 s`);
      await sleep(1000);
    }
    await this.page.click('#ready');
    this.note('ready');
  }

  /** Waits until the room's game has started: the room shows it (web/src/room.ts: showRoom). */
  async waitForGame(timeout = 300_000): Promise<void> {
    const begun = () => {
      const game = document.getElementById('roomgame');
      return !!game && !game.classList.contains('hidden');
    };
    await this.page.waitForFunction(begun, null, { timeout });
    this.note(`room ${this.room}'s game started`);
  }

  /** The room's state, as it shows beside its code: Starting, Waiting, In game, Ending, or open. */
  roomState(): Promise<string> {
    return this.page.evaluate(() => {
      const game = document.getElementById('roomgame')!;
      return game.classList.contains('hidden') ? 'open' : game.textContent || '';
    });
  }

  // ---- the game

  /** Runs a command on the engine's console, in the page or another tab of the player's. */
  command(text: string, at = this.page): Promise<void> {
    return at.evaluate((c) => window.Module.bo1Command(c), text);
  }

  /** A dvar's value, as the engine's console says it; null without an answer. */
  async dvar(name: string, at = this.page): Promise<string | null> {
    const said = this.next(new RegExp(`^"${name}" is: "`), 10_000);
    await this.command(name, at);
    const m = /^"[^"]*" is: "([^"]*?)(\^7)?"/.exec((await said) || '');
    return m ? m[1] : null;
  }

  /** The player in the game, as bo1_player says (null until the engine runs and has a snapshot). */
  async ask(at = this.page): Promise<PlayerState | null> {
    if (this.failure) throw this.failure;
    const line = new Promise<string>((done) => {
      this.answers.set(at, done);
      setTimeout(() => done(''), 5000);
    });
    const sent = await at.evaluate(() => {
      try {
        window.Module.bo1Command('bo1_player');
        return true;
      } catch {
        return false; // the engine is not loaded yet
      }
    }).catch(() => false);
    const text = sent ? await line : '';
    this.answers.delete(at);
    const m = new RegExp('^bo1_player: pm_type (-?\\d+) origin (\\S+) (\\S+) (\\S+) angles (\\S+) (\\S+) ' +
      'weapon (\\S+) clip (-?\\d+) stock (-?\\d+) forward ([01])').exec(text);
    if (!m) return null;
    const aim = new RegExp(' aimtargets (\\d+) nearest (\\S+) (\\S+) (\\S+) aimrange (\\S+) autoaimrange (\\S+) ' +
      'slowdown ([01]) autoaim (-?\\d+)').exec(text) || [];
    const lockOn = / lockon (-?\d+)/.exec(text) || [];
    const closest = / closest (\S+) (\S+) (\S+)/.exec(text) || [];
    const lunge = / lunges (\d+) lungedist (\d+)/.exec(text) || [];
    const melees = / melees (\d+)/.exec(text) || [];
    const arms = / viewmodel (\S+)/.exec(text) || [];
    const scores = / scores ([01])/.exec(text) || [];
    const animLost = / animlost (\d+)/.exec(text) || [];
    return {
      pmType: +m[1],
      origin: [+m[2], +m[3], +m[4]],
      pitch: +m[5],
      yaw: +m[6],
      weapon: m[7],
      clip: +m[8],
      stock: +m[9],
      forward: m[10] === '1',
      aimTargets: +(aim[1] || 0),
      nearest: { x: +(aim[2] || 0), y: +(aim[3] || 0), distance: +(aim[4] || 0) },
      aimRange: +(aim[5] || 0),
      autoAimRange: +(aim[6] || 0),
      slowdown: aim[7] === '1',
      autoAim: aim.length ? +aim[8] : -1,
      lockOn: lockOn.length ? +lockOn[1] : -1,
      closest: { x: +(closest[1] || 0), y: +(closest[2] || 0), distance: +(closest[3] || 0) },
      lunges: +(lunge[1] || 0),
      lungeDist: +(lunge[2] || 0),
      melees: +(melees[1] || 0),
      arms: arms[1] || '',
      scores: scores[1] === '1',
      animLost: animLost.length ? +animLost[1] : -1,
    };
  }

  /**
   * The player now, asked up to 3 times, 5 s each: the engine waits for the page some seconds as the map first draws,
   * which may come just after the player spawns.
   */
  async state(at = this.page): Promise<PlayerState> {
    for (let i = 0; i < 3; i++) {
      const p = await this.ask(at);
      if (p) return p;
    }
    throw new Error('bo1_player did not answer, asked 3 times');
  }

  /**
   * The player once the engine has run at least the given frames (it answers bo1_player once a frame at most) and ms
   * have gone by: the input the page gave it before is taken.
   */
  async afterFrames(frames: number, ms: number): Promise<PlayerState> {
    const until = Date.now() + ms;
    let p = await this.state();
    for (let i = 1; i < frames || Date.now() < until; i++) {
      await sleep(100);
      p = await this.state();
    }
    return p;
  }

  /** The player once they have spawned: in the game, with a weapon, within limit seconds. */
  async spawned(at = this.page, limit = 300): Promise<PlayerState> {
    for (let waited = 0; ; waited += 2) {
      if (waited >= limit) throw new Error(`${this.name} did not spawn in ${limit} s`);
      await sleep(2000);
      const p = await this.ask(at);
      if (p && p.pmType === 0 && p.weapon !== 'none') return p;
    }
  }

  /**
   * The player once as wanted, asked once a second for up to the seconds given, or as they last were: a command
   * reaches the game in the page's next frame, and its answer comes back in a snapshot a frame later.
   */
  async until(seconds: number, wanted: (p: PlayerState) => boolean): Promise<PlayerState | null> {
    let p: PlayerState | null = null;
    for (let waited = 0; waited < seconds && !(p && wanted(p)); waited++) {
      await sleep(1000);
      p = (await this.ask()) || p;
    }
    return p;
  }

  /** Whether the page has the mouse captured (or not, as wanted), within ms. */
  async captured(wanted: boolean, ms: number): Promise<boolean> {
    const now = () => this.page.evaluate(() => document.pointerLockElement === document.getElementById('canvas'));
    for (let waited = 0; waited < ms; waited += 250) {
      if ((await now()) === wanted) return true;
      await sleep(250);
    }
    return (await now()) === wanted;
  }

  /** Whether the page is in full screen, its own. */
  fullScreen(): Promise<boolean> {
    return this.page.evaluate(() => !!document.fullscreenElement);
  }

  /** Whether the page is in full screen within ms (it asks for it once it has the mouse). */
  async fullScreenWithin(ms: number): Promise<boolean> {
    for (let waited = 0; waited < ms; waited += 250) {
      if (await this.fullScreen()) return true;
      await sleep(250);
    }
    return this.fullScreen();
  }

  /** A key held for ms: input is held, not tapped, as the engine reads it once a frame. */
  async press(key: string, ms = 400): Promise<void> {
    await this.page.keyboard.down(key);
    await sleep(ms);
    await this.page.keyboard.up(key);
  }

  /** Esc, held for 400 ms. */
  escape(): Promise<void> {
    return this.press('Escape');
  }

  /** The mouse's left button, held for ms where the mouse is. */
  async click(ms = 400): Promise<void> {
    await this.page.mouse.down();
    await sleep(ms);
    await this.page.mouse.up();
  }

  /** The fire button held for 400 ms, up to 5 times, until the clip has fewer rounds than before. */
  async fire(before: PlayerState): Promise<PlayerState> {
    for (let i = 1; ; i++) {
      await this.click();
      await sleep(1000);
      const fired = await this.state();
      if (fired.clip < before.clip) return fired;
      if (i === 5) {
        throw new Error(`5 presses of the fire button left ${fired.clip} of ${before.clip} rounds in the clip`);
      }
    }
  }

  /**
   * Walks back to where a walk of the test's began (from), to play on around the spawn: a level may have something
   * for whoever stands where the walk ended (Call of the Dead's sea freezes a player 30 s in it). Back is held (hold,
   * then release) until the player is within 32 units of the start, or past it, or has come no nearer in six looks,
   * or for 20 s. The player after it.
   */
  async walkBack(from: PlayerState, hold: () => Promise<void>, release: () => Promise<void>): Promise<PlayerState> {
    const away = (p: PlayerState) => Math.hypot(p.origin[0] - from.origin[0], p.origin[1] - from.origin[1]);
    const walked = away(await this.state());
    let nearest = walked;
    let stuck = 0;
    await hold();
    for (const until = Date.now() + 20_000; Date.now() < until;) {
      const now = away(await this.afterFrames(2, 500));
      if (now < 32 || now > nearest + 32) break;
      stuck = now > nearest - 8 ? stuck + 1 : 0;
      if (stuck >= 6) break;
      nearest = Math.min(nearest, now);
    }
    await release();
    await sleep(1000);
    const back = await this.state();
    if (walked >= 32 && walked - away(back) < 16) {
      throw new Error(`back held where a walk of ${walked.toFixed(0)} units ended: the player is ` +
        `${away(back).toFixed(0)} units from its start`);
    }
    this.note(`walked back, to ${away(back).toFixed(0)} units from where the walk of ${walked.toFixed(0)} began`);
    return back;
  }

  /**
   * Checks that the game read the game files from the browser's store alone: the page asked the server for none of
   * them by path, nor downloaded any once the engine ran.
   */
  checkReads(): void {
    const byPath = this.reads.filter((r) => !r.pack);
    const byGame = this.reads.filter((r) => r.byGame);
    if (byPath.length || byGame.length) {
      throw new Error(`the page asked the server for the game files: ${byPath.length} by path, ${byGame.length} once ` +
        `the game ran: ${[...byPath, ...byGame].slice(0, 5).map((r) => r.path).join(', ')}`);
    }
    this.note(`the game read nothing from the server (the page downloaded the map in ${this.reads.length} requests)`);
  }

  /**
   * The browser's memory, after a garbage collection (unless now), which the browser makes by itself some time after
   * a game: the working sets of its processes, which have the player's profile on their command line, and the page's
   * workers (the engine's threads). What the GPU process made is in the GPU's memory, not its own.
   */
  async memory(now = false): Promise<Memory> {
    if (!now) {
      const cdp = await this.context.newCDPSession(this.page);
      await cdp.send('HeapProfiler.collectGarbage');
      await cdp.detach();
      await sleep(2000);
    }
    const m: Memory = { renderer: 0, gpu: 0, workers: this.page.workers().length };
    const processes: [number, string][] = [];
    if (process.platform === 'win32') {
      const like = `*${this.profile}*`.replace(/'/g, "''");
      const out = execFileSync('powershell', ['-NoProfile', '-Command',
        `Get-CimInstance Win32_Process -Filter "Name='chrome.exe'" | ` +
        `Where-Object { $_.CommandLine -like '${like}' } | ` +
        'ForEach-Object { "$($_.WorkingSetSize) $($_.CommandLine)" }'], { encoding: 'utf8', windowsHide: true });
      for (const line of out.split('\n')) {
        const f = /^(\d+) (.*)$/.exec(line.trim());
        if (f) processes.push([+f[1] / 1024, f[2]]);
      }
    } else {
      for (const line of execFileSync('ps', ['-eo', 'rss=,args='], { encoding: 'utf8' }).split('\n')) {
        const f = /^\s*(\d+) (.*)$/.exec(line);
        if (f && f[2].includes(this.profile)) processes.push([+f[1], f[2]]);
      }
    }
    for (const [kb, command] of processes) {
      if (command.includes('--type=renderer')) m.renderer += kb / 1048576;
      else if (command.includes('--type=gpu-process')) m.gpu += kb / 1048576;
    }
    return m;
  }

  /** What memory says, for the log. */
  static gb(m: Memory): string {
    return `the page ${m.renderer.toFixed(2)} GB, the GPU process ${m.gpu.toFixed(2)} GB, ${m.workers} workers`;
  }

  /** Where the page is before a game ends, for inPlace after. */
  marks(): { loads: number; devicesGone: number } {
    return { loads: this.loads, devicesGone: this.devicesGone };
  }

  /**
   * Checks that the page came back from its game as it was, not reloaded (web/src/game.ts: stopEngine, backToLobby),
   * and freed the engine: its workers gone, and once the browser has collected it and the GPU process has freed what
   * the game made on its device (the page says "the last game's GPU device is gone"), the page within 0.3 GB of the
   * lobby's before the first game (lobbyMemory), and the GPU process within 0.5 (it keeps 0.2 to 0.3 GB of a game's
   * 1.2 once the device is gone, measured on an RTX 3080). A game's engine takes 1.5 GB and more of each. With no
   * lobbyMemory, whether the page reloaded alone. what: how the game ended; before: marks() before it did.
   */
  async inPlace(what: string, before: { loads: number; devicesGone: number }): Promise<void> {
    if (this.loads !== before.loads) throw new Error(`the page reloaded as ${what}`);
    const lobby = this.lobbyMemory;
    if (!lobby) {
      this.note(`the page stayed as ${what}`);
      return;
    }
    let m = await this.memory();
    for (let waited = 0; this.devicesGone === before.devicesGone; waited += 3) {
      if (waited >= 30) {
        throw new Error(`the page kept its game's GPU device, which the browser never collected: ${Player.gb(m)}`);
      }
      await sleep(1000);
      m = await this.memory();
    }
    await sleep(2000);
    m = await this.memory();
    if (m.workers > lobby.workers) {
      throw new Error(`the engine's workers stayed: ${m.workers}, ${lobby.workers} before the game`);
    }
    if (m.renderer > lobby.renderer + 0.3 || m.gpu > lobby.gpu + 0.5) {
      throw new Error(`the engine's memory stayed as ${what}: ${Player.gb(m)}; before the game ${Player.gb(lobby)}`);
    }
    this.note(`the page stayed as ${what}, the engine's memory freed: ${Player.gb(m)}`);
  }

  // ---- the gamepad (options.gamepad)

  /** A button of the gamepad held for ms. */
  async padPress(b: number, ms = 400): Promise<void> {
    await this.page.evaluate((b) => window.bo1Pad.button(b, 1), b);
    await sleep(ms);
    await this.page.evaluate((b) => window.bo1Pad.button(b, 0), b);
  }

  /**
   * A button of the gamepad held for 1 s, up to n times, until the console says a line that matches (within 5 s of
   * each): the line, or false. The engine reads the gamepad once a frame, which can outlast a press.
   */
  async padPressUntil(b: number, pattern: RegExp, n = 3): Promise<string | false> {
    for (let i = 0; i < n; i++) {
      const said = this.next(pattern, 6000);
      await this.padPress(b, 1000);
      const line = await said;
      if (line) return line;
    }
    return false;
  }

  /** A button of the gamepad held until the game has it (had), up to 5 s, then let go: the player then, and whether. */
  async padHold(b: number, had: (p: PlayerState) => boolean): Promise<{ s: PlayerState; had: boolean }> {
    const until = Date.now() + 5000;
    let s: PlayerState;
    await this.page.evaluate((b) => window.bo1Pad.button(b, 1), b);
    try {
      do s = await this.state();
      while (!had(s) && Date.now() < until);
    } finally {
      await this.page.evaluate((b) => window.bo1Pad.button(b, 0), b);
    }
    return { s, had: had(s) };
  }

  /** An axis of the gamepad pushed to value for ms, then let go. */
  async padPush(a: number, value: number, ms: number): Promise<void> {
    await this.page.evaluate(([a, v]) => window.bo1Pad.axis(a, v), [a, value]);
    await sleep(ms);
    await this.page.evaluate((a) => window.bo1Pad.axis(a, 0), a);
  }

  // ---- the end

  /**
   * Closes the player's Chrome, as a player closes theirs: its pages go, their GPU devices with them, and its GPU
   * process ends by itself. Fails when its processes are still there 10 s on (chrome.ts), noting it for the run.
   */
  async close(): Promise<void> {
    if (this.closing) return;
    this.closing = true;
    for (const l of [...this.listeners]) l.done(false);
    this.listeners.clear();
    const proc = this.chromeProcess;
    if (this.browser && proc) {
      const ended = proc.exitCode !== null ? Promise.resolve() : new Promise((done) => proc.once('exit', done));
      await this.browser.newBrowserCDPSession().then((s) => s.send('Browser.close')).catch(() => proc.kill());
      await Promise.race([ended, sleep(8000)]);
    } else {
      await Promise.race([this.context.close().catch(() => {}), sleep(8000)]);
    }
    await new Promise((done) => this.out.end(done));
    for (let i = 0; i < 5; i++) {
      if (!chromeLeft(this.profile)) return;
      await sleep(2000);
    }
    const left = chromeLeft(this.profile);
    if (!left) return;
    writeFileSync(stuckNote, `${left}\n`);
    throw new Error(`${left} processes of ${this.name}'s Chrome would not end: the GPU's driver holds them, and no ` +
      'longer answers. No Chrome is started after this. Restart the machine to be rid of them.');
  }
}
