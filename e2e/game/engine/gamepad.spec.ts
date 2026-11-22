// A player plays a map alone with a gamepad, as an Xbox controller is in Chrome (the standard mapping), and never
// clicks on the game. The gamepad is a simulated one (player.ts: PAD), which shows the page as one of its buttons is
// pressed, numbered after a device with no standard mapping, a wheel: the page gives the engine the gamepad, whatever
// Chrome numbers it, as an XInput one (bo1-zombies/web/src/library_bo1.js, web_input.cpp).
//
// In the lobby, the settings menu's Gamepad page goes live with the gamepad, shows the picture of each layout, and
// greys out as the gamepad goes (web/src/settings.ts); the gamepad alone then goes through the settings menu, which
// opens on its tabs alone as the game's does, and so do the mouse and the keyboard (gamepadMenu). In the game, his
// gamepad is off, as a new player's is: its first press, X on the pregame menu, turns it on and binds its layouts, and
// A skips the loading movie (play.ts: loadingMovie). Then the sticks walk and turn, the right trigger fires and rumbles
// the gamepad, Start pauses the game under its menu, the gamepad and the keyboard go through the game's menus
// (gamepadGame), the aim assist slows the view over a zombie and turns it on to one (gamepadAimAssist), and the knife
// lunges (gamepadLunge). From his spawn on he is unhurt (god), so that the zombies he waits for and turns to, which
// come and swing at him, never down him: a down would end his game, played alone.
import { expect, map, type Player, type PlayerState, test } from '../fixtures';
import { loadingMovie } from '../play';
import { PAD } from '../player';

const sleep = (ms: number) => new Promise((done) => setTimeout(done, ms));
const fail = (what: string): never => {
  throw new Error(what);
};
const degrees = (from: PlayerState, to: PlayerState) => Math.abs(((to.yaw - from.yaw + 540) % 360) - 180);
// the player as god keeps him from his spawn (gamepadGame): up, with a weapon; or the test fails at once
const up = (s: PlayerState, doing: string) => {
  if (s.pmType !== 0 || s.weapon === 'none') {
    fail(`a zombie downed the player ${doing}, whom god was to keep unhurt: pm_type ${s.pmType}, weapon ${s.weapon}`);
  }
};

interface MenuView {
  screen: number;
  page: string;
  tab: number;
  row: string;
  value: string;
  arrows: boolean;
  answer: number;
  labels: string;
}
interface View {
  pad: boolean;
  focus: string;
  picked: boolean;
  views: string;
  ask: boolean;
  starting: boolean;
  menu: MenuView | null;
}

// What the lobby shows, as the gamepad moves through it (web/src/padnav.ts: padNav; web/src/settings.ts: pad): whether
// the gamepad has the page (body.padnav), what has the focus (its id, or its text), the lobby's views shown, and the
// settings menu, open: its screen (0 Settings, 1 Controls), the page open ('' none), the tab the gamepad's cursor is
// on, the row (its name), its value and whether arrows go round it (a choice's), the popup's answer, and the gamepad
// picture's labels. Plain JavaScript, as padStep runs its source in the page.
function padView(): string {
  const a = document.activeElement;
  const menu = document.getElementById('options');
  const open = !!menu && !menu.classList.contains('hidden');
  const lit = open ? menu!.querySelector('.om-label.lit') as HTMLElement | null : null;
  const choice = lit && lit.nextElementSibling && lit.nextElementSibling.querySelector('.om-choice');
  return JSON.stringify({
    pad: document.body.classList.contains('padnav'),
    focus: a && a !== document.body ? a.id || (a.textContent || '').trim().slice(0, 40) : '',
    picked: !!a && a.getAttribute('aria-checked') === 'true',
    views: ['outside', 'solo', 'inside', 'results'].filter((id) => !document.getElementById(id)!.classList
      .contains('hidden')).join(),
    ask: !document.getElementById('ask')!.classList.contains('hidden'),
    starting: /^Starting in/.test(document.getElementById('ready')!.textContent || ''),
    menu: open ? {
      screen: [...menu!.querySelectorAll('.om-tab')].findIndex((t) => t.classList.contains('on')),
      page: ((menu!.querySelector('.om-pagetitle') || { dataset: {} }) as HTMLElement).dataset.page || '',
      tab: [...menu!.querySelectorAll('.om-side')].findIndex((t) => t.classList.contains('pad')),
      row: lit ? lit.dataset.row : '',
      value: lit && lit.nextElementSibling ? (lit.nextElementSibling as HTMLElement).dataset.value || '' : '',
      arrows: !!choice && getComputedStyle(choice, '::before').content !== 'none' &&
        getComputedStyle(choice, '::after').content !== 'none',
      answer: [...menu!.querySelectorAll('.om-answer')].findIndex((t) => t.classList.contains('pad')),
      labels: [...menu!.querySelectorAll('.om-pad-label')].map((l) => l.textContent).join(),
    } : null,
  });
}

// A press of a gamepad's button in the lobby, held for one of the page's frames, as the page reads the gamepad at the
// start of each, then let go: one press, one move. What the lobby shows once that changes (padView), within 5 s.
async function padStep(p: Player, b: number): Promise<View & { menu: MenuView }> {
  return JSON.parse(await p.page.evaluate(async ([b, viewSource]) => {
    const view = new Function('return (' + viewSource + ')()') as () => string;
    const frame = () => new Promise((done) => requestAnimationFrame(done));
    const before = view();
    window.bo1Pad.button(b, 1);
    await frame();
    window.bo1Pad.button(b, 0);
    for (const t0 = performance.now(); view() === before && performance.now() - t0 < 5000;) await frame();
    const after = view();
    // a frame for the page to read it let go, before another press: one in the same frame would be no new press
    await frame();
    await frame();
    return after;
  }, [b, padView.toString()] as [number, string]));
}

// a press of a key of the keyboard in the lobby: what it shows once that changes, within 5 s
async function keyStep(p: Player, key: string): Promise<View & { menu: MenuView }> {
  const before = await p.page.evaluate(padView);
  await p.page.keyboard.press(key);
  let now = before;
  for (const t0 = Date.now(); now === before && Date.now() - t0 < 5000; await sleep(50)) {
    now = await p.page.evaluate(padView);
  }
  return JSON.parse(now);
}

// presses of a gamepad's button until what the lobby shows passes the test, at most n: what it shows then, or false
async function padStepsUntil(p: Player, b: number, test: (v: View & { menu: MenuView }) => boolean, n: number,
  path: string[]): Promise<(View & { menu: MenuView }) | false> {
  for (let i = 0; i < n; i++) {
    const v = await padStep(p, b);
    path.push(JSON.stringify(v.menu ? v.menu : v.focus));
    if (test(v)) return v;
  }
  return false;
}

// The settings menu's Gamepad tab is greyed out until Chrome shows the page a gamepad, at a press of one of its
// buttons; its page then shows the layout chosen under the mouse, as the game's does: the controller with the
// buttons' labels and the lines to them for Button Layout, each of its 5 as a click picks it, and the sticks' for
// Stick Layout (web/src/settings.ts: padPicture). It greys out again as the gamepad goes, and comes back with it.
async function gamepadSettings(p: Player): Promise<void> {
  await p.page.click('#settings');
  await p.page.waitForSelector('#options:not(.hidden) .om-side', { timeout: 30_000 });
  await p.page.click('.om-tab >> nth=1');
  const tabLive = (live: boolean) => p.page.waitForFunction((live) => {
    const tab = document.querySelectorAll('#options .om-side')[5];
    return !!tab && tab.classList.contains('off') !== live;
  }, live, { timeout: 5000 }).then(() => true, () => false);
  if (!(await tabLive(false))) fail("the settings menu's Gamepad tab is live before Chrome showed the page a gamepad");
  await p.padPress(PAD.A);
  if (!(await tabLive(true))) {
    fail("the settings menu's Gamepad tab did not go live as Chrome showed the page the gamepad");
  }
  await p.page.click('#options .om-side >> nth=5');
  const row = (name: string) => p.page.locator(`#options .om-label[data-row="${name}"]`);
  const picture = () => p.page.evaluate(() => ({
    images: [...document.querySelectorAll<HTMLImageElement>('#options img.om-pad')]
      .map((i) => (i.complete && i.naturalWidth > 0 ? i.src.replace(/.*\/images\//, '') : 'unloaded ' + i.src)),
    labels: [...document.querySelectorAll('#options .om-pad-label')].map((l) => l.textContent),
    lines: document.querySelectorAll('#options .om-pad-lines line').length,
  }));
  const layouts = new Set<string>();
  await row('PLATFORM_BUTTON_LAYOUT_CAPS').hover();
  for (let i = 0; i < 5; i++) {
    await sleep(1000);
    const shown = await picture();
    expect([shown.images.join(), shown.lines, shown.labels.length], 'Button Layout under the mouse').toEqual(
      ['xenon_controller_top.png', 14, 12]);
    layouts.add(shown.labels.join());
    if (i === 0) await p.shot('settings-gamepad-buttons');
    await row('PLATFORM_BUTTON_LAYOUT_CAPS').click(); // the next layout; the fifth click is back at Default
  }
  expect(layouts.size, 'sets of labels the 5 button layouts showed').toBe(5);
  await row('PLATFORM_STICK_LAYOUT_CAPS').hover();
  await sleep(1000);
  const sticks = await picture();
  expect([sticks.images.join(), sticks.lines, sticks.labels.length], 'Stick Layout under the mouse').toEqual(
    ['xenon_controller_top.png,xenon_stick_move.png,xenon_stick_turn.png', 0, 6]);
  await p.shot('settings-gamepad-sticks');
  await p.page.evaluate(() => window.bo1Pad.plug(false));
  if (!(await tabLive(false))) fail("the settings menu's Gamepad tab did not grey out as the gamepad went");
  const gone = await picture();
  expect([gone.images.length, gone.labels.length], 'the Gamepad page, greyed out').toEqual([0, 0]);
  await p.page.evaluate(() => window.bo1Pad.plug(true));
  if (!(await tabLive(true))) fail("the settings menu's Gamepad tab did not go live as the gamepad came back");
  await p.page.click('#options .om-back');
  p.note("the Gamepad settings went live with the gamepad, showed each layout's picture, and greyed out as it went");
}

// The settings menu with the gamepad alone (web/src/settings.ts: pad). The mouse moves, which takes the page from the
// gamepad: the gamepad's first press then only shows the focus, on the settings button; A opens the menu on its tabs
// alone, as the game's opens, with the cursor on the first at once. A on it opens Graphics on its first row, and up
// goes round to its last, Field of View, at 80: right moves it a block, to 85, and left back, and B closes the page,
// back to the tabs alone, the cursor on its tab. Up goes round to the last tab, Default System Settings: A on it asks,
// with No marked, and A says no, the cursor back on the tab; A asks again, and B says no. Right on the tab switches to
// Controls, on the tab at its place, Gamepad, LB back, and RB to Controls again; the d-pad goes round its tabs to
// Gamepad, and A opens its page on its first row, Controller Vibration: left turns it down a twentieth, which the
// gamepad feels, and right back up. A move the browser makes up, the mouse still, leaves the cursor where it is. The
// d-pad goes down to Button Layout, which shows its picture and arrows round its choice, right picks the next layout
// and left the last again; on down to Aim Assist, the last row: A turns it off, and A on again. B closes the page, the
// cursor on its tab; Start closes the menu, and opens it again, on its tabs alone, where B closes it. A click opens it
// again, and one on Graphics its page: Esc, the mouse over a row, closes the page, and Esc again the menu. Then the
// keyboard, in the menu a click opened: its first press only shows the cursor, on Graphics; the arrows, Enter and Esc
// go as the d-pad, A and B do: Esc on a row of Sound closes its page, on its tab; in Controls, the left arrow on a
// key's row of Move, which has nothing for it, switches to Settings, as on a tab; and Esc on the tabs closes the menu.
async function gamepadMenu(p: Player): Promise<void> {
  await p.page.mouse.move(40, 40);
  await p.page.mouse.move(2, 2);
  await p.page.focus('#settings');
  const path: string[] = [];
  const say = (v: unknown) => JSON.stringify(v);
  let v = await padStep(p, PAD.A);
  if (!v.pad || v.menu || v.focus !== 'settings') {
    fail(`the gamepad's first press after the mouse did not just show the focus on the settings button: ${say(v)}`);
  }
  v = await padStep(p, PAD.A);
  if (!v.menu || v.menu.page || v.menu.tab !== 0) {
    fail('A on the settings button did not open the settings menu on its tabs alone, the cursor on Graphics: ' +
      say(v));
  }
  v = await padStep(p, PAD.A);
  if (v.menu.page !== 'graphics' || v.menu.row !== 'MENU_VIDEO_MODE_CAPS') {
    fail(`A on Graphics did not open its page on its first row, Video Mode: ${say(v)}`);
  }
  v = await padStep(p, PAD.UP);
  if (v.menu.row !== 'PLATFORM_FOV_CAPS' || v.menu.value !== '80') {
    fail(`A on Graphics, then up, did not go round to its last row, Field of View, at 80: ${say(v)}`);
  }
  v = await padStep(p, PAD.RIGHT);
  if (v.menu.value !== '85') fail(`right on Field of View did not move it a block, to 85: ${say(v)}`);
  v = await padStep(p, PAD.LEFT);
  if (v.menu.value !== '80') fail(`left on Field of View did not move it back to 80: ${say(v)}`);
  v = await padStep(p, PAD.B);
  if (v.menu.row || v.menu.tab !== 0 || v.menu.page) {
    fail(`B on Field of View did not close Graphics, back to the tabs alone with the cursor on its tab: ${say(v)}`);
  }
  v = await padStep(p, PAD.UP);
  if (v.menu.tab !== 4) fail(`up on Graphics did not go round to Default System Settings: ${say(v)}`);
  v = await padStep(p, PAD.A);
  if (v.menu.answer !== 1 || v.menu.page) {
    fail(`A on Default System Settings did not ask, over the tabs alone, with No marked: ${say(v)}`);
  }
  await p.shot('settings-gamepad-popup');
  v = await padStep(p, PAD.A);
  if (v.menu.answer !== -1 || v.menu.tab !== 4) {
    fail(`A on the popup's No did not close it, back on Default System Settings: ${say(v)}`);
  }
  v = await padStep(p, PAD.A);
  if (v.menu.answer !== 1) fail(`A on Default System Settings did not ask again, with No marked: ${say(v)}`);
  v = await padStep(p, PAD.B);
  if (v.menu.answer !== -1 || v.menu.tab !== 4) {
    fail(`B on the popup did not close it, back on Default System Settings: ${say(v)}`);
  }
  v = await padStep(p, PAD.RIGHT);
  if (v.menu.screen !== 1 || v.menu.tab !== 5 || v.menu.page) {
    fail(`right on Default System Settings did not switch the settings menu to Controls, its tabs alone, on ` +
      `Gamepad: ${say(v)}`);
  }
  v = await padStep(p, PAD.LB);
  if (v.menu.screen !== 0 || v.menu.tab !== 4) {
    fail(`LB did not switch the settings menu back to Settings, on Default System Settings: ${say(v)}`);
  }
  v = await padStep(p, PAD.RB);
  if (v.menu.screen !== 1) fail(`RB did not switch the settings menu to Controls again: ${say(v)}`);
  if (!(await padStepsUntil(p, PAD.DOWN, (w) => w.menu.tab === 5, 8, path)) || path.length !== 7) {
    fail(`the d-pad did not go round the tabs to Gamepad: ${path.join(' ')}`);
  }
  v = await padStep(p, PAD.A);
  if (v.menu.row !== 'MENU_CONTROLLER_VIBRATION_CAPS' || v.menu.value !== '1') {
    fail(`A on the Gamepad tab did not open its page on Controller Vibration, at 1: ${say(v)}`);
  }
  const felt = await p.page.evaluate(() => window.bo1Pad.rumbles.length);
  v = await padStep(p, PAD.LEFT);
  const rumble = await p.page.evaluate((n) => window.bo1Pad.rumbles.slice(n), felt);
  if (v.menu.value !== '0.95' || !rumble.some((r) => Math.abs((r.strongMagnitude ?? 0) - 0.95) < 1e-6)) {
    fail(`left on Controller Vibration did not turn it down to 0.95, felt: ${say(v)}, ${say(rumble)}`);
  }
  v = await padStep(p, PAD.RIGHT);
  if (v.menu.value !== '1') fail(`right on Controller Vibration did not turn it back up to 1: ${say(v)}`);
  const still = JSON.parse(await p.page.evaluate((viewSource) => {
    document.querySelector('#options .om-label.lit')!.dispatchEvent(new MouseEvent('mousemove', { bubbles: true }));
    return (new Function('return (' + viewSource + ')()') as () => string)();
  }, padView.toString()));
  if (!still.pad || still.menu.row !== 'MENU_CONTROLLER_VIBRATION_CAPS') {
    fail(`a mouse move the browser made up, the mouse still, took the settings menu from the gamepad: ${say(still)}`);
  }
  const layout = await padStepsUntil(p, PAD.DOWN, (w) => w.menu.row === 'PLATFORM_BUTTON_LAYOUT_CAPS', 8, path);
  if (!layout) return fail(`the d-pad did not reach Button Layout: ${path.join(' ')}`);
  if (layout.menu.labels.split(',').length !== 12) {
    fail(`Button Layout, with the cursor on it, shows no picture: ${say(layout)}`);
  }
  if (!layout.menu.arrows) fail(`Button Layout, with the cursor on it, has no arrows round its choice: ${say(layout)}`);
  await p.shot('settings-gamepad-cursor');
  const first = layout.menu.labels;
  v = await padStep(p, PAD.RIGHT);
  if (v.menu.labels === first) fail('right on Button Layout did not pick the next layout');
  v = await padStep(p, PAD.LEFT);
  if (v.menu.labels !== first) fail(`left on Button Layout did not pick the last layout again: ${say(v)}`);
  const assist = await padStepsUntil(p, PAD.DOWN, (w) => w.menu.row === 'MENU_AIM_ASSIST_CAPS', 3, path);
  if (!assist || assist.menu.value !== '1' || assist.menu.labels) {
    fail(`the d-pad did not go on down to Aim Assist, Enabled, without the controller's picture: ${path.join(' ')}`);
  }
  v = await padStep(p, PAD.A);
  if (v.menu.value !== '0') fail(`A on Aim Assist did not turn it off: ${say(v)}`);
  v = await padStep(p, PAD.A);
  if (v.menu.value !== '1') fail(`A on Aim Assist again did not turn it on: ${say(v)}`);
  v = await padStep(p, PAD.B);
  if (!v.menu || v.menu.row || v.menu.page || v.menu.tab !== 5) {
    fail(`B on Aim Assist did not close the Gamepad page, back to the tabs alone, the cursor on its tab: ${say(v)}`);
  }
  v = await padStep(p, PAD.START);
  if (v.menu || v.focus !== 'settings') fail(`Start did not close the settings menu: ${say(v)}`);
  v = await padStep(p, PAD.START);
  if (!v.menu || v.menu.page || v.menu.tab !== 0) {
    fail(`Start did not open the settings menu on its tabs alone, with the cursor on Graphics: ${say(v)}`);
  }
  v = await padStep(p, PAD.B);
  if (v.menu || v.focus !== 'settings') fail(`B on the tabs, no page open, did not close the settings menu: ${say(v)}`);
  // the mouse, in the menu it opened, once it shows (it reads the player's settings first): no page until a tab's click
  await p.page.click('#settings');
  await p.page.waitForSelector('#options:not(.hidden) .om-side', { timeout: 5000 });
  await p.page.hover('#options .om-side >> nth=0');
  await sleep(500);
  v = JSON.parse(await p.page.evaluate(padView));
  if (v.menu.page || (await p.page.locator('#options .om-label').count())) {
    fail(`the settings menu shows a page with the mouse over Graphics, before a click on it: ${say(v)}`);
  }
  await p.page.click('#options .om-side >> nth=0');
  await p.page.hover('#options .om-label:visible >> nth=0');
  v = await keyStep(p, 'Escape');
  if (!v.menu || v.menu.page || v.pad) {
    fail(`Esc, the mouse over a row of Graphics, did not close its page alone: ${say(v)}`);
  }
  v = await keyStep(p, 'Escape');
  if (v.menu || v.pad) fail(`Esc, the mouse on the menu with no page open, did not close it: ${say(v)}`);
  // the keyboard
  await p.page.click('#settings');
  await p.page.waitForSelector('#options:not(.hidden)', { timeout: 5000 });
  v = await keyStep(p, 'ArrowDown');
  if (!v.pad || !v.menu || v.menu.tab !== 0) {
    fail(`the keyboard's first press in the settings menu did not just show the cursor on Graphics: ${say(v)}`);
  }
  v = await keyStep(p, 'ArrowDown');
  if (v.menu.tab !== 1) fail(`the down arrow on Graphics did not go to Sound: ${say(v)}`);
  v = await keyStep(p, 'Enter');
  if (v.menu.row !== 'PLATFORM_VOICE_VOLUME_CAPS') fail(`Enter on Sound did not open its first row: ${say(v)}`);
  v = await keyStep(p, 'Escape');
  if (!v.menu || v.menu.row || v.menu.page || v.menu.tab !== 1) {
    fail(`Esc on a row of Sound did not close its page, back to the tabs alone, the cursor on its tab: ${say(v)}`);
  }
  v = await keyStep(p, 'ArrowRight');
  if (v.menu.screen !== 1 || v.menu.tab !== 1) {
    fail(`the right arrow on Sound's tab did not switch the menu to Controls, on the tab at its place: ${say(v)}`);
  }
  v = await keyStep(p, 'Enter');
  if (v.menu.page !== 'move' || v.menu.row !== 'MENU_FORWARD_CAPS') {
    fail(`Enter on Move did not open its first row: ${say(v)}`);
  }
  v = await keyStep(p, 'ArrowLeft');
  if (v.menu.screen !== 0 || v.menu.row || v.menu.page || v.menu.tab !== 0) {
    fail(`the left arrow on a key's row of Move did not switch the menu to Settings, its tabs alone, the cursor on ` +
      `the first: ${say(v)}`);
  }
  v = await keyStep(p, 'Escape');
  if (v.menu || v.focus !== 'settings') {
    fail(`Esc on the tabs, no page open, did not close the settings menu: ${say(v)}`);
  }
  p.note('the gamepad, the mouse and the keyboard went through the settings menu');
}

// The right stick held as a player holds it, set again at each of the engine's frames (bo1_player answers once a
// frame), and let go once aim gives null for the player now, or after ms: the stick's value (-1 to 1, right), or
// another axis's (the left stick's down, LY: back). The player as it was let go, the frames it was held for and the
// degrees it turned the view: a frame counts 100 ms of game time at most (com_maxFrameTime), and a busy machine draws
// fewer of them in a push of a fixed length.
async function holdStick(p: Player, aim: (s: PlayerState) => number | null, ms: number, axis: number = PAD.RX):
  Promise<{ s: PlayerState; frames: number; turned: number }> {
  const from = await p.state();
  const until = Date.now() + ms;
  let s = from;
  let frames = 0;
  try {
    for (let value; Date.now() < until && (value = aim(s)) !== null; frames++) {
      await p.page.evaluate(([a, v]) => window.bo1Pad.axis(a, v), [axis, value]);
      s = await p.state();
    }
  } finally {
    await p.page.evaluate((a) => window.bo1Pad.axis(a, 0), axis);
  }
  return { s, frames, turned: degrees(from, s) };
}

// The right stick turns the view to a zombie, the one nearest the crosshair or the one which picks, until it is within
// `within` of the middle across, or none is on screen, up to ms: as far over as the zombie is from the middle, times a
// gain that halves as the view goes past it and grows after a frame that barely closed in.
function steerToZombie(p: Player, within: number, ms: number, which = (s: PlayerState) => s.nearest) {
  let gain = 2;
  let lastX: number | null = null;
  return holdStick(p, (s) => {
    if (!s.aimTargets || Math.abs(which(s).x) < within) return null;
    const x = which(s).x;
    if (lastX !== null && Math.sign(lastX) !== Math.sign(x)) gain = Math.max(0.5, gain / 2);
    else if (lastX !== null && Math.abs(lastX) - Math.abs(x) < 0.01) gain = Math.min(8, gain * 1.5);
    lastX = x;
    return Math.sign(x) * Math.min(1, Math.max(0.15, gain * Math.abs(x)));
  }, ms);
}

// The game, with the gamepad: the left stick walks and back, the right one turns, the right trigger fires and rumbles
// the gamepad (bo1-zombies/src/cgame/cg_rumble.cpp); Start opens the game's menu, which pauses the game and stills the
// gamepad, B closes it, and so does Start, and the scoreboard neither fades under it nor shows as it closes
// (cg_sp_hud.cpp: CG_SP_RefreshAllHudFades). The menu opens on its first item; A picks it, and the d-pad and the left
// stick move through it (cl_gamepad.cpp: CL_GamepadGenerateAPad). Settings opens on its first tab; the d-pad goes up
// and down the tabs or a page's rows, A on a tab opens its page on its first row, and B closes the page
// (UI_PadKeyRuns); the keyboard goes the same way. Graphics' first row is Full Screen, the browser's
// (UI_WebFullScreen_PatchMenus); then the Frame Rate Limit (UI_FrameLimit_PatchMenus), the field of view
// (Item_Slider_HandleKey); the Game page has Show FPS (UI_ShowFps_PatchMenus); the Gamepad page Controller Vibration
// and Aim Assist (UI_GamepadRows_PatchMenus); right on a tab, as LB and RB, switches between Settings and Controls
// (UI_PadArrowKey). The arrow hides over the game while the gamepad plays. The keyboard alone, the gamepad off, goes
// into a key's row of Controls, where the left arrow switches to Settings, as the PC exe's did, and Esc, the mouse on
// the row, closes the page; the mouse finds the last tab of the Graphics page and of the Game page. Unplugged, the
// gamepad turns off; plugged in again, a press turns it on.
async function gamepadGame(p: Player): Promise<void> {
  const spawn = await p.spawned();
  p.note(`spawned alone at ${spawn.origin.join(' ')} with ${spawn.weapon}, ${spawn.clip}/${spawn.stock}`);
  // From here the player is unhurt (god), as the zombies come for him while the test turns the view and waits for
  // them, and a down ends a game played alone. A map played alone has cheats on (bo1-zombies/src/server_mp/
  // sv_ccmds_mp.cpp: SV_Map_f). The zombies still come and swing at him, and push him; he takes no damage (G_Damage
  // returns for god before the damage callback).
  expect(await p.dvar('sv_cheats'), 'cheats in the game played alone, which god needs').toBe('1');
  await p.command('god');
  p.note('god: the zombies do the player no harm');
  expect(await p.dvar('gpad_enabled'), 'the gamepad, once spawned').toBe('1');
  // the left stick forward (up: negative) for 3 s, up to 8 times: the map's intro holds the player still after
  // spawning, the screen black
  let walked: PlayerState | null = null;
  for (let i = 1; ; i++) {
    const start = await p.state();
    await p.padPush(PAD.LY, -1, 3000);
    await sleep(1000);
    walked = await p.state();
    const distance = Math.hypot(walked.origin[0] - start.origin[0], walked.origin[1] - start.origin[1]);
    if (distance >= 32) {
      p.note(`the left stick walked ${distance.toFixed(0)} units (push ${i})`);
      walked = await p.walkBack(start, () => p.page.evaluate((a) => window.bo1Pad.axis(a, 1), PAD.LY),
        () => p.page.evaluate((a) => window.bo1Pad.axis(a, 0), PAD.LY));
      break;
    }
    if (i === 8) {
      fail(`8 pushes of the left stick forward, 3 s each: the last moved the player ${distance.toFixed(0)} units`);
    }
  }
  // the right stick held right for 1.5 s, and 3 of the engine's frames at least
  const holdFrom = Date.now();
  let holdFrames = 0;
  const turn = await holdStick(p, () => (holdFrames++ < 3 || Date.now() - holdFrom < 1500 ? 1 : null), 30_000);
  await sleep(1000);
  const turned = await p.state();
  expect(degrees(walked!, turned), `degrees the right stick turned the player in ${turn.frames} frames`)
    .toBeGreaterThanOrEqual(10);
  // the shot rumbles the gamepad: the weapon's rumble, which the page plays on it
  const rumbles = () => p.page.evaluate(() => window.bo1Pad.rumbles.filter((r) =>
    (r as { type?: string }).type === 'dual-rumble' && (r.strongMagnitude ?? 0) + (r.weakMagnitude ?? 0) > 0).length);
  const rumbledBefore = await rumbles();
  for (let i = 1; ; i++) {
    await p.padPress(PAD.RT);
    await sleep(1000);
    const fired = await p.state();
    if (fired.clip < turned.clip) break;
    if (i === 5) fail(`5 pulls of the right trigger left ${fired.clip} of ${turned.clip} rounds in the clip`);
  }
  expect((await rumbles()) - rumbledBefore, 'rumbles of the gamepad as the right trigger fired').toBeGreaterThan(0);
  const cursor = () => p.page.evaluate(() => document.getElementById('canvas')!.style.cursor);
  expect(await cursor(), 'the arrow over the game as the gamepad plays').toBe('none');
  // Start at once after a shot, its rumble playing: the game, played alone, pauses under its menu, its clock with
  // it, and the gamepad stops rumbling while it is paused (CG_UpdateRumble). The page plays a rumble again every
  // 100 ms while it runs (library_bo1.js).
  const felt = (from: number) => p.page.evaluate((from) => window.bo1Pad.rumbles.filter((r) =>
    (r as { type?: string }).type === 'dual-rumble' && (r.strongMagnitude ?? 0) + (r.weakMagnitude ?? 0) > 0 &&
    r.at >= from).length, from);
  await p.padPress(PAD.RT, 150);
  if (!(await p.padPressUntil(PAD.START, /^input: the mouse is a menu's$/))) fail("Start did not open the game's menu");
  await sleep(1000);
  const pausedAt = await p.page.evaluate(() => performance.now());
  await sleep(2000);
  expect(await felt(pausedAt), "rumbles under the paused game's menu").toBe(0);
  await p.shot('gamepad-menu');
  expect((await p.state()).scores, "the scoreboard's fade under the paused game's menu").toBe(false);
  if (!(await p.padPressUntil(PAD.B, /^input: the mouse turns the view$/))) fail("B did not close the game's menu");
  expect((await p.state()).scores, "the scoreboard as B closed the game's menu").toBe(false);
  await sleep(1000);
  if (!(await p.padPressUntil(PAD.START, /^input: the mouse is a menu's$/))) fail("Start did not open the game's menu");
  await sleep(1500);
  const underAgain = await p.state();
  if (!(await p.padPressUntil(PAD.START, /^input: the mouse turns the view$/))) {
    fail("Start did not close the game's menu");
  }
  expect([underAgain.scores, (await p.state()).scores], "the scoreboard under the menu, and as Start closed it")
    .toEqual([false, false]);
  // the game's console says what has the focus in its menu (bo1_menufocus)
  const menuFocus = async () => {
    const line = p.next(/^menu focus: /, 5000);
    await p.command('bo1_menufocus');
    const said = (await line) || '';
    const m = /^menu focus: (\S+) item (-?\d+) at (\S+) (\S+) /.exec(said);
    return { line: said, menu: m ? m[1] : '', item: m ? Number(m[2]) : -1, x: m ? Number(m[3]) : NaN,
      y: m ? Number(m[4]) : NaN };
  };
  // The menu opens on its first item, Resume Carnage, which keeps the focus with the mouse still, off the menu's
  // items (ui_shared.cpp: Display_MouseMove), and A picks it, which closes the menu.
  await sleep(1000);
  if (!(await p.padPressUntil(PAD.START, /^input: the mouse is a menu's$/))) fail("Start did not open the game's menu");
  await sleep(1500);
  const opened = await menuFocus();
  expect(opened.item, `the item focused in the game's menu opened with Start (${opened.line})`)
    .toBeGreaterThanOrEqual(0);
  if (!(await p.padPressUntil(PAD.A, /^input: the mouse turns the view$/))) {
    fail("A did not pick the menu's first item");
  }

  await sleep(1000);
  if (!(await p.padPressUntil(PAD.START, /^input: the mouse is a menu's$/))) fail("Start did not open the game's menu");
  await sleep(1500);
  const first = await menuFocus();
  // A press the game sees once: held until the engine has read the gamepad with it down, then let go until it has
  // read it up (a console line is taken after the frame's read, so the second of two lines is a frame's after the
  // press); a menu's items animate, and take no key until they are done: a second after, what has the focus.
  const seen = async () => {
    await p.page.evaluate(() => new Promise<void>((done) => requestAnimationFrame(() => done())));
    await menuFocus();
    await menuFocus();
  };
  const tap = async (set: (v: number) => Promise<void>) => {
    await set(1);
    await seen();
    await set(0);
    await seen();
    await sleep(1000);
    return menuFocus();
  };
  const pressed = (b: number) => tap((v) => p.page.evaluate(([b, v]) => window.bo1Pad.button(b, v), [b, v]));
  const typed = (k: string) => tap((v) => (v ? p.page.keyboard.down(k) : p.page.keyboard.up(k)));
  const second = await tap((v) => p.page.evaluate(([a, v]) => window.bo1Pad.axis(a, v), [PAD.LY, v]));
  if (second.item < 0 || second.item === first.item) {
    fail(`the left stick down did not move from the game's menu's first item: ${first.line}, then ${second.line}`);
  }
  const graphics = await pressed(PAD.A);
  if (graphics.menu !== 'options_new_pc' || graphics.item < 0) fail(`A on Settings did not open it: ${graphics.line}`);
  const row = await pressed(PAD.A);
  if (row.menu !== 'options_graphics' || !(row.x > graphics.x)) {
    fail(`A on the Graphics tab did not open its page on its first row: ${graphics.line}, then ${row.line}`);
  }
  await p.shot('gamepad-menu-settings');
  // Graphics' first row is Full Screen, live in a game: right turns it off, the page leaving full screen, and right
  // again on, the page going back (library_bo1.js: followEngine)
  const fullScreenWas = [await p.dvar('bo1_web_fullscreen'), await p.fullScreen()];
  await pressed(PAD.RIGHT);
  const fullScreenOff = [await p.dvar('bo1_web_fullscreen'), await p.fullScreenWithin(0)];
  await pressed(PAD.RIGHT);
  const fullScreenOn = [await p.dvar('bo1_web_fullscreen'), await p.fullScreenWithin(5000)];
  expect([row.y, fullScreenWas, fullScreenOff, fullScreenOn], `Full Screen (${row.line}): the setting and the page`)
    .toEqual([-96, ['1', true], ['0', false], ['1', true]]);
  // the Frame Rate Limit: none by default on a PC, the last of its choices, Unlimited; right goes round to 30, and
  // left back to none. At 1 a second, set on the console, the page takes 1 frame a second of the game
  // (d3d9_webgpu.cpp: Frame), as it says twice a second (Module.bo1Perf), where it took more without one.
  const next = await pressed(PAD.DOWN);
  const limitWas = await p.dvar('bo1_maxfps');
  await pressed(PAD.RIGHT);
  const limitFirst = await p.dvar('bo1_maxfps');
  await pressed(PAD.LEFT);
  expect([next.y, limitWas, limitFirst, await p.dvar('bo1_maxfps')], `the Frame Rate Limit (${next.line})`)
    .toEqual([-39, '0', '30', '0']);
  const shownPerSecond = (ms: number) => p.page.evaluate((ms) => new Promise<number[]>((done) => {
    const m = window.Module as unknown as { bo1Perf?: (perf: { fps: number }) => void };
    const was = m.bo1Perf;
    const said: number[] = [];
    m.bo1Perf = (perf) => {
      said.push(perf.fps);
      if (was) was(perf);
    };
    setTimeout(() => {
      m.bo1Perf = was;
      done(said.slice(1));
    }, ms);
  }), ms);
  const mean = (a: number[]) => (a.length ? a.reduce((s, v) => s + v, 0) / a.length : NaN);
  const free = mean(await shownPerSecond(5000));
  await p.command('bo1_maxfps 1');
  await sleep(1000);
  const held = mean(await shownPerSecond(6000));
  await p.command('bo1_maxfps 0');
  expect(held, 'frames a second the page took at a limit of 1').toBeLessThanOrEqual(1.4);
  expect(held, 'frames a second at a limit of 1, and without one').toBeLessThan(free);
  const up = await pressed(PAD.UP);
  const round = await pressed(PAD.UP);
  if (next.x !== row.x || !(next.y > row.y) || up.item !== row.item || round.x !== row.x || !(round.y > next.y)) {
    fail(`the d-pad did not go down Graphics' rows, and up round to the last: ${row.line}, then ${next.line}, then ` +
      `${up.line}, then ${round.line}`);
  }
  const fov = await p.dvar('bo1_fov');
  await pressed(PAD.RIGHT);
  const fovUp = await p.dvar('bo1_fov');
  await pressed(PAD.LEFT);
  expect([round.y, fov, fovUp, await p.dvar('bo1_fov')], `the field of view (${round.line})`)
    .toEqual([171, '80', '85', '80']);
  // B on a row closes its page, back to Settings' tabs alone, on the page's tab; A opens it again on its first row
  const tab = await pressed(PAD.B);
  const into = await pressed(PAD.A);
  const back = await pressed(PAD.B);
  if (tab.menu !== 'options_new_pc' || tab.x !== graphics.x || tab.y !== graphics.y ||
    into.menu !== 'options_graphics' || into.item !== row.item || back.menu !== 'options_new_pc' ||
    back.item !== tab.item) {
    fail(`B on a row of Graphics, then A and B: ${tab.line}, then ${into.line}, then ${back.line}`);
  }
  // up from the first tab goes round to the last, Default System Settings, and down back to it
  const lastTab = await pressed(PAD.UP);
  const firstTab = await pressed(PAD.DOWN);
  if (lastTab.x !== tab.x || lastTab.y !== -39 || firstTab.item !== tab.item) {
    fail(`up from the Graphics tab, then down: ${tab.line}, then ${lastTab.line}, then ${firstTab.line}`);
  }
  const sound = await pressed(PAD.DOWN);
  const soundRow = await pressed(PAD.A);
  if (sound.x !== tab.x || !(sound.y > tab.y) || soundRow.menu !== 'options_sound' || !(soundRow.x > sound.x)) {
    fail(`A on the Sound tab did not open its page on its first row: ${sound.line}, then ${soundRow.line}`);
  }
  const soundBack = await pressed(PAD.B);
  const voice = await pressed(PAD.DOWN);
  const voiceRow = await pressed(PAD.A);
  const kept = await pressed(PAD.DOWN);
  if (soundBack.menu !== 'options_new_pc' || soundBack.item !== sound.item || voice.x !== tab.x ||
    voiceRow.menu !== 'options_voice' || !(voiceRow.x > voice.x) || kept.item !== voiceRow.item) {
    fail(`B on Sound's row, then Voice Chat's one row: ${soundBack.line}, then ${voice.line}, then ${voiceRow.line}, ` +
      `then ${kept.line}`);
  }
  // Game: under the game's two rows, Subtitles and Enable Console, Show FPS (bo1_fpscounter), which right turns on and
  // left off again; down from it goes round to the first. Then back to Voice Chat's row, for the keyboard.
  await pressed(PAD.B);
  const game = await pressed(PAD.DOWN);
  const subtitles = await pressed(PAD.A);
  const consoleRow = await pressed(PAD.DOWN);
  const fpsRow = await pressed(PAD.DOWN);
  const fpsWas = await p.dvar('bo1_fpscounter');
  await pressed(PAD.RIGHT);
  const fpsOn = await p.dvar('bo1_fpscounter');
  await p.shot('gamepad-menu-game');
  await pressed(PAD.LEFT);
  const fpsOff = await p.dvar('bo1_fpscounter');
  const gameRound = await pressed(PAD.DOWN);
  if (game.x !== tab.x || subtitles.menu !== 'options_game' || !(subtitles.x > game.x) || subtitles.y !== -115 ||
    consoleRow.x !== subtitles.x || consoleRow.y !== -96 || fpsRow.x !== subtitles.x || fpsRow.y !== -77 ||
    fpsWas !== '0' || fpsOn !== '1' || fpsOff !== '0' || gameRound.item !== subtitles.item) {
    fail(`the Game page: ${game.line}, then ${subtitles.line}, then ${consoleRow.line}, then ${fpsRow.line}, at ` +
      `${fpsWas}, then ${fpsOn}, then ${fpsOff}, then ${gameRound.line}`);
  }
  const gameBack = await pressed(PAD.B);
  const gameLast = await pressed(PAD.DOWN);
  if (gameBack.menu !== 'options_new_pc' || gameBack.item !== game.item || gameLast.item !== lastTab.item) {
    fail(`B on Game's row, then down: ${gameBack.line}, then ${gameLast.line}`);
  }
  await pressed(PAD.UP);
  await pressed(PAD.UP);
  const voiceAgain = await pressed(PAD.A);
  if (voiceAgain.item !== voiceRow.item) fail(`back to Voice Chat's row: ${voiceAgain.line}`);
  // the keyboard, as the gamepad: Esc closes the page, on its tab, the up arrow goes up the tabs, Enter opens the page
  // on its first row; the right arrow on a tab switches to Controls, on the tab at its place
  const escaped = await typed('Escape');
  const soundTab = await typed('ArrowUp');
  const entered = await typed('Enter');
  const out = await typed('Escape');
  if (escaped.menu !== 'options_new_pc' || escaped.item !== voice.item || soundTab.item !== sound.item ||
    entered.menu !== 'options_sound' || !(entered.x > soundTab.x) || out.menu !== 'options_new_pc' ||
    out.item !== sound.item) {
    fail(`the keyboard's Esc, up arrow, Enter and Esc: ${escaped.line}, then ${soundTab.line}, then ${entered.line}, ` +
      `then ${out.line}`);
  }
  const moveTab = await typed('ArrowRight');
  if (moveTab.menu !== 'options_controls_new_pc' || moveTab.x !== tab.x || moveTab.y !== sound.y) {
    fail(`the keyboard's right arrow on the Sound tab did not switch to Controls, on Move: ${moveTab.line}`);
  }
  // Move is the second tab of Controls, Gamepad the sixth
  for (let i = 0; i < 3; i++) await pressed(PAD.DOWN);
  const gamepadTab = await pressed(PAD.DOWN);
  const gamepadRow = await pressed(PAD.A);
  if (gamepadTab.x !== tab.x || gamepadRow.menu !== 'options_gamepad' || !(gamepadRow.x > gamepadTab.x)) {
    fail(`down the tabs of Controls to Gamepad, then A: ${gamepadTab.line}, then ${gamepadRow.line}`);
  }
  await p.shot('gamepad-menu-gamepad');
  // under Enable Gamepad, Controller Vibration (bo1_rumble): left turns it down to 0.95, the gamepad rumbling at it,
  // the heavy motor at it and the light one at half (win_gamepad.cpp: GPad_UpdateRumble), though the game is paused;
  // right up again. Up twice goes round to Aim Assist (bo1_aimassist): right off, and right again on. Up twice more is
  // Button Layout.
  const vibrationRow = await pressed(PAD.DOWN);
  const vibrationWas = await p.dvar('bo1_rumble');
  const rumblesWere = await p.page.evaluate(() => window.bo1Pad.rumbles.length);
  await pressed(PAD.LEFT);
  const vibrationDown = await p.dvar('bo1_rumble');
  const vibrationFelt = await p.page.evaluate((n) => window.bo1Pad.rumbles.slice(n), rumblesWere);
  await pressed(PAD.RIGHT);
  const vibrationUp = await p.dvar('bo1_rumble');
  const feltAt = (r: { strongMagnitude?: number; weakMagnitude?: number }) =>
    Math.abs((r.strongMagnitude ?? 0) - 0.95) < 0.001 && Math.abs((r.weakMagnitude ?? 0) - 0.475) < 0.001;
  expect([vibrationRow.x, gamepadRow.y, vibrationRow.y, vibrationWas, vibrationDown, vibrationUp,
    vibrationFelt.some(feltAt)], `Controller Vibration (${vibrationRow.line})`).toEqual(
    [gamepadRow.x, -115, -96, '1', '0.95', '1', true]);
  await pressed(PAD.UP);
  const assistRow = await pressed(PAD.UP);
  const assistWas = await p.dvar('bo1_aimassist');
  await pressed(PAD.RIGHT);
  const assistOff = await p.dvar('bo1_aimassist');
  await pressed(PAD.RIGHT);
  expect([assistRow.x, assistRow.y, assistWas, assistOff, await p.dvar('bo1_aimassist')],
    `Aim Assist (${assistRow.line})`).toEqual([gamepadRow.x, -1, '1', '0', '1']);
  await pressed(PAD.UP);
  const layoutRow = await pressed(PAD.UP);
  await p.shot('gamepad-menu-layout');
  expect([layoutRow.x, layoutRow.y], `Button Layout (${layoutRow.line})`).toEqual([gamepadRow.x, -39]);
  const gamepadBack = await pressed(PAD.B);
  if (gamepadBack.menu !== 'options_controls_new_pc' || gamepadBack.x !== gamepadTab.x ||
    gamepadBack.y !== gamepadTab.y) {
    fail(`B on a row of the Gamepad page did not close it, back to Controls on its tab: ${gamepadBack.line}`);
  }
  const switched = p.next(/^menu: options_\w+ takes the gamepad's key 23 as the right arrow$/, 15_000);
  const settingsAgain = await pressed(PAD.RIGHT);
  if (!(await switched) || settingsAgain.menu !== 'options_new_pc' || settingsAgain.x !== tab.x) {
    fail(`the d-pad's right on the Gamepad tab did not switch Controls to Settings: ${settingsAgain.line}`);
  }
  const switchedBack = p.next(/^menu: options_\w+ takes the gamepad's key 5 as the left arrow$/, 15_000);
  const controlsAgain = await pressed(PAD.LB);
  if (!(await switchedBack) || controlsAgain.menu !== 'options_controls_new_pc') {
    fail(`LB did not switch the game's Settings to Controls again: ${controlsAgain.line}`);
  }
  if (!(await p.padPressUntil(PAD.B, /^input: the mouse turns the view$/))) fail("B did not close the game's menu");
  await sleep(500);
  expect(await cursor(), 'the arrow over the game as the gamepad plays, the menu closed').toBe('none');
  await p.page.mouse.move(700, 400);
  await p.page.mouse.move(720, 410);
  await sleep(500);
  expect(await cursor(), 'the arrow over the game as the mouse moved').toBe('');

  // The keyboard alone, the gamepad off: the tabs of Controls open the keys' pages then. Esc opens the game's menu;
  // the down arrow goes to Settings, Enter opens it, the right arrow switches to Controls on Look, the down arrow goes
  // to Move, and Enter into its first row, a key's. The left arrow there switches to Settings, its tabs alone
  // (UI_PadKeyRuns); the up arrow to Graphics, the right arrow, the down arrow and Enter go back into the row. The
  // mouse moved onto the row, Esc closes the page, back to Controls' tabs: the menu is 1.5 px a unit here, centred.
  await p.command('gpad_enabled 0');
  await sleep(500);
  const keysMenu = p.next(/^input: the mouse is a menu's$/, 10_000);
  await p.escape();
  if (!(await keysMenu)) fail("Esc, the gamepad off, did not open the game's menu");
  await sleep(1500);
  await typed('ArrowDown');
  const keysTab = await typed('Enter');
  const keysLook = await typed('ArrowRight');
  const keysMove = await typed('ArrowDown');
  const keysRow = await typed('Enter');
  const keysBack = await typed('ArrowLeft');
  const upTo = async (y: number) => {
    let at = await menuFocus();
    for (let i = 0; i < 6 && at.y !== y; i++) at = await typed('ArrowUp');
    return at;
  };
  await upTo(keysTab.y);
  await typed('ArrowRight');
  await typed('ArrowDown');
  const keysIn = await typed('Enter');
  if (keysTab.menu !== 'options_new_pc' || keysLook.menu !== 'options_controls_new_pc' || keysLook.x !== keysTab.x ||
    keysLook.y !== keysTab.y || keysMove.x !== keysLook.x || !(keysMove.y > keysLook.y) ||
    keysRow.menu !== 'options_move' || !(keysRow.x > keysMove.x) || keysBack.menu !== 'options_new_pc' ||
    keysBack.x !== keysTab.x || keysIn.item !== keysRow.item) {
    fail(`the keyboard alone: ${keysTab.line}, then ${keysLook.line}, then ${keysMove.line}, then ${keysRow.line}, ` +
      `then ${keysBack.line}, then ${keysIn.line}`);
  }
  await p.page.mouse.move(640 + 1.5 * (keysRow.x + 50), 360 + 1.5 * (keysRow.y + 6));
  await p.page.mouse.move(640 + 1.5 * (keysRow.x + 60), 360 + 1.5 * (keysRow.y + 8));
  await sleep(1000);
  const hovered = await menuFocus();
  const closed = await typed('Escape');
  if (hovered.item !== keysRow.item || /, the keys lead$/.test(hovered.line) ||
    closed.menu !== 'options_controls_new_pc' || closed.x !== keysMove.x || closed.y !== keysMove.y) {
    fail(`Esc, the mouse on a key's row of Move, did not close its page: ${hovered.line}, then ${closed.line}`);
  }
  // The Graphics page's last tab in a game is Default System Settings, as the Game page's, the same item of each
  // (UI_FrameLimit_PatchMenus). The mouse finds it, the page open: two moves that come back to the same point are no
  // motion to the engine, so each ends at another point of the tab than the last.
  let mouseAt: number[] = [];
  const underMouse = async (x: number, y: number) => {
    const points = [[x + 50, y + 6], [x + 60, y + 8]];
    if (mouseAt[0] === points[1][0] && mouseAt[1] === points[1][1]) points.reverse();
    for (const [px, py] of points) await p.page.mouse.move(640 + 1.5 * px, 360 + 1.5 * py);
    mouseAt = points[1];
    await sleep(1000);
    return menuFocus();
  };
  await typed('ArrowLeft');
  const graphicsTab = await upTo(keysTab.y);
  const graphicsPage = await typed('Enter');
  const graphicsLast = await underMouse(keysTab.x, -39);
  await typed('Escape');
  const gameTab = await upTo(game.y);
  const gamePage = await typed('Enter');
  const gamePageLast = await underMouse(keysTab.x, -39);
  if (graphicsTab.menu !== 'options_new_pc' || graphicsPage.menu !== 'options_graphics' ||
    graphicsLast.menu !== 'options_graphics' || graphicsLast.y !== -39 || gameTab.menu !== 'options_new_pc' ||
    gamePage.menu !== 'options_game' || gamePageLast.menu !== 'options_game' ||
    gamePageLast.item !== graphicsLast.item) {
    fail(`the last tab of the Graphics page is not the Game page's: ${graphicsLast.line} there, and ` +
      `${gamePageLast.line} here`);
  }
  if (!(await p.padPressUntil(PAD.B, /^input: the mouse turns the view$/, 6))) fail("B did not close the game's menu");
  // the gamepad goes: it turns off; it comes back, and a press turns it on
  const turnedOff = p.next(/^gamepad: none is there, which turns it off$/, 10_000);
  await p.page.evaluate(() => window.bo1Pad.plug(false));
  if (!(await turnedOff)) fail('the gamepad, unplugged, did not turn off');
  await p.page.evaluate(() => window.bo1Pad.plug(true));
  if (!(await p.padPressUntil(PAD.X, /^gamepad: a button of it was pressed, which turns it on$/))) {
    fail('the gamepad, plugged in again, did not turn on at a press of X');
  }
  p.note('played with the gamepad');
}

// The aim assist (bo1-zombies/src/aim_assist/aim_assist.cpp), on as a new player's is. The first round's zombies come
// to the player, which this waits for, up to 4 min, the right stick turning the view with none on screen. The right
// stick then turns the view to the one nearest the crosshair until it is in the middle, in the aim assist's range and
// the auto aim's, where the view slows over it. With the sticks still the view follows none, and the right stick,
// pushed a little its way, then has the view follow it (the lock-on), up to 3 s. The left trigger, aiming down the
// sight, then turns the view on to it (the auto aim). A try that finds the zombie gone by then goes again, up to 5.
async function gamepadAimAssist(p: Player): Promise<void> {
  let s = await p.state();
  if (!(s.aimRange > 0) || !(s.autoAimRange > 0)) {
    fail(`the ${s.weapon} in hand has no aim assist: aimrange ${s.aimRange}, autoaimrange ${s.autoAimRange}`);
  }
  const until = Date.now() + 240_000;
  let followed: { at: PlayerState; entity: number; frames: number } | null = null;
  for (let attempt = 1; ; attempt++) {
    let looked = Date.now();
    for (;;) {
      if (Date.now() > until) {
        fail(`no zombie came to the middle of the screen, in the aim assist's ${s.aimRange} units and the auto aim's ` +
          `${s.autoAimRange}, with the view slowing over it, in 4 min: ${s.aimTargets} on screen, the nearest ` +
          `${s.nearest.distance} units off at ${s.nearest.x} ${s.nearest.y}, slowdown ${s.slowdown ? 1 : 0}`);
      }
      s = await p.state();
      up(s, 'as he waited for one in the middle of the screen');
      if (!s.aimTargets) {
        if (Date.now() - looked > 5000) {
          s = (await holdStick(p, (t) => (t.aimTargets ? null : 1), 5000)).s;
          looked = Date.now();
        } else {
          await sleep(1000);
        }
        continue;
      }
      looked = Date.now();
      if (Math.abs(s.nearest.x) >= 0.1) {
        s = (await steerToZombie(p, 0.05, 20_000)).s;
        continue;
      }
      if (s.nearest.distance <= Math.min(s.aimRange, s.autoAimRange) && s.slowdown) break;
      await sleep(500);
    }
    if (!followed) {
      // the sticks still, the lock-on lets go: the line may still have the steering's last push, a frame behind
      let still = s;
      for (let i = 0; i < 10 && still.lockOn >= 0; i++) still = await p.state();
      expect(still.lockOn, 'the entity the lock-on had the view follow with the sticks still').toBe(-1);
      // past the stick's dead zone (gpad_stick_deadzone_min, 0.2), and the lock-on's (aim_lockon_deflection, 0.05)
      const way = Math.sign(s.nearest.x) || 1;
      const push = await holdStick(p, (t) => (t.lockOn >= 0 ? null : 0.35 * way), 3000);
      if (push.s.lockOn >= 0 && push.frames > 0) {
        followed = { at: s, entity: push.s.lockOn, frames: push.frames };
      } else if (attempt === 5) {
        fail(`the right stick, 5 times pushed a little with a zombie near the crosshair, did not have the view ` +
          `follow ` +
          `it: the last ${s.nearest.distance} units off at ${s.nearest.x} ${s.nearest.y}`);
      } else {
        continue;
      }
      s = push.s;
    }
    const over = s;
    await p.page.evaluate((b) => window.bo1Pad.button(b, 1), PAD.LT);
    let snapped: PlayerState | null = null;
    for (let i = 0; i < 15 && !snapped; i++) {
      await sleep(300);
      const t = await p.state();
      if (t.autoAim >= 0) snapped = t;
    }
    await sleep(1000);
    const held = await p.state();
    await p.page.evaluate((b) => window.bo1Pad.button(b, 0), PAD.LT);
    await sleep(1000);
    if (snapped) {
      p.note(`the lock-on had the view follow a zombie ${followed!.at.nearest.distance} units off, and the aim ` +
        `assist ` +
        `slowed the view over one ${over.nearest.distance} units off, and the left trigger turned the view to it ` +
        `(entity ${snapped.autoAim}, try ${attempt}), which held it at ${held.nearest.x} ${held.nearest.y}`);
      return;
    }
    if (attempt === 5) {
      fail(`the left trigger, 5 times with a zombie near the crosshair, did not turn the view to it: the last ` +
        `${over.nearest.distance} units off at ${over.nearest.x} ${over.nearest.y} (autoaim ${held.autoAim})`);
    }
  }
}

// The knife's lunge (aim_assist.cpp: AimAssist_ApplyAutoMelee). Clicking the right stick melees, and with a zombie in
// the auto melee's reach (aim_automelee_range) and near the crosshair, the press's command asks the server to lunge at
// it (bo1_player: lunges). The zombie closest to the player comes on, which this waits for, up to 3 min, the right
// stick keeping it near the middle; with none within 300 units on screen, the stick turns the view round until one is.
// The click comes with the zombie 40 units off at least; one nearer the left stick backs away from. It is held until
// the game has it (bo1_player: melees). A click that asked for none goes again, up to 5 times.
async function gamepadLunge(p: Player): Promise<void> {
  const nearest = 40;
  const reach = Number(await p.dvar('aim_automelee_range'));
  expect(reach, 'units the auto melee reaches (aim_automelee_range)').toBeGreaterThan(nearest + 10);
  const before = await p.state();
  expect(before.lunges, 'commands that asked for a lunge before the knife was pressed').toBe(0);
  const until = Date.now() + 180_000;
  const near = (t: PlayerState) => t.aimTargets && t.closest.distance <= 300;
  let s = before;
  for (let press = 1; ;) {
    if (Date.now() > until) {
      fail(`no zombie came within the knife's reach, ${reach} units, near the middle of the screen, in 3 min: ` +
        `${s.aimTargets} on screen, the closest ${s.closest.distance} units off at ${s.closest.x} ${s.closest.y}`);
    }
    s = await p.state();
    up(s, 'as he waited for one to come within the knife\'s reach');
    if (!near(s)) {
      s = (await holdStick(p, (t) => (near(t) || t.weapon === 'none' ? null : 1), 5000)).s;
      continue;
    }
    if (Math.abs(s.closest.x) > 0.3) {
      s = (await steerToZombie(p, 0.1, 10_000, (t) => t.closest)).s;
      continue;
    }
    if (s.closest.distance > reach - 10) {
      await sleep(200);
      continue;
    }
    if (s.closest.distance < nearest) {
      const from = s;
      const away = (t: PlayerState) => !t.aimTargets || t.closest.distance >= nearest + 20 || t.weapon === 'none';
      s = (await holdStick(p, (t) => (away(t) ? null : 1), 15_000, PAD.LY)).s;
      if (s.weapon !== 'none' && s.aimTargets && s.closest.distance < nearest) {
        fail(`the left stick, held back for 15 s, took the player ${s.closest.distance} units from the zombie, from ` +
          `${from.closest.distance}: too near for the knife to lunge`);
      }
      continue;
    }
    const at = s;
    const click = await p.padHold(PAD.RS, (t) => t.melees > at.melees);
    if (click.s.lunges > before.lunges) {
      p.note(`the knife, the right stick clicked with a zombie ${at.closest.distance} units off, lunged at it: ` +
        `${click.s.lungeDist} units asked for (press ${press})`);
      return;
    }
    if (press === 5) {
      fail(`5 clicks of the right stick with a zombie in the knife's reach asked for no lunge: the last with it ` +
        `${at.closest.distance} units off, ${click.had ? 'which the game had' : 'which the game never had, held 5 s'}`);
    }
    press++;
    await p.afterFrames(5, 1500); // the knife's swing ends before the next click, which one during it doesn't lunge
  }
}

test('a player plays alone with a gamepad', async ({ players }) => {
  const max = await players.open('Max', { gamepad: true, args: '+set bo1_web_movieAtLoad 1' });
  max.background(() => loadingMovie(max, 'gamepad'));
  await max.getTheMap('#playalone');
  await gamepadSettings(max);
  await gamepadMenu(max);
  await max.page.click('#playalone');
  await max.waitFor(new RegExp(`^lobby: playing ${map} alone$`), 120_000);
  await max.waitFor(/^test: X, the gamepad's first press in the game, turned it on, binding its layouts/, 300_000);
  expect(max.find(new RegExp('^input: gamepad 1 connected: Xbox 360 Controller \\(XInput STANDARD GAMEPAD\\), the ' +
    'standard mapping$')),
    "the page says the gamepad connected, with the standard mapping").toBeTruthy();
  expect(max.find(/^input: gamepad 0 connected: Wheel .*, no standard mapping: the game can't read it$/),
    'the page says the wheel connected, with no standard mapping').toBeTruthy();
  await gamepadGame(max);
  await gamepadAimAssist(max);
  await gamepadLunge(max);
  // The client lost none of the zombies' anim commands: each of its frames takes in several of the game's snapshots,
  // and it replays what is left of each one's commands as it leaves it (cg_anim_commands.cpp:
  // CG_SP_ReplayAnimCommandsLeft).
  expect((await max.state()).animLost, "the zombies' anim commands the client lost").toBe(0);
  // The game's Default System Settings puts the mod's rows back with the game's own: Frame Rate Limit, Show FPS and
  // Full Screen, each set here on the console, at what its popup's Yes runs, "exec default_options.cfg"
  // (bo1-zombies/src/qcommon/cmd.cpp: Cmd_Exec_f).
  await max.command('bo1_maxfps 30; bo1_fpscounter 1; bo1_web_fullscreen 0');
  const set = [await max.dvar('bo1_maxfps'), await max.dvar('bo1_fpscounter'), await max.dvar('bo1_web_fullscreen')];
  await max.command('exec default_options.cfg');
  const back = [await max.dvar('bo1_maxfps'), await max.dvar('bo1_fpscounter'), await max.dvar('bo1_web_fullscreen')];
  expect([set, back], "the Frame Rate Limit, Show FPS and Full Screen as set, then as the game's defaults put them")
    .toEqual([['30', '1', '0'], ['0', '0', '1']]);
});
