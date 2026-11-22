// web/public/sw.js - the page's service worker, for the web client offline. It keeps the page
// and what it loads (its script and sheet, the engine's script and module, its host's script and the store's, the
// game's text for the options menu, the menus' music and sounds and their decoder, the font, the maps' pictures and lists of game
// files and of pipelines, the game files' manifest, the server's answer about itself) in the browser's cache, as
// they come from the server, and serves them from it when the server can't be reached, or a proxy in front of it
// answers with an error (5xx: 502, as it can't reach the server): the page then offers to play alone a map the store
// has (web/public/store.js, web/src/session.ts: goOffline). Never the lobby's calls, nor the game files, which the store holds. Online,
// nothing changes: each request goes to the server, and what it answers refreshes the cache.
'use strict';
// The cache's name changes when the list below loses a name, so that the old cache is dropped (activate).
const CACHE = 'zombies-shell-2';
const SHELL = ['/', '/assets/index.js', '/assets/index.css', '/bo1zombies.js', '/bo1zombies.wasm', '/player.js',
  '/store.js', '/xwma.js', '/sounds', '/oswald.woff2', '/inter.woff2', '/manifest.webmanifest', '/icon.svg',
  '/icon.png', '/game/manifest.txt', '/server'];

self.addEventListener('install', (e) => {
  e.waitUntil((async () => {
    const cache = await caches.open(CACHE);
    await Promise.all(SHELL.map((url) => fetch(url).then((rsp) => rsp.ok && cache.put(url, stored(rsp))).catch(() => {})));
    await self.skipWaiting();
  })());
});

self.addEventListener('activate', (e) => {
  e.waitUntil((async () => {
    for (const name of await caches.keys()) if (name !== CACHE) await caches.delete(name);
    await self.clients.claim();
  })());
});

// what the cache may hold, by path: the shell, a map's pictures and lists, the players' pictures the page showed, the
// lobby's music and sounds, the game's text and pictures for the options menu (by path alone: the page asks for the
// same names each time), the manifest; not the lobby's calls, nor the game files at a version (the store's), nor
// another origin's
function cacheable(url) {
  if (url.origin !== self.location.origin) return false;
  const p = url.pathname;
  if (p.startsWith('/lobby/') || p === '/certhash') return false;
  if (p.startsWith('/game/')) return p === '/game/manifest.txt';
  return p === '/' || p === '/index.html' || /^\/(assets\/)?[\w.-]+\.(js|css|wasm|woff2|webmanifest|png)$/.test(p) ||
    p.startsWith('/maps/') || p.startsWith('/images/') || p.startsWith('/pictures/') || p === '/server' ||
    p === '/music' || p === '/sounds' || p === '/strings';
}

// the response as the cache keeps it: its body as fetch decoded it, so without the encoding's headers, which would
// have the browser decode it again as it comes back
function stored(rsp) {
  const headers = new Headers(rsp.headers);
  headers.delete('Content-Encoding');
  headers.delete('Content-Length');
  return new Response(rsp.body, { status: rsp.status, statusText: rsp.statusText, headers });
}

// keeps the response, but for one the cache has as it is (the same Last-Modified): the engine's module is 9 MB, which
// each visit would write again
async function keep(cache, key, rsp) {
  const modified = rsp.headers.get('Last-Modified');
  const had = modified && (await cache.match(key));
  if (had && had.headers.get('Last-Modified') === modified) return rsp.body && rsp.body.cancel();
  return cache.put(key, stored(rsp));
}

// the page asks for what else to keep: the maps' pictures and lists (web/src/cache.ts: cacheArt) and the player's own picture
// (keepAvatar), which it loaded before this worker took the page, on the first visit
self.addEventListener('message', (e) => {
  if (!e.data || e.data.op !== 'cache' || !Array.isArray(e.data.urls)) return;
  e.waitUntil((async () => {
    const cache = await caches.open(CACHE);
    await Promise.all(e.data.urls.map((url) => fetch(url).then((rsp) => rsp.ok && cache.put(url, stored(rsp))).catch(() => {})));
  })());
});

self.addEventListener('fetch', (e) => {
  const req = e.request;
  if (req.method !== 'GET') return;
  const url = new URL(req.url);
  if (!cacheable(url) || req.headers.has('range')) return;
  // the page at any of its urls (?room=, ?name=) is the one page
  const key = url.pathname === '/' || url.pathname === '/index.html' ? '/' : url.pathname;
  e.respondWith((async () => {
    const cache = await caches.open(CACHE);
    let rsp;
    try {
      rsp = await fetch(req);
    } catch (err) {
      const hit = await cache.match(key);
      if (hit) return hit;
      throw err;
    }
    if (rsp.ok) keep(cache, key, rsp.clone()).catch(() => {});
    else if (rsp.status >= 500) return (await cache.match(key)) || rsp;
    return rsp;
  })());
});
