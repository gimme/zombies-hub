// e2e/global-setup.ts - what every test's server shares, made once for the run in a temporary folder: the game files,
// the engine, and a stand-in for Plutonium's updater. fixtures.ts reads where they are from the environment. The game
// files are in game, with Black Ops and Black Ops II, in game-t5 with Black Ops alone, for a server of one game, and
// in game-t7 with Black Ops III too.
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { deflateSync } from 'node:zlib';

// The maps' fastfiles, each empty. The server lists the maps it has by them (server/t5.go: installedMaps,
// server/t6.go: t6Installed). Black Ops II also needs two files that every map loads.
const gameFiles = [
  ...['zombie_theater', 'zombie_pentagon', 'zombie_cosmodrome', 'zombie_coast', 'zombie_temple', 'zombie_moon',
    'zombie_cod5_prototype', 'zombie_cod5_asylum', 'zombie_cod5_sumpf', 'zombie_cod5_factory']
    .map((map) => `t5/zone/common/${map}.ff`),
  ...['patch_zm', 'zm_transit', 'zm_transit_dr', 'zm_nuked', 'zm_highrise', 'zm_prison', 'zm_buried', 'zm_tomb']
    .map((zone) => `t6/zone/all/${zone}.ff`),
  't6/binkw32.dll',
];

// Black Ops III, with two maps: the install, and Steam's dedicated server of it, which a server needs to host the
// game (server/t7.go: t7Installed). Each file has the size here, in bytes: the server sends the install's files to a
// launcher in packs, whose sizes the page shows (server/gamefiles.go: t7Sends). The server's folder has the file
// that Steam's server would otherwise fetch from the game's publisher as the test's server starts. The install also
// has the maps' pictures: two image packs with their loading screens, and a fastfile with their previews (t7Packs).
const t7Files: Record<string, number> = {
  't7/BlackOps3.exe': 0, 't7/zone/zm_common.ff': 0, 't7/zone/base.xpak': 3_000_000,
  't7/zone/zm_factory.ff': 0, 't7/zone/zm_factory.xpak': 1_000_000,
  't7/zone/zm_tomb.ff': 0, 't7/zone/zm_tomb.xpak': 2_000_000,
  't7-server/UnrankedServer/BlackOps3_UnrankedDedicatedServer.exe': 0,
  't7-server/UnrankedServer/identities/dedicatedpc/LPC/core_sv_ffotd_tu32_593.ff': 0,
};

// What the updater serves of Plutonium: what a T6 game needs of it (internal/plutonium: T6).
const plutonium: Record<string, string> = {
  'bin/plutonium-bootstrapper-win32.exe': 'the bootstrapper',
  'games/t6zm.exe': "T6's Zombies",
  'storage/t6/zone/zm_patch.ff': 'a zone',
  'storage/demonware/zm/playlists': 'playlists',
};

// A BC7 texture of 16x16 texels in one colour, whose red, green and blue have 7 bits each: 16 blocks of the mode
// with one subset (a 1 at bit 6). A block's two endpoints are the colour, opaque, each with a lowest bit of 1, and its
// texels are all at the first (server/bc7.go).
function bc7(r: number, g: number, b: number): Buffer {
  let bits = (1n << 6n) | (3n << 63n);
  [r, r, g, g, b, b, 127, 127].forEach((v, i) => { bits |= BigInt(v) << BigInt(7 + 7 * i); });
  const block = Buffer.alloc(16);
  block.writeBigUInt64LE(bits & 0xffffffffffffffffn, 0);
  block.writeBigUInt64LE(bits >> 64n, 8);
  return Buffer.concat(Array<Buffer>(16).fill(block));
}

// An image pack of Black Ops III (server/xpak.go): its header, the data of its items, the table of where they are,
// and the list of the images it names, each under a key. An item's data is one block: its header, with one command
// for the item's bytes as they are, and the bytes.
function xpak(listed: [key: number, text: string][], items: [key: number, data: Buffer][]): Buffer {
  const u64 = (...values: number[]): Buffer => {
    const b = Buffer.alloc(8 * values.length);
    values.forEach((v, i) => b.writeBigUInt64LE(BigInt(v), 8 * i));
    return b;
  };
  const blocks: Buffer[] = [], table: Buffer[] = [];
  let size = 0;
  for (const [key, bytes] of items) {
    const block = Buffer.alloc(128 + Math.ceil(bytes.length / 128) * 128);
    block.writeUInt32LE(1, 0);
    block.writeUInt32LE(bytes.length, 8);
    bytes.copy(block, 128);
    table.push(u64(key, size, block.length));
    blocks.push(block);
    size += block.length;
  }
  const list = Buffer.concat(listed.flatMap(([key, text]) => [u64(key, text.length), Buffer.from(text)]));
  const head = Buffer.alloc(256);
  head.write('KAPI');
  head.writeUInt16LE(10, 6);
  u64(1, 256 + size + 24 * items.length + list.length, items.length, 256, size, items.length, 256 + size,
    24 * items.length).copy(head, 8);
  u64(listed.length, 256 + size + 24 * items.length, list.length).copy(head, 96);
  return Buffer.concat([head, ...blocks, ...table, list]);
}

// A fastfile of Black Ops III whose zone has the images whole, each of 16x16 texels in BC7 (server/t7zone.go): the
// fastfile's header, and the zone in one block, compressed with zlib. An image there is its header, its name, and
// its pixels.
function fastfile(images: [name: string, pixels: Buffer][]): Buffer {
  const zone = Buffer.concat(images.flatMap(([name, pixels]) => {
    const head = Buffer.alloc(72);
    [16, 16, 16, 16].forEach((size, i) => head.writeUInt16LE(size, 2 * i));
    head.writeBigUInt64LE(BigInt(pixels.length), 40);
    head.writeBigUInt64LE(98n, 48);
    head.fill(0xff, 56, 64);
    return [head, Buffer.from(name + '\0'), pixels];
  }));
  const data = deflateSync(zone);
  const head = Buffer.alloc(584 + 16);
  head.write('TAff0000');
  head.writeUInt32LE(0x251, 8);
  head[13] = 1;
  head.writeBigUInt64LE(BigInt(zone.length), 144);
  [data.length, zone.length, data.length, 584].forEach((v, i) => head.writeUInt32LE(v, 584 + 4 * i));
  return Buffer.concat([head, data]);
}

// The pictures of Black Ops III's two maps (server/t7.go: t7MapList). The loading screens are in the game's image
// packs: the pack of the zone core_patch lists them, and a pack of the game's has them. The Giant's is red, and
// Origins' blue. The previews are in the zone of the game's menus: The Giant's is blue, and Origins' red.
const screens: [key: number, name: string, data: Buffer][] = [
  [1, 'img_loadscreen_zm_factory', bc7(127, 0, 0)], [2, 't7_menu_zm_loadscreen_tomb', bc7(0, 0, 127)]];
const t7Packs: Record<string, Buffer> = {
  't7/zone/core_patch.xpak': xpak(screens.map(([key, name]) => [key, ['width: 16', 'height: 16', 'levels: 1',
    'format: BC7', 'name: ' + name, 'type: image', 'offset0: 0', 'size0: 256'].join('\n')]), []),
  't7/zone/base_patch.xpak': xpak([], screens.map(([key, , data]) => [key, data])),
  't7/zone/core_frontend.ff': fastfile([['img_mapvote_zm_factory', bc7(0, 0, 127)],
    ['img_t7_menu_zm_preview_tomb', bc7(127, 0, 0)]]),
};

function write(file: string, text: string | Buffer): void {
  mkdirSync(dirname(file), { recursive: true });
  writeFileSync(file, text);
}

export default async function globalSetup(): Promise<() => Promise<void>> {
  const dir = mkdtempSync(join(tmpdir(), 'zombies-e2e-'));
  for (const file of gameFiles) write(join(dir, 'game', file), '');
  for (const file of gameFiles.filter((f) => f.startsWith('t5/'))) write(join(dir, 'game-t5', file), '');
  for (const file of gameFiles) write(join(dir, 'game-t7', file), '');
  for (const [file, size] of Object.entries(t7Files)) write(join(dir, 'game-t7', file), 'x'.repeat(size));
  for (const [file, pack] of Object.entries(t7Packs)) write(join(dir, 'game-t7', file), pack);

  // The engine is an archive with its version and BO1Zombies.exe (internal/bo1zombies). The stand-in game runs in
  // place of BO1Zombies.exe, so the file is empty.
  const engine = join(dir, 'engine');
  write(join(engine, 'engine.json'), JSON.stringify({ version: 'standin', build: 'standin' }));
  write(join(engine, 'bin', 'BO1Zombies.exe'), '');
  execFileSync('tar', ['-czf', join(dir, 'engine.tar.gz'), '-C', engine, 'engine.json', 'bin']);

  // Plutonium's updater: its manifest, and each file by the SHA-1 of its contents.
  const sha1 = (text: string) => createHash('sha1').update(text).digest('hex');
  const updater = createServer((req, res) => {
    const base = `http://127.0.0.1:${(updater.address() as AddressInfo).port}`;
    if (req.url === '/info.json') {
      res.end(JSON.stringify({
        revision: 1, baseUrl: base + '/files/',
        files: Object.entries(plutonium).map(([name, data]) => ({ name, size: data.length,
          hash: sha1(data).toUpperCase() })),
      }));
      return;
    }
    const data = Object.values(plutonium).find((d) => req.url === '/files/' + sha1(d));
    res.statusCode = data === undefined ? 404 : 200;
    res.end(data);
  });
  await new Promise<void>((resolve) => updater.listen(0, '127.0.0.1', resolve));

  process.env.ZOMBIES_E2E_FILES = dir;
  process.env.ZOMBIES_E2E_PLUTONIUM = `http://127.0.0.1:${(updater.address() as AddressInfo).port}/info.json`;
  return async () => {
    updater.close();
    rmSync(dir, { recursive: true, force: true });
  };
}
