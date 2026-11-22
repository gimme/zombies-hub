// web/public/player.js - the web client's host: what a page runs the engine with, as an ES module any page can load. It knows
// whether the browser can run the game, keeps the game files a map needs in the browser's store (web/public/store.js), starts
// the engine (bo1zombies.js: bo1Engine) into a room's game or a map alone, says what the engine says, and stops it,
// leaving the page as it was. It knows no page: it takes its canvases (or an element to make them in) and its
// callbacks, and reads no element by id and no global of a page's. web/src/main.ts, the lobby's page, is one page on
// it.
//
//   import { createPlayer, support } from '/player.js';
//
//   const can = support();                      // { ok, alone, lacks, why }
//   if (!can.ok) say(can.why);
//   const player = createPlayer({
//     container: document.getElementById('game'),   // the module makes its canvases in it
//     onStatus: (text) => say(text),                // the engine's word on what it is doing
//     onEnd: (why, text) => {                       // the game ended: 'over', 'left', 'kicked', 'dropped', ...
//       if (!player.stop()) location.reload();      // (an engine still starting stops only with the page)
//       player.lock.release();
//       say(why === 'over' ? 'Game over.' : text);
//     },
//     onError: (text) => say(text),                 // the engine failed: only a reload clears it
//   });
//
//   const has = await player.store.status('zombie_theater');           // { have, total, done, none, ... }
//   if (!has || has.none) say('This browser can\'t play the map.');
//   else if (!has.done) {
//     say('Missing ' + (has.total - has.have) + ' bytes');
//     const end = await player.store.download('zombie_theater', { token });  // onStore says how far it is
//     if (!end || !end.done) return say(end && end.error || 'The download stopped.');   // player.store.stop() stops it
//   }
//   if (!(await player.lock.take())) return say('Your game is open in another tab.');
//   const begun = await player.join({ session: '/play/' + token, name: 'Ann', address: location.hostname + ':3075',
//     map: 'zombie_theater' });
//   if (!begun.started) say(begun.why);
//
// One player a page: the engine is the page's one (its script defines bo1Engine once, and finds the movie's canvas by
// its id), and the player keeps the last game's memory for the next. Make it once, and keep it.
//
// support()   whether this browser can run the game here: { ok, alone, lacks, why }. ok: it can play a room's game;
//             alone: it can play a map alone at least (no WebTransport); lacks: what it lacks first, of 'secure' (the
//             page is not served securely), 'isolation' (the page is not cross-origin isolated, which the engine's
//             threads need), 'storage' (no private file system for the game files), 'webgpu', 'webtransport'; why: that
//             in a sentence. Whether WebGPU finds a GPU, only a start says (its why).
// plain(text) the game's text without its colour codes (^1), as onEnd's and onError's may have them.
// LOCK        the name of the lock of the tab that plays (lock, below), which web/src/settings.ts looks for too.
//
// createPlayer(options) makes the player. Its options, all but the canvases optional:
//   container        an element the module makes its two canvases in, the movie's under the game's, each filling it
//                    (position: absolute; the container is positioned, sized and in the document by the page), and
//                    shows and hides as the game loads. Or:
//   canvas, movie    the page's own canvases: the game's (it takes the keyboard's focus: tabindex), and the loading
//                    movie's, under it, which must have the id "movie" (the engine finds it by that; with none, the
//                    engine's loading screen shows the map's still). The page then shows and hides them itself
//                    (onLoading, onMovie), as web/index.html does. The movie's canvas is another after each game
//                    (stop: it can go to a worker once), with the same id, classes and style.
//   settings         the player's settings, as web/src/settings.ts's bo1Settings gives them, or any object with some of:
//                    saved() (a promise: what the player changed is in their config, which a start waits for),
//                    backBuffer() (a promise of { width, height }, the size the game draws at), cinematicGain() (the
//                    loading movie's volume, 0 to 1), fullScreen() (whether a click into the game goes full screen),
//                    mobile (a phone or a tablet), refreshHz() (the display's, 0 when not measured), maxFpsDefault
//                    (the Frame Rate Limit of a config without one), recommended ({ dvar: value }, set after the
//                    game's own for a new player). Without one, or for what it lacks: the screen's size, the game's
//                    own volume, full screen, mobile as the browser says, no limit, nothing recommended.
//   args             more of the engine's command line, for every game: ['+set', 'cl_loadingMovie', '0'].
//   recommended      recommended settings more, over the settings' own.
//   module           more for the engine's Module, for debugging: bo1GpuFlags, bo1PipelineList, bo1CompileRate,
//                    bo1ShaderSalt, bo1DoneLate, bo1DoneRate (bo1-zombies/web/src/library_gpu.js), bo1Perf (what each frame takes,
//                    twice a second), print, printErr (the engine's console; the browser's without).
//   gpuTimings       true asks the GPU for its clock, for bo1Perf's GPU times.
//   dedicated        true runs a dedicated server with no window (testing).
//   log(text)        what the module itself has to say (a download's progress, the engine's memory taken again);
//                    console.log with "web player: " before it without.
// and its callbacks, each optional. What the engine says comes on the page's thread as it happens:
//   onStatus(text)   the engine's word on what it is doing (connecting, what it waits for), for a status line.
//   onState(state)   the player's state changed (state, below).
//   onLoading()      the engine's loading screen shows the map it loads: until then the game's canvas has nothing to
//                    show over the movie.
//   onMovie(what)    the map's loading movie: 'starts' (its canvas is to show, under the game's, which is to be see-
//                    through, and unseen until onLoading), 'shows' (its first picture is up), 'over' (the game's canvas
//                    alone again).
//   onOutro()        the game ended, and its outro rolls first (Call of the Dead's, 38 s), after which onEnd comes.
//   onGame(line)     a line the game says to its lobby: bo1_lobby: ..., of a game of the page's own (playAlone), and
//                    bo1_player: ... (the answers to the console's bo1_player).
//   onWaited(text)   the player waited for the GPU's pipelines before going in, and how that went.
//   onBuild(text)    the server runs another build of the game than this engine, and refused it (text says which); the
//                    game's end follows.
//   onEnd(why, text) the game ended. why is the engine's: 'over' (game over), 'left' (the player left), 'kicked',
//                    'dropped' (the connection was lost), or another word for a failure, and text its sentence. Of a
//                    game alone, 'over' has "Game over on round 12." for text. The engine may say it again as it quits
//                    after: the first is the one to act on. The page then stops the game (stop).
//   onError(text)    the engine failed: a fatal error, or a crash. It runs no more, and can't be stopped: the page says
//                    why, and reloads when the player has read it.
//   onStore(map, state)
//                    what the store has of the map changed (state: store.states[map], below; undefined once it is to
//                    be asked again), or of every map, for map ''.
//   onUsage(bytes, persisted)
//                    how much the store holds, as the module asks: as it starts, as a download ends, after remove.
//   onLockLost()     another tab took the lock (lock, below): the game is that tab's, and this one stops its own.
//
// The player:
//   join({ session, name, address, map, args, dev })
//                    plays a room's game on a server: session is the path of the WebTransport session that takes the
//                    player to their seat ('/play/<token>'), name the name the game knows them by, address the
//                    server's 'host:port' (the UDP port players reach it at), map the room's map (for its loading movie
//                    and its list of pipelines), args more command line for this game. dev: true leaves the engine's
//                    own colour where a pipeline still compiles (it blinks), which is black without. A promise of
//                    { started, why }: started once the engine is on its way, the callbacks saying the rest; else why
//                    it isn't, a sentence, '' when a game was started already or the page stopped this one meanwhile.
//   playAlone({ map, name, cheats, args, dev })
//                    plays a map alone, on no server: the engine runs the game itself, and ends at game over. cheats:
//                    true starts it with cheats on (devmap). The same promise.
//   stop()           ends the engine, its game over or not, and takes down all it put on the page (its threads,
//                    workers, sound, listeners, GPU device, the mouse and keyboard it held, full screen), so that the
//                    next game starts in the page as the first did. It keeps the engine's memory for the next, weakly,
//                    which takes it while the browser has yet to free it (a game started within 3 s of the stop waits
//                    that long first, for the last engine's threads to have ended). True once stopped. False when the
//                    engine can't be stopped, which only a reload of the page does: it was still starting, or it
//                    failed (onError).
//   command(text)    runs a console command in the game, as if typed into its console (it throws before the engine
//                    is up).
//   prefetch()       fetches the engine ahead (some 3.4 MB compressed), for the browser's cache to have it by the game.
//   started          a game was started, and not stopped since.
//   state            'idle', 'starting' (from join or playAlone), 'running' (the engine is up: it connects and loads),
//                    'ended' (onEnd came; stop is next), 'failed' (onError came).
//   module           the engine's Module of the game that runs, or of the next: for DevTools and tests
//                    (module.bo1GpuFlags, module.ccall).
//   gpu              the GPU as WebGPU names it, and the browser, once a game started: "nvidia ... , Chrome 141".
//   startedAt        when the last start began loading the engine (performance.now()).
//   canvas           the game's canvas.
//
//   store            the game files in the browser's store. The engine reads the install from it alone, so a map is
//                    downloaded into it, or imported from the player's own copy of the game, before its game.
//     start()        starts the store's worker, as the first use does: false when this browser has no store.
//     status(map)    a promise of what the store has of the map (a state, below), asking the server for the map's list;
//                    null without a store.
//     download(map, { token })
//                    downloads what the store lacks of the map from the server (token: the player's, sent as the
//                    Authorization's bearer, where the server asks for one), or imports it from the game folder where
//                    that has it (useFolder). One at a time: another map's stops. onStore says how far it is (the
//                    state's running, and progress). A promise of the state it ended with: done, or error (why, in
//                    words), off (the server sends no game files), none (not a map a browser plays), inFolder false
//                    (the folder hasn't it after all: the next downloads it), stopped; null when it didn't start (a
//                    game runs, or no store). One that stalls starts again by itself, from what it kept.
//     stop()         stops the download; what it kept stays, and the next goes on from it.
//     ensure(map, { token })
//                    a promise of true once the store has all of the map, after downloading what it lacks; false when
//                    it can't.
//     states         by map, what the store last said of it, for reading: { have, total (bytes), done, running (a
//                    download of it is), import (which imports), error, none, off, inFolder (the game folder has what
//                    the store lacks of it), differs (it has another version of it), stopped }. A map not there has yet
//                    to be asked for (know, status).
//     know(map)      asks what the store has of the map, unless that is known or asked already (onStore says); false
//                    without a store.
//     asking         how many of those questions wait for their answers.
//     downloading    the map being downloaded, '' for none.
//     progress(map)  { got, need }: the bytes of the running download so far, and all it is to get.
//     retry()        forgets the states that say a download failed or the server sends none, for them to be asked
//                    again.
//     usage()        a promise of { bytes, persisted }: how much the store holds, and whether the browser keeps it for
//                    good.
//     remove()       removes the game files from the store (not the player's settings); a promise, done once gone.
//     useFolder(folder)
//                    the player's game folder to import from (a directory handle, or { name, files } of a file
//                    input's; null for none), which the page may read. A promise of whether it holds an install.
//
//   lock             one tab of a player's plays at a time: the one that holds the lock (LOCK), until it releases it,
//                    closes, or another takes it. The page takes it before a game and releases it after (stop leaves
//                    it).
//     take(steal)    a promise of true once this tab holds it; false when another tab does (steal: true takes it from
//                    that tab, whose onLockLost says so).
//     release()
//     held           this tab holds it.
//     any()          a promise of whether any tab holds it, this one too.
//
// The server paths it relies on, from the root of the page's origin:
//   /bo1zombies.js, /bo1zombies.wasm   the engine (this module loads the script, which loads the rest)
//   /store.js                          the store's script: this module's worker, and the engine's file system backend
//   /maps/<map>/files                  GET: the list of the game files the map reads (web/public/store.js; 404: no such map)
//   /maps/<map>/pack                   POST: the pieces of them the store lacks (web/public/store.js; 403: none are sent)
//   /maps/<map>/pipelines              GET: the GPU pipelines the map draws with, compiled as it loads (the engine)
//   /game/manifest.txt                 GET: the game files' manifest, which the engine opens them by (web/public/store.js)
//   /certhash                          GET: the hash of the server's certificate for WebTransport, where it is
//                                      self-signed (the engine); and the session itself, at address and session
// and the page is to be served securely and cross-origin isolated (Cross-Origin-Opener-Policy: same-origin,
// Cross-Origin-Embedder-Policy: require-corp), with those paths' answers the same origin's.

export const LOCK = 'zombies-game';
const ENGINE = '/bo1zombies.js';
const STORE = '/store.js';
// How long after an engine stopped the next one waits before it takes its memory (start). The browser ends an engine's
// threads, a worker each, a moment after it is told to (terminate() returns before a worker is gone), and until then
// they run on: they write to the memory, and call the page's thread through it, where the next engine would take their
// calls for its own. On a PC busy with other work, a game started half a second after the last one stopped failed so,
// one a second after had a thread fail, and one two seconds after ran.
const THREADS_END = 3000;

// the game's text, without its colour codes (^1 ...)
export const plain = (text) => String(text || '').replace(/\^\d/g, '').trim();

const WHY = {
  secure: 'Browsers run the game only on a secure page: over HTTPS, or at localhost.',
  isolation: 'The game needs a cross-origin isolated page, for its threads: the server is to send it with ' +
    'Cross-Origin-Opener-Policy: same-origin and Cross-Origin-Embedder-Policy: require-corp.',
  storage: 'This browser has no private file system, which keeps the game files.',
  webgpu: 'This browser doesn\'t support WebGPU, which the game needs.',
  webtransport: 'This browser doesn\'t support WebTransport, which a game on the server needs.',
};

export function support() {
  const lacks = !self.isSecureContext ? 'secure'
    : !self.crossOriginIsolated ? 'isolation'
    : !(navigator.storage && navigator.storage.getDirectory) ? 'storage'
    : !navigator.gpu ? 'webgpu'
    : typeof WebTransport === 'undefined' ? 'webtransport' : '';
  return { ok: !lacks, alone: !lacks || lacks === 'webtransport', lacks, why: lacks ? WHY[lacks] : '' };
}

// Whether the page is on mobile, a phone or a tablet, as the browser says (web/src/settings.ts: mobile, which a page's
// settings say instead)
const onMobile = () => !!(navigator.userAgentData && navigator.userAgentData.mobile) ||
  /Android|iPhone|iPad|iPod/.test(navigator.userAgent);

// The browser's version, after the GPU's name: what Chrome holds a page's frames to on a phone, and what lifts that,
// went by its version.
function browserSays() {
  const brand = ((navigator.userAgentData && navigator.userAgentData.brands) || [])
    .find((b) => /Chrome|Chromium|Edge|Opera|Brave|Samsung/.test(b.brand) && !/Not/.test(b.brand));
  const m = /(?:Chrome|Firefox|Version)\/(\d+)/.exec(navigator.userAgent);
  return brand ? ', ' + brand.brand.replace(/^Google /, '') + ' ' + brand.version
    : m ? ', ' + m[0].replace('/', ' ') : '';
}

// the pages of the memory the last game wrote, zeroed, as a new memory has them (its data, its stacks, the flag that
// has a new engine set up its data); those it never wrote are left, so as not to make the browser hold them: MB zeroed
function clearUsed(memory) {
  const words = new Int32Array(memory.buffer);
  let pages = 0;
  for (let page = 0; page < words.length; page += 1024) {
    for (let i = page, end = page + 1024; i < end; i++) {
      if (words[i] !== 0) {
        words.fill(0, page, end);
        pages++;
        break;
      }
    }
  }
  return Math.round(pages / 256);
}

// a canvas of the module's own fills its container, its picture keeping its shape, the back buffer's, with black bars
// at its sides or above and below where the container's is another
const FILL = 'position: absolute; inset: 0; width: 100%; height: 100%; background: #000; object-fit: contain; ';

export function createPlayer(options) {
  const opts = options || {};
  const settings = opts.settings || {};
  const log = opts.log || ((text) => console.log('web player: ' + text));
  // a page that is not secure, or not isolated, runs no engine, and has no store
  const hostable = !!self.isSecureContext && !!self.crossOriginIsolated;

  // The canvases: the page's own, which it shows and hides itself; or the module's, in the page's container. The
  // movie's has the id the engine finds it by (bo1-zombies/web/src/library_bo1.js: bo1_movie_show).
  const own = !opts.canvas;
  let canvas = opts.canvas || null;
  let movie = opts.movie || null;
  if (own) {
    if (!opts.container) throw new Error('createPlayer: a container to make the canvases in, or a canvas');
    movie = document.createElement('canvas');
    movie.id = 'movie';
    movie.setAttribute('aria-hidden', 'true');
    movie.style.cssText = FILL + 'display: none; pointer-events: none;';
    canvas = document.createElement('canvas');
    canvas.tabIndex = 0;
    canvas.style.cssText = FILL + 'display: block; outline: none;';
    opts.container.append(movie, canvas);
  }

  // the engine command line: Zombies, straight into the map or onto the server
  const baseArgs = [
    '+set', 'bo1_zombies', '1',
    '+set', 'fs_b', '/game',
    '+set', 'fs_h', '/game',
    '+set', 'logfile', '0',
    '+set', 'net_ip', '127.0.0.1',
    '+set', 'r_fullscreen', '0',
    '+set', 'r_borderless', '1',
    '+set', 'vid_xpos', '0',
    '+set', 'vid_ypos', '0',
    '+set', 'com_introPlayed', '1',
    // the display paces the engine (bo1-zombies/web/src/d3d9_device.cpp: Pace); its own cap of 85 would leave a faster display
    // showing some frames twice
    '+set', 'com_maxfps', '0',
    // a packet to the server every frame up to 250 a second, which a display of 240 Hz has: it has the player's input
    // sooner (MP sends at most 30 a second). A packet carries the moves of the frames since the one before last, 32 at
    // most (bo1-zombies/src/client_mp/cl_input_mp.cpp), which 250 keeps to at 1000 frames a second
    '+set', 'cl_maxpackets', '250',
  ];
  if (opts.dedicated) baseArgs.push('+set', 'dedicated', '1');
  for (const a of opts.args || []) if (a) baseArgs.push(String(a));

  // A game was started, and not stopped since (start, stop), and where it is (state).
  let started = false;
  let state = 'idle';
  // The page runs a game's engine at a time, and stays as it ends (stop), as the next starts in it: engine is the last
  // one started ({ ready }: once it runs), runs counts the starts, and those stopped while they were under way (a start
  // goes no further once it isn't the last), and memory is the last one's memory, held weakly.
  let engine = null;
  let runs = 0;
  let memory = null;
  let stoppedAt = 0; // when the last engine stopped (stop), whose threads end a moment after (THREADS_END)
  let startedAt = 0; // when the last start began loading the engine
  // The canvas is black while the engine starts and joins its game (a few seconds), until its loading screen shows the
  // map it loads (status: loading). The map's loading movie plays from the engine's start (bo1-zombies/web/src/bink_web.cpp: the
  // map the page gives it, Module.bo1LoadingMap), its first picture half a second to a second and a half after, on its
  // own canvas under the game's (bo1-zombies/web/src/library_bo1.js: bo1_movie_show), which shows nothing over it until the
  // engine's loading screen does (loading), and from then on lets it through where that screen draws nothing. Once it
  // is over (ended, or the engine stopped it), the canvas is the game's alone.
  let loadingShows = false;
  // What the game of the page's own says to its lobby, which the page is (bo1-zombies/src/server_mp/sv_lobby_mp.cpp: bo1_lobby; the
  // engine hands the page those lines of its console, bo1-zombies/web/src/web_main.cpp: Sys_Print): game over, on what round (null
  // until then). Its game then ends as a room's game's players' does, saying it was game over (status: ended), after
  // Call of the Dead's outro; the text says on what round.
  let over = null;
  const overText = () => 'Game over' + (over.round ? ' on round ' + over.round : '') + '.';
  // The GPU as WebGPU names it, and that the game's textures are decompressed for it, where it takes none of their
  // block compression (gpuInit)
  let gpu = '';
  let module = null; // the engine's Module, of the game that runs or of the next

  const say = (name, ...args) => opts[name] && opts[name](...args);
  function setState(next) {
    if (state === next) return;
    state = next;
    say('onState', next);
  }

  function movieSaid(what) {
    if (own && what === 'starts') {
      movie.style.display = 'block';
      canvas.style.background = 'transparent';
      if (!loadingShows) canvas.style.opacity = '0';
    } else if (own && what === 'over') {
      movie.style.display = 'none';
      canvas.style.background = '#000';
      canvas.style.opacity = '';
    }
    say('onMovie', what);
  }
  function ended(why, text) {
    if (state !== 'failed') setState('ended');
    say('onEnd', why, text);
  }
  function failed(text) {
    setState('failed');
    say('onError', text);
  }

  // engine -> page (bo1-zombies/web/src/library_bo1.js: bo1_status)
  function status(what, text) {
    if (what === 'build') {
      // the server is another build of the game (text says which: bo1-zombies/src/client_mp/cl_main_mp.cpp, CL_RefuseBuild)
      say('onBuild', text);
    } else if (what === 'ended') {
      // the game ended, and why (bo1-zombies/src/client_mp/cl_main_mp.cpp: CL_GameEnded): "<why>\n<text>"
      const at = text.indexOf('\n');
      const why = at < 0 ? text : text.slice(0, at);
      ended(why, why === 'over' && over ? overText() : at < 0 ? '' : text.slice(at + 1));
    } else if (what === 'waited') {
      // the player waited seconds for pipelines before going in, and how that went (bo1-zombies/web/src/d3d9_webgpu.cpp: SayWaited)
      say('onWaited', text);
    } else if (what === 'loading') {
      // the engine's loading screen shows the map it loads (bo1-zombies/src/client/cl_cin.cpp: CL_LoadingMovieStart)
      loadingShows = true;
      if (own) canvas.style.opacity = '';
      say('onLoading');
    } else if (what === 'movie') {
      movieSaid(text);
    } else if (what === 'outro') {
      // the game ended, and Call of the Dead's outro rolls first (38 s), after which the engine says why (ended)
      say('onOutro');
    } else if (what === 'game') {
      const said = /^bo1_lobby: over( (\d+))?$/.exec(text);
      if (said) over = { round: said[2] ? +said[2] : 0 };
      say('onGame', text);
    } else if (what === 'error') {
      failed(text);
    } else if (what === 'exit') {
      // the engine quit: at game over, which the game of the page's own said (over), or the player left (the console's
      // quit)
      if (over) ended('over', overText());
      else ended('left', 'You quit the game.');
    } else {
      say('onStatus', text);
    }
  }

  // The engine's Module, as each game's starts out (bo1Engine: bo1-zombies/web/CMakeLists.txt), which the engine fills in as it
  // runs: each game has a copy (stop).
  const config = Object.assign({
    canvas,
    arguments: baseArgs,
    // renderer debugging, flags added up (bo1-zombies/web/src/d3d9_webgpu.cpp); DevTools can change it as the game runs
    bo1GpuFlags: 0,
    // on mobile, a phone or a tablet, where the page goes three frames ahead of the GPU whatever its queue says
    // (bo1-zombies/web/src/d3d9_webgpu.cpp: AheadMost)
    bo1Mobile: settings.mobile !== undefined ? !!settings.mobile : onMobile(),
    // the display's refresh in hertz, as the page measured it (web/src/settings.ts: refreshHz; 0 not measured), which the
    // engine's display mode has, and its Graphics menu's Frame Rate Limit goes by until the game measures its own as it
    // plays (bo1-zombies/web/src/web_window.cpp: Bo1_DisplayHz)
    bo1RefreshHz: () => (settings.refreshHz ? settings.refreshHz() : 0),
    // the Frame Rate Limit's default, 90 on mobile and none on a PC (web/src/settings.ts: DEFAULTS), which a config
    // without the setting has (bo1-zombies/web/src/web_main.cpp: Bo1_DefaultMaxFps)
    bo1MaxFpsDefault: settings.maxFpsDefault !== undefined ? Number(settings.maxFpsDefault) : 0,
    // mobile's recommended settings, {dvar: value}, which the engine sets after the game's own (web/src/settings.ts:
    // RECOMMENDED; bo1-zombies/web/src/web_main.cpp: Sys_ArchiveInfo); none on a PC
    bo1Recommended: Object.assign({}, settings.recommended, opts.recommended),
    print: (t) => console.log(t),
    printErr: (t) => console.warn(t),
  }, opts.module, {
    bo1Status: status,
    onAbort: (what) => failed('The game crashed: ' + what),
    // runs a console command once the engine is up, as if typed into the console (bo1-zombies/web/src/web_main.cpp); what it
    // prints goes to the browser's console. For the game tests (e2e/game/player.ts) and DevTools.
    bo1Command: (text) => module.ccall('Bo1_ConsoleCommand', null, ['string'], [String(text)]),
  });
  module = Object.assign({}, config);

  // A game's device, destroyed as its engine stopped (library_gpu.js: bo1GpuStop), and collected: the browser's WebGPU
  // client queues what frees what was made on it in the GPU process (its pipelines, a few hundred MB there in
  // software), and sends it with its next call, which a page with no game makes none of. One call sends it, a moment
  // after, as the browser may finish collecting the rest after the device.
  const deviceGone = typeof FinalizationRegistry === 'undefined' ? null : new FinalizationRegistry(() => {
    setTimeout(() => {
      if (started) return; // the next game's calls send it
      log('the last game\'s GPU device is gone');
      navigator.gpu.requestAdapter().catch(() => {});
    }, 1000);
  });

  // WebGPU's device, for the engine to draw with (bo1-zombies/web/src/d3d9_webgpu.cpp), on the game's Module. Without one the game
  // can't start: why, '' with one.
  async function gpuInit(game) {
    if (opts.dedicated) return '';
    if (!navigator.gpu) return WHY.webgpu;
    try {
      const adapter = await navigator.gpu.requestAdapter({ powerPreference: 'high-performance' });
      if (!adapter) return 'WebGPU found no GPU.';
      // timestamp-query: the GPU's clock, for bo1Perf's line of what a frame takes it (d3d9_webgpu.cpp: the GPU's time)
      const requiredFeatures = ['texture-compression-bc', 'float32-filterable', 'float32-blendable',
        ...(opts.gpuTimings ? ['timestamp-query'] : [])].filter((f) => adapter.features.has(f));
      const requiredLimits = {};
      for (const l of ['maxColorAttachmentBytesPerSample', 'maxInterStageShaderVariables'])
        requiredLimits[l] = adapter.limits[l];
      const device = await adapter.requestDevice({ requiredFeatures, requiredLimits });
      game.bo1GpuDevice = device;
      if (deviceGone) deviceGone.register(device, 0);
      const info = adapter.info || {};
      const name = ([info.vendor, info.architecture, info.description].filter(Boolean).join(' ') || 'an adapter') +
        (info.isFallbackAdapter ? ' (software)' : '');
      console.log('[bo1] WebGPU: ' + name);
      // d3d9_webgpu.cpp: MakeTexture decompresses them then, to 4 to 8 times their size
      gpu = name + browserSays() +
        (device.features.has('texture-compression-bc') ? '' : '\ntextures decompressed (no BC)');
      return '';
    } catch (e) {
      return 'WebGPU failed: ' + e.message;
    }
  }

  // the engine's script, once a page: it defines bo1Engine. False when the server has none.
  let script = null;
  function loadEngine() {
    if (typeof globalThis.bo1Engine !== 'undefined') return Promise.resolve(true);
    if (!script) {
      script = new Promise((resolve) => {
        const s = document.createElement('script');
        s.src = ENGINE;
        s.onload = () => resolve(true);
        s.onerror = () => {
          s.remove();
          script = null; // the next start asks again
          resolve(false);
        };
        document.body.appendChild(s);
      });
    }
    return script;
  }

  // The engine, fetched ahead, as a player waits for their game: the browser has it in its cache by the time the game
  // starts (the server sends it compressed, about 3.4 MB; once cached, the request asks only whether it changed).
  let prefetched = false;
  function prefetch() {
    if (prefetched || started || !hostable || !navigator.gpu) return;
    prefetched = true;
    for (const file of [ENGINE, ENGINE.replace(/\.js$/, '.wasm')])
      fetch(file, { priority: 'low' }).then((rsp) => rsp.arrayBuffer()).catch(() => {});
  }

  // one argument of the command line, whatever the name: no quote in it ends it early
  const quoted = (name) => '"' + String(name).replace(/["\x00-\x1f]/g, '') + '"';

  // The engine: it plays the room's game (what.address: the server that has it, what.session: the player's seat), or a
  // map alone (what.map; cheats: devmap). { started, why }: why it didn't start, '' when the page stopped it meanwhile.
  async function start(what) {
    if (started) return { started: false, why: '' };
    if (!hostable) return { started: false, why: support().why };
    started = true;
    setState('starting');
    const run = ++runs;
    const off = { started: false, why: '' }; // the page stopped it meanwhile (stop), or started another
    const cant = (why) => {
      started = false;
      setState('idle');
      return { started: false, why };
    };
    // the player's command line, and what this game adds to it
    const args = baseArgs.slice();
    // what the player changed in the settings is in their config before the engine reads it; and the back buffer, the
    // canvas, is the size they picked there, the screen's at first (web/src/settings.ts: backBuffer), which is the engine's
    // for the game: in a window, or at another shape than the screen's, the picture keeps its own (object-fit)
    let size = { width: Math.max(screen.width, screen.height), height: Math.min(screen.width, screen.height) };
    if (settings.saved) await settings.saved();
    if (settings.backBuffer) size = await settings.backBuffer();
    if (run !== runs) return off;
    const game = module;
    canvas.width = size.width;
    canvas.height = size.height;
    store.stop();
    startedAt = performance.now();
    over = null;
    loadingShows = false;
    if (!opts.dedicated) {
      // the map, whose loading movie the engine plays as it starts (Module.bo1LoadingMap: bo1-zombies/web/src/web_main.cpp), at the
      // player's cinematics and master volumes, as the engine has yet to read their config then (Module.bo1MovieVolume)
      game.bo1LoadingMap = what.map || '';
      if (settings.cinematicGain) game.bo1MovieVolume = settings.cinematicGain();
    }
    // the player's Full Screen setting, as their config has it: whether the click that takes them into the game goes
    // full screen (bo1-zombies/web/src/library_bo1.js: fullScreenOn), which the engine has yet to read then too
    if (settings.fullScreen) game.bo1FullScreen = settings.fullScreen();
    // WebGPU leaves out what a pipeline still compiling would draw, where its stand-in isn't ready
    // (bo1-zombies/web/src/d3d9_webgpu.cpp), and the scene shows its clear colour there: black, except for a page that asks (dev),
    // where the engine's own blinks orange and blue to show it
    if (!what.dev) args.push('+set', 'r_clear', '3', '+set', 'r_clearColor', '0 0 0 1');
    for (const a of what.args || []) if (a) args.push(String(a));
    if (what.address) {
      // the session's path takes the player to their seat in their room's game (bo1-zombies/web/src/library_bo1.js: bo1_net_start)
      game.bo1SessionPath = what.session || '/';
      args.push('+set', 'name', quoted(what.name || 'Player'), '+connect', String(what.address));
    } else {
      // a game of the page's own: as a room's (bo1_lobby, bo1-zombies/src/server_mp/sv_lobby_mp.cpp), it ends at game over rather
      // than start the map again, saying so, and kicks no one for standing idle
      if (what.name) args.push('+set', 'name', quoted(what.name));
      args.push('+set', 'bo1_lobby', '1', what.cheats ? '+devmap' : '+map', String(what.map));
    }
    const why = await gpuInit(game);
    if (run !== runs) return off;
    if (why) return cant(why);
    if (!(await loadEngine())) {
      return run !== runs ? off : cant('bo1zombies.js is missing: build the web target first (web/build.ps1).');
    }
    if (run !== runs) return off;
    game.arguments = args;
    // the last game's memory, while the browser has yet to free it, which the player holds weakly (stop): cleared, the
    // engine takes it, rather than a new one beside it, which the two of them might be more than the browser gives a
    // page; else the engine makes a new one. Not before the last engine's threads have ended (THREADS_END): a game
    // started right after the last one stopped waits for that.
    if (memory && memory.deref()) {
      const wait = stoppedAt + THREADS_END - performance.now();
      if (wait > 0) {
        await new Promise((resolve) => setTimeout(resolve, wait));
        if (run !== runs) return off;
      }
    }
    const last = memory && memory.deref();
    if (last) {
      log('the engine takes the last game\'s memory, ' + clearUsed(last) + ' MB of it cleared');
      game.wasmMemory = last;
    }
    engine = { ready: false };
    const starting = engine;
    globalThis.bo1Engine(game).then(() => {
      starting.ready = true;
      if (engine === starting && state === 'starting') setState('running');
    }, (e) => log('the engine did not start: ' + e));
    canvas.focus();
    return { started: true, why: '' };
  }

  function join(what) {
    const w = what || {};
    if (!w.address) return Promise.resolve({ started: false, why: 'No server to join.' });
    return start({ address: w.address, session: w.session, name: w.name, map: w.map, args: w.args, dev: w.dev });
  }
  function playAlone(what) {
    const w = what || {};
    if (!w.map) return Promise.resolve({ started: false, why: 'No map to play.' });
    return start({ map: w.map, name: w.name, cheats: w.cheats, args: w.args, dev: w.dev });
  }

  // The engine ends (library_bo1.js: bo1Stop), its game over, and the page stays, as it was before the game: false
  // while it is still starting, which only a reload stops, or once it failed. A start still under way (start) goes no
  // further.
  function stop() {
    runs++;
    const was = engine;
    engine = null;
    if (was && !was.ready || state === 'failed') return false;
    try {
      if (was) {
        memory = new WeakRef(module.bo1Stop());
        stoppedAt = performance.now();
      }
    } catch (e) {
      log('the engine did not stop: ' + e);
      return false;
    }
    module = Object.assign({}, config);
    // the movie's canvas went to a worker (library_bo1.js: bo1_movie_show), as it does once: the next game's is another
    if (movie) {
      const next = movie.cloneNode(false);
      if (own) next.style.display = 'none';
      movie.replaceWith(next);
      movie = next;
    }
    if (own) {
      canvas.style.background = '#000';
      canvas.style.opacity = '';
    }
    canvas.style.cursor = ''; // the engine's, over its menus (library_bo1.js)
    if (document.fullscreenElement) document.exitFullscreen().catch(() => {});
    started = false;
    over = null;
    loadingShows = false;
    setState('idle');
    return true;
  }

  // ---- the game files kept in the browser (web/public/store.js) ----
  // The engine reads the install from the browser's store alone, so a map is downloaded into it before its game. The
  // module runs the store's script as a worker, which says what the store has of a map (from the server's list of what
  // the map reads, server/files.go), downloads the rest, and says what the store holds. A download stops as another
  // map's starts, or the page stops it; what it kept stays, and the next goes on from it. Or it imports the map from
  // the player's own install, where the page gave their game folder (useFolder: web/public/store.js, importing) and the folder
  // has all the store lacks of the map: nothing comes from the server then.
  const store = (() => {
    let worker = null;
    let downloading = ''; // the map the worker downloads now
    let token = ''; // the player's, of that download
    // by map: what the worker last said of it: { have, total, done, running, error, none, off, import (it imports) }
    const states = {};
    // by map: what the store had of it as its download began, which the download counts from: it needs the rest (with
    // mods to come, what the room's game needs of their files too, so that the map's own total means nothing to the
    // player)
    const base = {};
    const asked = new Set(); // the maps whose state the worker was asked for, and hasn't said yet
    let changes = 0; // the downloads that ended, or were stopped (storeChanged), and the store's removals
    let logged = 0; // when the log last had the progress
    let retries = 0; // the downloads started again after a stall (web/public/store.js: its watchdog), in a row
    // the player's game folder, as the page gave it (none: null): a directory handle, or a file input's { name, files }
    let folder = null;
    let folderGen = 0; // the folders given to the worker: its words about one before are of no account
    const folderReplies = new Map(); // by gen: what the worker says of the folder (useFolder)
    const knowers = new Map(); // by map: who waits for its state (status)
    let enders = []; // who waits for the running download's end (download)
    let usages = []; // who waits to hear how much the store holds (usage)
    let removals = []; // who waits for the store to be removed (remove)
    const mb = (n) => Math.round(n / 1e6);

    // those who wait for a map's state have it, once the store said it; a state that went meanwhile is asked again
    function settle() {
      for (const [map, list] of knowers) {
        if (states[map]) {
          knowers.delete(map);
          for (const resolve of list) resolve(states[map]);
        } else know(map);
      }
    }
    // the map's state changed ('': every map's): the page hears
    function changed(map) {
      settle();
      say('onStore', map, map ? states[map] : undefined);
    }
    // the running download ended, with the state
    function end(said) {
      const list = enders;
      enders = [];
      for (const resolve of list) resolve(said);
    }

    function start() {
      if (worker || !hostable || !navigator.storage || !navigator.storage.getDirectory) return !!worker;
      worker = new Worker(STORE, { name: 'zombies-store' });
      worker.postMessage({ op: 'folder', folder, gen: folderGen });
      worker.onmessage = (e) => heard(e.data);
      worker.onerror = (e) => log('the store\'s worker failed: ' + e.message);
      worker.postMessage({ op: 'usage' });
      return true;
    }
    function heard(m) {
      if (m.op === 'folder') {
        const said = folderReplies.get(m.gen);
        folderReplies.delete(m.gen);
        if (said) said(m.install);
      } else if (m.op === 'progress') {
        // of a folder before (useFolder): the worker is asked again
        if (m.gen !== folderGen) return;
        // what a map's state says, but while the worker downloads it, when that is the download's own word
        if (m.map === downloading && !m.download) return;
        asked.delete(m.map);
        // a state the worker was asked for before the store last changed: it is asked again (know)
        if (m.asked !== undefined && m.asked !== changes) return changed(m.map);
        states[m.map] = m;
        if (m.map === downloading && !m.running || m.stopped) storeChanged(m.map);
        let ends = false;
        if (m.map === downloading && !m.running) {
          downloading = '';
          if (m.done) {
            retries = 0;
            if (m.import) log('imported ' + m.map + ' from the game folder (' + mb(m.imported) + ' MB)');
            else log('downloaded ' + m.map + ' (' + mb(m.total) + ' MB)');
          } else if (m.noInstall) {
            // the folder is no longer one to import from: the page has the player choose another
            log((folder && folder.name) + ' holds no Black Ops install now');
          } else if (m.inFolder === false) {
            log((folder && folder.name) + (m.differs ? ' has another version of ' : ' hasn\'t ') + m.map +
              ': the next downloads it');
          } else if (m.error && /^stalled/.test(m.error) && retries < 5) {
            // the worker is stuck in the browser's file system: a new one goes on from what the store has
            retries++;
            log((m.import ? 'importing ' : 'downloading ') + m.map + ' ' + m.error + ': starting it again (' +
              retries + ')');
            worker.terminate();
            worker = null;
            asked.clear(); // what the worker was asked, a new one is
            if (begin(m.map, true)) return;
          } else if (m.error) {
            log((m.import ? 'importing ' : 'downloading ') + m.map + ' failed: ' + m.error);
          } else if (m.off) {
            log('the server sends no game files');
          }
          if (worker) worker.postMessage({ op: 'usage' });
          ends = true;
        } else if (m.running && Date.now() - logged > 10000) {
          logged = Date.now();
          log((m.import ? 'importing ' : 'downloading ') + m.map + ': ' + mb(m.have) + ' of ' + mb(m.total) + ' MB');
        }
        changed(m.map);
        if (ends) end(m);
      } else if (m.op === 'usage') {
        const list = usages;
        usages = [];
        for (const resolve of list) resolve({ bytes: m.bytes, persisted: !!m.persisted });
        say('onUsage', m.bytes, !!m.persisted);
      } else if (m.op === 'removed') {
        for (const map of Object.keys(states)) delete states[map];
        asked.clear();
        changes++;
        worker.postMessage({ op: 'usage' });
        changed('');
        const list = removals;
        removals = [];
        for (const resolve of list) resolve(true);
      }
    }
    // The worker says what the store has of the map, unless the module knows already.
    function know(map) {
      if (!start()) return false;
      if (map && !states[map] && !asked.has(map)) {
        asked.add(map);
        worker.postMessage({ op: 'status', map, asked: changes });
      }
      return true;
    }
    function status(map) {
      if (states[map]) return Promise.resolve(states[map]);
      if (!map || !know(map)) return Promise.resolve(null);
      return new Promise((resolve) => {
        if (!knowers.has(map)) knowers.set(map, []);
        knowers.get(map).push(resolve);
      });
    }
    // A download of the map ended, or was stopped: the store has more of each map that shares its pieces, so the worker
    // says the others' states again, and what it says of a question of before, which may not have the pieces, is asked
    // again.
    function storeChanged(map) {
      changes++;
      for (const [other, s] of Object.entries(states)) if (other !== map && !s.running) delete states[other];
    }
    // resume: it goes on after a stall, counting from where it began
    function begin(map, resume) {
      if (started || !start()) return false;
      if (downloading && downloading !== map) stop();
      if (!resume || base[map] === undefined) base[map] = (states[map] && states[map].have) || 0;
      downloading = map;
      const imports = !!folder && (!states[map] || states[map].inFolder !== false);
      states[map] = Object.assign({}, states[map], { running: true, error: '', import: imports });
      log(imports ? 'importing ' + map + ' from ' + folder.name : 'downloading ' + map);
      // kept for good, if the browser grants it (an installed page is granted it; else it goes by how much the page is
      // used), rather than dropped when the browser needs the room
      if (navigator.storage.persist) navigator.storage.persist().catch(() => {});
      worker.postMessage({ op: 'download', map, token, import: imports });
      changed(map);
      return true;
    }
    function download(map, how) {
      if (!map) return Promise.resolve(null);
      const was = token;
      token = (how && how.token) || '';
      if (!begin(map, false)) {
        token = was;
        return Promise.resolve(null);
      }
      return new Promise((resolve) => enders.push(resolve));
    }
    function stop() {
      if (!downloading) return;
      log('stopped downloading ' + downloading);
      worker.postMessage({ op: 'stop' });
      const map = downloading;
      delete states[map]; // the worker says what it kept
      asked.add(map);
      downloading = '';
      end({ map, stopped: true });
    }
    async function ensure(map, how) {
      let s = await status(map);
      // a download of it under way: its end
      if (s && s.running && downloading === map) s = await new Promise((resolve) => enders.push(resolve));
      if (!s || s.none || s.off) return false;
      if (s.done) return true;
      s = await download(map, how);
      return !!(s && s.done);
    }
    function progress(map) {
      const s = states[map] || { have: 0, total: 0 };
      const from = Math.min(base[map] || 0, s.have);
      return { got: s.have - from, need: s.total - from };
    }
    function retry() {
      for (const [m, s] of Object.entries(states)) if (s.error || s.off) delete states[m];
      changed('');
    }
    function usage() {
      if (!worker) return Promise.resolve({ bytes: 0, persisted: false });
      worker.postMessage({ op: 'usage' });
      return new Promise((resolve) => usages.push(resolve));
    }
    function remove() {
      if (!start()) return Promise.resolve(false);
      // the download under way first, which would keep what it fetches after
      stop();
      worker.postMessage({ op: 'remove' });
      return new Promise((resolve) => removals.push(resolve));
    }
    // The folder the module imports from (none: null), given to the store's worker, which says whether it holds an
    // install: true when it does. The maps' states are asked again, for whether the folder has each; an import from the
    // folder before stops.
    function useFolder(f) {
      if (downloading && states[downloading] && states[downloading].import) stop();
      folder = f || null;
      const gen = ++folderGen;
      for (const [m, s] of Object.entries(states)) if (!s.running) delete states[m];
      asked.clear();
      let said = Promise.resolve(false);
      if (start()) {
        said = new Promise((resolve) => folderReplies.set(gen, resolve));
        worker.postMessage({ op: 'folder', folder, gen });
      }
      changed('');
      return said;
    }

    return {
      start, status, download, stop, ensure, know, progress, retry, usage, remove, useFolder, states,
      get asking() { return asked.size; },
      get downloading() { return downloading; },
    };
  })();

  // ---- the lock ----
  // The player's tab that plays holds this lock, until it closes or reloads, releases it, or another tab takes it
  // (take: steal), which this one hears (onLockLost).
  const lock = (() => {
    let held = false; // this tab holds it
    let release = null;
    return {
      get held() { return held; },
      // the lock, taken from another tab too when steal; true once this tab holds it, false when another tab does
      take(steal) {
        // (no locks: not a secure page, which can't play anyway)
        if (held || !navigator.locks) return Promise.resolve(true);
        return new Promise((resolve) => {
          navigator.locks.request(LOCK, steal ? { steal: true } : { ifAvailable: true }, (l) => {
            if (!l) return resolve(false);
            held = true;
            resolve(true);
            return new Promise((done) => { release = done; });
          }).catch(() => {
            // another tab took it: the game is that tab's
            held = false;
            release = null;
            say('onLockLost');
          });
        });
      },
      release() {
        if (release) release();
        held = false;
        release = null;
      },
      // whether any tab holds it, this one too
      any() {
        if (!navigator.locks) return Promise.resolve(false);
        return navigator.locks.query().then((locks) => locks.held.some((l) => l.name === LOCK));
      },
    };
  })();

  return {
    join, playAlone, stop, prefetch, store, lock,
    command: (text) => module.bo1Command(text),
    get started() { return started; },
    get state() { return state; },
    get module() { return module; },
    get gpu() { return gpu; },
    get startedAt() { return startedAt; },
    get canvas() { return canvas; },
  };
}
