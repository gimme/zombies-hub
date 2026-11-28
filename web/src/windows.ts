// web/src/windows.ts - games that the player's own PC plays, outside the page.
//
// A Black Ops room's game can be played in the Windows client instead of the page: the player runs the line that
// their profile shows (playLine). A Black Ops II room's game is always played on the player's PC, in their own copy of
// Plutonium, which the launcher starts into the room's game. A player can also join by hand, with a line for the
// game's console (t6Console). A Black Ops III room's game is played on the player's PC too, on BOIII, which the
// launcher starts and joins to the room's game as well, and which a player can join by hand too (t7Console).
import type { Room, View } from './api';
import type { MainButton } from './controls';
import { canRun } from './engine';
import { launcherLink } from './link';
import { me, offline, page, view } from './lobby';
import { params } from './params';
import { served } from './server';
import { store } from './storage';

/**
 * Whether the player plays their room's Black Ops game outside this page: their Windows client is open, as the lobby
 * sees it (server/rooms.go: onWindows), or the page can't run the game. Such a page joins no game and needs no map.
 */
export const onWindows = (): boolean => !!view && (!!view.you.windows || !canRun);

// The UDP port that players join the server's games at, or 0 until the server has said.
const udpPort = (): number => served.port || 0;

/**
 * The line that plays the player's room's game in the Windows client, as them. Their key takes them to their seat
 * (server/relay.go).
 */
export const playLine = (): string =>
  'Play.bat ' + location.hostname + (udpPort() ? ':' + udpPort() : '') + ' ' + (me.key || '');

/**
 * The line that joins a Black Ops II room's game by hand, for the console of the player's game, which Plutonium
 * started in LAN mode. It gives the room's password, which is the same for each of the room's games, and connects.
 * It sets no name: the game's console has no command for it. The game plays under the name it started with.
 */
export const t6Console = (room: Room): string =>
  'password ' + room.password + ';connect ' + location.hostname + ':' + (udpPort() || location.port || 3075);

/**
 * The line that joins a Black Ops III room's game by hand, for the console of the player's game, which BOIII started.
 * It gives the room's password and connects, as t6Console does. The game's console takes a line as a command when the
 * line starts with a slash.
 */
export const t7Console = (room: Room): string =>
  '/password ' + room.password + ';connect ' + location.hostname + ':' + (udpPort() || location.port || 3075);

// The launcher (launcher/): a program on the player's PC, which a link of its scheme starts once it is installed.
// The link names this server and the player, by their key. The launcher asks the server what to play, gets what the
// PC lacks for it, waits for the room's game, and starts the player's game into it. It has no window: the page says
// what it does, from what the server says of it (api.ts: You.launcher and the fields after it).
//
// The browser asks the player whether the link may open the launcher. On a PC without the launcher, the browser does
// nothing with the link, and tells the page nothing either way. So the page watches what follows a click (openLauncher):
// the browser's question takes the focus from the page, and a launcher that starts calls the server. When neither
// happens within LAUNCHER_WAIT, the PC has no launcher, as far as the page can tell (launcherMissed). Until then, the
// room's button says that the launcher starts (launcherOpening).
//
// The launcher's releases are on GitHub. The server says where the latest release's setup is (server/web.go: GET
// /server), and the page links to it. GitHub sends the file as a download, so the page stays.
//
// The launcher is a Windows program. On a Mac it runs in Wine, and a script of the same release installs it there
// and runs it (launcher/mac.sh). The page shows a Mac the line that fetches the script from GitHub and runs it, for
// the player to paste into Terminal (launcherMacLine). A script that the browser downloaded would need Terminal too:
// macOS opens it in an editor, and lets no downloaded script run at a double click.

/**
 * Whether the page is on a Mac, as the browser says: Chromium's client hints, or else the platform's name
 * (settings.ts has the same test, for a key's name). ?mac=1 or ?mac=0 in the page's URL says so instead, to see a
 * Mac's page on a PC.
 */
export const onMac = ((): boolean => {
  const forced = params.get('mac');
  if (forced === '1' || forced === '0') return forced === '1';
  const hints = (navigator as unknown as { userAgentData?: { platform?: string } }).userAgentData;
  return hints && hints.platform ? hints.platform === 'macOS' : /^Mac/.test(navigator.platform || '');
})();

/** Whether the server has said where the launcher is. Until it has, a Black Ops II room is joined by hand. */
export const launcherOn = (): boolean => !!served.launcher;

/**
 * Whether the launcher starts a game on this computer: any that it starts on a PC, and Black Ops II on a Mac, where
 * Black Ops III doesn't run.
 */
export const launcherStarts = (of?: string): boolean => launcherOn() && !(onMac && of === 't7');

/**
 * The line that installs the launcher on a Mac, for Terminal, or '' on another system and until the server has said
 * where the script is. bash runs the script that curl fetches. The script then has its own text, which it keeps for
 * the launcher's links (launcher/mac.sh: keep_script).
 */
export const launcherMacLine = (): string =>
  onMac && served.launcherMac ? `/bin/bash -c "$(curl -fsSL ${served.launcherMac})"` : '';

/**
 * Whether the server sends a game's files to launchers (Title.downloads). The launcher of a player whose copy of the
 * game lacks the room's map can then download the server's, and use them instead.
 */
export const launcherDownloads = (v: View, of?: string): boolean => !!v.titles.find((t) => t.id === of)?.downloads;

/** The address of the launcher's latest release, which installs it. */
export const launcherSetup = (): string => served.launcher || '';

/**
 * Whether a launcher of the player's has run from this browser. Until one has, a Black Ops II room shows the line that
 * installs it.
 */
export let launcherHere = store.get('zombies-launcher') === '1';

/**
 * Whether the player's last click that opened the launcher started none (openLauncher), until a launcher of theirs
 * runs. The room then shows the line that installs it, as it does in a browser that has never seen one.
 */
export let launcherMissed = false;

/**
 * Whether the player clicked to open the launcher, and no launcher of theirs has shown itself since (openLauncher).
 * The room's button says meanwhile that the launcher starts, as it goes on saying once the launcher has. A click that
 * left the button as it was would look as if it did nothing.
 */
export let launcherOpening = false;

/** Notes that the player's launcher runs, as the server says. */
export function launcherSeen(): void {
  launcherMissed = false;
  if (launcherOpening) {
    launcherOpening = false;
    stopWatching();
  }
  if (launcherHere) return;
  launcherHere = true;
  store.set('zombies-launcher', '1');
}

// How long the launcher has to be at a step before the room's button says the step. A step that is over at once, as
// the check of game files that are all there, would otherwise show for a blink.
const STEP_SHOWS = 800;
let step = ''; // the step that the launcher last said it is at ("Checking files"), or ''
let stepSince = 0; // when the page first heard of that step
let stepShown = ''; // the step that the button says
let stepTimer = 0;

/**
 * Returns the step of the launcher that the room's button is to say, or '' for none, when the launcher says that it
 * is at the step doing. That is doing once the launcher has been at it for STEP_SHOWS. Until then it is the step
 * that the button said before, and none at once when the launcher is at none.
 */
export function launcherStep(doing: string): string {
  if (doing !== step) {
    step = doing;
    stepSince = Date.now();
    clearTimeout(stepTimer);
    if (doing) stepTimer = window.setTimeout(() => page.redraw(), STEP_SHOWS);
  }
  if (!doing || Date.now() - stepSince >= STEP_SHOWS - 20) stepShown = doing; // a timer may come a little early
  return stepShown;
}

/**
 * What a view's main button says while the player's launcher is at something, and the button so does nothing: the
 * launcher starts after a click (launcherOpening), downloads, or is at a step that it names (launcherStep). A room's
 * button and Play local's both go by it (room.ts: showRoomButton, solo.ts: showSoloPC). It is null when the launcher
 * leaves the button to its view: no launcher runs and none starts, the launcher's last try failed, or the launcher
 * has the PC ready.
 *
 * With launches, the button is also the one that launches the game, as Play local's is: it then says the launcher's
 * step while the PC is ready too, which is the game that launches. A room has a button of its own for that
 * (room.ts: showLaunch), and its main button is the room's again once the PC is ready.
 */
export function launcherSays(v: View, launches: boolean): MainButton | null {
  const you = v.you;
  const says = (label: string): MainButton => ({ label, disabled: true });
  if (you.launcherFailed) return null;
  // A page that can't reach its server hears nothing of the launcher: its button says that the game starts.
  if (!you.launcher) return launcherOpening ? says(offline ? 'Starting...' : 'Connecting...') : null;
  if (you.launcherDownloading) return says(`Downloading · ${Math.floor((you.launcherGot || 0) * 100)}%`);
  const doing = launcherStep(you.launcherDoing || '');
  if (!you.launcherReady) return says((doing || 'Connecting') + '...');
  return launches && doing ? says(doing + '...') : null;
}

/**
 * What the page knows of a game of the player's own PC, on this PC: that the launcher found the game there, whether
 * the folder is the player's own copy of it (own), which the launcher downloads nothing into, and the maps that the
 * folder has, or null where the launcher has not said. It is what the player's launcher says while it runs and is at
 * that game. The launcher stops saying it a while after the player's last room, so the browser's storage keeps what
 * it said last, and the page goes by that meanwhile: it is what the PC most likely has still. null: no launcher of
 * the player's has had the game ready in this browser.
 */
export interface OnPC { own: boolean; maps: string[] | null }

export function onPC(v: View, of: string): OnPC | null {
  const you = v.you;
  const key = 'zombies-pc-' + of;
  if (you.launcher && you.launcherOf === of && (you.launcherFolder || you.launcherReady)) {
    const now: OnPC = { own: !!you.launcherOwn, maps: you.launcherFolder ? you.launcherMaps || [] : null };
    const text = JSON.stringify(now);
    if (store.get(key) !== text) store.set(key, text);
    return now;
  }
  try {
    const kept = JSON.parse(store.get(key) || 'null') as OnPC | null;
    if (!kept || typeof kept !== 'object') return null;
    const maps = Array.isArray(kept.maps) ? kept.maps.filter((m) => typeof m === 'string') : null;
    return { own: kept.own === true, maps };
  } catch {
    return null; // what the storage has is none of this page's
  }
}

// How long a click waits for a sign of the launcher, where the browser asked nothing. A launcher that starts tells
// the server so at once, before it looks for a later release of itself (internal/launcherapi: POST
// /launcher/started), which a program that Windows starts does in well under a second. A launcher of a release
// before that call says it only after its look at GitHub, a second or so later. On a Mac, Wine starts first, and the
// launcher said so two seconds after the click on the one Mac that was timed. A sign that comes late still takes the
// line that installs the launcher away (launcherSeen).
const LAUNCHER_WAIT = onMac ? 4000 : 1500;
// How long the button goes on saying that the launcher starts once the browser's question is answered, which the
// page takes it to be when it has the focus again. A player who answered no then has the button back.
const LAUNCHER_ANSWERED = onMac ? 6000 : 5000;
// How long it says so at most, for a question that stays open.
const LAUNCHER_LIMIT = 60000;
let stopWatching = (): void => {};

/**
 * Opens the launcher. tls=0 is for a page on plain HTTP, which the launcher takes from a server on its own network.
 * The room's button says that the launcher starts (launcherOpening) until one shows itself, or until nothing more
 * can follow the click. Where nothing followed it at all, the room then shows the line that installs the launcher
 * (launcherMissed).
 *
 * With solo, the link asks the launcher to start a game for the player alone, under their name: the game of, with
 * the map of it that the link names, which the launcher gets first where its game files lack it (a room's Launch
 * game names the room's). A
 * page that can't reach its server sends no key: the launcher then calls no server, and plays from what the PC has
 * (internal/launcherapi). The server can't tell such a page of the launcher, so its button says that the game starts
 * for a while, and the page takes nothing for a sign that the PC has no launcher.
 *
 * Without solo, the launcher gets the PC ready and starts nothing. A room's button names no game: the launcher goes
 * by the player's room. Play local, which a player in no room is at, names the game of, and with map, a map of it
 * for the launcher to download (solo.ts).
 */
export function openLauncher(solo = false, of = '', map = ''): void {
  const link = launcherLink({ host: location.host, tls: location.protocol === 'https:',
    key: solo && offline ? '' : me.key || '', solo, name: (view && view.you.name) || me.name || '', of, map });
  stopWatching();
  if (offline) {
    launcherOpening = true;
    const timer = window.setTimeout(() => {
      launcherOpening = false;
      page.redraw();
    }, LAUNCHER_ANSWERED);
    stopWatching = () => clearTimeout(timer);
    page.redraw();
    location.assign(link);
    return;
  }
  let asked = false; // the browser asked whether to open the launcher, or another window took the focus
  let limit = 0;
  // Nothing more is expected of the click: the button is as it was before it.
  const over = () => {
    stopWatching();
    if (!launcherOpening) return;
    launcherOpening = false;
    page.redraw();
  };
  const away = () => {
    asked = true;
  };
  const back = () => {
    if (!asked) return;
    clearTimeout(limit);
    limit = window.setTimeout(over, LAUNCHER_ANSWERED);
  };
  const timer = window.setTimeout(() => {
    if (asked || (view && view.you.launcher) || launcherMissed) return;
    console.log('lobby: no launcher started: the room shows where to install it');
    launcherMissed = true;
    launcherOpening = false;
    stopWatching();
    page.redraw();
  }, LAUNCHER_WAIT);
  limit = window.setTimeout(over, LAUNCHER_LIMIT);
  stopWatching = () => {
    clearTimeout(timer);
    clearTimeout(limit);
    removeEventListener('blur', away);
    removeEventListener('focus', back);
    document.removeEventListener('visibilitychange', away);
  };
  addEventListener('blur', away);
  addEventListener('focus', back);
  document.addEventListener('visibilitychange', away);
  launcherOpening = true;
  page.redraw();
  location.assign(link);
}
