import { expect, test } from 'vitest';
import type { PlayerStats } from './api';
import { scoreboard } from './scoreboard';

// A player's line. Every stat is 0 unless it is given.
const line = (name: string, stats: Partial<PlayerStats> = {}): PlayerStats => ({
  id: name, name, score: 0, cash: 0, kills: 0, headshots: 0, downs: 0, revives: 0, doors: 0, ...stats,
});

test('a column in which the players are level has no marks', () => {
  const board = scoreboard([line('a', { kills: 5 }), line('b', { kills: 5 })]);
  expect(board.marks).toEqual({});
  expect(board.mvp).toBeNull();
  expect(board.anyRed).toBe(false);
});

test('a party of two marks the best alone', () => {
  const board = scoreboard([line('a', { kills: 9 }), line('b', { kills: 5 })]);
  expect(board.marks.kills).toEqual({ best: 9, worst: null });
  expect(board.mvp?.name).toBe('a');
  expect(board.anyRed).toBe(false);
});

test('a party of four marks the best and the worst', () => {
  const board = scoreboard([line('a', { kills: 9 }), line('b', { kills: 5 }), line('c', { kills: 7 }),
    line('d', { kills: 2 })]);
  expect(board.marks.kills).toEqual({ best: 9, worst: 2 });
  expect(board.anyRed).toBe(true);
});

test('players tied for the best are marked when two did worse, and not when one did', () => {
  const tied = [line('a', { kills: 9 }), line('b', { kills: 9 })];
  expect(scoreboard([...tied, line('c', { kills: 1 }), line('d', { kills: 2 })]).marks.kills?.best).toBe(9);
  // With one worse, that player's mark of the worst says it all.
  expect(scoreboard([...tied, line('c', { kills: 1 })]).marks.kills).toEqual({ best: null, worst: 1 });
});

test('fewer downs are better', () => {
  const board = scoreboard([line('a', { downs: 0 }), line('b', { downs: 3 }), line('c', { downs: 6 })]);
  expect(board.marks.downs).toEqual({ best: 0, worst: 6 });
});

test('a report without the doors of every player has no column of doors', () => {
  const old = line('b');
  delete old.doors;
  const stats = scoreboard([line('a', { doors: 3 }), old]).columns.map(([stat]) => stat);
  expect(stats).toEqual(['cash', 'score', 'kills', 'headshots', 'downs', 'revives']);
});

test('the MVP has the most best values after the worst are taken off', () => {
  const board = scoreboard([
    line('a', { kills: 9, headshots: 9, score: 9, revives: 0, downs: 9 }),
    line('b', { kills: 5, headshots: 5, score: 5, revives: 9, downs: 0 }),
    line('c', { kills: 2, headshots: 2, score: 2, revives: 5, downs: 5 }),
  ]);
  // a has three best values and two worst, b has two best values, c has three worst values
  expect(board.mvp?.name).toBe('b');
});

test('of players who are level, the one with the most cash is the MVP, and no one when that is level too', () => {
  // a and b have one best value each, and the same cash
  expect(scoreboard([line('a', { kills: 9, cash: 100 }), line('b', { revives: 9, cash: 100 }), line('c')]).mvp)
    .toBeNull();
  // a and b have two best values each, and b has more cash
  expect(scoreboard([line('a', { kills: 9, headshots: 9, cash: 100 }), line('b', { revives: 9, cash: 150 }),
    line('c')]).mvp?.name).toBe('b');
});
