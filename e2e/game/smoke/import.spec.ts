// A player brings their own game. On a server that sends no game files, the map's button offers to import the map
// from the player's game folder, and the player picks their Black Ops install (the game files, as the server has
// them). The store keeps each piece of it only as its hash in the map's list says it is the server's, and the page
// asks the server for none of it (the base data files the install lacks come with the engine's module). The player
// plays the map alone from the store, to game over, the game reading nothing from the server.
//
// The server runs without NET_ADMIN, which its firewall needs: it says its games then reach the network.
import { expect, test } from '../fixtures';
import { loadingMovie } from '../play';

test.use({ serverOptions: { env: { ZOMBIES_DOWNLOAD_RATE: '' }, noFirewall: true } });

test("a player imports a map from their own install, and plays it", async ({ server, players }) => {
  expect(server.find(/^entrypoint: the games can reach the network, as only NET_ADMIN lets a firewall stop them/),
    'the server says its games reach the network').toBeTruthy();
  expect(server.find(/^server: the server sends no game files/), 'the server says it sends no game files').toBeTruthy();
  const install = process.env.ZOMBIES_TEST_INSTALL;
  if (!install) throw new Error('no ZOMBIES_TEST_INSTALL: run the game tests with docker/test-games.sh');
  // the movie starts as the game loads the map, for Kit to click over it and skip it
  const kit = await players.open('Kit', { importFrom: install, args: '+set bo1_web_movieAtLoad 1' });
  kit.background(() => loadingMovie(kit, 'solo'));
  const { map } = await kit.getTheMap('#playalone', install);
  const imported = await kit.waitFor(new RegExp(`^lobby: imported ${map} from the game folder \\(([0-9]+) ` +
    `MB\\)$`), 300_000);
  kit.note(imported);
  await kit.page.click('#playalone');
  await kit.waitFor(new RegExp(`^lobby: playing ${map} alone$`), 60_000);
  const p = await kit.spawned();
  kit.note(`spawned alone at ${p.origin.join(' ')} with ${p.weapon}`);
  kit.checkReads();
  expect(kit.reads, "the page's requests for the map").toEqual([]);
  // game over: loadgame_continue on the game's console, which the level's game over comes to
  const over = kit.next(/^lobby: the game ended \(over\): /, 120_000);
  await kit.command('loadgame_continue');
  expect(await over, 'game over ended the game').toBeTruthy();
  await expect(kit.page.locator('#playalone'), 'the page is back in the lobby, offering to play alone')
    .toHaveText('Play', { timeout: 60_000 });
});
