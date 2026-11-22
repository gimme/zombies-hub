// A room's game that hangs (freeze: its main thread loops) ends, with a report of where each thread was: the server
// takes a game that runs no frame for 20 s as hung (ZOMBIES_HANG_TIMEOUT; a minute unless it says).
import { expect, map, test } from '../fixtures';

test.use({ serverOptions: { env: { ZOMBIES_HANG_TIMEOUT: '20s' } } });

test("a room's game that hangs ends, saying where its threads were", async ({ server, api }) => {
  const { room } = await api.room('Hang');
  await server.poll(`room ${room}'s game runs`, 60_000, async () =>
    (await server.console(`${room} status`)).includes(`[${room}] map: ${map}`));
  const from = server.mark();
  await server.console(`${room} freeze 600`);
  await server.waitFor(new RegExp(`^server: room ${room}'s game ended: exit code 4: hung$`), 90_000, from);
  expect(server.find(new RegExp(`^\\[${room}\\] BO1_HEADLESS THREAD `), from), 'where the threads were').toBeTruthy();
});
