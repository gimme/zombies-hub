// web/src/files.ts - the game files that the browser keeps, as the page's views read them.
//
// The engine reads the game files from the browser's store alone (web/public/store.js, through player.js: store), so
// a map must be in the store before its game. The button of a view, where Ready or Play is, gets the map first
// (mapButton): it says "Download" and the size until the store has all of the map, fills as the download comes, and
// is Ready once the map is there.
//
// The host runs the store in a worker. The worker says what the store has of a map, from the server's list of what
// the map reads (server/files.go), and downloads the rest. A download stops when its map is no longer the button's.
// What it kept stays, and the next download goes on from there.
//
// A player who has Black Ops can import a map from their own copy instead (folder.ts). The page tells the lobby what
// it has of its room's map (reportFiles): the room shows that to the others, and the player can be ready only with
// the map (server/rooms.go: missing).
import type { MainButton } from './controls';
import { dlState, type GameFolder, player, started } from './engine';
import { act, ask, me, offline, view } from './lobby';
import { served } from './server';
import { onWindows } from './windows';

/** Bytes as whole megabytes. */
export const mb = (n: number): number => Math.round(n / 1e6);

/** Bytes as a size to read: 1.2 GB, or at least 1 MB. */
export const size = (n: number): string => (n >= 1e9 ? (n / 1e9).toFixed(1) + ' GB' : Math.max(1, mb(n)) + ' MB');

/** Asks the store how much it holds. The host's onUsage has the answer, which the profile shows (main.ts). */
export function storeUsage(): void {
  player.store.usage();
}

/**
 * Asks the store what it has of the map, unless the page knows. Once the store has answered, asks about each other
 * map that the lobby may offer, one at a time. A map that the player picks later then has its state already, and its
 * button waits for nothing.
 */
export function knowMap(map: string): void {
  if (!player.store.know(map)) return;
  if (player.store.asking || !view || onWindows() || started) return;
  const next = view.maps.find((m) => m.have && m.list && !dlState[m.name]);
  if (next) knowMap(next.name);
}

/** Downloads the map from the server, as the player, or imports it from their game folder where the folder has it. */
export function startDownload(map: string): void {
  if (started) return;
  player.store.download(map, { token: me.token });
}

export function stopDownload(): void {
  player.store.stop();
}

/** Asks the store again about what failed, now that the server answers again. */
export function downloadAgain(): void {
  reported = '';
  player.store.retry();
}

/**
 * Makes sure the store has all of the map, downloading what it lacks. Resolves to false when it can't: the server
 * sends no game files or has no list of the map, or the download failed.
 */
export const whenMap = (map: string): Promise<boolean> => player.store.ensure(map, { token: me.token });

// ---- The player's game folder (folder.ts sets it) ----

/** The folder that the page imports maps from, or null. */
export let folder: GameFolder | null = null;

/** Whether the page may read the folder: the player allowed it, on this visit or for good. */
export let folderOk = false;

/** What the profile says of the folder that the player chose last: that it holds no install. */
export let folderNote = '';

export function setFolder(next: GameFolder | null, ok: boolean): void {
  folder = next;
  folderOk = !!next && ok;
}

export function setFolderNote(text: string): void {
  folderNote = text;
}

// ---- A map's button ----

/** What a map's button says and does (mapButton). */
export interface MapButton extends MainButton {
  /** The store has the map: the button does what it is for (Ready, Play). */
  ready?: boolean;
  /** How the button gets the map: 'import' from the player's game folder, or 'download' from the server. */
  gets?: string;
  /** Why the page can't get the map as things are. The grid of maps greys such a map out (mapgrid.ts). */
  cant?: string;
}

/**
 * What a map's button says and does, by what the store has of the map. label is what the button says once the map is
 * there.
 *
 * The button imports the map from the player's game folder where the folder has it. Until the page may read the
 * folder, it takes the folder to have every map. Otherwise the button downloads the map, where the server sends game
 * files. Where it sends none and the page has no folder, the button imports from a folder that the player chooses as
 * they press it.
 *
 * The sizes are what the download fetches. Pieces that the store already has from other maps are not counted.
 */
export function mapButton(map: string, label: string): MapButton {
  const s = dlState[map];
  if (!s) return { label, disabled: true };
  if (s.done) return { label, ready: true };
  if (s.running) {
    const p = player.store.progress(map);
    return { label: mb(p.got).toLocaleString('en-US') + ' / ' + mb(p.need).toLocaleString('en-US') + ' MB',
      disabled: true, got: p.need ? p.got / p.need : 0 };
  }
  const serves = !!served.downloads && !s.off && !offline;
  const gets = folder ? (s.inFolder !== false ? 'import' : serves ? 'download' : '') : serves ? 'download' : 'import';
  const noServer = offline ? 'no connection to the server' : 'the server sends no game files';
  const missing = s.none ? 'not one a browser can play'
    : offline && !s.total ? 'not in this browser, and no connection to the server'
    : !gets ? 'not in your game folder, and ' + noServer
    : '';
  if (missing) {
    return { label: 'Missing map', disabled: true, cant: missing, title: missing[0].toUpperCase() + missing.slice(1) };
  }
  const title = gets === 'download' ? 'From the server'
    : folder ? 'From your game folder, ' + folder.name : 'From your own copy of the game';
  // The player is yet to choose the folder that the button would import from, so the grid greys the map out.
  const cant = gets === 'import' && !folder ? 'no game folder chosen, and ' + noServer : '';
  if (s.error) return { label: 'Retry', gets, title, cant };
  const left = (s.total || 0) - (s.have || 0);
  return { label: (gets === 'import' ? 'Import · ' : 'Download · ') + size(left), gets, title, cant };
}

/**
 * Sets the line under a map's button. It says why the button failed to get the map, or that the player's game folder
 * has another version of the map, which the button now downloads. m is the button (mapButton), and shown is whether
 * the button shows.
 */
export function showNote(line: HTMLElement, map: string, m: MapButton, shown: boolean): void {
  const s = dlState[map];
  const text = !s || s.running || m.ready ? ''
    : s.error ? (s.import ? 'The import failed: ' : 'The download failed: ') + s.error + '.'
    : s.differs ? 'Your game folder has another version of this map.' : '';
  line.classList.toggle('hidden', !shown || !text);
  line.textContent = text;
}

// ---- What the lobby hears ----

let reported = ''; // what the lobby last heard
let reportedRunning = ''; // the map of the running download that the lobby last heard of
let reportTimer = 0;
let reportedAt = 0;
let lastReport: Promise<unknown> = Promise.resolve();

/** Has the lobby hear again what the page has of its room's map. A lobby that started again has forgotten. */
export function reportAgain(): void {
  reported = '';
}

/**
 * Tells the lobby what the page has of its room's map (POST /lobby/files), when that changed. The lobby hears of a
 * download's start at once, and then how far it is at most every 2 s. Resolves once the lobby has heard.
 */
export function reportFiles(): Promise<unknown> {
  const room = view && view.room;
  const s = room && dlState[room.map];
  if (!s || offline || onWindows() || !me.token) return lastReport;
  const p = s.running ? player.store.progress(room.map) : { got: 0, need: Math.max(0, (s.total || 0) - (s.have || 0)) };
  const body = { map: room.map, done: !!s.done, running: !!s.running, got: p.got, need: p.need };
  const key = JSON.stringify(body);
  if (key === reported) return lastReport;
  clearTimeout(reportTimer);
  const wait = body.running && reportedRunning === body.map ? reportedAt + 2000 - Date.now() : 0;
  if (wait > 0) {
    reportTimer = window.setTimeout(reportFiles, wait);
    return lastReport;
  }
  reported = key;
  reportedRunning = body.running ? body.map : '';
  reportedAt = Date.now();
  return (lastReport = ask('files', body));
}

/**
 * Says to the lobby that the player is ready, or no longer is. The lobby hears first what the page has of the room's
 * map, because it takes a player as ready only with the map.
 */
export async function readyNow(ready: boolean): Promise<boolean> {
  await reportFiles();
  return act('ready', { ready });
}
