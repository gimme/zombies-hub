// A Black Ops room's game, as its players come and go: a player leaves the game and rejoins it, reloads in it, leaves
// the room, and plays the server's new build; the room takes no one while it plays; its size; and a game that no one
// joins, or that a client of another build can't join, is called off.
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { apiPlayer, expect, joinRoom, makeRoom, playRoomGame, said, test } from '../fixtures';

declare global {
  // eslint-disable-next-line no-var
  var standin: { leave(): void; over(round?: number): void };
  // eslint-disable-next-line no-var
  var marked: boolean | undefined;
}

test('a player leaves the game and rejoins it, then leaves the room, and the game lets their character go', async (
  { server, player }) => {
  const ann = await player('Ann');
  const bob = await player('Bob');
  const code = await makeRoom(ann);
  await joinRoom(bob, code);
  await playRoomGame(ann, bob);
  await bob.evaluate(() => { globalThis.marked = true; });

  // the game's menu: Quit. The page is back in the room, which offers the character back.
  await bob.evaluate(() => globalThis.standin.leave());
  await expect(bob.locator('#ready')).toHaveText('Rejoin');
  await expect(bob.locator('#ready')).toHaveAttribute('data-does', 'rejoin');
  await server.waitFor(new RegExp(`^\\[${code}\\] bo1_lobby: quit 127\\.1\\.`));
  expect(await bob.evaluate(() => globalThis.marked), 'the page reloaded').toBe(true);
  await expect(ann.locator('#ready')).toHaveText('In game');
  await bob.locator('#ready').click();
  await expect(bob.locator('#ready')).toHaveText('In game');

  // Leave room, out of the game: the page asks first, and the game lets the character go.
  await bob.evaluate(() => globalThis.standin.leave());
  await expect(bob.locator('#ready')).toHaveText('Rejoin');
  await bob.locator('#leave').click();
  await expect(bob.locator('#asktext')).toHaveText("Leave the game? You won't be able to rejoin.");
  await bob.locator('#askyes').click();
  await expect(bob.locator('#outside')).toBeVisible();
  await server.waitFor(new RegExp(`^\\[${code}\\] bo1_lobby: out 127\\.1\\.`));
  await expect(ann.locator('#members')).not.toContainText('Bob');
});

test('a page that reloads in its game asks first, and offers the game back', async ({ player }) => {
  const ann = await player('Ann');
  const bob = await player('Bob');
  await joinRoom(bob, await makeRoom(ann));
  await playRoomGame(ann, bob);
  const asked: string[] = [];
  bob.on('dialog', (d) => {
    asked.push(d.type());
    d.accept();
  });
  await bob.reload();
  expect(asked).toEqual(['beforeunload']);
  await expect(bob.locator('#ready')).toHaveText('Rejoin');
  await bob.locator('#ready').click();
  await expect(bob.locator('#ready')).toHaveText('In game');
});

test('a room in game takes no one else', async ({ player }) => {
  const ann = await player('Ann');
  const bob = await player('Bob');
  const code = await makeRoom(ann);
  await joinRoom(bob, code);
  await playRoomGame(ann, bob);
  const dee = await player('Dee');
  await dee.locator('#code').fill(code);
  await dee.locator('#joincode').click();
  await expect(dee.locator('#notice')).toHaveText(`Room ${code} is in game.`);
  await expect(dee.locator('#outside')).toBeVisible();
});

test('a page left open while the server was updated reloads as its game starts', async ({ server, player }) => {
  const ann = await player('Ann');
  const bob = await player('Bob');
  await joinRoom(bob, await makeRoom(ann));
  // a new build of the engine: the server's page changes with it (server/web.go: webVersion)
  mkdirSync(join(server.dataDir, 'engine', 'web'));
  writeFileSync(join(server.dataDir, 'engine', 'web', 'bo1zombies.js'), '');
  const reloaded = bob.waitForEvent('load');
  await playRoomGame(ann, bob);
  await reloaded;
  expect(said(bob)).toContain('lobby: joining room ' + (await bob.locator('#roomcode').textContent()) + "'s game");
});

test.describe('a server of rooms of up to 6', () => {
  test.use({ serverArgs: ['-max-players', '6'] });

  test("the host sets the room's size", async ({ server, player }) => {
    const ann = await player('Ann');
    const code = await makeRoom(ann);
    const sizes = ann.locator('#sizesetting .seg button');
    await expect(sizes).toHaveText(['2', '3', '4', '5', '6']);
    await expect(ann.locator('#sizesetting .seg button[aria-checked=true]')).toHaveText('4');
    await expect(ann.locator('#count')).toHaveText('1 / 4');
    await expect(ann.locator('#members li')).toHaveCount(4);
    await ann.locator('#sizesetting .seg button[data-value="6"]').click();
    await expect(ann.locator('#count')).toHaveText('1 / 6');
    await expect(ann.locator('#members li')).toHaveCount(6);
    await server.waitFor(new RegExp(`^server: room ${code} takes 6 players$`));
    const bob = await player('Bob');
    await joinRoom(bob, code);
    await expect(bob.locator('#sizetext')).toHaveText('6 players');
  });
});

test.describe('a server whose games wait 3 s for their players', () => {
  test.use({ serverArgs: ['-load-timeout', '3s', '-min-players', '1'] });

  test('a game that no one joins is called off', async ({ server }) => {
    const nobody = await apiPlayer(server, 'Nobody');
    await nobody.say('create', { private: true });
    await nobody.say('files', { map: 'zombie_theater', done: true }); // as a page that has the map says
    expect(await nobody.say('ready', { ready: true })).toBe('{}\n');
    const ends = await server.waitFor(/^server: room [A-Z]{4}'s game ends: /, 15_000);
    expect(ends).toMatch(/Game called off: Nobody didn't join in time\.$/);
    nobody.close();
  });
});

test.describe('a server whose games may have one player', () => {
  test.use({ serverArgs: ['-min-players', '1'] });

  test("a client of another build can't join, and its page says so", async ({ server, player }) => {
    const other = await player('Other', { query: 'autostart=1&args=%2Bset%20bo1_build%20other', wait: false });
    const refusal = 'The server runs build standin of the game, and this client build other.';
    await expect(other.locator('#status')).toContainText(refusal + ' Reloading the page would not help');
    const ends = await server.waitFor(/^server: room [A-Z]{4}'s game ends: /, 15_000);
    expect(ends).toContain(`Game called off: Other's game failed to load. ${refusal}`);
  });
});
