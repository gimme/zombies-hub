// web/src/music.ts - the lobby's music: the music of the game's Zombies menu, which the game loops in its own lobby.
//
// The server sends it from the game files (server/mapart.go: GET /music) as a WAV of MS-ADPCM, the form the game keeps
// it in. Browsers don't play that, so the page decodes it (adpcm.ts) into a buffer that Web Audio loops.
//
// The music plays while the page shows the lobby (musicShown), and not while the page asks the player to sign in. A
// browser plays no sound before the player's first gesture on the page (gestures.ts), unless they have played sound on
// it before. The music starts at that gesture, or at once where the browser allows it. The button at the top turns the
// music off and on, and the browser remembers the choice (zombies-music). The music stops when a game starts
// (musicStop), because the engine has its own sounds, and starts again when the page is back in the lobby
// (musicAgain). It pauses too while the player's game runs outside the page, which has its own sounds as well: their
// own Black Ops II, or the Windows client (musicOutside).
import { decodeAdpcm } from './adpcm';
import { $ } from './dom';
import { GESTURES } from './gestures';
import { lobbyMode } from './params';
import { bo1Settings } from './settings';
import { store } from './storage';

export const music = {
  ctx: null as AudioContext | null,
  gain: null as GainNode | null,
  buffer: null as AudioBuffer | null,
  off: store.get('zombies-music') === 'off',
  started: false,
  stopped: false,
  shown: false,
  outside: false,
};

// The level the game plays the music of its Zombies menu at. The alias mus_zmb_mainmenu in code_post_gfx.ff is at
// -10 dB, like the music of the game's other menus. Its sound group, music, plays at -2 dB, under master at -4 dB
// (server/sounds.go has the groups, for the menus' sounds). The game multiplies that by the square of the music volume
// and of the master volume that the player set (bo1Settings.musicGain; bo1-zombies/src/sound/snd.cpp:
// SND_GetBaseLevel). The result is 0.04 at the game's default volumes (1 and 0.5).
const MUSIC_VOLUME = 0.3162 * 0.7943 * 0.6310;
const musicLevel = (): number => MUSIC_VOLUME * bo1Settings.musicGain();

/**
 * Follows a change of the player's volumes. The level is eased over a few hundredths of a second, for a slider that
 * is dragged.
 */
export function musicLevelChanged(): void {
  if (!music.ctx || !music.gain || music.stopped) return;
  music.gain.gain.setTargetAtTime(musicLevel(), music.ctx.currentTime, 0.03);
}

/** Shows the music's button, loads the music and plays it once the browser allows it. */
export function musicInit(): void {
  if (typeof AudioContext === 'undefined' || !lobbyMode) return;
  const button = $('music');
  button.classList.remove('hidden');
  button.setAttribute('aria-pressed', String(!music.off));
  button.title = music.off ? 'Music: off' : 'Music: on';
  button.onclick = () => {
    music.off = !music.off;
    store.set('zombies-music', music.off ? 'off' : 'on');
    button.setAttribute('aria-pressed', String(!music.off));
    button.title = music.off ? 'Music: off' : 'Music: on';
    musicPlay();
  };
  const loaded = fetch('/music')
    .then((rsp) => (rsp.ok ? rsp.arrayBuffer() : Promise.reject(new Error(String(rsp.status)))));
  loaded.then((wav) => {
    const { channels, rate } = decodeAdpcm(wav);
    music.ctx = new AudioContext({ sampleRate: rate });
    const buffer = music.buffer = music.ctx.createBuffer(channels.length, channels[0].length, rate);
    channels.forEach((samples, c) => buffer.copyToChannel(samples, c));
    music.gain = music.ctx.createGain();
    music.gain.gain.value = musicLevel();
    music.gain.connect(music.ctx.destination);
    console.log(`lobby: music, ${Math.round(buffer.duration)} s at ${rate} Hz${music.off ? ', off' : ''}`);
    musicPlay();
  }).catch((e) => {
    console.log('lobby: no music (' + e.message + ')');
    button.classList.add('hidden');
  });
  // The page can't tell which event the browser counts as a gesture (gestures.ts), so each of them tries to play. When
  // the page waited for the first event alone, the music stayed silent on a phone, where that event is a finger's
  // touch, which is no gesture to Chrome.
  for (const type of GESTURES) document.addEventListener(type, () => musicPlay(), true);
}

// Plays the music as the player wants it: looping from its start the first time, and going on after the button turned
// it off.
function musicPlay(): void {
  if (!music.ctx || music.stopped) return;
  if (music.off || !music.shown || music.outside) {
    music.ctx.suspend().catch(() => {});
    return;
  }
  if (!music.started && music.gain) {
    music.started = true;
    const source = music.ctx.createBufferSource();
    source.buffer = music.buffer;
    source.loop = true;
    source.connect(music.gain);
    source.start();
  }
  music.ctx.resume().catch(() => {});
}

/** Tells the music whether the lobby shows. It does when the player is signed in or offline, and not at the sign-in. */
export function musicShown(shown: boolean): void {
  music.shown = shown;
  musicPlay();
}

/**
 * Tells the music whether the player's game runs outside the page, from its start until the room has no game. The
 * music pauses meanwhile, and then goes on from where it was.
 */
export function musicOutside(outside: boolean): void {
  if (outside === music.outside) return;
  music.outside = outside;
  musicPlay();
}

/** Stops the music when a game starts. */
export function musicStop(): void {
  music.stopped = true;
  if (music.ctx) music.ctx.close().catch(() => {});
  $('music').classList.add('hidden');
}

/**
 * Plays the music again, from its start, when the page is back in the lobby after a game. It gets a new audio
 * context.
 */
export function musicAgain(): void {
  if (!music.stopped) return;
  music.stopped = false;
  music.started = false;
  // a context that the music made when it finished loading during the game
  if (music.ctx && music.ctx.state !== 'closed') music.ctx.close().catch(() => {});
  music.ctx = null;
  if (!music.buffer) return;
  music.ctx = new AudioContext({ sampleRate: music.buffer.sampleRate });
  music.gain = music.ctx.createGain();
  music.gain.gain.value = musicLevel();
  music.gain.connect(music.ctx.destination);
  $('music').classList.remove('hidden');
  musicPlay();
}
