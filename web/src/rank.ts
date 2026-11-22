// web/src/rank.ts - the numbers of the rows of a ranked list.

/**
 * The number of each row of a list that is ranked already. Rows that are level on the score share the number of the
 * first of them, and the row after them has the number of its own place: 1, 1, 3.
 */
export function ranks<R>(rows: R[], score: (row: R) => unknown): number[] {
  let number = 0;
  return rows.map((row, i) => {
    if (!i || score(row) !== score(rows[i - 1])) number = i + 1;
    return number;
  });
}
