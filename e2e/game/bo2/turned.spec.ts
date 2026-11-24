// A room of Turned on Black Ops II: two players, each from a PC of their own (t6.ts), on Diner. Everyone starts as
// a zombie, and the first to pick up the cure is the human: Ann, whom the test moves onto it (steer.gsc), picks it up
// as a player does. When the clock runs out, the game makes the player with the most points the winner, which the
// result says. A game of Turned counts no rounds, has no teams, and is on no leaderboard.
import { expect, test } from '../fixtures';
import { gameUp, reported, settled, steerReport, t6Boards } from '../t6';

test.use({ serverOptions: { t6: true } });

test('in a room of Turned, the player who takes the cure is the human, and with the most points wins',
  async ({ server, t6 }) => {
    test.skip(!process.env.ZOMBIES_TEST_T6, 'no Black Ops II game files (game/t6)');
    await t6.steer();
    const ann = await t6.player('Ann');
    const bob = await t6.player('Bob');
    expect((await ann.say('create', { map: 'zm_cleansed_diner', private: true })).trim(), 'Ann makes a room')
      .toBe('{}');
    const room = await server.poll('Ann made a room', 10_000, async () => server.roomOf('Ann'));
    expect((await bob.say('join', { room })).trim(), 'Bob joins it').toBe('{}');
    expect((await bob.view()).room?.members.filter((m) => m.team), 'no teams in Turned').toEqual([]);
    const from = server.mark();
    for (const player of [ann, bob]) {
      expect((await player.say('ready', { ready: true })).trim(), `${player.name} is ready`).toBe('{}');
    }

    await gameUp(server, room, from);
    await server.waitFor(new RegExp(`^server: room ${room}'s game has begun$`), 900_000, from);
    await settled(server, room, ['Ann', 'Bob']);
    // everyone a zombie at the start, once the level has set them up
    await reported(server, room, ['Ann', 'Bob'], (rows) => rows.every((p) => p.zombie));

    const over = server.mark();
    await server.console(`${room} set steer_cure Ann`);
    await server.waitFor(new RegExp(`^\\[${room}\\] zombies_steer: Ann is at the cure$`), 10_000, over);
    await server.poll('Ann picked up the cure', 30_000, async () => {
      const players = await steerReport(server, room);
      const human = players.find((p) => !p.zombie);
      return human?.name === 'Ann' && human.score > players.find((p) => p.name === 'Bob')!.score;
    });
    await ann.shot('human');
    await server.console(`${room} set steer_timeup 1`);
    await server.waitFor(new RegExp(`^\\[${room}\\] zombies_lobby: over 1$`), 60_000, over);
    await server.waitFor(new RegExp(`^server: room ${room}'s game is over$`), 10_000, over);
    const result = (await bob.view()).room?.result;
    expect(result?.round, 'no round in Turned').toBeUndefined();
    expect(result?.players.map((p) => `${p.name} ${p.won ? 'won' : 'lost'}`), "the result's players")
      .toEqual(['Ann won', 'Bob lost']);
    expect(result?.players.filter((p) => p.team), 'no teams in Turned').toEqual([]);
    const boards = await t6Boards(server, ann);
    expect(boards.filter((m) => m.map.startsWith('zm_cleansed_')), 'no leaderboards of Turned').toEqual([]);
    expect(boards.flatMap((m) => [...m.duo, ...m.squad, ...m.bigSquad]), 'no records').toEqual([]);
    await server.waitFor(new RegExp(`^server: room ${room}'s game ended: quit$`), 60_000, over);
  });
