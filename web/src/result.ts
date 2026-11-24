// web/src/result.ts - how a game went: the report that a room shows after game over, and the leaderboards show of a
// record's game.
//
// The report has the round that the game reached, how long it lasted, and the scoreboard: a line for each player.
// The best and the worst of each column are marked, and a stamp names the MVP (scoreboard.ts). A game of Grief or
// Turned, on a Black Ops II map, has a winner, which the report names: the team that won Grief, or the player who won
// Turned. Turned counts no rounds.
import type { GameResult, Room } from './api';
import { avatar } from './avatar';
import { $, el } from './dom';
import { clock } from './format';
import { me } from './lobby';
import { mapInfo, mapShot } from './maps';
import { scoreboard, statOf } from './scoreboard';
import { store } from './storage';

/**
 * The report of the room's last game, when the room is to show it: the game ended at game over, the player was in
 * it, and they have not gone back to the room since. Otherwise null. A player who played in the Windows client sees
 * it on their page too.
 */
export function resultToShow(room: Room | null): GameResult | null {
  const result = room && room.result;
  if (!result || store.get('zombies-seen-result') === result.id) return null;
  return result.players.some((p) => p.id === me.id) ? result : null;
}

let shownResult = ''; // the ID of the report that the page last showed

/** Draws the report of a game. */
export function showResult(result: GameResult): void {
  // The stamp of the MVP comes down the first time that a report shows (page.css: .mvp.new).
  const fresh = shownResult !== result.id;
  if (fresh) {
    shownResult = result.id;
    console.log('lobby: how the game went: ' + (result.round ? 'round ' + result.round : 'no round') + ', ' +
      result.players.length + (result.players.length === 1 ? ' player' : ' players'));
  }
  const map = mapInfo(result.map);
  mapShot($('resultshot'), result.map);
  $('resultkicker').textContent = 'Game over · ' + map.title + (map.mode ? ' · ' + map.mode : '');
  const n = (x: number) => x.toLocaleString('en-US');
  $('resulttitle').textContent = result.round ? 'Round ' : 'Game over';
  if (result.round) $('resulttitle').append(el('span', '', n(result.round)));
  // The totals of the party. A game with a winner names it, marked: the team that won, or the player. A game with
  // teams and no winner is played for cash (the Grief mod), so its totals are the cash of each team, with the higher
  // one marked.
  const teams = result.players.some((p) => p.team);
  const winner = result.players.find((p) => p.won);
  const sum = (stat: 'cash' | 'score' | 'kills', team?: number) =>
    result.players.reduce((total, p) => total + (team && p.team !== team ? 0 : p[stat]), 0);
  const cash = [sum('cash', 1), sum('cash', 2)];
  const totals = $('resulttotals');
  totals.textContent = '';
  const shown: [value: string, label: string, won?: boolean][] = winner
    ? [[clock(result.seconds), 'Time'], [winner.team ? 'Team ' + winner.team : winner.name, 'Winner', true]]
    : teams
      ? [[clock(result.seconds), 'Time'], [n(cash[0]), 'Team 1 cash', cash[0] > cash[1]],
        [n(cash[1]), 'Team 2 cash', cash[1] > cash[0]]]
      : [[clock(result.seconds), 'Time'], [n(sum('cash')), 'Team cash'], [n(sum('score')), 'Team score'],
        [n(sum('kills')), 'Team kills']];
  for (const [value, label, won] of shown) {
    const t = el('div');
    t.append(el('b', won ? 'won' : '', value), el('span', 'label', label));
    totals.append(t);
  }
  const { columns, marks, mvp, anyRed } = scoreboard(result.players);
  const head = $('resulthead');
  head.textContent = '';
  head.append(el('th', '', 'Player'), ...columns.map(([, , label]) => el('th', '', label)));
  const rows = $('resultrows');
  rows.textContent = '';
  for (const p of result.players) {
    const tr = el('tr', p.id === me.id ? 'mine' : '');
    const who = el('td');
    const player = el('div', 'player');
    player.append(avatar(p.name, p.avatar, p.id === me.id), p.name);
    if (p.id === me.id) player.append(el('span', 'sr', ' (you)'));
    if (p.team) player.append(el('i', '', 'Team ' + p.team));
    if (mvp === p) {
      const stamp = el('span', 'mvp' + (fresh ? ' new' : ''), 'MVP');
      stamp.title = 'The most golden stats' + (anyRed ? ', minus red ones' : '');
      player.append(stamp);
    }
    who.append(player);
    tr.append(who);
    for (const [stat, way, label] of columns) {
      const value = statOf(p, stat);
      const cell = el('span', 'stat', n(value));
      const m = marks[stat];
      const mark = m && (value === m.best ? 'best' : value === m.worst ? 'worst' : '');
      if (mark) {
        cell.classList.add(mark);
        const [more, less] = stat === 'score' ? ['Highest', 'Lowest'] : stat === 'cash' ? ['Most', 'Least']
          : ['Most', 'Fewest'];
        cell.title = ((mark === 'best') === (way > 0) ? more : less) + ' ' + label.toLowerCase();
      }
      const td = el('td');
      td.append(cell);
      tr.append(td);
    }
    rows.append(tr);
  }
}
