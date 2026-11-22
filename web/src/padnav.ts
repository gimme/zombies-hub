// web/src/padnav.ts - the gamepad in the lobby: it moves the focus between the controls and presses them.
//
// The page reads the gamepad while it shows the lobby. While the page shows the game, the game reads the gamepad
// itself (bo1-zombies/web/src/library_bo1.js: pollPads). The page reads only a gamepad with the standard mapping, as
// the game does.
//
// What the gamepad does in the lobby:
//   - The d-pad or the left stick moves the focus to the nearest control that way, a panel at a time. Left and right
//     go as the W3C's spatial navigation finds it. Up and down go a row at a time, and keep the column (move).
//   - A presses the control that has the focus. In a text field, A is Enter (activate).
//   - B goes back (back).
//   - Start opens the options menu, and closes it, as the game's Start does with its menu. The options menu has a
//     cursor of its own, which takes the presses while the menu is open (settings.ts: pad).
//   - The right stick scrolls (scroll).
//
// Chrome has no events for the buttons of a gamepad, so the page reads them each frame while a gamepad is connected
// (frame). A held direction moves again after 400 ms, then every 120 ms. What is held when the lobby shows does
// nothing until it is let go: the B that left a game, for one. Each press of a button is a gesture, as a click or a
// key is (gestures.ts: GESTURES).
//
// The gamepad has the page from a press of one of its buttons until the mouse moves or is pressed. The body has the
// class padnav for that time, and the style sheet draws the focus then (page.css). While the gamepad has the page, a
// control always has the focus. When the view changes, the focus goes to where the new view starts (keepFocus, home).
// The first press after the mouse had the page only shows where the focus is.
//
// The part also tells the lobby whether the page reads a gamepad (tellGamepad).
import { openBoards } from './boards';
import { $, isOff } from './dom';
import { readyNow } from './files';
import { PAD_PRESS } from './gestures';
import { ask, me, offline, view } from './lobby';
import { openPicker, openProfile, pickerOpen, pictureTip } from './profile';
import { answerQuestion, askLeave, asking } from './question';
import { readyGets } from './room';
import { overlay } from './screen';
import { bo1Settings } from './settings';
import { openSolo } from './solo';
import { focusQuietly, overSound, playSound } from './sounds';

// Chrome shows a page a gamepad only once one of its buttons is pressed with the page in front. It fires
// gamepadconnected then, however long the gamepad has been connected to the computer. The console says each gamepad
// that comes and goes, in the lobby and in a game, and whether the game can read it. The game reads a gamepad with
// the standard mapping (bo1-zombies/web/src/library_bo1.js: pollPads). The Gamepad tab of the options menu is live
// once the page reads one (settings.ts).
window.addEventListener('gamepadconnected', (e) => console.log(`input: gamepad ${e.gamepad.index} connected: ` +
  `${e.gamepad.id}, ` +
  (e.gamepad.mapping === 'standard' ? 'the standard mapping' : 'no standard mapping: the game can\'t read it')));
window.addEventListener('gamepaddisconnected', (e) => console.log(`input: gamepad ${e.gamepad.index} disconnected`));

// ---- What the page reads of a gamepad ----

// A button or a direction, by the name that press takes. The options menu takes the same names, except start
// (settings.ts: pad).
type Button = 'a' | 'b' | 'lb' | 'rb' | 'start';
type Way = 'up' | 'down' | 'left' | 'right';
type Press = Button | Way;

// Where each button is among the buttons of a gamepad with the standard mapping.
const BUTTONS: Record<Button, number> = { a: 0, b: 1, lb: 4, rb: 5, start: 9 };
// Each direction: its button of the d-pad, its axis of the left stick, and the sign of that axis that way.
const DIRS: Record<Way, [button: number, axis: number, sign: number]> = {
  up: [12, 1, -1], down: [13, 1, 1], left: [14, 0, -1], right: [15, 0, 1],
};
const isWay = (n: Press): n is Way => n in DIRS;
// Object.entries with the names typed. Each of the two tables has only the names of its type.
const entries = <K extends string, V>(table: Record<K, V>) => Object.entries(table) as [K, V][];
// A held direction moves again after 400 ms, then every 120 ms.
const REPEAT = [400, 120];
// What the focus can go to.
const CONTROL = 'button, input:not([type=file]), a[href], [tabindex]:not([tabindex="-1"])';

// The edges of a box in the window, as a DOMRect has them.
interface Box {
  left: number;
  right: number;
  top: number;
  bottom: number;
}

// The last move of the focus (move).
interface Memory {
  at: HTMLElement; // the control that the focus went to
  flat: boolean; // whether the move was left or right
  // The span across the way that the focus keeps from where it began going that way. It is measured from the top of
  // the control for a move left or right, and from its left for a move up or down.
  span: number[];
}

let running = false; // whether the page reads the gamepads each frame (frame)
// When each held button or direction presses again, by its name. Infinity is never. It is null while the lobby does
// not show.
let held: Record<string, number> | null = null;
// Where the focus was, and in which view (viewOf). By these, keepFocus finds a control that the page drew again: a
// room of the list of rooms, or a map.
let lastRect: DOMRect | null = null;
let lastView = '';
let memory: Memory | null = null;
// How many gamepads the page reads. The options menu and the lobby hear when that changes (frame).
let padCount = 0;
// The buttons held on each gamepad, a bit each, as the page last read them while the lobby showed.
let buttons: number[] | null = null;

// The right stick scrolls what the gamepad has (scroller). The page needs it because a view on a phone is longer than
// the screen, and the focus goes only where a control is. The speed is up to SCROLL pixels a second. It grows with
// the square of how far the stick is pushed, so the scroll is slow near the middle. The middle itself is dead (DEAD).
// The stick scrolls nothing until it has been let go once since the lobby showed, because the player looked around
// with it in the game.
const SCROLL = 1500;
const DEAD = 0.2;
// Set once the stick has been let go: when the page last read it, and the part of a pixel that is yet to scroll.
let stick: { at: number; rest: number } | null = null;

// Returns the buttons that are pressed on a gamepad, a bit each.
const buttonsOf = (p: Gamepad): number => p.buttons.reduce((bits, b, i) => (b.pressed ? bits | (1 << i) : bits), 0);

// Returns the gamepads that the page reads: those that are connected and have the standard mapping.
const pads = (): Gamepad[] => (navigator.getGamepads ? [...navigator.getGamepads()] : [])
  .filter((p): p is Gamepad => !!p && p.connected && p.mapping === 'standard');

// Whether the lobby shows: the overlay is over the game, and the page is visible.
const shown = (): boolean => !overlay.classList.contains('hidden') && document.visibilityState === 'visible';

// Whether the gamepad has the page (body.padnav).
const on = (): boolean => document.body.classList.contains('padnav');

// Whether the options menu is open.
const settingsShown = (): boolean => bo1Settings.shown();

// Whether a box scrolls up and down: its content is taller than it is, and its overflow scrolls.
const scrolls = (e: Element): boolean =>
  e.scrollHeight > e.clientHeight + 1 && /auto|scroll/.test(getComputedStyle(e).overflowY);

// Returns the box that the right stick scrolls, or null. It is the nearest box that scrolls around the control with
// the focus: the lobby's page, or the pictures to pick from. With no focus in the scope, the search starts at the
// scope itself. When nothing around scrolls, it is the first box in the scope that does.
function scroller(): Element | null {
  const root = scope();
  const a = document.activeElement;
  for (let e: Element | null = a && root.contains(a) ? a : root; e; e = e.parentElement) if (scrolls(e)) return e;
  return [...root.querySelectorAll('*')].find(scrolls) || null;
}

// Scrolls by the right stick of the gamepad that pushes its stick furthest. While the options menu is open, the stick
// scrolls nothing.
function scroll(list: Gamepad[], now: number): void {
  const y = list.reduce((most, p) => (Math.abs(p.axes[3] || 0) > Math.abs(most) ? p.axes[3] : most), 0);
  const push = Math.max(0, Math.abs(y) - DEAD) / (1 - DEAD);
  if (!stick) {
    if (!push) stick = { at: now, rest: 0 };
    return;
  }
  // The time since the last read counts for at most 100 ms. A longer wait, with the page hidden for a while, would
  // scroll far at once.
  const ms = Math.min(now - stick.at, 100);
  stick.at = now;
  if (!push || settingsShown()) {
    stick.rest = 0;
    return;
  }
  const by = Math.sign(y) * push * push * SCROLL * ms / 1000 + stick.rest;
  const whole = Math.trunc(by);
  stick.rest = by - whole;
  const e = whole && scroller();
  if (e) e.scrollBy({ top: whole, behavior: 'instant' });
}

// Returns the buttons and the directions that are down on at least one gamepad. A direction is down by its button of
// the d-pad, or by the left stick pushed more than halfway.
function read(list: Gamepad[]): Set<Press> {
  const down = new Set<Press>();
  for (const p of list) {
    for (const [name, i] of entries(BUTTONS)) if (p.buttons[i] && p.buttons[i].pressed) down.add(name);
    for (const [name, [i, axis, way]] of entries(DIRS)) {
      if ((p.buttons[i] && p.buttons[i].pressed) || (p.axes[axis] || 0) * way > 0.5) down.add(name);
    }
  }
  return down;
}

// Reads the gamepads, each frame while one is connected and the lobby shows. It presses what is newly down, and
// presses a held direction again when its time has come.
function frame(now: number): void {
  const list = pads();
  if (list.length !== padCount) {
    padCount = list.length;
    // The options menu hears here that a gamepad came or went, which may be sooner than Chrome's events say it
    // (settings.ts: gamepadsChanged).
    bo1Settings.gamepadsChanged();
    tellGamepad();
  }
  if (!list.length) {
    // The page reads no gamepad now. The next gamepadconnected starts the reading again (start).
    running = false;
    held = buttons = stick = null;
    return;
  }
  if (!shown()) {
    // The game reads the gamepad while it shows. The page then only looks every 250 ms.
    held = buttons = stick = null;
    setTimeout(() => requestAnimationFrame(frame), 250);
    return;
  }
  requestAnimationFrame(frame);
  // A button that is newly pressed is a gesture (gestures.ts). The page says so before the press does anything,
  // because what the press does may play a sound. While the game shows, the game says its own presses.
  const was = buttons;
  buttons = list.map(buttonsOf);
  if (was && buttons.some((bits, i) => bits & ~(was[i] || 0))) document.dispatchEvent(new Event(PAD_PRESS));
  if (on() && !settingsShown()) keepFocus();
  scroll(list, now);
  const down = read(list);
  if (!held) {
    // The lobby shows from this frame on. What is held now does nothing until it is let go.
    held = {};
    for (const n of down) held[n] = Infinity;
    return;
  }
  for (const n of Object.keys(held) as Press[]) if (!down.has(n)) delete held[n];
  for (const n of down) {
    if (!(n in held)) {
      held[n] = isWay(n) ? now + REPEAT[0] : Infinity; // only a direction presses again while it is held
      press(n);
    } else if (now >= held[n]) {
      held[n] = now + REPEAT[1];
      press(n);
    }
  }
}

// Starts reading the gamepads each frame, unless the page reads them already. The page starts when it loads, and
// again at each gamepad that Chrome shows it. A browser without navigator.getGamepads has none to read.
function start(): void {
  if (running || !navigator.getGamepads) return;
  running = true;
  requestAnimationFrame(frame);
}
window.addEventListener('gamepadconnected', start);
start();

// ---- Who has the page, and where the focus is ----

// The mouse takes the page from the gamepad when it moves or is pressed. Only a move of the mouse itself counts
// (movementX, movementY). The browser also sends a mousemove while the mouse is still, when what is under it changes:
// a view that is drawn again, or a scroll.
const hide = (): void => {
  if (!on()) return;
  document.body.classList.remove('padnav');
  pictureTip(); // the tip of the picture that had the focus goes with the focus ring (profile.ts)
};
document.addEventListener('mousemove', (e) => (e.movementX || e.movementY) && hide());
document.addEventListener('pointerdown', hide);
// The focus left the control that the gamepad put it on: the mouse or a key moved it, or the page was drawn again. The
// span that move kept is forgotten.
document.addEventListener('focusout', (e) => {
  if (memory && e.target === memory.at) memory = null;
});
// Remembers where the focus is, and in which view, for keepFocus. Only the lobby, its question and the pictures to
// pick from count.
document.addEventListener('focusin', (e) => {
  const to = e.target;
  if (!(to instanceof Element)) return;
  if (!overlay.contains(to) && !$('ask').contains(to) && !$('picker').contains(to)) return;
  lastRect = to.getBoundingClientRect();
  lastView = viewOf();
});

// Returns the part of the page that the focus can go in: the lobby's question while it shows (question.ts), else the
// pictures to pick from while they show, else the profile's menu while it is open (profile.ts), else the overlay.
const scope = (): HTMLElement => (asking() ? $('ask') : pickerOpen() ? $('picker')
  : !$('profile').classList.contains('hidden') ? $('me') : overlay);

// Returns the names of what shows: the lobby's views, the profile, the pictures to pick from, and the question. The
// place that the page remembers of a control holds only while these are the same (keepFocus).
const viewOf = (): string => [
  ...['outside', 'solo', 'inside', 'results', 'boards'].filter((id) => !$(id).classList.contains('hidden')),
  !$('profile').classList.contains('hidden') ? 'profile' : '', pickerOpen() ? 'picker' : '',
  asking() ? 'ask' : ''].join();

// Whether the focus can go to an element: it is not disabled, the page draws it, and it is not hidden. A button or a
// text field can be disabled. A link or another element can't.
const usable = (e: Element & { disabled?: boolean }) =>
  !e.disabled && e.getClientRects().length > 0 && getComputedStyle(e).visibility !== 'hidden';

// Returns the controls that the focus can go to, in the page's order.
const controls = (): HTMLElement[] => [...scope().querySelectorAll<HTMLElement>(CONTROL)].filter(usable);

// Returns the control of the list that has the focus, or null.
const focused = (list: HTMLElement[]): HTMLElement | null => {
  const a = document.activeElement;
  return a instanceof HTMLElement && list.includes(a) ? a : null;
};

// Returns the grid of maps of the view that shows, or null. A room has one, which its host picks from while the room
// has no game. The page to play offline has the other. No grid counts while a question shows.
const maps = (): HTMLElement | null =>
  [$('mapgrid'), $('solomaps')].find((g) => g.getClientRects().length && !asking()) || null;

// Returns the button of the picked map in a grid of maps, if the focus can go to it. Each child of a grid is the
// button of a map (mapgrid.ts: showMaps).
const pickedMap = (grid: HTMLElement): HTMLButtonElement | undefined => ([...grid.children] as HTMLButtonElement[])
  .find((b) => b.getAttribute('aria-checked') === 'true' && usable(b));

// Returns the main button of a view: Ready, Play, Rejoin, or Back to the room. It is the red button (.primary). With
// grid, a red button in that grid of maps does not count. The main button is grey while it is off, when it says how
// far a download is, or Missing map (controls.ts: setButton). It is then the big button (.big). The focus can still go
// to it, and wait on it.
const mainButton = (list: HTMLElement[], grid?: HTMLElement): HTMLElement | undefined =>
  list.find((e) => e.classList.contains('primary') && !(grid && grid.contains(e)))
  || list.find((e) => e.classList.contains('big'));

// Returns where the focus starts in a view, by the first of these that holds:
//   - a question shows: its No
//   - the pictures to pick from show: the player's picture among them, or the first control
//   - the overlay has a line to click (Click to play, or Back to the room after a game's error): that line
//   - the view has maps: the picked map
//   - else the view's main button (Ready, Play, Back to the room), or its first control
function home(list: HTMLElement[]): HTMLElement | undefined {
  if (asking()) return $('askno');
  if (pickerOpen()) return list.find((e) => e.getAttribute('aria-checked') === 'true') || list[0];
  if (scope() === overlay && list.includes($('play'))) return $('play');
  const grid = scope() === overlay && maps();
  // Only the host picks the map of a room. The others start on Ready.
  const start = grid && (grid !== $('mapgrid') || (view && view.room && view.room.host)) && pickedMap(grid);
  return start || mainButton(list) || list[0];
}

// Keeps the focus on a control while the gamepad has the page. When no control of the scope has the focus, it goes
// to the control that is now where the focus was, if the view is the same: the page draws the list of rooms and the
// maps of a room again when they change. Otherwise it goes to where the view starts (home). The move makes no sound.
function keepFocus(): void {
  const a = document.activeElement;
  if (a && a !== document.body && scope().contains(a) && usable(a)) return;
  const list = controls();
  if (!list.length) return;
  const middle = lastRect && [(lastRect.left + lastRect.right) / 2, (lastRect.top + lastRect.bottom) / 2];
  const same = middle && lastView === viewOf() && list.find((e) => {
    const r = e.getBoundingClientRect();
    return r.left <= middle[0] && middle[0] <= r.right && r.top <= middle[1] && middle[1] <= r.bottom;
  });
  focus(same || home(list), true);
}

// Moves the focus to a control, and scrolls the control into the window. With quiet, a button that the focus comes
// to makes no sound.
function focus(e: HTMLElement | undefined, quiet = false): void {
  if (!e) return;
  memory = null; // move sets it again after this call
  if (quiet) focusQuietly(e);
  else e.focus();
  e.scrollIntoView({ block: 'nearest', inline: 'nearest' });
  lastRect = e.getBoundingClientRect();
  lastView = viewOf();
  // The page plays the sound of a button that the keyboard's focus comes to (sounds.ts: soundsInit). It knows that
  // focus by the mark that the browser gives it, :focus-visible. Where the browser does not mark the focus that the
  // gamepad moved, the sound plays here.
  if (!quiet && e.tagName === 'BUTTON' && !e.matches(':focus-visible')) playSound(overSound(e));
}

// ---- Where a direction takes the focus ----

// The focus goes the way the gamepad points as the W3C's spatial navigation has it
// (https://www.w3.org/TR/css-nav-1/#select-the-best-candidate), with the weights that its authors' polyfill
// (WICG/spatial-navigation) and Chrome's own spatial navigation give it. From a box a, the focus goes to the nearest
// of the controls ahead of a (distance). Of controls that are as near, the first in the page's order wins.
//
// A box b is ahead of a when it is wholly past the edge of a that way. It is also ahead when it is past that edge in
// part: b overlaps a across the way, starts no sooner than a, and goes on further than a does (the standard's
// isOutside: isRightSide, isBelow).

// Whether b is in line with a, left and right: the two overlap in height.
const rows = (a: Box, b: Box): boolean => b.bottom > a.top && b.top < a.bottom;

// Whether b is in line with a, up and down: the two overlap in width.
const columns = (a: Box, b: Box): boolean => b.right > a.left && b.left < a.right;

// Whether b is ahead of a, each way.
const ahead: Record<Way, (a: Box, b: Box) => boolean> = {
  right: (a, b) => b.left >= a.right || (b.left >= a.left && b.right > a.right && rows(a, b)),
  left: (a, b) => a.left >= b.right || (a.left >= b.left && a.right > b.right && rows(a, b)),
  down: (a, b) => b.top >= a.bottom || (b.top >= a.top && b.bottom > a.bottom && columns(a, b)),
  up: (a, b) => a.top >= b.bottom || (a.top >= b.top && a.bottom > b.bottom && columns(a, b)),
};

// Returns how far b is from a, the way the gamepad points: A + B - C - D.
//   - A is the straight line between their nearest points.
//   - B is the gap between them across the way. A box that is not in line with a gets half the size of a across the
//     way more. A box is in line with a when it overlaps a across the way. B counts 30 times left and right, so that
//     the next control in a row comes first. It counts twice up and down, for the nearest row and then the nearest
//     control in it.
//   - C is up to 5, for how much of the size of a across the way b is in line with.
//   - D is the square root of the area where they overlap.
function distance(way: Way, a: Box, b: Box): number {
  const flat = way === 'left' || way === 'right';
  const dx = Math.max(0, b.left - a.right, a.left - b.right);
  const dy = Math.max(0, b.top - a.bottom, a.top - b.bottom);
  const overX = Math.min(a.right, b.right) - Math.max(a.left, b.left);
  const overY = Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top);
  const inLine = flat ? overY : overX;
  const size = (flat ? a.bottom - a.top : a.right - a.left) || 1;
  const B = ((flat ? dy : dx) + (inLine > 0 ? 0 : size / 2)) * (flat ? 30 : 2);
  const C = inLine > 0 ? 5 * Math.min(inLine / size, 1) : 0;
  const D = overX > 0 && overY > 0 ? Math.sqrt(overX * overY) : 0;
  return Math.hypot(dx, dy) + B - C - D;
}

// Returns the control of list that is nearest to a, of those ahead of a. With anywhere, of all of them.
function nearest(way: Way, a: Box, list: HTMLElement[], anywhere = false): HTMLElement | null {
  let best: HTMLElement | null = null;
  let bestDistance = Infinity;
  for (const e of list) {
    const b = e.getBoundingClientRect();
    const d = anywhere || ahead[way](a, b) ? distance(way, a, b) : Infinity;
    if (d < bestDistance) {
      best = e;
      bestDistance = d;
    }
  }
  return best;
}

// The focus may be on a control that was scrolled out of the window: by the right stick, a finger or the wheel. The
// standard then looks from the edge of the window, and not from the control, which would scroll all the way back. A
// direction brings the focus back into the window at the edge that it comes from (enter). A then only shows where the
// focus is, as the first press after the mouse does. It does not press what the player can't see (press).

// Whether an element is in the window, in whole or in part.
const inWindow = (e: Element): boolean => {
  const r = e.getBoundingClientRect();
  return r.bottom > 0 && r.top < innerHeight && r.right > 0 && r.left < innerWidth;
};

// Returns the control in the window that is nearest to the edge that the direction comes from: for down, the top
// one. Of controls that are as near, the first in the page's order wins.
function enter(way: Way, list: HTMLElement[]): HTMLElement | null {
  const edges: Record<Way, (r: Box) => number> = {
    down: (r) => r.top, up: (r) => innerHeight - r.bottom, right: (r) => r.left, left: (r) => innerWidth - r.right,
  };
  const from = edges[way];
  let best: HTMLElement | null = null;
  let bestDistance = Infinity;
  for (const e of list.filter(inWindow)) {
    const d = from(e.getBoundingClientRect());
    if (d < bestDistance) {
      best = e;
      bestDistance = d;
    }
  }
  return best;
}

// The standard looks among all the controls at once, however they are grouped, and only from where the focus is. The
// lobby adds what the menus of TVs and consoles do: the focus moves a panel at a time, and up and down a row at a
// time, keeping its column (move).
//
// Groups. A group is a box that the page draws: a panel, or the header (GROUP). The focus goes first to the nearest
// control that way in its own group. With none there, it looks in the group around that one. There, each group counts
// as a whole, next to the controls that are in no group. The focus goes to the nearest of them, and in a group to
// its nearest control.
//   - Up from All maps on a leaderboard goes to Rounds, in the panel just above, however far to the side. It does
//     not go on up to the title, which is in line with All maps.
//   - Up from Copy link, at the top of the panel of a room, goes to no control of a panel beside it, in another
//     column.
// A row of buttons to pick one of is not a group. Its box goes across its panel, past its buttons, so it would be in
// line with everything under it.
//
// Left and right. The focus goes only to a control in line with it, beside it in its row, or to the panel beside it.
// It does not go on a slant up or down. The beam of Android's focus finder does the same.
//   - Right from the last map in the second row of a room's maps goes to the panel of the room, not up to a map of
//     the first row.
//   - Left or right from Back, at the bottom of a leaderboard, goes nowhere, not to the button of a map at its top.
//   - On a phone, left from Leaderboards, under the title, does not go to a button far below it.
//
// Up and down. The focus goes to the row above or below, as to the next line of a text. Of the controls that way, the
// row is those level with the nearest (nextRow). In the row, the focus goes to the control nearest the span that it
// keeps, which the next paragraph explains. The standard's distance does not do this, because it weighs going far to
// the side against going on further. With it, the focus went from the Settings tab of a room past the Private room
// row, whose one control is at the far right, to the buttons of Room size under it.
//
// Out of its own group, or in no group, the focus goes first to the next row of what is in line with it. It goes to
// another column only when nothing is in line. Up from the maps to play offline goes to the header above them, not to
// Back to the lobby, which ends a little above them in the column beside.
//
// The span kept. While the focus goes on up or down, it keeps the span across the way of the control where it began
// going that way, as the cursor of a text keeps its column. Up again from Rounds goes to the title, above All maps,
// not to Leaderboards, above Rounds. Down again goes back to All maps. Left and right keep the span up and down. Any
// other move of the focus forgets the span.
//
// The span picks the control within a row, as the column picks the place within a line of text. It picks neither
// the row nor the group.
//   - A group is in line when it is in line with where the focus is (at). The focus so keeps to its column of the
//     page. With the span, the focus went down from Leaderboards, with no rooms beside, to Make a room and on to Pick
//     a map, and from there back up to Leaderboards, in line with the span, past Play together, in line with Pick a
//     map.
//   - Left and right keep to the row that the focus is in. With the span, the focus went left from Copy link to
//     Shangri-La, the last map in the first row of a room's maps, and no further left, because no map is in line with
//     Copy link.
const GROUP = 'header, .panel';

// Returns the nearest group around e, within root. For an element in no group, it returns root.
function groupOf(e: HTMLElement, root: HTMLElement): HTMLElement {
  for (let g = e.parentElement; g && g !== root; g = g.parentElement) if (g.matches(GROUP)) return g;
  return root;
}

// Returns what the control c counts as in the group g: the outermost group around c within g, as a whole, or c
// itself.
function unitOf(c: HTMLElement, g: HTMLElement): HTMLElement {
  let unit = c;
  for (let p = c.parentElement; p && p !== g; p = p.parentElement) if (p.matches(GROUP)) unit = p;
  return unit;
}

// An element and its box in the window.
type Placed = [e: HTMLElement, box: DOMRect];

// Returns where the focus goes up or down from a, among list. Of the elements ahead of a (with anywhere, of all of
// them), the row is those level with the nearest. Of that row, it returns the element nearest to the span across of
// at(it), which is that of a unless at says otherwise. Of elements as near, the one nearest to the middle of that
// span wins, and then the first in the page's order.
function nextRow(
  way: Way, a: Box, list: HTMLElement[], anywhere = false, at: (e: HTMLElement) => Box = () => a,
): HTMLElement | null {
  const boxes = list.map((e): Placed => [e, e.getBoundingClientRect()])
    .filter(([, b]) => anywhere || ahead[way](a, b));
  if (!boxes.length) return null;
  const gap = ([, b]: Placed) => Math.max(0, way === 'down' ? b.top - a.bottom : a.top - b.bottom);
  const near = boxes.reduce((m, x) => (gap(x) < gap(m) ? x : m));
  const row = boxes.filter((x) => x === near || (x[1].top < near[1].bottom && x[1].bottom > near[1].top));
  const side = ([e, b]: Placed) => Math.max(0, b.left - at(e).right, at(e).left - b.right);
  const middle = ([e, b]: Placed) => Math.abs(b.left + b.right - at(e).left - at(e).right);
  return row.reduce((m, x) => (side(x) < side(m) || (side(x) === side(m) && middle(x) < middle(m)) ? x : m))[0];
}

// Moves the focus from a control the way the gamepad points, among list, as the comment above GROUP says.
function move(way: Way, list: HTMLElement[], from: HTMLElement): void {
  const flat = way === 'left' || way === 'right';
  const pick = flat ? nearest : nextRow;
  const r = from.getBoundingClientRect();
  // The span is kept from the control's own box, because the page may have scrolled since the last move.
  const kept = memory && memory.at === from && memory.flat === flat ? memory.span : null;
  const span = kept ? kept.map((s) => s + (flat ? r.top : r.left)) : flat ? [r.top, r.bottom] : [r.left, r.right];
  const a: Box = flat ? { left: r.left, right: r.right, top: span[0], bottom: span[1] }
    : { left: span[0], right: span[1], top: r.top, bottom: r.bottom };
  const root = scope();
  let to: HTMLElement | null = null;
  // Looks among the controls and the groups of the focus's own group, then of the group around that, and so on out.
  for (let came = from, g = groupOf(from, root); ; came = g, g = groupOf(g, root)) {
    const units = new Map<HTMLElement, HTMLElement[]>();
    for (const c of list) {
      if (!g.contains(c) || came.contains(c)) continue;
      const u = unitOf(c, g);
      units.set(u, (units.get(u) || []).concat(c));
    }
    const own = came === from && g !== root;
    const all = [...units.keys()];
    // What a unit is in line with: where the focus is. Only for a control, up or down, it is the span kept.
    const at = (u: HTMLElement): Box => (flat || u.matches(GROUP) ? r : a);
    const lined = all.filter((u) => (flat ? rows : columns)(at(u), u.getBoundingClientRect()));
    const unit = flat ? nearest(way, a, lined)
      : (!own && nextRow(way, a, lined, false, at)) || nextRow(way, a, all, false, at);
    const inUnit = unit && units.get(unit);
    if (inUnit) to = pick(way, a, inUnit) || pick(way, a, inUnit, true);
    if (to || g === root) break;
  }
  if (!to) return;
  const t = to.getBoundingClientRect();
  focus(to);
  memory = { at: to, flat, span: span.map((s) => s - (flat ? t.top : t.left)) };
}

// ---- What a press does ----

// A presses the control. A text field gets Enter. A map is picked, unless it is picked already, and the focus goes on
// to the button that plays it (Ready, Play), because a room opens with the focus on its maps.
function activate(e: HTMLElement): void {
  if (e.tagName === 'INPUT') {
    e.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', bubbles: true, cancelable: true }));
    return;
  }
  const grid = maps();
  if (grid && grid.contains(e)) {
    if (e.getAttribute('aria-checked') !== 'true') e.click();
    const play = mainButton(controls(), grid);
    if (play) focus(play);
    return;
  }
  e.click();
}

// B goes back, by the first of these that holds:
//   - a question shows: the answer is No
//   - the pictures to pick from show: they close
//   - the profile's menu is open: it closes
//   - the player is ready in a room: they are no longer ready
//   - the view has maps, and the focus is not on them: the focus goes back to the picked map
//   - the leaderboards show: they go back to what showed before them
//   - the page to play offline shows: it goes back to the lobby
//   - a room shows: the player leaves it once they say so (question.ts: askLeave). B is then on the maps, or the room
//     shows no maps, because it is another player's or has a game.
//   - the results of a game show: their Back button is clicked
function back(from: HTMLElement | null): void {
  if (asking()) return answerQuestion(false); // not a click on its No: the game's B closes a popup with no sound
  if (pickerOpen()) return openPicker(false);
  if (!$('profile').classList.contains('hidden')) {
    // Esc closes it, and gives the focus back to the name (profile.ts: profileInit).
    $('namechip').dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', code: 'Escape', bubbles: true }));
    return;
  }
  // The player is ready in a room, and waits for the others or for the countdown to its game. B makes them not
  // ready, which calls the countdown off. The Ready button does the same then: it says Unready, or Cancel under
  // Starting in.
  const room = !$('inside').classList.contains('hidden') && view && view.room;
  if (view && room && room.state === 'open' && view.you.ready) {
    if (usable($('ready')) && !isOff($<HTMLButtonElement>('ready')) && !readyGets) $('ready').click();
    else readyNow(false);
    return;
  }
  const grid = maps();
  const picked = grid && !(from && grid.contains(from)) && pickedMap(grid);
  if (picked) return focus(picked);
  // Not a click on their Back buttons: the game's B closes a menu with its closing sound alone (sounds.ts: menuSound).
  if (!$('boards').classList.contains('hidden')) return openBoards(false);
  if (!$('solo').classList.contains('hidden')) return openSolo(false);
  if (!$('inside').classList.contains('hidden')) return askLeave();
  const b = $('resultsback');
  if (usable(b)) b.click();
}

// Does what a press of a button or a direction does. The gamepad has the page from then on. While the options menu
// is open, Start closes it, and its cursor takes every other press (settings.ts: pad).
function press(n: Press): void {
  const had = on();
  document.body.classList.add('padnav');
  if (!had) pictureTip('focus'); // a picture that has the focus now has its ring, and so its tip (profile.ts)
  if (settingsShown()) return n === 'start' ? bo1Settings.close() : bo1Settings.pad(n);
  if (n === 'start') {
    if (asking() || !usable($('settings'))) return;
    openProfile(false); // a click outside the profile's menu closes it too
    $('settings').click();
    return;
  }
  const list = controls();
  const from = focused(list);
  if (n === 'b') return back(from);
  // The first press after the mouse had the page, or with the focus on no control, shows where the focus is.
  if (!from) return focus(home(list), true);
  if (!had) return;
  if (isWay(n)) inWindow(from) ? move(n, list, from) : focus(enter(n, list) || from);
  else if (n === 'a') inWindow(from) ? activate(from) : focus(from, true);
}

/** The gamepad in the lobby, as the rest of the page uses it. */
export const padNav = {
  /**
   * Does what a press of a button or a direction does in the lobby: 'a', 'b', 'lb', 'rb', 'start', 'up', 'down',
   * 'left' or 'right'.
   */
  press,
  /** Returns how many gamepads the page reads. */
  count: (): number => padCount,
};

let padTold = false; // what the lobby last heard from this page

/**
 * Tells the lobby whether the page reads a gamepad that the game can use (POST /lobby/gamepad), unless the lobby last
 * heard the same from this page. The room of the player then shows a gamepad on them, to everyone in it (room.ts:
 * showRoom, PAD_ICON). The page tells the lobby when its first gamepad comes and when its last one goes (frame). A
 * page that is offline, or has no token yet, tells it nothing.
 *
 * With again, the page takes the lobby to know of no gamepad, and so tells it of one again. The lobby keeps the
 * answer only while a page of the player's is open, so the page tells it again when the lobby's events start
 * (session.ts: listen).
 *
 * A browser shows a page a gamepad only once one of its buttons is pressed with the page in front. So the room shows
 * the gamepad from that press on, not from when it was plugged in.
 */
export function tellGamepad(again = false): void {
  if (again) padTold = false;
  const has = padNav.count() > 0;
  if (has === padTold || offline || !me.token) return;
  padTold = has;
  ask('gamepad', { gamepad: has });
}
