// Two players play a room's game to game over. Ann makes the room; Bob opens its link through a reverse proxy, as from
// another machine, and joins its game at the page's host name. Round 1 waits for both. Ann plays: the mouse, the keys,
// the game's menu, full screen and a window (playGame). Bob drops, twice, and takes his character back
// (dropAndComeBack), then quits the game from its menu and leaves the room (leaveGame). The game keeps his character
// until he leaves. Game over (the console's fast_restart, which the level's own game over comes to as well) ends the
// game, and Ann's page is back in the room, showing how it went.
import { expect, map, type Player, type PlayerState, test } from '../fixtures';
import { inGame, kinoSpawnPoint, loadingMovie, onKino, outro } from '../play';

const sleep = (ms: number) => new Promise((done) => setTimeout(done, ms));

// how far the player turned from one state to another, in degrees
const degrees = (from: PlayerState, to: PlayerState) => Math.abs(((to.yaw - from.yaw + 540) % 360) - 180);

// the mouse moved 300 px from (x, y), by dx and dy a step, in 20 steps
async function sweep(p: Player, x: number, y: number, dx: number, dy: number): Promise<void> {
  for (let i = 1; i <= 20; i++) {
    await p.page.mouse.move(x + i * dx, y + i * dy);
    await sleep(50);
  }
  await sleep(1500);
}

// Esc as the page connects to the game and as the game loads its map cancels nothing
// (bo1-zombies/src/client/cl_keys.cpp)
async function escWhileLoading(p: Player): Promise<string[]> {
  const pressed: string[] = [];
  for (const [pattern, as] of [[/^net: WebTransport to \S+ open$/, 'the page connected to the game'],
    [/^Loaded fastfile 'common_zombie' /, 'the game loaded its map']] as const) {
    if (!(await p.next(pattern, 900_000))) break;
    await p.escape();
    pressed.push(as);
    p.note(`pressed Esc as ${as}`);
  }
  return pressed;
}

// Ann's part: once spawned, the mouse turns nothing until a click captures it, in full screen; she walks, through a
// tap of Tab, and back; turns; fires; holds Tab; Esc opens the game's menu and closes it, in full screen and in a
// window; then her client's dvars are the SP game's.
async function playGame(p: Player, escaped: Promise<string[]>): Promise<PlayerState> {
  const spawn = await p.spawned();
  p.note(`spawned at ${spawn.origin.join(' ')} with ${spawn.weapon}, ${spawn.clip}/${spawn.stock}`);
  if ((await escaped).length < 2) throw new Error('the player did not press Esc as the game loaded');

  // What the spawn draws compiled while the page loaded (the tier 0 of the map's list, web/pipelines/<map>.bin): no
  // draw of it waits for its pipeline. Zombies and barricade boards around the spawn vary from game to game, and with
  // them a few pipelines the list may lack, drawn with a stand-in meanwhile; more than a few means it is out of date.
  const counts = () => p.page.evaluate(() => ['Bo1_PipelineWaits', 'Bo1_FirstTierWaits', 'Bo1_PipelinesLacked',
    'Bo1_PipelineStandIns'].map((f) => window.Module.ccall(f, 'number', [], [])));
  const before = await counts();
  await sleep(3000); // the weapon comes up
  const [waited, firstTier, lacked, standIns] = (await counts()).map((n, i) => n - before[i]);
  expect(firstTier, 'draws of what the list compiles before going in that waited for their pipelines').toBe(0);
  expect(lacked, 'pipelines the spawn needed that web/pipelines/<map>.bin lacks (docker/pipelines.sh lists them)')
    .toBeLessThanOrEqual(5);
  p.note(`${waited} draws waited for their pipelines after spawning, ${standIns} of them drawn with a stand-in, ` +
    `${lacked} pipelines the list lacks`);

  // the mouse not captured turns nothing: it is the player's, on its way to the click that captures it
  const still = await p.state();
  await sweep(p, 340, 360, 15, 0);
  expect(degrees(still, await p.state()), 'degrees the mouse turned the player before a click captured it')
    .toBeLessThanOrEqual(2);
  // a click on the game captures the mouse, and puts the page in full screen; the game doesn't see it (it would fire)
  await p.click();
  expect(await p.captured(true, 5000), 'a click on the game captured the mouse').toBe(true);
  expect(await p.fullScreen(), 'the click that captured the mouse went full screen').toBe(true);
  await sleep(1000);
  const start = await p.state();
  expect(start.clip, 'rounds in the clip after the click that captured the mouse').toBe(still.clip);
  await p.shot('spawned');

  // W held for 4 s, through a tap of Tab (the scoreboard) after 1 s: the engine holds forward on once Tab is up, as
  // the scoreboard's closing lets go of no key (bo1-zombies/src/ui_mp/ui_main_mp.cpp: UI_SetActiveMenu)
  await p.page.keyboard.down('KeyW');
  const held = await p.afterFrames(2, 1000);
  await p.press('Tab', 300);
  const tabbed = await p.afterFrames(3, 1000);
  await sleep(1700);
  await p.page.keyboard.up('KeyW');
  await sleep(1000);
  const walked = await p.state();
  expect(held.forward, 'W held for 1 s: the engine holds forward').toBe(true);
  expect(tabbed.forward, 'W held through a tap of Tab: the engine holds forward').toBe(true);
  expect(walked.forward, 'W up for 1 s: the engine holds forward').toBe(false);
  const distance = Math.hypot(walked.origin[0] - start.origin[0], walked.origin[1] - start.origin[1]);
  expect(distance, 'units W held for 4 s moved the player').toBeGreaterThanOrEqual(32);
  p.note(`walked ${distance.toFixed(0)} units, on through a tap of Tab`);
  const back = await p.walkBack(start, () => p.page.keyboard.down('KeyS'), () => p.page.keyboard.up('KeyS'));

  // turn, the mouse 300 px to the right; fire; the scoreboard while Tab is held (ping among its columns)
  await sweep(p, 640, 360, 15, 0);
  const turned = await p.state();
  expect(degrees(back, turned), 'degrees the mouse turned the player').toBeGreaterThanOrEqual(10);
  const fired = await p.fire(turned);
  p.note(`fired: ${fired.weapon} ${turned.clip} -> ${fired.clip} rounds in the clip`);
  await p.page.keyboard.down('Tab');
  await sleep(2000);
  await p.shot('scores');
  await p.page.keyboard.up('Tab');

  // In full screen, Esc is the page's (Keyboard Lock): it opens the game's menu, and the page keeps the mouse, the
  // menu's cursor moving as it does, the view turning not at all; the menu pauses nothing, a room's game going on
  // under it (bo1-zombies/src/client_mp/cl_parse_mp.cpp); and Esc closes it, the mouse turning the view again.
  const menuOpens = p.next(/^input: the mouse is a menu's$/, 10_000);
  await p.escape();
  expect(await menuOpens, "Esc opened the game's menu").toBeTruthy();
  expect(await p.captured(false, 2000), 'the page let the mouse go as the menu opened in full screen').toBe(false);
  const inMenu = await p.state();
  await sweep(p, 940, 360, -15, 5);
  expect(degrees(inMenu, await p.state()), "degrees the mouse turned the player in the game's " +
    "menu").toBeLessThanOrEqual(2);
  await p.shot('menu');
  expect(await p.dvar('cl_paused'), "cl_paused under the game's menu").toBe('0');
  const lookAgain = p.next(/^input: the mouse turns the view$/, 10_000);
  await p.escape();
  expect(await lookAgain, "Esc closed the game's menu").toBeTruthy();
  expect(await p.captured(true, 0), 'the mouse is the game\'s as the menu closed in full screen').toBe(true);
  const menuClosed = await p.state();
  await sweep(p, 640, 460, 15, 0);
  expect(degrees(menuClosed, await p.state()), 'degrees the mouse turned the player once the menu closed')
    .toBeGreaterThanOrEqual(10);

  // the player leaves full screen (F11, or holding Esc), which turns their Full Screen setting off
  // (bo1-zombies/web/src/web_main.cpp: bo1_web_fullscreen): their clicks go full screen no more
  await p.page.evaluate(() => document.exitFullscreen());
  await sleep(2000);
  expect(await p.dvar('bo1_web_fullscreen'), 'the Full Screen setting once the player left full screen').toBe('0');
  if (!(await p.captured(true, 0))) {
    await p.click();
    expect(await p.captured(true, 5000), 'a click in a window captured the mouse').toBe(true);
  }
  // In a window, Chrome keeps Esc for itself, and lets the mouse go: the page gives the game an Esc then, which opens
  // its menu. The player's own Esc closes it; Chrome lets the page capture the mouse again only at a click or a key
  // (but Esc), here once nothing else was for 5 s: first a click, which stays in the window, then a key.
  const escInAWindow = async () => {
    await p.page.evaluate(() => window.bo1ChromeLetsGo());
    await sleep(6000);
    await p.escape();
    await sleep(2000);
    expect(await p.captured(true, 0), 'the page captured the mouse with no click or key just before').toBe(false);
  };
  await escInAWindow();
  await p.click();
  expect(await p.captured(true, 5000), 'the game had an Esc as Chrome let the mouse go, and a click captured ' +
    'it').toBe(true);
  expect(await p.fullScreen(), 'the click went full screen, though the player had left it').toBe(false);
  await escInAWindow();
  await p.press('KeyW');
  expect(await p.captured(true, 5000), 'the game had an Esc as Chrome let the mouse go, and a key captured ' +
    'it').toBe(true);
  expect(await p.fullScreen(), 'the key went full screen, though the player had left it').toBe(false);
  // Esc, which Chrome takes, and the game has one, which opens its menu; then a key, and within 5 s Esc again, which
  // closes the menu: Chrome would let the page capture the mouse again at once, which it doesn't, the menu closed
  // with Esc. Captured with that Esc down, Chrome would take its up as an Esc to let the mouse go, and the menu would
  // open again.
  const menu = p.next(/^input: the mouse is a menu's$/, 10_000);
  await p.escape();
  expect(await menu, "the player's Esc in a window opened the game's menu").toBeTruthy();
  await p.page.keyboard.press('KeyW');
  const closed = p.next(/^input: the mouse turns the view$/, 10_000);
  await p.page.keyboard.down('Escape');
  expect(await closed, "Esc closed the game's menu in a window").toBeTruthy();
  await sleep(500);
  expect(await p.captured(true, 0), 'the page captured the mouse with the Esc that closed the menu still ' +
    'down').toBe(false);
  await p.page.keyboard.up('Escape');
  expect(await p.captured(true, 2000), 'the page captured the mouse by itself as Esc closed the menu').toBe(false);
  expect(await p.next(/^input: the mouse is a menu's$/, 2000), 'the menu opened again as Esc was up').toBe(false);
  await p.click();
  expect(await p.captured(true, 5000), 'a click after Esc closed the menu in a window captured the mouse').toBe(true);

  // a client of a room's game has the SP exe's values where the MP exe registers others (bo1-zombies/src/bgame/
  // bg_misc.cpp: BG_UpdateZombieDvarDefaults) or MP's startup config sets others, the SP game's online config's
  // (src/client_mp/cl_parse_mp.cpp), and what the level set on it
  for (const [name, value] of [['cg_overheadNamesSize', '0.8'], ['cg_overheadIconSize', '0.9'],
    ['waypointOffscreenPadLeft', '40'], ['waypointOffscreenPointerDistance', '30'],
    ['player_lastStandBleedoutTime', '45'], ['cg_fovExtraCam', '65'], ['enable_global_wind', '1'],
    ['r_watersim_enabled', '1'], ['cg_headIconMinScreenRadius', '0.025'], ['playerPushAmount', '1']]) {
    expect(await p.dvar(name), `${name} on a client of a room's game`).toBe(value);
  }
  p.checkReads();
  return spawn;
}

// Bob's part: once spawned, a click captures the mouse and he fires; then his page reloads, its game not left, and
// the room offers to rejoin it (it asks first, in Chrome's dialog); he rejoins, reloads again as the page loads into
// his character (the game has it back in its slot, not yet in the world: bo1-zombies/src/client_mp/sv_client_mp.cpp:
// SV_ClientResuming), rejoins, and has his character back. Then his client names itself Mallory (its name setting,
// which it sends the game in its userinfo the frame after), and he says something, which the game hears after the
// userinfo, not within 800 ms of it (sv_floodProtect).
async function dropAndComeBack(p: Player): Promise<PlayerState> {
  const spawn = await p.spawned();
  p.note(`spawned at ${spawn.origin.join(' ')} with ${spawn.weapon}, ${spawn.clip}/${spawn.stock}`);
  await p.page.mouse.click(640, 360);
  await sleep(1000);
  await p.fire(spawn);
  await sleep(3000); // the last shot reaches the player's snapshots
  const before = await p.state();
  p.note(`dropping, at ${before.origin.join(' ')} with ${before.weapon}, ${before.clip}/${before.stock}`);
  const rejoin = async () => {
    const button = p.page.locator('#ready[data-does="here"], #ready[data-does="rejoin"]');
    await expect(button, 'the page offers to rejoin its game').toHaveText('Rejoin', { timeout: 30_000 });
    await button.click();
    p.note('rejoined the game');
  };
  const asked = p.askedToLeave;
  let back = p.next(/^lobby: in room \w+$/, 60_000);
  await p.page.reload();
  expect(await back, 'the page came back to the room').toBeTruthy();
  expect(p.askedToLeave, 'times the page asked before it left its game').toBe(asked + 1);
  await rejoin();
  await p.waitFor(/^Unloaded fastfile 'ui_mp'$/, 300_000, p.mark());
  p.note('dropping again, as the page loads into its character');
  back = p.next(/^lobby: in room \w+$/, 60_000);
  await p.page.reload();
  expect(await back, 'the page came back to the room the second time').toBeTruthy();
  await rejoin();
  const after = await p.spawned();
  p.note(`back, at ${after.origin.join(' ')} with ${after.weapon}, ${after.clip}/${after.stock}`);
  const moved = Math.hypot(after.origin[0] - before.origin[0], after.origin[1] - before.origin[1]);
  expect([after.weapon, after.clip, after.stock], 'the character the player came back as').toEqual(
    [before.weapon, before.clip, before.stock]);
  expect(moved, 'units from where they dropped').toBeLessThanOrEqual(64);
  await p.shot('back');
  await p.command('name Mallory');
  await p.afterFrames(3, 3000);
  await p.command('say renamed');
  p.note('renamed the client Mallory, then said renamed');
  return spawn;
}

// Quit in the game's menu, which asks first (popup_leavegame_coop), and Yes: the game's disconnect, which quits the
// game, as a drop; the engine ends, and the page comes back to the room, as it was, which offers to rejoin the game;
// then the room's Leave, which takes the character out of the game, as the page asks first.
async function leaveGame(p: Player): Promise<void> {
  const before = p.marks();
  const back = p.next(/^lobby: back in the lobby, the game's engine stopped$/, 60_000);
  const menu = p.next(/^input: the mouse is a menu's$/, 15_000);
  await p.escape();
  expect(await menu, "Esc opened the game's menu").toBeTruthy();
  await sleep(2000);
  // the keys move between the menu's buttons: its first has them, and Up goes round to its last, Quit, which opens
  // the popup that asks, No first; Up goes to Yes
  for (const key of ['ArrowUp', 'Enter', 'ArrowUp', 'Enter']) {
    await p.press(key);
    await sleep(2000);
  }
  p.note('quit the game, from its menu');
  expect(await back, 'the page came back to the room after the player quit the game').toBeTruthy();
  await p.inPlace('the player quit the game', before);
  await expect(p.page.locator('#ready[data-does="rejoin"]'), 'the room offers to rejoin the game').toBeVisible(
    { timeout: 30_000 });
  await p.shot('quit');
  await p.waitFor(/^lobby: the game ended \(left\): You left the game\.$/, 10_000);
  await p.page.click('#leave');
  await expect(p.page.locator('#ask:not(.hidden) #asktext'), 'the page asks before the player leaves the game')
    .toHaveText(/^Leave the game\?/, { timeout: 5000 });
  await p.page.click('#askyes');
  await expect(p.page.locator('#outside'), 'the page left the room').toBeVisible({ timeout: 15_000 });
  p.note('left the room');
}

test("two players play a room's game to game over", async ({ server, players }) => {
  await server.startProxy();
  const ann = await players.open('Ann');
  const room = await ann.makeRoom();
  // Bob's name has signs the game keeps of a name, as it says when he takes his character back (SV_SanitizeName)
  const bobName = "Bob_O'Neil";
  const bob = await players.open(bobName, { query: `room=${room}`, proxy: true });
  const escaped = escWhileLoading(ann);
  for (const p of [ann, bob]) {
    p.background(() => loadingMovie(p, 'room'));
    p.background(() => outro(p, false));
  }
  await bob.waitFor(new RegExp(`^lobby: in room ${room}$`), 60_000);

  // Ann's page downloads the map while Bob comes, as does Bob's, both from this server at once; she is ready once
  // he is in, and her page plays the game's lobby's sounds: his coming in, and the ticks of the countdown to the game
  // (web/src/sounds.ts: roomSounds, web/src/countdown.ts: countDown)
  bob.background(() => bob.getReady());
  await ann.getReady(2);
  await ann.waitForGame();
  await server.waitFor(new RegExp(`^\\[${room}\\] ------- Game Initialization -------$`), 120_000);
  await server.noZombies(room);
  await inGame(server, room, 'Ann', bobName);
  const played = (alias: string) => ann.soundsStarted.filter((s) => s === alias).length;
  expect(played('uin_lobby_join'), "lobby sounds of a player coming into Ann's room").toBeGreaterThanOrEqual(1);
  expect(played('uin_timer'), "ticks of the countdown to Ann's room's game").toBeGreaterThanOrEqual(1);
  expect(ann.find(new RegExp(`^lobby: downloaded ${map} `)), "Ann's page downloaded the map ahead of the game")
    .toBeTruthy();
  expect(server.find(new RegExp(`^files: Ann downloaded ${map}: [0-9]+ MB in `)), 'how the download went').toBeTruthy();
  // Ann's loading movie, which her page started with the engine, at a new player's cinematics and master volumes (1
  // and 0.5: 0.25 of its own), which the page gave the engine as it had yet to read her config
  expect(ann.find(new RegExp(`^\\[bo1\\] bink: the loading movie ${map}_load plays on the page, at 0\\.250 of its ` +
    'volume$')), "Ann's loading movie's volume").toBeTruthy();
  expect(bob.find(new RegExp(`^net: WebTransport to zombies\\.test:${server.port} open$`)),
    "Bob joined the game at the page's host name").toBeTruthy();
  expect(ann.find(/^\[bo1\] WebGPU: canvas /), 'Ann draws with WebGPU').toBeTruthy();
  expect(ann.find(/pipeline recipes, [0-9]* of them compiled before going in/),
    `Ann got ${map}'s list of pipelines to compile while it loaded`).toBeTruthy();

  // round 1 begins once both are in, not before: once both are past their loading movie
  const start = await server.waitFor(new RegExp(`^\\[${room}\\] bo1_lobby: start$`), 150_000);
  const ins = server.lines.slice(0, server.lines.indexOf(start))
    .filter((l) => l.startsWith(`[${room}] bo1_lobby: in `)).length;
  expect(ins, 'players in as round 1 began').toBe(2);
  // The level has each player's client let zombies push them (playerPushAmount 1), which the SP game's server had
  // from its host's client; a room's game, with no host, runs its host's slot's client dvars itself
  // (G_SP_HostClientServerCommand). It has the SP exe's defaults where the MP exe registers others
  // (BG_UpdateZombieDvarDefaults): its zombies lean as they run, and its sentry guns take 4 s to pick a target.
  for (const [dvar, value] of [['playerPushAmount', '1'], ['ai_useLeanRunAnimations', '1'],
    ['turret_SentryTargetTime', '4000']]) {
    await server.poll(`room ${room}'s game has ${dvar} ${value}`, 30_000, () => server.gameHas(room, dvar, value));
  }

  // a player's seat's address, as a pattern
  const seatOf = (name: string) => server.seatOf(room, name)!.replace(/\./g, '\\.');
  const [annSpawn, bobSpawn] = await Promise.all([playGame(ann, escaped), (async () => {
    const spawn = await dropAndComeBack(bob);
    const seat = seatOf(bobName);
    // the game has his character back in his slot, as client 1: he joined Ann's room, so he is its game's second
    // player, after Ann, its host, whichever connected first; the level gives spawn points and characters by it
    const tookBack = server.all(new RegExp(`^\\[${room}\\] ${seat}:28960 takes their character back \\(client [0-9], ` +
      `${bobName}\\)$`));
    expect(tookBack.length, 'times the game said Bob took his character back').toBeGreaterThanOrEqual(2);
    expect(tookBack.every((l) => l.includes('(client 1, ')), 'Bob, who joined Ann\'s room, is client 1').toBe(true);
    // his new client, which the level never met, is told again what the level set on his old one (SetClientDvars)
    expect(server.find(new RegExp(`^\\[${room}\\] ${bobName}'s client is told again the [1-9][0-9]* dvars the level ` +
      'set on it$')), "the game told Bob's new client again the dvars the level set on his old one").toBeTruthy();
    // the game keeps the name he joined with, the lobby's, for itself and for the level, which says who said it
    await server.waitFor(new RegExp(`^\\[${room}\\] ${bobName} keeps their name, though their client says Mallory$`));
    const said = await server.waitFor(new RegExp(`^\\[${room}\\] .*renamed$`));
    expect(said, 'who the game says said it').toContain(bobName);
    expect(said).not.toContain('Mallory');
    await leaveGame(bob);
    return spawn;
  })()]);
  if (onKino) {
    expect(kinoSpawnPoint(annSpawn), `Ann spawned at ${annSpawn.origin.join(' ')}, on a spawn point`).toBeTruthy();
    expect(kinoSpawnPoint(bobSpawn), `Bob spawned at ${bobSpawn.origin.join(' ')}, on a spawn point`).toBeTruthy();
  }

  const seat = seatOf(bobName);
  // The game keeps Bob's character as he quits, and lets it go only as he leaves the room (bo1_lobby_drop, a server
  // command, which the game runs with its server thread stopped: bo1-zombies/src/server_mp/sv_lobby_mp.cpp).
  await server.waitFor(new RegExp(`^\\[${room}\\] bo1_lobby: out ${seat}:28960$`), 30_000);
  const index = (pattern: RegExp) => server.lines.findIndex((l) => pattern.test(l));
  const quit = index(new RegExp(`^\\[${room}\\] ${bobName} quit the game: their character waits for them$`));
  const left = index(new RegExp(`^server: ${bobName} left room `));
  const out = index(new RegExp(`^\\[${room}\\] bo1_lobby: out ${seat}:28960$`));
  expect(quit, 'the game kept Bob\'s character as he quit').toBeGreaterThanOrEqual(0);
  expect(left, 'Bob left the room after he quit').toBeGreaterThan(quit);
  expect(out, 'the game let his character go once he left the room').toBeGreaterThan(left);

  // Game over ends the game, and Ann's page is back in the room. The level's end_game starts the level again, which
  // comes to SP_LoadGameContinue_f (bo1-zombies/src/game_sp/g_sp_loadgame.cpp), as the console's fast_restart does.
  // The game may be over by itself before that, which is looked for from Bob's quitting on: a zombie may have downed
  // Ann by then, and no one was left standing as the game let Bob's character go.
  await server.console(`${room} fast_restart`);
  await server.waitFor(new RegExp(`^server: room ${room}'s game ended: quit$`), 30_000, quit);
  expect(server.find(new RegExp(`^server: room ${room}'s game is over$`), quit), 'the lobby heard it was game over')
    .toBeTruthy();
  expect(server.find(new RegExp(`^\\[${room}\\] bo1_lobby: over [0-9]+$`), quit), 'the round it was over on')
    .toBeTruthy();
  // what Ann earned is never less than the points she started with, whatever she spent, which comes after it
  expect(server.find(new RegExp(`^\\[${room}\\] bo1_lobby: stats ${seatOf('Ann')}:28960 [1-9][0-9]{2,}( ` +
    `-?[0-9]+){6}$`), quit),
    "Ann's line of the scoreboard at game over").toBeTruthy();
  // on Call of the Dead, once its outro has rolled (38 s)
  await ann.waitFor(/^lobby: the game ended \(over\): .*BO1_GAMEOVER$/, 90_000);
  if (map === 'zombie_coast') await ann.waitFor(/^test: the outro rolls$/, 1000);
  await ann.waitFor(/^lobby: how the game went: round [0-9]+, 1 player$/, 30_000);
});
