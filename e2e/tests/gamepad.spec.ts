// The gamepad in the lobby (web/src/padnav.ts): B goes back, the d-pad moves the focus along the lobby and its top, A
// presses, and the right stick scrolls. A room shows the gamepad on its player, its button is the gamepad's to go to
// even while it is off, B calls its countdown off, and B on its map asks to leave. A refusal of the lobby shows over
// the page, and goes at a click and by itself.
import type { Page } from '@playwright/test';
import { expect, makeRoom, said, test } from '../fixtures';

test.use({ serverArgs: ['-min-players', '1'] });

// The gamepad's buttons and axes, in the standard mapping.
const PAD = { A: 0, B: 1, UP: 12, DOWN: 13, LEFT: 14, RIGHT: 15, RY: 3 };

interface FakePad {
  button(b: number, value: number): void;
  axis(a: number, value: number): void;
}
declare global {
  // eslint-disable-next-line no-var
  var pad: FakePad;
}

// The player's gamepad, as Chrome's Gamepad API gives it: the standard mapping, as an Xbox controller has it, shown to
// the page once one of its buttons is pressed. Chrome numbers it 1, after a device with no standard mapping (a wheel),
// which the page leaves alone. globalThis.pad moves it.
async function plugGamepad(page: Page): Promise<void> {
  await page.addInitScript(() => {
    const button = () => ({ pressed: false, touched: false, value: 0 });
    const pad = { id: 'Xbox 360 Controller (XInput STANDARD GAMEPAD)', index: 1, connected: true, mapping: 'standard',
      timestamp: 0, axes: [0, 0, 0, 0], buttons: Array.from({ length: 17 }, button),
      vibrationActuator: { type: 'dual-rumble', effects: ['dual-rumble'], playEffect: () => Promise.resolve('complete'),
        reset: () => Promise.resolve('complete') } };
    const wheel = { id: 'Wheel (Vendor: 046d Product: c24f)', index: 0, connected: true, mapping: '', timestamp: 0,
      axes: [0, 0, 0, 0, 0, 0], buttons: Array.from({ length: 12 }, button) };
    let shown = false;
    Object.defineProperty(navigator, 'getGamepads', { value: () => (shown ? [wheel, pad, null, null] : [null, null]) });
    const connected = (p: object) => window.dispatchEvent(Object.assign(new Event('gamepadconnected'), { gamepad: p }));
    globalThis.pad = {
      button(b, value) {
        pad.buttons[b] = { pressed: value > 0, touched: value > 0, value };
        pad.timestamp = performance.now();
        if (shown || !value) return;
        shown = true;
        connected(wheel);
        connected(pad);
      },
      axis(a, value) {
        pad.axes[a] = value;
        pad.timestamp = performance.now();
      },
    };
  });
}

// What the lobby shows as the gamepad moves through it: what has the focus (its id, or its text) and whether it is the
// map picked, the lobby's views shown, whether it asks, and whether a room's game is starting.
interface PadView { focus: string; picked: boolean; views: string; ask: boolean; starting: boolean }
const padView = (page: Page): Promise<PadView> => page.evaluate(() => {
  const a = document.activeElement;
  return {
    focus: a && a !== document.body ? a.id || (a.textContent || '').trim().slice(0, 40) : '',
    picked: !!a && a.getAttribute('aria-checked') === 'true',
    views: ['outside', 'solo', 'inside', 'results']
      .filter((id) => !document.getElementById(id)!.classList.contains('hidden')).join(),
    ask: !document.getElementById('ask')!.classList.contains('hidden'),
    starting: /^Starting in/.test(document.getElementById('ready')!.textContent || ''),
  };
});

// A press of a button, held for one of the page's frames, as the page reads the gamepad at the start of each, then let
// go: one press, one move. Waits until the lobby shows what test asks for.
async function press(page: Page, b: number, test: (v: PadView) => boolean): Promise<PadView> {
  await page.evaluate(async (b) => {
    const frame = () => new Promise((done) => requestAnimationFrame(done));
    globalThis.pad.button(b, 1);
    await frame();
    globalThis.pad.button(b, 0);
    await frame();
    await frame();
  }, b);
  let v = await padView(page);
  await expect.poll(async () => test(v = await padView(page)), { message: JSON.stringify(v) }).toBe(true);
  return v;
}

test('the gamepad goes through the lobby, and into a room and out', async ({ player }) => {
  const max = await player('Max', { wait: false });
  await plugGamepad(max);
  await max.reload();
  await max.locator('#soloopen').click();
  await expect(max.locator('#solo')).toBeVisible();
  // the first press shows the gamepad to the page, which reads it from then on
  await expect.poll(async () => (await press(max, PAD.B, () => true)).picked).toBe(true);
  await press(max, PAD.B, (v) => v.views === 'outside' && v.focus === 'make');
  for (let i = 0; i < 4 && (await padView(max)).focus !== 'home'; i++) await press(max, PAD.UP, () => true);
  expect((await padView(max)).focus).toBe('home');
  await press(max, PAD.RIGHT, (v) => v.focus === 'boardsopen');
  await press(max, PAD.RIGHT, (v) => v.focus === 'settings');
  await press(max, PAD.LEFT, (v) => v.focus === 'boardsopen');
  await press(max, PAD.LEFT, (v) => v.focus === 'home');
  await press(max, PAD.DOWN, (v) => v.focus === 'make');

  // A makes a room, which opens on its map, and shows a gamepad on its player
  await press(max, PAD.A, (v) => v.views === 'inside' && v.picked);
  await expect(max.locator('#members li.mine .pad')).toBeVisible();
  // the right stick scrolls the lobby, and back
  await max.evaluate((a) => globalThis.pad.axis(a, 1), PAD.RY);
  await expect.poll(() => max.locator('#overlay').evaluate((o) => o.scrollTop)).toBeGreaterThan(50);
  await max.evaluate((a) => globalThis.pad.axis(a, -1), PAD.RY);
  await expect.poll(() => max.locator('#overlay').evaluate((o) => o.scrollTop)).toBe(0);
  await max.evaluate((a) => globalThis.pad.axis(a, 0), PAD.RY);

  // A on the map goes to the room's button, A starts the countdown, and B calls it off
  await press(max, PAD.A, (v) => v.focus === 'ready');
  await press(max, PAD.A, (v) => v.starting);
  await press(max, PAD.B, (v) => !v.starting && !v.ask && v.views === 'inside' && v.focus === 'ready');
  await press(max, PAD.UP, (v) => v.focus === 'modsline');
  await press(max, PAD.UP, (v) => v.focus === 'copylink');
  await press(max, PAD.B, (v) => v.picked);
  // B on the map asks to leave, with the focus on Stay: B stays; asked again, left goes to Leave, and A leaves
  await press(max, PAD.B, (v) => v.ask && v.focus === 'askno');
  await press(max, PAD.B, (v) => !v.ask && v.picked && v.views === 'inside');
  await press(max, PAD.B, (v) => v.ask);
  await press(max, PAD.LEFT, (v) => v.focus === 'askyes');
  await press(max, PAD.A, (v) => v.views === 'outside');

  // a code of no room's: the lobby's refusal shows over the page, which stays where it is
  const make = async () => Math.round((await max.locator('#make').boundingBox())!.y);
  const before = await make();
  const refuse = async () => {
    await max.locator('#code').fill('ZZZZ');
    await max.locator('#code').press('Enter');
    await expect(max.locator('#notice')).toHaveText('There is no room ZZZZ.');
  };
  await refuse();
  await expect(max.locator('#notice')).toHaveClass(/refusal/);
  await expect(max.locator('#status')).toHaveText('');
  expect(await make()).toBe(before);
  await max.locator('#notice').click();
  await expect(max.locator('#notice')).toBeHidden();
  await refuse();
  await expect(max.locator('#notice')).toBeHidden({ timeout: 10_000 });

  // down to Pick a map, A opens the maps on the one picked, and A on it goes to Play, which A presses
  for (let i = 0; i < 8 && (await padView(max)).focus !== 'soloopen'; i++) await press(max, PAD.DOWN, () => true);
  expect((await padView(max)).focus).toBe('soloopen');
  await press(max, PAD.A, (v) => v.views === 'solo' && v.picked);
  await press(max, PAD.A, (v) => v.focus === 'playalone');
  await press(max, PAD.A, () => said(max).some((line) => /^lobby: playing \w+ alone$/.test(line)));
});

test("the room's button is the gamepad's to go to and wait on while it is off", async ({ player }) => {
  // the room's map downloads: its button counts, and is off
  const max = await player('Max', { maps: { zombie_theater: { have: 1_000_000, total: 2_000_000, running: true } },
    wait: false });
  await plugGamepad(max);
  await max.reload();
  await makeRoom(max);
  await expect(max.locator('#ready')).toHaveText('1 / 2 MB');
  await expect(max.locator('#ready')).toHaveAttribute('aria-disabled', 'true');
  await max.locator('#mapgrid button[aria-checked=true]').focus();
  await expect.poll(async () => (await press(max, PAD.A, () => true)).focus).toBe('ready');
  await press(max, PAD.A, (v) => v.focus === 'ready');
  await expect(max.locator('#ready')).toHaveText('1 / 2 MB');
  await press(max, PAD.B, (v) => v.picked);
});
