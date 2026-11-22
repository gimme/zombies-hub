// A server sends the game files at most at its rate (ZOMBIES_DOWNLOAD_RATE), which the downloads at once share: capped
// at 160 Mbit/s, it sends two browsers' downloads of the map at once at about 80 each. Then one of them downloads
// another map, whose button offers and counts only what the store lacks of it: less than its list, which shares
// pieces with the first map's.
import { expect, map, test } from '../fixtures';

test.use({ serverOptions: { env: { ZOMBIES_DOWNLOAD_RATE: '160M' } } });

test('downloads at once share the server\'s rate, and a map downloads only what the store lacks', async ({ server,
  players }) => {
  expect(server.find(/^server: browsers download the game files of the maps they play, at up to 160M a second in all/),
    'the server says its rate').toBeTruthy();
  const [hal, ida] = await Promise.all([players.open('Hal'), players.open('Ida')]);
  await Promise.all([hal.getTheMap('#playalone'), ida.getTheMap('#playalone')]);
  const rate = async (name: string) => +/\(([0-9]+) Mbit\/s\)$/.exec(await server.waitFor(
    new RegExp(`^files: ${name} downloaded ${map}: .* \\([0-9]+ Mbit/s\\)$`), 10_000))![1];
  const [halRate, idaRate] = [await rate('Hal'), await rate('Ida')];
  hal.note(`two downloads at once from a server capped at 160 Mbit/s went at ${halRate} and ${idaRate} Mbit/s`);
  for (const r of [halRate, idaRate]) {
    expect(r, 'Mbit/s of one of two downloads at once').toBeGreaterThanOrEqual(56);
    expect(r, 'Mbit/s of one of two downloads at once').toBeLessThanOrEqual(104);
  }
  expect(halRate + idaRate, 'Mbit/s of the two').toBeGreaterThanOrEqual(112);
  expect(halRate + idaRate, 'Mbit/s of the two').toBeLessThanOrEqual(176);

  const next = 'zombie_pentagon';
  await hal.page.click(`#solomaps button:has(img[src="/maps/${next}/preview"])`);
  const { counted, offered } = await hal.getTheMap('#playalone');
  const reads = +/reads ([0-9]+) MB/.exec(await server.waitFor(new RegExp(`^files: ${next} reads [0-9]+ ` +
    `MB`), 1000))![1];
  hal.note(`after ${map}, ${next} needed ${offered} MB more of its ${reads}, its button counting ${counted}`);
  expect(offered, `MB ${next}'s button offered`).toBeGreaterThan(0);
  expect(counted, `MB ${next}'s button counted to`).toBe(offered);
  expect(offered, `MB ${next} needed after ${map}`).toBeLessThan(reads);
});
