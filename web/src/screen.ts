// web/src/screen.ts - what the page has around the game: the status line, the toast, and the overlay.
//
// The overlay is everything of the page but the game: the lobby, or the status of a page that plays a map alone. It
// covers the game's canvas, and is hidden while a game has the page.
import { $ } from './dom';

/** The game's canvas. */
export const canvas = $<HTMLCanvasElement>('canvas');
export const overlay = $('overlay');
export const statusEl = $('status');
const toast = $('toast');

/**
 * Sets the status line, above the lobby. It says the state the page is in, and stays until the page sets another: what
 * the engine does, how far a download is, why the page can't run the game. A message that shows once goes over the page
 * instead (notice.ts).
 */
export function setStatus(text: string, isError = false): void {
  statusEl.textContent = text || '';
  statusEl.classList.toggle('error', isError);
}

let toastTimer = 0;

/** Shows a line over the game for 4 seconds. '' hides it. */
export function showToast(text: string): void {
  toast.textContent = text;
  toast.classList.toggle('hidden', !text);
  clearTimeout(toastTimer);
  if (text) toastTimer = window.setTimeout(() => toast.classList.add('hidden'), 4000);
}

/** Shows the overlay over the game. The game loses the keyboard and the mouse, and leaving the page asks nothing. */
export function showOverlay(): void {
  overlay.classList.remove('hidden');
  leaveAsks = false;
  canvas.blur();
  document.exitPointerLock && document.exitPointerLock();
}

// Whether leaving the page asks first. It does while a game has the page: closing the tab, reloading and going back
// all end the game. Ctrl+W closes a tab in a window, where Chrome keeps that key for itself. In full screen the game
// gets the key. Once the overlay is up, the game is over or the page reloads itself, and leaving asks nothing.
let leaveAsks = false;

/** Has the browser ask before the player leaves the page, from a game's start until the overlay shows again. */
export function askBeforeLeaving(): void {
  leaveAsks = true;
}

window.addEventListener('beforeunload', (e) => {
  if (!leaveAsks) return;
  e.preventDefault();
  e.returnValue = true; // Chrome before 119 asks only with this set
});
