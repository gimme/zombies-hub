// web/src/notice.ts - what the page says once, over the page, at its top.
//
// A notice shows for 6 s. It is a refusal of what the player just did, in red (showRefusal): the lobby's (no room of
// that code, a room that is full) or the page's own (its game is open in another tab, no leaderboards to show). Or it
// says how the player's last game alone ended. It goes sooner at a click on it, when the next one comes, when the
// lobby takes what the player does next, and when a game starts. Its text stays while it fades out.
import { $ } from './dom';

const noticeEl = $('notice');
let noticeTimer = 0;

/** Shows the text over the page. '' takes the notice away. */
export function showNotice(text: string, refusal = false): void {
  clearTimeout(noticeTimer);
  if (text) {
    noticeEl.textContent = text;
    noticeEl.classList.toggle('refusal', refusal);
    noticeTimer = window.setTimeout(() => noticeEl.classList.add('hidden'), 6000);
  }
  noticeEl.classList.toggle('hidden', !text);
}

export const showRefusal = (text: string): void => showNotice(text, true);

noticeEl.onclick = () => showNotice('');
