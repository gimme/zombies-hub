// A room's mods. Ann, its host, turns Unique Wall Weapons and Grief on in the Mods tab and sets Zombie Cap to 32. Their
// game has the cap on its command line, and its log says each mod rewrote its scripts: the wall buy's, Grief's 23
// places on Kino, and the zombies at once. No zombie spawns. A key over the pregame menu takes Ann in. On Kino, Ann
// buys the Olympia off the wall; Bob, at it, has no prompt and keeps his pistol; Ann gives it up, and Bob buys it.
import { expect, type Player, test } from '../fixtures';
import { inGame, loadingMovie, onKino } from '../play';

const sleep = (ms: number) => new Promise((done) => setTimeout(done, ms));

// Kino's Olympia off the wall by the spawn, and where a player stands to buy it (z is the eye's: the feet are 60 below)
const OLYMPIA = 'rottweil72_zm';
const WALL = [-390, -1265, 136];
const STAND = [-390, -1243, 150];

// The player goes to the wall buy (setviewpos: the test has cheats), looks at it and holds use: what they hold after
// (a buy shows within a few frames; a refusal is the weapon in hand still, 10 s on).
async function buyOffWall(p: Player, step: string): Promise<string> {
  await p.command(`setviewpos ${STAND.join(' ')} 270 0`);
  await p.until(20, (q) => Math.hypot(q.origin[0] - STAND[0], q.origin[1] - STAND[1]) < 32);
  await sleep(1000); // they come to rest on the floor
  const at = await p.state();
  const across = Math.hypot(WALL[0] - at.origin[0], WALL[1] - at.origin[1]);
  const pitch = Math.atan2(at.origin[2] + 60 - WALL[2], across) * 180 / Math.PI;
  const yaw = Math.atan2(WALL[1] - at.origin[1], WALL[0] - at.origin[0]) * 180 / Math.PI;
  await p.command(`setviewpos ${at.origin[0]} ${at.origin[1]} ${at.origin[2] + 62} ${yaw.toFixed(1)} ` +
    `${pitch.toFixed(1)}`);
  await p.until(15, (q) => Math.abs(((q.yaw - yaw + 540) % 360) - 180) < 5);
  p.note(`at the wall buy, at ${at.origin.join(' ')}, looking ${yaw.toFixed(1)} ${pitch.toFixed(1)}`);
  await p.shot(`wall-${step}`);
  await p.command('+activate');
  await sleep(1500);
  await p.command('-activate');
  const after = await p.until(10, (q) => q.weapon !== at.weapon);
  return (after || (await p.state())).weapon;
}

test.skip(!onKino, "the mods test plays Kino's wall buy");

test("a room's mods rewrite the level's scripts, and one player at a time holds a wall weapon", async ({ server,
  players }) => {
  const ann = await players.open('Ann');
  ann.background(() => loadingMovie(ann, 'key'));
  const room = await ann.makeRoom();
  await ann.page.click('#tabmods');
  for (const control of ['.mod:nth-child(1) .switch', '.mod:nth-child(2) .switch',
    '.mod:nth-child(3) .seg button:nth-child(2)']) {
    await ann.page.click('#panelmods ' + control);
  }
  await expect(ann.page.locator('#modcount'), "the mods Ann set").toHaveText('3');
  const bob = await players.open('Bob', { query: `room=${room}` });
  bob.background(() => loadingMovie(bob, 'room'));
  bob.background(() => bob.getReady());
  await ann.getReady(2);
  await ann.waitForGame();
  await server.waitFor(new RegExp(`^\\[${room}\\] ------- Game Initialization -------$`), 120_000);
  await ann.waitFor(/^test: a key over the pregame menu captured the mouse and went full screen/, 120_000);
  for (const part of ['bo1_wall_taken', "decide_hide_show_hint's prompt", "weapon_spawn_think's purchase"]) {
    await server.waitFor(new RegExp(`^\\[${room}\\] bo1_mod_uniquewallweapons: maps/_zombiemode_weapons\\.gsc: ` +
      `${part} rewritten$`), 60_000);
  }
  await server.poll("the game rewrote the scripts in Grief's 23 places", 60_000, async () =>
    server.all(new RegExp(`^\\[${room}\\] bo1_mod_grief: maps/[a-z_]+\\.gsc: .* rewritten$`)).length === 23);
  await server.waitFor(new RegExp(`^\\[${room}\\] bo1_mod_zombiecap: maps/_zombiemode\\.gsc: the zombies at once ` +
    'rewritten$'), 60_000);
  await server.poll(`room ${room}'s game has Zombie Cap at 32`, 30_000, () =>
    server.gameHas(room, 'bo1_mod_zombiecap', '32'));
  expect(server.find(new RegExp(`^\\[${room}\\] bo1_mod_[a-z]+: .* not found, left as is$`)),
    'a script the game left as it was, for a mod').toBeUndefined();
  await server.noZombies(room);
  await inGame(server, room, 'Ann', 'Bob');

  const [annHad, bobHad] = await Promise.all([ann.spawned(), bob.spawned()]);
  await sleep(3000); // the weapons come up
  expect(await buyOffWall(ann, 'buy'), 'what Ann holds after her buy off the wall').toBe(OLYMPIA);
  await ann.command('setviewpos -101 -1256 170 61 0');
  await sleep(2000);
  expect(await buyOffWall(bob, 'held'), 'what Bob holds after his buy, Ann holding the gun').toBe(bobHad.weapon);
  await ann.command('take weapons'); // `take <weapon>` leaves it the current weapon, which bo1_player says
  const gaveUp = await ann.until(10, (q) => q.weapon !== OLYMPIA);
  expect(gaveUp?.weapon, `what Ann holds after take weapons (she had ${annHad.weapon})`).not.toBe(OLYMPIA);
  await sleep(1000); // the prompt is back within 0.1 s of the game's time
  expect(await buyOffWall(bob, 'free'), 'what Bob holds after his buy, the gun free').toBe(OLYMPIA);
  await server.console(`${room} quit`);
});
