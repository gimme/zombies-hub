// docker/player/pipelines.mjs - the lists of pipelines the web client draws with on each map (bo1-zombies/web/src/d3d9_webgpu.cpp:
// recipes), for docker/pipelines.sh: Playwright drives headless Chromium, drawing with WebGPU, through each map.
//
//   node pipelines.mjs <server url> <out dir> <map>...      writes <out dir>/<map>.bin as each map is done
//
// The page records each pipeline's recipe the first time a draw uses it (Bo1_PipelineRecipes). A map is first played in
// a game of the server's, as a room's first player does (a private room of one), which draws some things the map alone
// doesn't: its spawn, through the round's intro, goes into tier 0, and turning all around after into tier 2. Then it
// loads in the browser alone (?map=), where cheats work. Tier 0, which the client compiles before the player goes in,
// is what the spawn draws: looking all around it (every 30 degrees, at three pitches) through the round's intro, with
// the HUD and the first weapon. Tier 2, of which the client makes the stand-ins ahead (its tier 1) and the pipelines
// as draws need them, in the order drawn here, is what the map can
// show, drawn by construction rather than by going places: every cell of the world at once, every static model in its
// most detailed form (r_mod_drawAllCells), lit as the map's lights light it, once with no light's shadow map and once
// with every light's that may have one (r_mod_forceLightKind; the kinds of light a map has, the sun, spots, omnis,
// each with a shadow map where shadows are on for it: r_mod_lightkinds), so each lit surface draws with each technique
// it can have wherever the camera stands, where in play the few lights nearest the camera have shadow maps; every
// effect the zones have, played in front of the camera (bo1_fxall); every weapon the player can hold, given and raised
// (bo1_weapons), the spawn's weapon first, fired, aimed and reloaded; the pause menu, opened as Esc does (toggleMenu),
// whose blur the lists lacked; and zombies coming for a while (god mode). The
// two go into the map's list, each recipe once, at its first place and its lowest tier. The spawn is then drawn once
// more, by a new player (a new browser context) whose shadow maps are off, as mobile's recommended settings have them
// (web/src/settings.ts: RECOMMENDED) and as a player can set them: its lit surfaces draw with other techniques then, and no
// shadow map's passes draw, so a third of what that spawn draws with is no pipeline of the spawn with them on. The list
// says of each of the spawn's recipes which spawn drew with it (TIER_SHADOWS, TIER_NO_SHADOWS, or 0 for both), and a
// player compiles before going in those of their own setting (bo1-zombies/web/src/d3d9_webgpu.cpp: TakeRecipes). Each map has a
// list of its own:
// another map's recipe whose shaders this map's zones have too would compile here for nothing. The game draws with 4x
// anti-aliasing, a new player's: a recipe says whether its draw went to the back buffer's multisampled targets, which a
// player's samples then decide, with anti-aliasing or without; one made without it would serve players with it nothing
// of the scene.
import { writeFileSync } from 'node:fs';
import { chromium } from 'playwright';

const [server, out, ...maps] = process.argv.slice(2);
if (!server || !out || !maps.length) {
  console.error('usage: pipelines.mjs <server url> <out dir> <map>...');
  process.exit(2);
}
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const log = (line) => console.log(`pipelines: ${line}`);

// the full Chromium in its headless mode, WebGPU on SwiftShader
const browser = await chromium.launch({
  channel: 'chromium',
  args: ['--use-angle=swiftshader', '--enable-unsafe-webgpu', '--enable-features=Vulkan', '--use-vulkan=swiftshader',
    '--autoplay-policy=no-user-gesture-required'],
});

// sizeof(Recipe) (bo1-zombies/web/src/d3d9_webgpu.cpp); the header says, and is checked: 416 since Recipe has hints[16] after
// multisampled (408 with hints[8], 400 before)
const RECIPE = 416;
const TIER = 398; // Recipe::tier
// Recipe::tier in the file: 0 what the spawn draws with whatever the player's shadow maps, 2 the rest, and what the
// spawn draws with with shadow maps on alone, or off alone (bo1-zombies/web/src/d3d9_webgpu.cpp: TIER_SHADOWS, TIER_NO_SHADOWS)
const TIER_SHADOWS = 4, TIER_NO_SHADOWS = 5;

// The page clears as for players (web/src/game.ts: startEngine), which docker/server.sh's dev server leaves out to show what
// is left out of the picture; some of what the game draws depends on it.
const PLAYERS = '&args=%2Bset%20r_clear%203';

// a page of the web client at url, in the browser context (whose store keeps a map once downloaded), and what drives
// it; start: what the page is asked to do once loaded
async function open(context, url, name, start) {
  const page = await context.newPage();
  const listeners = new Set();
  let antiAliasing = '';
  page.on('console', (m) => {
    const t = m.text();
    for (const listen of listeners) listen(t);
    const back = /^\[bo1\] webgpu: the back buffer is \d+x\d+, (.*)$/.exec(t);
    if (back) antiAliasing = back[1];
    if (/^\[bo1\] (gpu error|webgpu: )|R_ShaderWarming|^page: /.test(t)) console.log(t);
  });
  page.on('pageerror', (e) => console.log(`page: uncaught ${e.message}`));
  await page.goto(url);
  if (start) await start(page);
  const command = (text) => page.evaluate((t) => Module.bo1Command(t), text);

  // a console command's answer: what the page's console says from the command on, up to the line done matches; null
  // when none does in ms, or the page takes no commands yet (the engine answers on its next frame, which comes seconds
  // apart with several maps' lists made at once on one machine: docker/pipelines.sh, ZOMBIES_PIPELINES_NAME)
  async function say(text, done, ms = 30000) {
    const lines = [];
    let finish;
    const ended = new Promise((resolve) => (finish = resolve));
    const listen = (t) => {
      lines.push(t);
      if (done.test(t)) finish(true);
    };
    listeners.add(listen);
    const timer = setTimeout(() => finish(false), ms);
    try {
      const sent = await page.evaluate((t) => {
        try {
          Module.bo1Command(t);
          return true;
        } catch {
          return false;
        }
      }, text);
      return sent && (await ended) ? lines : null;
    } finally {
      clearTimeout(timer);
      listeners.delete(listen);
    }
  }

  // the player, from bo1_player (e2e/game/player.ts: ask)
  async function ask() {
    const text = ((await say('bo1_player', /^bo1_player: /)) || []).find((l) => l.startsWith('bo1_player: ')) || '';
    const m = /^bo1_player: pm_type (-?\d+) origin (\S+) (\S+) (\S+) angles (\S+) (\S+) weapon (\S+)/.exec(text);
    return m && { pmType: +m[1], origin: [+m[2], +m[3], +m[4]], weapon: m[7] };
  }
  async function recipes() {
    const b64 = await page.evaluate(() => {
      const heap = self.HEAPU8 || Module.HEAPU8;
      const at = Module.ccall('Bo1_PipelineRecipes', 'number', [], []) >>> 0;
      const n = Module.ccall('Bo1_PipelineRecipesSize', 'number', [], []);
      let s = '';
      for (let i = 0; i < n; i += 0x8000) s += String.fromCharCode(...heap.subarray(at + i, at + Math.min(n, i + 0x8000)));
      return btoa(s);
    });
    return Buffer.from(b64, 'base64');
  }

  let p = null;
  for (let waited = 0; !(p && p.pmType === 0 && p.weapon !== 'none'); waited += 2) {
    if (waited >= 600) throw new Error(`${name}: the map did not download and the player spawn in 600 s`);
    await sleep(2000);
    p = await ask();
  }
  log(`${name}: spawned at ${p.origin.join(' ')}`);
  if (antiAliasing !== '4x anti-aliasing')
    throw new Error(`${name}: the game draws with ${antiAliasing || 'a back buffer the page did not say'}, not 4x anti-aliasing`);
  return { page, command, say, recipes, origin: p.origin, weapon: p.weapon };
}

// all around the spawn (every 30 degrees, level, down and up, then straight down and up), through the round's intro:
// what the spawn draws with
async function lookAround(command, origin) {
  const [x, y, z] = origin;
  const views = [[0, 85], [0, -85]];
  for (const pitch of [0, 30, -30]) for (let yaw = 0; yaw < 360; yaw += 30) views.push([yaw, pitch]);
  for (const [yaw, pitch] of views) {
    await command(`setviewpos ${x} ${y} ${z + 60} ${yaw} ${pitch}`);
    await sleep(1200);
  }
  await command(`setviewpos ${x} ${y} ${z + 60} 0 0`);
  await sleep(3000);
}

// The spawn with shadow maps off, in a new player's first game, which starts with the recommended settings: the page
// adds this one to them (web/src/main.ts: &recommended=), with the rest a PC's, its 4x anti-aliasing too, as a recipe drawn
// with it serves a player without (Recipe::multisampled). Its recipes.
async function playSpawnWithout(context, map) {
  const name = `${map} without shadow maps`;
  const { page, command, say, recipes, origin } = await open(
    context,
    `${server}/?map=${map}&autostart=1&recommended=sm_enable:0${PLAYERS}`,
    name,
  );
  const said = ((await say('sm_enable', /"sm_enable" is:/)) || []).find((l) => /"sm_enable" is:/.test(l)) || '';
  if (!/"sm_enable" is: "0/.test(said))
    throw new Error(`${name}: the game's shadow maps are not off (${said || 'sm_enable did not answer'})`);
  await command('god');
  await lookAround(command, origin);
  const spawn = await recipes();
  log(`${name}: ${(spawn.length - 12) / RECIPE} pipelines at the spawn`);
  await page.close();
  return spawn;
}

async function playMap(context, map) {
  const { page, command, say, recipes, origin, weapon } = await open(
    context,
    `${server}/?map=${map}&autostart=1${PLAYERS}`,
    map,
  );
  await command('god');
  const count = async () => ((await recipes()).length - 12) / RECIPE;

  // tier 0: all around the spawn, through the round's intro
  const [x, y, z] = origin;
  await lookAround(command, origin);
  const spawn = await recipes();
  log(`${map}: ${(spawn.length - 12) / RECIPE} pipelines at the spawn`);

  // the pause menu, opened and closed as Esc does (CL_ToggleMenu_f): its blur's passes (filter_symmetric_4 and _8,
  // feedbackblend), which a list made before this lacks, draw the menu's first frames with their stand-ins, unblurred,
  // or not at all where those are compiling too
  await command('toggleMenu');
  await sleep(3000);
  await command('toggleMenu');
  await sleep(1500);
  log(`${map}: ${await count()} pipelines with the pause menu`);

  // tier 2: every cell at once, then lit by each kind of light the map has, every light drawn as that kind (a frame
  // with every cell takes the engine, drawing in software, some seconds)
  await command('r_mod_drawAllCells 1');
  await sleep(8000);
  log(`${map}: ${await count()} pipelines with every cell drawn`);
  const said = await say('r_mod_lightkinds', /^r_mod_lightkinds:/);
  if (!said) throw new Error(`${map}: r_mod_lightkinds did not answer`);
  const kinds = said
    .filter((l) => l.startsWith('r_mod_lightkinds:'))
    .flatMap((l) => l.slice('r_mod_lightkinds:'.length).split(' ').filter((k) => /^[1-6]$/.test(k)));
  log(`${map}: the kinds of light: ${kinds.join(' ') || 'none'}`);
  // every light as its own kind, without its shadow map, then with one where it may have one in play: which lights have
  // one in a frame depends on where the camera is
  for (const [mode, shadows] of [[1, 'without shadow maps'], [2, 'with shadow maps']]) {
    await command(`r_mod_forceLightKind ${mode}`);
    await sleep(8000);
    log(`${map}: ${await count()} pipelines with every light drawn ${shadows}`);
  }
  await command('r_mod_forceLightKind 0');
  await command('r_mod_drawAllCells 0');

  // every effect the zones have, played in front of the camera, 6 a frame, each for a second
  await command(`setviewpos ${x} ${y} ${z + 60} 0 0`);
  const played = await say('bo1_fxall 6 1000', /^bo1_fxall: done/, 900000);
  if (!played) throw new Error(`${map}: bo1_fxall did not finish in 900 s`);
  log(`${map}: ${await count()} pipelines with the effects (${played.find((l) => l.startsWith('bo1_fxall: done'))})`);

  // every weapon the player can hold, given alone and raised; the spawn's first, fired, aimed and reloaded (its muzzle
  // flash's light, its shells)
  const listed = await say('bo1_weapons', /^bo1_weapons: \d+ weapons$/);
  if (!listed) throw new Error(`${map}: bo1_weapons did not answer`);
  const names = listed.map((l) => /^bo1_weapons: weapon (\S+)$/.exec(l)?.[1]).filter(Boolean);
  const weapons = [weapon, ...names.filter((n) => n !== weapon)];
  for (const [i, name] of weapons.entries()) {
    await command('take weapons');
    await command(`give ${name}`);
    await sleep(1500);
    if (i === 0) {
      await command('+attack');
      await sleep(800);
      await command('-attack');
      await command('+speed_throw');
      await sleep(1200);
      await command('-speed_throw');
      await command('+reload');
      await sleep(300);
      await command('-reload');
      await sleep(1500);
    }
  }
  log(`${map}: ${await count()} pipelines with the ${weapons.length} weapons`);
  await command('take weapons');
  await command(`give ${weapon}`);
  await command('give ammo');
  await sleep(1500);

  // every character model the level has (the zombies' bodies and heads, picked at random in play), stood up six at a
  // time in front of the camera (bo1_models), and the hands of every character with the spawn's weapon (bo1_viewmodel),
  // each under the lights without their shadow maps and with them
  const models = await say('bo1_models', /^bo1_models: \d+ models$/);
  if (!models) throw new Error(`${map}: bo1_models did not answer`);
  const modelCount = +(/^bo1_models: (\d+) models$/.exec(models.find((l) => /^bo1_models: \d+ models$/.test(l)))?.[1] || 0);
  for (let first = 0; first < modelCount; first += 6)
    for (const mode of [1, 2]) {
      await command(`r_mod_forceLightKind ${mode}`);
      await command(`bo1_models ${first} 6`);
      await sleep(2500);
    }
  await command('bo1_models clear');
  const hands = (await say('bo1_viewmodels', /^bo1_viewmodels: \d+ models$/)) || [];
  const handsNames = hands.map((l) => /^bo1_viewmodels: model (\S+)$/.exec(l)?.[1]).filter(Boolean);
  for (const name of handsNames)
    for (const mode of [1, 2]) {
      await command(`r_mod_forceLightKind ${mode}`);
      await command(`bo1_viewmodel ${name}`);
      await sleep(2000);
    }
  await command('r_mod_forceLightKind 0');
  log(`${map}: ${await count()} pipelines with the ${modelCount} character models and ${handsNames.length} hands`);

  // zombies coming, and shot at
  for (let i = 0; i < 12; i++) {
    await command('+attack');
    await sleep(1000);
    await command('-attack');
    await sleep(2500);
  }
  const all = await recipes();
  log(`${map}: ${(all.length - 12) / RECIPE} pipelines in all`);
  await page.close();
  return { all, spawnCount: (spawn.length - 12) / RECIPE };
}

// a game of the server's on the map, joined: its spawn through the round's intro, then turning all around (no cheats
// there). The player makes a private room of the map, as the page's lobby takes it (server/rooms.go: create), and the
// room's button downloads the map and says the player is ready, as ?autostart=1 does with the server's default map
// (web/src/session.ts: whatTheUrlAsks). The player leaves the room after, which ends its game.
async function joinServer(context, map) {
  const name = `the server's ${map}`;
  let lobby = null;
  const { page, command, recipes } = await open(context, `${server}/?name=Pipelines${PLAYERS}`, name, async (page) => {
    await page.waitForSelector('#outside:not(.hidden)', { timeout: 30000 });
    // what the page says to the lobby, as the player whose token the page keeps
    lobby = (action, body = {}) => page.evaluate(([action, body]) => fetch('/lobby/' + action, { method: 'POST',
      body: JSON.stringify(body), headers: { 'Content-Type': 'application/json',
        Authorization: 'Bearer ' + localStorage.getItem('zombies-token') } }).then((r) => r.ok), [action, body]);
    if (!(await lobby('create', { private: true, map }))) throw new Error(`${name}: the lobby made no room of it`);
    await page.waitForSelector('#inside:not(.hidden)', { timeout: 30000 });
    for (let waited = 0; ; waited++) {
      const says = await page.textContent('#ready');
      if (says === 'Ready') break;
      if (waited >= 600) throw new Error(`${name}: the room's button did not get the map: it says ${says}`);
      if (says.startsWith('Download · ')) await page.click('#ready');
      await sleep(1000);
    }
    await page.click('#ready');
    await page.waitForSelector('#roomgame:not(.hidden)', { timeout: 60000 });
  });
  await sleep(10000);
  const spawn = await recipes();
  log(`${name}: ${(spawn.length - 12) / RECIPE} pipelines at the spawn`);
  for (const [turn, ms] of [['right', 3000], ['lookup', 1000], ['lookdown', 2000], ['lookup', 1000]]) {
    await command(`+${turn}`);
    await sleep(ms);
    await command(`-${turn}`);
  }
  await sleep(2000);
  const all = await recipes();
  log(`${name}: ${(all.length - 12) / RECIPE} pipelines in all`);
  await lobby('leave');
  await page.close();
  return { all, spawnCount: (spawn.length - 12) / RECIPE };
}

// each map's list: a recipe once (whatever its tier), at its first place; the server's game first, as its game starts:
// the round's intro, as the first player to join sees it; the spawn with shadow maps off last, in a context of its own
// (a new player's, whose first game starts with the recommended settings). A recipe a spawn drew with is the spawn's,
// of both settings or of the one (its tier in the file), and the others the rest. A map that fails writes no list
// (one it had stays), and the next goes on.
const failed = [];
for (const map of maps) {
  let header = null;
  const order = [];
  // recipe without its tier -> its record, and whether the spawn drew with it with shadow maps on, and off
  const seen = new Map();
  const take = (all, spawnCount, without) => {
    if (all.readUInt32LE(0) !== 0x50314f42 || all.readUInt32LE(8) !== RECIPE)
      throw new Error(`${map}: not a list of ${RECIPE}-byte recipes`);
    header = header || all.subarray(0, 12);
    for (let i = 0; 12 + (i + 1) * RECIPE <= all.length; i++) {
      const r = Buffer.from(all.subarray(12 + i * RECIPE, 12 + (i + 1) * RECIPE));
      r[TIER] = 0;
      const key = r.toString('base64');
      let known = seen.get(key);
      if (!known) {
        known = { r, on: false, off: false };
        seen.set(key, known);
        order.push(known);
      }
      if (i < spawnCount) known[without ? 'off' : 'on'] = true;
    }
  };
  try {
    const context = await browser.newContext({ viewport: { width: 1280, height: 720 } });
    try {
      for (const play of [joinServer, playMap]) {
        const { all, spawnCount } = await play(context, map);
        take(all, spawnCount, false);
      }
    } finally {
      await context.close();
    }
    const newPlayer = await browser.newContext({ viewport: { width: 1280, height: 720 } });
    try {
      const spawn = await playSpawnWithout(newPlayer, map);
      take(spawn, (spawn.length - 12) / RECIPE, true);
    } finally {
      await newPlayer.close();
    }
  } catch (e) {
    log(`${map} failed, and has no list: ${e.message}`);
    failed.push(map);
    continue;
  }
  // the stand-ins are tier 1
  for (const k of order) k.r[TIER] = k.on && k.off ? 0 : k.on ? TIER_SHADOWS : k.off ? TIER_NO_SHADOWS : 2;
  const count = (tier) => order.filter((k) => k.r[TIER] === tier).length;
  const file = `${out}/${map}.bin`;
  writeFileSync(file, Buffer.concat([header, ...order.map((k) => k.r)]));
  log(`${map}: ${order.length} pipelines, ${count(0) + count(TIER_SHADOWS)} of them before going in with shadow maps on ` +
    `and ${count(0) + count(TIER_NO_SHADOWS)} with them off (${count(0)} with both), in ${file}`);
}
await browser.close();
if (failed.length) {
  log(`no list for ${failed.join(' ')}`);
  process.exitCode = 1;
}
