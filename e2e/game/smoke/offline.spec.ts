// A player plays a map alone offline. Through the proxy, as from another machine, they download the map picked to
// play alone into the browser's store, and the service worker (web/public/sw.js) keeps the page and the engine. Then
// the proxy stops, the browser's only way to the server and its open connections with it; the page reloads from the
// worker's cache, says in the rooms' list that there is no connection to the server, offers the map the store has,
// and plays it, reading nothing from the server.
import { expect, test } from '../fixtures';
import { loadingMovie } from '../play';

test('a player plays a map alone offline', async ({ server, players }) => {
  await server.startProxy();
  const fay = await players.open('Fay', { proxy: true });
  fay.background(() => loadingMovie(fay, 'room'));
  const { map } = await fay.getTheMap('#playalone');
  await server.stopProxy();
  fay.note('the proxy stopped: the browser can no longer reach the server');
  await fay.page.reload({ waitUntil: 'domcontentloaded' });
  await expect.poll(() => fay.page.evaluate(() => ({
    rooms: document.getElementById('rooms')!.textContent,
    play: document.getElementById('playalone')!.textContent,
    can: document.getElementById('playalone')!.getAttribute('aria-disabled') !== 'true',
  })).catch((e: Error) => ({ error: e.message })), { message: 'the page offline offers the map', timeout: 90_000 })
    .toEqual({ rooms: 'No connection to the server.', play: 'Play', can: true });
  await fay.shot('offline');
  fay.note(`offline, ${map} offered`);
  await fay.page.click('#playalone');
  const p = await fay.spawned();
  fay.note(`spawned alone offline at ${p.origin.join(' ')} with ${p.weapon}`);
  await fay.shot('offline-alone');
  fay.checkReads();
});
