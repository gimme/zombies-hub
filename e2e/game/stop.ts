// e2e/game/stop.ts - stopping a run of the game tests safely, at any time: docker/test-games.sh asks for it (Ctrl+C, or
// test-games.sh --stop from another terminal) with a file, stopFile, which the run's runner watches for (setup.ts:
// watchForStop). The run also stops once the script that started it is gone (ZOMBIES_TEST_SCRIPT, its process id): its
// terminal closed, which in a console ends Git Bash's processes before they can ask, or it was killed.
//
// The run then stops as Playwright stops one on its own Ctrl+C: the runner starts no other test, and asks each worker
// to stop. A worker gives up on what its test was doing (even a fixture's teardown under way, which goes on by itself),
// and tears the test's fixtures down, as at a test's end: each player's Chrome closes as a player closes theirs
// (player.ts: close), and each server stops. The worker then closes what is left of its tests before it ends
// (fixtures.ts: leftovers), as a Chrome still open would end with it. The run ends by itself, its unfinished tests
// reported as interrupted, with exit code 130.
//
// No signal of the terminal's reaches the run, whose runner and workers run apart from it (test-games.sh), and a stop
// ends no Chrome from outside, which can leave the GPU's driver hung (chrome.ts).
import { existsSync } from 'node:fs';
import { join } from 'node:path';

/** The request to stop the run. test-games.sh makes it, and deletes one an earlier run left before a run starts. */
export const stopFile = join(import.meta.dirname, '..', '..', 'build', 'test', 'stop');

/** Whether the run was asked to stop: a player's Chrome or a server that would start now does not. */
export function stopAsked(): boolean {
  return existsSync(stopFile);
}

// Whether the process of that id has ended (one this user may not look at is there). A number Windows gives again to
// another process soon after keeps the run going, as if the script were there.
function gone(pid: number): boolean {
  try {
    process.kill(pid, 0);
    return false;
  } catch (e) {
    return (e as NodeJS.ErrnoException).code === 'ESRCH';
  }
}

/**
 * In the runner: once the stop file is there, the script that started the run is gone, or the runner has SIGTERM or
 * SIGHUP, stops the run as Playwright stops it at Ctrl+C. Returns whether it is stopping by then, and what ends the
 * watch, for the run's end.
 */
export function watchForStop(): { stopping: () => boolean; end: () => void } {
  // Playwright ends a worker by force (on Windows taskkill /T, which takes its Chrome) once the worker has not ended
  // 5 min after it was asked to; a stopped test's teardown can take a few minutes on a busy machine (bigsquad's six
  // players, some 20 s each at most, then its server's stop), so the runner waits 15 min for it.
  process.env.PWTEST_CHILD_PROCESS_TIMEOUT ??= String(15 * 60_000);
  let stopping = false;
  let why = 'as it was asked';
  // Playwright's Ctrl+C: one stops the run, and another, more than a second after it, cuts the teardown short; and a
  // SIGINT with none of Playwright's listeners there at the time ends the runner. So the runner keeps a listener of
  // its own, and passes Playwright one SIGINT, the first: what comes after it, a signal or another request, is the
  // same stop.
  const emit = process.emit;
  process.emit = function (this: NodeJS.Process, event: string | symbol, ...rest: unknown[]) {
    if (event === 'SIGINT') {
      if (stopping) return true;
      stopping = true;
      clearInterval(timer);
      console.log(`\ntest-games: the run stops, ${why}: its tests end, each player's Chrome closing as at a test's ` +
        'end and each server stopping, and no other test starts. That can take a few minutes.');
    }
    return emit.call(this, event, ...rest);
  } as typeof process.emit;
  const stop = () => process.emit('SIGINT');
  process.on('SIGINT', () => {});
  process.on('SIGTERM', stop);
  process.on('SIGHUP', stop);
  const script = Number(process.env.ZOMBIES_TEST_SCRIPT) || 0;
  const check = () => {
    if (stopAsked()) {
      stop();
    } else if (script && gone(script)) {
      why = 'as the script that started it is gone (its terminal closed, or it was killed)';
      stop();
    }
  };
  const timer = setInterval(check, 500);
  timer.unref();
  check();
  return {
    stopping: () => stopping,
    end: () => clearInterval(timer),
  };
}
