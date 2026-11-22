import { expect, test } from 'vitest';
import { ranks } from './rank';

const same = (x: number) => x;

test('rows that are level share a number, and the next row has the number of its place', () => {
  expect(ranks([30, 30, 25], same)).toEqual([1, 1, 3]);
  expect(ranks([30, 25, 25, 25, 20], same)).toEqual([1, 2, 2, 2, 5]);
});

test('rows with different scores are numbered from 1', () => {
  expect(ranks([30, 25, 20], same)).toEqual([1, 2, 3]);
  expect(ranks([30], same)).toEqual([1]);
  expect(ranks([], same)).toEqual([]);
});

test('the score is what the caller says it is', () => {
  const rows = [{ round: 30, name: 'Ann' }, { round: 30, name: 'Bob' }];
  expect(ranks(rows, (row) => row.round)).toEqual([1, 1]);
  expect(ranks(rows, (row) => row.name)).toEqual([1, 2]);
});
