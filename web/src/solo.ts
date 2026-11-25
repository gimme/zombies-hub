// web/src/solo.ts - the page to play locally, which the lobby calls Play local: a game on no game of the server's.
// Black Ops plays a map alone in this page. Black Ops II starts on the player's PC, at its own menu, where they pick a
// map and how to play it: so the view's name says neither "offline" nor "solo".
//
// It is one of the lobby's views, and shows what a room does: the map, and one button. For Black Ops, the button gets
// the map until the store has it (files.ts: mapButton), and is Play from then on (game.ts: playAlone). The player's
// pick of the map is kept in the browser's storage (maps.ts: soloMapName). The view has the button that opens Black
// Ops' options menu too, under Play (main.ts: settingsInit).
//
// For Black Ops II, the button opens the launcher with a link to play alone (windows.ts: openLauncher), and then says
// what the launcher does, as a room's button does: the launcher gets what the PC lacks, then starts the game
// (launcher/main.go: alone). A page that can't reach its server opens the launcher with no key, and the launcher then
// plays from what the PC has. Where the server hosts both games, the view has a switch between them. It shows the game
// that the player last picked, there or for a room, which the browser keeps (maps.ts: lastTitle). Under Black Ops II
// the view also says how to get to Plutonium's online servers, with Plutonium's own launcher (showSoloOwn).
//
// The view shows with ?local=1 in the URL, which a reload keeps. The lobby's "Go", under Play local, opens it, as a
// step that the browser's Back goes back from, and "Back to the lobby" closes it.
import type { View } from './api';
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
import { inWords, placeInstall, PLUTONIUM, showInstall, site, type Words } from './room';
import { served } from './server';
import { bo1Settings } from './settings';
import { menuSound } from './sounds';
import { store } from './storage';
import { titleName } from './titles';
import { launcherOn, launcherOpening, launcherSeen, launcherStep, onMac } from './windows';

/** How the view's button gets the map, while the store lacks it: 'download' or 'import' (files.ts: mapButton). */
export let soloGets = '';

/** Whether the view's button opens the launcher, to play Black Ops II alone (main.ts). */
export let soloLaunches = false;

/**
 * Whether the page offers Black Ops II to play locally: the server hosts it and offers the launcher. A page that can't
 * reach its server goes by what it last saw, which the browser's storage keeps. It offers the game whether or not a
 * launcher has run from this browser: the launcher is the PC's, which another browser may have opened. The page
 * keeps nothing before the server has said whether it offers the launcher (served), which it says just after the
 * page loads.
 */
function offersT6(v: View): boolean {
  if (offline) return store.get('zombies-solo-t6') === '1';
  const offers = hasTitle(v.maps, 't6') && launcherOn();
  if (Object.keys(served).length) store.set('zombies-solo-t6', offers ? '1' : '');
  return offers;
}

/** The names of the games, for a page that can't reach its server (View.titles is empty then). */
const TITLES: Record<string, string> = { t5: 'Black Ops', t6: 'Black Ops II' };

/** Draws the page to play locally from a view of the lobby. */
export function showSolo(v: View): void {
  const t5 = hasTitle(v.maps, 't5');
  const t6 = offersT6(v);
  $('soloentry').classList.toggle('hidden', !t5 && !t6);
  // the game: the one the player last picked, here or for a room, where the page offers both
  const of = t6 && (!t5 || lastTitle.get() === 't6') ? 't6' : 't5';
  showSoloTitle(v, t5 && t6, of);
  $('soloimg').classList.toggle('hidden', of === 't6');
  $('solomaps').classList.toggle('hidden', of === 't6');
  $('solokicker').textContent = of === 't6' ? 'Game' : 'Map';
  $('soloown').classList.toggle('hidden', of !== 't6');
  $('solo').dataset.of = of; // Black Ops II's card is as tall as its words (page.css)
  soloLaunches = of === 't6';
  if (of === 't6') return showSoloT6(v);
  showInstall(v, false);
  const picked = soloMapName(v);
  const map = mapInfo(picked);
  const img = $<HTMLImageElement>('soloimg');
  if (picked) mapPicture(img, picked);
  else img.removeAttribute('src');
  $('solotitle').textContent = picked ? map.title : 'No map';
  delete $('solodesc').dataset.of; // Black Ops II's words go (showSoloT6)
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
 * Draws the switch between the two games, where the page offers both. The buttons are made once, so that the picked
 * one keeps the focus.
 */
function showSoloTitle(v: View, both: boolean, of: string): void {
  const box = $('solotitles');
  box.classList.toggle('hidden', !both);
  if (!both) return;
  if (!box.firstElementChild) {
    const seg = el('div', 'seg');
    seg.setAttribute('role', 'radiogroup');
    seg.setAttribute('aria-label', 'The game to play locally');
    for (const id of ['t5', 't6']) {
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
    box.append(seg);
  }
  for (const b of box.querySelectorAll<HTMLElement>('button')) b.setAttribute('aria-checked', String(b.dataset.value === of));
}

/**
 * Draws Black Ops II in the page to play locally: what it is, and its button, which opens the launcher. The button
 * then says what the launcher does, from what the server says of it, as a room's does (room.ts: showRoomButton). The
 * launcher says that the game launches for a while after it launched it, and then nothing: the button is Launch game
 * again. A
 * page that can't reach its server hears nothing of the launcher: its button says that the game starts for a moment
 * after the click. What installs the launcher shows here as in a room (room.ts: showInstall), and under the game, how
 * to get to Plutonium's online servers instead (showSoloOwn).
 */
function showSoloT6(v: View): void {
  $('solotitle').textContent = titleName(v.titles, 't6') || TITLES.t6;
  // The words are made once, and not again at each view: the link in them keeps the focus.
  const desc = $('solodesc');
  if (desc.dataset.of !== 't6') {
    desc.dataset.of = 't6';
    desc.replaceChildren(`Launches the game on this ${onMac ? 'Mac' : 'PC'} through `, site(PLUTONIUM),
      ', in LAN mode. Offline, and no account needed.');
    desc.scrollTop = 0;
  }
  desc.classList.remove('hidden');
  if (v.you.launcher) launcherSeen();
  const doing = launcherStep(v.you.launcherDoing || '');
  let m: MainButton = { label: 'Launch game' };
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
  showInstall(v, !offline);
  showSoloOwn();
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
