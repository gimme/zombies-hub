// web/src/boards.ts - the leaderboards (server/leaderboards.go).
//
// The leaderboards are a view of their own, over whatever the player was on. The button at the top opens them, with
// ?boards=1 in the URL, as a step that the browser's Back goes back from. The button stays while they show, lit, and
// closes them again (boardsButton). The boards are read from the server each time they open. A game that the page
// starts closes them, so that the page comes back to the room (closeBoardsForGame).
//
// Each game that the server hosts has boards of its own. The player picks first the game, on a server of two, then
// the kind of board that the game has (Rounds, Easter eggs), then a map, or all of them (titles.ts: pickBoard). Until
// they pick a game, the boards show the game of their room, or outside a room the game they last picked for one
// (maps.ts: lastTitle).
//
// show draws the views (show.ts). It shows the leaderboards while boardsOpen is set, or the report
// of one game in their place while boardReport is set, and it calls loadBoards.
import type { AllBoard, AllRow, BoardRecord, GameResult, Leaderboards } from './api';
import { avatar } from './avatar';
import { $, el, placeTip } from './dom';
import { clock } from './format';
import { auth, me, page, view } from './lobby';
import { lastTitle, mapInfo, mapPicture } from './maps';
import { showRefusal } from './notice';
import { params } from './params';
import { ranks } from './rank';
import { menuSound } from './sounds';
import { store } from './storage';
import { ALL_MAPS, type Kind, kindsOf, pickBoard } from './titles';

/** Whether the leaderboards show. */
export let boardsOpen = params.get('boards') === '1';
// Whether opening them added a step to the browser's history, which closing them goes back from.
let boardsPushed = false;
// What the server said when they opened, or null while they are still to be read.
let boards: Leaderboards | null = null;
/** The report of the game of a row that the player clicked. It shows in place of the boards. */
export let boardReport: GameResult | null = null;
let boardsAsked = false; // the page is reading them

// the kinds of boards, by their names on the page
const KIND_NAMES: Record<Kind, string> = { rounds: 'Rounds', eggs: 'Easter eggs' };
// the rounds from which a map's tile has a frame, and the frame
const MILESTONES: [number, string][] = [[20, 'silver'], [30, 'gold']];
// What the player picked: the game, and of each game the kind, and of each game's kind the map (pickBoard). It is kept
// while the page is open.
const boardPicked: Record<string, string> = {};

/** Opens or closes the leaderboards, as the player asks, with the sound of a large menu. */
export function openBoards(open: boolean): void {
  if (open === boardsOpen) return;
  if (!open && boardsPushed) return history.back(); // the page's popstate listener then closes them (boardsFollowUrl)
  setUrl(open);
  boardsOpen = boardsPushed = open;
  boards = boardReport = null;
  menuSound(open, true);
  page.redraw();
}

/**
 * Closes the leaderboards for a game that the page starts. No sound plays, and the browser's history keeps its
 * steps.
 */
export function closeBoardsForGame(): void {
  if (!boardsOpen) return;
  setUrl(false);
  boardsOpen = boardsPushed = false;
  boards = boardReport = null;
  page.redraw();
}

function setUrl(open: boolean): void {
  const url = new URL(location.href);
  if (open) {
    url.searchParams.set('boards', '1');
    history.pushState(null, '', url);
  } else {
    url.searchParams.delete('boards');
    history.replaceState(null, '', url);
  }
}

/**
 * Follows the URL after a step in the browser's history: the leaderboards show when the URL has ?boards=1. They are
 * read again when they open.
 */
export function boardsFollowUrl(url: URL): void {
  boardsOpen = boardsPushed = url.searchParams.get('boards') === '1';
  boards = boardReport = null;
}

/**
 * The button at the top. It opens the leaderboards. While they show it goes back one step, as the gamepad's B does:
 * from the report of a game to the boards, and from the boards to what showed before them.
 */
export function boardsButton(): void {
  if (!boardsOpen || !boardReport) return openBoards(!boardsOpen);
  closeBoardReport();
}

/** Goes back from the report of a game to the boards. */
export function closeBoardReport(): void {
  boardReport = null;
  page.redraw();
}

/** Reads the leaderboards from the server, unless the page has them, and shows them. */
export async function loadBoards(): Promise<void> {
  if (boards || boardsAsked) return;
  boardsAsked = true;
  try {
    const rsp = await fetch('/lobby/leaderboards', { headers: auth(), cache: 'no-store' });
    if (rsp.status === 401) return void page.hello();
    if (!rsp.ok) throw new Error('its answer was ' + rsp.status);
    boards = await rsp.json();
  } catch (e) {
    console.log('lobby: no leaderboards (' + (e as Error).message + ')');
    showRefusal('Could not get the leaderboards from the server.');
    return;
  } finally {
    boardsAsked = false;
  }
  if (boardsOpen) showBoards();
}

// Shows the report of a game that the boards name, in their place, from its top.
function openBoardReport(report: GameResult): void {
  boardReport = report;
  page.redraw();
  $('overlay').scrollTop = 0;
}

interface Row {
  id: string;
  name: string;
  avatar?: string;
  /** The ID of the report of the row's game, on a map's board. */
  game?: string;
}

// A table of a board: its rows, in the order the server ranked them. A row has its number, the player, and the
// board's cells. score is what the board ranks by: rows that are level on it share a number (rank.ts).
function boardTable<R extends Row>(title: string, heads: string[], rows: R[], score: (row: R) => unknown,
  cells: (row: R) => HTMLElement[]): HTMLElement {
  const panel = el('section', 'panel tablewrap');
  panel.append(el('div', 'boardhead', title));
  if (!rows.length) {
    panel.append(el('p', 'boardempty', 'No results'));
    return panel;
  }
  const table = el('table');
  const head = el('tr');
  head.append(el('th', '', '#'), el('th', 'who', 'Player'), ...heads.map((h) => el('th', '', h)));
  const thead = el('thead');
  thead.append(head);
  const tbody = el('tbody');
  const numbers = ranks(rows, score);
  rows.forEach((row, i) => {
    const mine = row.id === me.id;
    const tr = el('tr', (numbers[i] === 1 ? 'first' : '') + (mine ? ' mine' : ''));
    const who = el('td', 'who');
    const player = el('div', 'player');
    // A row whose game has a report opens the report: its name is a button that covers the row.
    const report = boards && row.game ? boards.games[row.game] : undefined;
    let name: string | HTMLButtonElement = row.name;
    if (report) {
      name = el('button', 'rowlink', row.name);
      name.type = 'button';
      name.title = 'See how this game went';
      name.onclick = () => openBoardReport(report);
    }
    player.append(avatar(row.name, row.avatar, mine), name);
    if (mine) player.append(el('span', 'sr', ' (you)'));
    who.append(player);
    tr.append(el('td', '', String(numbers[i])), who, ...cells(row));
    tbody.append(tr);
  });
  table.append(thead, tbody);
  panel.append(table);
  return panel;
}

function showBoards(): void {
  if (!boards) return;
  boardTip(null); // the tip of a mark that is about to go
  // a row of buttons that pick one of several, with the picked one marked
  const picker = (box: HTMLElement, items: [string, string][], picked: string | undefined,
    pick: (key: string) => void) => {
    // the buttons are drawn again: the focus stays on the picked one
    const hadFocus = box.contains(document.activeElement);
    box.textContent = '';
    for (const [key, label] of items) {
      const b = el('button', '', label);
      b.type = 'button';
      b.setAttribute('role', 'radio');
      b.setAttribute('aria-checked', String(key === picked));
      b.onclick = () => {
        pick(key);
        showBoards();
      };
      box.append(b);
      if (hadFocus && key === picked) b.focus();
    }
  };
  const tables = $('boardtables');
  tables.textContent = '';
  const shown = pickBoard(boards, boardPicked, view && view.room ? view.room.of : lastTitle.get());
  // The game, where the server hosts two. Under it, the kinds of board that the game has, then its maps.
  $('boardtitles').classList.toggle('hidden', boards.titles.length < 2);
  picker($('boardtitles'), boards.titles.map((t): [string, string] => [t.id, t.name]), shown?.title.id, (id) => {
    boardPicked[''] = id;
  });
  if (!shown) return;
  const { title, kind } = shown;
  picker($('boardtabs'), kindsOf(title).map((k): [string, string] => [k, KIND_NAMES[k]]), kind, (k) => {
    boardPicked[title.id] = k;
  });
  const list = shown.boards.maps;
  const rounds = kind === 'rounds';
  // parties of five and more, on a server whose rooms hold them (-max-players)
  const big = !!view && view.size > 4;
  // the board of all the maps shows until the player picks a map
  const all = shown.map === ALL_MAPS;
  picker($('boardpicks'), list.length ? [[ALL_MAPS, rounds ? 'All maps' : 'All eggs'],
    ...list.map((m): [string, string] => [m.map, mapInfo(m.map).title])] : [], shown.map,
  (map) => {
    boardPicked[title.id + ':' + kind] = map;
  });
  tables.classList.toggle('all', all);
  if (all && list.length) return showAllBoard(tables, list.map((m) => m.map), rounds, shown.boards.all, boards);
  const board = list.find((m) => m.map === shown.map);
  if (!board) return;
  const head = rounds ? 'Round' : 'Time';
  type MapRow = (typeof board.duo)[number];
  const value = (row: MapRow) => el('td', 'record', rounds ? String(row.round || 0) : clock(row.seconds || 0));
  const party = (row: MapRow) => [el('td', '', String(row.party)), value(row)];
  const score = (row: MapRow) => (rounds ? row.round : row.seconds);
  tables.append(boardTable('Duo', [head], board.duo, score, (row) => [value(row)]),
    boardTable('Squad (3-4)', ['Players', head], board.squad, score, party));
  const bigTitle = `Squad (5${view && view.size > 5 ? '-' + view.size : ''})`; // 5-6 in rooms of 6
  if (big) tables.append(boardTable(bigTitle, ['Players', head], board.bigSquad, score, party));
}

// Shows a kind's board of all its maps (server/leaderboards.go: allBoard), for the rounds or for the easter eggs.
//
// First comes the player's own card, with a tile for each map. A tile is grey until the player has a best on the map,
// with a party of any size. Then it has the map's colours with the best over it, and a frame that says how good the
// best is: the silver or gold of MILESTONES, or an easter egg's gold. A tile opens the report of the game that set its
// best, as a row of a map's board does. Under the rounds' tiles is a legend of what silver and gold stand for.
//
// Then come the players, ranked by the sum of their bests (for easter eggs, those with more of them first). Each has a
// mark for each map, in the tiles' order: filled where they have a best, in the silver or gold of that best.
function showAllBoard(tables: HTMLElement, maps: string[], rounds: boolean, all: AllBoard, boards: Leaderboards): void {
  const n = (x: number) => x.toLocaleString('en-US');
  // The frames a best may have. key is the name under which the browser keeps that the player has seen the frame
  // (seenBests). A best wears the last of its frames.
  const frames: { key: string; frame: string; has: (r: BoardRecord) => boolean }[] = rounds
    ? MILESTONES.map(([round, frame]) => ({ key: String(round), frame, has: (r) => (r.round || 0) >= round }))
    : [{ key: 'egg', frame: 'gold', has: () => true }];
  const framesOf = (r: BoardRecord | undefined) => (r ? frames.filter((f) => f.has(r)) : []);
  const frameOf = (r: BoardRecord | undefined) => framesOf(r).pop()?.frame || '';
  const byMap = (row: AllRow) => new Map(row.bests.map((r): [string, BoardRecord] => [r.map, r]));
  const says = (r: BoardRecord) => (rounds ? 'round ' + n(r.round || 0) : clock(r.seconds || 0));

  const mine = byMap(all.mine);
  const card = el('section', 'panel tiles');
  const sum = el('div', 'sum');
  sum.append(el('b', '', rounds ? n(all.mine.round || 0) : clock(all.mine.seconds || 0)),
    el('span', 'label', rounds ? 'Total rounds' : 'Total time'));
  const head = el('div', 'tileshead');
  head.append(el('h2', 'display', rounds ? 'Your maps' : 'Your easter eggs'), sum);
  // What the player sees of a tile for the first time is animated (.tile.newlit, .newframe), one tile after the
  // other, and only once: a board that shows again, back from a game's report, would otherwise play it again.
  const seen = seenBests();
  let turns = 0;
  const grid = el('div', 'tilegrid');
  grid.style.setProperty('--cols', String(Math.min(maps.length, 5)));
  grid.addEventListener('animationend', (e) => {
    const played = ({ tilelit: 'newlit', tileframe: 'newframe' } as Record<string, string>)[e.animationName];
    if (played) (e.target as Element).closest('.tile')?.classList.remove(played);
  });
  for (const name of maps) {
    const r = mine.get(name);
    const report = r && boards.games[r.game];
    const cls = 'tile' + (r ? ' lit ' + frameOf(r) : '');
    let tile: HTMLElement;
    if (report) {
      const button = tile = el('button', cls);
      button.type = 'button';
      button.title = 'See how this game went';
      button.onclick = () => openBoardReport(report);
    } else {
      tile = el('div', cls);
    }
    const photo = el('span', 'photo');
    const img = mapPicture(el('img'), name);
    img.alt = ''; // the map's name is under the picture
    photo.append(img);
    if (r) {
      const score = el('span', 'score', rounds ? n(r.round || 0) : clock(r.seconds || 0));
      if (rounds) score.prepend(el('small', '', 'Round'));
      photo.append(score);
      const fresh = (what: string) => {
        const key = (rounds ? 'rounds:' : 'eggs:') + name + ':' + what;
        return !seen.has(key) && !!seen.add(key);
      };
      const lit = fresh('lit');
      const framed = framesOf(r).map((f) => fresh(f.key)).some(Boolean);
      if (lit) tile.classList.add('newlit');
      if (framed) tile.classList.add('newframe');
      if (lit || framed) tile.style.setProperty('--wait', turns++ * 120 + 'ms');
    }
    tile.append(photo, el('span', 'name', mapInfo(name).title));
    grid.append(tile);
  }
  if (turns) store.set('zombies-seen-bests', JSON.stringify({ id: me.id, keys: [...seen] }));
  card.append(head, grid);
  // what silver and gold stand for, on the frames and the marks, where a map may have either
  if (rounds) {
    const legend = el('div', 'legend');
    for (const [round, frame] of MILESTONES) {
      const mark = el('span', 'dots');
      mark.append(el('span', frame));
      const item = el('span', 'label');
      item.append(mark, 'Round ' + round + '+');
      legend.append(item);
    }
    card.append(legend);
  }

  const dots = (row: AllRow) => {
    const bests = byMap(row);
    const box = el('span', 'dots');
    const said: string[] = [];
    for (const name of maps) {
      const r = bests.get(name);
      const dot = el('span', r ? frameOf(r) || 'lit' : '');
      const tip = dot.dataset.tip = mapInfo(name).title + ': ' + (r ? says(r) : rounds ? 'no round yet' : 'not done');
      if (r) said.push(tip);
      box.append(dot);
    }
    box.setAttribute('role', 'img');
    box.setAttribute('aria-label', said.join(', '));
    const td = el('td');
    td.append(box);
    return td;
  };
  tables.append(card, boardTable(rounds ? 'All maps' : 'All easter eggs',
    rounds ? ['Maps', 'Total'] : ['Easter eggs', 'Time'], all.rows,
    (row) => (rounds ? row.round : row.bests.length + ' in ' + row.seconds),
    (row) => [dots(row), el('td', 'record', rounds ? n(row.round || 0) : clock(row.seconds || 0))]));
}

// What the player has seen of the tiles on their cards (showAllBoard): each map's colours and frames, for the rounds
// and for the easter eggs. This browser keeps it for the player it knows.
function seenBests(): Set<string> {
  try {
    const kept = JSON.parse(store.get('zombies-seen-bests') || '{}');
    return new Set(kept.id === me.id ? kept.keys : []);
  } catch {
    return new Set();
  }
}

/**
 * Shows a tip over a mark of a board of all the maps, with the map and the best that the mark stands for (its
 * data-tip), while the pointer is on it. null takes the tip away.
 */
export function boardTip(mark: HTMLElement | null): void {
  const tip = $('boardtip');
  tip.classList.toggle('hidden', !mark);
  if (!mark) return;
  tip.textContent = mark.dataset.tip || '';
  placeTip(tip, mark.getBoundingClientRect());
}
