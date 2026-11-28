// The leaderboards: each game's own, with its kinds of board and its maps; a game's players on the board of all the
// maps and on the map's own, numbered alike where level, a row's report, and the button that opens and closes them; a
// game that no one finished; and a player who leaves the end scoreboard while the game runs on for the others.
import { expect, joinRoom, makeRoom, playRoomGame, said, test } from '../fixtures';

declare global {
  // eslint-disable-next-line no-var
  var standin: { leave(): void; over(round?: number): void };
}

test('the leaderboards are by game: first the game, then its kinds of board, then its maps', async ({ player }) => {
  const kim = await player('Kim');
  await kim.locator('#boardsopen').click();
  const picked = (box: string) => kim.locator(`#${box} button[aria-checked=true]`);
  await expect(kim.locator('#boardtitles button')).toHaveText(['Black Ops', 'Black Ops II']);
  await expect(picked('boardtitles')).toHaveText('Black Ops');
  await expect(kim.locator('#boardtabs button')).toHaveText(['Rounds', 'Easter eggs']);
  await expect(kim.locator('#boardpicks button')).toHaveCount(11); // All maps, and each of the 10
  await kim.locator('#boardtabs').getByRole('radio', { name: 'Easter eggs' }).click();
  await expect(kim.locator('#boardpicks button')).toHaveText(['All eggs', 'Ascension', 'Call of the Dead',
    'Shangri-La', 'Moon']);

  // Black Ops II, at the kind picked: the easter eggs of the maps that have a main quest, then its rounds of its maps
  await kim.locator('#boardtitles').getByRole('radio', { name: 'Black Ops II' }).click();
  await expect(kim.locator('#boardtabs button')).toHaveText(['Rounds', 'Easter eggs']);
  await expect(picked('boardtabs')).toHaveText('Easter eggs');
  await expect(kim.locator('#boardpicks button')).toHaveText(['All eggs', 'TranZit', 'Die Rise', 'Mob of the Dead',
    'Buried', 'Origins']);
  await kim.locator('#boardtabs').getByRole('radio', { name: 'Rounds' }).click();
  await expect(picked('boardpicks')).toHaveText('All maps');
  await expect(kim.locator('#boardpicks button')).toHaveCount(10);
  await expect(kim.locator('#boardtables .tile .name').first()).toHaveText('TranZit');
  // back to Black Ops, at the kind picked last
  await kim.locator('#boardtitles').getByRole('radio', { name: 'Black Ops', exact: true }).click();
  await expect(picked('boardtabs')).toHaveText('Rounds');
});

test("the leaderboards list a game's players, and open its report", async ({ server, player }) => {
  const lea = await player('Lea');
  const ola = await player('Ola');
  const code = await makeRoom(lea);
  await joinRoom(ola, code);
  await playRoomGame(lea, ola);
  server.command(`${code} over 5`);
  await expect(lea.locator('#results')).toBeVisible();
  // back in the room, its host picks another map, which is behind the page from then on
  const backdrop = lea.locator('#backdropimg');
  await expect(backdrop).toHaveAttribute('src', '/maps/zombie_theater/loadscreen');
  await lea.locator('#resultsback').click();
  await lea.locator('#mapgrid').getByRole('radio', { name: 'Ascension', exact: true }).click();
  await expect(backdrop).toHaveAttribute('src', '/maps/zombie_cosmodrome/loadscreen');

  await lea.locator('#boardsopen').click();
  // first the board of all the maps: its table lists the player, and their card has their round on the room's map
  await expect(lea.locator('#boardtables.all tr.mine')).toBeVisible();
  const card = lea.locator('#boardtables .tile:has(img[src="/maps/zombie_theater/preview"]) .score');
  await expect(card).toContainText('5');
  // then the map's own: players level with its best share its first place
  await lea.locator('#boardpicks').getByRole('radio', { name: 'Kino der Toten', exact: true }).click();
  await expect(lea.locator('#boardtables:not(.all) tr.mine')).toBeVisible();
  const rows = lea.locator('#boardtables:not(.all) tbody tr');
  await expect(rows).toHaveCount(2);
  for (const row of await rows.all()) await expect(row.locator('td').first()).toHaveText('1');

  // the row opens the game's report, in the room's results' place, and goes back to the boards. The report has its
  // game's map behind it for as long as it shows: the page's own backdrop is the same after it.
  await expect(backdrop).toHaveAttribute('src', '/maps/zombie_cosmodrome/loadscreen');
  await lea.locator('#boardtables tr.mine .rowlink').click();
  await expect(lea.locator('#results')).toBeVisible();
  await expect(lea.locator('#resultsback')).toHaveText('Back to the leaderboards');
  await expect(lea.locator('#resulttitle')).toHaveText('Round 5');
  await expect(backdrop).toHaveAttribute('src', '/maps/zombie_theater/loadscreen');
  await lea.locator('#resultsback').click();
  await expect(lea.locator('#boards')).toBeVisible();
  await expect(backdrop).toHaveAttribute('src', '/maps/zombie_cosmodrome/loadscreen');
  await lea.locator('#boardtabs button:nth-child(2)').click();
  await expect(lea.locator('#boardtabs button:nth-child(2)')).toHaveAttribute('aria-checked', 'true');

  // the button at the top stays pressed while they show, and closes them; then their Back does
  await expect(lea.locator('#boardsopen')).toHaveAttribute('aria-pressed', 'true');
  await lea.locator('#boardsopen').click();
  await expect(lea.locator('#boards')).toBeHidden();
  await expect(lea.locator('#boardsopen')).toHaveAttribute('aria-pressed', 'false');
  await lea.locator('#boardsopen').click();
  await expect(lea.locator('#boards')).toBeVisible();
  await lea.locator('#boardsback').click();
  await expect(lea.locator('#boards')).toBeHidden();
});

test('a game that no one finished is on the leaderboards with its report', async ({ server, player }) => {
  const una = await player('Una');
  const uri = await player('Uri');
  const code = await makeRoom(una);
  await joinRoom(uri, code);
  await playRoomGame(una, uri);
  server.command(`${code} round 1`);
  await server.waitFor(/^server: Una's best on zombie_theater with a party of 2: round 1$/);
  await server.waitFor(/^server: Uri's best on zombie_theater with a party of 2: round 1$/);
  server.command(`${code} quit`);
  await expect(una.locator('#note')).toContainText('The game ended');

  await una.locator('#boardsopen').click();
  await una.locator('#boardtables button.tile:has(img[src="/maps/zombie_theater/preview"])').click();
  await expect(una.locator('#results')).toBeVisible();
  // the report of the game as its last round began
  await expect(una.locator('#resultkicker')).toContainText('Kino der Toten');
  await expect(una.locator('#resultrows tr.mine')).toContainText('Una');
});

test('a player who leaves the end scoreboard sees the report while the game runs on for the others', async (
  { server, player }) => {
  const gil = await player('Gil');
  const gus = await player('Gus');
  const code = await makeRoom(gil);
  await joinRoom(gus, code);
  await playRoomGame(gil, gus);
  server.command(`${code} over 4 6`); // the end scoreboard shows for 6 s
  await server.waitFor(new RegExp(`^server: room ${code}'s game is over$`));

  await gil.evaluate(() => globalThis.standin.over());
  await expect(gil.locator('#results')).toBeVisible();
  await expect(gil.locator('#roomgame')).toHaveText('Ending');
  // Gus's page stays in the game until the game ends
  await expect(gus.locator('#overlay')).toBeHidden();
  await expect.poll(() => said(gus).find((line) => line.startsWith('lobby: the game ended')), { timeout: 15_000 })
    .toBe('lobby: the game ended (over): Game over on round 4.');
  await expect(gus.locator('#results')).toBeVisible();
  await expect(gus.locator('#resulttitle')).toHaveText('Round 4');
});
