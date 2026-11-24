// A room of Grief on Black Ops II plays in two teams, and takes more players than the server's most (two here): three
// players, each from a PC of their own (t6.ts). The host picks Grief as the room's mode, which moves the room to a map
// of Grief, and sizes the room for three. The room shows the first two in team 1 and the third in team 2, and the game
// puts them in those teams. With the zombies away (steer.gsc), team 1 goes down when the test says, and once the round
// is over, the game makes team 2 the winner. The result says so, and Grief's games are on no leaderboard.
import { expect, test } from '../fixtures';
import { gameOver, makeRoom, noRecords, playGame } from '../t6';

const sleep = (ms: number) => new Promise((done) => setTimeout(done, ms));

test.use({ serverOptions: { t6: true, env: { ZOMBIES_MAX_PLAYERS: '2' } } });
test.skip(!process.env.ZOMBIES_TEST_T6, 'no Black Ops II game files (game/t6)');

test('a room of Grief plays in two teams, past the most players, and the team left standing wins',
  async ({ server, t6 }) => {
    const players = await Promise.all([t6.player('Ann'), t6.player('Bob'), t6.player('Cid')]);
    const [ann, , cid] = players;
    const room = await makeRoom(server, players, 'zm_standard_town', { versus: 'grief' }, { size: 3 });
    const view = (await cid.view()).room;
    expect(view?.map, "the room's map, of Grief").toBe('zm_grief_transit');
    expect(view?.maxSize, 'the most players a room of Grief takes').toBe(8);
    expect(view?.members.map((m) => `${m.name} ${m.team}`), "the room's teams")
      .toEqual(['Ann 1', 'Bob 1', 'Cid 2']);

    const { rows } = await playGame(server, room, players);
    expect(await server.console(`${room} sv_maxclients`), "the game's slots").toMatch(/"sv_maxclients" is: "8\^7"/);
    await cid.shot('joined');
    expect(rows.map((p) => `${p.name} ${p.team}`).sort(), "the game's teams").toEqual(['Ann A', 'Bob A', 'Cid B']);

    // team 1 (A) goes down, and team 2 (B) is left standing at the round's end
    const over = server.mark();
    await server.console(`${room} set steer_down A`);
    await server.waitFor(new RegExp(`^\\[${room}\\] zombies_steer: downed 2 of A$`), 10_000, over);
    await sleep(3000); // Grief sees that team A is down, and waits for the round's end
    await server.console(`${room} set steer_clear 1`);
    await gameOver(server, room, '1 2', over);
    const result = (await ann.view()).room?.result?.players.map((p) => `${p.name} ${p.team} ${p.won ? 'won' : 'lost'}`);
    expect(result?.[0], 'the winner, first').toBe('Cid 2 won');
    expect(result?.slice(1).sort(), 'the rest').toEqual(['Ann 1 lost', 'Bob 1 lost']);
    await noRecords(server, ann, 'zm_grief_');
    await server.waitFor(new RegExp(`^server: room ${room}'s game ended: quit$`), 60_000, over);
  });
