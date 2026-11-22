// web/src/mapgrid.ts - what a room and the page to play offline both show of maps: the grid to pick a map from, and
// the card of the picked map.
import type { MapInfo } from './api';
import { el } from './dom';
import { dlState } from './engine';
import { mapButton } from './files';
import { mapPicture } from './maps';

/**
 * Draws a grid of maps to pick from: a button for each map, with the picked one checked. pick is called with the
 * name of the map that is clicked. Maps that the server lacks are greyed out.
 *
 * With browser, a browser is to play the map. Greyed out too are then the maps that a browser can't play, because
 * the server has no list of what they read, and the maps that the page can't get as things are (files.ts: mapButton's
 * cant).
 *
 * The buttons are made again only when the maps or the pick change, so that a button keeps the focus.
 */
export function showMaps(
  grid: HTMLElement, maps: MapInfo[], picked: string, pick: (name: string) => unknown, browser: boolean,
): void {
  const key = JSON.stringify([maps.map((m) => [m.name, m.have, m.list]), picked, browser]);
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
    // null: the store is yet to say what it has of the map, after a download ended or the folder changed. The button
    // stays as it was.
    const why = !m.have || (browser && !m.list && m.of !== 't6') ? 'not available'
      : !browser || m.of === 't6' ? '' : dlState[m.name] ? mapButton(m.name, '').cant || '' : null;
    if (why === null) return;
    b.disabled = !!why;
    b.title = why ? m.title + ': ' + why : m.title;
  });
}

/**
 * Makes a map's card as tall as the tallest map needs. The card has the title and text of every map in it, unseen
 * (page.css: .about), so it keeps its height whichever map it shows, at any width of the window. Call of the Dead
 * has the most text. The unseen texts are made again only when the maps change.
 */
export function showAbout(box: HTMLElement, maps: MapInfo[]): void {
  const key = JSON.stringify(maps.map((m) => [m.title, m.desc]));
  if (box.dataset.key === key) return;
  box.dataset.key = key;
  for (const ghost of box.querySelectorAll('.ghost')) ghost.remove();
  for (const m of maps) {
    const ghost = el('div', 'ghost');
    ghost.setAttribute('aria-hidden', 'true');
    ghost.append(el('h1', 'display', m.title));
    if (m.desc) ghost.append(el('p', '', m.desc));
    box.append(ghost);
  }
}
