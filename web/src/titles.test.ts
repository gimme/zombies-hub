import { expect, test } from 'vitest';
import type { KindBoards, Leaderboards, MapInfo, TitleBoards } from './api';
import { ALL_MAPS, kindsOf, mapsOf, pickBoard, titleName } from './titles';

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
    expect(pickBoard(boards, {})).toEqual({ title: t5, kind: 'rounds', boards: t5.rounds, map: ALL_MAPS });
    expect(pickBoard(boards, {}, 't6')).toEqual({ title: t6, kind: 'rounds', boards: t6.rounds, map: ALL_MAPS });
    // the player's pick comes before the room's title
    expect(pickBoard(boards, { '': 't5' }, 't6')?.title).toBe(t5);
  });

test('the leaderboards keep what the player picked of each title, where the boards have it', () => {
  const picked = { '': 't5', t5: 'eggs', 't5:eggs': 'zombie_moon', 't5:rounds': 'zombie_theater', t6: 'eggs' };
  expect(pickBoard(boards, picked)).toEqual({ title: t5, kind: 'eggs', boards: t5.eggs, map: 'zombie_moon' });
  // Black Ops II has no easter eggs: its rounds show, of all its maps
  expect(pickBoard(boards, { ...picked, '': 't6' })).toEqual(
    { title: t6, kind: 'rounds', boards: t6.rounds, map: ALL_MAPS });
  // a map that the kind's boards lack, and a title that the server no longer hosts
  expect(pickBoard(boards, { '': 't5', 't5:rounds': 'zm_standard_town' })?.map).toBe(ALL_MAPS);
  expect(pickBoard({ titles: [t6], games: {} }, picked)?.title).toBe(t6);
  expect(pickBoard({ titles: [], games: {} }, picked)).toBeNull();
});
