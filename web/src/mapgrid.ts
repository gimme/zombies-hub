// web/src/mapgrid.ts - what a room and the page to play locally both show of maps: the grid to pick a map from, and
// the card of the picked map.
import type { MapInfo } from './api';
import { el } from './dom';
import { dlState } from './engine';
import { mapButton } from './files';
import { mapPicture } from './maps';
import { ownPC } from './titles';

/**
 * Draws a grid of maps to pick from: a button for each map, with the picked one checked. pick is called with the
 * name of the map that is clicked. Maps that the server lacks are greyed out.
 *
 * With browser, a browser is to play the map. Greyed out too are then the maps that a browser can't play, because
 * the server has no list of what they read, and the maps that the page can't get as things are (files.ts: mapButton's
 * cant).
 *
 * With locked, as while a room's game runs, no map can be picked: all are greyed out but the picked one.
 *
 * The buttons are made again only when the maps or the pick change, so that a button keeps the focus.
 */
export function showMaps(
  grid: HTMLElement, maps: MapInfo[], picked: string, pick: (name: string) => unknown, browser: boolean,
  locked = false,
): void {
  grid.classList.toggle('locked', locked);
  const key = JSON.stringify([maps.map((m) => [m.name, m.have, m.list]), picked, browser, locked]);
  if (grid.dataset.key !== key) {
    grid.dataset.key = key;
    grid.textContent = '';
    for (const m of maps) {
      const b = el('button');
      b.type = 'button';
      b.setAttribute('role', 'radio');
      b.setAttribute('aria-checked', String(m.name === picked));
      b.title = m.title;
      const img = mapPicture(el('img'), m.name);
      img.alt = ''; // the button's text says the map
      b.append(img, m.title);
      b.onclick = () => pick(m.name);
      grid.append(b);
    }
  }
  maps.forEach((m, i) => {
    const b = grid.children[i] as HTMLButtonElement;
    if (locked) {
      b.disabled = true;
      return;
    }
    // null: the store is yet to say what it has of the map, after a download ended or the folder changed. The button
    // stays as it was.
    const why = !m.have || (browser && !m.list && !ownPC(m.of)) ? 'not available'
      : !browser || ownPC(m.of) ? '' : dlState[m.name] ? mapButton(m.name, '').cant || '' : null;
    if (why === null) return;
    b.disabled = !!why;
    b.title = why ? m.title + ': ' + why : m.title;
  });
}

/**
 * Shows a map's text on its card. The card is as tall as its picture whichever map it shows, of either game, so that
 * what is under it stays put (page.css: .about). A text longer than the card has room for scrolls, as Call of the
 * Dead's does, from its top for each map.
 */
export function showDesc(p: HTMLElement, desc: string): void {
  if (p.textContent !== desc) {
    p.textContent = desc;
    p.scrollTop = 0;
  }
  p.classList.toggle('hidden', !desc);
}
