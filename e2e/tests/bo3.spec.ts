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
  return {
    /** Asks what to play, at Black Ops III, with the PC ready and the game folder having the zones. */
    has: async (zones: string) =>
      (await fetch(server.url + '/launcher/play?of=t7&ready=1&has=' + encodeURIComponent(zones), { headers })).status,
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

test("Play local has Black Ops III's maps, with what the PC has of them and what it would download", async ({ server, player }) => {
  const ann = await player('Ann');
  await ann.locator('#soloopen').click();
  await ann.locator('#solotitles').getByRole('radio', { name: 'Black Ops III', exact: true }).click();
  await expect(ann.locator('#solotitle')).toHaveText('Black Ops III');
  await expect(ann.locator('#solodesc')).toHaveText('Launches the game on this PC through BOIII, at its menu. No account needed.');
  await expect(ann.locator('#soloown')).toBeHidden(); // Plutonium's online servers are Black Ops II's
  // what a PC without the game gets from the server, once the server has listed the game's files
  await expect(ann.locator('#solofiles')).toHaveText(
    'A PC without the game gets it from this server: 3 MB for every map, and then the maps you pick.');
  // the game's maps: the first that the server has is picked, and none says what the PC has before a launcher did
  const maps = ann.locator('#solomaps');
  await expect(maps.getByRole('radio', { name: 'The Giant', exact: true })).toHaveAttribute('aria-checked', 'true');
  await expect(maps.getByRole('radio', { name: 'Shadows of Evil' })).toBeDisabled(); // a map that the server lacks
  await expect(maps.locator('.size')).toHaveCount(0);
  await expect(ann.locator('#playalone')).toHaveText('Launch game');

  // her launcher, at this game, with The Giant in the folder that the game starts from
  const launcher = await launcherOf(server, ann);
  expect(await launcher.has('zone/zm_factory.ff')).toBe(200);
  await expect(maps.getByRole('radio', { name: /^The Giant/ }).locator('.size')).toHaveText('Installed');
  await expect(maps.getByRole('radio', { name: /^Origins/ }).locator('.size')).toHaveText('2 MB');
  await expect(ann.locator('#playalone')).toHaveText('Launch game');
  // a map that the folder lacks: the button downloads it
  await maps.getByRole('radio', { name: /^Origins/ }).click();
  await expect(maps.getByRole('radio', { name: /^Origins/ })).toHaveAttribute('aria-checked', 'true');
  await expect(ann.locator('#playalone')).toHaveText('Download · 2 MB');
  expect(await launcher.has('zone/zm_factory.ff,zone/zm_tomb.ff')).toBe(200);
  await expect(maps.getByRole('radio', { name: /^Origins/ }).locator('.size')).toHaveText('Installed');
  await expect(ann.locator('#playalone')).toHaveText('Launch game');
  // the pick is kept, and is this game's alone
  await ann.reload();
  await expect(ann.locator('#solotitle')).toHaveText('Black Ops III');
  await expect(maps.getByRole('radio', { name: /^Origins/ })).toHaveAttribute('aria-checked', 'true');
  await ann.locator('#solotitles').getByRole('radio', { name: 'Black Ops', exact: true }).click();
  await expect(ann.locator('#solokicker')).toHaveText('Map');
  await expect(ann.locator('#solofiles')).toBeHidden();
});
