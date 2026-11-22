// A Black Ops room: its players get ready, play its game in their pages, and see its report and the leaderboards.
import { expect, joinRoom, makeRoom, playRoomGame, test } from '../fixtures';

test('a room plays its game to game over, and its round is on the leaderboards', async ({ server, player }) => {
  const ann = await player('Ann');
  const bob = await player('Bob');
  const code = await makeRoom(ann);
  await joinRoom(bob, code);
  await expect(ann.locator('#members')).toContainText('Bob');
  // the countdown, then the game: each page joins it through the relay
  await playRoomGame(ann, bob);

  server.command(`${code} round 3`);
  server.command(`${code} over 5`);
  for (const page of [ann, bob]) {
    await expect(page.locator('#results')).toBeVisible();
    await expect(page.locator('#resulttitle')).toHaveText('Round 5');
    await expect(page.locator('#resultrows')).toContainText('Ann');
    await expect(page.locator('#resultrows')).toContainText('Bob');
  }

  await ann.locator('#resultsback').click();
  await ann.locator('#boardsopen').click();
  await expect(ann.locator('#boards')).toBeVisible();
  await expect(ann.locator('#boardtables')).toContainText('Ann');
});
