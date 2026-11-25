// A map played alone, from the lobby's Play local: no room and no game on the server; at game over the page is back
// in the lobby, which says so for a moment and offers to play again. Play local is Black Ops, and has its options
// menu under Play.
import { expect, said, test } from '../fixtures';

test("Play local has Black Ops' options menu, which closes back to its button", async ({ player }) => {
  const eve = await player('Eve');
  await expect(eve.locator('#settings')).toBeHidden();
  await eve.locator('#soloopen').click();
  await expect(eve).toHaveURL(/[?&]local=1(&|$)/); // the view's own URL, which a reload keeps
  const button = eve.locator('#solo #settings');
  await expect(button).toHaveText('Black Ops settings');
  await button.click();
  await expect(eve.locator('#options')).toBeVisible();
  await expect(button).toHaveAttribute('aria-expanded', 'true');
  await eve.keyboard.press('Escape');
  await expect(eve.locator('#options')).toBeHidden();
  await expect(button).toHaveAttribute('aria-expanded', 'false');
  await expect(button).toBeFocused();
});

declare global {
  // eslint-disable-next-line no-var
  var standin: { leave(): void; over(round?: number): void };
}

test('a map played alone ends at game over, back in the lobby', async ({ server, player }) => {
  const eve = await player('Eve');
  await eve.locator('#soloopen').click();
  await expect(eve.locator('#solo')).toBeVisible();
  const map = (await eve.locator('#solotitle').textContent()) || '';
  await expect(eve.locator('#playalone')).toHaveText('Play');
  await eve.locator('#playalone').click();
  await expect.poll(() => said(eve).find((line) => line.startsWith('lobby: playing '))).toMatch(/ alone$/);
  await expect(eve.locator('#overlay')).toBeHidden();

  await eve.evaluate(() => globalThis.standin.over(7));
  await expect(eve.locator('#notice')).toHaveText('Game over on round 7.');
  await expect(eve.locator('#notice')).not.toHaveClass(/refusal/);
  await expect(eve.locator('#status')).toHaveText('');
  await expect(eve.locator('#playalone')).toHaveText('Play');
  await expect(eve.locator('#playalone')).not.toHaveAttribute('aria-disabled', 'true');
  await expect(eve.locator('#solotitle')).toHaveText(map);
  // the word goes by itself, as a refusal does
  await expect(eve.locator('#notice')).toBeHidden({ timeout: 12_000 });
  expect(server.log()).not.toMatch(/Eve made room|starts a game/);
});
