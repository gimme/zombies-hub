// More players than the four the levels were made for: six (ZOMBIES_TEST_PLAYERS, 5 to 8), on a server whose rooms
// hold them (ZOMBIES_MAX_PLAYERS), on any map (docker/test-games.sh --map). Ann makes a room, sets its size to theirs,
// and the others join it, one at a time. Their game rewrites the level's scripts that go by a player's number
// (bo1-zombies/src/clientscript/cscr_parser.cpp: s_scrMorePlayers): all that the map compiles, each found. Each player
// spawns with a character's arms, the four characters' (Five's share one pair); past the fourth, a player has the
// character of the player 4 before them (on Moon, the order they connected in). Each spawns apart from the others. The
// zombies, which come from the start, have them all: game over. The game's report has them all, the lobby keeps their
// round as a big squad's, and no script failed in the game, whose errors it says (bo1_scripterrors). Some three
// minutes, and a GB or more of memory a browser: docker/test-games.sh runs it alone.
import { expect, map, type Player, test } from '../fixtures';
import { loadingMovie } from '../play';

const sleep = (ms: number) => new Promise((done) => setTimeout(done, ms));
const count = Number(process.env.ZOMBIES_TEST_PLAYERS || 6);
const names = ['Ann', 'Bob', 'Cy', 'Dee', 'Eve', 'Fay', 'Gil', 'Hal'].slice(0, count);

// what the game rewrites on every map, and on this one
const parts = [
  "maps/_zombiemode.gsc: onPlayerConnect's character number",
  "maps/_zombiemode.gsc: coop_player_spawn_placement's spawn points",
  "maps/_zombiemode.gsc: the starting points of a party's size",
  "maps/_zombiemode_powerups.gsc: watch_for_drop's starting points",
  "maps/_zombiemode_load.gsc: the character model's number",
  "maps/_zombiemode_load.gsc: player_set_viewmodel's character number",
  "maps/_loadout.gsc: give_model's character number",
  "maps/_zombiemode_spawner.gsc: track_players_intersection_tracker's two players in one place",
  "maps/_gameskill.gsc: getCoopValue's number of players",
  ...({
    zombie_theater: ["maps/zombie_theater_teleporter.gsc: get_array_spots' spots"],
    zombie_cod5_asylum: ["maps/zombie_cod5_asylum.gsc: spawn_point_override's spawn points"],
    zombie_cod5_sumpf: ["maps/zombie_cod5_sumpf.gsc: sumpf_player_spawn_placement's spawn points"],
    zombie_cod5_factory: ["maps/zombie_cod5_factory_teleporter.gsc: teleport_players' spots"],
    zombie_pentagon: ["maps/_zombiemode_ai_thief.gsc: the thief's health of four players or more"],
    zombie_cosmodrome: ["maps/zombie_cosmodrome_lander.gsc: lock_players_intro's seat",
      "maps/zombie_cosmodrome_lander.gsc: a rider's seat", "maps/zombie_cosmodrome_lander.gsc: a downed rider's seat"],
    zombie_temple: ["maps/zombie_temple_pack_a_punch.gsc: Pack-a-Punch's plates",
      "maps/zombie_temple_sq_skits.gsc: fail_skit's line of the group",
      "maps/zombie_temple_sq_skits.gsc: fail_skit's line of one player",
      "maps/zombie_temple_sq_skits.gsc: skit_interupt's speaker",
      "maps/zombie_temple_sq.gsc: get_variant_from_entity_num's character",
      "maps/zombie_temple_sq_brock.gsc: radio2_override's character"],
    zombie_moon: ["maps/zombie_moon.gsc: give_player_model_override's character",
      "maps/zombie_moon_teleporter.gsc: teleport_player_to_target's spot"],
  } as Record<string, string[]>)[map] || [],
];
const quote = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

test.use({ serverOptions: { env: { ZOMBIES_MAX_PLAYERS: String(count), ZOMBIES_LOAD_TIMEOUT: '600s',
  ZOMBIES_GAME_ARGS: '+set bo1_scripterrors 50' } } });

test(`${count} players play a room's game`, async ({ server, players }) => {
  expect(count, 'ZOMBIES_TEST_PLAYERS').toBeGreaterThanOrEqual(5);
  expect(count, 'ZOMBIES_TEST_PLAYERS').toBeLessThanOrEqual(8);
  const all: Player[] = [];
  const open = async (name: string, query?: string) => {
    const p = await players.open(name, { query });
    p.background(() => loadingMovie(p, 'room'));
    all.push(p);
    return p;
  };
  const ann = await open('Ann');
  const room = await ann.makeRoom();
  const from = server.mark();
  await ann.page.click(`#sizesetting .seg button[data-value="${count}"]`);
  await server.waitFor(new RegExp(`^server: room ${room} takes ${count} players$`), 10_000, from);
  // one at a time, so that they are numbered in this order (server/rooms.go: placesOf); each says they are ready once
  // all are in: one who joins after makes everyone say it again
  for (const name of names.slice(1)) {
    const p = await open(name, `room=${room}`);
    await p.waitFor(new RegExp(`^lobby: in room ${room}$`), 120_000);
  }
  for (const p of all.slice(1)) p.background(() => p.getReady(count));
  await ann.getReady(count);
  await ann.waitForGame(600_000);
  await server.waitFor(new RegExp(`^\\[${room}\\] ------- Game Initialization -------$`), 120_000, from);
  for (const part of parts) {
    await server.waitFor(new RegExp(`^\\[${room}\\] players past 4: ${quote(part)} rewritten$`), 30_000, from);
  }
  expect(server.find(new RegExp(`^\\[${room}\\] players past 4: .* not found, left as is$`), from),
    "a script of the level's the game left as it was, for a fifth player").toBeUndefined();

  // where each spawned, and their arms, 5 s after they spawned: the level moves each to their spawn point as they are
  // all in (_zombiemode.gsc: coop_player_spawn_placement)
  const spawns = await Promise.all(all.map(async (p) => {
    await p.spawned(p.page, 600);
    await sleep(5000);
    let s = null;
    for (let asked = 0; !s && asked < 12; asked++) s = await p.ask();
    if (!s) throw new Error(`bo1_player did not answer ${p.name}, asked for a minute`);
    p.note(`spawned at ${s.origin.join(' ')} with ${s.weapon}, arms ${s.arms}`);
    await p.shot('spawned');
    await p.page.keyboard.down('Tab');
    await sleep(3000);
    await p.shot('scores');
    await p.page.keyboard.up('Tab');
    return s;
  }));
  const arms = spawns.map((s) => s.arms);
  expect(arms.filter((a) => !a || a === 'none'), "players without a character's arms").toEqual([]);
  expect(new Set(arms).size, `characters' arms of the players: ` +
    `${arms.join(', ')}`).toBe(map === 'zombie_pentagon' ? 1 : 4);
  for (let i = 0; i < count; i++) {
    for (let j = i + 1; j < count; j++) {
      const [a, b] = [spawns[i].origin, spawns[j].origin];
      // a player is 30 units across: two closer than that stand in each other
      expect(Math.hypot(a[0] - b[0], a[1] - b[1]), `units between ${names[i]} and ${names[j]} as they spawned`)
        .toBeGreaterThanOrEqual(32);
      if (map !== 'zombie_moon' && j - i === 4) expect(arms[j], `${names[j]}'s arms, ${names[i]}'s`).toBe(arms[i]);
    }
  }

  await server.waitFor(new RegExp(`^server: room ${room}'s game is over$`), 1_200_000, from);
  // their end scoreboard to its end: their game ends with the level, or, as Call of the Dead's outro takes minutes
  // here, as the page gives up on it once the room's game has ended
  for (const p of all) {
    await p.waitFor(/^lobby: the game ended \((over\): .*BO1_GAMEOVER|left\): The game ended\.)$/, 300_000);
  }
  await server.waitFor(new RegExp(`^server: room ${room}'s game ended: `), 30_000, from);
  // a line for each player as round 1 begins, and again at game over
  const reported = new Set(server.all(new RegExp(`^\\[${room}\\] bo1_lobby: stats [0-9.]+:28960( -?[0-9]+){7}$`), from)
    .map((l) => l.split(' ')[3]));
  expect(reported.size, "players in the game's report").toBe(count);
  expect(server.find(new RegExp(`^\\[${room}\\] bo1_lobby: over [0-9]+$`), from), 'the report says game over')
    .toBeTruthy();
  await ann.waitFor(new RegExp(`^lobby: how the game went: round [0-9]+, ${count} players$`), 30_000);
  for (const name of names) {
    expect(server.find(new RegExp(`^server: ${name}'s best on ${map} with a party of ${count}: round [0-9]+$`), from),
      `the round the lobby kept of ${name}'s, as a party of ${count}`).toBeTruthy();
  }
  const failed = server.find(new RegExp(`^\\[${room}\\] .*(script runtime error|throwing script exception)`), from);
  expect(failed, 'a script that failed in the game').toBeUndefined();
});
