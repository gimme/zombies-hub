// e2e/game/play.ts - what more than one game test has a player do, or checks beside what they do.
import { map, type Server } from './server';
import { PAD, type Player, type PlayerState } from './player';

const sleep = (ms: number) => new Promise((done) => setTimeout(done, ms));

/** How a player takes the game in over its pregame menu (loadingMovie). */
export type Takes = 'room' | 'solo' | 'gamepad' | 'key';

/**
 * The map's loading movie (bo1-zombies/web/src/bink_web.cpp, src/client/cl_cin.cpp), checked beside what the player
 * does (Player.background). The page plays it from the engine's start, or with +set bo1_web_movieAtLoad 1 as the game
 * loads the map, and says when its first picture shows: a screenshot of it five seconds in ("loading"). A movie that
 * can't be opened (the install has them all), or that the page can't show, fails the player.
 *
 * A game loaded before the movie ends waits for it, the pregame menu up (bo1-zombies/src/client_mp/cl_cgame_mp.cpp:
 * CL_FirstSnapshot). As the game loads, the mouse is no one's, the browser's arrow over the movie
 * (CL_LoadingMovieHidesCursor): the click that takes the player into the game captures it and goes full screen
 * (bo1-zombies/web/src/library_bo1.js), which keeps it as the pregame menu's cursor shows. In a room the players wait
 * for the movie; alone (solo), the player clicks over it as the game loads, presses Esc on the pregame menu, which
 * skips nothing, and clicks again, which skips the movie, the mouse captured as the game goes in and the movie playing
 * on over it until they spawn (CL_LoadingMovieOverGame). With a gamepad, X on the pregame menu, its first press in the
 * game, turns it on, a new player's being off, and binds its layouts (bo1-zombies/src/win32/win_input.cpp:
 * IN_GamepadsMove), and goes full screen as a click does (library_bo1.js: padPressed); A then skips the movie. With a
 * key (key), the player's first input in the game takes them in, full screen with the mouse captured. A machine busy
 * enough loads the map late, and the movie may end by itself before the click or A, which then have nothing to skip.
 */
export async function loadingMovie(p: Player, takes: Takes): Promise<void> {
  if (!(await p.next(/^\[bo1\] command line: /, 900_000))) return;
  const idle = p.next(/^input: the mouse is no one's$/, 900_000);
  let loaded = false;
  const hasLoaded = p.next(/^The game has loaded, and waits for the loading movie$/, 900_000);
  hasLoaded.then((line) => { loaded = !!line; }, () => {});
  const opened = p.next(/^page: the loading movie shows/, 20_000);
  const failed = p.next(new RegExp("^(\\[bo1\\] bink: .*(can't open|not a Bink movie|no decoder|no video in it|" +
    'opening the .* decoder|no thread).*|movie: .*|BinkOpen failed on .*|R_Cinematic_BinkOpen .* failed.*)$'), 20_000);
  const which = await Promise.race([opened.then((ok) => (ok ? 'opened' : '')),
    failed.then((bad) => (bad ? 'failed' : ''))]);
  let skipped = false;
  p.next(/^The loading movie was skipped$/, 900_000).then((line) => { skipped = !!line; }, () => {});
  let movieOver = false; // skipped, or ended by itself (web/src/game.ts: movieSaid)
  p.next(/^page: the loading movie is over$/, 900_000).then((line) => { movieOver = !!line; }, () => {});
  if (which === 'failed') {
    throw new Error("the loading movie could not be opened or shown (the page's console says why)");
  }
  if (which !== 'opened') {
    throw new Error('the engine started, and its loading movie neither showed nor failed in 20 s');
  }
  // the click over the movie as the game loads, alone: the game loads within seconds, so it comes first
  if (takes === 'solo') {
    if (!(await idle)) throw new Error("the mouse was never no one's over the loading movie as the game loaded");
    if (loaded) throw new Error('the game loaded before the player could click over its loading movie');
    await p.click(100);
    if (!(await p.captured(true, 5000))) {
      throw new Error('a click over the loading movie as the game loaded did not capture the mouse');
    }
    if (!(await p.fullScreenWithin(5000))) {
      throw new Error('the click that took the player into the game did not go full screen');
    }
    if (skipped) throw new Error('the click that took the player into the game skipped the loading movie');
  }
  await sleep(5000);
  await p.shot('loading');
  p.note('the loading movie plays');
  if (!(await hasLoaded)) return;
  if (takes === 'solo' && (await p.captured(false, 3000))) {
    throw new Error("the page let the mouse go as the pregame menu's cursor showed, in full screen");
  }
  await p.page.mouse.move(640, 400);
  await sleep(1000);
  await p.shot('pregame');
  if (takes === 'gamepad') {
    const bound = p.next(/^gamepad: on, with buttons_default and thumbstick_default$/, 30_000);
    if (!(await p.padPressUntil(PAD.X, /^gamepad: a button of it was pressed, which turns it on$/))) {
      throw new Error("X on the pregame menu, the gamepad's first press in the game, held for 1 s 3 times, did " +
        'not turn it on');
    }
    if (!(await bound)) {
      throw new Error('the gamepad turned on without its layouts bound (buttons_default, thumbstick_default)');
    }
    if (!(await p.fullScreenWithin(5000))) {
      throw new Error("X, the gamepad's first press in the game, did not go full screen");
    }
    await sleep(1000);
    await p.shot('pregame-gamepad');
    if (!(await p.padPressUntil(PAD.A, /^The loading movie was skipped$/))) {
      if (!movieOver) throw new Error('A on the pregame menu, held for 1 s 3 times, did not skip the loading movie');
      p.note("X, the gamepad's first press in the game, turned it on, binding its layouts, and went full screen, " +
        'and A had nothing to skip: the loading movie had ended by itself, the machine slow');
      return;
    }
    p.note("X, the gamepad's first press in the game, turned it on, binding its layouts, and went full screen, and A " +
      'skipped the loading movie');
    return;
  }
  if (takes === 'key') {
    await p.page.keyboard.press('KeyV');
    if (!(await p.captured(true, 5000))) throw new Error('a key over the pregame menu did not capture the mouse');
    if (!(await p.fullScreenWithin(5000))) throw new Error('a key over the pregame menu did not go full screen');
    p.note('a key over the pregame menu captured the mouse and went full screen' +
      (movieOver ? ', the loading movie having ended by itself' : ''));
    return;
  }
  if (takes !== 'solo') return;
  await p.escape();
  await sleep(1000);
  if (skipped) throw new Error('Esc skipped the loading movie, which the left mouse button skips');
  const left = p.next(/^The loading movie leaves the game: /, 900_000);
  const overBefore = movieOver;
  await p.click(100);
  for (let waited = 0; !skipped && waited < 5000; waited += 250) await sleep(250);
  if (!skipped && overBefore) {
    p.note('a click over the loading movie captured the mouse and went full screen, the pregame menu kept the mouse ' +
      'in full screen, Esc skipped nothing, and the next click had nothing to skip: the loading movie had ended by ' +
      'itself, the machine slow');
    return;
  }
  if (!skipped) throw new Error('the click on the pregame menu did not skip the loading movie');
  if (!(await p.captured(true, 5000))) {
    throw new Error('the mouse was not captured as the game went in, the loading movie skipped');
  }
  const why = await Promise.race([left, sleep(10_000).then(() => '')]);
  if (why !== 'The loading movie leaves the game: the player spawned') {
    throw new Error(`the loading movie did not play on over the game until the player spawned ` +
      `(${why || 'it never left'})`);
  }
  p.note('a click over the loading movie captured the mouse and went full screen, the pregame menu kept the mouse in ' +
    'full screen, Esc skipped nothing, and the next click skipped the movie, the mouse captured as the game went in ' +
    'and the movie playing on over it until the player spawned');
}

/**
 * Call of the Dead's outro, at game over (bo1-zombies/src/client/cl_cin.cpp: CL_PlayOutro), checked beside what the
 * player does: once the engine has it open, a screenshot of it five seconds in ("outro"); the game ends with it.
 * Alone (solo), a key skips the rest, and the game ends at once.
 */
export async function outro(p: Player, solo: boolean): Promise<void> {
  if (!(await p.next(/^Playing cinematic zombie_coast_outro$/, 3_600_000))) return;
  if (!(await p.next(/^Playing cinematic .*zombie_coast_outro\.bik: .* frames at /, 20_000))) {
    throw new Error('the outro was asked for, and did not play in 20 s');
  }
  await sleep(5000);
  await p.shot('outro');
  p.note('the outro rolls');
  if (!solo) return;
  const ended = p.next(/^lobby: the game ended \(over\): /, 5000);
  await p.page.keyboard.press('Space');
  if (!(await ended)) throw new Error('a key did not skip the outro: the game had not ended 5 s after it');
  p.note('a key skipped the outro');
}

/**
 * Has a room's game start for its players: the host makes the room, the others open it by its link, one at a time
 * (they are numbered in that order: server/rooms.go), each gets its map and says they are ready, the host last, once
 * all are in. Returns the room's code once its game has started, its game loaded on the server.
 */
export async function startRoomGame(server: Server, host: Player, open: (room: string) => Promise<Player[]>):
  Promise<string> {
  const room = await host.makeRoom();
  const others = await open(room);
  for (const p of others) p.background(() => p.getReady());
  await host.getReady(others.length + 1);
  await host.waitForGame();
  await server.waitFor(new RegExp(`^\\[${room}\\] ------- Game Initialization -------$`), 120_000);
  return room;
}

/** Waits until the room's game has each player in it, past loading. */
export async function inGame(server: Server, room: string, ...names: string[]): Promise<void> {
  for (const name of names) {
    await server.poll(`${name} is in room ${room}'s game`, 300_000, () => server.inGame(room, name));
  }
}

/**
 * Kino's spawn points (initial_spawn_points in its entity string), where _zombiemode.gsc puts its players: the one a
 * player spawned on, within 4 units, or undefined.
 */
export function kinoSpawnPoint(p: PlayerState): number[] | undefined {
  return [[-101, -1256], [-91, -1331], [76, -1343], [100, -1240]]
    .find(([x, y]) => (p.origin[0] - x) ** 2 + (p.origin[1] - y) ** 2 <= 16);
}

/** The game is on Kino, which some tests play alone. */
export const onKino = map === 'zombie_theater';
