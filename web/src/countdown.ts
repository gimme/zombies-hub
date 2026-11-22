// web/src/countdown.ts - the countdown to a room's game, on the room's button.
//
// Once everyone in a room is ready, its game starts after a countdown of 4 seconds (server/rooms.go). The button then
// shows "Starting in 3" and Cancel, and a click on it calls the start off.
//
// The button shows the whole seconds left: 3, 2, 1, then 0 during the last second, in which a click still calls the
// start off. A count that ended at 1 would start the game just as a player goes to click.
//
// The count ticks as the game's own lobby does, for everyone in the room. The game plays uin_timer each time the
// whole seconds left to its game are fewer, once they are under 6. The page plays it each time the number it shows
// drops: at 2, at 1 and at 0.
import { $, el } from './dom';
import { playSound } from './sounds';

let countdownTimer = 0;
let countdownEnds = 0; // when the countdown that the page last showed ends, or 0 for none
let countdownLeft = -1; // the fewest seconds that countdown has shown, or -1 before it has shown any

// The tick plays at half the game's level (-6 dB). The other menu sounds play at the game's level, and the tick was
// loud among them. All of them measure about as loud overall. But a click is 0.1 to 0.2 s of treble, nearly all of
// it above 5 kHz, and the tick is 0.4 s with nearly as much from 1.5 to 5 kHz, where the ear is most sensitive: 7 dB
// over the click there. At half the level, the tick is about as loud as the click.
const TICK_LEVEL = 0.5;

/**
 * Shows the countdown on the room's button: the game starts in ms. With 0 there is no countdown, and the button says
 * label.
 */
export function countDown(ms: number, label: string): void {
  clearInterval(countdownTimer);
  countdownTimer = 0;
  const button = $('ready');
  // While it counts, the button is a row of the count and Cancel (page.css: button.counting).
  button.classList.toggle('counting', !!ms);
  if (!ms) {
    countdownEnds = 0;
    button.textContent = label;
    return;
  }
  const ends = Date.now() + ms;
  // The countdown is a new one when the page showed none, or when it ends more than a second from where the last one
  // did. A countdown that was called off and begun again may come with no view in between that says so. A view's
  // delay moves the end of the same countdown by less.
  if (!countdownEnds || Math.abs(ends - countdownEnds) > 1000) countdownLeft = -1;
  countdownEnds = ends;
  const tick = () => {
    // The whole seconds left, where a whole number counts as the one under it: 4 s to the game show 3. It is 0 from
    // the last second on, until a view says that the game started.
    const left = Math.max(0, Math.ceil((ends - Date.now()) / 1000) - 1);
    button.replaceChildren(el('span', 't', 'Starting in ' + left), el('span', 'sub t', 'Cancel'));
    if (countdownLeft >= 0 && left < countdownLeft && left < 6) playSound('uin_timer', false, TICK_LEVEL);
    countdownLeft = countdownLeft < 0 ? left : Math.min(countdownLeft, left);
  };
  tick();
  countdownTimer = window.setInterval(tick, 200);
}
