import { expect, test } from 'vitest';
import { clock, plain } from './format';

test('clock writes seconds as minutes and seconds, with hours from an hour on', () => {
  expect(clock(0)).toBe('0:00');
  expect(clock(5)).toBe('0:05');
  expect(clock(65)).toBe('1:05');
  expect(clock(725)).toBe('12:05');
  expect(clock(3599)).toBe('59:59');
  expect(clock(3600)).toBe('1:00:00');
  expect(clock(3725)).toBe('1:02:05');
});

test("plain takes the colour codes out of the game's text", () => {
  expect(plain('^1Game ^7over ')).toBe('Game over');
  expect(plain('no codes')).toBe('no codes');
  expect(plain(undefined)).toBe('');
});
