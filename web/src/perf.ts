// web/src/perf.ts - the page's performance display: what each frame takes, and a tour that measures a map's views.
//
// &perf=1 shows a debug view at the top right of the page: what each frame takes (showPerf). The engine reports its
// frame timings twice a second (bo1-zombies/web/src/d3d9_webgpu.cpp: ReportPerf). The game has a frame rate counter
// of its own, on F9.
//
// &perf=tour, with ?map=, shows the same view, and the page measures the map's fixed views by itself once the player
// has spawned (perfTourRun). The page then shows what each view took, to copy. The tour compares how the page draws
// now with how it draws under the &gpu= flags 1024 and 2048. &toggle= names other &gpu= flags to compare, such as
// &toggle=4096.
import { $ } from './dom';
import { player } from './engine';
import { map, params } from './params';
import { loadedServer } from './server';

const perfEl = $('perf');

/** Whether the page runs the tour: &perf=tour, with ?map= (perfTourRun). */
export const perfTour = params.get('perf') === 'tour' && params.has('map');

/**
 * Whether the page shows the performance view: &perf=1, or the tour. The page then asks for the GPU's clock too, for
 * the view's GPU times (main.ts: makePlayer).
 */
export const showPerfView = params.get('perf') === '1' || perfTour;

/**
 * What the frames that the page showed in the last half second took, as the engine reports it
 * (bo1-zombies/web/src/d3d9_webgpu.cpp: ReportPerf). The times are in milliseconds.
 */
export interface PerfReport {
  /** The frames shown a second. */
  fps: number;
  /** The time between two frames: the average, and the longest. */
  frameMs: number;
  worstMs: number;
  /**
   * The mouse lag: from the movement that a frame was built with to the GPU having the frame. It is -1 when the mouse
   * did not move.
   */
  lagMs: number;
  /** What the game thread, the render thread and the page took for a frame. */
  gameMs: number;
  renderMs: number;
  pageMs: number;
  /**
   * The share of its animation frames that the page left to the GPU to catch up, from 0 to 1. The view shows it as
   * the time that the page waited for the GPU.
   */
  gpu: number;
  /** How the page paces the engine: an index of PACING. */
  pacing: number;
  /**
   * The size of the back buffer that the engine draws to, and its samples a pixel. With anti-aliasing, it has more
   * than 1 sample.
   */
  width: number;
  height: number;
  samples: number;
  /** What a frame took the GPU. It is -1 when the device has no clock to say. */
  gpuMs: number;
  /**
   * The time and the count of each kind of pass in a frame (d3d9_webgpu.cpp: the GPU's time). A scene pass has the
   * size of the back buffer and many draws. A full-screen pass has that size and few draws: a post effect, the HUD,
   * the canvas. The other passes have other sizes: the shadow maps, the bloom.
   */
  gpuSceneMs: number;
  gpuScenePasses: number;
  gpuFullMs: number;
  gpuFullPasses: number;
  gpuOtherMs: number;
  gpuOtherPasses: number;
  /** How long after the page sent a frame it heard that the GPU was done with it. It is -1 when no frame was done. */
  gpuDoneMs: number;
  /** The page's animation frames a second. */
  animationFrames: number;
  /**
   * The most frames that the page went ahead of the GPU, averaged over its animation frames: 2 to 3
   * (d3d9_webgpu.cpp: AheadMost).
   */
  ahead: number;
}

// A number of a report, by its name.
type Field = keyof PerfReport;

// How the page paces the engine, by the number in a report (bo1-zombies/web/src/d3d9_device.cpp: Pace).
const PACING = ['serial', 'pipelined', 'queued'];

// The stamp of what the gamepad last said, and when the page first saw that stamp (performance.now()).
let padHeard = { stamp: -1, at: 0 };

// Returns what the page has of the player's input, which is the view's last line. It says whether the page has the
// focus, whether it is full screen, whether the mouse is captured, and how long ago the gamepad that the game reads
// last said anything. Chrome stamps what a gamepad says, and the stamp moves only when a button or a stick does. A
// stick that is held still says nothing.
//
// The line is for a phone, where a popup of another app can take the gamepad from Chrome's window. Chrome on Android
// tells the page nothing of that. The page keeps the focus while the popup has the window's focus, and Chrome goes on
// showing the gamepad as it last heard it: a stick that was held stays held. The game lets go of the sticks when the
// page loses the focus (bo1-zombies/web/src/library_bo1.js: focusChanged). This line says whether the game could have
// done so. "focus" during such a popup, with a silent gamepad, means that Chrome said nothing.
function inputSays(): string {
  const pad = (navigator.getGamepads ? [...navigator.getGamepads()] : [])
    .find((p) => p && p.connected && p.mapping === 'standard');
  const now = performance.now();
  if (pad && pad.timestamp !== padHeard.stamp) padHeard = { stamp: pad.timestamp, at: now };
  return (document.hasFocus() ? 'focus' : 'no focus') + (document.fullscreenElement ? ', full screen' : ', a window') +
    ', mouse ' + (document.pointerLockElement ? 'captured' : 'free') +
    (pad ? ', gamepad heard ' + ((now - padHeard.at) / 1000).toFixed(1) + ' s ago' : ', no gamepad');
}

// The page's animation frames a second in the engine's last 10 reports, which span 5 s. The view shows the least and
// the most of them. They say what the rate was before a screenshot was taken. On a phone, a screenshot takes a finger
// on the screen or a press of the phone's buttons. Either puts the phone's display at 120 Hz, where a game played
// with a gamepad had it at 60.
const framesSeen: number[] = [];

/**
 * Shows what each frame takes, from a report of the engine. The engine calls it twice a second, as its Module's
 * bo1Perf (main.ts: makePlayer). It does nothing without the performance view.
 *
 * After the timings, the view says what the console says when a game starts: the size and anti-aliasing of the
 * picture that the engine draws, and the GPU as WebGPU names it. When the GPU takes none of the block compression of
 * the game's textures, the view also says that the textures are decompressed for it (web/public/player.js: gpu).
 * These lines are for mobile, where reading the console takes a cable and a PC. The last line is the player's input
 * (inputSays).
 */
export function showPerf(p: PerfReport): void {
  if (!showPerfView) return;
  if (tourReports) tourReports.push(p);
  framesSeen.push(Math.round(p.animationFrames));
  if (framesSeen.length > 10) framesSeen.shift();
  const ms = (v: number) => v.toFixed(1);
  const passes = (n: number, kind: string) => Math.round(n) + ' ' + kind; // the passes of a kind in a frame
  // The line about waiting for the GPU shows when the page waited 5% of the time or more, or went fewer than three
  // frames ahead. The lines of the GPU's times show when the report has them.
  perfEl.textContent = (tourSays ? tourSays + '\n' : '') +
    Math.round(p.fps) + ' FPS  ' + ms(p.frameMs) + ' ms, worst ' + ms(p.worstMs) +
    '\nmouse lag ' + (p.lagMs < 0 ? '-' : Math.round(p.lagMs) + ' ms') + ', pacing ' + (PACING[p.pacing] || p.pacing) +
    '\ngame ' + ms(p.gameMs) + '  render ' + ms(p.renderMs) + '  page ' + ms(p.pageMs) + ' ms' +
    (p.gpu >= 0.05 || p.ahead < 2.95 ? '\nwaiting for the GPU ' + Math.round(p.gpu * 100) + '% of the time' +
      (p.ahead < 2.95 ? ', ' + (p.ahead < 2.05 ? 'two' : 'two or three') + ' frames ahead at most' : '') : '') +
    (p.gpuDoneMs >= 0 ? '\nframes done ' + ms(p.gpuDoneMs) + ' ms after sent' : '') +
    (p.gpuMs >= 0 ? '\nGPU ' + ms(p.gpuMs) + ' ms a frame' +
      '\n  ' + passes(p.gpuScenePasses, 'scene') + ' ' + ms(p.gpuSceneMs) + ', ' +
      passes(p.gpuFullPasses, 'full-screen') + ' ' + ms(p.gpuFullMs) +
      '\n  ' + passes(p.gpuOtherPasses, 'other') + ' ' + ms(p.gpuOtherMs) + ', between passes ' +
      ms(Math.max(0, p.gpuMs - p.gpuSceneMs - p.gpuFullMs - p.gpuOtherMs)) : '') +
    '\nanimation frames ' + Math.round(p.animationFrames) + ' a second, ' + Math.min(...framesSeen) + ' to ' +
      Math.max(...framesSeen) + ' in 5 s' +
    (p.width ? '\n' + p.width + 'x' + p.height + ', ' + (p.samples > 1 ? p.samples + 'x' : 'no') + ' anti-aliasing'
      : '') +
    (player.gpu ? '\n' + player.gpu : '') + '\n' + inputSays();
  perfEl.classList.remove('hidden');
}

// ---- The tour (&perf=tour) ----
//
// The page measures the map's fixed views by itself, as docker/player/perf.mjs does from outside. The tour is for a
// phone, which no script drives. It runs in a game of the page's own (?map=), because that game has cheats on, and a
// cheat places the eye (setviewpos). The tour waits for the player to spawn. It then puts them at the spawn, and at
// the two views of Kino der Toten that cost the most (TOUR_VIEWS).
//
// At each view, the tour takes the engine's reports (showPerf) three times. The first time, the page draws as it does
// now. The second time, the &gpu= flags 1024 and 2048 are set (main.ts lists the flags). They turn off two cuts in
// the renderer's calls to WebGPU: the renderer then binds the vertex shader constants of each draw, and makes a call
// for each draw. That is some 60% more calls at Kino's heaviest view. The third time, the page draws as it does now
// again, so that what drifts meanwhile, such as a phone that warms up, weighs on both ways. The difference between
// the two ways is what those calls cost the device, and what cutting more of them would save.
//
// A row of the table is the reports of 6 s. The tour takes them once two periods of 3 s in a row took the same, to
// within a twelfth (measure). It waits for that because the pipelines of the other way compile first, and because a
// GPU with time to spare clocks down.
//
// When the tour is done, the rows show over the page, under a button that copies them, and in the console.
//
// &toggle= names other &gpu= flags for the tour to set the second time. &toggle=4096 keeps the page at most two
// frames ahead of the GPU. Without that flag, the page goes a third frame ahead while the GPU keeps pace.

// A fixed view: its name, and where the eye is and looks, as setviewpos takes it.
type TourView = [name: string, at: string];

// The fixed views of a map besides its spawn, by map. Kino der Toten (zombie_theater) has the two views that cost
// the most (docker/player/perf.mjs: VIEWS).
const TOUR_VIEWS: Record<string, TourView[]> = {
  zombie_theater: [['heaviest', '100 1724 276 270 0'], ['second', '760 1540 48 180 0']],
};

// The &gpu= flags that turn off the renderer's cuts in calls to WebGPU.
const TOUR_CUTS = 1024 | 2048;

// The &gpu= flags that the tour sets the second time: those of &toggle=, or those of the cuts.
const tourToggle = parseInt(params.get('toggle') || '', 10) || TOUR_CUTS;

// The engine's reports since a measurement began, or null while none runs.
let tourReports: PerfReport[] | null = null;

// What the tour is doing, which the view shows as its first line. It is '' without a tour.
let tourSays = '';

// Takes the game's answer to bo1_player while the tour waits for one (tourSaid).
let tourAnswer: ((line: string) => void) | null = null;

/**
 * Hears a line that the game says (web/public/player.js: onGame). The page calls it for every line (main.ts:
 * makePlayer). A line that answers bo1_player goes to the tour while the tour waits for one (perfTourRun).
 */
export function tourSaid(line: string): void {
  if (tourAnswer && line.startsWith('bo1_player: ')) tourAnswer(line);
}

// What a measurement gave: the reports, and whether the frames were steady.
interface Measured {
  list: PerfReport[];
  steady: boolean;
}

/**
 * Runs the tour: waits for the player to spawn, measures each view, and shows the table over the page. The page calls
 * it once, as it starts (main.ts: startPage).
 */
export async function perfTourRun(): Promise<void> {
  const sleep = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));
  // Runs a console command in the game. It does nothing when the engine isn't up yet.
  const command = (text: string): void => {
    try {
      player.module.bo1Command(text);
    } catch {
      // The engine isn't up yet.
    }
  };
  // Asks the game where the player is (the console's bo1_player). The game sends its answers to the page once the
  // page has asked to hear them (bo1-zombies/web/src/web_main.cpp: Bo1_PageHearsPlayer). An answer comes as a line
  // that the game says (tourSaid). Returns false when the engine isn't up yet.
  const askPlayer = (): boolean => {
    try {
      player.module.ccall!('Bo1_PageHearsPlayer', null, [], []);
      player.module.bo1Command('bo1_player');
      return true;
    } catch {
      return false;
    }
  };
  // Waits until the player has spawned with a weapon in hand: the game's answer says pm_type 0, and a weapon other
  // than none. Each round, the tour waits 2 s, asks, and waits up to 1.5 s for the answer. spawn is where the player
  // stands and looks, as setviewpos takes it: the eye, then yaw and pitch. The eye is 60 units above the origin, and
  // the answer has the pitch before the yaw.
  tourSays = 'tour: waiting for the player to spawn';
  let spawn = '';
  while (!spawn) {
    await sleep(2000);
    const line = await new Promise<string>((resolve) => {
      tourAnswer = resolve;
      setTimeout(() => resolve(''), 1500);
      if (!askPlayer()) resolve('');
    });
    tourAnswer = null;
    const m = /^bo1_player: pm_type 0 origin (\S+) (\S+) (\S+) angles (\S+) (\S+) weapon (\S+)/.exec(line);
    if (m && m[6] !== 'none') spawn = [+m[1], +m[2], +m[3] + 60, +m[5], +m[4]].join(' ');
  }
  // With these cheats, the player takes no damage, and the zombies don't come for them.
  command('god');
  command('notarget');
  const mean = (list: PerfReport[], k: Field) => list.reduce((s, p) => s + p[k], 0) / (list.length || 1);
  const frames: number[] = []; // the page's animation frames a second, in every report taken
  // Returns the engine's reports of the next ms milliseconds.
  const reports = async (ms: number): Promise<PerfReport[]> => {
    tourReports = [];
    await sleep(ms);
    const list = tourReports;
    tourReports = null;
    for (const p of list) frames.push(Math.round(p.animationFrames));
    return list;
  };
  // Whether two lists of reports have the same mean of a number: to within a twelfth of the first, or within least.
  const same = (a: PerfReport[], b: PerfReport[], k: Field, least: number) =>
    Math.abs(mean(a, k) - mean(b, k)) <= Math.max(least, mean(a, k) / 12);
  // Measures the view as the page draws it now. It waits 3 s, then takes the reports of 3 s at a time, until two
  // periods in a row have the same frame time and the same page time: within a twelfth, or within 0.5 ms and 0.3 ms
  // where a twelfth is less. The result is the reports of both periods. After 10 periods, which take 30 s, it gives
  // up. The result is then the reports of the last period, marked as not steady.
  const measure = async (): Promise<Measured> => {
    await sleep(3000);
    let before = await reports(3000);
    for (let i = 0; i < 9; i++) {
      const now = await reports(3000);
      if (before.length && now.length && same(before, now, 'frameMs', 0.5) && same(before, now, 'pageMs', 0.3)) {
        return { list: before.concat(now), steady: true };
      }
      before = now;
    }
    return { list: before, steady: false };
  };
  // The table's columns. Each is a mean over the reports of a row, of these numbers (PerfReport says what they are):
  //   FPS, frame, worst          fps, frameMs, and the longest worstMs
  //   game, render, page         gameMs, renderMs, pageMs
  //   waits, ahead               gpu as a percentage, ahead
  //   done                       gpuDoneMs, over the reports that have it
  //   GPU, scene, full, other    gpuMs, gpuSceneMs, gpuFullMs, gpuOtherMs, over the reports that have the GPU's time
  const cells = (label: string, list: (string | number)[]) =>
    ' ' + label.padEnd(10) + list.map((v) => String(v).padStart(7)).join('');
  const ms = (list: PerfReport[], k: Field) => (list.length ? mean(list, k).toFixed(1) : '-');
  const row = (label: string, got: Measured): string => {
    const r = got.list;
    if (!r.length) return ' ' + label.padEnd(10) + ' no reports';
    const timed = r.filter((p) => p.gpuMs >= 0);
    const done = r.filter((p) => p.gpuDoneMs >= 0);
    return cells(label, [Math.round(mean(r, 'fps')), ms(r, 'frameMs'), Math.max(...r.map((p) => p.worstMs)).toFixed(1),
      ms(r, 'gameMs'), ms(r, 'renderMs'), ms(r, 'pageMs'), Math.round(mean(r, 'gpu') * 100) + '%', ms(r, 'ahead'),
      ms(done, 'gpuDoneMs'), ms(timed, 'gpuMs'), ms(timed, 'gpuSceneMs'), ms(timed, 'gpuFullMs'),
      ms(timed, 'gpuOtherMs')]) +
      (got.steady ? '' : '  not steady in 30 s');
  };
  const views: TourView[] = [['spawn', spawn], ...(Object.hasOwn(TOUR_VIEWS, map) ? TOUR_VIEWS[map] : [])];
  const rows = [cells('', ['FPS', 'frame', 'worst', 'game', 'render', 'page', 'waits', 'ahead', 'done', 'GPU', 'scene',
    'full', 'other'])];
  const toggled = tourToggle === TOUR_CUTS ? 'cuts off' : tourToggle === 4096 ? 'two ahead' : 'gpu^' + tourToggle;
  // The three measurements of a view: the label of each row, and whether the flags flip before it.
  const ways: [label: string, flip: boolean][] = [['now', false], [toggled, true], ['now again', true]];
  let last: PerfReport | null = null;
  let step = 0;
  for (const [name, at] of views) {
    command('setviewpos ' + at);
    rows.push(name);
    for (const [label, flip] of ways) {
      tourSays = 'tour: ' + name + ', ' + label + ' (' + ++step + ' of ' + views.length * ways.length + ')';
      if (flip) player.module.bo1GpuFlags ^= tourToggle;
      const got = await measure();
      last = got.list[got.list.length - 1] || last;
      rows.push(row(label, got));
    }
  }
  tourSays = 'tour: done';
  const server = await loadedServer;
  // The lines that show: what the tour measured, on what device, then the table.
  const lines = [
    'perf tour of ' + map + (last && last.width ? ': ' + last.width + 'x' + last.height + ', ' +
      (last.samples > 1 ? last.samples + 'x' : 'no') + ' anti-aliasing' : '') +
      (last && last.gpuMs >= 0 ? '; passes: ' + Math.round(last.gpuScenePasses) + ' scene, ' +
        Math.round(last.gpuFullPasses) + ' full-screen, ' + Math.round(last.gpuOtherPasses) + ' other' : ''),
    player.gpu.replace(/\n/g, ', ') + ', ' + (navigator.hardwareConcurrency || '?') + ' cores; screen ' +
      screen.width + 'x' + screen.height + ' at ' + devicePixelRatio + '; ' + inputSays() +
      (frames.length ? '; animation frames ' + Math.min(...frames) + ' to ' + Math.max(...frames) + ' a second' : '') +
      (server.web ? '; web client ' + server.web : ''),
    ...rows,
  ];
  for (const line of lines) console.log('perf tour: ' + line);
  $('tourtext').textContent = lines.join('\n');
  $('tourcopy').onclick = async () => {
    try {
      await navigator.clipboard.writeText(lines.join('\n'));
      $('tourcopied').textContent = 'Copied.';
    } catch {
      // Without a clipboard, the page selects the lines, to copy by hand.
      getSelection()!.selectAllChildren($('tourtext'));
      $('tourcopied').textContent = 'Selected: copy it by hand.';
    }
  };
  $('tour').classList.remove('hidden');
}
