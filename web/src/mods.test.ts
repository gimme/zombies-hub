import { expect, test } from 'vitest';
import type { Mod } from './api';
import { modNames, modOn, modValue } from './mods';

const grief: Mod = { id: 'grief', name: 'Grief', desc: '', titles: ['t5'] };
const cap: Mod = { id: 'cap', name: 'Zombie Cap', desc: '', options: [{ value: 24 }, { value: 32 }, { value: 48 }],
  titles: ['t5', 't6'] };

test('a switch is on when the room names it', () => {
  expect(modValue(['grief'], grief)).toBe(true);
  expect(modValue([], grief)).toBe(false);
  expect(modOn(['cap=32'], grief)).toBe(false);
});

test('a mod with options is at its first option until the room names another', () => {
  expect(modValue([], cap)).toBe(24);
  expect(modOn([], cap)).toBe(false);
  expect(modValue(['grief', 'cap=32'], cap)).toBe(32);
  expect(modOn(['cap=32'], cap)).toBe(true);
  expect(modOn(['cap=24'], cap)).toBe(false);
});

test('the names of the mods that are on come in the order of the server, with the value of an option', () => {
  expect(modNames([cap, grief], ['grief', 'cap=48'])).toEqual(['Zombie Cap 48', 'Grief']);
  expect(modNames([cap, grief], [])).toEqual([]);
  expect(modNames(undefined, ['grief'])).toEqual([]);
});
