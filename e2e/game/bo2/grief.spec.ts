// A room of Grief on Black Ops II plays in two teams, and takes more players than the server's most (two here): three
// players, each from a PC of their own (t6.ts). The host picks Grief as the room's mode, which moves the room to a map
// of Grief, and sizes the room for three. The room shows the first two in team 1 and the third in team 2, and the game
// puts them in those teams. With the zombies away (steer.gsc), team 1 goes down when the test says, and once the round
// is over, the game makes team 2 the winner. The result says so, and Grief's games are on no leaderboard.
import { expect, test } from '../fixtures';
import { gameUp, reported, settled, t6Boards } from '../t6';

const sleep = (ms: number) => new Promise((done) => setTimeout(done, ms));

test.use({ serverOptions: { t6: true, env: { ZOMBIES_MAX_PLAYERS: '2' } } });

test('a room of Grief plays in two teams, past the most players, and the team left standing wins',
  async ({ server, t6 }) => {
    test.skip(!process.env.ZOMBIES_TEST_T6, 'no Black Ops II game files (game/t6)');
    await t6.steer();
    const ann = await t6.player('Ann');
    const bob = await t6.player('Bob');
    const cid = await t6.player('Cid');
    expect((await ann.say('create', { map: 'zm_standard_town', private: true })).trim(), 'Ann makes a room')
      .toBe('{}');
    expect((await ann.say('room', { versus: 'grief' })).trim(), 'Ann picks Grief').toBe('{}');
    expect((await ann.say('room', { size: 3 })).trim(), 'Ann sizes the room for three, past the most').toBe('{}');
    const room = await server.poll('Ann made a room', 10_000, async () => server.roomOf('Ann'));
    for (const player of [bob, cid]) {
      expect((await player.say('join', { room })).trim(), `${player.name} joins it`).toBe('{}');
    }
    const view = (await cid.view()).room;
    expect(view?.map, "the room's map, of Grief").toBe('zm_grief_transit');
    expect(view?.maxSize, 'the most players a room of Grief takes').toBe(8);
    expect(view?.members.map((m) => `${m.name} ${m.team}`), "the room's teams")
      .toEqual(['Ann 1', 'Bob 1', 'Cid 2']);
    const from = server.mark();
    for (const player of [ann, bob, cid]) {
      expect((await player.say('ready', { ready: true })).trim(), `${player.name} is ready`).toBe('{}');
    }

    await gameUp(server, room, from);
    expect(await server.console(`${room} sv_maxclients`), "the game's slots").toMatch(/"sv_maxclients" is: "8\^7"/);
    await server.waitFor(new RegExp(`^server: room ${room}'s game has begun$`), 900_000, from);
    await settled(server, room, ['Ann', 'Bob', 'Cid']);
    await cid.shot('joined');
    expect((await reported(server, room, ['Ann', 'Bob', 'Cid'])).map((p) => `${p.name} ${p.team}`).sort(),
      "the game's teams").toEqual(['Ann A', 'Bob A', 'Cid B']);

    // team 1 (A) goes down, and team 2 (B) is left standing at the round's end
    const over = server.mark();
    await server.console(`${room} set steer_down A`);
    await server.waitFor(new RegExp(`^\\[${room}\\] zombies_steer: downed 2 of A$`), 10_000, over);
    await sleep(3000); // Grief sees that team A is down, and waits for the round's end
    await server.console(`${room} set steer_clear 1`);
    await server.waitFor(new RegExp(`^\\[${room}\\] zombies_lobby: over 1 2$`), 60_000, over);
    await server.waitFor(new RegExp(`^server: room ${room}'s game is over$`), 10_000, over);
    const players = (await ann.view()).room?.result?.players
      .map((p) => `${p.name} ${p.team} ${p.won ? 'won' : 'lost'}`);
    expect(players?.[0], 'the winner, first').toBe('Cid 2 won');
    expect(players?.slice(1).sort(), 'the rest').toEqual(['Ann 1 lost', 'Bob 1 lost']);
    const boards = await t6Boards(server, ann);
    expect(boards.filter((m) => m.map.startsWith('zm_grief_')), 'no leaderboards of Grief').toEqual([]);
    expect(boards.flatMap((m) => [...m.duo, ...m.squad, ...m.bigSquad]), 'no records').toEqual([]);
    await server.waitFor(new RegExp(`^server: room ${room}'s game ended: quit$`), 60_000, over);
  });
