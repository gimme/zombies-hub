// web/src/maps.ts - the Zombies maps, as the lobby's view lists them.
import type { MapInfo, View } from './api';
import { view } from './lobby';
import { store } from './storage';

/** What the lobby says of a map. A map that the view does not list has only its name. */
export const mapInfo = (name: string): MapInfo =>
  (view && view.maps.find((m) => m.name === name)) || { name, title: name };

/** The maps a page plays in the engine: Black Ops' own. A Black Ops II map is played in Plutonium. */
export const t5Maps = (maps: MapInfo[]): MapInfo[] => maps.filter((m) => m.of !== 't6');

/**
 * Whether the server has any map of a title. The page to play offline asks it of the maps, which a page that is
 * offline still has (session.ts: goOffline).
 */
export const hasTitle = (maps: MapInfo[], of: string | undefined): boolean => maps.some((m) => m.of === of && m.have);

/**
 * The map that the player picked to play offline (solo.ts), which the browser's storage keeps. It is the server's
 * default map until they pick one, or when the server lacks their pick, and the first map that a browser can play
 * when the server lacks that too. It is '' when there is none.
 */
export function soloMapName(v: View): string {
  const have = (name: string) => !!name && v.maps.some((m) => m.name === name && m.have && m.list);
  const kept = store.get('zombies-solo-map');
  if (have(kept)) return kept;
  return have(v.defaultMap) ? v.defaultMap : v.maps.find((m) => m.have && m.list)?.name || '';
}

/**
 * The game that the player last picked as a room's host (room.ts: showRoomTitle), by its title's id, which the
 * browser's storage keeps: the room they make next is of it (main.ts), and the leaderboards show it outside a room
 * (boards.ts). '' until they pick one.
 */
export const lastTitle = {
  get: (): string => store.get('zombies-title'),
  set: (of: string): void => store.set('zombies-title', of),
};

/**
 * Sets an <img> to a map's picture: its preview, or the picture of another kind, such as its loading screen
 * (server/mapart.go). The picture is set only when it changes. The <img> says the map's game (data-of): a Black Ops
 * II map's preview is its picture on its card in the game's lobby, which keeps its own shape (page.css).
 */
export function mapPicture(img: HTMLImageElement, name: string, kind?: string): HTMLImageElement {
  const map = mapInfo(name);
  const src = '/maps/' + encodeURIComponent(name) + '/' + (kind || 'preview');
  if (img.getAttribute('src') !== src) img.src = src;
  img.alt = map.title;
  img.dataset.of = map.of || '';
  return img;
}

/**
 * Shows a map in a shot (index.html: .shot), a picture with words over it. A Black Ops map's is its preview, with no
 * words. A Black Ops II map's is its card in the game's lobby, which has a band at its foot. The lobby writes the
 * map's place in the band, over the map's name and its mode, if it has one alone, and so does the shot.
 */
export function mapShot(shot: HTMLElement, name: string): void {
  const map = mapInfo(name);
  const t6 = map.of === 't6';
  mapPicture(shot.querySelector('img')!, name, t6 ? 'card' : undefined);
  shot.dataset.of = map.of || '';
  const words = shot.querySelector('.words')!;
  words.classList.toggle('hidden', !t6 || !map.place);
  words.children[0].textContent = (t6 && map.place) || '';
  words.children[1].textContent = t6 && map.place ? map.title + (map.mode ? ' / ' + map.mode : '') : '';
}
