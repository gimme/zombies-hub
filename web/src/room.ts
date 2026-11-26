// web/src/room.ts - the room that the player is in.
//
// The room shows its game, its map, its options, its members, and one button. The game is one of those that the
// server hosts (server/title.go), and the map is one of that game's. The options are whether it is private, how its
// players play against each other (its mode, which the code calls its versus: co-op, Grief or Turned), how many
// players it takes, and its mods. While the room has no game running, the host picks the game, on a server of two,
// and the mode, and a map of those, and sets the options, and the others see them. The others may suggest a map of
// the room's game and mode.
//
// A room on a Black Ops map has the button that opens Black Ops' options menu, under the room's button (main.ts:
// settingsInit). A room on a Black Ops II map has none: the menu sets the game that the browser plays, and a Black Ops
// II game is the player's own Plutonium's.
//
// The button says what the player can do now, and does it (main.ts: roomDoes). While the room has no game, the button
// gets the room's map until the browser's store has it (files.ts: mapButton), and is Ready from then on. The game
// starts once everyone is ready, after a countdown (countdown.ts). While the room's game runs, the button is the way
// into the game for a player who is outside it, and otherwise only says where the player is (gameButton).
//
// The game of a room on a Black Ops II or a Black Ops III map is played on the player's own PC (windows.ts). The
// room's button then opens the launcher, and a box has the rest: how to install the launcher, and how to join by hand
// (showPC).
//
// While the room's game runs, its maps, and its host's pick of its game, stay as they were, and can't be picked: the
// room looks the same, and nothing under them moves.
import type { Member, Mod, Room, View } from './api';
import type { Options } from './controls';
import { avatar } from './avatar';
import {
  badgeSay, copy, flipSwitch, optionButtons, optionShow, setButton, showOnOff, switchButton, switchOn, switchShow,
} from './controls';
import { countDown } from './countdown';
import { $, el } from './dom';
import { canRun, started } from './engine';
import { knowMap, type MapButton, mapButton, showNote } from './files';
import { checkLock, endNote, forgetOtherTab, lastEnd, lockElsewhere, otherTab } from './game';
import { act, ask, me, offline, view } from './lobby';
import { showDesc, showMaps } from './mapgrid';
import { lastTitle, mapInfo, mapShot } from './maps';
import { modNames, modOn, modValue } from './mods';
import { mayLeaveGame } from './question';
import { bo1Settings } from './settings';
import { store } from './storage';
import { CLIENTS, mapsOf, ownPC, titleName, versusesOf, versusOf } from './titles';
import {
  launcherDownloads, launcherHere, launcherMacLine, launcherMissed, launcherOpening, launcherSeen,
  launcherSetup, launcherStarts, launcherStep, onMac, onWindows, t6Console, t7Console,
} from './windows';

// A gamepad, on a player whose page reads one: its two grips, its d-pad and two of its buttons.
const PAD_ICON = '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" ' +
  'stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
  '<path d="M7 6.5h10a4.5 4.5 0 0 1 4.4 3.6l.8 4.4a3 3 0 0 1-5.3 2.4L15.5 15h-7l-1.4 1.9a3 3 0 0 1-5.3-2.4l.8-4.4' +
  'A4.5 4.5 0 0 1 7 6.5z"/><path d="M8 9v3.6M6.2 10.8h3.6M15.4 11.9h.01M17.9 9.7h.01"/></svg>';

const SEAT_ICON = '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" ' +
  'stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
  '<circle cx="12" cy="8" r="4"/><path d="M4 21a8 8 0 0 1 16 0"/></svg>';

/** Draws the room from a view of the lobby. */
export function showRoom(v: View, room: Room): void {
  rememberRoomTitle(room);
  $('roomcode').textContent = room.code;
  // The room's game, beside its code, while it has one. A game starts first. A Black Ops II room then waits, once its
  // server is up, for the players' own games to join. A game is in play from round 1.
  const states: Record<string, string> = {
    loading: room.up ? 'Waiting' : 'Starting', playing: 'In game', ending: 'Ending',
  };
  const game = states[room.state] || '';
  $('roomgame').textContent = game;
  $('roomgame').classList.toggle('hidden', !game);
  const note = [endNote(lastEnd), room.note].filter(Boolean).join(' ');
  $('note').textContent = note;
  $('note').classList.toggle('hidden', !note);
  const open = room.state === 'open';

  const map = mapInfo(room.map);
  mapShot($('mapshot'), room.map);
  // Over the map's name, its game, on a server of two. That is where the others see the room's game.
  $('mapkicker').textContent = (v.titles.length > 1 && titleName(v.titles, room.of)) || 'Map';
  $('maptitle').textContent = map.title;
  showDesc($('mapdesc'), map.desc || '');
  showRoomTitle(v, room, open);
  showRoomVersus(v, room, open);
  showRoomMaps(v, room);
  // Whether the room is private: a switch for the host, and a badge for the others.
  $('visibility').classList.toggle('hidden', !room.host);
  switchShow($('visibility'), room.private);
  $('vistext').classList.toggle('hidden', room.host);
  showOnOff($('vistext'), room.private);
  showRoomSize(v, room);
  // The room's mods are those that the games of its title can have.
  showRoomMods(v.mods.filter((m) => m.titles.includes(room.of)), room, open);
  showPC(v, room);

  // The list has a row for each seat of the room, with an empty row for each seat that no one has. The list, and
  // what is below it, then stays in place as players come and go.
  const list = $('members');
  list.textContent = '';
  for (const m of room.members) list.append(memberRow(m, room, open));
  for (let i = room.members.length; i < room.size; i++) {
    const li = el('li', 'empty');
    li.setAttribute('aria-label', 'An empty seat');
    const seat = el('span', 'avatar');
    seat.innerHTML = SEAT_ICON;
    li.append(seat);
    list.append(li);
  }
  $('count').textContent = room.members.length + ' / ' + room.size;

  // Once the page is not to be in the room's game, the room's next game, or rejoining this one, is any tab's.
  if (!room.join) forgetOtherTab();
  // Another tab of the player's plays the game, or did until it closed. The button moves the game to this tab.
  const elsewhere = !!room.join && otherTab && !started && !onWindows();
  if (elsewhere) checkLock();
  showRoomButton(v, room);
  // The host can end a game that no one plays, whose characters wait for players who may not come back.
  $('endgame').classList.toggle('hidden', !room.canEnd);
  // Black Ops' options menu, in a room on a Black Ops map. A browser without a private file system has no menu
  // (settings.ts: available).
  $('roomsettings').classList.toggle('hidden', room.of !== 't5' || !bo1Settings.available);
  $('roomstate').textContent = elsewhere && lockElsewhere ? 'Your game is open in another tab.' : '';
}

// A member's row in the list: their picture, their name, what they are in the room, and a badge of their state.
function memberRow(m: Member, room: Room, open: boolean): HTMLLIElement {
  const mine = m.id === me.id;
  const ready = open && !!m.ready;
  const li = el('li', mine ? 'mine' : '');
  const who = el('div', 'who grow');
  const name = el('span', 'name', m.name);
  if (mine) name.append(el('span', 'sr', ' (you)'));
  // A member has a team in a room with Grief on, or on a map of Grief (server/mods.go: teamOf). On Windows says that
  // they play a Black Ops game in the Windows client and not in their page. Every player of a Black Ops II or Black
  // Ops III game plays it outside their page, on Windows or on a Mac, so the room says it of none.
  const what = [m.team && 'Team ' + m.team, m.host && 'Host', m.windows && !ownPC(room.of) && 'On Windows',
    m.away && 'Away'].filter(Boolean).join(' · ');
  const line = el('div', 'line');
  line.append(name);
  if (m.gamepad) {
    const pad = el('span', 'pad');
    pad.innerHTML = PAD_ICON;
    pad.title = 'Gamepad connected';
    pad.setAttribute('role', 'img');
    pad.setAttribute('aria-label', 'Gamepad connected');
    line.append(pad);
  }
  who.append(line);
  if (what) who.append(el('span', 'what', what));
  const badge = el('span', 'badge');
  // The launcher of a player of a game on their own PC may download the game files while the room's game already
  // waits for them.
  const downloads = ownPC(room.of) && !!m.downloading;
  if (mine && room.left) { // their character is gone from the game
    badge.classList.add('off');
    badgeSay(badge, 'Out of game');
  } else if (m.inGame || (ready && !downloads)) {
    badge.classList.add('on');
    badgeSay(badge, m.inGame ? 'In game' : 'Ready', ready);
  } else if (open || downloads) {
    badge.classList.add('off');
    badgeSay(badge, m.downloading ? 'Downloading' : m.missing ? 'Missing map' : 'Not ready');
  }
  li.append(avatar(m.name, m.avatar, mine, m.place), who, badge);
  // How far their download is, as a bar along the row.
  if ((open || ownPC(room.of)) && m.got && m.got > 0) {
    const got = el('span', 'got');
    got.style.width = (m.got * 100).toFixed(1) + '%';
    li.append(got);
  }
  return li;
}

// What the room's button says and does. does names what a click does (main.ts: roomDoes).
interface RoomButton extends MapButton {
  does?: string;
  primary?: boolean;
}

/** How the room's button gets the room's map, while the store lacks it: 'download' or 'import' (files.ts). */
export let readyGets = '';

/** Draws the room's button, the line under it, and the countdown on it. */
export function showRoomButton(v: View, room: Room): void {
  const open = room.state === 'open';
  const windows = onWindows();
  // The game of a Black Ops II room is played by the player's own Plutonium, and that of a Black Ops III room by
  // their own BOIII. The page needs no map for it, and does not run the engine.
  const pc = ownPC(room.of);
  const game = titleName(v.titles, room.of);
  let m: RoomButton;
  let lacks = ''; // what the line under the button says of a player's copy of the game that lacks the room's map
  if (!open) m = gameButton(v, room);
  else if (!canRun && !v.you.windows && !pc) m = { label: "Can't play in this browser", disabled: true };
  else if (onMac && room.of === 't7') m = { label: "Can't play on a Mac", disabled: true };
  else {
    // No one can be ready while all of the server's games are running (server/rooms.go: full), or while the room has
    // fewer players than a game needs.
    const short = v.minPlayers - room.members.length;
    const label = v.you.ready ? 'Unready' : v.full ? 'Server full' : short > 0 ? `Need ${short} more` : 'Ready';
    if (!windows && !pc) knowMap(room.map);
    m = windows || pc ? { label, ready: true } : mapButton(room.map, label);
    if (m.ready && !v.you.ready && (v.full || short > 0)) m.disabled = true;
    m.does = m.gets ? 'map' : 'ready';
    m.primary = !!m.gets || (!!m.ready && !v.you.ready);
    // In a room of a game of the players' own PCs, once the server has said where the launcher is, the button opens
    // the launcher first. The launcher then gets what the PC lacks for the game, and the button says how far that
    // is. After that the button is Ready. Not for a player who joins by hand, with that checkbox checked.
    //
    // A copy of the game without the pack of the room's map lacks the map (server/launcher.go: pcLacks). The player
    // is then not ready until the host picks another map. Where the server sends the game's files, the button is
    // Download: it opens the launcher, which downloads the map into the game files that it downloaded, or asks
    // whether to download the server's game files and use them in place of the player's own copy.
    if (pc && launcherStarts(room.of) && !v.you.ready && !handOn()) {
      const mine = room.members.find((x) => x.id === me.id);
      if (v.you.launcherFailed) m = { label: 'Try again', does: 'launch', primary: true };
      else if (!v.you.launcher) {
        // From the click until the launcher shows itself, the button says what it goes on saying once the launcher
        // has (windows.ts: launcherOpening).
        m = launcherOpening ? { label: 'Connecting...', disabled: true }
          : { label: 'Connect', does: 'launch', primary: true };
      } else if (!v.you.launcherReady) {
        // The button says the launcher's step once the launcher has been at it for a moment (windows.ts: launcherStep).
        m = { label: mine && mine.downloading ? `Downloading · ${Math.floor((mine.got || 0) * 100)}%`
          : (launcherStep(v.you.launcherDoing || '') || 'Connecting') + '...', disabled: true };
      } else if (mine && mine.missing) {
        const sends = launcherDownloads(v, room.of);
        lacks = `Your ${game} doesn't have this map` + (sends
          ? '. The launcher can download it from the server.'
          : ': it comes with a map pack.');
        m = sends ? { label: 'Download', does: 'launch', primary: true } : { label: 'Missing map', disabled: true };
      }
    }
  }
  readyGets = m.gets || '';
  $('ready').dataset.does = m.disabled ? '' : m.does || '';
  setButton($('ready'), m, m.primary);
  showNote($('roomnote'), room.map, m, open && !windows && canRun && !pc);
  // The launcher has no window, so the room says what it failed at, and what the player's copy of the game lacks.
  const says = pc ? v.you.launcherFailed || lacks : '';
  if (says) {
    $('roomnote').textContent = says;
    $('roomnote').classList.remove('hidden');
  }
  countDown(open && room.startsIn ? room.startsIn : 0, m.label);
}

// What the room's button says and does while the room's game runs.
function gameButton(v: View, room: Room): RoomButton {
  const mine = room.members.find((m) => m.id === me.id);
  const said = (label: string): RoomButton => ({ label, disabled: true });
  if (room.state === 'ending') return said('Ending the game...');
  if (room.left) return said('Out of the game'); // their character is gone from the game
  if (!room.seat) return said('Game in progress'); // the game began without them
  if (!ownPC(room.of)) {
    // Another tab of the player's plays the game, or did until it closed. The button moves the game to this tab.
    if (room.join && otherTab && !started && !onWindows()) {
      return { label: lockElsewhere ? 'Play here' : 'Rejoin', does: 'here', primary: true };
    }
    // The player's character waits in the game for a few minutes while they are away, and the button takes them back
    // to it. On Windows, the line of the Windows client does.
    if (room.state === 'playing' && !room.join && !onWindows()) {
      return { label: 'Rejoin', does: 'rejoin', primary: true };
    }
    return said(mine && mine.inGame ? 'In game' : 'Game in progress');
  }
  // A game of the players' own PCs is joined by the player's own PC. The page knows of it what the server does:
  // whether their game is in it. Until it is, the button opens the launcher. A launcher that is already at it does
  // nothing more. A player who joins by hand, with that checkbox checked, gets no launcher, as at the Ready button
  // (showRoomButton).
  if (mine && mine.inGame) return said('In game');
  if (!launcherStarts(room.of) || handOn()) return said('Join by hand');
  // Their launcher has started their game, which is on its way in.
  if (v.you.launcher && v.you.launcherDoing) return said(v.you.launcherDoing + '...');
  return { label: 'Join the game', does: 'launch', primary: true };
}

/**
 * Draws the box of a room on a map of a game of the players' own PCs, Black Ops II or III. Nothing opens the
 * launcher but the room's button. The box has the
 * line that installs the launcher, and the line that joins by hand. The line that installs the launcher is there in a
 * browser that has never seen a launcher of the player's run, after a click of the room's button that started no
 * launcher (windows.ts: launcherMissed), and when the server refused the player's launcher as too old. That launcher
 * can't say so itself, because the server takes no report from it. On a Mac the line is one for Terminal, and it is
 * there too when the launcher said that the script that runs it is too old for it.
 */
export function showPC(v: View, room: Room): void {
  const box = $('pcbox');
  const pc = ownPC(room.of);
  box.classList.toggle('hidden', !pc);
  if (!pc) return;
  if (v.you.launcher) launcherSeen();
  // Where the launcher starts the game, joining by hand is a checkbox (handOn), which the browser remembers.
  // Checked, it shows the line that joins by hand, and the room's button opens no launcher (showRoomButton), nor does
  // the box say where to get one. Where no launcher starts the game, joining by hand is the only way: the line is
  // there, and no checkbox.
  const starts = launcherStarts(room.of);
  const byHand = !starts || handOn();
  $('pchandrow').classList.toggle('hidden', !starts);
  $('pchandline').classList.toggle('hidden', !byHand);
  // How to get to the game's console, in words that are made once for the game: a link in them keeps the focus.
  const how = $('pchandhow');
  if (how.dataset.of !== room.of) {
    how.dataset.of = room.of || '';
    how.replaceChildren(...inWords(BY_HAND[room.of || ''] || []));
  }
  $('pcconsole').textContent = room.of === 't7' ? t7Console(room) : t6Console(room);
  placeInstall($('pcbox'));
  showInstall(v, !byHand, room.of);
}

/**
 * Puts what installs the launcher (#pcinstall) into box: the room's box for Black Ops II, or Play local's (solo.ts).
 * The two never show at once, so the page has it once, and draws it wherever it is (showInstall).
 */
export function placeInstall(box: HTMLElement): void {
  const install = $('pcinstall');
  if (install.parentElement === box) return;
  if (box.id === 'pcbox') box.prepend(install);
  else box.append(install);
}

/**
 * Draws what installs the launcher, where the launcher is to start the game of (wanted): the link to its setup, or on
 * a Mac the line for Terminal, and the "?" by them, with what the launcher is. It shows in a browser that has never
 * seen a launcher of the player's run, after a click that started none (windows.ts: launcherMissed), and when the
 * launcher is too old (View.you.launcherOld).
 */
export function showInstall(v: View, wanted: boolean, of = 't6'): void {
  installOf = of;
  const old = !!v.you.launcherOld;
  // A Mac has no program to download: the launcher is installed there by a line for Terminal (windows.ts:
  // launcherMacLine), which the box shows in place of the link. The same line brings an old launcher up to date.
  const line = launcherMacLine();
  const get = wanted && (!launcherHere || old || launcherMissed);
  $('pcinstall').classList.toggle('hidden', !get);
  // A Mac's launcher is old also when the script that runs it is (launcher/mac.go), which is no matter of the server.
  const why = old ? (line ? 'Your Zombies Launcher needs an update.' : 'Your Zombies Launcher is too old for this server.')
    : launcherMissed ? 'Nothing happened?' : 'First time?';
  $('pcget').classList.toggle('hidden', !get || !!line);
  $('pcwhy').textContent = why;
  $('pcsetup').textContent = old ? 'Install the new one' : 'Install Zombies Launcher';
  $<HTMLAnchorElement>('pcsetup').href = launcherSetup();
  // The line has no button that copies it. A content blocker takes a page that puts a shell's line on the clipboard
  // for one that tricks the player into running it, and says so (uBlock Origin: "a potential ClickFix attack"). A
  // click on the line selects all of it.
  $('pcmac').classList.toggle('hidden', !get || !line);
  $('pcmachow').textContent = why + (old ? ' Install the new one with this line in Terminal:'
    : ' Install Zombies Launcher with this line in Terminal:');
  $('pcmacline').textContent = line;
  // The "?" by what installs the launcher shows what the launcher is and how it goes again, for a player to know
  // before they install it.
  const about = $('pcabout');
  about.classList.toggle('hidden', !get || !aboutShown);
  // The words are made once for the page's platform and the game, and not again at each view: a link in them keeps
  // the focus.
  const kind = (line ? 'mac ' : 'pc ') + of;
  if (about.dataset.of !== kind) {
    about.dataset.of = kind;
    about.replaceChildren(...inWords(line ? ABOUT_MAC : aboutPC(of)));
  }
  for (const b of $('pcinstall').querySelectorAll('button.what')) b.setAttribute('aria-expanded', String(aboutShown));
}

// What the launcher is, which the "?" shows (showPC): text, and the names of what the launcher installs, each with its
// site. The words name no game, as the launcher starts more than one, but for the client that the game of (installOf)
// plays on (titles.ts: CLIENTS). A Mac's are of what the line for Terminal installs (launcher/mac.sh), which is for
// Black Ops II alone. The app that removes the launcher there is "run": opened, it could be taken for the launcher's
// menu in the menu bar.
export type Words = (string | [name: string, href: string] | { text: string; hint: string })[];
export const PLUTONIUM: [string, string] = CLIENTS.t6;
const aboutPC = (of: string): Words => ['Zombies Launcher is a small program that helps start the game on this PC ' +
  'and connects it to the room. It also installs the required game client (', CLIENTS[of] || PLUTONIUM, ').'];
let installOf = 't6'; // the game that what installs the launcher was last drawn for (showInstall)
// How to get to the console that the line that joins by hand is for (showPC), by the game.
const BY_HAND: Record<string, Words> = {
  t6: ['In the console of Black Ops II Zombies, started by Plutonium in LAN mode:'],
  t7: ['Start Black Ops III with ', ['BOIII', 'https://github.com/Ezz-lol/boiii-free/releases/latest'],
    " (its boiii.exe goes in the game's folder). Once the room's game is up, type this in the game's console:"],
};
const ABOUT_MAC: Words = ['This script installs Zombies Launcher: a small program that helps start the game on this ' +
  'Mac and connects it to the room. It also installs the required game client (', PLUTONIUM, ') and its Mac ' +
  'compatibility tool (', ['plutonium-mac-aio', 'https://github.com/LuidgiAlt/plutonium-mac-aio'], '). That can ' +
  'take a few minutes. To uninstall the launcher, run the "Zombies Launcher" app.'];
let aboutShown = false; // whether the "?" was clicked, and shows it

/**
 * Words for the page: their text, each site they name as a link to it, and words with a hint, which shows when the
 * pointer is on them.
 */
export const inWords = (words: Words): (string | HTMLElement)[] => words.map((part) => {
  if (typeof part === 'string') return part;
  if (Array.isArray(part)) return site(part);
  const span = el('span', 'hint', part.text);
  span.title = part.hint;
  return span;
});

/** A link to a site, which opens in a tab of its own: the page stays. */
export function site([name, href]: [string, string]): HTMLAnchorElement {
  const a = el('a', '', name);
  a.href = href;
  a.target = '_blank';
  a.rel = 'noopener';
  return a;
}

/** Whether the player joins their room's game by hand, on their own PC, as that checkbox says (showPC). */
function handOn(): boolean {
  return $<HTMLInputElement>('pchand').checked;
}

/**
 * Tells the lobby whether the player joins by hand (server/rooms.go: setHand). Their launcher, which may still run
 * from their last game, is then sent no game: it would otherwise start their game into the room's, or have their game
 * that runs join it. The lobby forgets it when it starts again, so the page tells it each time the lobby's events start
 * (session.ts: listen).
 */
export function tellHand(): void {
  if (!offline && me.token) ask('hand', { hand: handOn() });
}

// The room's size (server/rooms.go: room.size), in the Settings tab: how many players it takes, which is 4 until the
// host sets it. The host picks among the sizes that a room on this server may have on its map, from the players that
// a game needs to the most that a room holds there: 8 on a map of Grief, four in each team. A size that the room has
// too many players for can't be picked. The others see the size. Where the room can have one size, there is nothing
// to pick, and no row (settingRow).
function showRoomSize(v: View, room: Room): void {
  const sizes = [];
  for (let n = v.minPlayers; n <= room.maxSize; n++) sizes.push({ value: n });
  const box = settingRow($('sizesetting'), $('sizetext'), room.host, { name: 'Room size', options: sizes },
    (size) => ({ size }));
  for (const b of box.querySelectorAll('button')) {
    b.disabled = Number(b.dataset.value) < room.members.length;
    b.title = b.disabled ? 'The room has more players' : '';
  }
  optionShow(box, room.size);
  badgeSay($('sizetext'), room.size + (room.size === 1 ? ' player' : ' players'));
}

/**
 * A setting of the room's with options (optionButtons), its row: the host's buttons, before the badge that shows the
 * others the setting, and hidden where there is only one option. The buttons are made again only when the options
 * change, so that a button keeps the focus as it is picked. Returns the buttons' box, for the caller to show the
 * setting in (optionShow).
 */
function settingRow<V extends number | string>(row: HTMLElement, badge: HTMLElement, host: boolean,
  options: Options<V>, set: (value: V) => object): Element {
  row.classList.toggle('hidden', options.options.length < 2);
  const key = JSON.stringify(options.options);
  if (row.dataset.key !== key) {
    row.dataset.key = key;
    row.querySelector('.seg')?.remove();
    badge.before(optionButtons(options, set));
  }
  const box = row.querySelector('.seg')!;
  box.classList.toggle('hidden', !host);
  badge.classList.toggle('hidden', host);
  return box;
}

// The room's mods (server/mods.go), in the Mods tab. The host sets each while the room has no game: a switch on or
// off, or a mod with options to one of them. The others see how the mods are set. Everyone is told without opening
// the tab: the tab counts the mods that are on, and the line above the room's button names them, or says None. A
// click on the line opens the tab. The tab's rows are made again only when the mods change, or who may set them, so
// that a control keeps the focus as it is set.
function showRoomMods(mods: Mod[], room: Room, open: boolean): void {
  const on = room.mods || [];
  const names = modNames(mods, on);
  const count = $('modcount');
  count.textContent = String(on.length);
  count.classList.toggle('none', !on.length);
  count.classList.toggle('hidden', !mods.length);
  $('tabmods').title = names.join(', ');
  const line = $('modsline');
  line.classList.toggle('hidden', !mods.length);
  line.classList.toggle('on', on.length > 0);
  $('modslinecount').textContent = String(on.length);
  $('modslinecount').classList.toggle('hidden', !on.length);
  showNames($('modsnames'), names.length ? names : ['None']);
  const list = $('panelmods');
  const control = (i: number) => list.children[i].lastChild as HTMLElement;
  const key = JSON.stringify([mods, room.host && open]);
  if (list.dataset.key !== key) {
    list.dataset.key = key;
    list.textContent = '';
    // The change that the lobby is asked for names the mods as the tab's controls have them, with one of them as the
    // player just set it. The page's view may not have heard yet of a mod that was set a moment ago.
    const set = () => ({ mods: mods.map((m, i) => modSet(m, control(i))).filter(Boolean) });
    for (const mod of mods) {
      const row = el('div', 'setting mod');
      const text = el('span', '', mod.name);
      text.append(el('span', 'desc', mod.desc));
      row.append(text);
      if (!(room.host && open)) {
        row.append(el('span', 'badge'));
      } else if (mod.options) {
        row.append(optionButtons({ name: mod.name, options: mod.options }, set));
      } else {
        const b = switchButton(mod.name);
        b.onclick = () => flipSwitch(b, set);
        row.append(b);
      }
      list.append(row);
    }
  }
  mods.forEach((mod, i) => {
    const c = control(i);
    const value = modValue(on, mod);
    if (c.classList.contains('switch')) switchShow(c, value === true);
    else if (c.classList.contains('seg')) optionShow(c, Number(value));
    else if (!mod.options) showOnOff(c, value === true);
    else {
      // For a player who can't set it, a mod with options shows its value, as on, or as off at the game's own value.
      c.classList.toggle('on', modOn(on, mod));
      c.classList.toggle('off', !modOn(on, mod));
      badgeSay(c, String(value), modOn(on, mod));
    }
  });
}

// What a mod's control in the tab has the mod at, as the lobby names it. null for a mod that is off.
function modSet(mod: Mod, control: HTMLElement): string | null {
  if (!mod.options) return switchOn(control) ? mod.id : null;
  const picked = control.querySelector<HTMLElement>('[aria-checked=true]');
  return picked && picked !== control.firstChild ? mod.id + '=' + picked.dataset.value : null;
}

// The names on a line, which may not have room for all of them. The line shows as many whole names as fit, and then
// "…". It never shows a part of a name, which reads as a name itself: the line's own ellipsis cut "More Zombies" to
// "More …". The line's tooltip has all the names.
const lineNames = new WeakMap<HTMLElement, string[]>();

function showNames(box: HTMLElement, names: string[]): void {
  box.title = names.join(', ');
  lineNames.set(box, names);
  fitNames(box);
}

function fitNames(box: HTMLElement): void {
  const names = lineNames.get(box) || [];
  for (let n = names.length; n > 0; n--) {
    box.textContent = names.slice(0, n).join(', ') + (n < names.length ? ', …' : '');
    if (box.scrollWidth <= box.clientWidth) break;
  }
}

// Shows one of the two tabs of the room's options: Settings, or Mods.
function showRoomTab(mods: boolean): void {
  $('tabsettings').setAttribute('aria-selected', String(!mods));
  $('tabmods').setAttribute('aria-selected', String(mods));
  $('panelsettings').classList.toggle('hidden', mods);
  $('panelmods').classList.toggle('hidden', !mods);
}

// A click on a map in the room. The host picks the map. Anyone else suggests it, or takes their mark off the map that
// they had marked.
function pickRoomMap(name: string): unknown {
  const room = view && view.room;
  if (!room) return;
  if (room.host) return name !== room.map && act('room', { map: name });
  const mine = room.members.find((m) => m.id === me.id);
  return act('mark', { map: mine && mine.mark === name ? '' : name });
}

// The room and its game, as the page last saw the player in them (rememberRoomTitle).
let roomTitleSeen = '';

/**
 * Remembers the game of the player's room as the game they last picked (maps.ts: lastTitle), as they come into the
 * room, whether they made it or joined it, and as its game changes: being in a room of a game counts as picking it.
 * A game that they pick on the leaderboards while in the room stays picked until then.
 */
function rememberRoomTitle(room: Room): void {
  const seen = room.code + ' ' + room.of;
  if (seen === roomTitleSeen) return;
  roomTitleSeen = seen;
  if (room.of) lastTitle.set(room.of);
}

// The room's game, over its maps, on a server that hosts two (View.titles). The host picks it while the room has no
// game, and the room goes to a map of it (server/rooms.go: setRoom), which the grid then shows the maps of. The
// browser remembers the room's game, as it does whenever the player is in a room (rememberRoomTitle). The others see
// the game over the map's name. The options are made again only when the games change, so that an option keeps the
// focus as it is picked.
function showRoomTitle(v: View, room: Room, open: boolean): void {
  const box = $('titlepick');
  box.classList.toggle('hidden', v.titles.length < 2 || !room.host);
  const key = JSON.stringify(v.titles);
  if (box.dataset.key !== key) {
    box.dataset.key = key;
    box.textContent = '';
    const options = { name: "The room's game", options: v.titles.map((t) => ({ value: t.id, label: t.name })) };
    box.append(optionButtons(options, (title) => ({ title })));
  }
  for (const b of box.querySelectorAll('button')) b.disabled = !open;
  optionShow(box.firstElementChild!, room.of);
}

// The room's mode (its versus: titles.ts), in the Settings tab, where the room's game has maps of more than one: how
// its players play against each other. The host picks it while the room has no game, and the room goes to a map of
// it (server/rooms.go: setRoom), which the grid then shows the maps of. The others see it, as they see the size, and
// the row says what the mode is (settingRow).
function showRoomVersus(v: View, room: Room, open: boolean): void {
  const versuses = versusesOf(v.maps, room.of);
  const options = { name: 'Mode', options: versuses.map((x) => ({ value: x.id, label: x.name })) };
  const box = settingRow($('versussetting'), $('versustext'), room.host, options, (versus) => ({ versus }));
  for (const b of box.querySelectorAll('button')) b.disabled = !open;
  const now = versuses.find((x) => x.id === versusOf(mapInfo(room.map)));
  optionShow(box, now ? now.id : '');
  $('versusdesc').textContent = now ? now.desc : '';
  badgeSay($('versustext'), now ? now.name : '');
}

/**
 * Draws the room's grid of maps: those of the room's game and versus. Each has the pictures of the players who
 * suggest it on it. While the room's game runs, none can be picked.
 */
export function showRoomMaps(v: View, room: Room): void {
  const grid = $('mapgrid');
  const maps = mapsOf(v.maps, room.of, versusOf(mapInfo(room.map)));
  showMaps(grid, maps, room.map, pickRoomMap, !onWindows(), room.state !== 'open');
  maps.forEach((m, i) => {
    const b = grid.children[i];
    b.querySelector('.marks')?.remove();
    const who = room.members.filter((p) => p.mark === m.name);
    if (!who.length) return;
    const marks = el('span', 'marks');
    marks.title = who.map((p) => p.name).join(', ');
    for (const p of who) marks.append(avatar(p.name, p.avatar, p.id === me.id, p.place));
    b.append(marks);
  });
}

/** Makes the room's controls work, apart from its button (main.ts). The page calls it once, when it starts. */
export function roomInit(): void {
  addEventListener('resize', () => fitNames($('modsnames'))); // the names of the mods that fit the line now
  $('visibility').onclick = () => flipSwitch($('visibility'), (on) => ({ private: on }));
  $('tabsettings').onclick = () => showRoomTab(false);
  $('tabmods').onclick = () => showRoomTab(true);
  // The line above the button opens the Mods tab, which may be below the window's foot.
  $('modsline').onclick = () => {
    showRoomTab(true);
    $('tabmods').focus();
    $('panelmods').scrollIntoView({ block: 'nearest', behavior: 'smooth' });
  };
  // A player who joins by hand, with that checkbox checked, needs no launcher. The button is Ready then, and the lobby
  // sends their launcher no game (tellHand). The browser remembers the checkbox for the player's next rooms and
  // visits.
  $<HTMLInputElement>('pchand').checked = store.get('zombies-by-hand') === '1';
  $('pchand').onchange = () => {
    store.set('zombies-by-hand', handOn() ? '1' : '');
    tellHand();
    if (view && view.room) {
      showPC(view, view.room);
      showRoomButton(view, view.room);
    }
  };
  $('endgame').onclick = () => act('end');
  $('copypc').onclick = () => copy($('copypc'), $('pcconsole').textContent || '');
  for (const b of $('pcinstall').querySelectorAll<HTMLButtonElement>('button.what')) {
    b.onclick = () => {
      aboutShown = !aboutShown;
      if (view && view.room) showPC(view, view.room);
      else if (view) showInstall(view, true, installOf); // in Play local
    };
  }
  $('leave').onclick = async () => (await mayLeaveGame()) && act('leave');
  $('copylink').onclick = () => view && view.room && copy($('copylink'), location.origin + '/?room=' + view.room.code);
}
