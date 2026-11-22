// A map the browser lacks, on a server that sends no game files (no -download-rate): the page offers to import it
// from the player's own copy of the game, the room says the player is missing it, and the lobby takes them as ready
// only with it. The game folder menu, under the player's name, says when a folder holds no install, and a folder whose
// install lacks the map leaves it missing.
import type { Page } from '@playwright/test';
import { apiPlayer, expect, makeRoom, test } from '../fixtures';

// About 1.2 GB of the map to get.
const missing = { zombie_theater: { have: 0, total: 1_234_567_890 } };

declare global {
  // eslint-disable-next-line no-var
  var picks: number | undefined;
}

// The folder picker gives a folder of the browser's private file system: first an empty one, then one with an install
// in it (a folder main), whose maps are other files than the store's.
async function fakeFolderPicker(page: Page): Promise<void> {
  await page.addInitScript(() => {
    (window as unknown as { showDirectoryPicker: () => Promise<FileSystemDirectoryHandle> }).showDirectoryPicker =
      async () => {
        globalThis.picks = (globalThis.picks || 0) + 1;
        const root = await navigator.storage.getDirectory();
        if (globalThis.picks === 1) return root.getDirectoryHandle('player-empty', { create: true });
        const common = await root.getDirectoryHandle('player-common', { create: true });
        const install = await common.getDirectoryHandle('Call of Duty Black Ops', { create: true });
        await install.getDirectoryHandle('main', { create: true });
        return common;
      };
  });
}

test('a map the browser lacks is offered for import, and the room says it is missing', async ({ player }) => {
  const gus = await player('Gus', { maps: missing });
  await gus.locator('#soloopen').click();
  await expect(gus.locator('#playalone')).toHaveText(/^Import · \d/);
  await expect(gus.locator('#playalone')).toHaveAttribute('title', 'From your own copy of the game');
  await expect(gus.locator('#playalone')).not.toHaveAttribute('aria-disabled', 'true');
  await gus.locator('#soloback').click();

  await makeRoom(gus);
  await expect(gus.locator('#ready')).toHaveText(/^Import · \d/);
  await expect(gus.locator('#members li .badge').first()).toHaveText('Missing map');
  // the grid of the room's maps greys the map out, no game folder being chosen
  const picked = gus.locator('#mapgrid button[aria-checked=true]');
  await expect(picked).toBeDisabled();
  await expect(picked).toHaveAttribute('title', /: no game folder chosen, and the server sends no game files$/);
});

test('the game folder menu says when a folder holds no install, and a folder that lacks the map leaves it missing',
  async ({ player }) => {
    const lou = await player('Lou', { maps: missing, wait: false });
    await fakeFolderPicker(lou);
    await lou.reload();
    await makeRoom(lou);
    await lou.locator('#namechip').click();
    await expect(lou.locator('#folderpick')).toHaveText('Choose folder');
    await expect(lou.locator('#folderhelp')).toHaveText('Import your own copy of the game files.');
    await expect(lou.locator('#foldername')).toHaveText('');
    await lou.locator('#folderpick').click();
    await expect(lou.locator('#folderhelp')).toHaveText('That folder holds no Black Ops install.');
    await expect(lou.locator('#foldername')).toHaveText('');
    await lou.locator('#folderpick').click();
    await expect(lou.locator('#foldername')).toHaveText('player-common');
    await expect(lou.locator('#folderpick')).toHaveText('Change');
    await lou.keyboard.press('Escape');
    await expect(lou.locator('#ready')).toHaveText('Missing map');
    await expect(lou.locator('#ready')).toHaveAttribute('aria-disabled', 'true');
    await expect(lou.locator('#mapgrid button[aria-checked=true]')).toBeDisabled();
  });

test("the lobby takes a player as ready only with the room's map, and sends no game files", async ({ server }) => {
  const jo = await apiPlayer(server, 'Jo');
  await jo.say('create', {});
  expect(JSON.parse(await jo.say('ready', { ready: true })).error).toBe("You don't have the room's map yet.");
  const pack = await fetch(server.url + '/maps/zombie_theater/pack', { method: 'POST', body: '[]',
    headers: { Authorization: 'Bearer ' + jo.token } });
  expect(pack.status).toBe(403);
  jo.close();
});
