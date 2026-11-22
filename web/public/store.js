// web/public/store.js - the game files kept in the browser: the store the web client reads the install from, all of it, and
// that a map's download fills (web/src/files.ts: mapButton). It is the browser's private file system (OPFS), kept
// across visits (and for good once the page has persistent storage: navigator.storage.persist).
//
// Layout:
//   packs/<name>.pack            pieces of the game files, one after another, as a download (or an import) kept them:
//                                each a piece of the file whose contents have a hash (server/hashes.go), its bytes start
//                                to end, of those a map's list names (server/files.go). Then the pack's index, JSON:
//                                { "pack": 1, "pieces": { <hash>: [[start, end, at], ...] } }, at: where in the pack the
//                                piece is; then the index's length, 4 bytes, little-endian. A piece is the same whichever
//                                map's list names it, so the store keeps it once, and a second map downloads only what
//                                the first lacked, into packs of its own. A file changed on the server is another hash,
//                                of which the store has nothing.
//   lists/<map>.json             each map's list, as the page last had it from the server: what a map needs, offline too
//   manifest.txt                 the game files' manifest, as last had: their version, each file's path, size and hash,
//                                which the engine opens them by
//   players/                     the player's own folder, the engine's /home/players (bo1-zombies/web/src/web_main.cpp): their
//                                config.cfg, which keeps their settings from one visit to the next. The engine reads
//                                and writes it, through Emscripten's OPFS backend (bo1-zombies/web/src/library_bo1.js), and this
//                                script leaves it be: remove takes the game files, and the settings stay.
//
// Run two ways: loaded with importScripts into the engine's file system backend (bo1-zombies/web/src/library_bo1.js), on its worker
// thread, which reads pieces; and as a worker of the page's, which the engine's host starts (web/public/player.js: store, new
// Worker('/store.js', { name: 'zombies-store' })), which says how much of a map the store has, gets the rest (from the server, or from the player's own install, if the page
// gives it their game folder), says what the store holds, and clears it. A pack is written whole (packWriter): through
// a writable stream to <name>.part, which gets its index and is moved to <name>.pack once it holds kPackBytes, or the
// download ends, so that a pack is whole whenever it has its name (a stream's file stays empty when its writing stops:
// the worker stopped, the tab closed, no room); the listing of the packs (index) takes no part file. Only the download
// (and its import) writes, and never to a pack that has its name, so a game reading the store never meets a write. A
// download that stops, or fails, keeps the pieces it wrote, and the next goes on from them; one whose worker ends (the
// page's watchdog stopped it, the tab closed) loses those of the pack it was writing, 64 MB at most.
'use strict';

const bo1Store = (() => {
  const available = typeof navigator !== 'undefined' && !!navigator.storage && !!navigator.storage.getDirectory;
  let rootPromise = null;
  const dirs = new Map(); // a directory's path ('packs') -> a promise of its handle

  function root() {
    if (!rootPromise) rootPromise = navigator.storage.getDirectory();
    return rootPromise;
  }

  // the directory at the path, made when create; null when it is not there
  function dir(path, create) {
    const key = path + (create ? '+' : '');
    if (!dirs.has(key)) {
      dirs.set(key, (async () => {
        let d = await root();
        for (const part of path.split('/').filter(Boolean)) {
          try {
            d = await d.getDirectoryHandle(part, { create });
          } catch (e) {
            return null;
          }
        }
        return d;
      })().then((d) => {
        if (!d) dirs.delete(key);
        return d;
      }));
    }
    return dirs.get(key);
  }

  const pieceName = (start, end) => start + '-' + end;

  // ---- the packs ----
  const kPackBytes = 64 << 20; // what a pack holds, about: a download's last holds less
  // the packs read so far, by name ('<name>.pack'): { handle, pieces (its index's), reader (readPiece) }
  const packs = new Map();
  const writing = new Set(); // the part files being written here (packWriter), which the listing leaves be
  let listed = null; // a promise of the pieces the store has (index)

  // a pack's index, from its end
  async function readIndex(handle) {
    const file = await handle.getFile();
    const length = file.size >= 4 ? new DataView(await file.slice(file.size - 4).arrayBuffer()).getUint32(0, true) : -1;
    if (length < 0 || length > file.size - 4) throw new Error('no index');
    const index = JSON.parse(await file.slice(file.size - 4 - length, file.size - 4).text());
    if (!index || index.pack !== 1 || !index.pieces) throw new Error('not a pack this page reads');
    return index.pieces;
  }
  function addPieces(byHash, pack) {
    for (const [hash, list] of Object.entries(pack.pieces)) {
      if (!byHash.has(hash)) byHash.set(hash, new Map());
      const have = byHash.get(hash);
      for (const [start, end, at] of list) {
        const name = pieceName(start, end);
        if (!have.has(name)) have.set(name, { start, end, pack, at });
      }
    }
  }
  // The pieces the store has, from the packs' indexes: hash -> its pieces' names (start-end) -> { start, end, pack, at
  // (where in the pack it is) }, one of each (two tabs downloading a map at once keep its pieces twice). The part files a
  // write left behind (its worker stopped, its tab closed) go; one another tab writes stays, as its stream holds it.
  // Read again after forget, but for the indexes of the packs read before.
  function index() {
    if (!listed) {
      listed = (async () => {
        const byHash = new Map();
        const d = await dir('packs', false);
        if (!d) return byHash;
        const found = [];
        const parts = [];
        for await (const [name, child] of d.entries()) {
          if (child.kind !== 'file') continue;
          if (name.endsWith('.pack')) found.push(child);
          else if (name.endsWith('.part') && !writing.has(name)) parts.push(name);
        }
        for (const name of parts) await d.removeEntry(name).catch(() => {});
        await Promise.all(found.filter((h) => !packs.has(h.name)).map(async (handle) => {
          try {
            packs.set(handle.name, { handle, pieces: await readIndex(handle), reader: null });
          } catch (e) {
            console.warn('store: ' + handle.name + ': ' + e.message);
          }
        }));
        for (const h of found) if (packs.has(h.name)) addPieces(byHash, packs.get(h.name));
        return byHash;
      })().catch((e) => {
        listed = null;
        throw e;
      });
    }
    return listed;
  }
  // the pieces the store has of the file with the hash: their names (start-end) -> each
  async function listPieces(hash) {
    return (await index()).get(hash) || new Map();
  }
  // the pieces the store has of the file with the hash, by start: { start, end, pack, at }
  async function pieces(hash) {
    return [...(await listPieces(hash)).values()].sort((a, b) => a.start - b.start || a.end - b.end);
  }
  // the listing of the packs is read again, as another tab's download may have added to it
  const forget = () => {
    listed = null;
  };

  // Reading: a pack through a synchronous access handle, read-only, which other tabs' games may hold too, opened once:
  // a few ms, where a read takes a hundredth of a millisecond (through the pack's File, most of one; a game reads
  // thousands of pieces as it loads, an image each). Where the browser makes no such handle (not on a dedicated worker;
  // no read-only mode, and another tab's game holds the pack), the pack's File. Making the handle, Chromium held the file
  // handle only weakly as it asked the browser whether the page may use storage (Blink:
  // FileSystemFileHandle::createSyncAccessHandle): a file handle garbage collected meanwhile dropped the making, and its
  // promise never settled; the pack holds it.
  function reader(pack) {
    if (!pack.reader) {
      pack.reader = (async () => {
        if (pack.handle.createSyncAccessHandle) {
          try {
            return { sync: await pack.handle.createSyncAccessHandle({ mode: 'read-only' }) };
          } catch (e) {
            // its File
          }
        }
        return { file: await pack.handle.getFile() };
      })().catch(() => {
        pack.reader = null;
        return null; // gone
      });
    }
    return pack.reader;
  }
  // into.length bytes of the piece from at, into the array into: true once there; false when the store has it no more
  async function readPiece(piece, at, into) {
    const r = await reader(piece.pack);
    try {
      if (r && r.sync) return r.sync.read(into, { at: piece.at + at }) === into.length;
      if (!r) return false;
      const from = piece.at + at;
      const bytes = new Uint8Array(await r.file.slice(from, from + into.length).arrayBuffer());
      into.set(bytes);
      return bytes.length === into.length;
    } catch (e) {
      piece.pack.reader = null; // gone since it was had (NotReadableError)
      return false;
    }
  }

  // A download's packs: each piece added to the pack being written (above), which is ended, getting its index and its
  // name, once it holds kPackBytes, and as the download ends (end). The pieces go to its stream kWriteBytes at a time:
  // 640 MB took 6.5 s to write a piece a write, 2 s 4 MB a write. The steps go one at a time; once one fails (no room,
  // say), its pack goes, and each step after it fails.
  const kWriteBytes = 4 << 20;
  function packWriter() {
    // the pack being written: { d, name, handle, stream, at (its length), pieces, held (what its stream lacks yet),
    // written (its length as its stream last had what it held) }
    let open = null;
    let steps = Promise.resolve();
    const step = (f) => (steps = steps.then(async () => {
      try {
        await f();
      } catch (e) {
        if (open) {
          const p = open;
          open = null;
          writing.delete(p.name + '.part');
          if (p.stream) await p.stream.abort().catch(() => {});
          await p.d.removeEntry(p.name + '.part').catch(() => {});
        }
        throw e;
      }
    }));
    async function flush() {
      if (!open.held.length) return;
      const held = open.held;
      open.held = [];
      await open.stream.write(new Blob(held));
    }
    async function finish() {
      const p = open;
      const index = new TextEncoder().encode(JSON.stringify({ pack: 1, pieces: p.pieces }));
      const length = new Uint8Array(4);
      new DataView(length.buffer).setUint32(0, index.length, true);
      p.held.push(index, length);
      await flush();
      await p.stream.close();
      await p.handle.move(p.name + '.pack');
      open = null;
      writing.delete(p.name + '.part');
      packs.set(p.name + '.pack', { handle: p.handle, pieces: p.pieces, reader: null });
      listed = null;
    }
    return {
      add: (hash, start, end, bytes) => step(async () => {
        if (!open) {
          const d = await dir('packs', true);
          const name = Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 8);
          writing.add(name + '.part');
          const handle = await d.getFileHandle(name + '.part', { create: true });
          open = { d, name, handle, stream: null, at: 0, pieces: {}, held: [], written: 0 };
          open.stream = await handle.createWritable();
        }
        open.held.push(bytes);
        (open.pieces[hash] = open.pieces[hash] || []).push([start, end, open.at]);
        open.at += bytes.length;
        if (open.at >= kPackBytes) await finish();
        else if (open.at - open.written >= kWriteBytes) {
          open.written = open.at;
          await flush();
        }
      }),
      end: () => step(async () => {
        if (open) await finish();
      }),
    };
  }

  // a text file of the store (the manifest, a map's list): written in place, as a writable stream commits what it wrote
  // only as it closes; one cut short (its writing stopped) is empty, which its reader takes for none
  async function readText(name) {
    const at = name.lastIndexOf('/');
    try {
      const d = await dir(name.slice(0, Math.max(0, at)), false);
      return d ? await (await (await d.getFileHandle(name.slice(at + 1))).getFile()).text() : null;
    } catch (e) {
      return null;
    }
  }
  async function writeText(name, text) {
    const at = name.lastIndexOf('/');
    try {
      const d = await dir(name.slice(0, Math.max(0, at)), true);
      const w = await (await d.getFileHandle(name.slice(at + 1), { create: true })).createWritable();
      await w.write(text);
      await w.close();
      return true;
    } catch (e) {
      console.warn('store: could not keep ' + name + ': ' + e.message);
      return false;
    }
  }

  // the manifest at the version: lowercase path -> { path, size, hash }; from the store, else from the server, kept then
  const manifests = new Map();
  function parseManifest(text) {
    const map = new Map();
    for (const line of text.split('\n').slice(1)) {
      const [path, size, hash] = line.replace(/\r$/, '').split('\t');
      if (path && size && hash) map.set(path.toLowerCase(), { path, size: +size, hash });
    }
    return map;
  }
  function manifest(version) {
    if (!manifests.has(version)) {
      manifests.set(version, (async () => {
        let text = available ? await readText('manifest.txt') : null;
        if (!text || !text.startsWith(version + '\n')) {
          const rsp = await fetch('/game/manifest.txt', { cache: 'no-store' });
          if (!rsp.ok) throw new Error('the manifest: ' + rsp.status);
          text = await rsp.text();
          if (!text.startsWith(version + '\n')) throw new Error('the game files are at another version than ' + version);
          if (available) await writeText('manifest.txt', text);
        }
        return parseManifest(text);
      })().catch((e) => {
        manifests.delete(version);
        throw e;
      }));
    }
    return manifests.get(version);
  }

  // what an older page kept, which no page reads now: a directory a version of the game files, in chunks
  // (v<16 hex digits>), or pieces/, a file a piece
  const old = (name) => name === 'pieces' || /^v[0-9a-f]{16}$/.test(name);

  // removes the game files the store holds (and what an older page kept); not players/, the player's settings. A pack
  // a game in another tab reads stays: the browser lets no one remove a file open to read.
  async function remove() {
    packs.clear();
    listed = null;
    try {
      const r = await root();
      const names = [];
      for await (const [name] of r.entries()) names.push(name);
      for (const name of names) {
        if (name === 'packs') {
          const d = await r.getDirectoryHandle(name);
          const inPacks = [];
          for await (const [pack] of d.entries()) inPacks.push(pack);
          let stay = 0;
          for (const pack of inPacks) await d.removeEntry(pack).catch(() => stay++);
          if (stay) console.warn('store: ' + stay + ' packs stay, read by a game in another tab');
          else await r.removeEntry(name, { recursive: true }).catch(() => {});
        } else if (name === 'lists' || name === 'manifest.txt' || old(name)) {
          await r.removeEntry(name, { recursive: true }).catch((e) => console.warn('store: ' + name + ': ' + e.message));
        }
      }
    } catch (e) {
      // no store
    }
    dirs.clear();
    manifests.clear();
  }

  async function sweepOld() {
    try {
      const r = await root();
      const found = [];
      for await (const [name, handle] of r.entries()) if (handle.kind === 'directory' && old(name)) found.push(name);
      for (const name of found) {
        console.log('store: removing ' + name + ', game files an older page kept');
        await r.removeEntry(name, { recursive: true }).catch(() => {});
      }
    } catch (e) {
      // no store
    }
  }

  return { available, index, listPieces, pieces, readPiece, packWriter, forget, readText, writeText, manifest, remove, sweepOld, pieceName };
})();

// ---- the page's worker: what the store has of a map, and its download ----
if (typeof WorkerGlobalScope !== 'undefined' && self.name === 'zombies-store') {
  // the download under way stops as its controller aborts: told to stop, as another begins, or to remove the store;
  // running ends once it has
  let stopper = new AbortController();
  let running = Promise.resolve();
  // the player's game folder, as the page last gave it (none: null), and a promise of the install in it (installIn),
  // found once a folder for the maps' states, and anew for each import; gen counts the folders the page gave, and each
  // word to the page carries it, for the page to tell a word about a folder before from one about this one
  let folder = null;
  let install = Promise.resolve(null);
  let gen = 0;
  const post = (msg) => self.postMessage(Object.assign({ op: 'progress', gen }, msg));
  const installNow = (fresh) => {
    if (fresh) install = folder ? installIn(folder).catch(() => null) : Promise.resolve(null);
    return install;
  };

  // the map's list (server/files.go: /maps/<map>/files), from the server, kept in the store; the one kept when the server
  // can't be reached; null when the server has none (a map a browser can't play). The browser's cache asks the server
  // whether its copy is the list (its ETag), and has the list again only once it changed.
  async function mapList(map, signal) {
    const kept = 'lists/' + map + '.json';
    let rsp = null;
    try {
      rsp = await fetch('/maps/' + encodeURIComponent(map) + '/files', { cache: 'no-cache', signal });
    } catch (e) {
      if (signal && signal.aborted) throw e;
    }
    if (rsp && rsp.status === 404) return null;
    if (rsp && rsp.ok) {
      const text = await rsp.text();
      if ((await bo1Store.readText(kept)) !== text) await bo1Store.writeText(kept, text);
      return JSON.parse(text);
    }
    try {
      const text = await bo1Store.readText(kept);
      if (text) return JSON.parse(text);
    } catch (e) {
      // cut short as it was written: none
    }
    throw new Error(rsp ? 'the list of what ' + map + ' reads: ' + rsp.status : 'no connection to the server');
  }

  // the list's pieces, in its order, each with whether the store has it, and its file in the list and place in it
  async function piecesOf(list) {
    const out = [];
    for (const f of list.files) {
      const have = await bo1Store.listPieces(f.hash);
      f.pieces.forEach(([start, end], i) => {
        out.push({ hash: f.hash, start, end, have: have.has(bo1Store.pieceName(start, end)), file: f, i });
      });
    }
    return out;
  }
  const sum = (pieces) => pieces.reduce((n, p) => n + (p.have ? p.end - p.start : 0), 0);

  // what the store has of the map: { map, have, total, done, inFolder (the player's folder has what the store lacks of
  // it: hasAll; none without a folder) }; none: the server has no list of it; and what said says (the page's asked, which
  // it tells an answer to a question of before the store changed by; stopped, of a download stopped). What a download
  // says has download: true (web/public/player.js takes no state but the download's while it runs), and import: true when it
  // imports.
  async function status(map, said) {
    bo1Store.forget();
    const list = await mapList(map);
    if (!list) return post(Object.assign({ map, none: true }, said));
    const pieces = await piecesOf(list);
    const have = sum(pieces);
    const found = await install;
    const inFolder = found ? { inFolder: await hasAll(found, pieces) } : {};
    post(Object.assign({ map, have, total: list.bytes, done: have >= list.bytes }, inFolder, said));
  }

  // A watchdog: nothing read, nor a write ended, for a while, and the download is given up as stalled, for the page to
  // start it again (web/public/player.js: a new worker, which ends a step the browser's file system hung in). watch(p) is p,
  // unless the download stalls first; moved() says something came, or a write ended; waiting() says what it waits for.
  function watchdog(waiting) {
    let moved = Date.now();
    let stall = null;
    const stalled = new Promise((resolve, reject) => {
      stall = reject;
    });
    stalled.catch(() => {});
    const timer = setInterval(() => {
      if (Date.now() - moved > 30000) stall(new Error('stalled, waiting ' + waiting()));
    }, 5000);
    return {
      watch: (p) => Promise.race([p, stalled]),
      moved: () => {
        moved = Date.now();
      },
      stop: () => clearInterval(timer),
    };
  }

  // ---- importing: a map's pieces from the player's own install ----
  // The player's game folder (web/src/folder.ts: their own copy of the game files, which they choose), by each file's path in
  // the install, lowercase, as Windows finds it: path -> a function giving a promise of its File; null when the folder
  // holds no install. The folder is a directory handle (the browser's folder picker's), or the files of one a file
  // input picked, each with its path from it, the folder's name first. It is the install (zone/ and main/ are in it),
  // or the folder the install is in (Steam's common/).
  async function installIn(folder) {
    const files = new Map();
    if (folder.files) {
      const paths = folder.files.map((f) => f.webkitRelativePath.replace(/^[^/]*\//, '').toLowerCase());
      const isInstall = (root) => ['zone/', 'main/'].every((dir) => paths.some((p) => p.startsWith(root + dir)));
      const tops = new Set(paths.map((p) => p.slice(0, p.indexOf('/') + 1)).filter(Boolean));
      const root = isInstall('') ? '' : [...tops].find(isInstall);
      if (root === undefined) return null;
      folder.files.forEach((f, i) => {
        if (paths[i].startsWith(root)) files.set(paths[i].slice(root.length), async () => f);
      });
      return files;
    }
    const dirs = async (dir) => {
      const out = new Map();
      for await (const [name, h] of dir.entries()) if (h.kind === 'directory') out.set(name.toLowerCase(), h);
      return out;
    };
    const isInstall = (d) => d.has('zone') && d.has('main');
    const top = await dirs(folder);
    let root = isInstall(top) ? folder : null;
    for (const d of root ? [] : top.values()) {
      if (isInstall(await dirs(d))) {
        root = d;
        break;
      }
    }
    if (!root) return null;
    const walk = async (dir, prefix) => {
      for await (const [name, h] of dir.entries()) {
        if (h.kind === 'directory') await walk(h, prefix + name.toLowerCase() + '/');
        else files.set(prefix + name.toLowerCase(), () => h.getFile());
      }
    };
    await walk(root, '');
    return files;
  }

  // whether the install has what the store lacks of the list: each file at its path, of the list's size. An import of a
  // map it has may yet find a piece of other bytes, of another version of the game (importPieces).
  async function hasAll(found, pieces) {
    for (const f of new Set(pieces.filter((p) => !p.have).map((p) => p.file))) {
      const get = found.get(f.path.toLowerCase());
      const file = get && await get().catch(() => null);
      if (!file || file.size !== f.size) return false;
    }
    return true;
  }

  const hex = (bytes) => Array.from(new Uint8Array(bytes), (b) => b.toString(16).padStart(2, '0')).join('');

  // The pieces the store lacks, from the install (installIn): each read from the install's file of its path, when it
  // has one of the list's size, and kept only when it is the server's piece, its SHA-256 the list's (sums,
  // server/hashes.go: pieceSums): an install of another version of the game, or language, has other bytes there, which
  // the store, keeping pieces by the server's hashes, would never let go. Four at a time, each read, checked and
  // written (to the pack, bo1Store.packWriter) before the next; no more of a file once a piece of it is another's.
  // kept(p) as each is kept; what the install lacked of the list, and had another version of, by path.
  async function importPieces(install, pieces, signal, dog, pack, kept) {
    const lacks = new Set();
    const differs = new Set();
    const opened = new Map(); // the list's file -> a promise of the install's File of it, null for none of its size
    const fileOf = (f) => {
      if (!opened.has(f)) {
        const get = install.get(f.path.toLowerCase());
        const got = get ? get().catch(() => null) : Promise.resolve(null);
        opened.set(f, got.then((file) => (file && file.size === f.size ? file : null)));
      }
      return opened.get(f);
    };
    const todo = pieces.filter((p) => !p.have);
    let next = 0;
    const one = async () => {
      while (next < todo.length) {
        signal.throwIfAborted();
        const p = todo[next++];
        const f = p.file;
        if (lacks.has(f.path) || differs.has(f.path)) continue;
        const file = await fileOf(f);
        // (a file that changed since the player picked the folder can't be read)
        const bytes = file && await dog.watch(file.slice(p.start, p.end).arrayBuffer()).catch((e) => {
          if (/^stalled/.test(e.message)) throw e;
          return null;
        });
        if (!bytes) {
          lacks.add(f.path);
          continue;
        }
        dog.moved();
        if (!f.sums || hex(await crypto.subtle.digest('SHA-256', bytes)).slice(0, 32) !== f.sums[p.i]) {
          differs.add(f.path);
          continue;
        }
        await dog.watch(pack.add(p.hash, p.start, p.end, new Uint8Array(bytes))).catch((e) => {
          throw noRoom(e, 'the game files');
        });
        dog.moved();
        kept(p);
      }
    };
    try {
      await Promise.all([one(), one(), one(), one()]);
    } finally {
      next = todo.length; // the others stop, once one failed
    }
    return { lacks: [...lacks], differs: [...differs] };
  }

  // what a write of the store's failed with, for the page: the browser has no room, mostly
  const noRoom = (e, what) => new Error(e.name === 'QuotaExceededError' ?
    'the browser has no room for ' + what + ' (' + e.message + ')' : 'could not keep ' + what + ': ' + e.message);

  // The map's pieces the store lacks: from the server (server/files.go: /maps/<map>/pack) in one stream, in the list's
  // order, each kept as it comes, a few writes at a time. Or, imports, from the install in the player's game folder
  // (importing, above), with nothing from the server: an install that lacks any of it (hasAll), or has other bytes of a
  // piece, leaves the map to the server, the page hearing that the folder hasn't it (inFolder false; differs: it has
  // another version). Progress goes to the page now and then: the bytes of the list the store has. Done once it has
  // them all, saying how many came from the folder (imported); a piece it could not keep fails the download, which can
  // go on later from what it kept. What it keeps goes into packs of its own (bo1Store.packWriter), the last ended before
  // the page hears how it went, so that the game it starts then finds it all.
  async function download(map, token, signal, imports) {
    const pack = bo1Store.packWriter();
    let end;
    try {
      end = await downloadTo(pack, map, token, signal, imports);
    } catch (e) {
      await pack.end().catch(() => {});
      throw e;
    }
    await pack.end().catch((e) => {
      throw noRoom(e, map);
    });
    post(Object.assign({ map, download: true, import: imports }, end));
  }
  // the download into the pack: what the page hears as it ends
  async function downloadTo(pack, map, token, signal, imports) {
    bo1Store.forget();
    const found = imports && await installNow(true);
    if (imports && !found) return { error: 'your game folder holds no Black Ops install now', noInstall: true };
    let list, pieces, have, total;
    let took = null; // what the install lacked as it was read, and had another version of
    let imported = 0; // the bytes kept from the install
    let lastPost = 0;
    const progress = (force) => {
      if (force || Date.now() - lastPost > 500) {
        lastPost = Date.now();
        post({ map, download: true, import: imports, have, total, running: true });
      }
    };
    const kept = (from) => (p) => {
      p.have = true;
      have += p.end - p.start;
      if (from === 'install') imported += p.end - p.start;
      progress(false);
    };
    // the map's list, the manifest at its version (kept for the engine, offline too), what the store has of the list,
    // and what the install has that the store lacks: 'none' for no list, 'lacks' when the install lacks some of it
    const prepare = async () => {
      list = await mapList(map, signal);
      if (!list) return 'none';
      await bo1Store.manifest(list.version);
      pieces = await piecesOf(list);
      have = sum(pieces);
      total = list.bytes;
      if (found && !(await hasAll(found, pieces))) return 'lacks';
      progress(true);
      if (!found) return '';
      const dog = watchdog(() => 'for the game folder, or the store');
      try {
        took = await importPieces(found, pieces, signal, dog, pack, kept('install'));
      } finally {
        dog.stop();
      }
      return took.lacks.length || took.differs.length ? 'lacks' : '';
    };
    // the folder hasn't the map: the page has the server send it, where it sends game files
    const lacks = () => ({ have, total, inFolder: false, differs: !!(took && took.differs.length) });
    let prepared = await prepare();
    if (prepared === 'none') return { none: true };
    if (prepared === 'lacks') return lacks();
    const wanted = (p) => !p.have && !found;
    let rsp = null;
    for (let attempt = 1; ; attempt++) {
      // runs of the pieces wanted of the server, by their place in the list: [first, end)
      const want = [];
      pieces.forEach((p, i) => {
        if (!wanted(p)) return;
        const run = want[want.length - 1];
        if (run && run[1] === i) run[1] = i + 1;
        else want.push([i, i + 1]);
      });
      if (!want.length) {
        rsp = null;
        break;
      }
      rsp = await fetch('/maps/' + encodeURIComponent(map) + '/pack', {
        method: 'POST', signal, cache: 'no-store',
        headers: Object.assign({ 'Content-Type': 'application/json' }, token ? { Authorization: 'Bearer ' + token } : {}),
        body: JSON.stringify({ version: list.version, want }),
      });
      // the game files changed on the server since the list: its new one
      if (rsp.status !== 409 || attempt > 1) break;
      prepared = await prepare();
      if (prepared === 'none') return { none: true };
      if (prepared === 'lacks') return lacks();
    }
    if (rsp && rsp.status === 403) return { have, total, off: true };
    if (rsp && !rsp.ok) throw new Error('the server answered ' + rsp.status);
    if (rsp) await receive(rsp, map, pieces, wanted, pack, kept('server'));
    if (have < total) {
      return { have, total, error: 'kept ' + Math.round(have / 1e6) + ' of ' + Math.round(total / 1e6) +
        ' MB: the rest could not be written (the browser\'s console says why)' };
    }
    return { have, total, done: true, imported };
  }

  // The pieces the server sends (servePack), those of the list wanted, in its order, each added to the pack as it comes,
  // a few at a time; kept(p) as each is. A piece it could not keep fails it.
  async function receive(rsp, map, pieces, wanted, pack, kept) {
    const todo = pieces.filter(wanted);
    const reader = rsp.body.getReader();
    const writes = new Set();
    const dog = watchdog(() => (writes.size ? 'for the store' : 'for the server'));
    let failed = null;
    const keep = (p, bytes) => {
      const w = pack.add(p.hash, p.start, p.end, bytes).then(() => {
        dog.moved();
        kept(p);
      }, (e) => {
        failed = failed || noRoom(e, map);
      }).finally(() => writes.delete(w));
      writes.add(w);
    };
    try {
      let chunk = new Uint8Array(0);
      let used = 0;
      for (const p of todo) {
        const bytes = new Uint8Array(p.end - p.start);
        let got = 0;
        while (got < bytes.length) {
          if (used === chunk.length) {
            const { done, value } = await dog.watch(reader.read());
            if (done) throw new Error('the server sent less than it said');
            dog.moved();
            chunk = value;
            used = 0;
          }
          const n = Math.min(bytes.length - got, chunk.length - used);
          bytes.set(chunk.subarray(used, used + n), got);
          got += n;
          used += n;
        }
        keep(p, bytes);
        // a few writes at a time: the stream waits for them
        while (writes.size >= 8) await dog.watch(Promise.race(writes));
        if (failed) throw failed;
      }
      await dog.watch(Promise.all(writes));
    } finally {
      dog.stop();
      reader.cancel().catch(() => {});
    }
    if (failed) throw failed;
  }

  function stop() {
    stopper.abort();
    stopper = new AbortController();
  }

  self.onunhandledrejection = (e) => {
    console.warn('store: ' + (e.reason && e.reason.message || e.reason));
  };
  bo1Store.sweepOld();
  self.onmessage = async (e) => {
    const m = e.data;
    if (m.op === 'status') {
      const said = { asked: m.asked };
      status(m.map, said).catch((err) => post(Object.assign({ map: m.map, error: err.message }, said)));
    } else if (m.op === 'download') {
      stop();
      const { signal } = stopper;
      // (import: from the player's game folder)
      const imports = !!m.import && !!folder;
      running = download(m.map, m.token, signal, imports).catch((err) => {
        if (signal.aborted) {
          console.log('store: the download of ' + m.map + ' was stopped');
          const said = { stopped: true };
          return status(m.map, said).catch((e) => post(Object.assign({ map: m.map, error: e.message }, said)));
        }
        post({ map: m.map, download: true, import: imports, error: err.message });
      });
    } else if (m.op === 'folder') {
      // the player's game folder (none: null), which the maps' states say whether it has them from now on; the page
      // hears whether it holds an install
      folder = m.folder;
      gen = m.gen;
      self.postMessage({ op: 'folder', gen, install: !!(await installNow(true)) });
    } else if (m.op === 'stop') {
      stop();
    } else if (m.op === 'usage') {
      // the origin's: the store is all but a little of it
      let usage = 0, persisted = false;
      try {
        ({ usage } = await navigator.storage.estimate());
        persisted = await navigator.storage.persisted();
      } catch (err) {
        // no estimate
      }
      const any = (await bo1Store.readText('manifest.txt')) !== null;
      self.postMessage({ op: 'usage', bytes: any ? usage : 0, persisted });
    } else if (m.op === 'remove') {
      // the download under way first, which would keep what it fetches after
      stop();
      await running;
      await bo1Store.remove();
      self.postMessage({ op: 'removed' });
    }
  };
}
