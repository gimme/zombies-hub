// Two players stand still in a room's game, zombies and all, until the zombies have them both: the level's own game
// over, which the console's fast_restart (the other tests') doesn't come to. Its end scoreboard says Esc leaves it.
import { expect, map, type Player, test } from '../fixtures';
import { inGame, loadingMovie, outro, startRoomGame } from '../play';

// Game over's end scoreboard (the level's intermission), whose foot says Esc leaves it (bo1-zombies/src/cgame/
// cg_sp_hud.cpp: CG_SP_DrawIntermission), which the engine says as it first shows. The player presses it at once:
// their game ends, saying it was game over (bo1-zombies/src/client_mp/cl_main_mp.cpp: CL_LeaveGameOver), and the page
// is back in the room, which shows how the game went (server/rooms.go: the game's result, said as the end scoreboard
// began), whether or not the game runs on for the others: the room's state says which (ending, or open).
async function leaveEndScoreboard(p: Player): Promise<string> {
  await p.waitFor(/^Game over's end scoreboard shows: /, 1_200_000);
  await p.shot('end-scoreboard');
  const ended = p.next(/^lobby: the game ended \(over\): You left the end scoreboard\.$/, 30_000);
  const back = p.next(/^lobby: back in the lobby, the game's engine stopped$/, 45_000);
  await p.escape();
  expect(await ended, 'Esc on the end scoreboard ended the game, saying it was game over').toBeTruthy();
  expect(await back, 'the page came back to the room').toBeTruthy();
  await expect(p.page.locator('#results'), 'the room shows how the game went').toBeVisible({ timeout: 10_000 });
  const state = (await p.roomState()).toLowerCase();
  p.note(`Esc left the end scoreboard, and the room shows how the game went, its game ${state}`);
  await p.shot('left-over');
  return state;
}

// the loading movie and Call of the Dead's outro, checked beside what the player does
function watch(p: Player): Player {
  p.background(() => loadingMovie(p, 'room'));
  p.background(() => outro(p, false));
  return p;
}

test('both players leave the end scoreboard, which ends the game, and the lobby keeps their rounds', async ({ server,
  players }) => {
  const from = server.mark();
  const lea = watch(await players.open('Lea'));
  let ola!: Player;
  const room = await startRoomGame(server, lea, async (code) => [ola = watch(await players.open('Ola', {
    query: `room=${code}` }))]);
  // on Ascension, its easter egg's stat compiled in (s_scrSourceFixes)
  if (map === 'zombie_cosmodrome') {
    await server.waitFor(new RegExp(`^\\[${room}\\] scripts: maps/zombie_cosmodrome_eggs\\.gsc: reward_wait's stat ` +
      'for the easter egg rewritten$'), 30_000);
  }
  await inGame(server, room, 'Lea', 'Ola');
  // round 1 waits for the map's loading movie, which they don't skip: Call of the Dead's is two minutes
  await server.waitFor(new RegExp(`^server: Lea's best on ${map} with a party of 2: round 1$`), 180_000, from);
  expect(server.find(new RegExp(`^server: room ${room}'s game is over$`), from),
    'game over before round 1 was said').toBeUndefined();
  await server.waitFor(new RegExp(`^server: room ${room}'s game is over$`), 540_000, from);
  expect(await leaveEndScoreboard(lea), "the room's state once Lea left").toMatch(/^(ending|open)$/);
  expect(await leaveEndScoreboard(ola), "the room's state once Ola left").toMatch(/^(ending|open)$/);
  await server.waitFor(new RegExp(`^server: room ${room}'s game ended: quit$`), 30_000, from);
  expect(server.find(new RegExp(`^\\[${room}\\] Game over: everyone has left the end scoreboard$`), from),
    'the game ended as everyone had left its end scoreboard').toBeTruthy();
  for (const p of [lea, ola]) {
    expect(p.find(/Playing cinematic zombie_coast_outro/), "Call of the Dead's outro, for a player who left the end " +
      'scoreboard').toBeUndefined();
  }
  // going down cost one of them points, of the 500 they earned, though the end scoreboard has the 500 by now
  expect(server.find(new RegExp(`^\\[${room}\\] bo1_lobby: stats [0-9.]+:28960 500 ([0-9]{1,2}|[0-4][0-9]{2})` +
    '( [0-9]+){5}$'), from), "the report keeps what a player had left to spend").toBeTruthy();
  // the lobby kept each one's round as a duo's: as round 1 began, and at game over if it was a later one
  for (const name of ['Lea', 'Ola']) {
    await server.waitFor(new RegExp(`^server: ${name}'s best on ${map} with a party of 2: round [0-9]+$`), 10_000,
      from);
  }
});

test('one player leaves the end scoreboard, and the other watches it to its end', async ({ server, players }) => {
  const from = server.mark();
  const gil = watch(await players.open('Gil'));
  let gus!: Player;
  const room = await startRoomGame(server, gil, async (code) => [gus = watch(await players.open('Gus', {
    query: `room=${code}` }))]);
  await inGame(server, room, 'Gil', 'Gus');
  await server.waitFor(new RegExp(`^server: room ${room}'s game is over$`), 540_000, from);
  // The room shows Gil how the game went as it runs on for Gus, who watches it to its end, some 18 s: Gus's page
  // waits for the game, though the room has no seat for Gus in a game that is ending, and Gus's game ends as the
  // level exits, saying it was game over; on Call of the Dead, its outro rolls first.
  expect(await leaveEndScoreboard(gil), "the room's state once Gil left").toBe('ending');
  await gus.waitFor(/^lobby: the game ended \(over\): .*BO1_GAMEOVER$/, 90_000);
  expect(gus.find(/^lobby: the game ended \(left\)/), "Gus's page left the game before its end scoreboard was over")
    .toBeUndefined();
  expect(server.find(new RegExp(`^\\[${room}\\] Game over: everyone has left the end scoreboard$`), from),
    'the game ended before Gus had left its end scoreboard').toBeUndefined();
  if (map === 'zombie_coast') await gus.waitFor(/^test: the outro rolls$/, 1000);
  await gus.waitFor(/^lobby: how the game went: round [0-9]+, 2 players$/, 30_000);
});
