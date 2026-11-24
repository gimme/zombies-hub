// A room's mods: its host sets them in the Mods tab, the others see how they are set and can't change them, Grief puts
// the players in two teams, and the room's game has them on its command line. A room has the mods that its game has.
import { expect, joinRoom, makeRoom, playRoomGame, test } from '../fixtures';
import type { Page } from '@playwright/test';

const MODS = ['Unique Wall Weapons', 'Grief', 'Zombie Cap 32'];

// The Mods tab's count, its switches, and Zombie Cap's options: * checked.
const modsShown = (page: Page) => page.evaluate(() => [document.getElementById('modcount')!.textContent,
  [...document.querySelectorAll('#panelmods .switch')].map((b) => b.getAttribute('aria-checked')).join(' '),
  [...document.querySelectorAll<HTMLButtonElement>('#panelmods .seg button')].map((b) => b.textContent +
    (b.getAttribute('aria-checked') === 'true' ? '*' : '')).join(' ')]);

// Each member's line under their name, in the room's order.
const memberLines = (page: Page) => page.locator('#members li:not(.empty) .what').allTextContents();

test('the host turns mods on, the others see them, and the game has them', async ({ server, player }) => {
  const ann = await player('Ann');
  const code = await makeRoom(ann);
  expect(await modsShown(ann)).toEqual(['0', 'false false', '24* 32']);
  await expect(ann.locator('#modsnames')).toHaveText('None');
  expect(await memberLines(ann)).toEqual(['Host']);

  await ann.locator('#tabmods').click();
  const controls = ['.mod:nth-child(1) .switch', '.mod:nth-child(2) .switch',
    '.mod:nth-child(3) .seg button:nth-child(2)'];
  for (const [i, control] of controls.entries()) {
    await ann.locator('#panelmods ' + control).click();
    await expect(ann.locator('#modcount')).toHaveText(String(i + 1));
  }
  await expect(ann.locator('#modcount')).not.toHaveClass(/none/);
  expect(await modsShown(ann)).toEqual(['3', 'true true', '24 32*']);
  // the line above Ready: every name, or the first of them whole and "…" where the line is too short
  await expect(ann.locator('#modsnames')).toHaveAttribute('title', MODS.join(', '));
  const shown = ((await ann.locator('#modsnames').textContent()) || '').replace(/, …$/, '').split(', ');
  expect(MODS.slice(0, shown.length)).toEqual(shown);
  expect(await memberLines(ann)).toEqual(['Team 1 · Host']);

  const bob = await player('Bob');
  await joinRoom(bob, code);
  await expect(bob.locator('#modcount')).toHaveText('3');
  await bob.locator('#modsline').click();
  await expect(bob.locator('#tabmods')).toHaveAttribute('aria-selected', 'true');
  await expect(bob.locator('#panelmods .badge')).toHaveText(['On', 'On', '32']);
  await expect(bob.locator('#panelmods button')).toHaveCount(0);
  await expect.poll(() => memberLines(bob)).toEqual(['Team 1 · Host', 'Team 2']);

  // the room's maps, which only its host picks: a click on one marks it for the host
  await bob.locator('#mapgrid button:nth-child(2)').click();
  await expect(bob.locator('#mapgrid button:nth-child(2) .marks .avatar.mine')).toBeVisible();
  await expect(ann.locator('#mapgrid button:nth-child(2) .marks .avatar:not(.mine)')).toBeVisible();

  await playRoomGame(ann, bob);
  await server.waitFor(new RegExp(`^\\[${code}\\] stand-in: args .*\\+set bo1_mod_uniquewallweapons 1 ` +
    '\\+set bo1_mod_grief 1 \\+set bo1_mod_zombiecap 32 '));
});

test("a room has the mods of its game: Black Ops II has no Grief, which is a mode there", async ({ player }) => {
  const ann = await player('Ann');
  await makeRoom(ann);
  await ann.locator('#tabmods').click();
  for (const control of ['.mod:nth-child(1) .switch', '.mod:nth-child(2) .switch',
    '.mod:nth-child(3) .seg button:nth-child(2)']) {
    await ann.locator('#panelmods ' + control).click();
  }
  await expect(ann.locator('#modcount')).toHaveText('3');

  // the room keeps the mods that its new game has, and the tab has those alone
  await ann.locator('#tabsettings').click();
  await ann.locator('#titlepick').getByRole('radio', { name: 'Black Ops II', exact: true }).click();
  await expect(ann.locator('#modcount')).toHaveText('2');
  await expect(ann.locator('#modsnames')).toHaveAttribute('title', 'Unique Wall Weapons, Zombie Cap 32');
  await ann.locator('#tabmods').click();
  await expect(ann.locator('#panelmods .mod')).toHaveCount(2);
  expect(await modsShown(ann)).toEqual(['2', 'true', '24 32*']);
  await ann.locator('#panelmods .seg button:nth-child(1)').click();
  await expect(ann.locator('#modsnames')).toHaveAttribute('title', 'Unique Wall Weapons');

  // back on Black Ops, Grief is there again, and off
  await ann.locator('#tabsettings').click();
  await ann.locator('#titlepick').getByRole('radio', { name: 'Black Ops', exact: true }).click();
  await ann.locator('#tabmods').click();
  await expect(ann.locator('#panelmods .mod')).toHaveCount(3);
  expect(await modsShown(ann)).toEqual(['1', 'true false', '24* 32']);
});
