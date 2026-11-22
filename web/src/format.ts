// web/src/format.ts - how the page writes numbers and the game's text.

/** Seconds as a clock shows them: 12:05, 1:02:05. */
export const clock = (s: number): string =>
  (s >= 3600 ? Math.floor(s / 3600) + ':' + String(Math.floor(s / 60) % 60).padStart(2, '0') : Math.floor(s / 60)) +
  ':' + String(s % 60).padStart(2, '0');

/** The game's text without its colour codes (^1 ...). */
export const plain = (text: unknown): string => String(text || '').replace(/\^\d/g, '').trim();
