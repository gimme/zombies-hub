// Black Ops III: a game that the players' own PCs play, on BOIII, as Black Ops II is on Plutonium. The launcher starts
// it, and the server sends its game files a map at a time. The tests' server has that game with two maps (the game
// files of global-setup.ts, in game-t7), and a launcher is its calls to the server, as in bo2.spec.ts. No game starts
// here: the stand-in for the games is none of Steam's dedicated server.
import type { Page } from '@playwright/test';
import { expect, gameFiles, test } from '../fixtures';

test.use({ serverArgs: async ({}, use) => use(['-game-dir', gameFiles('game-t7'), '-download-rate', 'unlimited']) });

/** The calls of a player's launcher to the server, by the player's key, as a launcher of a version makes them. */
async function launcherOf(server: { url: string }, page: Page, version = '4') {
  await expect(page.locator('#playbat')).toHaveText(/ [0-9a-f]{16}$/);
  const key = ((await page.locator('#playbat').textContent()) || '').split(' ').pop();
  const headers = { Authorization: 'Key ' + key, 'Zombies-Launcher-Version': version };
  const post = async (path: string, body?: object) =>
    (await fetch(server.url + path, { method: 'POST', headers, body: body && JSON.stringify(body) })).status;
  return {
    /**
     * Asks what to play, at Black Ops III, with the PC ready and the game folder having the zones: the folder of the
     * game files that the launcher downloaded, or with own, the player's own copy of the game.
     */
    has: async (zones: string, own = false) => (await fetch(server.url + '/launcher/play?of=t7&ready=1' +
      (own ? '&own=1' : '') + '&has=' + encodeURIComponent(zones), { headers })).status,
    /** Reports how the launcher is doing: its download, or nothing. */
    says: (report: object) => post('/launcher/files', report),
    /** Says that the launcher ends, as its player quit it. */
    stops: () => post('/launcher/stopped'),
    /** As has, from a launcher whose game runs, which it started. */
    runs: async (zones: string) =>
      (await fetch(server.url + '/launcher/play?of=t7&ready=1&game=1&has=' + encodeURIComponent(zones), { headers })).status,
    /** Asks what to play as a launcher from before it started Black Ops III does. */
    asks: async () => (await fetch(server.url + '/launcher/play?ready=1', { headers })).status,
  };
}

test('a Black Ops III room opens the launcher, says where to get it, and how to join by hand', async ({ server, player }) => {
  const ann = await player('Ann');
  await ann.locator('#make').click();
  await ann.locator('#titlepick').getByRole('radio', { name: 'Black Ops III', exact: true }).click();
  await ann.locator('#mapgrid').getByRole('radio', { name: 'Origins', exact: true }).click();
  await expect(ann.locator('#maptitle')).toHaveText('Origins');
  // the room's button opens the launcher, and the box says where to get it, and what it installs for this game
  await expect(ann.locator('#ready')).toHaveText('Connect');
  await expect(ann.locator('#ready')).toHaveAttribute('data-does', 'launch');
  // nothing launches the game before the launcher is there: the room's other button is off, and says what comes first
  await expect(ann.locator('#launchgame')).toHaveText('Launch game');
  await expect(ann.locator('#launchgame')).toHaveAttribute('aria-disabled', 'true');
  await expect(ann.locator('#launchgame')).toHaveAttribute('title', /^Connect the launcher first/);
  await expect(ann.locator('#pcget')).toHaveText('First time? Install Zombies Launcher.');
  await ann.locator('#pcget button.what').click();
  await expect(ann.locator('#pcabout').getByRole('link', { name: 'BOIII' }))
    .toHaveAttribute('href', 'https://github.com/Ezz-lol/boiii-free');
  // by hand: the line for the console of the player's own game, which takes a command after a slash
  await expect(ann.locator('#pcconsole')).toBeHidden();
  await ann.locator('#pchandrow').getByText('Join by hand').click();
  await expect(ann.locator('#pchandhow')).toContainText('Start Black Ops III with BOIII');
  await expect(ann.locator('#pcconsole')).toHaveText(/^\/password [0-9a-f]+;connect [^;]+:\d+$/);
  await expect(ann.locator('#ready')).toHaveText('Need 1 more');
  await ann.locator('#pchandrow').getByText('Join by hand').click();
  await expect(ann.locator('#ready')).toHaveText('Connect');

  // a launcher from before it started Black Ops III is refused in this room: the page says to get the new one
  const old = await launcherOf(server, ann, '3');
  expect(await old.asks()).toBe(426);
  await expect(ann.locator('#pcget')).toHaveText('Your Zombies Launcher is too old for this server. Install the new one.');
  // one that starts it: a copy of the game without the room's map, which the server sends
  const launcher = await launcherOf(server, ann);
  expect(await launcher.has('zone/zm_factory.ff')).toBe(200);
  await expect(ann.locator('#pcinstall')).toBeHidden();
  await expect(ann.locator('#members li.mine .badge')).toHaveText('Missing map');
  await expect(ann.locator('#ready')).toHaveText('Download');
  await expect(ann.locator('#roomnote')).toHaveText("Your Black Ops III doesn't have this map. The launcher can " +
    'download the game files from the server instead.');
  await expect(ann.locator('#launchgame')).toHaveAttribute('aria-disabled', 'true');
  expect(await launcher.has('zone/zm_factory.ff,zone/zm_tomb.ff')).toBe(200);
  await expect(ann.locator('#members li.mine .badge')).toHaveText('Not ready');
  await expect(ann.locator('#ready')).toHaveText('Need 1 more');
  await expect(ann.locator('#roomnote')).toBeHidden();
  // With the PC ready for the room's map, the room's other button launches the game now, at its menu, ahead of the
  // room's game. It is off again while the game runs that the launcher started, and says so, and on once that game
  // is closed. A player who joins by hand has no such button.
  await expect(ann.locator('#launchgame')).toHaveText('Launch game');
  await expect(ann.locator('#launchgame')).not.toHaveAttribute('aria-disabled', 'true');
  expect(await launcher.runs('zone/zm_factory.ff,zone/zm_tomb.ff')).toBe(200);
  await expect(ann.locator('#launchgame')).toHaveText('Game running');
  await expect(ann.locator('#launchgame')).toHaveAttribute('aria-disabled', 'true');
  await expect(ann.locator('#ready')).toHaveText('Need 1 more');
  expect(await launcher.has('zone/zm_factory.ff,zone/zm_tomb.ff')).toBe(200);
  await expect(ann.locator('#launchgame')).toHaveText('Launch game');
  await expect(ann.locator('#launchgame')).not.toHaveAttribute('aria-disabled', 'true');
  await ann.locator('#pchandrow').getByText('Join by hand').click();
  await expect(ann.locator('#launchgame')).toBeHidden();
});

test("Play local has Black Ops III's maps: none is picked, and a click downloads one that the PC lacks", async ({ server, player }) => {
  const ann = await player('Ann');
  await ann.locator('#soloopen').click();
  await ann.locator('#solotitles').getByRole('radio', { name: 'Black Ops III', exact: true }).click();
  await expect(ann.locator('#solotitle')).toHaveText('Black Ops III');
  await expect(ann.locator('#solodesc')).toHaveText(
    "Launches the game on this PC through BOIII. Steam must be installed, but doesn't need to be running. No account needed.");
  await expect(ann.locator('#soloown')).toBeHidden(); // Plutonium's online servers are Black Ops II's
  // The page knows nothing of the game on this PC: its button connects the launcher, as a room's does. The game's
  // maps are tiles, of which none is picked and none says anything, but for a map that the server lacks.
  const maps = ann.locator('#solomaps');
  const tile = (name: string) => maps.locator('.maptile', { hasText: name });
  await expect(maps).toHaveAttribute('role', 'group');
  await expect(maps.getByRole('radio')).toHaveCount(0);
  await expect(maps.getByRole('button')).toHaveCount(0);
  await expect(maps.locator('.size')).toHaveCount(0);
  await expect(tile('The Giant')).not.toHaveClass(/off/);
  await expect(tile('Shadows of Evil')).toHaveClass(/off/);
  await expect(ann.locator('#playalone')).toHaveText('Connect');
  await ann.locator('#playalone').click(); // which starts no launcher on this PC
  await expect(ann.locator('#playalone')).toHaveText('Connecting...');
  await expect(ann.locator('#pcget')).toHaveText('Nothing happened? Install Zombies Launcher.');
  await expect(ann.locator('#playalone')).toHaveText('Connect');

  // Her launcher, at this game, with The Giant in the folder of the game files that it downloaded. The button now
  // launches the game. The Giant says nothing more, and Origins, which the folder lacks, is a button with its size.
  const launcher = await launcherOf(server, ann);
  expect(await launcher.has('zone/zm_factory.ff')).toBe(200);
  await expect(ann.locator('#playalone')).toHaveText('Launch game');
  await expect(tile('The Giant').locator('.size')).toHaveCount(0);
  const origins = maps.getByRole('button', { name: /^Origins/ });
  await expect(origins.locator('.size')).toHaveText('2 MB');
  await expect(origins).toHaveAttribute('title', 'Download Origins · 2 MB');
  await expect(maps.getByRole('button')).toHaveCount(1);
  // a click on it has the launcher download the map: the main button says how far that is, and the map's is off
  await origins.click();
  expect(await launcher.says({ running: true, got: 1, need: 4 })).toBe(200);
  await expect(ann.locator('#playalone')).toHaveText('Downloading · 25%');
  await expect(origins).toHaveAttribute('aria-disabled', 'true');
  expect(await launcher.says({})).toBe(200);
  expect(await launcher.has('zone/zm_factory.ff,zone/zm_tomb.ff')).toBe(200);
  await expect(maps.getByRole('button')).toHaveCount(0);
  await expect(tile('Origins').locator('.size')).toHaveCount(0);
  await expect(ann.locator('#playalone')).toHaveText('Launch game');

  // The player's own copy of the game, which the launcher only reads: a map that it lacks is not installed, and no
  // click downloads it.
  expect(await launcher.has('zone/zm_factory.ff', true)).toBe(200);
  await expect(tile('Origins').locator('.size')).toHaveText('Not installed');
  await expect(tile('Origins')).toHaveClass(/lacks/);
  await expect(maps.getByRole('button')).toHaveCount(0);
  expect(await launcher.has('zone/zm_factory.ff')).toBe(200);
  await expect(maps.getByRole('button', { name: /^Origins/ })).toBeVisible();

  // The launcher stops, and the page goes by what it said last, which the browser keeps: also when the page loads
  // again. The button launches the game, with no launcher to connect first.
  expect(await launcher.stops()).toBe(200);
  await expect(ann.locator('#playalone')).toHaveText('Launch game');
  await expect(maps.getByRole('button', { name: /^Origins/ }).locator('.size')).toHaveText('2 MB');
  await ann.reload();
  await expect(ann.locator('#solotitle')).toHaveText('Black Ops III');
  await expect(ann.locator('#playalone')).toHaveText('Launch game');
  await expect(maps.getByRole('button', { name: /^Origins/ }).locator('.size')).toHaveText('2 MB');
  await expect(tile('The Giant').locator('.size')).toHaveCount(0);
  // Black Ops' maps, in the same place, are the ones to pick from
  await ann.locator('#solotitles').getByRole('radio', { name: 'Black Ops', exact: true }).click();
  await expect(ann.locator('#solokicker')).toHaveText('Map');
  await expect(maps).toHaveAttribute('role', 'radiogroup');
  await expect(maps.getByRole('radio', { name: 'Kino der Toten', exact: true })).toHaveAttribute('aria-checked', 'true');
  await expect(maps.locator('.maptile')).toHaveCount(0);
});

test("a Black Ops III map has its picture in the game's map select, and its loading screen behind the page", async ({ player }) => {
  const ann = await player('Ann');
  await ann.locator('#make').click();
  await ann.locator('#titlepick').getByRole('radio', { name: 'Black Ops III', exact: true }).click();
  await expect(ann.locator('#maptitle')).toHaveText('The Giant');
  // the picture's size once it has loaded, and its colour at its middle
  const picture = (img: HTMLImageElement): string => {
    if (!img.complete || !img.naturalWidth) return 'not loaded';
    const canvas = document.createElement('canvas');
    canvas.width = img.naturalWidth;
    canvas.height = img.naturalHeight;
    const g = canvas.getContext('2d')!;
    g.drawImage(img, 0, 0);
    const [r, , b] = g.getImageData(img.naturalWidth >> 1, img.naturalHeight >> 1, 1, 1).data;
    return `${img.naturalWidth}x${img.naturalHeight} ${r > 200 && b < 60 ? 'red' : b > 200 && r < 60 ? 'blue' : 'neither'}`;
  };
  // the room's map: its preview is its picture in the game's map select, which is no loading screen, in a shot of
  // its wide shape
  const shot = ann.locator('#mapshot');
  await expect(shot.locator('img')).toHaveAttribute('src', '/maps/zm_factory/preview');
  await expect.poll(() => shot.locator('img').evaluate(picture)).toBe('640x270 blue');
  const box = (await shot.boundingBox())!;
  expect(Math.abs(box.width / box.height - 64 / 27)).toBeLessThan(0.03);
  // the page's backdrop is the map's loading screen
  const backdrop = ann.locator('#backdropimg');
  await expect(backdrop).toHaveAttribute('src', '/maps/zm_factory/loadscreen');
  await expect.poll(() => backdrop.evaluate(picture)).toBe('16x9 red');
  // the room's grid has each map's, and the room goes to the map picked
  const origins = ann.locator('#mapgrid').getByRole('radio', { name: 'Origins', exact: true });
  await expect.poll(() => origins.locator('img').evaluate(picture)).toBe('640x270 red');
  await origins.click();
  await expect(shot.locator('img')).toHaveAttribute('src', '/maps/zm_tomb/preview');
  await expect.poll(() => backdrop.evaluate(picture)).toBe('16x9 blue');

  // Outside a room the backdrop is Black Ops' map to play locally: also while Play local shows Black Ops III, of
  // which no map is picked there. That game's maps have their pictures in its grid.
  await ann.locator('#leave').click();
  await expect(backdrop).toHaveAttribute('src', '/maps/zombie_theater/loadscreen');
  await ann.locator('#soloopen').click();
  await ann.locator('#solotitles').getByRole('radio', { name: 'Black Ops III', exact: true }).click();
  await expect(ann.locator('#solotitle')).toHaveText('Black Ops III');
  const tile = ann.locator('#solomaps .maptile', { hasText: 'Origins' });
  await expect.poll(() => tile.locator('img').evaluate(picture)).toBe('640x270 red');
  await expect(backdrop).toHaveAttribute('src', '/maps/zombie_theater/loadscreen');
  await ann.locator('#soloback').click();
  await expect(backdrop).toHaveAttribute('src', '/maps/zombie_theater/loadscreen');
});
