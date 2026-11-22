// web/src/engine.ts - the engine's host, as the page uses it (web/public/player.js).
//
// The host is a file of its own at a fixed address, which the page loads when it starts (main.ts). It runs the engine
// on the page's canvases, keeps the game files in the browser's store, and holds the lock of the tab that plays. This
// file has the types of the host's interface, the player that the page made with it, and what the page's parts read
// of it: whether the browser can run the game, whether a game is on, and what the store has of each map.
import { setStatus } from './screen';

/** What the store has of a map (player.js: store.states). */
export interface MapState {
  /** The bytes the store has of the map, and all the bytes the map needs. */
  have?: number;
  total?: number;
  /** The store has all of the map. */
  done?: boolean;
  /** A download of the map is running. With import, it is an import from the player's game folder. */
  running?: boolean;
  import?: boolean;
  /** Why the last download failed. */
  error?: string;
  /** A browser can't play the map. */
  none?: boolean;
  /** The server sends no game files. */
  off?: boolean;
  /** Whether the player's game folder has what the store lacks of the map. */
  inFolder?: boolean;
  /** The game folder has another version of the map. */
  differs?: boolean;
  stopped?: boolean;
  /** The game folder no longer holds an install. The host says so for every map (onStore with map ''). */
  noInstall?: boolean;
}

/**
 * The player's own copy of the game, which the store imports maps from: a directory handle, or the files of a file
 * input in a browser without the folder picker.
 */
export type GameFolder = FileSystemDirectoryHandle | { name: string; files: File[] };

/** The engine's Module object, which the engine's script, DevTools and the browser tests read. */
export interface EngineModule {
  /** The map that the engine loads. */
  bo1LoadingMap?: string;
  /** The renderer's debugging flags, added up (&gpu=). The host sets them for every game, and they may change in it. */
  bo1GpuFlags: number;
  /** Runs a console command in the game. The host sets it for every game. It throws before the engine is up. */
  bo1Command: (text: string) => void;
  /** Calls a function of the engine by its name, as Emscripten's ccall does. The engine sets it once it is up. */
  ccall?: (name: string, returns: string | null, argTypes: string[], args: unknown[]) => unknown;
  [name: string]: unknown;
}

/** How a start went (player.js: join, playAlone). why is '' when the page stopped the start or a game runs already. */
export interface Begun {
  started: boolean;
  why?: string;
}

/** The options of createPlayer. player.js describes each. */
export interface PlayerOptions {
  canvas: HTMLCanvasElement;
  movie: HTMLCanvasElement;
  settings?: object;
  dedicated?: boolean;
  args?: string[];
  recommended?: Record<string, string>;
  gpuTimings?: boolean;
  module?: Record<string, unknown>;
  log?: (text: string) => void;
  onStatus?: (text: string) => void;
  onLoading?: () => void;
  onMovie?: (what: 'starts' | 'shows' | 'over') => void;
  onOutro?: () => void;
  onGame?: (line: string) => void;
  onWaited?: (text: string) => void;
  onBuild?: (text: string) => void;
  onEnd?: (why: string, text: string) => void;
  onError?: (text: string) => void;
  onStore?: (map: string, state: MapState | undefined) => void;
  onUsage?: (bytes: number, persisted: boolean) => void;
  onLockLost?: () => void;
}

/** The player that createPlayer makes. player.js describes each member. */
export interface Player {
  join(game: { session: string; name: string; address: string; map: string; dev?: boolean }): Promise<Begun>;
  playAlone(game: { map: string; name?: string; cheats?: boolean; dev?: boolean }): Promise<Begun>;
  stop(): boolean;
  prefetch(): void;
  readonly module: EngineModule;
  readonly gpu: string;
  readonly startedAt: number;
  readonly store: {
    start(): boolean;
    download(map: string, options: { token?: string }): Promise<MapState | null>;
    stop(): void;
    ensure(map: string, options: { token?: string }): Promise<boolean>;
    readonly states: Record<string, MapState>;
    know(map: string): boolean;
    readonly asking: number;
    readonly downloading: string;
    progress(map: string): { got: number; need: number };
    retry(): void;
    usage(): Promise<{ bytes: number; persisted: boolean }>;
    remove(): Promise<void>;
    useFolder(folder: GameFolder | null): Promise<boolean>;
  };
  readonly lock: {
    take(steal: boolean): Promise<boolean>;
    release(): void;
    any(): Promise<boolean>;
  };
}

/** The host's module. */
export interface Host {
  createPlayer(options: PlayerOptions): Player;
  support(): { ok: boolean; alone: boolean; lacks: string; why: string };
}

declare global {
  interface Window {
    /** The engine's Module of the game that runs, or of the next. DevTools and the browser tests use it. */
    Module?: EngineModule;
  }
}

/**
 * Whether this browser can run the game on this page. It can't on a page that is not secure, or not cross-origin
 * isolated. A browser without WebGPU counts as able: the lobby is the same, and a game's start says what is missing.
 */
export let canRun = true;

/** Sets canRun from what the host says, and says on the status line why the browser can't run the game. */
export function checkSupport(host: Host): void {
  const lacks = host.support().lacks;
  if (lacks === 'secure') {
    canRun = false;
    setStatus('Browsers run the game only on a secure page: over HTTPS (a reverse proxy in front of the server adds ' +
      'it), or at localhost.', true);
  } else if (lacks === 'isolation') {
    canRun = false;
    setStatus('This page must be served by the server, docker/server.sh (it needs cross-origin isolation for ' +
      'threads).', true);
  }
}

/** The page's player. The page makes it once, when it starts, before anything reads it (main.ts: makePlayer). */
export let player: Player;

/** What the store last said of each map, by map (player.store.states). A map that is not there is yet to be asked. */
export let dlState: Record<string, MapState> = {};

export function setPlayer(made: Player): void {
  player = made;
  dlState = made.store.states;
  window.Module = made.module;
}

/**
 * Whether a game has the page: from the click that starts the engine until the page is back in the lobby (game.ts:
 * startEngine, backToLobby).
 */
export let started = false;

export function setStarted(on: boolean): void {
  started = on;
}
