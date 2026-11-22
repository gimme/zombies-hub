// A Black Ops II room: its host picks the game, then a map of it; its players get ready, their own Plutonium joins its
// game through the relay, and they see its report and the game's own leaderboards.
import { createSocket, type Socket } from 'node:dgram';
import { expect, test } from '../fixtures';

/**
 * Joins a room's game as a player's Plutonium does: from the address the player's page calls from (127.0.0.1), to the
 * server's port, which relays it to the room's game (server/t6.go: t6Seat). It asks until the game answers: the game
 * takes the first packets only once it is up.
 */
async function plutonium(port: number, name: string): Promise<Socket> {
  const socket = createSocket('udp4');
  const answered = new Promise<void>((resolve) => socket.once('message', () => resolve()));
  const ask = () => socket.send(Buffer.from('\xff\xff\xff\xffconnect ' + name, 'latin1'), port, '127.0.0.1');
  ask();
  const timer = setInterval(ask, 500);
  await answered;
  clearInterval(timer);
  return socket;
}

test('a room plays its game to game over, and its round is on the leaderboards', async ({ server, player }) => {
  const ann = await player('Ann');
  const bob = await player('Bob');

  await ann.locator('#make').click();
  await ann.locator('#titlepick').getByRole('radio', { name: 'Black Ops II', exact: true }).click();
  await expect(ann.locator('#maptitle')).toHaveText('TranZit'); // its first map: the server's default is Black Ops'
  await ann.locator('#mapgrid').getByRole('radio', { name: 'Town', exact: true }).click();
  await expect(ann.locator('#maptitle')).toHaveText('Town');
  await expect(ann.locator('#roomcode')).toHaveText(/^[A-Z]{4}$/);
  const code = (await ann.locator('#roomcode').textContent()) || '';
  await bob.locator('#code').fill(code);
  await bob.locator('#joincode').click();
  await expect(ann.locator('#members')).toContainText('Bob');

  await ann.locator('#ready').click();
  await bob.locator('#ready').click();
  // the countdown, then the game, which the players join from their own PCs
  const games = await Promise.all([plutonium(server.port, 'Ann'), plutonium(server.port, 'Bob')]);
  for (const page of [ann, bob]) await expect(page.locator('#ready')).toHaveText('In game');

  server.command(`${code} round 3`);
  server.command(`${code} over 5`);
  for (const page of [ann, bob]) {
    await expect(page.locator('#results')).toBeVisible();
    await expect(page.locator('#resulttitle')).toHaveText('Round 5');
    await expect(page.locator('#resultrows')).toContainText('Ann');
    await expect(page.locator('#resultrows')).toContainText('Bob');
  }
  for (const game of games) game.close();

  // the leaderboards open on the room's game, whose card has Ann's round on Town
  await ann.locator('#resultsback').click();
  await ann.locator('#boardsopen').click();
  await expect(ann.locator('#boardtitles button[aria-checked=true]')).toHaveText('Black Ops II');
  await expect(ann.locator('#boardtables .tile.lit')).toHaveCount(1);
  await expect(ann.locator('#boardtables .tile.lit .name')).toHaveText('Town');
  await expect(ann.locator('#boardtables')).toContainText('Ann');
});
