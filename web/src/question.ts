// web/src/question.ts - the lobby's own question, over the page, and the question that leaving a room's game asks.
//
// The page asks its questions itself, not with the browser's confirm(). While confirm() shows, the page can't read
// the gamepad, so the gamepad could not answer it. The page's own question is answered with the gamepad as with the
// mouse and the keyboard: B and Esc are No (padnav.ts).
import type { View } from './api';
import { $ } from './dom';
import { act, view } from './lobby';
import { focusQuietly } from './sounds';

let askAnswer: (yes: boolean) => void = () => {}; // answers the question that shows

/**
 * Asks a question over the page, with a button for each answer. Resolves to true for yes. The question opens with
 * the focus on the No button, and gives the focus back to what had it.
 *
 * The buttons sound as those of the game's popups do (sounds.ts). An answer that is no click on a button has no
 * sound, as Esc has none on the game's popups: Esc, the gamepad's B, a click beside the question, or answerQuestion.
 */
export function askInPage(question: string, yes: string, no: string): Promise<boolean> {
  const box = $('ask');
  const had = document.activeElement;
  $('asktext').textContent = question;
  $('askyes').textContent = yes;
  $('askno').textContent = no;
  box.classList.remove('hidden');
  $('askno').focus();
  return new Promise((done) => {
    const answer = askAnswer = (v: boolean) => {
      askAnswer = () => {};
      box.classList.add('hidden');
      box.onclick = box.onkeydown = $('askyes').onclick = $('askno').onclick = null;
      if (had instanceof HTMLElement && had.isConnected && had.getClientRects().length) focusQuietly(had);
      done(v);
    };
    $('askyes').onclick = () => answer(true);
    $('askno').onclick = () => answer(false);
    box.onclick = (e) => e.target === box && answer(false);
    box.onkeydown = (e) => {
      if (e.key !== 'Escape') return;
      e.preventDefault();
      answer(false);
    };
  });
}

/** Whether a question shows. */
export const asking = (): boolean => !$('ask').classList.contains('hidden');

/** Answers the question that shows, with no sound. */
export function answerQuestion(yes: boolean): void {
  askAnswer(yes);
}

// Leaving the room takes the player out of its game too (server/rooms.go: leave), and they can't take their character
// back. A game that is still loading is called off for everyone. So the page asks first.
const LEAVE_GAME = "Leave the game? You won't be able to rejoin.";

function inRoomGame(): boolean {
  const room = view && view.room;
  return !!room && !!room.seat && !room.left && (room.state === 'loading' || room.state === 'playing');
}

/**
 * Asks whether to leave the room's game, when the player has a seat in one. Resolves to true to go on. For all that
 * takes the player out of their room: Leave room, the page's title, a link to another room, signing out.
 */
export async function mayLeaveGame(): Promise<boolean> {
  return !inRoomGame() || askOfRoom(LEAVE_GAME);
}

let leaveAsked: { code: string; state: string } | null = null; // the room that the question asks of, and its state then

// Asks whether to leave the room that the player is in. Resolves to true for Leave, if the player is still in that
// room. The question is answered as Stay when the room is gone, when its game starts or ends, and when its countdown
// starts (leaveQuestionGone).
function askOfRoom(question: string): Promise<boolean> {
  const room = view && view.room;
  if (asking() || !room) return Promise.resolve(false);
  const asked = leaveAsked = { code: room.code, state: room.state };
  return askInPage(question, 'Leave', 'Stay').then((yes) => {
    if (leaveAsked === asked) leaveAsked = null;
    return yes && !!view && !!view.room && view.room.code === asked.code;
  });
}

/**
 * Leaves the room once the player says so, for the gamepad's B on the room's maps. It asks even when the room has no
 * game, because B is easily pressed.
 */
export function askLeave(): void {
  askOfRoom(inRoomGame() ? LEAVE_GAME : 'Leave the room?').then((yes) => yes && act('leave'));
}

/** Takes the question about leaving a room away when a view no longer has what it asked of (askOfRoom). */
export function leaveQuestionGone(v: View): void {
  const r = v.room;
  if (leaveAsked && asking() && (!r || r.code !== leaveAsked.code || r.state !== leaveAsked.state || r.startsIn)) {
    askAnswer(false);
  }
}
