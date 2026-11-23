// web/src/windows.ts - games that the player's own PC plays, outside the page.
//
// A Black Ops room's game can be played in the Windows client instead of the page: the player runs the line that
// their profile shows (playLine). A Black Ops II room's game is always played on the player's PC, in their own copy of
// Plutonium, which the launcher starts into the room's game. A player can also join by hand, with a line for the
// game's console (t6Console).
import type { Room } from './api';
import { canRun } from './engine';
import { me, page, view } from './lobby';
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

// The launcher (launcher/): a program on the player's PC, which a link of its scheme starts once it is installed.
// The link names this server and the player, by their key. The launcher asks the server what to play, gets what the
// PC lacks for it, waits for the room's game, and starts the player's game into it. It has no window: the page says
// what it does, from what the server says of it (api.ts: You.launcher and the fields after it).
//
// The browser asks the player whether the link may open the launcher. On a PC without the launcher, the browser does
// nothing with the link, and tells the page nothing either way. So the page watches what follows a click (openLauncher):
// the browser's question takes the focus from the page, and a launcher that starts calls the server. When neither
// happens within LAUNCHER_WAIT, the PC has no launcher, as far as the page can tell (launcherMissed).
//
// The launcher's releases are on GitHub. The server says where the latest release's setup is (server/web.go: GET
// /server), and the page links to it. GitHub sends the file as a download, so the page stays.

/** Whether the server has said where the launcher is. Until it has, a Black Ops II room is joined by hand. */
export const launcherOn = (): boolean => !!served.launcher;

/**
 * Whether the server sends game files. The launcher of a player whose copy of Black Ops II lacks the room's map can
 * then download the server's, and use them instead.
 */
export const launcherDownloads = (): boolean => !!served.downloads;

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

/** Notes that the player's launcher runs, as the server says. */
export function launcherSeen(): void {
  launcherMissed = false;
  if (launcherHere) return;
  launcherHere = true;
  store.set('zombies-launcher', '1');
}

// How long a click waits for a sign of the launcher. A launcher that starts tells the server so at once, before it
// looks for a later release of itself (internal/launcherapi: POST /launcher/started), which a program that Windows
// starts does in well under a second. A launcher of a release before that call says it only after its look at GitHub,
// a second or so later. A sign that comes late still takes the line that installs the launcher away (launcherSeen).
const LAUNCHER_WAIT = 1500;
let stopWatching = (): void => {};

/**
 * Opens the launcher. tls=0 is for a page on plain HTTP, which the launcher takes from a server on its own network.
 * Where nothing follows the click, the room shows the line that installs the launcher (launcherMissed).
 */
export function openLauncher(): void {
  stopWatching();
  let asked = false; // the browser asked whether to open the launcher, or another window took the focus
  const away = () => {
    asked = true;
  };
  const timer = window.setTimeout(() => {
    stopWatching();
    if (asked || (view && view.you.launcher) || launcherMissed) return;
    console.log('lobby: no launcher started: the room shows where to install it');
    launcherMissed = true;
    page.redraw();
  }, LAUNCHER_WAIT);
  stopWatching = () => {
    clearTimeout(timer);
    removeEventListener('blur', away);
    document.removeEventListener('visibilitychange', away);
  };
  addEventListener('blur', away);
  document.addEventListener('visibilitychange', away);
  location.assign('zombies-launcher://' + location.host + '/play?key=' + (me.key || '') +
    (location.protocol === 'https:' ? '' : '&tls=0'));
}
