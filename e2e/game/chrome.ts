// e2e/game/chrome.ts - the Chrome the players play in: this PC's own, headless, drawing with its GPU as a player's
// does. ZOMBIES_CHROME is its executable, where it is not where Chrome installs.
//
// Each player's Chrome has a profile folder of its own under profiles, which is on the command line of each of its
// processes. A Chrome closed as a player closes theirs lets its GPU go in order; one ended from outside is cut off
// with its GPU work under way, for the GPU's driver to clear up after. On one machine the driver once stopped
// answering after dozens of those, and held the processes of each Chrome started after: nothing ends them but a
// restart, and the machine's other programs hang with them. So a player's Chrome is closed (player.ts: close), its
// processes are looked for after, and once some stay, the run notes it (stuckNote) and starts no Chrome after that.
import { execFileSync } from 'node:child_process';
import { closeSync, existsSync, openSync, readdirSync, readSync, statSync, unlinkSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

/** The players' profile folders, one each. */
export const profiles = join(tmpdir(), 'zombies-test-profiles');

/** The note that the GPU's driver holds a Chrome's processes. */
export const stuckNote = join(import.meta.dirname, '..', '..', 'build', 'test', 'gpu-stuck');

/** How Playwright starts this PC's Chrome. */
export function chrome(): { executablePath?: string; channel?: string } {
  return process.env.ZOMBIES_CHROME ? { executablePath: process.env.ZOMBIES_CHROME } : { channel: 'chrome' };
}

/** This PC's Chrome's executable, for a Chrome started by hand: ZOMBIES_CHROME's, or where Chrome installs. */
export function chromeExecutable(): string {
  const env = process.env;
  const places = process.platform === 'win32'
    ? [env.PROGRAMFILES, env['PROGRAMFILES(X86)'], env.LOCALAPPDATA].filter((d): d is string => !!d)
      .map((d) => join(d, 'Google', 'Chrome', 'Application', 'chrome.exe'))
    : process.platform === 'darwin' ? ['/Applications/Google Chrome.app/Contents/MacOS/Google Chrome']
      : ['/opt/google/chrome/chrome', '/usr/bin/google-chrome', '/usr/bin/google-chrome-stable'];
  const found = [env.ZOMBIES_CHROME, ...places].find((p): p is string => !!p && existsSync(p));
  if (!found) throw new Error(`no Chrome installed (looked in ${places.join(', ')}): ZOMBIES_CHROME is its executable`);
  return found;
}

/** How many Chrome processes have the folder on their command line. */
export function chromeLeft(folder: string): number {
  if (process.platform === 'win32') {
    const like = `*${folder}*`.replace(/'/g, "''");
    const out = execFileSync('powershell', ['-NoProfile', '-Command',
      `@(Get-CimInstance Win32_Process -Filter "Name='chrome.exe'" | ` +
      `Where-Object { $_.CommandLine -like '${like}' }).Count`], { encoding: 'utf8', windowsHide: true });
    return parseInt(out.trim(), 10) || 0;
  }
  try {
    return parseInt(execFileSync('pgrep', ['-fc', '--', escape(folder)], { encoding: 'utf8' }).trim(), 10) || 0;
  } catch {
    return 0; // pgrep exits 1 when it finds none
  }
}

/** Ends the Chrome processes that have the folder on their command line, by force. */
export function endChrome(folder: string): void {
  try {
    if (process.platform === 'win32') {
      const like = `*${folder}*`.replace(/'/g, "''");
      execFileSync('powershell', ['-NoProfile', '-Command',
        `Get-CimInstance Win32_Process -Filter "Name='chrome.exe'" | ` +
        `Where-Object { $_.CommandLine -like '${like}' } | ` +
        'ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }'],
      { windowsHide: true });
    } else {
      execFileSync('pkill', ['-9', '-f', '--', escape(folder)]);
    }
  } catch {
    // none left to end
  }
}

const escape = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

/**
 * Deletes the files of the NVIDIA driver's cache of Chrome's shaders that have more than mb MB written, on Windows,
 * and returns how many MB each had. The driver keeps what it compiled for a program in a file of the program's own,
 * which nothing was seen to clear, and adds to it the slower the more it holds: thirty runs of shaders it had not seen
 * took a browser's wait for them from 2 s to 30, its file at 229 MB by then. A file deleted is made anew, and compiles
 * as fast as it first did. 64 MB is some six builds' of a map's shaders (a first load of Kino writes 10).
 *
 * The files are in the folder of programs with no sandbox, as the players' Chromes are. A file is Chrome's by its name,
 * whose last eight digits are a number of the program's name: 64ef8c69 for chrome.exe, and the next for each Chrome
 * running beside the first. One in use can't be deleted, and stays: the user's own Chrome has one in the folder while
 * it runs. So does one that does not begin as the driver's did when this was written, with "nvph" and, from its ninth
 * byte, how much of it is written.
 */
export function clearDriverCache(mb: number): number[] {
  const local = process.env.LOCALAPPDATA;
  if (process.platform !== 'win32' || !local || !mb) return [];
  const dir = join(local, 'NVIDIA', 'DXCache');
  const cleared: number[] = [];
  let names: string[];
  try {
    names = readdirSync(dir);
  } catch {
    return [];
  }
  for (const name of names) {
    const m = /^[0-9a-f]{8}([0-9a-f]{8})\.nvph$/.exec(name);
    const n = m ? parseInt(m[1], 16) - 0x64ef8c69 : -1;
    if (n < 0 || n >= 64) continue;
    const file = join(dir, name);
    try {
      const head = Buffer.alloc(16);
      const fd = openSync(file, 'r');
      readSync(fd, head, 0, 16, 0);
      closeSync(fd);
      const written = head.readUInt32LE(8);
      if (head.readUInt32LE(0) !== 1752200814 || written <= mb * 1048576 || written > statSync(file).size) continue;
      unlinkSync(file);
      cleared.push(Math.round(written / 1048576));
    } catch {
      // in use, or gone
    }
  }
  return cleared;
}
