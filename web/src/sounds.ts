// web/src/sounds.ts - the game's menu sounds, which the page plays where the game's menus and lobby play them.
//
// The server sends the sounds from the game files (server/sounds.go: GET /sounds). The page plays:
//
//   - a button's sound under the mouse or under the keyboard's focus (uin_navigation_over), and its click
//     (uin_navigation_click), as the game's lobby plays them. The options menu plays its own (settings.ts: SOUND).
//   - the sounds of one of the game's large menus, for a view that opens over the lobby as such a menu does
//     (menuSound).
//   - the sounds of the game's lobby: a player who comes into the room or goes (roomSounds), and the tick of the
//     countdown to the room's game (countdown.ts: countDown).
//
// Most sounds come as the game's zone keeps them, in xWMA, which browsers don't play. The web client's decoder
// (xwma.js, built from bo1-zombies/web/src/xwma_page.cpp) decodes them when the page starts. The game streams the two
// sounds of a player coming and going from its archives, as it does the music, so they come as a WAV of MS-ADPCM
// (adpcm.ts).
//
// Each sound plays at the level the game plays a menu's sound at: the volume of its alias, times the volume of its
// sound group, times the square of the sound effects volume and of the master volume that the player set
// (bo1Settings.sfxGain). A change in the options menu changes the level at once. A browser plays the sounds after the
// player's first gesture on the page (gestures.ts), like the music. They stop when a game starts (soundsStop), because
// the engine has its own.
import { decodeAdpcm } from './adpcm';
import { $, isOff } from './dom';
import { GESTURES } from './gestures';
import { me } from './lobby';
import { musicLevelChanged } from './music';
import { lobbyMode } from './params';
import { bo1Settings } from './settings';

interface Sound {
  buffer: AudioBuffer;
  /** The least and the most of its alias's volume. Each play picks a volume between them. */
  volume: [number, number];
  /** The volume of its sound group. */
  group: number;
}

/**
 * A sound as the server sends it (server/sounds.go). wav is set for a streamed sound, and the rest for an xWMA
 * sound.
 */
interface SentSound {
  volume: [number, number];
  group?: number;
  wav?: string;
  data: string;
  decoded: number[];
  channels: number;
  rate: number;
  bytesPerSecond: number;
  blockAlign: number;
}

/** The web client's xWMA decoder, an Emscripten module (xwma.js). */
interface Xwma {
  HEAPU8: Uint8Array;
  HEAP16: Int16Array;
  _malloc(size: number): number;
  _free(at: number): void;
  _bo1_xwma_decode(channels: number, rate: number, bytesPerSecond: number, blockAlign: number, dataAt: number,
    packets: number, frames: number, pcmAt: number): number;
}
// xwma.js defines it once the script has loaded.
declare const bo1Xwma: undefined | (() => Promise<Xwma>);

export const sounds = { ctx: null as AudioContext | null, list: {} as Record<string, Sound>, stopped: false };

/** Loads and decodes the sounds, and plays a button's sounds from then on. */
export function soundsInit(): void {
  if (typeof AudioContext === 'undefined' || !lobbyMode) return;
  const decoder = new Promise<Xwma>((resolve, reject) => {
    const s = document.createElement('script');
    s.src = 'xwma.js';
    s.onload = () => {
      if (typeof bo1Xwma === 'function') bo1Xwma().then(resolve, reject);
      else reject(new Error('no decoder'));
    };
    s.onerror = () => reject(new Error('no xwma.js'));
    document.head.appendChild(s);
  });
  const list: Promise<Record<string, SentSound>> = fetch('/sounds')
    .then((rsp) => (rsp.ok ? rsp.json() : Promise.reject(new Error(String(rsp.status)))));
  Promise.all([list, decoder]).then(([got, xwma]) => {
    const ctx = new AudioContext();
    for (const [name, s] of Object.entries(got)) {
      try {
        sounds.list[name] = { buffer: s.wav ? decodeStreamed(ctx, s.wav) : decodeXwma(xwma, ctx, s), volume: s.volume,
          group: typeof s.group === 'number' ? s.group : 1 };
      } catch (e) {
        console.log('lobby: the sound ' + name + ' does not decode (' + (e as Error).message + ')');
      }
    }
    if (!Object.keys(sounds.list).length) throw new Error('none in the game files');
    sounds.ctx = ctx;
    console.log('lobby: sounds, ' + Object.keys(sounds.list).join(', '));
  }).catch((e) => console.log('lobby: no sounds (' + e.message + ')'));
  bo1Settings.useSounds(playSound);
  // the player's volumes, which the music follows when they change
  bo1Settings.onVolume(musicLevelChanged);
  bo1Settings.load().catch(() => {});

  // A button's sounds. The options menu plays its own. A button sounds under the mouse, but not under a finger, which
  // clicks it, and under the keyboard's focus when Tab moves it (overSound). Its click sounds too, and that click is a
  // gesture that lets the browser play sounds from then on.
  //
  // A Back button has no click sound, as the game's has none. It plays uin_navigation_over again. The Backs are: the
  // Back of a view that opened as a large menu, with the menu's closing sound (menuSound). Leave room, like the Back
  // of the game's lobby (menu_xboxlive_lobby, in patch.ff), which opens its question with that sound. And the
  // leaderboards' button at the top while the leaderboards show, because it closes them then (boards.ts:
  // boardsButton). This listener hears the click before the button does, so the leaderboards still show.
  //
  // The lobby's own question (question.ts: askInPage) is like the game's leavelobbywarning popup (frontend.ff). Its Yes
  // plays uin_navigation_submenu_over with its click. Its No plays its click alone.
  //
  // Each button that sounds under the mouse also shows the mouse over it (the sheet's button:hover), so that no sound
  // comes from nowhere.
  const BACK = ['soloback', 'boardsback', 'leave'];
  const isBack = (b: HTMLButtonElement) =>
    BACK.includes(b.id) || b.id === 'boardsopen' && !$('boards').classList.contains('hidden');
  // A main button that is off (aria-disabled) sounds only when the focus comes to it, which it can.
  const button = (e: Event, off = false): HTMLButtonElement | null => {
    const b = e.target instanceof Element && e.target.closest('button');
    return b && !b.disabled && (off || !isOff(b)) && !b.closest('#options') ? b : null;
  };
  document.addEventListener('pointerover', (e) => {
    const b = button(e);
    if (b && e.pointerType === 'mouse' && !b.contains(e.relatedTarget as Node | null)) playSound(overSound(b));
  });
  document.addEventListener('focusin', (e) => {
    const b = button(e, true);
    if (b && !quietFocus && b.matches(':focus-visible')) playSound(overSound(b));
  });
  document.addEventListener('click', (e) => {
    const b = button(e);
    if (!b) return;
    playSound(isBack(b) ? 'uin_navigation_over' : 'uin_navigation_click', true);
    if (b.id === 'askyes') playSound('uin_navigation_submenu_over', true);
  }, true);
  const gesture = () => {
    if (sounds.ctx && sounds.ctx.state === 'suspended' && !sounds.stopped) sounds.ctx.resume().catch(() => {});
  };
  for (const type of GESTURES) document.addEventListener(type, gesture, true);
}

/**
 * The sound of a button when the mouse or the focus comes to it. A choice of the lobby's question has a popup's sound,
 * as the choices of the game's popups have.
 */
export function overSound(b: Element): string {
  return b.closest('#ask') ? 'uin_navigation_submenu_over' : 'uin_navigation_over';
}

// Set while the page moves the focus itself. A button that the focus comes to makes no sound then.
let quietFocus = false;

/**
 * Moves the focus without the sound of the button it comes to. The page uses it when it moves the focus itself and
 * the player did not: back to what had the focus when a question goes, or to where a view starts when the view
 * changes under the gamepad.
 */
export function focusQuietly(e: HTMLElement): void {
  quietFocus = true;
  e.focus();
  quietFocus = false;
}

/**
 * Plays one of the game's menu sounds, by its alias. A sound that a gesture plays (a click) plays even before the
 * browser lets the page play sounds, because the gesture lets it. Any other sound plays only after that. The sound
 * plays at the game's level, times level for a sound that the page turns down.
 */
export function playSound(name: string, gesture = false, level = 1): void {
  const s = sounds.list[name], ctx = sounds.ctx;
  if (!s || !ctx || sounds.stopped) return;
  if (ctx.state !== 'running') {
    if (!gesture) return;
    ctx.resume().catch(() => {});
  }
  const [least, most] = s.volume;
  const gain = ctx.createGain();
  gain.gain.value = (least + Math.random() * (most - least)) * s.group * level * bo1Settings.sfxGain();
  const source = ctx.createBufferSource();
  source.buffer = s.buffer;
  source.connect(gain).connect(ctx.destination);
  source.start();
}

/**
 * Plays the sound of one of the game's large menus opening or closing. A view of the lobby that opens over it as such
 * a menu does plays them: the maps to play offline, like the game's Zombies map select (levels_zombie, in frontend.ff
 * and patch.ff), and the leaderboards, like the game's (menu_leaderboards). The game's menu plays
 * uin_navigation_menu_lg_open when it opens, and uin_navigation_menu_lg_close when Esc or its Back closes it, after
 * uin_navigation_over (soundsInit: BACK). The page plays them when the player opens such a view (gesture: the click on
 * its button lets the browser play the sound) and when they close it: with its Back, the title, the gamepad's B or
 * the browser's Back. A game that starts closes such a view without the sound, because the sounds have stopped by
 * then (soundsStop).
 *
 * The sounds are soft. At the game's level they are some 14 dB under its menu's music, and a listen to the retail
 * game heard none.
 */
export function menuSound(open: boolean, gesture = false): void {
  playSound(open ? 'uin_navigation_menu_lg_open' : 'uin_navigation_menu_lg_close', gesture);
}

// The room the page last showed, and its players. It is null before the page's first view.
let roomHad: { code: string; ids: string[] } | null = null;

/**
 * Plays the game's lobby sounds for the room the page shows: uin_lobby_join when another player comes into the room,
 * or when the player comes into a room that others are in, and uin_lobby_leave when another player goes. The game's
 * own lobby plays them the same way. Nothing plays for the room the page opens in, for a room the player makes and is
 * alone in, or when the player leaves a room themself.
 */
export function roomSounds(room: { code: string; members: { id: string }[] } | null): void {
  const had = roomHad;
  const has = roomHad = { code: room ? room.code : '', ids: room ? room.members.map((m) => m.id) : [] };
  if (!had || !room) return;
  const from = had.code === has.code ? had.ids : [];
  if (has.ids.some((id) => id !== me.id && !from.includes(id))) playSound('uin_lobby_join');
  if (from.some((id) => id !== me.id && !has.ids.includes(id))) playSound('uin_lobby_leave');
}

/** Stops the sounds when a game starts. The click that started the game still sounds. */
export function soundsStop(): void {
  sounds.stopped = true;
  const ctx = sounds.ctx;
  if (ctx) setTimeout(() => ctx.close().catch(() => {}), 1000);
}

/** Plays the sounds again when the page is back in the lobby after a game. They get a new audio context. */
export function soundsAgain(): void {
  if (!sounds.stopped) return;
  sounds.stopped = false;
  if (sounds.ctx) sounds.ctx = new AudioContext();
}

// Decodes an xWMA sound with the web client's decoder into a buffer that Web Audio plays. The sound's length is the
// last entry of its table of decoded bytes, at 2 bytes a sample for each channel.
function decodeXwma(xwma: Xwma, ctx: AudioContext, s: SentSound): AudioBuffer {
  const data = Uint8Array.from(atob(s.data), (c) => c.charCodeAt(0));
  const frames = Math.floor(s.decoded[s.decoded.length - 1] / (2 * s.channels));
  const dataAt = xwma._malloc(data.length), pcmAt = xwma._malloc(frames * s.channels * 2);
  try {
    xwma.HEAPU8.set(data, dataAt);
    const n = xwma._bo1_xwma_decode(s.channels, s.rate, s.bytesPerSecond, s.blockAlign, dataAt, s.decoded.length,
      frames, pcmAt);
    if (n !== frames || !frames) throw new Error('not WMA 2 of ' + s.channels + ' channels at ' + s.rate + ' Hz');
    const pcm = new Int16Array(xwma.HEAP16.buffer, pcmAt, frames * s.channels);
    const buffer = ctx.createBuffer(s.channels, frames, s.rate);
    for (let c = 0; c < s.channels; c++) {
      const out = buffer.getChannelData(c);
      for (let i = 0; i < frames; i++) out[i] = pcm[i * s.channels + c] / 32768;
    }
    return buffer;
  } finally {
    xwma._free(dataAt);
    xwma._free(pcmAt);
  }
}

// Decodes a sound that the game streams, a WAV of MS-ADPCM in base64, into a buffer that Web Audio plays.
function decodeStreamed(ctx: AudioContext, wav: string): AudioBuffer {
  const decoded = decodeAdpcm(Uint8Array.from(atob(wav), (c) => c.charCodeAt(0)).buffer);
  const buffer = ctx.createBuffer(decoded.channels.length, decoded.channels[0].length, decoded.rate);
  decoded.channels.forEach((samples, c) => buffer.copyToChannel(samples, c));
  return buffer;
}
