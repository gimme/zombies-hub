import { expect, test } from 'vitest';
import type { KindBoards, Leaderboards, MapInfo, TitleBoards } from './api';
import { ALL_MAPS, firstBehind, kindsOf, mapsOf, type Picked, pickBoard, titleName, versusesOf } from './titles';

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

test("a room's maps are those of its versus, and it may pick among the versus that the server has maps of", () => {
  const maps: MapInfo[] = [{ name: 'zombie_theater', title: 'Kino der Toten', of: 't5', have: true },
    { name: 'zm_standard_town', title: 'Town', of: 't6', mode: 'Survival', have: true },
    { name: 'zm_grief_town', title: 'Town', of: 't6', mode: 'Grief', versus: 'grief', have: true },
    { name: 'zm_grief_farm', title: 'Farm', of: 't6', mode: 'Grief', versus: 'grief' },
    { name: 'zm_cleansed_diner', title: 'Diner', of: 't6', mode: 'Turned', versus: 'turned' }];
  expect(mapsOf(maps, 't6', '').map((m) => m.name)).toEqual(['zm_standard_town']);
  expect(mapsOf(maps, 't6', 'grief').map((m) => m.name)).toEqual(['zm_grief_town', 'zm_grief_farm']);
  // the server lacks the Diner: no Turned
  expect(versusesOf(maps, 't6').map((v) => v.name)).toEqual(['Co-op', 'Grief']);
  expect(versusesOf(maps, 't5').map((v) => v.name)).toEqual(['Co-op']);
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

test('a browser that has come to no map has Kino der Toten behind the lobby, or a map of the first game that the ' +
  'server has', () => {
  const map = (name: string, of: string, have = true): MapInfo => ({ name, title: name, of, have });
  const all = [map('zombie_pentagon', 't5'), map('zombie_theater', 't5'), map('zm_transit', 't6'),
    map('zm_standard_town', 't6'), map('zm_zod', 't7'), map('zm_factory', 't7')];
  // Kino der Toten wherever the server has it, whatever its default map is
  expect(firstBehind(all, 'zombie_theater')).toBe('zombie_theater');
  expect(firstBehind(all, 'zm_factory')).toBe('zombie_theater');
  // Black Ops without it: the server's default map where that is the game's, else the game's first
  const noKino = all.map((m) => (m.name === 'zombie_theater' ? { ...m, have: false } : m));
  expect(firstBehind([...noKino, map('zombie_moon', 't5')], 'zombie_moon')).toBe('zombie_moon');
  expect(firstBehind(noKino, 'zm_factory')).toBe('zombie_pentagon');
  // no Black Ops: Black Ops II's default map, or its first, before any of Black Ops III
  const noT5 = all.map((m) => (m.of === 't5' ? { ...m, have: false } : m));
  expect(firstBehind(noT5, 'zm_standard_town')).toBe('zm_standard_town');
  expect(firstBehind(noT5, 'zm_factory')).toBe('zm_transit');
  // Black Ops III alone, and a server with no map
  const t7Alone = all.filter((m) => m.of === 't7');
  expect(firstBehind(t7Alone, 'zm_factory')).toBe('zm_factory');
  expect(firstBehind(t7Alone, 'zombie_theater')).toBe('zm_zod');
  expect(firstBehind(all.map((m) => ({ ...m, have: false })), 'zombie_theater')).toBe('');
});
