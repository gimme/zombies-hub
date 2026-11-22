// docker/player/host.mjs - a page that is not the lobby's plays a map through the engine's host (web/public/player.js), as
// another project's page would: the check that the host needs no page of ours. The page is this script's own, a
// container and a module script, served to the browser in the server's name (the request never reaches the server),
// cross-origin isolated as the server's page is; all it loads comes from the server. It has no settings object, the
// host makes its canvases, and it knows the game only by the host's callbacks.
//
//   node docker/player/host.mjs <server> [map] [games]      e.g. http://localhost:3075 zombie_theater 3
//
// It asks the store for the map, downloads what it lacks, takes the lock and plays the map alone (playAlone) until
// round 1 begins; then the engine quits (the console's quit) and the page stops it (stop). It plays so games times
// (default 3), the first of the others at once, to see the next game wait for the last engine's threads to end and
// take its memory (web/public/player.js: THREADS_END), and the rest stopped by the page with the game still running, as it is
// when a room's game ends before the engine says so. It fails on an error the page throws, as a thread that failed is
// one. Exit code 0 when all of them reached round 1.
//
// It runs the installed Chrome, headless, on this machine's GPU (ZOMBIES_CHROME: its executable, where it is not where
// Chrome installs), against a server that is up: docker/server.sh's, say. It needs the player's library (npm ci in
// docker/player).
import { chromium } from 'playwright';

const [server, map = 'zombie_theater', count = '3'] = process.argv.slice(2);
if (!server) {
  console.error('usage: node docker/player/host.mjs <server> [map] [games]');
  process.exit(2);
}
const games = Math.max(1, parseInt(count, 10) || 3);

const html = `<!doctype html>
<meta charset="utf-8">
<title>The engine's host on a page of its own</title>
<style>html, body { margin: 0; height: 100%; background: #000; } #game { position: fixed; inset: 0; }</style>
<div id="game"></div>
<script type="module">
import { createPlayer, support, plain } from '/player.js';
const log = (text) => console.log('host: ' + text);
const map = ${JSON.stringify(map)};
log('support: ' + JSON.stringify(support()));
let said = 0;
const player = createPlayer({
  container: document.getElementById('game'),
  onStatus: (text) => text && log('status: ' + text),
  onState: (state) => log('state: ' + state),
  onLoading: () => log('the loading screen shows'),
  onMovie: (what) => log('movie: ' + what),
  onGame: (line) => log('game: ' + line),
  onStore: (m, s) => {
    if (!s || !s.running || Date.now() - said < 2000) return;
    said = Date.now();
    const p = player.store.progress(m);
    log('downloading ' + m + ': ' + Math.round(p.got / 1e6) + ' of ' + Math.round(p.need / 1e6) + ' MB');
  },
  onEnd: (why, text) => {
    log('ended (' + why + '): ' + plain(text));
    if (player.state !== 'ended') return; // the engine said so before
    const stopped = player.stop();
    player.lock.release();
    log('stopped: ' + stopped);
  },
  onError: (text) => log('error: ' + text),
});
window.player = player;
window.play = async () => {
  const has = await player.store.status(map);
  if (!has || has.none) return log('no game: ' + (has ? map + ' is no map a browser plays' : 'this browser has no store'));
  log('the store has ' + Math.round(has.have / 1e6) + ' of ' + Math.round(has.total / 1e6) + ' MB of ' + map);
  if (!has.done) {
    const end = await player.store.download(map, {});
    if (!end || !end.done) return log('no game: the download ended with ' + JSON.stringify(end));
  }
  if (!(await player.lock.take())) return log('no game: another tab plays');
  const begun = await player.playAlone({ map, name: 'Host' });
  log(begun.started ? 'started, on ' + player.gpu : 'no game: ' + begun.why);
};
log('ready');
</script>`;

const browser = await chromium.launch(Object.assign({ headless: true, args: ['--autoplay-policy=no-user-gesture-required'] },
  process.env.ZOMBIES_CHROME ? { executablePath: process.env.ZOMBIES_CHROME } : { channel: 'chrome' }));
const page = await browser.newPage({ viewport: { width: 1280, height: 720 } });
const lines = [];
const waiters = [];
let failed = '';
page.on('console', (m) => {
  const t = m.text();
  if (/^(host|web player): |^\[bo1\] (assert|gpu error)/.test(t)) console.log(t);
  lines.push(t);
  for (const w of waiters.slice()) {
    if (!w.re.test(t)) continue;
    waiters.splice(waiters.indexOf(w), 1);
    w.resolve(t);
  }
});
page.on('pageerror', (e) => {
  console.log('page: uncaught ' + e.message);
  failed = failed || 'the page threw: ' + e.message;
});
// the next line the page says that matches, or one it said since the game began; null after ms
const next = (re, ms) => new Promise((resolve) => {
  const had = lines.find((l) => re.test(l));
  if (had) return resolve(had);
  const w = { re, resolve };
  waiters.push(w);
  setTimeout(() => {
    if (!waiters.includes(w)) return;
    waiters.splice(waiters.indexOf(w), 1);
    resolve(null);
  }, ms);
});
async function fail(text) {
  console.log('host: FAILED: ' + text);
  await browser.close();
  process.exit(1);
}

await page.route('**/host-check.html', (route) => route.fulfill({
  status: 200, contentType: 'text/html; charset=utf-8', body: html,
  headers: { 'Cross-Origin-Opener-Policy': 'same-origin', 'Cross-Origin-Embedder-Policy': 'require-corp' },
}));
await page.goto(server.replace(/\/$/, '') + '/host-check.html');
if (!(await next(/^host: ready$/, 30000))) await fail('the page did not load /player.js');

for (let game = 1; game <= games; game++) {
  lines.length = 0;
  await page.evaluate(() => window.play());
  // (a first game downloads the map first)
  const begun = await next(/^host: (started|no game)/, 900000);
  if (!begun || !begun.startsWith('host: started')) await fail(`game ${game} did not start`);
  if (!(await next(/^host: game: bo1_lobby: start$/, 300000))) await fail(`game ${game} did not reach round 1`);
  if (!lines.includes('host: state: running') || !lines.includes('host: the loading screen shows'))
    await fail(`game ${game} reached round 1 without the host saying its engine ran and its loading screen showed`);
  await page.waitForTimeout(3000);
  const shown = await page.evaluate(() => {
    const [movie, canvas] = document.querySelectorAll('#game canvas');
    return `${canvas.width}x${canvas.height}, the movie's canvas ${movie.id} ${movie.style.display}, the game's ` +
      (document.activeElement === canvas ? 'with the focus' : 'without the focus');
  });
  console.log(`host: game ${game} is in round 1: ${shown}`);
  if (game === 1) {
    // the engine quits, and the page stops it as it says so
    await page.evaluate(() => window.player.command('quit'));
    if (!(await next(/^host: stopped: true$/, 60000))) await fail(`game ${game} did not end and stop at quit`);
  } else {
    // the page stops the engine with its game still running
    const stopped = await page.evaluate(() => {
      const ok = window.player.stop();
      window.player.lock.release();
      return ok;
    });
    if (!stopped) await fail(`game ${game}'s engine did not stop`);
  }
  if (failed) await fail(failed);
}
await page.waitForTimeout(1000);
if (failed) await fail(failed);
console.log(`host: passed: a page of its own played ${map} ${games} times through web/public/player.js`);
await browser.close();
