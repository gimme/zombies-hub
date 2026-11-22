// A page in the background (another tab in front of it) has no animation frames, which draw the game: the page wakes
// the engine itself meanwhile, so that it goes on, and the game, which lets a character go after 5 minutes without a
// word, still hears from it. Chrome here throttles a page in the background, as a player's does. A second tab of the
// player's (their browser's: the same player) shows the room, and leaves the game to the first, which plays on; Play
// here moves the game to it, and the first steps back to the room, staying. Once the second closes, the page opened
// again offers to rejoin the game rather than join it, and the first rejoins the game, to the character. Cy stands
// idle all along, over the 2 minutes the game's config would kick them after (g_inactivity), which a room's game
// doesn't. Then an error that unloads the map (error 1: Com_Error ERR_DROP) ends the game, which tells its player
// first: their page goes back to the room.
import { expect, test } from '../fixtures';
import { inGame, loadingMovie } from '../play';

const sleep = (ms: number) => new Promise((done) => setTimeout(done, ms));

test('a page behind another tab plays on, a second tab takes the game, and an error ends it', async ({ server,
  players }) => {
  const cy = await players.open('Cy', { query: 'autostart=1', throttled: true });
  cy.background(() => loadingMovie(cy, 'room'));
  const room = await server.poll("Cy's room is in the log", 60_000, async () => server.roomOf('Cy'));
  // no zombies, which would down Cy's character as no tab of theirs plays it (the game moving between tabs)
  await server.waitFor(new RegExp(`^\\[${room}\\] ------- Game Initialization -------$`), 300_000);
  await server.noZombies(room);
  await inGame(server, room, 'Cy');
  await cy.spawned();

  // another tab in front: the page is hidden, and has no animation frames
  await cy.page.evaluate(() => {
    window.bo1AnimationFrames = 0;
    const count = () => {
      window.bo1AnimationFrames++;
      requestAnimationFrame(count);
    };
    requestAnimationFrame(count);
  });
  const uncover = await cy.cover();
  await sleep(1000);
  expect(await cy.page.evaluate(() => document.hidden), 'the page is hidden behind another tab').toBe(true);
  const frames = () => cy.page.evaluate(() => window.bo1AnimationFrames);
  const before = await frames();
  await sleep(10_000);
  expect(await server.inGame(room, 'Cy'), 'the game hears from the page, hidden for 10 s').toBe(true);
  await sleep(10_000);
  expect(await frames(), 'animation frames of the hidden page').toBe(before);
  const hidden = await cy.ask();
  expect(hidden, 'the engine answers, its page hidden for 20 s').toBeTruthy();
  cy.note(`the engine went on, hidden for 20 s with no animation frames: at ${hidden!.origin.join(' ')}`);
  await uncover();
  await sleep(1000);
  expect(await cy.page.evaluate(() => document.hidden), 'the page hidden once in front again').toBe(false);

  // a second tab, at the room's url, which the page keeps (?room=)
  const second = await cy.context.newPage();
  cy.watch(second, 'tab 2: ');
  await second.goto(cy.page.url());
  await expect(second.locator('#roomstate'), 'the second tab leaves the game to the first').toHaveText(
    'Your game is open in another tab.', { timeout: 30_000 });
  await sleep(5000);
  expect(await cy.ask(), 'the first tab plays on, a second one open').toBeTruthy();
  await cy.shot('second-tab');
  const marks = cy.marks();
  const stepsBack = cy.next(/^lobby: back in the lobby, the game's engine stopped$/, 30_000);
  await second.click('#ready[data-does="here"]');
  expect(await stepsBack, 'the first tab stepped back to the room as the second played').toBeTruthy();
  await cy.inPlace('the game went to the second tab', marks);
  const there = await cy.spawned(second);
  cy.note(`the second tab plays, at ${there.origin.join(' ')}`);
  await second.close();

  // the player opens the page again: it offers to rejoin the game rather than join it (the room's button: its own
  // Rejoin, or Play here's, until the lobby has heard of the tab)
  const reopened = await cy.context.newPage();
  cy.watch(reopened, 'tab 3: ');
  const from = cy.mark();
  await reopened.goto(cy.page.url());
  await reopened.waitForFunction(() => {
    const button = document.getElementById('ready')!;
    return ['rejoin', 'here'].includes(button.dataset.does || '') && button.textContent === 'Rejoin';
  }, null, { timeout: 30_000 });
  await sleep(3000);
  expect(cy.find(/^tab 3: lobby: joining room/, from), 'the page opened again joined the game by itself')
    .toBeUndefined();
  await expect(reopened.locator('#ready')).toHaveText('Rejoin');
  await reopened.close();
  await cy.page.bringToFront();
  // once no tab has had the game for a while, the first tells the lobby, and the room offers to rejoin it
  await cy.page.click('#ready[data-does="rejoin"]', { timeout: 30_000 });
  const back = await cy.spawned();
  cy.note(`the first tab plays again, at ${back.origin.join(' ')}`);
  expect(server.find(new RegExp(`^server: Cy's game in room ${room} ended \\(closed\\): `)),
    "the lobby heard that Cy's second tab closed").toBeTruthy();
  expect(server.find(/GAME_DROPPEDFORINACTIVITY$/), 'the game kicked a player for standing idle').toBeUndefined();
  expect(await server.inGame(room, 'Cy'), 'the game has Cy, back in the first tab').toBe(true);

  const end = server.mark();
  await server.console(`${room} error 1`);
  await server.waitFor(new RegExp(`^server: room ${room}'s game ended: exit code 2: `), 30_000, end);
  await cy.waitFor(/^lobby: the game ended \([a-z]+\): .*Testing drop error/, 30_000);
});
