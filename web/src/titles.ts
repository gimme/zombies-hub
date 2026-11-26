// web/src/titles.ts - the games that the server hosts, which the code calls titles (server/title.go), and what the
// page shows of one title apart from the other: a room's maps, and the leaderboards' boards.
//
// A room plays one title, its map's, and one versus (co-op, Grief or Turned), its map's too. Its grid has the maps of
// that title and versus alone (room.ts). The leaderboards show the boards of one title at a time: its rounds, or its
// easter eggs where it has them, of one map or of all its maps (boards.ts). The maps of Grief and Turned have no
// boards. Nothing here reads the page, so its tests run it as it is (titles.test.ts).
import type { KindBoards, Leaderboards, MapInfo, Title, TitleBoards } from './api';

/** The maps of a title, in the view's order, and of one versus where it is given (versusOf). */
export const mapsOf = (maps: MapInfo[], of: string, versus?: string): MapInfo[] =>
  maps.filter((m) => m.of === of && (versus === undefined || versusOf(m) === versus));

/** A way to play against each other: its id, its name, and what a room says of it. */
export interface Versus {
  id: string;
  name: string;
  desc: string;
}

/**
 * The ways to play against each other, which the code calls a versus (server/title.go: mapEntry.versus), in the order
 * that a room offers them: co-op, where the players play together against the zombies, then Grief, in two teams, and
 * Turned, each for themselves.
 */
const VERSUS: Versus[] = [
  { id: '', name: 'Co-op', desc: 'Everyone together against the zombies.' },
  { id: 'grief', name: 'Grief', desc: 'Two teams. A team wins once the other is down and it lasts the round.' },
  { id: 'turned', name: 'Turned', desc: 'One human, everyone else a zombie. The most points wins.' },
];

/** The versus of a map. */
export const versusOf = (map: MapInfo): string => map.versus || '';

/** The kinds of versus that the server has maps of in a title, which a room of the title may pick among. */
export const versusesOf = (maps: MapInfo[], of: string): Versus[] =>
  VERSUS.filter((v) => mapsOf(maps, of, v.id).some((m) => m.have));

/**
 * Whether a title's games are played on the players' own PCs, outside the page (server/title.go: inPage): Black Ops
 * II's, on Plutonium, and Black Ops III's, on BOIII. The page needs no map for them, and does not run the engine.
 */
export const ownPC = (of?: string): boolean => of === 't6' || of === 't7';

/** Whether the server has pictures of a title's maps. It has none of a Black Ops III map (server/t7.go: t7MapList). */
export const hasPictures = (of?: string): boolean => of !== 't7';

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
 * What the player picked on the leaderboards: a title, a kind, and of each title's kind a map, under '<id>:<kind>'.
 * The kind is one for all the titles: it stays as the player picks another title.
 */
export interface Picked {
  title?: string;
  kind?: Kind;
  maps: Record<string, string>;
}

/**
 * The board to show, of the boards that the server sent, by what the player picked. Where they have picked nothing,
 * or the boards lack their pick, it is the title prefer (their room's) or else the first, its rounds, and its board
 * of all the maps. A title without easter eggs shows its rounds where they picked the easter eggs. null where the
 * server hosts no title.
 */
export function pickBoard(boards: Leaderboards, picked: Picked, prefer = ''): Board | null {
  const title = [picked.title, prefer].map((id) => boards.titles.find((t) => t.id === id)).find(Boolean)
    || boards.titles[0];
  if (!title) return null;
  const kind: Kind = picked.kind === 'eggs' && title.eggs ? 'eggs' : 'rounds';
  const of = kind === 'eggs' && title.eggs ? title.eggs : title.rounds;
  const map = picked.maps[title.id + ':' + kind];
  return { title, kind, boards: of, map: of.maps.some((m) => m.map === map) ? map : ALL_MAPS };
}
