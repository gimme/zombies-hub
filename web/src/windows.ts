// web/src/windows.ts - games that the player's own PC plays, outside the page.
//
// A Black Ops room's game can be played in the Windows client instead of the page: the player runs the line that
// their profile shows (playLine). A Black Ops II room's game is always played on the player's PC, in their own copy of
// Plutonium, which the launcher starts into the room's game. A player can also join by hand, with a line for the
// game's console (t6Console).
import type { Room } from './api';
import { canRun } from './engine';
import { me, view } from './lobby';
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
 * started in LAN mode. It sets the name the game is to know the player by, gives the room's password, and connects.
 * The name is the player's, with a number where another player in the room has the same (server/rooms.go:
 * roomView.As). The password is the same for each of the room's games.
 */
export const t6Console = (room: Room): string =>
  'name "' + (room.as || me.name) + '";password ' + room.password + ';connect ' + location.hostname + ':' +
  (udpPort() || location.port || 3075);

// The launcher (launcher/): a program on the player's PC, which a link of its scheme starts once it is installed.
// The link names this server and the player, by their key. The launcher asks the server what to play, gets what the
// PC lacks for it, waits for the room's game, and starts the player's game into it. It has no window: the page says
// what it does, from what the server says of it (api.ts: You.launcher and the fields after it).
//
// The browser asks the player whether the link may open the launcher. On a PC without the launcher, the browser does
// nothing with the link, and tells the page nothing either way.
//
// The launcher's releases are on GitHub. The server says where the latest release's setup is (server/web.go: GET
// /server), and the page links to it. GitHub sends the file as a download, so the page stays.

/** Whether the server has said where the launcher is. Until it has, a Black Ops II room is joined by hand. */
export const launcherOn = (): boolean => !!served.launcher;

/** The address of the launcher's latest release, which installs it. */
export const launcherSetup = (): string => served.launcher || '';

/**
 * Whether a launcher of the player's has run from this browser. Until one has, a Black Ops II room shows the line that
 * installs it.
 */
export let launcherHere = store.get('zombies-launcher') === '1';

/** Notes that the player's launcher runs, as the server says. */
export function launcherSeen(): void {
  if (launcherHere) return;
  launcherHere = true;
  store.set('zombies-launcher', '1');
}

/** Opens the launcher. tls=0 is for a page on plain HTTP, which the launcher takes from a server on its own network. */
export const openLauncher = (): void => location.assign('zombies-launcher://' + location.host + '/play?key=' +
  (me.key || '') + (location.protocol === 'https:' ? '' : '&tls=0'));
