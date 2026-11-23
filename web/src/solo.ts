// web/src/solo.ts - the page to play offline: a map alone in this page, on no game of the server's.
//
// It is one of the lobby's views, and shows what a room does: the map, and one button. The button gets the map until
// the store has it (files.ts: mapButton), and is Play from then on (game.ts: playAlone). The player's pick of the map
// is kept in the browser's storage (maps.ts: soloMapName). Playing offline is Black Ops alone, so the view has the
// button that opens Black Ops' options menu too, under Play (main.ts: settingsInit).
//
// The view shows with ?offline=1 in the URL, which a reload keeps. "Pick a map" opens it, as a step that the browser's
// Back goes back from, and "Back to the lobby" closes it.
import type { View } from './api';
import { setButton } from './controls';
import { $ } from './dom';
import { canRun, started } from './engine';
import { knowMap, type MapButton, mapButton, showNote } from './files';
import { endNote, forgetLastEnd, lastEnd } from './game';
import { page } from './lobby';
import { showDesc, showMaps } from './mapgrid';
import { hasTitle, mapInfo, mapPicture, soloMapName, t5Maps } from './maps';
import { showNotice } from './notice';
import { params } from './params';
import { bo1Settings } from './settings';
import { menuSound } from './sounds';
import { store } from './storage';

/** How the view's button gets the map, while the store lacks it: 'download' or 'import' (files.ts: mapButton). */
export let soloGets = '';

/** Draws the page to play offline from a view of the lobby. */
export function showSolo(v: View): void {
  // Playing offline is Black Ops in the browser. A server without Black Ops does not offer it.
  $('soloentry').classList.toggle('hidden', !hasTitle(v.maps, 't5'));
  const picked = soloMapName(v);
  const map = mapInfo(picked);
  const img = $<HTMLImageElement>('soloimg');
  if (picked) mapPicture(img, picked);
  else img.removeAttribute('src');
  $('solotitle').textContent = picked ? map.title : 'No map';
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

/** Whether the page to play offline shows. */
export let soloOpen = params.get('offline') === '1';
let soloPushed = false; // opening it added a step to the browser's history, which closing it goes back from

/** Opens or closes the page to play offline, as the player asks, with the sound of a large menu. */
export function openSolo(open: boolean): void {
  if (open === soloOpen) return;
  if (!open && soloPushed) return history.back(); // the page's popstate listener then closes it (soloFollowUrl)
  const url = new URL(location.href);
  if (open) {
    url.searchParams.set('offline', '1');
    history.pushState(null, '', url);
  } else {
    url.searchParams.delete('offline');
    history.replaceState(null, '', url);
  }
  soloPushed = open;
  soloOpen = open;
  menuSound(open, true);
  page.redraw();
}

/** Opens or closes the page to play offline as the URL says, after a step in the browser's history (show.ts). */
export function soloFollowUrl(url: URL): void {
  soloOpen = soloPushed = url.searchParams.get('offline') === '1';
}

/** Closes the page to play offline for a room that the player is in. The browser's history keeps its steps. */
export function closeSolo(): void {
  soloOpen = soloPushed = false;
}
