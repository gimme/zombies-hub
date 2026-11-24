// Two Black Ops II players play a room's game to game over, each from a PC of their own (t6.ts: their page and their
// game at once). They make a room on Town and join it, and say they are ready. Their games join the room's game in the
// order of their places, through the server's port. With the zombies away (steer.gsc), they go down together when the
// test says, which is game over in round 1: the room has the game's result, and the round is on the leaderboards of
// two players.
import { expect, test } from '../fixtures';
import { gameOver, makeRoom, playGame, t6Boards } from '../t6';

test.use({ serverOptions: { t6: true } });
test.skip(!process.env.ZOMBIES_TEST_T6, 'no Black Ops II game files (game/t6)');

test("two players play a room's game of Black Ops II to game over", async ({ server, t6 }) => {
  const players = await Promise.all([t6.player('Ann'), t6.player('Bob')]);
  const [ann] = players;
  const room = await makeRoom(server, players, 'zm_standard_town');
  const { from } = await playGame(server, room, players, (rows) => rows.every((p) => !p.down));
  const joined = server.all(new RegExp(`^relay: .* \\((Ann|Bob), room ${room}\\) joined over UDP, as `), from);
  expect(joined.map((l) => /\((\w+), room/.exec(l)![1]), 'the players joined in the order of their places')
    .toEqual(['Ann', 'Bob']);
  await ann.shot('joined');

  const over = server.mark();
  await server.console(`${room} set steer_down all`);
  await server.waitFor(new RegExp(`^\\[${room}\\] zombies_steer: downed 2 of all$`), 10_000, over);
  await gameOver(server, room, '1', over);
  const result = (await ann.view()).room?.result;
  expect(result?.map, "the result's map").toBe('zm_standard_town');
  expect(result?.round, "the result's round").toBe(1);
  expect(result?.players.map((p) => p.name).sort(), "the result's players").toEqual(['Ann', 'Bob']);
  expect(result?.players.filter((p) => p.won || p.team), 'no winner and no teams in co-op').toEqual([]);
  const town = (await t6Boards(server, ann)).find((m) => m.map === 'zm_standard_town');
  expect(town?.duo.map((r) => r.name).sort(), "Town's board of two players").toEqual(['Ann', 'Bob']);
  // the end plays out, and the server stops the game
  await server.waitFor(new RegExp(`^server: room ${room}'s game ended: quit$`), 60_000, over);
});
