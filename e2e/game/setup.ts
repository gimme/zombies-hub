// e2e/game/setup.ts - before the game tests: the watch for a request to stop the run (stop.ts), what
// docker/test-games.sh says is there, no Chrome left of an earlier run's players (chrome.ts), and the GPU driver's
// cache of Chrome's shaders not grown past ZOMBIES_TEST_DRIVER_CACHE MB a file (default 64; 0 leaves it alone). It runs
// in the runner, and returns what ends the watch as the run ends.
import { rmSync } from 'node:fs';
import { chromeLeft, clearDriverCache, endChrome, profiles, stuckNote } from './chrome';
import { watchForStop } from './stop';

export default async function setup(): Promise<() => void> {
  if (!process.env.ZOMBIES_TEST_GAME) {
    throw new Error('no ZOMBIES_TEST_GAME: run the game tests with docker/test-games.sh');
  }
  const watch = watchForStop();
  // a run asked to stop before its tests begin has nothing to make ready
  if (watch.stopping()) return watch.end;
  if (chromeLeft(profiles)) {
    endChrome(profiles);
    await new Promise((resolve) => setTimeout(resolve, 3000));
    const left = chromeLeft(profiles);
    if (left) {
      throw new Error(`${left} Chrome processes of an earlier run's players are still there, and would not end: the ` +
        "GPU's driver holds them. A Chrome started now would hang with them: restart the machine first");
    }
  }
  if (watch.stopping()) return watch.end;
  rmSync(stuckNote, { force: true });
  rmSync(profiles, { recursive: true, force: true });
  const mb = Number(process.env.ZOMBIES_TEST_DRIVER_CACHE ?? 64);
  const cleared = clearDriverCache(mb);
  if (cleared.length) {
    console.log(`deleted ${cleared.length} of the GPU driver's files of Chrome's shaders (${cleared.join(', ')} MB ` +
      `written, more than ZOMBIES_TEST_DRIVER_CACHE's ${mb}): the fuller a file, the longer a browser waits for a ` +
      "map's shaders");
  }
  return watch.end;
}
