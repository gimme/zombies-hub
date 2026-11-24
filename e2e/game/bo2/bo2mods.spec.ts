// A Black Ops II room's mods, which the server's script in the room's game implements (server/t6.go: t6Script). Two
// players play a room's game on Town with Zombie Cap at 32 and Unique Wall Weapons on. The level then lets 32 zombies
// be alive at once. Ann gets a gun off the wall, and both stand at its wall buy (steer.gsc): the script hides the wall
// buy's prompt from Bob, and not from Ann. The game's result names the mods.
//
// On Origins, a generator's capture keeps some of the zombies at once for itself. With Zombie Cap at 32, the map
// counts them down from 32 the moment it sets its limit: the script has taken the place of the map's function.
import { expect, test } from '../fixtures';
import { gameOver, makeRoom, playGame } from '../t6';

test.use({ serverOptions: { t6: true } });
test.skip(!process.env.ZOMBIES_TEST_T6, 'no Black Ops II game files (game/t6)');

test("a room's game of Black Ops II has the room's mods", async ({ server, t6 }) => {
  const mods = ['uniquewallweapons', 'zombiecap=32'];
  const players = await Promise.all([t6.player('Ann'), t6.player('Bob')]);
  const [ann] = players;
  const room = await makeRoom(server, players, 'zm_standard_town', { mods });
  expect(await ann.say('room', { mods: ['grief'] }), 'no Grief on in a room of Black Ops II, where it is a mode')
    .toContain("Black Ops II games can't have Grief.");
  const { from } = await playGame(server, room, players, (rows) => rows.every((p) => !p.down));
  for (const mod of ['uniquewallweapons 1', 'zombiecap 32']) {
    expect(server.all(new RegExp(`^\\[${room}\\] zombies_lobby: mod ${mod}$`), from), `the game has ${mod}`)
      .toHaveLength(1);
  }

  await server.console(`${room} set steer_limit 1`);
  await server.waitFor(new RegExp(`^\\[${room}\\] zombies_steer: the zombies at once are 32$`), 10_000, from);

  await server.console(`${room} set steer_wall Ann`);
  const has = await server.waitFor(new RegExp(`^\\[${room}\\] zombies_steer: Ann has \\S+ off the wall$`), 10_000,
    from);
  const weapon = / has (\S+) off the wall$/.exec(has)![1];
  await server.waitFor(new RegExp(`^\\[${room}\\] zombies_lobby: wall ${weapon} Bob$`), 30_000, from);
  await ann.shot('wall');
  expect(server.all(new RegExp(`^\\[${room}\\] zombies_lobby: wall ${weapon} Ann$`), from),
    'the wall buy is not hidden from Ann, who has its gun').toEqual([]);

  const over = server.mark();
  await server.console(`${room} set steer_down all`);
  await gameOver(server, room, '1', over);
  expect((await ann.view()).room?.result?.mods, "the result's mods").toEqual(mods);
});

test("on Origins, a generator's capture counts down from Zombie Cap's 32", async ({ server, t6 }) => {
  const players = await Promise.all([t6.player('Ann'), t6.player('Bob')]);
  const room = await makeRoom(server, players, 'zm_classic_tomb', { mods: ['zombiecap=32'] });
  const { from } = await playGame(server, room, players, (rows) => rows.every((p) => !p.down));
  await server.console(`${room} set steer_limit 1`);
  await server.waitFor(new RegExp(`^\\[${room}\\] zombies_steer: the zombies at once are 32$`), 10_000, from);
  // no generator is being captured, so the capture's zombies are none
  await server.console(`${room} set steer_capture 1`);
  await server.waitFor(new RegExp(`^\\[${room}\\] zombies_steer: a capture leaves the round 32 zombies at once$`),
    10_000, from);
  // and the limit stays there: the script does not take the map's value for one to add to
  await new Promise((done) => setTimeout(done, 1000));
  const after = server.mark();
  await server.console(`${room} set steer_limit 1`);
  await server.waitFor(new RegExp(`^\\[${room}\\] zombies_steer: the zombies at once are 32$`), 10_000, after);

  const over = server.mark();
  await server.console(`${room} set steer_down all`);
  await gameOver(server, room, '1', over);
});
