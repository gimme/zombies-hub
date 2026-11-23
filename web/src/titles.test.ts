import { expect, test } from 'vitest';
import type { KindBoards, Leaderboards, MapInfo, TitleBoards } from './api';
import { ALL_MAPS, kindsOf, mapsOf, type Picked, pickBoard, titleName } from './titles';

// A kind's boards of these maps, with no one on them.
const kind = (...maps: string[]): KindBoards => ({
  maps: maps.map((map) => ({ map, duo: [], squad: [], bigSquad: [] })),
  all: { rows: [], mine: { id: 'me', name: 'Me', bests: [] } },
});
const t5: TitleBoards = { id: 't5', name: 'Black Ops', rounds: kind('zombie_theater', 'zombie_moon'),
  eggs: kind('zombie_moon') };
const t6: TitleBoards = { id: 't6', name: 'Black Ops II', rounds: kind('zm_standard_town') };
const boards: Leaderboards = { titles: [t5, t6], games: {} };

test("a room's maps are those of its title", () => {
  const maps: MapInfo[] = [{ name: 'zombie_theater', title: 'Kino der Toten', of: 't5' },
    { name: 'zm_standard_town', title: 'Town', of: 't6' }, { name: 'zombie_moon', title: 'Moon', of: 't5' }];
  expect(mapsOf(maps, 't5').map((m) => m.name)).toEqual(['zombie_theater', 'zombie_moon']);
  expect(mapsOf(maps, 't6').map((m) => m.name)).toEqual(['zm_standard_town']);
  expect(titleName([{ id: 't6', name: 'Black Ops II' }], 't6')).toBe('Black Ops II');
  expect(titleName([{ id: 't5', name: 'Black Ops' }], 't6')).toBe('');
});

test('a title has its rounds, and its easter eggs where it has them', () => {
  expect(kindsOf(t5)).toEqual(['rounds', 'eggs']);
  expect(kindsOf(t6)).toEqual(['rounds']);
});

test("the leaderboards show the first title's rounds of all its maps, or the room's title, until the player picks",
  () => {
    expect(pickBoard(boards, { maps: {} })).toEqual({ title: t5, kind: 'rounds', boards: t5.rounds, map: ALL_MAPS });
    expect(pickBoard(boards, { maps: {} }, 't6')).toEqual(
      { title: t6, kind: 'rounds', boards: t6.rounds, map: ALL_MAPS });
    // the player's pick comes before the room's title
    expect(pickBoard(boards, { title: 't5', maps: {} }, 't6')?.title).toBe(t5);
  });

test('the leaderboards keep what the player picked: one kind for all the titles, and a map of each, where the boards ' +
  'have them', () => {
  const picked: Picked = { title: 't5', kind: 'eggs', maps: { 't5:eggs': 'zombie_moon', 't5:rounds': 'zombie_theater' } };
  expect(pickBoard(boards, picked)).toEqual({ title: t5, kind: 'eggs', boards: t5.eggs, map: 'zombie_moon' });
  // Black Ops II's maps here have no easter egg: its rounds show, of all its maps
  expect(pickBoard(boards, { ...picked, title: 't6' })).toEqual(
    { title: t6, kind: 'rounds', boards: t6.rounds, map: ALL_MAPS });
  // the easter eggs of a title that has them, once the player picks them for another title
  const t6Eggs: TitleBoards = { ...t6, eggs: kind('zm_standard_town') };
  expect(pickBoard({ titles: [t5, t6Eggs], games: {} }, { ...picked, title: 't6' })?.kind).toBe('eggs');
  // a map that the kind's boards lack, and a title that the server no longer hosts
  expect(pickBoard(boards, { title: 't5', maps: { 't5:rounds': 'zm_standard_town' } })?.map).toBe(ALL_MAPS);
  expect(pickBoard({ titles: [t6], games: {} }, picked)?.title).toBe(t6);
  expect(pickBoard({ titles: [], games: {} }, picked)).toBeNull();
});
