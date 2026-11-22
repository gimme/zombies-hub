// web/src/scoreboard.ts - what the scoreboard of a game's report marks: the best and the worst of each column, and
// the MVP (result.ts draws it).
import type { PlayerStats } from './api';

export type Stat = 'cash' | 'score' | 'kills' | 'headshots' | 'downs' | 'revives' | 'doors';

/** A column of the scoreboard: its stat, whether more of it is better (1) or fewer (-1), and its heading. */
export type Column = [stat: Stat, way: 1 | -1, label: string];

const COLUMNS: Column[] = [['cash', 1, 'Cash'], ['score', 1, 'Score'], ['kills', 1, 'Kills'],
  ['headshots', 1, 'Headshots'], ['downs', -1, 'Downs'], ['revives', 1, 'Revives'], ['doors', 1, 'Door buys']];

/** The values that a column marks as its best and its worst, each null where the column marks none. */
export interface Marks {
  best: number | null;
  worst: number | null;
}

export interface Scoreboard {
  /**
   * The columns that the line of every player has. A report from before the game counted the doors that each player
   * bought has no such column (server/rooms.go: playerStats).
   */
  columns: Column[];
  /** The marks of each column in which the players differ. */
  marks: Partial<Record<Stat, Marks>>;
  /** The MVP, or null where the players are level. */
  mvp: PlayerStats | null;
  /** Whether any player has a worst value. A party of two has none. */
  anyRed: boolean;
}

/** A player's value in a column that every player has. */
export const statOf = (p: PlayerStats, stat: Stat): number => p[stat] ?? 0;

/**
 * Works out what the scoreboard of a game marks.
 *
 * A column marks its worst value where at least two players did better. With only one better, the worst says no more
 * than the mark of the best does. It marks its best value for one player alone, or for players who are tied for it
 * with at least two who did worse. With only one worse, the best says no more than the mark of the worst does.
 *
 * The MVP is the player with the most best values once their worst values are taken off, if that leaves any. Of
 * players who are level, it is the one with the most cash, and no one where that is level too.
 */
export function scoreboard(players: PlayerStats[]): Scoreboard {
  const columns = COLUMNS.filter(([stat]) => players.every((p) => p[stat] !== undefined));
  const marks: Partial<Record<Stat, Marks>> = {};
  for (const [stat, way] of columns) {
    const values = players.map((p) => statOf(p, stat));
    const most = Math.max(...values), least = Math.min(...values);
    if (most === least) continue;
    const [best, worst] = way > 0 ? [most, least] : [least, most];
    const others = (value: number) => values.filter((v) => v !== value).length;
    marks[stat] = { best: others(best) >= 2 || others(best) === values.length - 1 ? best : null,
      worst: others(worst) >= 2 ? worst : null };
  }
  const tally = players.map((p) => {
    const count = (which: keyof Marks) => columns.filter(([stat]) => statOf(p, stat) === marks[stat]?.[which]).length;
    return { p, gold: count('best'), red: count('worst') };
  });
  const top = Math.max(0, ...tally.map((t) => t.gold - t.red));
  const level = tally.filter((t) => t.gold - t.red === top);
  const most = Math.max(...level.map((t) => t.p.cash));
  const richest = level.filter((t) => t.p.cash === most);
  return { columns, marks, mvp: top > 0 && richest.length === 1 ? richest[0].p : null,
    anyRed: tally.some((t) => t.red > 0) };
}
