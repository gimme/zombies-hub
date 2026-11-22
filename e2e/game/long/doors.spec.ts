// A door a player buys counts for them in the game's report, which nothing in the game keeps: the level's scripts
// tell their black box of each buy, and the game counts the doors' (bo1-zombies/src/server_mp/sv_lobby_mp.cpp:
// SV_Lobby_BlackBox). On Kino, whose cheapest doors cost 750, where a player starts with 500: Dan, the room's host, and
// Dot stand where they spawned, unhurt (god: the test has cheats), and Dan knifes the zombies that come for the two,
// 130 points each on round 1, until the game's status says he has a door's points. Then he goes to the foyer's lower
// door, on its lobby side, looks at it and presses use: its cost is gone from his points. At game over (the console's
// fast_restart) the game's report counts one door for Dan and none for Dot, and each one's page shows them so. Some
// two minutes.
import { expect, type Player, type PlayerState, test } from '../fixtures';
import { inGame, loadingMovie, onKino, startRoomGame } from '../play';

const sleep = (ms: number) => new Promise((done) => setTimeout(done, ms));
const at = (p: PlayerState) => `${p.origin.join(' ')}, looking ${p.yaw} ${p.pitch}`;

// the foyer's lower door, and where a player stands to buy it (z is the eye's: the feet are 60 below)
const DOOR = [-580, -641, 133];
const STAND = [-528, -641, 170];

// The report the page shows at game over: its column of the doors each player bought, as "Dan 1, Dot 0".
async function doorBuys(p: Player): Promise<string> {
  await expect(p.page.locator('#results'), 'the page shows how the game went').toBeVisible({ timeout: 120_000 });
  const report = await p.page.evaluate(() => ({
    heads: [...document.querySelectorAll('#results thead th')].map((th) => th.textContent),
    rows: [...document.querySelectorAll<HTMLTableRowElement>('#resultrows tr')].map((tr) => ({
      name: [...tr.querySelector('.player')!.childNodes].filter((n) => n.nodeType === 3).map((n) => n.textContent)
        .join(''),
      cells: [...tr.cells].map((td) => td.textContent),
    })),
  }));
  const column = report.heads.indexOf('Door buys');
  expect(column, `the report's column of door buys, of ${report.heads.join(', ')}`).toBeGreaterThanOrEqual(0);
  await p.shot('door-report');
  return report.rows.map((r) => `${r.name} ${r.cells[column]}`).join(', ');
}

test.skip(!onKino, 'the doors test has been played on Kino alone');

test('a door a player buys counts for them in the report', async ({ server, players }) => {
  const watch = (p: Player) => {
    p.background(() => loadingMovie(p, 'room'));
    return p;
  };
  const dan = watch(await players.open('Dan'));
  let dot!: Player;
  const room = await startRoomGame(server, dan, async (code) => [dot = watch(await players.open('Dot', {
    query: `room=${code}` }))]);
  await inGame(server, room, 'Dan', 'Dot');
  await server.waitFor(new RegExp(`^\\[${room}\\] bo1_lobby: start$`), 150_000);
  for (const p of [dan, dot]) {
    const spawn = await p.spawned();
    p.note(`spawned at ${spawn.origin.join(' ')} with ${spawn.weapon}`);
  }
  await sleep(3000); // the weapons come up
  for (const p of [dan, dot]) await p.command('god');
  const points = async () => (await server.rowOf(room, 'Dan'))?.points ?? 0;

  // Dan turns to the zombie closest on screen (setviewpos), or a step round with none in reach, and swings, until he
  // has a door's points: the screen is some 80 degrees across, and a yaw grows to the left
  let rich = false;
  const knifing = (async () => {
    const turnTo = (p: PlayerState, yaw: number) =>
      dan.command(`setviewpos ${p.origin[0]} ${p.origin[1]} ${p.origin[2] + 62} ${yaw.toFixed(1)} 0`);
    let swings = 0;
    for (const began = Date.now(); !rich;) {
      if (Date.now() - began > 900_000) {
        throw new Error(`${swings} swings of the knife in 15 min earned no door's points`);
      }
      const p = await dan.state();
      if (p.pmType !== 0) throw new Error(`the zombies downed Dan, whom god was to keep unhurt: pm_type ${p.pmType}`);
      const reach = p.aimTargets > 0 && p.closest.distance <= 96;
      if (!reach) await turnTo(p, p.yaw + 45);
      else if (Math.abs(p.closest.x) > 0.15) await turnTo(p, p.yaw - Math.atan(p.closest.x * 0.85) * 180 / Math.PI);
      if (!reach || Math.abs(p.closest.x) > 0.15) await dan.afterFrames(2, 300);
      await dan.command('+melee');
      await dan.afterFrames(2, 400);
      await dan.command('-melee');
      await dan.afterFrames(2, 600); // the swing ends
      if (++swings % 10 === 0) dan.note(`${swings} swings of the knife, the closest zombie ${p.closest.distance} ` +
        `units off`);
    }
  })();
  const had = await server.poll("Dan has a door's 750 points, of the zombies he knifed", 900_000, async () => {
    const now = await points();
    return now >= 750 && now;
  });
  rich = true;
  await knifing;
  dan.note(`has ${had} points`);

  // to the door, as the wall buy's players go to their wall
  await dan.command(`setviewpos ${STAND.join(' ')} 180 0`);
  await dan.until(20, (q) => Math.hypot(q.origin[0] - STAND[0], q.origin[1] - STAND[1]) < 32);
  await sleep(1000); // he comes to rest on the floor
  const p = await dan.state();
  const across = Math.hypot(DOOR[0] - p.origin[0], DOOR[1] - p.origin[1]);
  const pitch = Math.atan2(p.origin[2] + 60 - DOOR[2], across) * 180 / Math.PI;
  const yaw = Math.atan2(DOOR[1] - p.origin[1], DOOR[0] - p.origin[0]) * 180 / Math.PI;
  await dan.command(`setviewpos ${p.origin[0]} ${p.origin[1]} ${p.origin[2] + 62} ${yaw.toFixed(1)} ` +
    `${pitch.toFixed(1)}`);
  await dan.until(15, (q) => Math.abs(((q.yaw - yaw + 540) % 360) - 180) < 5);
  await dan.shot('door');
  await dan.command('+activate');
  await sleep(1500);
  await dan.command('-activate');
  dan.note(`pressed use at the door, at ${at(await dan.state())}`);
  // a swing under way as he was told may have earned him one more kill's 130 before he went
  await server.poll("the door's 750 are gone from Dan's points", 30_000, async () => (await points()) <= had - 600);

  const seat = (name: string) => server.seatOf(room, name)!.replace(/\./g, '\\.');
  const over = server.mark();
  await server.console(`${room} fast_restart`);
  await server.waitFor(new RegExp(`^server: room ${room}'s game ended: quit$`), 60_000, over);
  expect(server.find(new RegExp(`^\\[${room}\\] bo1_lobby: stats ${seat('Dan')}:28960( -?[0-9]+){6} 1$`), over),
    "the report's one door for Dan").toBeTruthy();
  expect(server.find(new RegExp(`^\\[${room}\\] bo1_lobby: stats ${seat('Dot')}:28960( -?[0-9]+){6} 0$`), over),
    "the report's no door for Dot").toBeTruthy();
  expect(await doorBuys(dan), "the door buys Dan's page shows").toBe('Dan 1, Dot 0');
  expect(await doorBuys(dot), "the door buys Dot's page shows").toBe('Dan 1, Dot 0');
});
