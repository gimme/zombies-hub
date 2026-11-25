// A player plays a map alone, in their browser: the page runs the game itself, as single player did, on no game of the
// server's. A new player, Eve turns the shadows off in the lobby's settings menu first, which plays the game's menu
// sounds as the game's menu does, as the lobby's music plays at the game's level for her volumes; the maps to play
// locally close and open with the sounds of the game's large menus (web/src/sounds.ts: menuSound). Her first game has
// the shadows off (it sets the recommended settings, then runs her config again: bo1-zombies/src/qcommon/common.cpp,
// Com_CheckSetRecommended), and draws with their 4x anti-aliasing. Her loading movie starts as the game loads the map
// (bo1_web_movieAtLoad), and she skips it (play.ts: loadingMovie). Restart Level starts the map again; her brightness
// shows; game over ends the game, and the page is back in the lobby, saying so: the page stays, its engine's memory
// freed. There the settings menu shows what the game kept, and she changes more in it; she plays again, and the game
// starts with all of them: the browser keeps her config in its store (web/public/store.js: players/).
import { expect, map, Player, test } from '../fixtures';
import { loadingMovie, outro } from '../play';

const sleep = (ms: number) => new Promise((done) => setTimeout(done, ms));

// What the settings menu, open, shows on its pages: each row's value, or a key's row's keys, by the row's name. Its
// tabs open the pages, as a player's clicks do. The last tab of each screen resets the screen, and is left alone, as
// is a tab that is greyed out.
async function menuValues(p: Player): Promise<Record<string, string>> {
  const values: Record<string, string> = {};
  for (const screen of [0, 1]) {
    await p.page.click(`#options .om-tab >> nth=${screen}`);
    const tabs = p.page.locator('#options .om-side');
    for (let i = 0; i < (await tabs.count()) - 1; i++) {
      if (await tabs.nth(i).evaluate((b) => b.classList.contains('off'))) continue;
      await tabs.nth(i).click();
      await sleep(200);
      Object.assign(values, await p.page.evaluate(() => Object.fromEntries(
        [...document.querySelectorAll<HTMLElement>('#options .om-stage .om-value[data-row]')]
          .map((v) => [v.dataset.row, v.dataset.keys !== undefined ? v.dataset.keys : v.dataset.value]))));
    }
  }
  return values;
}

// What the engine says the settings and keys are: the sensitivity, F8's and X's bindings ('' unbound), the mouse's
// pitch (negative: inverted), the field of view, the picture's size and shape, the shadows, the anti-aliasing and the
// frame rate limit
async function settings(p: Player): Promise<Record<string, string | null>> {
  const key = async (name: string) => {
    const said = p.next(new RegExp(`^"${name}" (= "|is not bound)`, 'i'), 10_000);
    await p.command('bind ' + name);
    const m = /^"[^"]*" = "(.*)"$/i.exec((await said) || '');
    return m ? m[1] : '';
  };
  return { sensitivity: await p.dvar('sensitivity'), f8: await key('F8'), x: await key('X'),
    pitch: await p.dvar('m_pitch'), fov: await p.dvar('bo1_fov'), aspect: await p.dvar('r_aspectRatio'),
    mode: await p.dvar('r_mode'), wide: await p.dvar('wideScreen'), shadows: await p.dvar('sm_enable'),
    aa: await p.dvar('r_aaSamples'), maxfps: await p.dvar('bo1_maxfps') };
}

// the game's menu and lobby sounds the page started while it did something
async function heardWhile(p: Player, does: () => Promise<void>): Promise<string[]> {
  p.soundsStarted.length = 0;
  await does();
  await sleep(500);
  return p.soundsStarted.slice();
}

test('a player plays a map alone, twice, with the settings they changed', async ({ server, players }) => {
  const eve = await players.open('Eve', { args: '+set bo1_web_movieAtLoad 1', antiAliasing: true });
  eve.background(() => loadingMovie(eve, 'solo'));
  eve.background(() => outro(eve, true));
  await eve.getTheMap('#playalone');

  // The game's menu sounds, which the page decodes as it starts (web/src/sounds.ts): the menus' three, a large
  // menu's two, and the lobby's three, two of which the game streams (server/sounds.go), each with its sound group's
  // volume, the ui group's under master. The Settings button, the Graphics tab and the shadows' row play the sounds
  // of a button under the mouse and its click, of a tab, and of a row under the mouse.
  await expect.poll(() => eve.find(/^lobby: sounds, /)?.replace(/^lobby: sounds, /, '').split(', ').length,
    { message: "the page's 8 menu sounds" }).toBe(8);
  const groups = await eve.page.evaluate(() => fetch('/sounds').then((r) => r.json())
    .then((sent: Record<string, { group?: number }>) => Object.values(sent)
      .map((s) => (typeof s.group === 'number' ? s.group : 1))));
  for (const g of groups) expect(g, "a menu sound's group volume").toBeCloseTo(0.1995, 3);
  const heard = await heardWhile(eve, async () => {
    await eve.page.click('#settings');
    await eve.page.waitForSelector('#options:not(.hidden) .om-side', { timeout: 30_000 });
    await eve.page.click('#options .om-side >> nth=0');
    await eve.page.locator('.om-stage .om-label[data-row="MENU_SHADOWS_CAPS"]').click();
  });
  expect(await eve.page.getAttribute('.om-value[data-row="MENU_SHADOWS_CAPS"]', 'data-value'),
    'the shadows after a click on them').toBe('0');
  expect(heard, 'the sounds of the Settings button, the Graphics tab and the shadows\' row').toEqual([
    'uin_navigation_over', 'uin_navigation_click', 'uin_navigation_over', 'uin_navigation_click',
    'uin_navigation_over']);
  // the lobby's music, at the level the game plays its Zombies menu's at (its alias's -10 dB, its group's -2 dB and
  // master's -4 dB), with a new player's volumes (music 1, master 0.5)
  const music = await eve.page.waitForFunction(() => window.bo1MusicGain && window.bo1MusicGain.gain.value, null,
    { timeout: 30_000 }).then((h) => h.jsonValue() as Promise<number>);
  expect(music, "the lobby's music's level").toBeCloseTo(0.3162 * 0.7943 * 0.631 * 0.25, 3);
  const menuBefore = await menuValues(eve);
  await eve.page.click('.om-back');

  // Back to the lobby plays a button's sound under the mouse once more, not a click, and the large menu's closing;
  // Play local's Go plays its click, and the menu's opening
  const back = await heardWhile(eve, async () => {
    await eve.page.click('#soloback');
    await eve.page.waitForSelector('#solo.hidden', { state: 'attached', timeout: 10_000 });
  });
  const count = (sounds: string[], name: string) => sounds.filter((n) => n === name).length;
  expect([count(back, 'uin_navigation_menu_lg_close'), count(back, 'uin_navigation_click'), back.at(-1)],
    `Back to the lobby's sounds: ${back.join(', ')}`).toEqual([1, 0, 'uin_navigation_menu_lg_close']);
  expect(count(back, 'uin_navigation_over'), `Back to the lobby's sounds: ` +
    `${back.join(', ')}`).toBeGreaterThanOrEqual(2);
  const open = await heardWhile(eve, async () => {
    await eve.page.click('#soloopen');
    await eve.page.waitForSelector('#solo:not(.hidden)', { timeout: 10_000 });
  });
  const at = open.indexOf('uin_navigation_menu_lg_open');
  expect(at >= 1 && open[at - 1] === 'uin_navigation_click' && count(open, 'uin_navigation_menu_lg_open') === 1,
    `Go's sounds: ${open.join(', ')}`).toBe(true);

  eve.lobbyMemory = await eve.memory();
  eve.note(`before the first game, ${Player.gb(eve.lobbyMemory)}`);
  await eve.page.click('#playalone');
  await eve.waitFor(new RegExp(`^lobby: playing ${map} alone$`), 60_000);
  const p = await eve.spawned();
  eve.note(`spawned alone at ${p.origin.join(' ')} with ${p.weapon}, ${p.clip}/${p.stock}`);
  expect(eve.antiAliasing, 'the first game draws with the recommended settings\' ' +
    'anti-aliasing').toBe('4x anti-aliasing');
  // the loading movie played over the load, which alone is the game's server's (SV_SpawnServer), not after it, at her
  // cinematics and master volumes (1 and 0.5: 0.25 of its own)
  const movie = eve.lines.indexOf(`Playing the loading movie ${map}_load`);
  const zone = eve.lines.findIndex((l) => l.startsWith(`Loaded fastfile '${map}' `));
  expect(movie >= 0 && movie < zone, 'the loading movie played over the load').toBe(true);
  expect(eve.find(new RegExp(`^\\[bo1\\] bink: the loading movie ${map}_load plays on the page, at 0\\.250 of its ` +
    'volume$')), "Eve's loading movie's volume").toBeTruthy();
  eve.checkReads();
  expect(server.roomOf('Eve'), 'a room of Eve\'s, playing alone').toBeUndefined();
  expect(server.find(/^server: room [A-Z]+ starts a game of .* for Eve/), 'a game the server started for Eve')
    .toBeUndefined();

  // Restart Level in the game's menu (popup_restart_warning: fade, silence, fast_restart) starts the map again, as
  // alone in SP, rather than end the game (bo1-zombies/src/game_sp/g_sp_loadgame.cpp: SP_FastRestart); the player
  // fires first, once the level lets them, a few seconds in
  await eve.page.mouse.click(640, 360);
  let fired = p;
  for (let waited = 0; fired.clip >= p.clip; waited += 3) {
    if (waited >= 90) {
      throw new Error(`the player could not fire in 90 s: ${fired.clip} of ${p.clip} rounds in the clip`);
    }
    await eve.click();
    await sleep(2600);
    fired = await eve.state();
  }
  await eve.command('fade 0 0 0 255 0 0 1; silence; fast_restart');
  const again = await eve.until(120, (q) => q.pmType === 0 && q.clip === p.clip && q.stock === p.stock);
  expect(again && again.clip === p.clip && again.pmType === 0, 'Restart Level started the map again, the clip full')
    .toBe(true);
  // the base data files come with the engine's module (bo1-zombies/web/CMakeLists.txt: data/main), the playlists'
  // settings among them, which keep Mule Kick on every map (bo1-zombies/src/bgame/bg_misc.cpp)
  expect(await eve.dvar('scr_zm_extra_perk_all'), 'Mule Kick on every map').toBe('1');

  // The game's brightness, its gamma ramp, which the page applies as the frame goes onto the canvas, as a display
  // would (bo1-zombies/web/src/d3d9_webgpu.cpp: Present): the screen faded to grey 128 shows at brightness 2 as the
  // ramp has it, 181 (R_CalcGammaRamp), and as it is again at brightness 1.
  const ramp = (v: number, gamma: number) => Math.round(Math.round(Math.pow(v / 255, 1 / gamma) * 65535) / 257);
  const settle = async (want: number, what: string) => {
    let c: number[] = [];
    for (let waited = 0; waited < 20_000; waited += 500) {
      c = await eve.middleColor();
      if (c.every((v) => Math.abs(v - want) <= 1)) return c;
      await sleep(500);
    }
    throw new Error(`${what}: the middle of the screen is ${c.join(' ')}, not ${want}`);
  };
  await eve.command('set cg_defaultFadeScreenColor 0.50196 0.50196 0.50196 1; fade 0 0 0 255 0 0 1');
  const grey = await settle(128, 'the screen faded to grey 128');
  await eve.command('r_gamma 2');
  await settle(ramp(grey[0], 2), 'at brightness 2, the screen faded to grey');
  await eve.command('r_gamma 1');
  await settle(grey[0], 'at brightness 1 again, the screen faded to grey');
  await eve.command('fade 0 0 0 0 0; reset cg_defaultFadeScreenColor');

  // the first game has the shadows off, as set in the lobby before it, and no frame rate limit, a PC's default; its
  // Graphics menu has the Frame Rate Limit where Screen Refresh Rate was (bo1-zombies/src/ui/ui_main.cpp:
  // UI_FrameLimit_PatchMenus), its choices limits up to the display's rate as the page measured it (60 where it
  // measured none), then Unlimited
  const were = await settings(eve);
  expect([were.sensitivity, were.f8], 'settings of an earlier visit').not.toEqual(['7', 'say kept']);
  expect(were.shadows, 'the shadows in the first game').toBe('0');
  expect(were.maxfps, 'the frame rate limit in the first game').toBe('0');
  const menu = eve.find(/^bo1_maxfps: menu options_graphics item [0-9]+ is the Frame Rate Limit, /) || '';
  const hz = Number(/\(the display ([0-9]+) Hz\)/.exec(menu)?.[1]) || 60;
  expect(menu, "the Graphics menu's Frame Rate Limit").toMatch(new RegExp(`^bo1_maxfps: menu options_graphics item ` +
    `[0-9]+ is the Frame Rate Limit, at 0:( [0-9]+)* ${hz} @MPUI_RULES_UNLIMITED_CAPS \\(the display [0-9]+ Hz\\), ` +
    '[1-9][0-9]* labels$'));
  // the engine writes the player's config in the frame after a setting changes (Com_WriteConfiguration)
  await eve.command('sensitivity 7');
  await eve.command('bind F8 "say kept"');
  await sleep(2000);
  await eve.state();
  eve.note(`in the game, ${Player.gb(await eve.memory(true))}`);

  // game over (loadgame_continue, which the level's game over comes to; Call of the Dead's outro rolls first, which
  // a key skips): the page comes back to the lobby, which says it was game over over the page for a moment
  // (web/src/notice.ts: showNotice), and offers to play alone again
  const before = eve.marks();
  const over = eve.next(/^lobby: the game ended \(over\): /, 120_000);
  await eve.command('loadgame_continue');
  expect(await over, 'game over ended the game').toBeTruthy();
  await expect.poll(() => eve.page.evaluate(() => {
    const n = document.getElementById('notice')!;
    return [n.classList.contains('hidden') || n.classList.contains('refusal') ? '' : n.textContent,
      document.getElementById('status')!.textContent, document.getElementById('playalone')!.textContent,
      document.getElementById('playalone')!.getAttribute('aria-disabled') !== 'true'];
  }), { message: 'the page is back in the lobby, which says it was game over', timeout: 60_000 })
    .toEqual([expect.stringMatching(/^Game over( on round \d+)?\.$/), '', 'Play', true]);
  const saidAt = Date.now();
  await eve.shot('over');
  await eve.inPlace('it was game over', before);
  await eve.page.waitForSelector('#notice.hidden', { state: 'attached',
    timeout: Math.max(1000, saidAt + 12_000 - Date.now()) });
  expect(await eve.fullScreen(), 'the page in full screen as the game ended').toBe(false);

  // The settings menu in the lobby (web/src/settings.ts) opens on its tabs alone, as the game's does, and shows the
  // sensitivity the game kept and the shadows off as set before it, in the game's own text (GET /strings); what it
  // showed before the first game from its own defaults it shows now from the config the game wrote, but for what the
  // player changed. Under the mouse it says the key that sets Full Screen and Show FPS in a game, and on a key's row
  // that a click changes it, then what it waits for. The player inverts the mouse, binds X to reload as well as R,
  // picks the aspect ratio 4:3 and the video mode after Auto (a standard 4:3 resolution below the screen's), turns
  // anti-aliasing off, and drags the field of view to its top. Nothing lies over Graphics' rows, and texture filtering
  // is greyed out at anisotropy 16 alone, where the web client filters trilinear whatever it says.
  await eve.page.click('#settings');
  await eve.page.waitForSelector('#options:not(.hidden) .om-side', { timeout: 30_000 });
  expect(await eve.page.locator('#options .om-label').count(), 'rows of the settings menu as it opened').toBe(0);
  const row = (name: string) => eve.page.locator(`.om-stage [data-row="${name}"]`).first();
  const shown = await menuValues(eve);
  await eve.page.click('.om-tab >> nth=0');
  await eve.page.click('.om-side >> nth=0');
  expect(shown.MENU_MOUSE_SENSITIVITY_CAPS, 'the sensitivity the menu shows').toBe('7');
  expect(await row('PLATFORM_FOV_CAPS').textContent(), "the field of view's name").toBe('FIELD OF VIEW');
  expect(shown.MENU_SHADOWS_CAPS, 'the shadows the menu shows').toBe('0');
  const drift = Object.keys(menuBefore).filter((name) => name in shown && shown[name] !== menuBefore[name] &&
    !['MENU_SHADOWS_CAPS', 'MENU_MOUSE_SENSITIVITY_CAPS'].includes(name) && !/\bF8\b/.test(shown[name]));
  expect(drift.map((name) => `${name} ${menuBefore[name]}, then ${shown[name]}`),
    'what the menu showed from its defaults, then from the config the game wrote').toEqual([]);
  await eve.shot('settings');
  const hint = () => eve.page.locator('.om-hint').textContent();
  await row('MENU_FULLSCREEN_CAPS').hover();
  const fullScreenKey = await hint();
  await eve.page.click('.om-side >> nth=3');
  await row('BO1_SHOW_FPS_CAPS').hover();
  expect([fullScreenKey, await hint()], 'what the menu says over Full Screen and Show FPS').toEqual([
    expect.stringMatching(/^In-game shortcut: (F11|Control\+Command\+F)$/), 'In-game shortcut: F9']);
  await eve.page.click('.om-tab >> nth=1');
  await eve.page.click('.om-side >> nth=0');
  await row('MENU_INVERT_MOUSE_CAPS').click();
  await eve.page.click('.om-side >> nth=2');
  const reload = row('MENU_RELOAD_WEAPON_CAPS');
  await reload.hover();
  const change = await hint();
  await reload.click();
  const wait = await hint();
  await eve.page.keyboard.press('x');
  await expect(eve.page.locator('.om-hint'), 'what a key\'s row says once bound').toHaveText('CLICK to change',
    { timeout: 5000 });
  expect([change, wait], "what a key's row says under the mouse, then clicked").toEqual(['CLICK to change',
    expect.stringMatching(/^Waiting for new key/)]);
  await eve.page.click('.om-tab >> nth=0');
  await eve.page.click('.om-side >> nth=0');
  const covered = await eve.page.evaluate(() => {
    const over: string[] = [];
    for (const e of document.querySelectorAll<HTMLElement>('.om-stage [data-row]')) {
      const r = e.getBoundingClientRect();
      for (const y of [r.top + 1, r.bottom - 1]) {
        const at = document.elementFromPoint(r.left + r.width / 2, y);
        if (!at || !e.contains(at)) over.push(`${e.dataset.row} under ${at ? at.className || at.tagName : 'nothing'}`);
      }
    }
    return over;
  });
  expect(covered, "what lies over Graphics' rows").toEqual([]);
  await row('MENU_ASPECT_RATIO_CAPS').click();
  await sleep(500);
  await row('MENU_VIDEO_MODE_CAPS').click();
  await sleep(500);
  const mode = await eve.page.locator('.om-value[data-row="MENU_VIDEO_MODE_CAPS"]').textContent() || '';
  const lines = await eve.page.evaluate(() => Math.round(screen.height * (window.devicePixelRatio || 1)));
  const [mw, mh] = (/^(\d+)x(\d+)$/.exec(mode) || []).slice(1).map(Number);
  expect(mw / mh === 4 / 3 && mh >= 600 && mh < lines, `the video mode ${mode} after Auto at 4:3, the screen ` +
    `${lines} ` +
    'lines').toBe(true);
  await row('PATCH_MENU_OPTION_SHADERWARMING_CAPS').hover();
  const why = await hint();
  await eve.page.mouse.move(5, 5);
  expect([!!why, await hint()], 'what the menu says over shader warming, greyed out, then off it').toEqual([true, '']);
  await row('MENU_ANTIALIASING_CAPS').click();
  await sleep(500);
  const slide = async (name: string, end: boolean) => {
    const bar = eve.page.locator(`.om-stage [data-row="${name}"] .om-bar`);
    const box = (await bar.boundingBox())!;
    await bar.click({ position: { x: end ? box.width - 1 : 1, y: box.height / 2 } });
    await sleep(500);
  };
  const filtering = async () => /\boff\b/.test(await eve.page.locator('.om-label[data-row="MENU_TEXTURE_MIPMAPS_CAPS"]')
    .getAttribute('class') || '');
  const at16 = await filtering();
  await slide('MENU_TEXTURE_ANISOTROPY_CAPS', false);
  const at1 = await filtering();
  await slide('MENU_TEXTURE_ANISOTROPY_CAPS', true);
  expect([at16, at1, await filtering()], 'texture filtering greyed out at anisotropy 16, 1 and 16').toEqual(
    [true, false, true]);
  await slide('PLATFORM_FOV_CAPS', true);
  const set = await menuValues(eve);
  expect([set.MENU_INVERT_MOUSE_CAPS, set.MENU_RELOAD_WEAPON_CAPS, set.PLATFORM_FOV_CAPS, set.MENU_ASPECT_RATIO_CAPS,
    set.MENU_VIDEO_MODE_CAPS, set.MENU_TEXTURE_ANISOTROPY_CAPS, set.MENU_ANTIALIASING_CAPS],
  'what the settings menu set').toEqual(['1', 'R X', '115', 'standard', mode, '16', '1']);
  await eve.shot('settings-set');
  await eve.page.click('.om-back');

  // Again: the game starts with the settings the first kept, and those set in the lobby, read from the browser's
  // store; the click that takes her into it goes full screen, as in her first (the page left full screen as that
  // ended). The picture is 4:3 at the video mode the menu showed, which the page sized the canvas to as the game
  // started (web/src/settings.ts: backBuffer), and which the engine took, its view 4:3 (wideScreen 0), the picture with
  // black bars at its sides in the page's 16:9 window.
  await eve.page.click('#playalone');
  await eve.spawned();
  await eve.page.mouse.click(640, 360);
  expect(await eve.captured(true, 5000), 'a click on the second game captured the mouse').toBe(true);
  expect(await eve.fullScreenWithin(5000), 'the click that took her into the second game went full screen').toBe(true);
  const kept = await settings(eve);
  expect([kept.sensitivity, kept.f8], 'the settings the first game kept').toEqual(['7', 'say kept']);
  expect(Number(kept.pitch), "the mouse's pitch, inverted").toBeLessThan(0);
  expect([kept.x, Number(kept.fov)], "X's binding, and the field of view").toEqual(['+reload', 115]);
  expect(await eve.page.evaluate(() => {
    const c = document.getElementById('canvas') as HTMLCanvasElement;
    return c.width + 'x' + c.height;
  }), "the canvas's size").toBe(mode);
  expect([kept.aspect, kept.mode, Number(kept.wide)], "the game's picture").toEqual(['standard', mode, 0]);
  expect([kept.aa, eve.antiAliasing], "the second game's anti-aliasing").toEqual(['1', 'no anti-aliasing']);
  await eve.shot('kept');
});
