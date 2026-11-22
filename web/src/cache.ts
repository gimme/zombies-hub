// web/src/cache.ts - what the service worker keeps for the page offline (web/public/sw.js).
//
// The service worker keeps the page and the engine in the browser's cache, for when the server can't be reached. It
// takes over the page only once it is installed, which is after the first visit's own requests. The page therefore
// asks it to cache what the page loaded before that: the maps' pictures and lists, and the player's picture.
import type { MapInfo } from './api';
import { store } from './storage';

if ('serviceWorker' in navigator && self.isSecureContext) {
  navigator.serviceWorker.register('/sw.js')
    .catch((e: Error) => console.log('lobby: no service worker: ' + e.message));
}

// Asks the service worker to cache the URLs, once it has the page.
function cache(urls: () => string[] | null): void {
  if (!('serviceWorker' in navigator)) return;
  navigator.serviceWorker.ready.then((reg) => {
    const sw = navigator.serviceWorker.controller || reg.active;
    const list = sw && urls();
    if (sw && list) sw.postMessage({ op: 'cache', urls: list });
  }).catch(() => {});
}

let artCached = false;

/** Has the service worker cache the pictures of the maps, and the lists of the maps that the server has. Once. */
export function cacheArt(maps: MapInfo[]): void {
  if (artCached) return;
  cache(() => {
    if (artCached) return null;
    artCached = true;
    const urls = [];
    for (const m of maps) {
      const at = '/maps/' + encodeURIComponent(m.name) + '/';
      urls.push(at + 'preview', at + 'loadscreen');
      if (m.have) urls.push(at + 'files', at + 'pipelines');
    }
    return urls;
  });
}

let avatarKept: string | null = null;

/**
 * Keeps the player's picture for the page offline, each time it changes: its URL in the browser's storage, and the
 * picture itself in the service worker's cache when it is one of the game's. A picture from Discord is not cached.
 */
export function keepAvatar(url: string): void {
  if (url === avatarKept) return;
  avatarKept = url;
  store.set('zombies-avatar', url);
  if (url.startsWith('/pictures/')) cache(() => [url]);
}
