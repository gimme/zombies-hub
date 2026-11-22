// A player's tabs: one plays the room's game, and another shows the room and leaves the game to it until the player
// plays there (Play here). A tab opened once the one that played closed offers to rejoin the game rather than join it,
// and once no tab has had the game for a while, the room offers to rejoin it.
import { expect, said, test } from '../fixtures';

declare global {
  // eslint-disable-next-line no-var
  var marked: boolean | undefined;
}

test.use({ serverArgs: ['-min-players', '1'] });

test('a second tab leaves the game to the first until the player plays there', async ({ server, player }) => {
  // ?autostart=1: a room of the player's own, whose game starts at once
  const first = await player('Cy', { query: 'autostart=1', wait: false });
  await expect(first.locator('#ready')).toHaveText('In game');
  const code = (await first.locator('#roomcode').textContent()) || '';
  await first.evaluate(() => { globalThis.marked = true; });

  const second = await player('Cy', { tab: first, query: 'room=' + code });
  await expect(second.locator('#roomstate')).toHaveText('Your game is open in another tab.');
  await expect(first.locator('#ready')).toHaveText('In game');
  await second.locator('#ready[data-does="here"]').click();
  await expect(second.locator('#ready')).toHaveText('In game');
  await expect.poll(() => said(first)).toContain("lobby: back in the lobby, the game's engine stopped");
  expect(await first.evaluate(() => globalThis.marked), 'the first tab reloaded').toBe(true);
  await second.close();

  // the page opened again, as the tab that played has closed
  const third = await player('Cy', { tab: first, query: 'room=' + code });
  await expect(third.locator('#ready')).toHaveText('Rejoin');
  await expect(third.locator('#ready')).toHaveAttribute('data-does', /^(rejoin|here)$/);
  await third.waitForTimeout(3000);
  await expect(third.locator('#ready')).toHaveText('Rejoin');
  expect(said(third).filter((line) => line.startsWith('lobby: joining room'))).toEqual([]);
  await third.close();

  // no tab has had the game for 10 s: the first tells the lobby, and the room offers the game back
  await first.bringToFront();
  await expect(first.locator('#ready[data-does="rejoin"]')).toHaveText('Rejoin', { timeout: 30_000 });
  await server.waitFor(new RegExp(`^server: Cy's game in room ${code} ended \\(closed\\): `));
  await first.locator('#ready').click();
  await expect(first.locator('#ready')).toHaveText('In game');
});
