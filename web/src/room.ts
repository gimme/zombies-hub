// web/src/room.ts - the room that the player is in.
//
// The room shows its game, its map, its options, its members, and one button. The game is one of those that the
// server hosts (server/title.go), and the map is one of that game's. The options are whether it is private, how many
// players it takes, and its mods. While the room has no game running, the host picks the game, on a server of two,
// then a map of it, and sets the options, and the others see them. The others may suggest a map of the room's game.
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
// The game of a room on a Black Ops II map is played on the player's own PC (windows.ts). The room's button then
// opens the launcher, and a box has the rest: how to install the launcher, and how to join by hand (showT6).
import type { Member, Mod, Room, View } from './api';
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
import { showAbout, showMaps } from './mapgrid';
import { mapInfo, mapPicture } from './maps';
import { modNames, modOn, modValue } from './mods';
import { mayLeaveGame } from './question';
import { bo1Settings } from './settings';
import { store } from './storage';
import { mapsOf, titleName } from './titles';
import {
  launcherDownloads, launcherHere, launcherOn, launcherSeen, launcherSetup, onWindows, t6Console,
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
  mapPicture($<HTMLImageElement>('mapimg'), room.map);
  // Over the map's name, its game, on a server of two. That is where the others see the room's game.
  $('mapkicker').textContent = (v.titles.length > 1 && titleName(v.titles, room.of)) || 'Map';
  $('maptitle').textContent = map.title;
  $('mapdesc').textContent = map.desc || '';
  $('mapdesc').classList.toggle('hidden', !map.desc);
  showAbout($('mapabout'), mapsOf(v.maps, room.of));
  showRoomTitle(v, room, open);
  showRoomMaps(v, room);
  // Whether the room is private: a switch for the host, and a badge for the others.
  $('visibility').classList.toggle('hidden', !room.host);
  switchShow($('visibility'), room.private);
  $('vistext').classList.toggle('hidden', room.host);
  showOnOff($('vistext'), room.private);
  showRoomSize(v, room);
  // The mods are the engine's. A Black Ops II room has none, and no tab of them.
  const t6 = room.of === 't6';
  $('tabmods').classList.toggle('hidden', t6);
  if (t6) showRoomTab(false);
  showRoomMods(t6 ? [] : v.mods, room, open);
  showT6(v, room);

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
  // A member has a team in a room with Grief on (server/mods.go: teamOf).
  const what = [m.team && 'Team ' + m.team, m.host && 'Host', m.windows && 'On Windows', m.away && 'Away']
    .filter(Boolean).join(' · ');
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
  // A Black Ops II player's launcher may download the game files while the room's game already waits for them.
  const downloadsT6 = room.of === 't6' && !!m.downloading;
  if (mine && room.left) { // their character is gone from the game
    badge.classList.add('off');
    badgeSay(badge, 'Out of game');
  } else if (m.inGame || (ready && !downloadsT6)) {
    badge.classList.add('on');
    badgeSay(badge, m.inGame ? 'In game' : 'Ready', ready);
  } else if (open || downloadsT6) {
    badge.classList.add('off');
    badgeSay(badge, m.downloading ? 'Downloading' : m.missing ? 'Missing map' : 'Not ready');
  }
  li.append(avatar(m.name, m.avatar, mine, m.place), who, badge);
  // How far their download is, as a bar along the row.
  if ((open || room.of === 't6') && m.got && m.got > 0) {
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
  // The game of a Black Ops II room is played by the player's own Plutonium. The page needs no map for it, and does
  // not run the engine.
  const t6 = room.of === 't6';
  let m: RoomButton;
  let lacks = ''; // what the line under the button says of a Black Ops II player's copy that lacks the room's map
  if (!open) m = gameButton(v, room);
  else if (!canRun && !v.you.windows && !t6) m = { label: "Can't play in this browser", disabled: true };
  else {
    // No one can be ready while all of the server's games are running (server/rooms.go: full), or while the room has
    // fewer players than a game needs.
    const short = v.minPlayers - room.members.length;
    const label = v.you.ready ? 'Unready' : v.full ? 'Server full' : short > 0 ? `Need ${short} more` : 'Ready';
    if (!windows && !t6) knowMap(room.map);
    m = windows || t6 ? { label, ready: true } : mapButton(room.map, label);
    if (m.ready && !v.you.ready && (v.full || short > 0)) m.disabled = true;
    m.does = m.gets ? 'map' : 'ready';
    m.primary = !!m.gets || (!!m.ready && !v.you.ready);
    // In a Black Ops II room, once the server has said where the launcher is, the button opens the launcher first.
    // The launcher then gets what the PC lacks for the game, and the button says how far that is. After that the
    // button is Ready. Not for a player who joins by hand, with that checkbox checked.
    //
    // A copy of the game without the pack of the room's map lacks the map (server/t6.go: lacks). The player is then
    // not ready until the host picks another map. Where the server sends game files, the button is Download: it
    // opens the launcher, which asks whether to download the server's game files and use them instead.
    if (t6 && launcherOn() && !v.you.ready && !handOn()) {
      const mine = room.members.find((x) => x.id === me.id);
      if (v.you.launcherFailed) m = { label: 'Try again', does: 'launch', primary: true };
      else if (!v.you.launcher) m = { label: 'Connect', does: 'launch', primary: true };
      else if (!v.you.launcherReady) {
        m = { label: mine && mine.downloading ? `Downloading · ${Math.floor((mine.got || 0) * 100)}%`
          : (v.you.launcherDoing || 'Starting') + '...', disabled: true };
      } else if (mine && mine.missing) {
        lacks = "Your Black Ops II doesn't have this map" + (launcherDownloads()
          ? '. The launcher can download the game files from the server instead.'
          : ': it comes with a map pack.');
        m = launcherDownloads() ? { label: 'Download', does: 'launch', primary: true }
          : { label: 'Missing map', disabled: true };
      }
    }
  }
  readyGets = m.gets || '';
  $('ready').dataset.does = m.disabled ? '' : m.does || '';
  setButton($('ready'), m, m.primary);
  showNote($('roomnote'), room.map, m, open && !windows && canRun && !t6);
  // The launcher has no window, so the room says what it failed at, and what the player's copy of the game lacks.
  const says = t6 ? v.you.launcherFailed || lacks : '';
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
  if (room.of !== 't6') {
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
  // A Black Ops II game is joined by the player's own PC. The page knows of it what the server does: whether their
  // game is in it. Until it is, the button opens the launcher. A launcher that is already at it does nothing more. A
  // player who joins by hand, with that checkbox checked, gets no launcher, as at the Ready button (showRoomButton).
  if (mine && mine.inGame) return said('In game');
  if (!launcherOn() || handOn()) return said('Join by hand');
  // Their launcher has started their game, which is on its way in.
  if (v.you.launcher && v.you.launcherDoing) return said(v.you.launcherDoing + '...');
  return { label: 'Join the game', does: 'launch', primary: true };
}

/**
 * Draws the box of a room on a Black Ops II map. Nothing opens the launcher but the room's button. The box has the
 * line that installs the launcher, and the line that joins by hand. The line that installs the launcher is there in a
 * browser that has never seen a launcher of the player's run, and when the server refused the player's launcher as
 * too old. That launcher can't say so itself, because the server takes no report from it.
 */
export function showT6(v: View, room: Room): void {
  const box = $('t6box');
  box.classList.toggle('hidden', room.of !== 't6');
  if (room.of !== 't6') return;
  if (v.you.launcher) launcherSeen();
  // Where the server offers the launcher, joining by hand is a checkbox (handOn), which the browser remembers.
  // Checked, it shows the line that joins by hand, and the room's button opens no launcher (showRoomButton), nor does
  // the box say where to get one. Where the server offers no launcher, joining by hand is the only way: the line is
  // there, and no checkbox.
  const byHand = !launcherOn() || handOn();
  $('t6handrow').classList.toggle('hidden', !launcherOn());
  $('t6handline').classList.toggle('hidden', !byHand);
  $('t6console').textContent = t6Console(room);
  const old = !!v.you.launcherOld;
  $('t6get').classList.toggle('hidden', byHand || (launcherHere && !old));
  $('t6why').textContent = old ? 'Your Zombies Launcher is too old for this server.' : 'First time?';
  $('t6setup').textContent = old ? 'Install the new one' : 'Install Zombies Launcher';
  $('t6then').textContent = old ? ', then connect again.' : '.';
  $<HTMLAnchorElement>('t6setup').href = launcherSetup();
}

/** Whether the player joins their Black Ops II room's game by hand, as that checkbox says (showT6). */
function handOn(): boolean {
  return $<HTMLInputElement>('t6hand').checked;
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
// host sets it. The host picks among the sizes that a room on this server may have, from the players that a game
// needs to the most that a room holds. A size that the room has too many players for can't be picked. The others see
// the size. Where the server's rooms have one size, there is nothing to pick, and no row. The options are made again
// only when they change, so that an option keeps the focus as it is picked.
function showRoomSize(v: View, room: Room): void {
  const sizes = [];
  for (let n = v.minPlayers; n <= v.size; n++) sizes.push(n);
  const row = $('sizesetting');
  row.classList.toggle('hidden', sizes.length < 2);
  const key = JSON.stringify(sizes);
  if (row.dataset.key !== key) {
    row.dataset.key = key;
    row.querySelector('.seg')?.remove();
    const options = { name: 'Room size', options: sizes.map((value) => ({ value })) };
    $('sizetext').before(optionButtons(options, (size) => ({ size })));
  }
  const box = row.querySelector('.seg')!;
  box.classList.toggle('hidden', !room.host);
  for (const b of box.querySelectorAll('button')) {
    b.disabled = Number(b.dataset.value) < room.members.length;
    b.title = b.disabled ? 'The room has more players' : '';
  }
  optionShow(box, room.size);
  $('sizetext').classList.toggle('hidden', room.host);
  badgeSay($('sizetext'), room.size + (room.size === 1 ? ' player' : ' players'));
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

// The room's game, over its maps, on a server that hosts two (View.titles). The host picks it while the room has no
// game, and the room goes to a map of it (server/rooms.go: setRoom), which the grid then shows the maps of. The
// others see the game over the map's name. The options are made again only when the games change, so that an option
// keeps the focus as it is picked.
function showRoomTitle(v: View, room: Room, open: boolean): void {
  const box = $('titlepick');
  box.classList.toggle('hidden', v.titles.length < 2 || !room.host || !open);
  const key = JSON.stringify(v.titles);
  if (box.dataset.key !== key) {
    box.dataset.key = key;
    box.textContent = '';
    const options = { name: "The room's game", options: v.titles.map((t) => ({ value: t.id, label: t.name })) };
    box.append(optionButtons(options, (title) => ({ title })));
  }
  optionShow(box.firstElementChild!, room.of);
}

/**
 * Draws the room's grid of maps: those of the room's game. Each has the pictures of the players who suggest it on
 * it.
 */
export function showRoomMaps(v: View, room: Room): void {
  const grid = $('mapgrid');
  const open = room.state === 'open';
  grid.classList.toggle('hidden', !open);
  if (!open) return;
  const maps = mapsOf(v.maps, room.of);
  showMaps(grid, maps, room.map, pickRoomMap, !onWindows());
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
  $<HTMLInputElement>('t6hand').checked = store.get('zombies-by-hand') === '1';
  $('t6hand').onchange = () => {
    store.set('zombies-by-hand', handOn() ? '1' : '');
    tellHand();
    if (view && view.room) {
      showT6(view, view.room);
      showRoomButton(view, view.room);
    }
  };
  $('endgame').onclick = () => act('end');
  $('copyt6').onclick = () => copy($('copyt6'), $('t6console').textContent || '');
  $('leave').onclick = async () => (await mayLeaveGame()) && act('leave');
  $('copylink').onclick = () => view && view.room && copy($('copylink'), location.origin + '/?room=' + view.room.code);
}
