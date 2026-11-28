// web/src/solo.ts - the page to play locally, which the lobby calls Play local: a game on no game of the server's.
// Black Ops plays a map alone in this page. Black Ops II and Black Ops III start on the player's PC, at their own
// menus, where the player picks a map and how to play it: so the view's name says neither "offline" nor "solo".
//
// It is one of the lobby's views, and shows what a room does: the map, and one button. For Black Ops, the button gets
// the map until the store has it (files.ts: mapButton), and is Play from then on (game.ts: playAlone). The player's
// pick of the map is kept in the browser's storage (maps.ts: soloMapName). The view has the button that opens Black
// Ops' options menu too, under Play (main.ts: settingsInit).
//
// For Black Ops II and III, the view is a room's without the room. Its button opens the launcher
// (windows.ts: openLauncher), and says what the launcher does, as a room's button does (launcherSays). It is Connect
// while the page knows nothing of the game on this PC: the launcher then gets what the PC lacks, and starts nothing.
// From then on it is Launch game, where a room's is Ready: the launcher starts the game at its menu (launcher/main.go:
// alone). What the launcher said of the PC is kept in the browser, so the button is Launch game on the player's next
// visit too (onPC). A page that can't reach its server opens the launcher with no key, and the launcher then plays
// from what the PC has. Where the server hosts more than one game, the view has a switch between them. It shows the
// game that the player last picked, there or for a room, which the browser keeps (maps.ts: lastTitle). Under Black
// Ops II the view also says how to get to Plutonium's online servers, with Plutonium's own launcher (showSoloOwn).
// Where the server sends a game a map at a time, as it does Black Ops III, the view has that game's maps: those that
// the PC lacks, each of which a click downloads (showSoloMaps). No map is picked there: the game starts at its menu.
//
// The view shows with ?local=1 in the URL, which a reload keeps. The lobby's "Go", under Play local, opens it, as a
// step that the browser's Back goes back from, and "Back to the lobby" closes it.
import type { View } from './api';
import { type MainButton, setButton } from './controls';
import { $, el, isOff } from './dom';
import { canRun, started } from './engine';
import { knowMap, type MapButton, mapButton, showNote } from './files';
import { endNote, forgetLastEnd, lastEnd } from './game';
import { offline, page } from './lobby';
import { showDesc, showMaps } from './mapgrid';
import { hasTitle, lastTitle, mapInfo, mapPicture, soloMapName, t5Maps } from './maps';
import { showNotice } from './notice';
import { params } from './params';
import { inWords, placeInstall, showInstall, site, type Words } from './room';
import { served } from './server';
import { bo1Settings } from './settings';
import { menuSound } from './sounds';
import { store } from './storage';
import { bytesText, CLIENTS, mapsOf, ownPC, titleName } from './titles';
import { launcherSays, launcherSeen, launcherStarts, onMac, type OnPC, onPC, openLauncher } from './windows';

/** How the view's button gets the map, while the store lacks it: 'download' or 'import' (files.ts: mapButton). */
export let soloGets = '';

/**
 * The game that the view's button opens the launcher for, on the player's own PC, by its title's id (main.ts), or ''
 * while the view shows Black Ops, which the page plays. soloConnects: the button has the launcher get the PC ready,
 * and launches nothing, as the page knows nothing yet of that game on this PC (showSoloPC).
 */
export let soloLaunches = '';
export let soloConnects = false;

/**
 * Whether the page offers a game of the players' own PCs to play locally: the server hosts it, and the launcher
 * starts it on this computer. A page that can't reach its server goes by what it last saw, which the browser's
 * storage keeps. It offers the game whether or not a launcher has run from this browser: the launcher is the PC's,
 * which another browser may have opened. The page keeps nothing before the server has said whether it offers the
 * launcher (served), which it says just after the page loads.
 */
function offersPC(v: View, of: string): boolean {
  if (offline) return store.get('zombies-solo-' + of) === '1';
  const offers = hasTitle(v.maps, of) && launcherStarts(of);
  if (Object.keys(served).length) store.set('zombies-solo-' + of, offers ? '1' : '');
  return offers;
}

/** The names of the games, for a page that can't reach its server (View.titles is empty then). */
const TITLES: Record<string, string> = { t5: 'Black Ops', t6: 'Black Ops II', t7: 'Black Ops III' };

/** Draws the page to play locally from a view of the lobby. */
export function showSolo(v: View): void {
  // the games that the page offers: Black Ops, which it plays itself, and those of the player's own PC
  const offered = Object.keys(TITLES).filter((id) => (ownPC(id) ? offersPC(v, id) : hasTitle(v.maps, id)));
  $('soloentry').classList.toggle('hidden', !offered.length);
  // the game: the one the player last picked, here or for a room, where the page offers more than one
  const of = offered.includes(lastTitle.get()) ? lastTitle.get() : offered[0] || 't5';
  showSoloTitle(v, offered, of);
  const pc = ownPC(of);
  $('soloimg').classList.toggle('hidden', pc);
  $('solokicker').textContent = pc ? 'Game' : 'Map';
  $('soloown').classList.toggle('hidden', of !== 't6');
  $('solo').dataset.of = of; // a game of the player's own PC has a card as tall as its words (page.css)
  soloLaunches = pc ? of : '';
  soloConnects = false;
  if (pc) return showSoloPC(v, of);
  // Black Ops' maps, of which the player picks the one to play
  const grid = $('solomaps');
  grid.classList.remove('hidden');
  grid.setAttribute('role', 'radiogroup');
  grid.setAttribute('aria-label', 'The map to play locally');
  showInstall(v, false);
  const picked = soloMapName(v);
  const map = mapInfo(picked);
  const img = $<HTMLImageElement>('soloimg');
  if (picked) mapPicture(img, picked);
  else img.removeAttribute('src');
  $('solotitle').textContent = picked ? map.title : 'No map';
  delete $('solodesc').dataset.of; // the words of a game of the player's own PC go (showSoloPC)
  showDesc($('solodesc'), map.desc || '');
  showMaps($('solomaps'), t5Maps(v.maps), picked, (name) => {
    if (name === picked) return;
    store.set('zombies-solo-map', name);
    page.redraw();
  }, true);
  knowMap(picked);
  const m: MapButton = picked ? mapButton(picked, 'Play') : { label: 'Play', disabled: true };
  if (!canRun || started) m.disabled = true;
  $<HTMLButtonElement>('soloopen').disabled = !canRun;
  soloGets = m.gets || '';
  setButton($('playalone'), m, true);
  showNote($('solonote'), picked, m, canRun && !started);
  // A browser without a private file system has no options menu (settings.ts: available).
  $('settings').classList.toggle('hidden', !bo1Settings.available);
  // How the player's last game alone ended is said once, over the page (notice.ts). On the status line it would stay
  // until the lobby took an action of the player's.
  if (lastEnd && lastEnd.solo) {
    showNotice(lastEnd.why === 'over' ? lastEnd.text || 'Game over.' : endNote(lastEnd));
    forgetLastEnd();
  }
}

/**
 * Draws the switch between the games, where the page offers more than one. The buttons are made again only when the
 * games change, so that the picked one keeps the focus.
 */
function showSoloTitle(v: View, offered: string[], of: string): void {
  const box = $('solotitles');
  box.classList.toggle('hidden', offered.length < 2);
  if (offered.length < 2) return;
  const key = offered.join(' ');
  if (box.dataset.key !== key) {
    box.dataset.key = key;
    const seg = el('div', 'seg');
    seg.setAttribute('role', 'radiogroup');
    seg.setAttribute('aria-label', 'The game to play locally');
    for (const id of offered) {
      const b = el('button', '', titleName(v.titles, id) || TITLES[id]);
      b.type = 'button';
      b.setAttribute('role', 'radio');
      b.dataset.value = id;
      b.onclick = () => {
        if (b.getAttribute('aria-checked') === 'true') return;
        lastTitle.set(id);
        page.redraw();
      };
      seg.append(b);
    }
    box.replaceChildren(seg);
  }
  for (const b of box.querySelectorAll<HTMLElement>('button')) b.setAttribute('aria-checked', String(b.dataset.value === of));
}

/**
 * Draws a game of the player's own PC in the page to play locally, Black Ops II or III: what it is, and its button,
 * which opens the launcher.
 *
 * The button goes by what the page knows of the game on this PC (windows.ts: onPC). While it knows nothing, the
 * button is Connect, as a room's is: the launcher gets the PC ready for the game, and asks where the game is, or
 * whether to download it, where it finds none. Once the launcher has had the game ready, in this visit or an earlier
 * one, the button is Launch game: the launcher starts the game at its menu. Meanwhile the button says what the
 * launcher does, from what the server says of it, as a room's does (windows.ts: launcherSays), and Try again after a
 * try that failed, with what it failed at under it. A page that can't reach its server hears nothing of the
 * launcher: its button is Launch game, and says that the game starts for a moment after the click.
 *
 * What installs the launcher shows here as in a room (room.ts: showInstall), and under Black Ops II, how to get to
 * Plutonium's online servers instead (showSoloOwn). A game that the server sends a map at a time has its maps under
 * it (showSoloMaps).
 */
function showSoloPC(v: View, of: string): void {
  const title = v.titles.find((t) => t.id === of);
  $('solotitle').textContent = (title && title.name) || TITLES[of];
  // The words are made once for the game, and not again at each view: the link in them keeps the focus.
  const desc = $('solodesc');
  if (desc.dataset.of !== of) {
    desc.dataset.of = of;
    desc.replaceChildren(`Launches the game on this ${onMac ? 'Mac' : 'PC'} through `, site(CLIENTS[of]),
      of === 't6' ? ', in LAN mode. Offline, and no account needed.'
        // BOIII starts no game on a PC without Steam (launcher/boiii.go: errNoSteam)
        : ". Steam must be installed, but doesn't need to be running. No account needed.");
    desc.scrollTop = 0;
  }
  desc.classList.remove('hidden');
  if (v.you.launcher) launcherSeen();
  // The card says no size of the game's files: the launcher says it, where it asks before it downloads them
  // (launcher/files.go: askFiles).
  const pc = onPC(v, of);
  const says = launcherSays(v, true);
  soloConnects = !pc && !offline;
  const does: MainButton = { label: v.you.launcherFailed ? 'Try again' : soloConnects ? 'Connect' : 'Launch game' };
  setButton($('playalone'), says || does, true);
  soloGets = '';
  showSoloMaps(v, of, pc, !says);
  // The launcher has no window, so the page says what it failed at.
  const note = $('solonote');
  note.textContent = v.you.launcherFailed || '';
  note.classList.toggle('hidden', !v.you.launcherFailed);
  $('settings').classList.add('hidden'); // Black Ops' options
  placeInstall($('soloinstall'));
  showInstall(v, !offline, of);
  if (of === 't6') showSoloOwn();
}

/**
 * Draws the maps of a game of the player's own PC in the page to play locally, where the server sends that game's
 * files a map at a time, as it does Black Ops III's: each map then has a download of its own. A game that it sends
 * whole, or not at all, has no maps here.
 *
 * No map is picked: the game starts at its own menu, where the player picks one. A map is a tile, which says
 * nothing more while the PC has the map, or while the page does not know what the PC has (pc). A map that the PC
 * lacks says so under its name:
 *
 *   - its size, where the launcher downloads it: into the game files that the launcher downloaded, and none other.
 *     The tile is then a button, which has the launcher download the map and launch nothing (openLauncher). It is off
 *     while the launcher is at something else (free): the main button says what.
 *   - "Not installed" otherwise: in the player's own copy of the game, which the launcher only reads, and for a map
 *     of no pack of its own, which comes with what every map needs.
 *
 * A map that the server lacks is greyed out. The tiles are made again only when what they say changes, so that one
 * keeps the focus.
 */
function showSoloMaps(v: View, of: string, pc: OnPC | null, free: boolean): void {
  const title = v.titles.find((t) => t.id === of);
  const maps = mapsOf(v.maps, of);
  const grid = $('solomaps');
  const shows = !!title && !!title.downloads && maps.some((m) => m.bytes);
  grid.classList.toggle('hidden', !shows);
  if (!shows) return;
  const name = (title && title.name) || TITLES[of];
  grid.classList.remove('locked');
  grid.setAttribute('role', 'group');
  grid.setAttribute('aria-label', `The maps of ${name} on this ${onMac ? 'Mac' : 'PC'}`);
  // what each map's tile is: off (the server lacks the map), get (a click downloads it), lacks, or '' (nothing to say)
  const tiles = maps.map((m) => {
    if (!m.have) return { kind: 'off', says: '' };
    if (!pc || !pc.maps || pc.maps.includes(m.name)) return { kind: '', says: '' };
    return !pc.own && m.bytes ? { kind: 'get', says: bytesText(m.bytes) } : { kind: 'lacks', says: 'Not installed' };
  });
  const key = 'pc ' + JSON.stringify(maps.map((m, i) => [m.name, tiles[i].kind, tiles[i].says]));
  if (grid.dataset.key !== key) {
    grid.dataset.key = key;
    grid.textContent = '';
    maps.forEach((m, i) => {
      const { kind, says } = tiles[i];
      const tile = kind === 'get' ? el('button', 'get') : el('div', ('maptile ' + kind).trim());
      const pic = el('span', 'pic');
      const img = mapPicture(el('img'), m.name);
      img.alt = ''; // the tile's text says the map
      pic.append(img);
      tile.append(pic, m.title);
      if (says) tile.append(el('span', 'size', says));
      tile.title = kind === 'off' ? m.title + ': not available'
        : kind === 'get' ? `Download ${m.title} · ${says}` : m.title;
      if (tile instanceof HTMLButtonElement) {
        tile.type = 'button';
        // A tile that is off takes a click too, and does nothing, as the view's main button (controls.ts: setButton).
        tile.onclick = () => !isOff(tile) && openLauncher(false, of, m.name);
      }
      grid.append(tile);
    });
  }
  for (const b of grid.querySelectorAll('button')) {
    if (free) b.removeAttribute('aria-disabled');
    else b.setAttribute('aria-disabled', 'true');
  }
}

// How to get to Plutonium's online servers, which the view says under Black Ops II (showSoloOwn). The launcher
// starts the game in LAN mode, which needs no Plutonium account and reaches none of Plutonium's own servers
// (launcher/launch.go): the game's words say so (showSoloT6), so that the two ways are not taken for one. Plutonium's
// own launcher plays on those servers, with an account, on the same copy of Plutonium. A PC's steps are those of
// Plutonium's install guide. A Mac's are plutonium-mac-aio's, which runs Plutonium there: its launch.sh, in the
// folder that the line for Zombies Launcher put it in, with Plutonium's updater (launcher/mac.sh: get_updater). The
// folder that Plutonium's launcher asks for is the one that the launcher's menu names (launcher/menu.go: line), which
// a hint on the step's words says. A launcher that has started the game has noted the folder there itself, for a
// Plutonium that had none (launcher/plutonium.go), and the step is then done. The steps are folded until the player
// opens them: most only launch the game.
interface Step { words: Words; line?: string }
const ACCOUNT: [string, string] = ['free Plutonium account', 'https://forum.plutonium.pw/register'];
const SIGN_IN: Step = { words: ['Sign in with a ', ACCOUNT, '.'] };
const OWN_PC: Step[] = [
  { words: ['Download and run ', ["Plutonium's own launcher", 'https://plutonium.pw/docs/install/'], '.'] },
  SIGN_IN,
  { words: ['At Black Ops II, press Setup, choose your ',
    { text: 'game folder', hint: "Zombies Launcher's tray menu shows the folder it uses." }, ', then press Play.'] },
];
const OWN_MAC: Step[] = [
  { words: ["With Zombies Launcher installed, start Plutonium's own launcher from Terminal:"],
    line: '~/plutonium-mac-aio/launch.sh' },
  SIGN_IN,
  { words: ['At Black Ops II, press Setup, enter your ',
    { text: 'game folder', hint: "Zombies Launcher's menu in the menu bar shows the folder it uses." },
    ', then press Play.'] },
];

let ownShown = false; // whether the player opened the steps

/**
 * Draws how to get to Plutonium's online servers, under Black Ops II's words (#soloown): a line that folds the steps
 * out and in, and the steps, which are a Mac's on a Mac, with the line for Terminal. They are made once, and not
 * again at each view: a link in them keeps the focus.
 */
function showSoloOwn(): void {
  const steps = $('soloownsteps');
  const fold = $('soloownopen');
  fold.setAttribute('aria-expanded', String(ownShown));
  steps.classList.toggle('hidden', !ownShown);
  if (steps.childElementCount) return;
  fold.onclick = () => {
    ownShown = !ownShown;
    showSoloOwn();
  };
  for (const step of onMac ? OWN_MAC : OWN_PC) {
    const li = el('li');
    li.append(...inWords(step.words));
    if (step.line) {
      const box = el('div', 'codeline');
      box.append(el('span', '', step.line));
      li.append(box);
    }
    steps.append(li);
  }
}

/** Whether the page to play locally shows. */
export let soloOpen = params.get('local') === '1';
let soloPushed = false; // opening it added a step to the browser's history, which closing it goes back from

/** Opens or closes the page to play locally, as the player asks, with the sound of a large menu. */
export function openSolo(open: boolean): void {
  if (open === soloOpen) return;
  if (!open && soloPushed) return history.back(); // the page's popstate listener then closes it (soloFollowUrl)
  const url = new URL(location.href);
  if (open) {
    url.searchParams.set('local', '1');
    history.pushState(null, '', url);
  } else {
    url.searchParams.delete('local');
    history.replaceState(null, '', url);
  }
  soloPushed = open;
  soloOpen = open;
  menuSound(open, true);
  page.redraw();
}

/** Opens or closes the page to play locally as the URL says, after a step in the browser's history (show.ts). */
export function soloFollowUrl(url: URL): void {
  soloOpen = soloPushed = url.searchParams.get('local') === '1';
}

/** Closes the page to play locally for a room that the player is in. The browser's history keeps its steps. */
export function closeSolo(): void {
  soloOpen = soloPushed = false;
}
