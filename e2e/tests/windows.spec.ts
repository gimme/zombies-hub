// A Windows player: their client (Play.bat <host> <key>) joins their room's game through the server's UDP port with
// their key. It waits for them to join a room, then for the game to start; in the game, it quits it as its window
// closes, and the room's game runs on until its host ends it.
import { createSocket, type Socket } from 'node:dgram';
import { apiPlayer, expect, test } from '../fixtures';

test.use({ serverArgs: ['-min-players', '1'] });

/** A client of the game on Windows: what it sends the server's port, and what it hears back. */
class WindowsClient {
  private socket: Socket = createSocket('udp4');
  private heard: string[] = [];

  constructor(private port: number, private key: string) {
    this.socket.on('message', (packet) => {
      const text = packet.subarray(4).toString('latin1');
      this.heard.push(text);
      // the game's answer to its getchallenge: it connects, as the client does
      const challenge = /^challengeResponse (-?\d+)/.exec(text);
      if (challenge) this.send(`connect "\\protocol\\1044\\challenge\\${challenge[1]}\\qport\\4321\\name\\Client"`);
    });
  }

  send(text: string, outOfBand = true): void {
    const packet = Buffer.from(text, 'latin1');
    this.socket.send(outOfBand ? Buffer.concat([Buffer.alloc(4, 255), packet]) : packet, this.port, '127.0.0.1');
  }

  /** Asks to join, as the client does every 3 s while it waits, and returns the answer. */
  async ask(): Promise<string> {
    const from = this.heard.length;
    this.send(`getchallenge 0 "" "0" "${this.key}"`);
    await expect.poll(() => this.heard.length).toBeGreaterThan(from);
    return this.heard[from];
  }

  close(): void {
    this.socket.close();
  }
}

test('a Windows client waits for its room and its game, then joins it with the key', async ({ server }) => {
  const win = await apiPlayer(server, 'Win');
  const client = new WindowsClient(server.port, win.key);
  expect(await client.ask()).toBe("print\nWaiting for you to join a room on the server's page...");
  await win.say('create', { private: true });
  const code = (await server.waitFor(/^server: Win made room [A-Z]{4} /)).split(' ')[4];
  expect(await client.ask()).toBe('print\nWaiting for the game to start...');
  expect(await win.say('ready', { ready: true })).toBe('{}\n');

  // the game, once it is up: the client connects, under the name the server gave Win, and round 1 begins
  await server.waitFor(new RegExp(`^\\[${code}\\] stand-in: Black Ops on `));
  await expect.poll(() => client.ask().then(() => server.log()), { timeout: 15_000 })
    .toMatch(new RegExp(`^\\[${code}\\] stand-in: 127\\.1\\.\\S+ connected as Win$`, 'm'));
  await server.waitFor(new RegExp(`^\\[${code}\\] bo1_lobby: start$`));

  // its window closes: the client says disconnect, and the server ends its route, but the game runs on
  client.send('disconnect', false);
  await server.waitFor(new RegExp(`^\\[${code}\\] bo1_lobby: quit 127\\.1\\.`));
  await server.waitFor(new RegExp(`^relay: .* \\(Win, room ${code}\\) left \\(they quit the game\\)$`));
  await new Promise((resolve) => setTimeout(resolve, 3000));
  expect(server.log()).not.toMatch(new RegExp(`^server: room ${code}'s game ends: `, 'm'));
  // its host ends the game that no one plays
  expect(await win.say('end')).toBe('{}\n');
  await server.waitFor(new RegExp(`^server: room ${code}'s game ends: Win ended the game\\.$`));
  client.close();
  win.close();
});

test("a Windows client can't join a Black Ops II room's game", async ({ server }) => {
  const win = await apiPlayer(server, 'Win');
  const client = new WindowsClient(server.port, win.key);
  await win.say('create', { private: true, map: 'zm_standard_town' });
  const code = (await server.waitFor(/^server: Win made room [A-Z]{4} /)).split(' ')[4];
  expect(await win.say('ready', { ready: true })).toBe('{}\n');
  await server.waitFor(new RegExp(`^server: room ${code} starts a game `));
  await expect.poll(() => client.ask(), { timeout: 15_000 })
    .toBe(`error\nRoom ${code} plays Black Ops II. Join it with the line on its page.`);
  client.close();
  win.close();
});
