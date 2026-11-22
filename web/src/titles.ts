// web/src/titles.ts - the games that the server hosts, which the code calls titles (server/title.go), and what the
// page shows of one title apart from the other: a room's maps, and the leaderboards' boards.
//
// A room plays one title, its map's. Its grid has the maps of that title alone (room.ts). The leaderboards show the
// boards of one title at a time: its rounds, or its easter eggs where it has them, of one map or of all its maps
// (boards.ts). Nothing here reads the page, so its tests run it as it is (titles.test.ts).
import type { KindBoards, Leaderboards, MapInfo, Title, TitleBoards } from './api';

/** The maps of a title, in the view's order. */
export const mapsOf = (maps: MapInfo[], of: string): MapInfo[] => maps.filter((m) => m.of === of);

/** The name of a title, such as Black Ops II, or '' for one that the server does not host. */
export const titleName = (titles: Title[], of: string): string => titles.find((t) => t.id === of)?.name || '';

/** A kind of board: the rounds, or the easter eggs. */
export type Kind = 'rounds' | 'eggs';

/** In place of a map: a kind's board of all its maps (boards.ts: showAllBoard). */
export const ALL_MAPS = 'all';

/** The kinds of board that a title has: its rounds, and its easter eggs where it has them. */
export const kindsOf = (t: TitleBoards): Kind[] => (t.eggs ? ['rounds', 'eggs'] : ['rounds']);

/** The board that shows: a title's boards of one kind, and of one map of it or of all its maps (ALL_MAPS). */
export interface Board {
  title: TitleBoards;
  kind: Kind;
  boards: KindBoards;
  map: string;
}

/**
 * The board to show, of the boards that the server sent, by what the player picked. picked has the title they picked
 * under '', the kind they picked of each title under its id, and the map they picked of each title's kind under
 * '<id>:<kind>'. Where they have picked nothing, or the boards lack their pick, it is the title prefer (their room's)
 * or else the first, its rounds, and its board of all the maps. null where the server hosts no title.
 */
export function pickBoard(boards: Leaderboards, picked: Record<string, string>, prefer = ''): Board | null {
  const title = [picked[''], prefer].map((id) => boards.titles.find((t) => t.id === id)).find(Boolean)
    || boards.titles[0];
  if (!title) return null;
  const kind: Kind = picked[title.id] === 'eggs' && title.eggs ? 'eggs' : 'rounds';
  const of = kind === 'eggs' && title.eggs ? title.eggs : title.rounds;
  const map = picked[title.id + ':' + kind];
  return { title, kind, boards: of, map: of.maps.some((m) => m.map === map) ? map : ALL_MAPS };
}
