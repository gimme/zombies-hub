// web/src/gestures.ts - the player's gestures, after the first of which the browser lets the page play sound.
//
// A gesture is a click, a key, or a press of a gamepad's button. Chrome counts a gamepad's press as user activation
// when getGamepads() sees it. The code that reads the gamepad then tells the rest of the page with a PAD_PRESS event:
// padNav in the lobby (padnav.ts), and pollPads in the game (bo1-zombies/web/src/library_bo1.js), where the game's
// sound and the loading movie start at it too.
//
// Of a mouse, Chrome counts the press (pointerdown). Of a finger, it counts the lift (pointerup) and not the touch. Of
// a key, it counts any but Esc. So not each of these events is a gesture to the browser, and what waits for a gesture
// tries at each of them (music.ts, sounds.ts).

export const PAD_PRESS = 'bo1-padpress';
export const GESTURES = ['pointerdown', 'pointerup', 'keydown', PAD_PRESS];
