// web/src/main.ts - the page's entry: it loads the engine's host, makes the player with it, and starts the page.
//
// The page shows the lobby of the server that served it (server/rooms.go). Players group into rooms, pick a map, and
// say that they are ready. Once everyone in a room is ready, its game starts, and each player's page plays it. A
// page can also play a map alone.
//
// The page's code is in parts, one for each thing that the page does. The parts are layered: a part imports only
// from the parts below it. From the bottom:
//   - helpers: dom.ts, storage.ts, params.ts, format.ts, notice.ts, screen.ts
//   - the lobby's state and the calls to it: lobby.ts, with the types of what the server sends in api.ts
//   - the engine's host and the game files: engine.ts, files.ts
//   - a game in the page: game.ts
//   - the views: profile.ts, rooms.ts, room.ts, solo.ts, result.ts, boards.ts
//   - what draws the views from the lobby's view: show.ts
//   - the gamepad in the lobby, the session with the lobby, and the game folder: padnav.ts, session.ts, folder.ts
//   - this file
//
// URL parameters:
//   ?room=KXQM            joins that room. A room's link has it.
//   ?autostart=1          plays at once: makes a private room and says that the player is ready, which starts its
//                         game. With ?room=, it only joins the room.
//   ?local=1              shows the page to play locally (solo.ts)
//   ?boards=1             shows the leaderboards (boards.ts)
//   &name=Ann             the player's name. It is kept for the next visit, as a name that is typed in is.
//   ?mac=1                shows a Mac's page on a PC, and ?mac=0 a PC's on a Mac: a Black Ops II room's line that
//                         installs the launcher (windows.ts: onMac)
//   ?map=zombie_theater   plays that map in the browser alone, with no lobby: zombie_theater, zombie_pentagon,
//                         zombietron, zombie_cod5_* and so on
//   &args=+set%20foo%201  more of the engine's command line
//   &dedicated=1          runs a dedicated server, with no window (testing)
//   &perf=1, &perf=tour   shows what each frame takes (perf.ts)
//   &recommended=sm_enable:0
//                         one more recommended setting, as dvar:value, with commas between several. A new player's
//                         first game starts with the recommended settings (settings.ts: RECOMMENDED).
//                         docker/pipelines.sh uses it to draw each map's spawn with shadow maps off too.
//
// URL parameters for debugging the renderer (bo1-zombies/web/src/d3d9_webgpu.cpp, library_gpu.js):
//   &gpu=8                flags, added up:
//                            1  no culling
//                            2  inverted culling
//                            4  logs every draw of the next frame
//                            8  logs every 10 s: the frame rate, the draws, the calls to WebGPU, the bytes written,
//                               the pipelines compiled, and the waits for a pipeline
//                           16  logs the profile of the next frame: the draws and state calls of each pass, and the
//                               bytes written, by where they went
//                           32  makes a call to WebGPU for each state that the engine sets, which the renderer
//                               otherwise avoids (to compare)
//                          128  also works each draw out in full, without what earlier draws worked out, and logs
//                               where the two differ, and every 10 s how many draws were checked
//                          256  drops every pipeline, so that draws make them again, and stand-ins draw meanwhile.
//                               Set it from DevTools: Module.bo1GpuFlags |= 256
//                         1024  binds the vertex shader constants of each draw as a uniform block, where the
//                               renderer otherwise says with the draw's first instance where they are (to compare)
//                         2048  makes a call to WebGPU for each draw, where the renderer otherwise draws alike
//                               draws as one (to compare)
//                         4096  keeps the page at most two frames ahead of the GPU, where it otherwise goes a third
//                               while the GPU keeps pace (to compare)
//   &pipelines=0          compiles none of the map's list of pipelines. Every draw's pipeline is then a miss, and is
//                         drawn with a stand-in until it is ready, which shows the stand-ins.
//   &compile=15           makes at most 15 pipelines a second ready, as on a device that compiles slowly. It shows
//                         what such a player waits for, and what is drawn meanwhile.
//   &salt=7               makes the game's shaders differ by a constant (7e-30) from any that were compiled before,
//                         so that neither the browser's cache nor the graphics driver's has them. Pipelines then
//                         compile as on a machine that never drew the game. Each test needs a number not used before.
//   &done=12              has the page hear that a frame is done 12 ms late, as on a device whose GPU keeps pace
//                         but takes long to say so. With &donerate=40, it hears of at most 40 frames a second, as
//                         from a GPU that is behind. They show how far ahead of the GPU the page goes.
import { atmosphereStart, setBackdrop } from './backdrop';
import { boardReport, boardTip, boardsButton, boardsOpen, closeBoardReport, openBoards } from './boards';
import { $, isOff } from './dom';
import { canRun, checkSupport, type Host, player, setPlayer, started } from './engine';
import { readyNow, setFolderNote, size } from './files';
import { chooseFolder, forgetFolder, getMap, loadFolder, storeSaid } from './folder';
import {
  buildRefused, checkLock, ending, gameEnded, gameFailed, lastEnd, loadingScreenShows, movieSaid, otherTab,
  outroStarted, playAlone, playHere, playMapAlone, reloadOnClick,
} from './game';
import { act, ask, me, offline, page, view } from './lobby';
import { lastTitle, soloMapName } from './maps';
import { musicInit } from './music';
import { dedicated, lobbyMode, map, params } from './params';
import { perfTour, perfTourRun, showPerf, showPerfView, tourSaid } from './perf';
import { profileInit } from './profile';
import { askInPage, asking } from './question';
import { readyGets, roomInit } from './room';
import { canvas, overlay, setStatus, showOverlay, showToast, statusEl } from './screen';
import { reload } from './server';
import { hello } from './session';
import { bo1Settings } from './settings';
import { openHome, showKept } from './show';
import { openSolo, soloConnects, soloGets, soloLaunches } from './solo';
import { soundsInit } from './sounds';
import { store } from './storage';
import { openLauncher } from './windows';

// Makes the page's player with the engine's host. The page gives the host the player's settings and what the URL
// asks of the engine, and hears from it what the engine says.
function makePlayer(host: Host): void {
  const number = (name: string) => parseFloat(params.get(name) || '') || 0;
  setPlayer(host.createPlayer({
    canvas,
    movie: $<HTMLCanvasElement>('movie'),
    settings: bo1Settings,
    dedicated,
    args: (params.get('args') || '').split(' '),
    recommended: Object.fromEntries((params.get('recommended') || '').split(',').map((s) => s.split(':'))
      .filter((s) => s.length === 2 && /^\w{1,31}$/.test(s[0]) && /^[\w.-]{1,31}$/.test(s[1]))),
    gpuTimings: showPerfView, // the GPU's clock, for what a frame takes the GPU (perf.ts)
    // The renderer's debugging parameters, which the header lists. DevTools can change bo1GpuFlags while the game runs.
    module: {
      bo1GpuFlags: parseInt(params.get('gpu') || '0', 10) || 0,
      bo1PipelineList: params.get('pipelines') !== '0',
      bo1CompileRate: number('compile'),
      bo1ShaderSalt: number('salt'),
      bo1DoneLate: number('done'),
      bo1DoneRate: number('donerate'),
      bo1Perf: showPerf, // the engine reports what each frame takes, twice a second
    },
    log: (text) => console.log('lobby: ' + text),
    onStatus: (text) => {
      setStatus(text);
      showToast(text);
    },
    onLoading: loadingScreenShows,
    onMovie: movieSaid,
    onOutro: outroStarted,
    onGame: tourSaid, // the game's answer to bo1_player, which &perf=tour asks for
    // The player waited for the GPU's pipelines before going into the game (bo1-zombies/web/src/d3d9_webgpu.cpp:
    // SayWaited). The lobby logs it with the map, the GPU and the browser, so that its log shows what made a first
    // load slow (server/rooms.go: waited). A page that plays a map alone (?map=) does not tell the lobby: the lobby
    // may not know the page, and would have it say hello, which makes the room that ?autostart=1 asks for.
    onWaited: (text) => {
      const said = (player.module.bo1LoadingMap || 'no map') + ': ' + text + '; ' + player.gpu.replace(/\n/g, ', ') +
        ', ' + (navigator.hardwareConcurrency || '?') + ' cores';
      const tell = !params.get('map') && !!me.token && !offline;
      console.log('[bo1] webgpu: ' + (tell ? 'the lobby hears of the wait' : 'a wait the lobby would hear of') + ': ' +
        said);
      if (tell) ask('waited', { text: said });
    },
    onBuild: buildRefused,
    onEnd: (why, text) => {
      gameEnded(why, text);
    },
    onError: (text) => {
      showOverlay();
      setStatus(text, true);
      gameFailed(text);
    },
    onStore: storeSaid,
    onUsage: (bytes) => {
      $('filestext').textContent = bytes ? size(bytes) : 'None';
      $('filesremove').classList.toggle('hidden', !bytes);
    },
    // Another tab took the lock, and the game is that tab's now.
    onLockLost: () => {
      if (started && !ending) gameEnded('tab', 'Your game moved to another tab.', true);
    },
  }));
}

// Makes the buttons work that open Black Ops' options menu (settings.ts): the one of the page to play locally, and
// the one of a room on a Black Ops map. The views show them (solo.ts, room.ts). The menu changes the player's config,
// which the browser's store keeps for the next game. A game starts once the changes are saved (game.ts: startEngine).
// When the menu closes, the focus is back on the button that opened it.
function settingsInit(): void {
  for (const id of ['settings', 'roomsettings']) {
    const button = $(id);
    button.onclick = () => {
      button.setAttribute('aria-expanded', 'true');
      bo1Settings.open('settings', () => {
        button.setAttribute('aria-expanded', 'false');
        button.focus();
      });
    };
  }
}

// What a click on the room's button does, by what the button is at (room.ts: showRoomButton).
const roomDoes: Record<string, () => unknown> = {
  map: () => view && view.room && getMap(view.room.map, readyGets),
  ready: () => view && readyNow(!view.you.ready),
  rejoin: () => playHere(true), // back to the player's character, which waits in the game for a few minutes
  here: () => playHere(false), // from another tab of the player's
  launch: () => openLauncher(), // a game that the player's own PC plays, of Black Ops II or III
};

// Starts the page, once it has the engine's host. Everything that shows the lobby reads the host's store.
function startPage(host: Host): void {
  checkSupport(host);
  makePlayer(host);
  loadFolder();
  atmosphereStart();
  if (!lobbyMode) startMapPage();
  else startLobbyPage();
}

// Starts a page that plays a map alone (?map=). A click on the page starts the game.
function startMapPage(): void {
  $('mapline').textContent = 'Map: ' + map;
  $('mapline').classList.remove('hidden');
  setBackdrop(map);
  $('play').classList.remove('hidden');
  overlay.classList.add('clickable');
  player.store.start();
  overlay.addEventListener('click', () => {
    if (reloadOnClick) reload(true);
    else if (!started) playMapAlone();
    else {
      overlay.classList.add('hidden');
      canvas.focus();
    }
  });
  let autostart = params.get('autostart') === '1';
  try {
    // The player had started the game, and the page reloaded to the server's new build (server.ts: reload).
    if (sessionStorage.getItem('zombies-start')) {
      sessionStorage.removeItem('zombies-start');
      autostart = true;
    }
  } catch {
    // Without storage, the player clicks to play again.
  }
  if (autostart) playMapAlone();
  if (perfTour) perfTourRun();
}

// Starts a page that shows the lobby: sets what the controls of its front do, and says hello to the lobby.
function startLobbyPage(): void {
  $('lobby').classList.remove('hidden');
  overlay.addEventListener('click', (e) => {
    if (reloadOnClick && (e.target as Element).closest('#play')) reload(false);
  });
  profileInit();
  // A mark on a board of all the maps shows its tip while the pointer is on it and the mark stays where it is.
  $('boardtables').addEventListener('mouseover', (e) => {
    boardTip((e.target as Element).closest<HTMLElement>('[data-tip]'));
  });
  $('boardtables').addEventListener('mouseout', () => boardTip(null));
  overlay.addEventListener('scroll', () => boardTip(null), { passive: true });
  roomInit();
  $('folderpick').onclick = () => chooseFolder('');
  $('folderforget').onclick = () => {
    console.log('lobby: no game folder');
    setFolderNote('');
    forgetFolder();
  };
  // A new room is of the game that the player last picked, where the server hosts it (maps.ts: lastTitle).
  $('make').onclick = () => {
    const title = lastTitle.get();
    act('create', view && view.titles.some((t) => t.id === title) ? { title } : {});
  };
  $('soloopen').onclick = () => openSolo(true);
  $('soloback').onclick = () => openSolo(false);
  $('boardsopen').onclick = boardsButton;
  $('boardsback').onclick = () => openBoards(false);
  $('home').onclick = openHome;
  // The two main buttons take a click while they are off too, and do nothing (controls.ts: setButton).
  const playButton = $<HTMLButtonElement>('playalone');
  playButton.onclick = () => {
    if (isOff(playButton)) return;
    // a game that the player's own PC plays: the launcher gets the PC ready for it first (Connect), or launches it
    if (soloLaunches) openLauncher(!soloConnects, soloLaunches);
    else if (soloGets) getMap(view ? soloMapName(view) : '', soloGets);
    else playAlone();
  };
  $('filesremove').onclick = async () => {
    if (player.store.start() && !asking() &&
      await askInPage('Remove the game files from this browser?', 'Remove', 'Keep')) player.store.remove();
  };
  player.store.start();
  const code = $<HTMLInputElement>('code');
  const joinCode = () => {
    const room = code.value.trim().toUpperCase();
    if (room) act('join', { room });
  };
  $('joincode').onclick = joinCode;
  code.addEventListener('keydown', (e) => e.key === 'Enter' && joinCode());
  const readyButton = $<HTMLButtonElement>('ready');
  readyButton.onclick = () => !isOff(readyButton) && roomDoes[readyButton.dataset.does || '']?.();
  $('resultsback').onclick = () => {
    if (boardsOpen && boardReport) {
      closeBoardReport();
      return;
    }
    const result = view && view.room && view.room.result;
    if (result) store.set('zombies-seen-result', result.id);
    page.redraw();
  };
  // The tab that plays may close without a word to the lobby. A tab that left the game to it looks every 2 s.
  setInterval(() => otherTab && checkLock(), 2000);
  // A page that can't run the game still has the lobby, for a player who plays in the Windows client.
  if (!canRun) {
    statusEl.textContent += ' You can still play with the Windows client, from the menu under your name.';
  }
  musicInit();
  soundsInit();
  settingsInit();
  if (lastEnd && lastEnd.view) showKept(lastEnd.view);
  hello();
}

// The engine's host is a file of its own at a fixed address, and not a part of this script's bundle: a page of
// another project loads it too (docker/player/host.mjs). The address is in a variable so that Vite leaves the import
// as it is.
const hostUrl = '/player.js';
import(/* @vite-ignore */ hostUrl).then(startPage, (e: Error) => {
  console.log('lobby: no player.js: ' + e.message);
  setStatus('The page could not load player.js, which runs the game: ' + e.message, true);
});

