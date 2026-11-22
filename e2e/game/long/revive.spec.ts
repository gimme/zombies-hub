// Of two players in a room's game, one goes down, and the other revives them, which every game of more than one comes
// to. The level revives a player only by one who touches their revive trigger, faces them, and has a clear sight trace
// and bullet trace to them (maps/_laststand.gsc: can_revive). On Kino, with no zombies: Rae, the room's host, holds a
// grenade until it goes off, which downs her, last stand (pm_type 6, 7 while someone revives her), as Roy stands at
// his spawn point; Roy goes to her (setviewpos: the test has cheats), faces her, and holds use for the 3 s the revive
// takes, the level's syrette in hand. Rae is on her feet again, and walks. The game runs on with both in it, to game
// over (the console's fast_restart), whose report counts Rae's down and Roy's revive. A minute and a half.
import { expect, type Player, type PlayerState, test } from '../fixtures';
import { inGame, loadingMovie, onKino, startRoomGame } from '../play';

const sleep = (ms: number) => new Promise((done) => setTimeout(done, ms));
const at = (p: PlayerState) => `${p.origin.join(' ')}, looking ${p.yaw} ${p.pitch}`;

// how far from the one down Roy stands: in their revive trigger (75 units around them on a zombie map)
const NEAR = 48;

test.skip(!onKino, 'the revive test has been played on Kino alone');

test('a player down is revived by the other', async ({ server, players }) => {
  const watch = (p: Player) => {
    p.background(() => loadingMovie(p, 'room'));
    return p;
  };
  const rae = watch(await players.open('Rae'));
  let roy!: Player;
  const room = await startRoomGame(server, rae, async (code) => [roy = watch(await players.open('Roy', {
    query: `room=${code}` }))]);
  // no zombies: Roy alone would not keep them off the two for the time a revive takes
  await server.noZombies(room);
  await inGame(server, room, 'Rae', 'Roy');
  await server.waitFor(new RegExp(`^\\[${room}\\] bo1_lobby: start$`), 150_000);
  const from = server.mark();
  for (const p of [rae, roy]) {
    const spawn = await p.spawned();
    p.note(`spawned at ${spawn.origin.join(' ')} with ${spawn.weapon}`);
    await p.page.mouse.move(640, 360);
    await p.click();
    expect(await p.captured(true, 5000), `a click on ${p.name}'s game captured the mouse`).toBe(true);
  }

  let down: number[] = [];
  let up = false;
  const wentDown = (async () => {
    // The level gives each player their first two grenades once it has drawn round 1's number, some 10 s after they
    // are all in; a press with none does nothing. G is held up to 3 times, 30 s each, until it goes off in hand.
    await sleep(12_000);
    let p: PlayerState | null = null;
    for (let press = 1; ; press++) {
      await rae.page.keyboard.down('KeyG');
      p = await rae.until(30, (q) => q.pmType === 6);
      await rae.page.keyboard.up('KeyG');
      if (p && p.pmType === 6) break;
      if (press === 3) {
        throw new Error(`G held 3 times, 30 s each, did not down Rae: ${p ? `pm_type ${p.pmType}` : '-'}`);
      }
      await sleep(2000);
    }
    const downAt = Date.now();
    down = p.origin;
    rae.note(`went down at ${at(p)}, a grenade held until it went off`);
    await rae.shot('down');
    for (let reviving = false; p.pmType !== 0;) {
      await sleep(500);
      p = (await rae.ask()) || p;
      if (p.pmType === 7 && !reviving) {
        reviving = true;
        rae.note(`someone is reviving her, ${((Date.now() - downAt) / 1000).toFixed(0)} s after she went down`);
      }
      if (![0, 6, 7].includes(p.pmType)) throw new Error(`Rae bled out before anyone revived her: pm_type ${p.pmType}`);
      if (Date.now() - downAt > 90_000) {
        throw new Error(`down for 90 s, Rae was neither revived nor bled out: ${at(p)}`);
      }
    }
    up = true;
    rae.note(`revived ${((Date.now() - downAt) / 1000).toFixed(0)} s after going down: on her feet at ${at(p)}`);
    await rae.shot('revived');
    // on her feet, she walks: a key held until she is 32 units on, for up to 5 s, forward, or back where Roy stands in
    // the way
    const walk = async (key: string) => {
      const start = await rae.state();
      const gone = (q: PlayerState) => Math.hypot(q.origin[0] - start.origin[0], q.origin[1] - start.origin[1]);
      await rae.page.keyboard.down(key);
      let now = start;
      for (const until = Date.now() + 5000; Date.now() < until && gone(now) < 32;) now = await rae.afterFrames(2, 500);
      await rae.page.keyboard.up(key);
      await sleep(1000);
      return gone(await rae.state());
    };
    let walked = await walk('KeyW');
    if (walked < 16) walked = await walk('KeyS');
    expect(walked, 'units Rae walked once revived, with W, then S').toBeGreaterThanOrEqual(16);
  })();

  const revived = (async () => {
    while (!down.length) await sleep(500);
    const away = (p: PlayerState) => Math.hypot(down[0] - p.origin[0], down[1] - p.origin[1]);
    const bearing = (p: PlayerState) => Math.atan2(down[1] - p.origin[1], down[0] - p.origin[0]) * 180 / Math.PI;
    const off = (p: PlayerState, yaw = bearing(p)) => Math.abs(((p.yaw - yaw + 540) % 360) - 180);
    let p = await roy.state();
    roy.note(`the other player is down at ${down.join(' ')}, ${away(p).toFixed(0)} units away`);
    // to her: NEAR units short of her on the straight way there, turned to her. z is the eye's, 60 above the feet,
    // and 24 more, as the floor between two spawn points is not level: the player comes to rest on it
    const yaw = bearing(p);
    const stand = [down[0] - NEAR * Math.cos(yaw * Math.PI / 180), down[1] - NEAR * Math.sin(yaw * Math.PI / 180)];
    await roy.command(`setviewpos ${stand[0].toFixed(0)} ${stand[1].toFixed(0)} ${down[2] + 84} ${yaw.toFixed(1)} 0`);
    const there = (q: PlayerState) => Math.abs(away(q) - NEAR) < 8 && off(q) < 5;
    p = (await roy.until(20, there)) || p;
    await sleep(1000);
    p = await roy.state();
    if (!there(p)) {
      throw new Error(`setviewpos did not take Roy to Rae, turned to her: ${away(p).toFixed(0)} units away`);
    }
    const held = Date.now();
    let syrette = 0;
    await roy.page.keyboard.down('KeyE');
    try {
      while (!up) {
        p = (await roy.ask()) || p;
        if (p.weapon === 'syrette_sp' && !syrette) {
          syrette = Date.now();
          roy.note(`reviving her: the syrette came to hand ${((syrette - held) / 1000).toFixed(1)} s after use went ` +
            `down`);
          await roy.shot('reviving');
        }
        if (Date.now() - held > 30_000) {
          throw new Error(`use held for 30 s, ${away(p).toFixed(0)} units from Rae and ${off(p).toFixed(0)} degrees ` +
            `off facing her (with ${p.weapon}): ${syrette ? 'the revive began and did not end' : 'it never began'}`);
        }
        await sleep(250);
      }
    } finally {
      await roy.page.keyboard.up('KeyE');
    }
    roy.note(`revived her: use held for ${((Date.now() - held) / 1000).toFixed(1)} s`);
  })();
  await Promise.all([wentDown, revived]);

  // the game runs on, both in it; game over, whose report has each player's downs and revives
  // (bo1-zombies/src/server_mp/sv_lobby_mp.cpp: SV_Lobby_Stats)
  await server.poll(`room ${room}'s game runs on, Rae and Roy in it`, 30_000, async () =>
    (await server.inGame(room, 'Rae')) && server.inGame(room, 'Roy'));
  expect(server.find(new RegExp(`^server: room ${room}'s game (is over|ends|ended)`), from),
    'the game ended as Rae went down, or was revived').toBeUndefined();
  const seat = (name: string) => server.seatOf(room, name)!.replace(/\./g, '\\.');
  const over = server.mark();
  await server.console(`${room} fast_restart`);
  await server.waitFor(new RegExp(`^server: room ${room}'s game ended: quit$`), 60_000, over);
  expect(server.find(new RegExp(`^\\[${room}\\] bo1_lobby: stats ${seat('Rae')}:28960( -?[0-9]+){4} 1 0 0$`), over),
    "the report's one down, and no revive, for Rae").toBeTruthy();
  expect(server.find(new RegExp(`^\\[${room}\\] bo1_lobby: stats ${seat('Roy')}:28960( -?[0-9]+){4} 0 1 0$`), over),
    "the report's one revive, and no down, for Roy").toBeTruthy();
});
