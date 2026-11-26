// web/src/solo.ts - the page to play locally, which the lobby calls Play local: a game on no game of the server's.
// Black Ops plays a map alone in this page. Black Ops II and Black Ops III start on the player's PC, at their own
// menus, where the player picks a map and how to play it: so the view's name says neither "offline" nor "solo".
//
// It is one of the lobby's views, and shows what a room does: the map, and one button. For Black Ops, the button gets
// the map until the store has it (files.ts: mapButton), and is Play from then on (game.ts: playAlone). The player's
// pick of the map is kept in the browser's storage (maps.ts: soloMapName). The view has the button that opens Black
// Ops' options menu too, under Play (main.ts: settingsInit).
//
// For Black Ops II and III, the button opens the launcher with a link to play alone (windows.ts: openLauncher), and
// then says what the launcher does, as a room's button does: the launcher gets what the PC lacks, then starts the game
// (launcher/main.go: alone). A page that can't reach its server opens the launcher with no key, and the launcher then
// plays from what the PC has. Where the server hosts more than one game, the view has a switch between them. It shows
// the game that the player last picked, there or for a room, which the browser keeps (maps.ts: lastTitle). Under
// Black Ops II the view also says how to get to Plutonium's online servers, with Plutonium's own launcher
// (showSoloOwn). Under Black Ops III it has that game's maps, for the player to pick the one that the launcher is to
// have ready: the server sends that game a map at a time (showSoloMaps).
//
// The view shows with ?local=1 in the URL, which a reload keeps. The lobby's "Go", under Play local, opens it, as a
// step that the browser's Back goes back from, and "Back to the lobby" closes it.
import type { MapInfo, View } from './api';
import { type MainButton, setButton } from './controls';
import { $, el } from './dom';
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
import { launcherOpening, launcherSeen, launcherStarts, launcherStep, onMac } from './windows';

/** How the view's button gets the map, while the store lacks it: 'download' or 'import' (files.ts: mapButton). */
export let soloGets = '';

/**
 * The game that the view's button opens the launcher for, to play it alone on the player's own PC, by its title's id
 * (main.ts), or '' while the view shows Black Ops, which the page plays. soloLaunchMap is the map of that game that
 * the player picked in the view, where it has them pick one, or ''.
 */
export let soloLaunches = '';
export let soloLaunchMap = '';

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
  $('solomaps').classList.toggle('hidden', of === 't6');
  $('solokicker').textContent = pc ? 'Game' : 'Map';
  $('soloown').classList.toggle('hidden', of !== 't6');
  $('solofiles').classList.toggle('hidden', !pc);
  $('solo').dataset.of = of; // a game of the player's own PC has a card as tall as its words (page.css)
  soloLaunches = pc ? of : '';
  soloLaunchMap = '';
  if (pc) return showSoloPC(v, of);
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
 * which opens the launcher. The button
 * then says what the launcher does, from what the server says of it, as a room's does (room.ts: showRoomButton). The
 * launcher says that the game launches for a while after it launched it, and then nothing: the button is Launch game
 * again. A
 * page that can't reach its server hears nothing of the launcher: its button says that the game starts for a moment
 * after the click. What installs the launcher shows here as in a room (room.ts: showInstall), and under Black Ops II,
 * how to get to Plutonium's online servers instead (showSoloOwn).
 *
 * Black Ops III has its maps under it (showSoloMaps): the server sends that game a map at a time, so the player picks
 * the map that the launcher is to have ready. The button is Download where the launcher's game files lack it.
 */
function showSoloPC(v: View, of: string): void {
  const title = v.titles.find((t) => t.id === of);
  $('solotitle').textContent = (title && title.name) || TITLES[of];
  // The words are made once for the game, and not again at each view: the link in them keeps the focus.
  const desc = $('solodesc');
  if (desc.dataset.of !== of) {
    desc.dataset.of = of;
    desc.replaceChildren(`Launches the game on this ${onMac ? 'Mac' : 'PC'} through `, site(CLIENTS[of]),
      of === 't6' ? ', in LAN mode. Offline, and no account needed.' : ', at its menu. No account needed.');
    desc.scrollTop = 0;
  }
  desc.classList.remove('hidden');
  // What a PC without the game gets from the server, once the server has listed the game's files.
  const sends = !!title && !!title.downloads;
  const maps = mapsOf(v.maps, of);
  const byMap = maps.some((m) => m.bytes);
  $('solofiles').textContent = !sends || !title.bytes ? ''
    : `A ${onMac ? 'Mac' : 'PC'} without the game gets it from this server: ${bytesText(title.bytes)}`
      + (byMap ? ' for every map, and then the maps you pick.' : '.');
  if (v.you.launcher) launcherSeen();
  let lacks = 0; // the size of the picked map, where the launcher's game files are known to lack it
  if (of !== 't6') {
    const picked = showSoloMaps(v, of, maps, sends);
    soloLaunchMap = picked;
    const there = v.you.launcherOf === of && !!v.you.launcherFolder;
    const map = maps.find((m) => m.name === picked);
    if (sends && there && map && map.bytes && !(v.you.launcherMaps || []).includes(picked)) lacks = map.bytes;
  }
  const doing = launcherStep(v.you.launcherDoing || '');
  let m: MainButton = { label: lacks ? 'Download · ' + bytesText(lacks) : 'Launch game' };
  if (v.you.launcherFailed) m = { label: 'Try again' };
  else if (!v.you.launcher) {
    if (launcherOpening) m = { label: offline ? 'Starting...' : 'Connecting...', disabled: true };
  } else if (v.you.launcherDownloading) {
    m = { label: `Downloading · ${Math.floor((v.you.launcherGot || 0) * 100)}%`, disabled: true };
  } else if (doing) m = { label: doing + '...', disabled: true };
  else if (!v.you.launcherReady) m = { label: 'Connecting...', disabled: true };
  setButton($('playalone'), m, true);
  soloGets = '';
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
 * Draws the maps of a game of the player's own PC in the page to play locally, and returns the one that is picked:
 * the player's last pick of that game's, which the browser's storage keeps, or the first that the server has. Once
 * the launcher has found the game's folder on the PC, each map says whether the folder has it: Installed, or what the
 * launcher would download for it, where the server sends the game's files (sends).
 */
function showSoloMaps(v: View, of: string, maps: MapInfo[], sends: boolean): string {
  const have = maps.filter((m) => m.have);
  const kept = store.get('zombies-solo-map-' + of);
  const picked = have.some((m) => m.name === kept) ? kept : have[0]?.name || '';
  const grid = $('solomaps');
  showMaps(grid, maps, picked, (name) => {
    if (name === picked) return;
    store.set('zombies-solo-map-' + of, name);
    page.redraw();
  }, false);
  const there = v.you.launcherOf === of && !!v.you.launcherFolder;
  maps.forEach((m, i) => {
    const b = grid.children[i];
    const says = !there || !m.have ? ''
      : (v.you.launcherMaps || []).includes(m.name) ? 'Installed' : sends && m.bytes ? bytesText(m.bytes) : 'Not installed';
    let size = b.querySelector('.size');
    if (!says) return size?.remove();
    if (!size) {
      size = el('span', 'size');
      b.append(size);
    }
    size.textContent = says;
    size.classList.toggle('there', says === 'Installed');
  });
  return picked;
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
