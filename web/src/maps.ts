// web/src/maps.ts - the Zombies maps, as the lobby's view lists them.
import type { MapInfo, View } from './api';
import { view } from './lobby';
import { store } from './storage';

/** What the lobby says of a map. A map that the view does not list has only its name. */
export const mapInfo = (name: string): MapInfo =>
  (view && view.maps.find((m) => m.name === name)) || { name, title: name };

/** Whether a map is one of Black Ops II's. */
export const isT6 = (name: string): boolean => mapInfo(name).of === 't6';

/** The maps a page plays in the engine: Black Ops' own. A Black Ops II map is played in Plutonium. */
export const t5Maps = (maps: MapInfo[]): MapInfo[] => maps.filter((m) => m.of !== 't6');

/** Whether the server has any map of a title. */
export const hasTitle = (maps: MapInfo[], of: string | undefined): boolean => maps.some((m) => m.of === of && m.have);

/** The maps a room picks from: those of each title the server has. */
export const roomMaps = (maps: MapInfo[]): MapInfo[] => maps.filter((m) => hasTitle(maps, m.of));

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
 * The picture of a Black Ops II map. The server reads none from that game's files, so the page draws the title's
 * name.
 */
export const T6_PICTURE = 'data:image/svg+xml,' + encodeURIComponent('<svg xmlns="http://www.w3.org/2000/svg" ' +
  'viewBox="0 0 400 300"><rect width="400" height="300" fill="#191412"/><text x="200" y="142" text-anchor="middle" ' +
  'font-family="Oswald, Arial Narrow, sans-serif" font-weight="600" font-size="40" letter-spacing="5" fill="#6b635a">' +
  'BLACK OPS</text><text x="200" y="200" text-anchor="middle" font-family="Oswald, Arial Narrow, sans-serif" ' +
  'font-weight="600" font-size="52" letter-spacing="8" fill="#8a2a33">II</text></svg>');

/**
 * Sets an <img> to a map's picture: its preview, or the picture of another kind, such as its loading screen
 * (server/mapart.go). The picture is set only when it changes.
 */
export function mapPicture(img: HTMLImageElement, name: string, kind?: string): HTMLImageElement {
  const src = isT6(name) ? T6_PICTURE : '/maps/' + encodeURIComponent(name) + '/' + (kind || 'preview');
  if (img.getAttribute('src') !== src) img.src = src;
  img.alt = mapInfo(name).title;
  return img;
}
