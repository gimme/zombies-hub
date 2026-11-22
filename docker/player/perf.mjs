// docker/player/perf.mjs - what the web client's frames cost the browser, for docker/perf.sh: Playwright drives headless
// Chromium, drawing with WebGPU, through Kino alone, and reads the renderer's frame profile (web/src/main.ts: &gpu=16) at
// fixed views: each pass' draws and the calls to WebGPU that set state for them, and the bytes the frame wrote, by where
// they went. These don't depend on how fast the GPU is, so a headless browser drawing in software counts what a gaming
// PC's does.
//
//   node perf.mjs <server url> <out file> [--survey] [--gpu <flags>] [--cpu] [--clear <mode>] [--args <engine args>]
//                 [--command <command>] [--toggle <command>] [--capture] [--mobile] [--overlay]
//                 [--screen <width>x<height>] [--chrome [--headless]] [<x>,<y>,<z>,<yaw>,<pitch>]...
//
// Each view is where the eye is, and where it looks; with none, the spawn as the player spawns and the views below
// (VIEWS). Each is profiled 3 times, a few seconds apart, then screenshot (<out file> less .json, -<view>.png), and the
// page's time a frame taken over 5 s (web/src/perf.ts: &perf=1). --chrome: the installed Chrome instead, in a window,
// drawing with the PC's GPU: the frame times a player gets (run on the PC with docker/server.sh up, after npm ci here:
// node docker/player/perf.mjs http://localhost:3075 build/perf/<name>.json --chrome; Chrome draws the window only while
// it shows: the screen on, not locked or covered). --headless, with --chrome: Chrome's headless mode, on the same GPU,
// in no window, for an agent comparing changes on the PC with no one at the screen. It shows frames at its own rate
// (about 64 a second), and the page's time a frame comes out a little higher than in the window (by 4-9% at the three
// views, over 5 games each way), but what a change adds to it comes out the same (--toggle gpu^128 added 5.1-5.4 ms
// at the heaviest view, in 2 games each way). --survey
// looks around every cell of the map from its middle first (r_mod_cells), and prints the views that cost the most.
// --mobile: the page's &mobile=1, a phone's settings (web/src/settings.ts: RECOMMENDED), whose frames have no shadow maps.
// --overlay: the page's &perf=1, which asks for the GPU's clock: the timings then have the GPU's own time a frame, by
// kind of pass (d3d9_webgpu.cpp: the GPU's time), and the screenshots the overlay.
// --screen: the screen's size, and the browser's window's, as in full screen; 1280x720 without it. The page draws at
// the screen's (web/src/settings.ts: Auto, which is half of it each way from 1800 lines up, and --mobile's 720 lines in its
// shape), so with --chrome a player's own screen's has the frame times they get, and the screenshots are that size. A
// wider picture takes in more draws: at 3440x1440 Kino's heaviest view has 8,130 where it has 7,850 at 1280x720, and
// an RTX 3080 on a 144 Hz display held 144 frames a second there, and 125-137 with gpu^3072 toggled (3 games), where
// at 1280x720 both ways held 140-144.
// --gpu: the page's &gpu= (index.html), such as 32 to draw as before fewer calls did. --clear: r_clear (default 3 with
// a black r_clearColor, as players' pages; 0 never clears the scene's colour). --args: more of the engine's command line, such as '+set r_clearColor 0 0 0 0'.
// --command: a console command run once, as the player has spawned, such as 'cg_drawGun 0' for screenshots to compare
// without the gun, which sways.
// --toggle: a console command run after each view's screenshot (and the waiting one's), then screenshot again
// (-toggled.png), and the page's time a frame taken toggled, both ways in turn, then run again (a toggle: back as it
// was): the same game, player and view, to compare. gpu^<bits>
// flips bits of the page's &gpu= instead, such as gpu^32 to draw as before fewer calls did. --capture: every
// draw of one frame at each view (-<view>.capture; web/src/main.ts: &gpu=4), and the WGSL of every shader module the page
// made, by the id a captured draw names it by (-shaders.json). A view whose first profile has draws whose
// pipelines compile (drawn with stand-ins, or left out) is screenshot as it last has some too (-<view>-waiting.png). --cpu:
// Chrome's CPU profile of
// the page for 5 s at each view too (<out file> less .json, -<view>.cpuprofile; DevTools' Performance panel opens it).
// The profiles go to <out file> as JSON.
import { writeFileSync } from 'node:fs';
import { chromium } from 'playwright';

const args = process.argv.slice(2);
const survey = args.includes('--survey');
const cpu = args.includes('--cpu');
const capture = args.includes('--capture');
const mobile = args.includes('--mobile');
const overlay = args.includes('--overlay');
const chrome = args.includes('--chrome');
const headless = args.includes('--headless');
const flagsAt = args.indexOf('--gpu');
const gpu = flagsAt >= 0 ? +args[flagsAt + 1] : 0;
const clearAt = args.indexOf('--clear');
const clear = clearAt >= 0 ? +args[clearAt + 1] : 3;
const extraAt = args.indexOf('--args');
const extra = extraAt >= 0 ? ` ${args[extraAt + 1]}` : '';
const toggleAt = args.indexOf('--toggle');
const toggle = toggleAt >= 0 ? args[toggleAt + 1] : '';
const commandAt = args.indexOf('--command');
const first = commandAt >= 0 ? args[commandAt + 1] : '';
const screenAt = args.indexOf('--screen');
const valued = new Set([flagsAt, extraAt, clearAt, toggleAt, commandAt, screenAt].filter((i) => i > 0).flatMap((i) => [i, i + 1]));
const [server, out, ...rest] = args.filter((a, i) => !['--survey', '--cpu', '--capture', '--mobile', '--overlay', '--chrome', '--headless'].includes(a) && !valued.has(i));
if (!server || !out || (headless && !chrome)) {
  console.error('usage: perf.mjs <server url> <out file> [--survey] [--gpu <flags>] [--cpu] [--clear <mode>] [--args <engine args>] [--command <command>] [--toggle <command>] [--capture] [--mobile] [--overlay] [--screen <width>x<height>] [--chrome [--headless]] [<x>,<y>,<z>,<yaw>,<pitch>]...');
  process.exit(2);
}
// the screen, and the window, which is all of it
const sized = /^([1-9]\d*)x([1-9]\d*)$/.exec(screenAt >= 0 ? `${args[screenAt + 1]}` : '1280x720');
if (!sized) {
  console.error(`perf: not a screen's size, <width>x<height>: ${args[screenAt + 1]}`);
  process.exit(2);
}
const screen = { width: +sized[1], height: +sized[2] };
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const log = (line) => console.log(`perf: ${line}`);

// Kino's views to compare from run to run, besides the spawn: the two that cost the most in a survey (--survey)
const VIEWS = [
  { name: 'heaviest', at: [100, 1724, 276, 270, 0] },
  { name: 'second', at: [760, 1540, 48, 180, 0] },
];
let views = rest.map((v) => {
  const n = v.split(',').map(Number);
  if (n.length !== 5 || n.some(Number.isNaN)) {
    console.error(`perf: not a view: ${v}`);
    process.exit(2);
  }
  return { name: v, at: n };
});

// the full Chromium in its headless mode, WebGPU on SwiftShader; --chrome: Chrome as players run it
// (--headless: in Chrome's headless mode, on the same GPU)
const browser = chrome
  ? await chromium.launch({ channel: 'chrome', headless, args: ['--autoplay-policy=no-user-gesture-required'] })
  : await chromium.launch({
    channel: 'chromium',
    args: ['--use-angle=swiftshader', '--enable-unsafe-webgpu', '--enable-features=Vulkan', '--use-vulkan=swiftshader',
      '--autoplay-policy=no-user-gesture-required'],
  });
const page = await browser.newPage({ viewport: screen, screen });
// --capture: each shader module's WGSL as the page makes it, by its label (d3d9_webgpu.cpp: ModuleHandle; vs<id> or
// ps<id>, a capture's "modules vs<id> ps<id>"), to read what a captured draw's shaders do
if (capture)
  await page.addInitScript(() => {
    const make = GPUDevice.prototype.createShaderModule;
    window.bo1Shaders = {};
    GPUDevice.prototype.createShaderModule = function (desc) {
      if (desc && desc.label) window.bo1Shaders[desc.label] = desc.code;
      return make.call(this, desc);
    };
  });

let answer = null;
let profileLines = null;
let captureLines = null;
const cells = [];
page.on('console', (m) => {
  const t = m.text();
  if (answer && t.startsWith('bo1_player: ')) answer(t);
  if (profileLines && t.startsWith('[bo1] webgpu profile: ')) profileLines.push(t.slice(22));
  if (captureLines && /^\[bo1\] cap /.test(t)) captureLines.push(t.slice(6));
  const cell = /^r_mod_cells: \d+ (\S+) (\S+) (\S+)/.exec(t);
  if (cell) cells.push([+cell[1], +cell[2], +cell[3]]);
  if (/^\[bo1\] (gpu error|webgpu: \d+ pipeline recipes|webgpu: \d+ draws worked out both ways|webgpu: a pipeline compiled|webgpu: \d+ pipelines compiled)|webgpu: going in|^page: |Pipelines (compiled|still compiling)|R_ShaderWarming/.test(t)) console.log(t);
});
page.on('pageerror', (e) => console.log(`page: uncaught ${e.message}`));
// as for players (web/src/game.ts: startEngine, black), which docker/server.sh's dev server leaves out
const engineArgs = `+set r_clear ${clear} +set r_clearColor 0 0 0 1${extra}`;
await page.goto(`${server}/?map=zombie_theater&autostart=1${mobile ? '&mobile=1' : ''}${overlay ? '&perf=1' : ''}${gpu ? `&gpu=${gpu}` : ''}&args=${encodeURIComponent(engineArgs)}`);
const command = (text) => page.evaluate((t) => Module.bo1Command(t), text);
const adapter = await page.evaluate(async () => {
  const a = await navigator.gpu?.requestAdapter({ powerPreference: 'high-performance' });
  return a ? `${a.info.vendor} ${a.info.architecture} ${a.info.description}`.trim() : 'none';
});
log(`WebGPU adapter: ${adapter}`);

// what &perf=1 shows (web/src/perf.ts: showPerf), as the engine reports it twice a second (d3d9_webgpu.cpp: ReportPerf)
await page.evaluate(() => {
  const show = Module.bo1Perf;
  Module.bo1Perf = (r) => {
    (window.bo1PerfReports ||= []).push(r);
    show(r);
  };
});
// the page's time a frame over the next `ms`, and the rest of the reports'
async function timings(ms) {
  await page.evaluate(() => (window.bo1PerfReports = []));
  await sleep(ms);
  const reports = await page.evaluate(() => window.bo1PerfReports);
  const mean = (k) => reports.reduce((s, r) => s + r[k], 0) / (reports.length || 1);
  const pages = reports.map((r) => r.pageMs);
  // the GPU's own time a frame and its kinds of passes' (--overlay: the page asks for the GPU's clock with &perf=1), over
  // the reports that have it, and how long after the page sent a frame the GPU was done with it
  const timedReports = reports.filter((r) => r.gpuMs >= 0);
  const gpuMean = (k) => (timedReports.length ? timedReports.reduce((s, r) => s + r[k], 0) / timedReports.length : -1);
  const done = reports.filter((r) => r.gpuDoneMs >= 0);
  return { pageMs: mean('pageMs'), pageMsLow: Math.min(...pages), pageMsHigh: Math.max(...pages), fps: mean('fps'),
    gameMs: mean('gameMs'), renderMs: mean('renderMs'), gpu: mean('gpu'), reports: reports.length,
    gpuMs: gpuMean('gpuMs'), gpuSceneMs: gpuMean('gpuSceneMs'), gpuScenePasses: gpuMean('gpuScenePasses'), gpuFullMs: gpuMean('gpuFullMs'),
    gpuFullPasses: gpuMean('gpuFullPasses'), gpuOtherMs: gpuMean('gpuOtherMs'), gpuOtherPasses: gpuMean('gpuOtherPasses'),
    gpuDoneMs: done.length ? done.reduce((s, r) => s + r.gpuDoneMs, 0) / done.length : -1 };
}
const timed = (t) =>
  `page ${t.pageMs.toFixed(2)} ms a frame (${t.pageMsLow.toFixed(2)}-${t.pageMsHigh.toFixed(2)} in ${t.reports} reports), ` +
  `${Math.round(t.fps)} FPS, game ${t.gameMs.toFixed(1)} ms, render ${t.renderMs.toFixed(1)} ms` +
  `${t.gpu >= 0.05 ? `, waiting for the GPU ${Math.round(t.gpu * 100)}% of the time` : ''}` +
  `${t.gpuDoneMs >= 0 ? `, frames done ${t.gpuDoneMs.toFixed(1)} ms after sent` : ''}` +
  `${t.gpuMs >= 0 ? `, GPU ${t.gpuMs.toFixed(1)} ms a frame (${t.gpuScenePasses.toFixed(0)} scene passes ${t.gpuSceneMs.toFixed(1)}, ` +
    `${t.gpuFullPasses.toFixed(0)} full-screen ${t.gpuFullMs.toFixed(1)}, ${t.gpuOtherPasses.toFixed(0)} other ${t.gpuOtherMs.toFixed(1)})` : ''}`;

// the player, from bo1_player (e2e/game/player.ts: ask)
async function ask() {
  const line = new Promise((resolve) => {
    answer = resolve;
    setTimeout(() => resolve(''), 5000);
  });
  const sent = await page.evaluate(() => {
    try {
      Module.bo1Command('bo1_player');
      return true;
    } catch {
      return false;
    }
  });
  const text = sent ? await line : '';
  answer = null;
  const m = /^bo1_player: pm_type (-?\d+) origin (\S+) (\S+) (\S+) angles (\S+) (\S+) weapon (\S+)/.exec(text);
  return m && { pmType: +m[1], origin: [+m[2], +m[3], +m[4]], angles: [+m[5], +m[6]], weapon: m[7] };
}

// --toggle's command, or flip of the page's flags
async function flip() {
  const bits = /^gpu\^(\d+)$/.exec(toggle);
  if (bits) await page.evaluate((b) => (Module.bo1GpuFlags ^= b), +bits[1]);
  else await command(toggle);
}

// the next frame's profile (d3d9_webgpu.cpp: ProfileLog), parsed
async function profile() {
  profileLines = [];
  await page.evaluate(() => (Module.bo1GpuFlags |= 16));
  for (let waited = 0; !profileLines.some((l) => l.startsWith('written: ')); waited += 100) {
    if (waited > 60000) throw new Error('no profile in 60 s');
    await sleep(100);
  }
  await sleep(300); // the pass and buffer lines, logged with it
  const lines = profileLines;
  profileLines = null;
  const num = (re, line) => {
    const m = re.exec(line);
    return m ? m.slice(1).map(Number) : [];
  };
  const head = lines.find((l) => / draws in \d+ passes/.test(l));
  const written = lines.find((l) => l.startsWith('written: '));
  const [draws, passes, sets] = num(/^(\d+) draws in (\d+) passes, (\d+) calls/, head);
  // the calls that drew them: fewer, where draws alike went as one (library_gpu.js: bo1gpu_draw_indexed)
  const [drawCalls = draws] = num(/^(\d+) calls to draw them/, lines.find((l) => /^\d+ calls to draw them/.test(l)) || '');
  const [waits] = num(/(\d+) draws whose pipelines were compiling/, head);
  const [submits] = num(/(\d+) submits/, head);
  const kb = [...written.matchAll(/(\d+) KB/g)].reduce((s, m) => s + +m[1], 0);
  const [writes] = num(/; (\d+) calls to the queue/, written);
  // calls to WebGPU for the frame's draws: the draws, and the state set for them; the queue's writes
  return { draws, drawCalls, passes, sets, calls: drawCalls + sets + writes, writes, kb, submits, waits, lines };
}

const brief = (p) =>
  `${p.draws} draws in ${p.passes} passes (${p.drawCalls} calls), ${p.sets} state calls, ${p.writes} writes: ${p.calls} calls; ${p.kb} KB written, ` +
  `${p.submits} submits${p.waits ? `, ${p.waits} draws waiting for pipelines` : ''}`;

let p = null;
for (let waited = 0; !(p && p.pmType === 0 && p.weapon !== 'none'); waited += 2) {
  if (waited >= 600) throw new Error('the map did not download and the player spawn in 600 s');
  await sleep(2000);
  p = await ask();
}
log(`spawned at ${p.origin.join(' ')}, facing ${p.angles.join(' ')}`);
await command('god');
await command('notarget');
if (first) await command(first);
const spawn = [p.origin[0], p.origin[1], p.origin[2] + 60, p.angles[1], p.angles[0]];
if (!views.length) views = [{ name: 'spawn', at: spawn }, ...VIEWS];

// pipelines compile as draws first need them, and the zone's stand-ins after the spawn (d3d9_webgpu.cpp: Preload): a
// profile counts only what is drawn.
// shot: a file to screenshot what draws whose pipelines compile look like (drawn with stand-ins, or left out), the last
// time a profile has any.
async function settled(name, shot) {
  let last = null;
  for (let i = 0; i < 40; i++) {
    last = await profile();
    if (!last.waits) return last;
    if (shot) {
      await page.screenshot({ path: `${shot}.png` });
      if (toggle) {
        await flip();
        await sleep(300);
        await page.screenshot({ path: `${shot}-toggled.png` });
        await flip();
      }
    }
    await sleep(3000);
  }
  log(`${name}: draws still waiting for pipelines`);
  return last;
}

const results = { views: [], survey: [] };
if (survey) {
  await command('r_mod_cells');
  await sleep(2000);
  await command('noclip');
  log(`surveying ${cells.length} cells`);
  for (const [cx, cy, cz] of cells)
    for (const yaw of [0, 90, 180, 270]) {
      await command(`setviewpos ${cx} ${cy} ${cz} ${yaw} 0`);
      await sleep(1500);
      const r = await settled(`${cx},${cy},${cz},${yaw},0`);
      results.survey.push({ at: [cx, cy, cz, yaw, 0], draws: r.draws, calls: r.calls, kb: r.kb });
      log(`${cx},${cy},${cz},${yaw},0: ${brief(r)}`);
    }
  await command('noclip');
  const top = [...results.survey].sort((a, b) => b.calls - a.calls).slice(0, 10);
  log('the views that cost the most:');
  for (const v of top) log(`  ${v.at.join(',')}: ${v.draws} draws, ${v.calls} calls, ${v.kb} KB`);
}

for (const v of views) {
  await command(`setviewpos ${v.at.join(' ')}`);
  await sleep(4000);
  const file = `${out.replace(/\.json$/, '')}-${v.name.replace(/[^\w.-]/g, '_')}`;
  const runs = [];
  for (let i = 0; i < 3; i++) {
    runs.push(await settled(v.name, i ? '' : `${file}-waiting`));
    await sleep(2000);
  }
  const view = { name: v.name, at: v.at, runs };
  results.views.push(view);
  await page.screenshot({ path: `${file}.png` });
  view.timings = [await timings(5000)];
  if (toggle) {
    await flip();
    await sleep(2000);
    await settled(v.name); // a toggle that draws with other pipelines (gpu^1024) compiles them first
    await page.screenshot({ path: `${file}-toggled.png` });
    // as it was and toggled in turn, so that what drifts in the game weighs on both
    view.toggled = [await timings(5000)];
    await flip();
    await sleep(1000);
    view.timings.push(await timings(5000));
    await flip();
    await sleep(1000);
    view.toggled.push(await timings(5000));
    await flip();
    await sleep(1000);
  }
  if (capture) {
    // every draw of a frame (d3d9_webgpu.cpp: CaptureDraw), to <file>.capture
    captureLines = [];
    await page.evaluate(() => (Module.bo1GpuFlags |= 4));
    for (let waited = 0; !captureLines.some((l) => l.startsWith('cap end')) && waited < 60000; waited += 100) await sleep(100);
    writeFileSync(`${file}.capture`, captureLines.join('\n') + '\n');
    captureLines = null;
  }
  if (cpu) {
    const cdp = await page.context().newCDPSession(page);
    await cdp.send('Profiler.enable');
    await cdp.send('Profiler.setSamplingInterval', { interval: 100 });
    await cdp.send('Profiler.start');
    await sleep(5000);
    const { profile } = await cdp.send('Profiler.stop');
    writeFileSync(`${file}.cpuprofile`, JSON.stringify(profile));
    await cdp.detach();
  }
  log(`${v.name} (${v.at.join(',')}):`);
  for (const r of runs) log(`  ${brief(r)}`);
  for (const t of view.timings) log(`  ${timed(t)}`);
  for (const t of view.toggled || []) log(`  toggled: ${timed(t)}`);
  for (const l of runs[runs.length - 1].lines) log(`    ${l}`);
}

writeFileSync(out, JSON.stringify(results, null, 1));
if (capture) writeFileSync(`${out.replace(/\.json$/, '')}-shaders.json`, JSON.stringify(await page.evaluate(() => window.bo1Shaders), null, 1));
await browser.close();
