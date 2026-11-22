// A client of another build doesn't join a room's game. A browser that says it is another build makes a room of its
// own and starts its game; it stops at the game's answer to its getchallenge, which says the server's build, and its
// page says which builds they are; its game is called off. A client that sends its connect anyway, saying no build
// (as a client older than the check), is refused at that, and the game's log says so.
import { createSocket } from 'node:dgram';
import { expect, test } from '../fixtures';

// What the game of the player's room answers a client that sends its connect whatever the server's build:
// getchallenge, then connect with the challenge, saying the build (none: as a client older than the check), each
// answer a line. Over UDP to the server's port, as a Windows client, with the player's key.
function udpClient(port: number, key: string, build = ''): Promise<string[]> {
  return new Promise((done, failed) => {
    const socket = createSocket('udp4');
    const answers: string[] = [];
    const send = (text: string) =>
      socket.send(Buffer.concat([Buffer.alloc(4, 255), Buffer.from(text, 'latin1')]), port, '127.0.0.1');
    const timer = setTimeout(() => {
      socket.close();
      failed(new Error(`the game answered ${answers.length ? answers.join(' | ') : 'nothing'} in 5 s`));
    }, 5000);
    socket.on('message', (packet) => {
      const text = packet.subarray(4).toString('latin1');
      answers.push(text.replace(/\n/g, ' '));
      const challenge = /^challengeResponse (-?\d+)/.exec(text);
      if (!challenge) {
        clearTimeout(timer);
        socket.close();
        done(answers);
        return;
      }
      send(`connect "\\protocol\\1044\\challenge\\${challenge[1]}\\qport\\4321${build ? `\\build\\${build}` : ''}"`);
    });
    send(`getchallenge 0 "" "0" "${key}"`);
  });
}

test('a client of another build is refused', async ({ server, players, api }) => {
  const other = await players.open('Other', { query: 'autostart=1', args: '+set bo1_build other', errors: true });
  const room = await server.poll("Other's room is in the log", 60_000, async () => server.roomOf('Other'));
  await server.waitFor(new RegExp(`^\\[${room}\\] ------- Game Initialization -------$`), 300_000);
  const said = await server.waitFor(new RegExp(`^\\[${room}\\] BO1 Zombies build `));
  const build = /BO1 Zombies build (.*)$/.exec(said)![1];
  other.note(`the game is build ${build}`);
  expect(server.find(new RegExp(`^\\[${room}\\] Opening IP socket: 127\\.0\\.0\\.1:30[0-9][0-9]$`)),
    'the game listens where only the server reaches it').toBeTruthy();
  await other.waitFor(new RegExp(`^page: error: The server runs build ${build} of the game, and this client build ` +
    'other\\. Reloading the page would not help'), 120_000);
  await server.waitFor(new RegExp(`^server: room ${room}'s game ends: Game called off: Other's game failed to ` +
    `load\\. ` +
    'The server runs build .* and this client build other\\.'), 30_000);

  const nobody = await api.room('Nobody');
  await server.waitFor(new RegExp(`^\\[${nobody.room}\\] ------- Game Initialization -------$`), 120_000);
  const from = server.mark();
  const answers = await udpClient(server.port, nobody.player.key);
  expect(answers[0].split(' ')[2], "the game's answer to getchallenge says its build").toBe(build);
  expect(answers.join('\n')).toContain(`The server runs build ${build} of the game, and this client an older one: ` +
    "join it with the server's build");
  await server.waitFor(new RegExp(`^\\[${nobody.room}\\] Refused [0-9.:-]+: a client of build \\(none: older\\), and ` +
    'the server is build '), 5000, from);
});
