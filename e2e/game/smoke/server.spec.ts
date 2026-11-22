// The server image, run as Unraid runs it: its games run as a user of their own, whom its firewall lets reach their
// players' seats and nothing else; a game still running as the server stops quits with it; and what the server wrote
// is PUID's and PGID's.
import { readdirSync } from 'node:fs';
import { join } from 'node:path';
import { docker, image, mount } from '../server';
import { expect, map, test } from '../fixtures';

test('the server keeps its games to themselves, and stops them as it stops', async ({ server, api }) => {
  expect(await server.console('rooms')).toMatch(/^server: no rooms$/m);
  expect(server.find(new RegExp(`^server: rooms of up to 4 players, 4 games at once; maps: (.*, )?${map}(,|$)`)),
    'the lobby offers the map').toBeTruthy();
  expect(server.find(/^server: the games run as user 3075, the server as 99$/),
    "the games run as a user of their own (entrypoint.sh)").toBeTruthy();

  // entrypoint.sh's firewall: the games' user sends to its players' seats (127.1.x.y:28960) and the lobby's watch of
  // the games (127.2.x.y:28960), and nowhere else, not to the internet nor the lobby, which the lobby's user reaches.
  // A connection the firewall drops waits, which the 2 s end.
  const sends = async (as: string, proto: string, address: string, port: number) =>
    (await server.exec(['timeout', '2', 'bash', '-c', `echo >/dev/${proto}/${address}/${port}`], as)).code === 0;
  expect(await sends('3075', 'udp', '127.1.0.1', 28960), "the games' user sends to a seat").toBe(true);
  expect(await sends('3075', 'udp', '127.2.12.4', 28960), "the games' user sends to the lobby's watch").toBe(true);
  expect(await sends('99', 'udp', '192.0.2.1', 9), "the lobby's user reaches the network").toBe(true);
  expect(await sends('99', 'tcp', '127.0.0.1', 3075), "the lobby's user reaches the lobby").toBe(true);
  expect(await sends('3075', 'udp', '192.0.2.1', 9), "the games' user reaches the network").toBe(false);
  expect(await sends('3075', 'tcp', '127.0.0.1', 3075), "the games' user reaches the lobby").toBe(false);
  expect(await sends('3075', 'udp', '127.0.0.1', 3075), "the games' user reaches the lobby's UDP port").toBe(false);

  // a room's game that waits for a player who never comes runs as the server stops, and quits with it
  const { room } = await api.room('Last');
  await server.poll(`room ${room}'s game runs`, 60_000, async () =>
    (await server.console(`${room} status`)).includes(`[${room}] map: ${map}`));
  const from = server.mark();
  await server.stop();
  expect(server.find(new RegExp(`^game ${room}: stopping$`), from), "the game quit as the server stopped").toBeTruthy();
  expect(readdirSync(join(server.dir, 'config', 'logs')).filter((f) => /^game-.*\.log$/.test(f)),
    'a log file of the game').toHaveLength(1);

  // What the server wrote is PUID:PGID's, as a container sees it, but for what the games wrote in their homes
  // (game/<n>/), which is their user's, 3075, with PGID. On Windows the engine the server unpacked is in a volume,
  // which the check mounts in its place.
  const engine = process.platform === 'win32' ? mount(`${server.name}-engine`, '/server/engine', true) : [];
  const other = await docker(['run', '--rm', '--entrypoint', 'find',
    ...mount(join(server.dir, 'config'), '/server', true), ...engine, image, '/server', '(', '!', '-group', '100',
    '-o', '!', '-user', '99', '!', '(', '-user', '3075', '-path', '/server/game/*/*', ')', ')',
    '-printf', 'server/%P (%U:%G)\n']);
  expect(other.out.split('\n').filter(Boolean).slice(0, 3), "what the server wrote that isn't PUID:PGID's").toEqual([]);
});
