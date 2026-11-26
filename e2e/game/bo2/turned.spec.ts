// A room of Turned on Black Ops II: two players, each from a PC of their own (t6.ts), on Diner. Everyone starts as
// a zombie, and the first to pick up the cure is the human: Ann, whom the test moves onto it (steer.gsc), picks it up
// as a player does. When the clock runs out, the game makes the player with the most points the winner, which the
// result says. A game of Turned counts no rounds, has no teams, and is on no leaderboard.
import { expect, test } from '../fixtures';
import { gameOver, makeRoom, noRecords, playGame, steerReport } from '../t6';

test.use({ serverOptions: { t6: true } });
test.skip(!process.env.ZOMBIES_TEST_T6, 'no Black Ops II game files (ZOMBIES_T6 in .env)');

test('in a room of Turned, the player who takes the cure is the human, and with the most points wins',
  async ({ server, t6 }) => {
    const players = await Promise.all([t6.player('Ann'), t6.player('Bob')]);
    const [ann, bob] = players;
    const room = await makeRoom(server, players, 'zm_cleansed_diner');
    expect((await bob.view()).room?.members.filter((m) => m.team), 'no teams in Turned').toEqual([]);
    // everyone a zombie at the start, once the level has set them up
    await playGame(server, room, players, (rows) => rows.every((p) => p.zombie));

    const over = server.mark();
    await server.console(`${room} set steer_cure Ann`);
    await server.waitFor(new RegExp(`^\\[${room}\\] zombies_steer: Ann is at the cure$`), 10_000, over);
    await server.poll('Ann picked up the cure', 30_000, async () => {
      const { rows } = await steerReport(server, room);
      const human = rows.find((p) => !p.zombie);
      return human?.name === 'Ann' && human.score > rows.find((p) => p.name === 'Bob')!.score;
    });
    await ann.shot('human');
    await server.console(`${room} set steer_timeup 1`);
    await gameOver(server, room, '1', over);
    const result = (await bob.view()).room?.result;
    expect(result?.round, 'no round in Turned').toBeUndefined();
    expect(result?.players.map((p) => `${p.name} ${p.won ? 'won' : 'lost'}`), "the result's players")
      .toEqual(['Ann won', 'Bob lost']);
    expect(result?.players.filter((p) => p.team), 'no teams in Turned').toEqual([]);
    await noRecords(server, ann, 'zm_cleansed_');
    await server.waitFor(new RegExp(`^server: room ${room}'s game ended: quit$`), 60_000, over);
  });
