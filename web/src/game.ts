// web/src/game.ts - a game in the page: its start, the lock of the tab that plays, and its end.
//
// The page plays one of three games:
//   - its room's game: the engine joins the server at the player's seat (joinUnlessElsewhere, playHere)
//   - a map alone from the lobby, Play offline: the engine runs the game itself, on no server (playAlone)
//   - a map alone on a page of its own, ?map=, which shows no lobby (playMapAlone)
//
// The engine says when its game ends, and why (bo1-zombies/src/client_mp/cl_main_mp.cpp: CL_GameEnded): game over, the
// player left, was kicked or dropped, or an error. The page then tells the lobby, stops the engine, and is back in
// the lobby, where the room says what happened (gameEnded). The page can also take the game from the engine: when it
// moves to another tab or to the Windows client, or when the room's game is gone.
//
// A player's tabs in one browser are one player, because the lobby's token is the browser's. One of them plays: the
// one that holds a lock (player.js: lock), until it closes, reloads, or another tab takes the lock. The other tabs
// show the room, and offer to move the game to them.
import type { Room, View } from './api';
import { closeBoardsForGame } from './boards';
import { $ } from './dom';
import { canRun, dlState, player, setStarted, started } from './engine';
import { mb, stopDownload, whenMap } from './files';
import { plain } from './format';
import { act, ask, me, page, view } from './lobby';
import { soloMapName } from './maps';
import { musicAgain, musicStop } from './music';
import { showNotice, showRefusal } from './notice';
import { lobbyMode, map } from './params';
import { openPicker } from './profile';
import { askBeforeLeaving, canvas, overlay, setStatus, showOverlay, showToast } from './screen';
import { fetchServer, loadedServer, reload, serverUpdated } from './server';
import { bo1Settings } from './settings';
import { soundsAgain, soundsStop } from './sounds';
import { onWindows } from './windows';

// ---- How the last game ended ----

/** How the player's last game ended. */
export interface LastEnd {
  /** The engine's word for why: over, left, kicked, dropped, or another for a failure. Or the page's: tab, windows. */
  why: string;
  text: string;
  /** The game was a map alone. */
  solo?: boolean;
  /** The lobby as the page showed it, when the page reloaded as the game ended (show.ts: showKept). */
  view?: View | null;
}

/**
 * How the player's last game ended, or null. The room says it once the page is back (room.ts), or the page to play
 * offline does (solo.ts). A page that had to reload as its game ended kept it in the session's storage.
 */
export let lastEnd: LastEnd | null = null;
try {
  lastEnd = JSON.parse(sessionStorage.getItem('zombies-ended') || 'null');
  sessionStorage.removeItem('zombies-ended');
} catch {
  lastEnd = null;
}

export function forgetLastEnd(): void {
  lastEnd = null;
}

/** What the end of the last game means to the player, in a sentence. '' for an end that needs no words. */
export function endNote(end: LastEnd | null): string {
  if (!end) return '';
  const text = plain(end.text);
  switch (end.why) {
    case 'over': return '';
    case 'left': return '';
    case 'windows': return '';
    case 'tab': return '';
    case 'kicked': return 'You were kicked from the game. ' + text;
    case 'dropped': return 'Lost the connection to the game: ' + text;
    default: return 'Your game failed: ' + text;
  }
}

// ---- The start ----

// The page runs one engine at a time, and stays when a game ends, so the next game starts in the same page. starts
// counts the starts and the stops. A start that is still under way goes no further once the count has moved on.
let starts = 0;

// The canvas is black while the engine starts and joins its game, for a few seconds, until the engine's loading screen
// shows the map (loadingScreenShows). The map's loading movie plays from the engine's start, on its own canvas under
// the game's (#movie). Its first picture comes 0.5 to 1.5 s after the click on Play. The game's canvas shows nothing
// over the movie until the loading screen does, and from then on lets the movie through where that screen draws
// nothing. Once the movie is over, the game's canvas is alone again.
let loadingShows = false;

/** The engine's loading screen shows the map that it loads (player.js: onLoading). */
export function loadingScreenShows(): void {
  loadingShows = true;
  canvas.classList.remove('unseen');
}

/** Follows the map's loading movie (player.js: onMovie). */
export function movieSaid(what: 'starts' | 'shows' | 'over'): void {
  const movie = $('movie');
  if (what === 'starts') {
    movie.classList.remove('hidden');
    canvas.classList.add('overmovie');
    if (!loadingShows) canvas.classList.add('unseen');
  } else if (what === 'shows') {
    console.log('page: the loading movie shows, ' + ((performance.now() - player.startedAt) / 1000).toFixed(1) +
      ' s after the game started');
  } else if (what === 'over') {
    console.log('page: the loading movie is over');
    movie.classList.add('hidden');
    canvas.classList.remove('overmovie', 'unseen');
  }
}

// The game that startEngine starts: the room's, by the path of the session that takes the player to their seat, or a
// map alone. cheats starts a map with cheats on (devmap).
type Game = { session: string; name: string } | { map: string; name?: string; cheats?: boolean };

// Starts the engine into a game. Returns whether the game started.
async function startEngine(what: Game): Promise<boolean> {
  if (started || !canRun) return false;
  setStarted(true);
  closeBoardsForGame();
  openPicker(false); // the picker is over the page, not in the overlay, so it would stay over the game
  const run = ++starts;
  // The host waits until the player's changed settings are saved in their config, which the engine reads
  // (player.js: settings.saved).
  bo1Settings.close();
  stopDownload();
  if (lobbyMode) {
    musicStop();
    soundsStop();
  }
  overlay.classList.add('hidden');
  showNotice('');
  setStatus('Loading the engine...');
  showToast('Loading the engine...');
  const server = await fetchServer();
  // A page that was left open while the server was updated would start another build than the server's. It reloads
  // instead, to the page that the server serves now.
  const then = await loadedServer;
  if (run !== starts) return false;
  if (then.web && server.web && then.web !== server.web) {
    setStatus('The server was updated. Reloading...');
    showToast('The server was updated. Reloading...');
    reload(!lobbyMode);
    return false;
  }
  // Says on the overlay why the game can't start.
  const fail = (text: string) => {
    showOverlay();
    setStatus(text, true);
    return false;
  };
  // Where a pipeline still compiles and its stand-in is not ready, WebGPU draws nothing, and the scene shows its clear
  // colour: black. On a dev server, the engine's own colour stays, which blinks orange and blue to show the gap
  // (bo1-zombies/web/src/d3d9_webgpu.cpp).
  const dev = !!server.dev;
  let begun;
  if ('session' in what) {
    if (!server.port) return fail('No server to join: this page must be served by one (docker/server.sh).');
    // The engine plays the loading movie of the room's map as it starts.
    begun = await player.join({ session: what.session, name: what.name, address: location.hostname + ':' + server.port,
      map: (view && view.room && view.room.map) || '', dev });
  } else {
    // The game is the page's own. Like a room's game, it ends at game over instead of starting the map again, and it
    // kicks no one for standing idle (bo1-zombies/src/server_mp/sv_lobby_mp.cpp: bo1_lobby).
    begun = await player.playAlone({ map: what.map, name: what.name, cheats: what.cheats, dev });
  }
  if (!begun.started) return begun.why ? fail(begun.why) : false;
  askBeforeLeaving();
  return true;
}

/** Whether a click on the overlay reloads the page: the game failed or ended in a way that only a reload clears. */
export let reloadOnClick = false;

// The server refused the engine, because its games run another build.
let refused = false;

/**
 * The server runs another build of the game than this engine, and refused it (player.js: onBuild). text says which
 * builds. Reloading the page gets the build that the server serves now, unless that is this one.
 */
export function buildRefused(text: string): void {
  refused = true;
  showOverlay();
  serverUpdated().then((updated) => {
    reloadOnClick = updated;
    overlay.classList.toggle('clickable', updated);
    if (updated) {
      $('play').textContent = 'Click to reload';
      $('play').classList.remove('hidden');
    }
    setStatus(text + (updated ? ' The server was updated since this page loaded.'
      : ' Reloading the page would not help: the server serves this build of the web client, but its games run ' +
        'another. Restarting the server fixes that.'), true);
  });
}

// ---- The lock of the tab that plays ----

/**
 * Whether the room's game is another tab's: another tab held the lock when this one was to join, or the page opened
 * on a game in play, which the tab that closed or reloaded had been playing. This tab then stays out of the game and
 * offers to take it.
 */
export let otherTab = false;

/** Makes the room's game any tab's to join again: the room has a new game, or the player may rejoin. */
export function forgetOtherTab(): void {
  otherTab = false;
}

/** Whether another tab holds the lock, as this tab last saw (checkLock). */
export let lockElsewhere = false;
let joining = false;

// Whether the page has just opened or reloaded, and has not looked at the lobby since. A game in play that such a
// page is to be in was another tab's, which closed, or this tab's before it reloaded: the room offers to rejoin it
// instead of joining. A game that is still loading is joined, because its round 1 waits for the player.
let justOpened = true;

/** Whether this is the page's first look at the lobby since it opened or came back from a game. Ends that state. */
export function firstLook(): boolean {
  const first = justOpened;
  justOpened = false;
  return first;
}

/** Makes the lobby's next view the page's first look at it again (firstLook). */
export function lookAgain(): void {
  justOpened = true;
}

// Takes the lock, from another tab too with steal. Returns true once this tab holds it, false when another tab does.
const takeLock = (steal: boolean): Promise<boolean> => player.lock.take(steal);
const dropLock = (): void => player.lock.release();

// When no tab has held the lock since, or 0 while one holds it.
let lockFreeSince = 0;

/**
 * Looks whether another tab holds the lock, for a tab that left the game to another: the room's button takes the
 * lock from that tab (Play here), or rejoins the game when no tab has it (Rejoin). Once no tab has held the lock for
 * 10 s, the tab that played has closed or reloaded, and the lobby hears that the player's game is in no tab. A tab
 * that reloads as its game loads takes the lock back within that time.
 */
export function checkLock(): void {
  if (!navigator.locks) return;
  player.lock.any().then((held) => {
    lockFreeSince = held ? 0 : lockFreeSince || Date.now();
    const room = view && view.room;
    const closed = lockFreeSince && Date.now() - lockFreeSince >= 10000;
    if (closed && otherTab && room && room.join && room.state === 'playing') {
      lockFreeSince = 0;
      ask('ended', { why: 'closed', text: 'The tab that played it closed or reloaded.' });
    }
    if (held === lockElsewhere) return;
    lockElsewhere = held;
    page.redraw();
  });
}

/**
 * Joins the room's game, unless another tab of the player's plays it. A tab joins only once it holds the lock. With
 * opened, the page has just opened on a game in play, and leaves it to the tab that played it (firstLook).
 */
export async function joinUnlessElsewhere(opened: boolean): Promise<void> {
  if (joining) return;
  joining = true;
  const free = await takeLock(false);
  joining = false;
  const room = view && view.room;
  if (started) return;
  if (!room || !room.join || onWindows()) {
    if (free) dropLock();
    return;
  }
  if (free && !opened) return joinGame(room);
  if (free) dropLock();
  otherTab = true;
  lockElsewhere = !free;
  page.redraw();
}

/**
 * Moves the room's game to this tab (Play here), taking the lock from the tab that has it. With rejoin, no tab plays
 * the game, and the player goes back to their character, which waits in the game for a few minutes.
 */
export async function playHere(rejoin: boolean): Promise<void> {
  await takeLock(true);
  otherTab = false;
  // The player asked for the game, so the next view joins it, even where it is the page's first look since its last
  // game ended (backToLobby).
  justOpened = false;
  if (rejoin && !(await act('rejoin'))) dropLock();
  else page.redraw();
}

async function joinGame(room: Room): Promise<void> {
  console.log('lobby: joining room ' + room.code + "'s game");
  lastEnd = null;
  // The player's seat in the game is their token's (server/relay.go). Their name is fit for a command line
  // (server/rooms.go: cleanName).
  await startEngine({ session: '/play/' + me.token, name: me.name });
}

/** Whether this page plays a map alone. */
export let solo = false;

/** Plays the map that the player picked to play offline (maps.ts: soloMapName). */
export async function playAlone(): Promise<void> {
  const name = view ? soloMapName(view) : '';
  if (!name || started) return;
  // One tab of the player's plays at a time: a room's game, or a map alone.
  if (!(await takeLock(false))) return showRefusal('Your game is open in another tab.');
  solo = true;
  lastEnd = null;
  console.log('lobby: playing ' + name + ' alone');
  await startEngine({ map: name, name: me.name || 'Player' });
}

// ---- The end ----

let leaveTimer = 0;

// Until when the engine may still be playing a map's outro. Call of the Dead's rolls at game over, for 38 s, and the
// engine says why the game ended only after it.
let outroUntil = 0;

/** The game ended, and the engine plays the map's outro first (player.js: onOutro). */
export function outroStarted(): void {
  outroUntil = Date.now() + 60000;
}

/**
 * Leaves the game in 15 s, unless the engine has ended it by then. For a game that is gone from the lobby while the
 * engine still loads or plays it. The engine says why itself when the game ends under it, after the outro. An engine
 * that is still loading may never hear.
 */
export function leaveSoon(): void {
  if (leaveTimer) return;
  const leave = () => {
    if (ending) return;
    if (Date.now() < outroUntil) leaveTimer = window.setTimeout(leave, outroUntil - Date.now());
    else gameEnded('left', 'The game ended.', true);
  };
  leaveTimer = window.setTimeout(leave, 15000);
}

/** Whether the page's game is ending: from gameEnded until the page is back in the lobby. */
export let ending = false;

/**
 * Ends the page's game: the engine ended it (player.js: onEnd), or the page takes it from the engine. The lobby hears
 * why, and the page comes back to the lobby. With unreported, the lobby is not told: the game went to another tab or
 * to the Windows client, or the lobby ended it.
 */
export async function gameEnded(why: string, text: string, unreported = false): Promise<void> {
  if (!lobbyMode) return mapGameEnded(plain(text) || (why === 'over' ? 'Game over.' : 'The game ended.'));
  if (ending) return;
  ending = true;
  console.log('lobby: the game ended (' + why + '): ' + plain(text));
  // The lobby says how the game ended, under the overlay already, so that nothing moves once the overlay shows. A
  // refusal for another build says everything itself, and what to do about it (buildRefused).
  if (!refused) {
    lastEnd = { why, text: plain(text), solo };
    setStatus(''); // the engine's last words, which the page of a reload would not have
    page.redraw();
  }
  showOverlay();
  // A map alone is no game of the lobby's. The page says itself how it ended (solo.ts).
  const reported = !unreported && !solo;
  if (reported) await ask('ended', { why, text: plain(text) });
  if (refused) return;
  if (stopEngine()) return backToLobby(reported);
  // The engine can't be stopped, so the page reloads, and shows the lobby as it is now from its first paint.
  try {
    sessionStorage.setItem('zombies-ended', JSON.stringify({ why, text: plain(text), solo, view }));
  } catch {
    // Without storage, the room says what the lobby knows.
  }
  location.reload();
}

// Stops the engine, and leaves the page as it was before the game (player.js: stop). Returns false when the engine
// can't be stopped, which is while it is still starting: only a reload ends it then. A start that is still under way
// goes no further (startEngine).
function stopEngine(): boolean {
  starts++;
  if (!player.stop()) return false;
  window.Module = player.module;
  return true;
}

// Puts the page back in the lobby once its engine has stopped, as a reload would: another tab may take the lock, and
// the lobby's music and sounds play again. With reported, the page told the lobby that the game ended, and the game is
// no longer the page's to join, as the lobby's next view will say. Otherwise the game went to another tab or to the
// Windows client, and what the lobby says still holds.
function backToLobby(reported: boolean): void {
  setStarted(false);
  ending = false;
  solo = false;
  loadingShows = false;
  outroUntil = 0;
  clearTimeout(leaveTimer);
  leaveTimer = 0;
  canvas.classList.remove('overmovie', 'unseen');
  dropLock();
  otherTab = false;
  // The game may have changed the player's config. The music's volume follows it (music.ts: musicLevelChanged).
  bo1Settings.load().catch(() => {});
  musicAgain();
  soundsAgain();
  console.log('lobby: back in the lobby, the game\'s engine stopped');
  if (reported && view && view.room) view.room.join = false;
  page.redraw();
  justOpened = true;
}

/**
 * The engine failed: a fatal error, or a crash (player.js: onError). The page stays and says so, and a click takes
 * the player back to the room, by a reload.
 */
export function gameFailed(text: string): void {
  if (!lobbyMode || ending) return;
  ending = true;
  if (!solo) ask('ended', { why: 'error', text: plain(text) });
  else {
    try {
      sessionStorage.setItem('zombies-ended', JSON.stringify({ why: 'error', text: plain(text), solo }));
    } catch {
      // Without storage, the lobby says nothing of it.
    }
  }
  reloadOnClick = true;
  overlay.classList.add('clickable');
  $('play').textContent = solo ? 'Back' : 'Back to the room';
  $('play').classList.remove('hidden');
}

// ---- A map alone on a page of its own (?map=) ----

function mapGameEnded(text: string): void {
  showOverlay();
  reloadOnClick = true;
  overlay.classList.add('clickable');
  $('play').textContent = 'Click to play again';
  $('play').classList.remove('hidden');
  setStatus(text);
}

let mapWanted = false;

/**
 * Plays the map of ?map=. The store gets the map first: the page downloads what it lacks, and the status line says
 * how far that is (showMapAlone).
 */
export async function playMapAlone(): Promise<void> {
  if (mapWanted || started) return;
  mapWanted = true;
  const ok = await whenMap(map);
  mapWanted = false;
  if (ok) startEngine({ map, cheats: true });
}

/** Says on the status line what the store has of the map of ?map=, until its game starts. */
export function showMapAlone(): void {
  const s = dlState[map];
  if (started || !s) return;
  if (s.running) setStatus('Downloading ' + map + ': ' + mb(s.have || 0) + ' of ' + mb(s.total || 0) + ' MB');
  else if (s.none) setStatus(map + ' can\'t be played in the browser.', true);
  else if (s.off) {
    setStatus('The server doesn\'t send game files. To play ' + map + ', import it from your own copy of the game in ' +
      'the lobby first.', true);
  } else if (s.error) setStatus('The download of ' + map + ' failed: ' + s.error, true);
}
