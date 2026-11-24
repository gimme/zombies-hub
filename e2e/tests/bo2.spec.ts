// A Black Ops II room: its host picks the game, then a map of it; its players join by hand and get ready, their own
// Plutonium joins its game through the relay, and they see its report and the game's own leaderboards, with the map's
// easter egg that they finished. The room's
// button opens the launcher, and the room links to the launcher's latest release, again after a press that started no
// launcher. A launcher too old for the server is turned away, and the room says so. A player whose copy of the game
// lacks the room's map is not ready, and where the server sends game files, the room's button offers them.
import type { Page } from '@playwright/test';
import { createSocket, type Socket } from 'node:dgram';
import { expect, joinRoom, makeRoom, test } from '../fixtures';

// Where the room links to the launcher: its latest release (internal/launcherapi: LatestFile).
const setup = 'https://github.com/gimme/zombies-hub/releases/download/launcher/zombies-launcher-setup.exe';

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

test('a room plays its game to game over, and its round and easter egg are on the leaderboards', async (
  { server, player }) => {
  const ann = await player('Ann');
  const bob = await player('Bob');

  await ann.locator('#make').click();
  await ann.locator('#titlepick').getByRole('radio', { name: 'Black Ops II', exact: true }).click();
  await expect(ann.locator('#maptitle')).toHaveText('TranZit'); // its first map: the server's default is Black Ops'
  await ann.locator('#mapgrid').getByRole('radio', { name: 'Die Rise', exact: true }).click();
  await expect(ann.locator('#maptitle')).toHaveText('Die Rise');
  await expect(ann.locator('#roomcode')).toHaveText(/^[A-Z]{4}$/);
  const code = (await ann.locator('#roomcode').textContent()) || '';
  await bob.locator('#code').fill(code);
  await bob.locator('#joincode').click();
  await expect(ann.locator('#members')).toContainText('Bob');

  // the button opens the launcher, unless the player joins by hand, which shows the line for the game's console; the
  // browser remembers that they do
  for (const page of [ann, bob]) {
    await expect(page.locator('#ready')).toHaveText('Connect');
    await expect(page.locator('#t6console')).toBeHidden();
    await page.locator('#t6handrow').getByText('Join by hand').click(); // the checkbox's words check it
    await expect(page.getByRole('checkbox', { name: 'Join by hand' })).toBeChecked();
    await expect(page.locator('#t6console')).toHaveText(/^password [0-9a-f]+;connect [^;]+:\d+$/);
    await expect(page.locator('#ready')).toHaveText('Ready');
  }
  // the server sends their launchers no game, which would join theirs while they join by hand
  for (const name of ['Ann', 'Bob']) {
    await server.waitFor(new RegExp(`^server: ${name} joins Black Ops II games by hand$`));
  }
  await bob.reload();
  await expect(bob.locator('#roomcode')).toHaveText(code);
  await expect(bob.getByRole('checkbox', { name: 'Join by hand' })).toBeChecked();
  await expect(bob.locator('#t6console')).toBeVisible();
  for (const page of [ann, bob]) {
    await expect(page.locator('#ready')).toHaveText('Ready');
    await page.locator('#ready').click();
  }
  // the countdown, then the game, which the players join from their own PCs. A player who no longer joins by hand has
  // the button that opens the launcher, into the game.
  await expect(ann.locator('#ready')).toHaveText('Join by hand');
  await ann.getByRole('checkbox', { name: 'Join by hand' }).uncheck();
  await expect(ann.locator('#ready')).toHaveText('Join the game');
  await expect(ann.locator('#ready')).toHaveAttribute('data-does', 'launch');
  await ann.getByRole('checkbox', { name: 'Join by hand' }).check();
  await expect(ann.locator('#ready')).toHaveText('Join by hand');
  const games = await Promise.all([plutonium(server.port, 'Ann'), plutonium(server.port, 'Bob')]);
  for (const page of [ann, bob]) await expect(page.locator('#ready')).toHaveText('In game');
  // every player of a Black Ops II game plays it outside their page, so the room says of none that they do
  await expect(ann.locator('#members')).not.toContainText('On Windows');
  // the room's maps, and its host's pick of its game, stay as they were while the game runs, and can't be picked
  for (const page of [ann, bob]) {
    await expect(page.locator('#mapgrid button[aria-checked=true]')).toHaveText('Die Rise');
    await expect(page.locator('#mapgrid button[aria-checked=true]')).toBeDisabled();
  }
  await expect(ann.locator('#titlepick button[aria-checked=true]')).toBeDisabled();

  server.command(`${code} round 3`);
  server.command(`${code} egg`); // the main quest done, with both players in the game
  await server.waitFor(/^server: Bob finished the easter egg of zm_classic_rooftop with a party of 2 in \d+ s$/);
  server.command(`${code} over 5`);
  for (const page of [ann, bob]) {
    await expect(page.locator('#results')).toBeVisible();
    await expect(page.locator('#resulttitle')).toHaveText('Round 5');
    await expect(page.locator('#resultrows')).toContainText('Ann');
    await expect(page.locator('#resultrows')).toContainText('Bob');
  }
  for (const game of games) game.close();

  // the leaderboards open on the room's game, whose card has Ann's round on Die Rise, and then her easter egg there
  await ann.locator('#resultsback').click();
  await ann.locator('#boardsopen').click();
  await expect(ann.locator('#boardtitles button[aria-checked=true]')).toHaveText('Black Ops II');
  await expect(ann.locator('#boardtables .tile.lit')).toHaveCount(1);
  await expect(ann.locator('#boardtables .tile.lit .name')).toHaveText('Die Rise');
  await expect(ann.locator('#boardtables')).toContainText('Ann');
  await ann.locator('#boardtabs').getByRole('radio', { name: 'Easter eggs' }).click();
  await expect(ann.locator('#boardtables .tile.lit.gold .name')).toHaveText('Die Rise');
  await expect(ann.locator('#boardtables.all tr.mine')).toContainText('Ann');
});

test("a launcher too old for the server is turned away, and the room links to the launcher's latest release",
  async ({ server, player }) => {
    const ann = await player('Ann');
    await ann.locator('#make').click();
    await ann.locator('#titlepick').getByRole('radio', { name: 'Black Ops II', exact: true }).click();
    await ann.locator('#mapgrid').getByRole('radio', { name: 'Town', exact: true }).click();
    await expect(ann.locator('#maptitle')).toHaveText('Town');
    // a player whose browser has seen no launcher of theirs is shown where to get it
    await expect(ann.locator('#ready')).toHaveText('Connect');
    await expect(ann.locator('#t6get')).toHaveText('First time? Install Zombies Launcher.');
    await expect(ann.locator('#t6setup')).toHaveAttribute('href', setup);
    await expect(ann.locator('#t6mac')).toBeHidden(); // the line for a Mac's Terminal
    // the "?" by it shows what the launcher is and how it goes again, and hides it again
    await expect(ann.locator('#t6about')).toBeHidden();
    await ann.locator('#t6get button.what').click();
    await expect(ann.locator('#t6about')).toContainText('helps start the game on this PC and connects it to the room');
    await expect(ann.locator('#t6about').getByRole('link', { name: 'Plutonium' }))
      .toHaveAttribute('href', 'https://plutonium.pw/');
    await ann.locator('#t6get button.what').click();
    await expect(ann.locator('#t6about')).toBeHidden();
    // the button, which starts no launcher on this PC: it says that the launcher starts, until the line says that
    // nothing happened, and it is the button it was
    await ann.locator('#ready').click();
    await expect(ann.locator('#ready')).toHaveText('Connecting...');
    await expect(ann.locator('#ready')).toHaveAttribute('aria-disabled', 'true');
    await expect(ann.locator('#t6get')).toHaveText('Nothing happened? Install Zombies Launcher.');
    await expect(ann.locator('#ready')).toHaveText('Connect');

    // calls of Ann's launcher, by her key, which the line of the Windows client in her profile has
    await expect(ann.locator('#playbat')).toHaveText(/ [0-9a-f]{16}$/);
    const key = ((await ann.locator('#playbat').textContent()) || '').split(' ').pop();
    const call = (version?: string) => fetch(server.url + '/launcher/play', {
      headers: { Authorization: 'Key ' + key, ...(version !== undefined && { 'Zombies-Launcher-Version': version }) },
    });
    // a launcher of any version says that it started, before it looks for a later release of itself: the room waits
    // for it, and no longer shows where to install it
    const started = await fetch(server.url + '/launcher/started', {
      method: 'POST', headers: { Authorization: 'Key ' + key },
    });
    expect(started.status).toBe(200);
    await expect(ann.locator('#ready')).toHaveText('Connecting...');
    await expect(ann.locator('#t6get')).toBeHidden();
    // its next call has no version: the server turns it away
    let rsp = await call();
    expect(rsp.status).toBe(426);
    expect(await rsp.text()).toContain('This launcher is too old for this server');
    await expect(ann.locator('#t6get')).toHaveText(
      'Your Zombies Launcher is too old for this server. Install the new one.');
    await expect(ann.locator('#t6setup')).toHaveAttribute('href', setup);
    await expect(ann.locator('#ready')).toHaveText('Connect');
    // a dev build, on a server that is no dev server
    rsp = await call('0');
    expect(rsp.status).toBe(426);
    expect(await rsp.text()).toContain('dev build');
    // a launcher that the server takes, of its lowest version (launcherapi.MinVersion): the room says no more of it
    rsp = await call('1');
    expect(rsp.status).toBe(200);
    await expect(ann.locator('#t6get')).toBeHidden();
    await expect(ann.locator('#ready')).toHaveText('Connecting...');
    // the button says what the launcher does once the launcher has been at it for a moment
    const doing = await fetch(server.url + '/launcher/files', {
      method: 'POST', headers: { Authorization: 'Key ' + key, 'Zombies-Launcher-Version': '1' },
      body: JSON.stringify({ doing: 'Checking files' }),
    });
    expect(doing.status).toBe(200);
    await expect(ann.locator('#ready')).toHaveText('Checking files...');
    // a launcher that its player quits says so: the button opens a launcher again at once, where the server would
    // otherwise wait for the launcher's next call for longer than this test does
    const stopped = await fetch(server.url + '/launcher/stopped', {
      method: 'POST', headers: { Authorization: 'Key ' + key },
    });
    expect(stopped.status).toBe(200);
    await expect(ann.locator('#ready')).toHaveText('Connect');
  });

test('a Mac is shown the line that installs the launcher from Terminal', async ({ server, player }) => {
  const ann = await player('Ann', { query: 'mac=1' }); // a Mac's page, on this PC
  await ann.locator('#make').click();
  await ann.locator('#titlepick').getByRole('radio', { name: 'Black Ops II', exact: true }).click();
  await expect(ann.locator('#ready')).toHaveText('Connect');
  // the line fetches the launcher's script from its latest release and runs it
  await expect(ann.locator('#t6get')).toBeHidden();
  await expect(ann.locator('#t6machow')).toHaveText('First time? Install Zombies Launcher with this line in Terminal:');
  await expect(ann.locator('#t6macline')).toHaveText(
    '/bin/bash -c "$(curl -fsSL ' + setup.replace('zombies-launcher-setup.exe', 'zombies-launcher-mac.sh') + ')"');
  // no button copies it: a content blocker would object
  await expect(ann.locator('#t6mac .codeline button')).toHaveCount(0);
  // the "?" by it says what the line does
  await ann.locator('#t6mac button.what').click();
  await expect(ann.locator('#t6about')).toContainText('This script installs Zombies Launcher');
  await expect(ann.locator('#t6about')).toContainText('run the "Zombies Launcher" app');
  await expect(ann.locator('#t6about').getByRole('link', { name: 'plutonium-mac-aio' }))
    .toHaveAttribute('href', 'https://github.com/LuidgiAlt/plutonium-mac-aio');
  // the button, which starts no launcher on this PC. A Mac's launcher has longer to show itself, as Wine starts first.
  await ann.locator('#ready').click();
  await expect(ann.locator('#ready')).toHaveText('Connecting...');
  await ann.waitForTimeout(2500); // past a PC's wait
  await expect(ann.locator('#ready')).toHaveText('Connecting...');
  await expect(ann.locator('#t6machow')).toHaveText(
    'Nothing happened? Install Zombies Launcher with this line in Terminal:', { timeout: 10000 });
  await expect(ann.locator('#ready')).toHaveText('Connect');

  // a launcher that the Mac's script is too old for (launcher/mac.go) says so, and ends: the room shows the line again,
  // and not the launcher's failure, until the next launcher of hers reports
  await expect(ann.locator('#playbat')).toHaveText(/ [0-9a-f]{16}$/);
  const key = ((await ann.locator('#playbat').textContent()) || '').split(' ').pop();
  const launcher = (path: string, body?: object) => fetch(server.url + path, {
    method: 'POST', headers: { Authorization: 'Key ' + key, 'Zombies-Launcher-Version': '1' },
    body: body && JSON.stringify(body),
  });
  expect((await launcher('/launcher/started')).status).toBe(200);
  await expect(ann.locator('#t6mac')).toBeHidden();
  expect((await launcher('/launcher/files', { failed: "This Mac's Zombies Launcher needs an update.", setup: true })).status)
    .toBe(200);
  expect((await launcher('/launcher/stopped')).status).toBe(200);
  await expect(ann.locator('#t6machow')).toHaveText(
    'Your Zombies Launcher needs an update. Install the new one with this line in Terminal:');
  await expect(ann.locator('#ready')).toHaveText('Connect');
  await expect(ann.locator('#roomnote')).toBeHidden();
  expect((await launcher('/launcher/started')).status).toBe(200);
  expect((await launcher('/launcher/files', {})).status).toBe(200);
  await expect(ann.locator('#t6mac')).toBeHidden();
});

test("Play offline starts Black Ops II on the player's PC, and says what the launcher does", async ({ server, player }) => {
  const ann = await player('Ann');
  await expect(ann.locator('#soloopen')).toHaveText('Play solo');
  await ann.locator('#soloopen').click();
  // the server hosts both games: Black Ops is picked first, and Black Ops II is one click away
  const titles = ann.locator('#solotitles');
  await expect(titles.getByRole('radio', { name: 'Black Ops', exact: true })).toHaveAttribute('aria-checked', 'true');
  await expect(ann.locator('#solomaps')).toBeVisible();
  await titles.getByRole('radio', { name: 'Black Ops II', exact: true }).click();
  await expect(titles.getByRole('radio', { name: 'Black Ops II', exact: true })).toHaveAttribute('aria-checked', 'true');
  await expect(ann.locator('#solotitle')).toHaveText('Black Ops II');
  await expect(ann.locator('#solodesc')).toHaveText('Launches the game on this PC through Plutonium.');
  await expect(ann.locator('#solodesc').getByRole('link', { name: 'Plutonium' }))
    .toHaveAttribute('href', 'https://plutonium.pw/');
  await expect(ann.locator('#solomaps')).toBeHidden();
  await expect(ann.locator('#settings')).toBeHidden(); // Black Ops' options
  // where to get the launcher, as in a room
  await expect(ann.locator('#t6get')).toHaveText('First time? Install Zombies Launcher.');
  await expect(ann.locator('#t6setup')).toHaveAttribute('href', setup);
  // the button, which starts no launcher on this PC
  await expect(ann.locator('#playalone')).toHaveText('Launch game');
  await ann.locator('#playalone').click();
  await expect(ann.locator('#playalone')).toHaveText('Connecting...');
  await expect(ann.locator('#t6get')).toHaveText('Nothing happened? Install Zombies Launcher.');
  await expect(ann.locator('#playalone')).toHaveText('Launch game');

  // calls of Ann's launcher, by her key: the button says what it does, and the box where to get it goes
  await expect(ann.locator('#playbat')).toHaveText(/ [0-9a-f]{16}$/);
  const key = ((await ann.locator('#playbat').textContent()) || '').split(' ').pop();
  const headers = { Authorization: 'Key ' + key, 'Zombies-Launcher-Version': '1' };
  const launcher = (path: string, body?: object) =>
    fetch(server.url + path, { method: 'POST', headers, body: body && JSON.stringify(body) });
  const ready = () => fetch(server.url + '/launcher/play?ready=1', { headers });
  expect((await launcher('/launcher/started')).status).toBe(200);
  await expect(ann.locator('#playalone')).toHaveText('Connecting...');
  await expect(ann.locator('#t6install')).toBeHidden();
  expect((await launcher('/launcher/files', { doing: 'Updating Plutonium' })).status).toBe(200);
  await expect(ann.locator('#playalone')).toHaveText('Updating Plutonium...');
  expect((await launcher('/launcher/files', { running: true, got: 50, need: 100 })).status).toBe(200);
  await expect(ann.locator('#playalone')).toHaveText('Downloading · 50%');
  expect((await launcher('/launcher/files', { doing: 'Launching the game' })).status).toBe(200);
  expect((await ready()).status).toBe(200);
  await expect(ann.locator('#playalone')).toHaveText('Launching the game...');
  expect((await launcher('/launcher/files', {})).status).toBe(200);
  await expect(ann.locator('#playalone')).toHaveText('Launch game');
  await expect(ann.locator('#playalone')).not.toHaveAttribute('aria-disabled', 'true');
  // a try that failed: the page says why, and the button tries again
  expect((await launcher('/launcher/files', { failed: "Couldn't find Black Ops II on this PC." })).status).toBe(200);
  await expect(ann.locator('#solonote')).toHaveText("Couldn't find Black Ops II on this PC.");
  await expect(ann.locator('#playalone')).toHaveText('Try again');
  expect((await launcher('/launcher/files', {})).status).toBe(200);
  await expect(ann.locator('#solonote')).toBeHidden();
  expect((await launcher('/launcher/stopped')).status).toBe(200);
  await expect(ann.locator('#playalone')).toHaveText('Launch game');
  // the browser keeps the pick, which is the game of the rooms too: the room she makes next is of Black Ops II, and a
  // pick for a room is Play offline's
  await ann.reload();
  await expect(ann.locator('#solotitle')).toHaveText('Black Ops II');
  await ann.locator('#soloback').click();
  await ann.locator('#make').click();
  await expect(ann.locator('#mapkicker')).toHaveText('Black Ops II');
  await ann.locator('#titlepick').getByRole('radio', { name: 'Black Ops', exact: true }).click();
  await expect(ann.locator('#mapkicker')).toHaveText('Black Ops');
  await ann.locator('#leave').click();
  await ann.locator('#soloopen').click();
  await expect(titles.getByRole('radio', { name: 'Black Ops', exact: true })).toHaveAttribute('aria-checked', 'true');
  await titles.getByRole('radio', { name: 'Black Ops II', exact: true }).click();
  await expect(ann.locator('#solotitle')).toHaveText('Black Ops II');
  // and the leaderboards' game: they open on Black Ops II, and a pick there is Play offline's
  await ann.locator('#boardsopen').click();
  const boardTitles = ann.locator('#boardtitles');
  await expect(boardTitles.getByRole('radio', { name: 'Black Ops II', exact: true }))
    .toHaveAttribute('aria-checked', 'true');
  await boardTitles.getByRole('radio', { name: 'Black Ops', exact: true }).click();
  await ann.locator('#boardsopen').click();
  await expect(titles.getByRole('radio', { name: 'Black Ops', exact: true })).toHaveAttribute('aria-checked', 'true');
  await titles.getByRole('radio', { name: 'Black Ops II', exact: true }).click();
  await expect(ann.locator('#solotitle')).toHaveText('Black Ops II');

  // a page that can't reach its server still offers it: the button says that the game starts for a moment, as the
  // server can't say what the launcher does
  await ann.route('**/lobby/**', (route) => route.abort());
  await ann.reload();
  await expect(ann.locator('#rooms')).toContainText('No connection to the server.', { timeout: 20000 });
  await expect(ann.locator('#solotitle')).toHaveText('Black Ops II');
  await expect(ann.locator('#playalone')).toHaveText('Launch game');
  await ann.locator('#playalone').click();
  await expect(ann.locator('#playalone')).toHaveText('Starting...');
  await expect(ann.locator('#playalone')).toHaveText('Launch game', { timeout: 10000 });
  await expect(ann.locator('#t6install')).toBeHidden();
  // so it does in a browser that has never seen a launcher run, as another browser on the PC may have
  const bea = await player('Bea');
  await bea.locator('#soloopen').click();
  await bea.locator('#solotitles').getByRole('radio', { name: 'Black Ops II', exact: true }).click();
  await expect(bea.locator('#t6get')).toHaveText('First time? Install Zombies Launcher.');
  await bea.route('**/lobby/**', (route) => route.abort());
  await bea.reload();
  await expect(bea.locator('#rooms')).toContainText('No connection to the server.', { timeout: 20000 });
  await expect(bea.locator('#solotitle')).toHaveText('Black Ops II');
  await expect(bea.locator('#playalone')).toHaveText('Launch game');
});

test("a room's game counts as its players' pick: for their next room, the leaderboards and Play offline", async ({ player }) => {
  const ann = await player('Ann');
  const bea = await player('Bea');
  const code = await makeRoom(ann);
  await ann.locator('#titlepick').getByRole('radio', { name: 'Black Ops II', exact: true }).click();
  await expect(ann.locator('#mapkicker')).toHaveText('Black Ops II');
  // Bea, who has picked no game, joins the room of Black Ops II
  await joinRoom(bea, code);
  await expect(bea.locator('#mapkicker')).toHaveText('Black Ops II');
  await bea.locator('#leave').click();
  await bea.locator('#soloopen').click();
  await expect(bea.locator('#solotitles').getByRole('radio', { name: 'Black Ops II', exact: true }))
    .toHaveAttribute('aria-checked', 'true');
  await bea.locator('#boardsopen').click();
  await expect(bea.locator('#boardtitles').getByRole('radio', { name: 'Black Ops II', exact: true }))
    .toHaveAttribute('aria-checked', 'true');
  await bea.locator('#boardsopen').click();
  await bea.locator('#soloback').click();
  await bea.locator('#make').click();
  await expect(bea.locator('#mapkicker')).toHaveText('Black Ops II');
});

test("a room's host picks its mode, Grief or Turned: the grid has the maps of it, a room of Grief takes 8, and its " +
  'players are in teams', async ({ player }) => {
  const ann = await player('Ann');
  const bob = await player('Bob');
  const code = await makeRoom(ann);
  // Black Ops has co-op alone: no mode to pick
  await expect(ann.locator('#versussetting')).toBeHidden();
  await ann.locator('#titlepick').getByRole('radio', { name: 'Black Ops II', exact: true }).click();
  await expect(ann.locator('#maptitle')).toHaveText('TranZit');
  const versus = ann.locator('#versussetting .seg');
  await expect(versus.getByRole('radio')).toHaveText(['Co-op', 'Grief', 'Turned']);
  await expect(versus.getByRole('radio', { name: 'Co-op' })).toHaveAttribute('aria-checked', 'true');
  await versus.getByRole('radio', { name: 'Grief' }).click();
  await expect(ann.locator('#maptitle')).toHaveText('Bus Depot');
  await expect(ann.locator('#mapshot .words span')).toHaveText('Bus Depot / Grief');
  await expect(ann.locator('#mapgrid').getByRole('radio')).toHaveText(
    ['Bus Depot', 'Town', 'Farm', 'Cell Block', 'Borough']);
  // a room of Grief takes up to 8, four in each team, on a server whose rooms take 4
  await ann.locator('#sizesetting .seg button[data-value="8"]').click();
  await expect(ann.locator('#count')).toHaveText('1 / 8');
  await joinRoom(bob, code);
  // the host's to pick: the others see it, and what it is
  await expect(bob.locator('#versussetting .seg')).toBeHidden();
  await expect(bob.locator('#versustext')).toHaveText('Grief');
  await expect(bob.locator('#versusdesc')).toHaveText(/^Two teams\./);
  await expect(bob.locator('#mapshot .words span')).toHaveText('Bus Depot / Grief');
  await expect(ann.locator('#members li .what')).toHaveText(['Team 1 · Host', 'Team 2']);
  // Turned: its maps, and the room takes 4 again
  await versus.getByRole('radio', { name: 'Turned' }).click();
  await expect(ann.locator('#maptitle')).toHaveText('Diner');
  await expect(ann.locator('#mapgrid').getByRole('radio')).toHaveText(['Diner', 'Borough']);
  await expect(ann.locator('#count')).toHaveText('2 / 4');
  await expect(ann.locator('#members li .what')).toHaveText(['Host']);
  // back in co-op, the map that the room had of it
  await versus.getByRole('radio', { name: 'Co-op' }).click();
  await expect(ann.locator('#maptitle')).toHaveText('TranZit');
  await expect(ann.locator('#mapgrid').getByRole('radio', { name: 'Town', exact: true })).toHaveCount(1);
});

/**
 * Makes a Black Ops II room on Origins as Ann, the map of a map pack. Returns what her launcher says to the server as
 * it runs with her PC ready: which of the maps' zones her game folder has (internal/launcherapi: has).
 */
async function onOrigins(server: { url: string }, ann: Page): Promise<(has: string) => Promise<number>> {
  await ann.locator('#make').click();
  await ann.locator('#titlepick').getByRole('radio', { name: 'Black Ops II', exact: true }).click();
  await ann.locator('#mapgrid').getByRole('radio', { name: 'Origins', exact: true }).click();
  await expect(ann.locator('#maptitle')).toHaveText('Origins');
  await expect(ann.locator('#playbat')).toHaveText(/ [0-9a-f]{16}$/);
  const key = ((await ann.locator('#playbat').textContent()) || '').split(' ').pop();
  return async (has) => (await fetch(server.url + '/launcher/play?ready=1&has=' + encodeURIComponent(has), {
    headers: { Authorization: 'Key ' + key, 'Zombies-Launcher-Version': '1' },
  })).status;
}

test("a player whose copy of Black Ops II lacks the room's map is not ready until the room's map is one they have",
  async ({ server, player }) => {
    const ann = await player('Ann');
    const says = await onOrigins(server, ann);
    // a copy without the pack of Origins
    expect(await says('zone/all/zm_transit.ff')).toBe(200);
    await expect(ann.locator('#members li.mine .badge')).toHaveText('Missing map');
    await expect(ann.locator('#ready')).toHaveText('Missing map');
    await expect(ann.locator('#ready')).toHaveAttribute('aria-disabled', 'true');
    await expect(ann.locator('#roomnote')).toHaveText("Your Black Ops II doesn't have this map: it comes with a map pack.");
    // the host picks a map that the copy has
    await ann.locator('#mapgrid').getByRole('radio', { name: 'Town', exact: true }).click();
    await expect(ann.locator('#maptitle')).toHaveText('Town');
    expect(await says('zone/all/zm_transit.ff')).toBe(200);
    await expect(ann.locator('#members li.mine .badge')).toHaveText('Not ready');
    await expect(ann.locator('#ready')).toHaveText('Need 1 more');
    await expect(ann.locator('#roomnote')).toBeHidden();
  });

test.describe('a server that sends game files', () => {
  test.use({ serverArgs: ['-download-rate', 'unlimited'] });

  test("offers them to a player whose copy of Black Ops II lacks the room's map", async ({ server, player }) => {
    const ann = await player('Ann');
    const says = await onOrigins(server, ann);
    expect(await says('zone/all/zm_transit.ff')).toBe(200);
    await expect(ann.locator('#members li.mine .badge')).toHaveText('Missing map');
    // the button opens the launcher, which asks before it downloads
    await expect(ann.locator('#ready')).toHaveText('Download');
    await expect(ann.locator('#ready')).toHaveAttribute('data-does', 'launch');
    await expect(ann.locator('#roomnote')).toHaveText("Your Black Ops II doesn't have this map. The launcher can " +
      'download the game files from the server instead.');
    // her launcher has the server's game files, which have the map
    expect(await says('zone/all/zm_tomb.ff,zone/all/zm_transit.ff')).toBe(200);
    await expect(ann.locator('#members li.mine .badge')).toHaveText('Not ready');
    await expect(ann.locator('#ready')).toHaveText('Need 1 more');
  });
});
