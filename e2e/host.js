// e2e/host.js - a stand-in for the engine's host (web/public/player.js), which the page tests serve at /player.js in
// its place. It has the host's interface (web/src/engine.ts) and runs no engine. It joins a room's game the way the
// engine does, over a WebTransport session to the server's relay, where the stand-in game (e2e/standin) is at the
// other end:
//
//   getchallenge  ->  challengeResponse 1 <build>   a client of another build stops here (onBuild, then onEnd)
//   connect       ->  connectResponse                 the game has the player in, and the page hears onLoading
//                 <-  standin over <n>                game over on round n: the page hears onEnd('over', ...)
//                 <-  standin quit                    the game quit: onEnd('dropped', ...), as for EXE_SERVERQUIT
//   disconnect    ->                                  the player left the game (standin.leave)
//
// A page that stops the engine (stop) only closes the session, as the engine says nothing to the game then.
//
// The client's build is the one the page's &args= sets (+set bo1_build other), or else the game's.
//
// Its store has all of every map, unless globalThis.standinMaps, which a test sets before the page loads, says what
// it has of a map ({ zombie_theater: { have: 1, total: 2 } }). A game folder that the page gives it holds an install
// where it has a folder main, in it or in a folder of it, and none of the maps the store lacks are in it. Its lock is
// the real host's (the Web Locks API), so tabs share it as they do with the engine.
//
// A test plays the player's part in the game through globalThis.standin in the page: standin.leave() leaves the room's
// game as the game's menu does, and standin.over(round) leaves game over's end scoreboard, or ends a map played alone
// at game over.

export const LOCK = 'zombies-game';

export const plain = (text) => String(text || '').replace(/\^\d/g, '').trim();

export function support() {
  return { ok: true, alone: true, lacks: '', why: '' };
}

const OOB = '\xff\xff\xff\xff';

// A packet as a WebTransport datagram of the relay's framing (server/framing.go): a whole packet. The text is bytes,
// one a character.
const frame = (text) => Uint8Array.from([0, ...Array.from(text, (c) => c.charCodeAt(0))]);
const unframe = (datagram) => (datagram[0] === 0 ? String.fromCharCode(...datagram.subarray(1)) : '');

export function createPlayer(options) {
  const say = (name, ...args) => options[name] && options[name](...args);
  const log = (text) => (options.log ? options.log(text) : console.log('stand-in host: ' + text));
  const args = options.args || [];
  const at = args.indexOf('bo1_build');
  const build = at > 0 && args[at - 1] === '+set' ? args[at + 1] : '';
  let state = 'idle';
  let startedAt = 0;
  let game = null; // the room's game: { transport, writer }, or null
  const module = {
    bo1GpuFlags: 0,
    bo1Command: (text) => log('command: ' + text),
  };

  // Ends the game the way the engine does: the page hears why, once.
  function end(why, text) {
    if (state !== 'running') return;
    state = 'ended';
    say('onEnd', why, text);
  }

  async function connect(session, name, address) {
    const hash = await (await fetch('/certhash')).text();
    const value = Uint8Array.from(hash.match(/../g), (h) => parseInt(h, 16));
    const transport = new WebTransport('https://' + address + session,
      { serverCertificateHashes: [{ algorithm: 'sha-256', value }] });
    const writer = transport.datagrams.writable.getWriter();
    game = { transport, writer };
    await transport.ready;
    transport.closed.then(() => end('dropped', 'The server closed the connection.'),
      (e) => end('dropped', 'The connection failed: ' + e.message));
    say('onStatus', 'Connecting...');
    // The game may not be up yet: the client asks again until it answers, as the engine's does.
    let joined = false;
    const ask = setInterval(() => !joined && writer.write(frame(OOB + 'getchallenge')).catch(() => {}), 1000);
    writer.write(frame(OOB + 'getchallenge'));
    const reader = transport.datagrams.readable.getReader();
    for (;;) {
      const { value: datagram, done } = await reader.read();
      if (done) break;
      const text = unframe(datagram);
      const challenge = text.match(/^\xff{4}challengeResponse \d+ (\S+)$/);
      if (challenge && build && build !== challenge[1]) {
        // as the engine refuses a server of another build (bo1-zombies/src/client_mp/cl_main_mp.cpp: CL_RefuseBuild)
        clearInterval(ask);
        const refusal = `The server runs build ${challenge[1]} of the game, and this client build ${build}.`;
        say('onBuild', refusal);
        end('error', refusal + " Join it with the server's build.");
        break;
      } else if (challenge) {
        writer.write(frame(OOB + 'connect "\\protocol\\1\\name\\' + name + '"'));
      } else if (text === OOB + 'connectResponse' && !joined) {
        joined = true;
        clearInterval(ask);
        say('onLoading');
        say('onStatus', 'In the game');
      } else if (text === OOB + 'standin quit') {
        end('dropped', 'Server quit');
      } else if (text.startsWith(OOB + 'standin over ')) {
        end('over', 'Game over on round ' + text.slice(OOB.length + 'standin over '.length) + '.');
      }
    }
    clearInterval(ask);
  }

  function begin(map) {
    if (state !== 'idle') return false;
    state = 'running';
    startedAt = performance.now();
    module.bo1LoadingMap = map;
    return true;
  }

  // Closes the session. With quit, the player quit the game, and the game hears so first.
  function close(quit) {
    if (!game) return;
    if (quit) game.writer.write(frame('disconnect')).catch(() => {});
    const { transport } = game;
    setTimeout(() => transport.close(), 100); // the disconnect goes first
    game = null;
  }

  globalThis.standin = {
    leave() {
      close(true);
      end('left', 'You left the game.');
    },
    over(round) {
      // a room's game: as the engine leaves its end scoreboard
      // (bo1-zombies/src/client_mp/cl_main_mp.cpp: CL_LeaveGameOver)
      if (game) {
        close(true);
        end('over', 'You left the end scoreboard.');
      } else end('over', 'Game over on round ' + round + '.');
    },
  };

  return {
    async join({ session, name, address, map }) {
      if (!begin(map)) return { started: false, why: '' };
      log('joining ' + address + session + ' as ' + name);
      connect(session, name, address).catch((e) => end('dropped', 'The connection failed: ' + e.message));
      return { started: true };
    },
    async playAlone({ map }) {
      if (!begin(map)) return { started: false, why: '' };
      log('playing ' + map + ' alone');
      setTimeout(() => say('onLoading'), 0);
      return { started: true };
    },
    stop() {
      close(false);
      state = 'idle';
      return true;
    },
    prefetch() {},
    command: (text) => module.bo1Command(text),
    get started() { return state !== 'idle'; },
    get state() { return state; },
    get module() { return module; },
    get gpu() { return 'stand-in'; },
    get startedAt() { return startedAt; },
    get canvas() { return options.canvas; },
    store: makeStore(say),
    lock: makeLock(say),
  };
}

// Whether a game folder (a directory handle) holds an install: a folder main, in it or in one of its folders.
async function holdsInstall(folder) {
  if (!folder || !('entries' in folder)) return false;
  for await (const [name, entry] of folder.entries()) {
    if (entry.kind !== 'directory') continue;
    if (name.toLowerCase() === 'main') return true;
    for await (const [inner, child] of entry.entries()) {
      if (child.kind === 'directory' && inner.toLowerCase() === 'main') return true;
    }
  }
  return false;
}

// The store has all of every map, but for what globalThis.standinMaps says.
function makeStore(say) {
  const states = {};
  const given = globalThis.standinMaps || {};
  let install = false; // the game folder holds an install, which lacks every map the store lacks
  const know = (map) => {
    if (map && !states[map]) {
      states[map] = given[map] ? { ...given[map], ...(install && { inFolder: false }) }
        : { have: 1, total: 1, done: true };
      setTimeout(() => say('onStore', map, states[map]), 0);
    }
    return true;
  };
  return {
    states,
    start: () => true,
    know,
    status: (map) => Promise.resolve(know(map) && states[map]),
    download: (map) => Promise.resolve(know(map) && states[map]),
    ensure: (map) => Promise.resolve(know(map) && !!states[map].done),
    stop() {},
    get asking() { return 0; },
    get downloading() { return ''; },
    progress: (map) => {
      const s = states[map] || { have: 0, total: 0 };
      return { got: s.have || 0, need: s.total || 0 };
    },
    retry() {},
    usage() {
      setTimeout(() => say('onUsage', 0, false), 0);
      return Promise.resolve({ bytes: 0, persisted: false });
    },
    remove: () => Promise.resolve(true),
    // the maps' states are asked again, for whether the folder has each
    async useFolder(folder) {
      install = await holdsInstall(folder);
      for (const map of Object.keys(states)) if (!states[map].done) delete states[map];
      setTimeout(() => say('onStore', '', undefined), 0);
      return install;
    },
  };
}

// The real host's lock (web/public/player.js).
function makeLock(say) {
  let held = false;
  let release = null;
  return {
    get held() { return held; },
    take(steal) {
      if (held) return Promise.resolve(true);
      return new Promise((resolve) => {
        navigator.locks.request(LOCK, steal ? { steal: true } : { ifAvailable: true }, (l) => {
          if (!l) return resolve(false);
          held = true;
          resolve(true);
          return new Promise((done) => { release = done; });
        }).catch(() => {
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
    any() {
      return navigator.locks.query().then((locks) => locks.held.some((l) => l.name === LOCK));
    },
  };
}
