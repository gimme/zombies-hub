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
// that Steam's server would otherwise fetch from the game's publisher as the test's server starts.
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

function write(file: string, text: string): void {
  mkdirSync(dirname(file), { recursive: true });
  writeFileSync(file, text);
}

export default async function globalSetup(): Promise<() => Promise<void>> {
  const dir = mkdtempSync(join(tmpdir(), 'zombies-e2e-'));
  for (const file of gameFiles) write(join(dir, 'game', file), '');
  for (const file of gameFiles.filter((f) => f.startsWith('t5/'))) write(join(dir, 'game-t5', file), '');
  for (const file of gameFiles) write(join(dir, 'game-t7', file), '');
  for (const [file, size] of Object.entries(t7Files)) write(join(dir, 'game-t7', file), 'x'.repeat(size));

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
