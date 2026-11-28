// web/src/backdrop.ts - the page's background: the loading screen of a map, with an atmosphere for the map over it.
//
// The background is the loading screen of one map (setBackdrop). The server sends the picture from the game files
// (server/mapart.go: GET /maps/<map>/loadscreen). In the lobby, the map is that of the result that shows, else that
// of the room, else the map of Black Ops that the player picked to play locally (show.ts: show). A page that plays a
// map alone shows that map (main.ts: startPage). A new picture fades in once it has loaded. Without a picture, the
// atmosphere is on black: the server has no game files, or the page is not served by a lobby.
//
// The atmosphere goes over the picture (atmosphereStart), and each map has its own (atmospheres). It is smoke that
// rises and curls, or fog that lies low, lit from under in the colour of the map. With it comes what suits the map:
// mist, damp air, embers, snow, drizzle, fireflies, leaves, dust, an alarm light, the beam of a projector, the rays
// of an eclipse, the flashes of the teleporters, or a rocket that launches out of sight.
//
// A WebGL shader draws the atmosphere on a canvas over the picture (#atmosphere). The canvas has half the size of
// the page and is stretched over it, because nothing in the atmosphere has an edge to lose. The page does the rest
// with two elements over the canvas: a shade that darkens the backdrop as the lights falter (#backdropdim), and film
// grain (#backdropgrain).
//
// The atmosphere draws a frame for each frame that the display shows, and only while the page shows: not while a
// game plays, and not in a tab that is out of sight. Its clock stops meanwhile, so it goes on from where it was. The
// picture under it drifts slowly, and shakes a little as a rocket launches.
//
// Where the player asks for less motion (prefers-reduced-motion), the atmosphere is one still frame, no light comes
// or goes, and the picture stays put. So it is where the browser draws WebGL without a GPU (a software renderer), in
// which a frame of the atmosphere takes so long that drawing each one would take most of a CPU, and the page would
// lag. A browser without WebGL shows the picture alone, and still.
import { $ } from './dom';
import { overlay } from './screen';

// The map whose loading screen is the background, or '' before the first.
let backdropMap = '';
// Has the atmosphere draw again, so that it goes over to the atmosphere of the new map. atmosphereStart sets it.
let atmosphereWake = (): void => {};

/**
 * Shows the loading screen of a map as the page's background, and has the atmosphere go over to the atmosphere of
 * that map. The picture fades in once it has loaded. Does nothing for '', or for the map that the background shows
 * already. Without picture, the map is one that the server has no pictures of (titles.ts: hasPictures): the page
 * asks for none, and the atmosphere is on black.
 */
export function setBackdrop(name: string, picture = true): void {
  if (!name || name === backdropMap) return;
  backdropMap = name;
  atmosphereWake();
  const img = $<HTMLImageElement>('backdropimg');
  img.classList.remove('shown');
  if (!picture) {
    img.onload = img.onerror = null;
    img.removeAttribute('src');
    return;
  }
  img.onload = () => img.classList.add('shown');
  img.onerror = () => img.classList.remove('shown');
  img.src = '/maps/' + encodeURIComponent(name) + '/loadscreen';
}

// The layers of the shader. An atmosphere has an amount of each, and the shader draws that much of it (withName).
const LAYERS = ['smoke', 'fog', 'mist', 'damp', 'embers', 'snow', 'rain', 'flies', 'leaves', 'dust', 'alarm', 'beam',
  'rays'] as const;
type Layer = (typeof LAYERS)[number];

// Everything that an atmosphere has an amount of: the layers, and four things that are no layers. film is the
// flicker and the grain of a film, and faltering is lights that falter. The page draws those two. flashes is a light
// that flashes, and launch is a rocket that launches. The shader draws those two as lights, and the page shakes the
// picture with the launch.
const AMOUNTS = [...LAYERS, 'film', 'faltering', 'flashes', 'launch'] as const;
type Amount = (typeof AMOUNTS)[number];

// A place or a shift: across and down.
type Pair = [number, number];

// A colour as the shader takes it: red, green and blue, each from 0 to 1.
type Colour = number[];

// The atmosphere of a map: its two colours, as #rrggbb, and how much it has of each layer and light, from 0 to 1.
// It has none of what it does not name.
interface Atmosphere extends Partial<Record<Amount, number>> {
  /** The colour of the glow under the haze. */
  glow: string;
  /** The colour of the sparks. The lights and what floats in the air take some of it too. */
  spark: string;
  /** Where the eclipse is in the loading screen: across and down it, from 0 to 1. */
  sun?: Pair;
}

// The atmosphere of each map, picked to go with its loading screen and with what the map is about. A map that is not
// here has plainAtmosphere, and so does a page with no map.
const atmospheres: Record<string, Atmosphere> = {
  // Kino der Toten, a theatre: thinner smoke, the faint beam of a projector over it with dust in the beam, and film
  // grain.
  zombie_theater: { glow: '#bf1a0f', spark: '#ff6b1f', smoke: 0.55, embers: 0.5, beam: 1, dust: 0.6, film: 1 },
  // Five: an alarm light that turns over the smoke.
  zombie_pentagon: { glow: '#c80d12', spark: '#ff4d2e', smoke: 1, embers: 0.4, alarm: 1 },
  // Ascension: now and then a rocket launches somewhere out of sight, below the page. The light of its engines
  // swells from under the smoke, and the page rumbles a little. Then the light fades as the rocket climbs away.
  zombie_cosmodrome: { glow: '#1f5fa8', spark: '#8cc8ff', smoke: 0.6, launch: 1 },
  // Call of the Dead: light snow over icy fog.
  zombie_coast: { glow: '#138f86', spark: '#9ff0dc', fog: 1, snow: 1 },
  // Shangri-La: jungle mist that hangs in wisps, lit by the rays of the eclipse, with leaves that drift down, and
  // fireflies. The rays come from where the eclipse is in the art (sun).
  zombie_temple: { glow: '#c4570c', spark: '#ffb040', mist: 1, flies: 1, rays: 1, leaves: 1, sun: [0.662, 0.156] },
  // Moon: dust that floats in low gravity, and a little smoke.
  zombie_moon: { glow: '#3c3cb4', spark: '#b4aaff', smoke: 0.35, dust: 1 },
  // Nacht der Untoten: ground fog, and embers.
  zombie_cod5_prototype: { glow: '#b8860f', spark: '#ffd25a', fog: 1, embers: 1 },
  // Verrückt: smoke, and lights that falter now and then.
  zombie_cod5_asylum: { glow: '#73289a', spark: '#de9cff', smoke: 1, embers: 0.4, faltering: 1 },
  // Shi No Numa: a humid night over the swamp. Damp air hangs over the water and rises slowly off it, fog lies low,
  // a light drizzle falls, and there are fireflies.
  zombie_cod5_sumpf: { glow: '#4a8a1a', spark: '#d6ff6a', fog: 1, damp: 1, rain: 1, flies: 1 },
  // Der Riese: smoke, lit now and then by the flashes of the teleporters.
  zombie_cod5_factory: { glow: '#2459c8', spark: '#9ad4ff', smoke: 1, embers: 0.4, flashes: 1 },
  // Black Ops II's maps. TranZit and Bus Depot, whose loading screen is the same postcard: the deadly fog that lies
  // between the stops, and embers from the lava in the cracks of the road.
  zm_classic_transit: { glow: '#b8461c', spark: '#ff8a3d', fog: 1, embers: 0.6 },
  zm_standard_transit: { glow: '#b8461c', spark: '#ff8a3d', fog: 1, embers: 0.6 },
  // Town: smoke and embers from its fires and the lava under its streets.
  zm_standard_town: { glow: '#c2561a', spark: '#ffa040', smoke: 1, embers: 1 },
  // Farm: ground fog round the barn at night, and fireflies.
  zm_standard_farm: { glow: '#a8641e', spark: '#ffd27a', fog: 1, flies: 0.7 },
  // Nuketown: smoke and embers, and lightning now and then, as in its comic of what falls from the sky.
  zm_standard_nuked: { glow: '#2f6fd0', spark: '#ffd84a', smoke: 0.7, embers: 0.5, flashes: 1 },
  // Die Rise: smoke and dust among the broken skyscrapers, in the purple of its comic's energy.
  zm_classic_rooftop: { glow: '#7a2fbf', spark: '#c58bff', smoke: 0.8, dust: 0.6, embers: 0.3 },
  // Mob of the Dead: fog off the bay at night, embers, and lightning now and then over Alcatraz.
  zm_classic_prison: { glow: '#b0201a', spark: '#ff6a3d', fog: 1, embers: 0.6, flashes: 0.6 },
  // Buried: dust in the cave, embers, and the lanterns of its town, which falter.
  zm_classic_processing: { glow: '#a8551c', spark: '#ffb15c', dust: 1, embers: 0.5, faltering: 0.6 },
  // Origins: smoke and embers over the trenches, and a light rain, in the gold of its loading screen.
  zm_classic_tomb: { glow: '#c98a1a', spark: '#ffd27a', smoke: 0.7, embers: 0.8, rain: 0.5 },
  // Black Ops III's maps. Shadows of Evil: rain on Morg City at night, fog in its streets, and neon that falters, in
  // the green of its poster.
  zm_zod: { glow: '#12806f', spark: '#8ff5d6', fog: 0.7, rain: 0.8, faltering: 0.5 },
  // The Giant, which is Der Riese in the snow: its smoke and the flashes of its teleporters, and snow.
  zm_factory: { glow: '#2459c8', spark: '#9ad4ff', smoke: 0.8, snow: 0.7, flashes: 1 },
  // Der Eisendrache: snow over the castle, and fog off the mountain.
  zm_castle: { glow: '#3a6ea8', spark: '#bfe0ff', fog: 0.8, snow: 1 },
  // Zetsubou No Shima: a swamp on an island in the rain. Damp air and mist among the trees, and fireflies.
  zm_island: { glow: '#1c7a5a', spark: '#9fe8a0', mist: 0.8, damp: 1, rain: 0.6, flies: 0.6 },
  // Gorod Krovi: Stalingrad on fire under its dragons, in smoke and embers.
  zm_stalingrad: { glow: '#c8501a', spark: '#ffa23d', smoke: 1, embers: 1 },
  // Revelations: smoke and dust among the pieces of the other maps, embers, and lightning now and then, in the gold
  // of its loading screen and the blue of the light at its middle.
  zm_genesis: { glow: '#b8901c', spark: '#8fd8ff', smoke: 0.6, dust: 0.8, embers: 0.4, flashes: 0.5 },
};
// Black Ops III's other maps are those of the games before it (Zombies Chronicles). Each has the loading screen that
// the map has in Black Ops or Black Ops II, and so its atmosphere: Shangri-La's eclipse is where it was.
const remade: Record<string, string> = {
  zm_prototype: 'zombie_cod5_prototype', zm_asylum: 'zombie_cod5_asylum', zm_sumpf: 'zombie_cod5_sumpf',
  zm_theater: 'zombie_theater', zm_cosmodrome: 'zombie_cosmodrome', zm_temple: 'zombie_temple', zm_moon: 'zombie_moon',
  zm_tomb: 'zm_classic_tomb',
};
for (const [map, was] of Object.entries(remade)) atmospheres[map] = atmospheres[was];
// Smoke and embers in the colours of Kino der Toten.
const plainAtmosphere: Atmosphere = { glow: '#bf1a0f', spark: '#ff6b1f', smoke: 1, embers: 1 };

// The name of the uniform that says how much of a layer the shader draws: withSmoke for smoke.
const withName = (layer: Layer): string => 'with' + layer[0].toUpperCase() + layer.slice(1);

// The other uniforms of the shader.
const UNIFORMS = ['res', 'time', 'glow', 'spark', 'alarmAngle', 'lamp', 'lampAt', 'launch', 'shake', 'sunAt'] as const;
type Uniform = (typeof UNIFORMS)[number] | Layer;

// The vertex shader passes the corners of the triangle through.
const vertex = 'attribute vec2 pos; void main() { gl_Position = vec4(pos, 0.0, 1.0); }';

// The fragment shader draws the atmosphere at one pixel. res is the size of the canvas in pixels, time is the clock
// in seconds, and glow and spark are the colours of the atmosphere.
const fragment = `
  #ifdef GL_FRAGMENT_PRECISION_HIGH
  precision highp float;
  #else
  precision mediump float;
  #endif
  uniform vec2 res;
  uniform float time;
  uniform vec3 glow;
  uniform vec3 spark;
  uniform float ${LAYERS.map(withName).join(', ')};
  // The way the alarm light faces: an angle, round from the right.
  uniform float alarmAngle;
  // A light that comes and goes, for the flashes of the teleporters. lamp is its colour, as bright as the light is
  // now. lampAt is its place. Every place here is in heights of the page, from the bottom left of the page.
  uniform vec3 lamp;
  uniform vec2 lampAt;
  // A rocket that launches out of sight, below the page. x is how far across the page it is, y the seconds since its
  // engines lit, and z how much of it there is, 0 for none.
  uniform vec3 launch;
  // How far the rumble of the launch has shaken the page.
  uniform vec2 shake;
  // The place of the eclipse, which moves as the art drifts.
  uniform vec2 sunAt;

  float hash(vec2 p) {
    vec3 q = fract(vec3(p.xyx) * 0.1031);
    q += dot(q, q.yzx + 33.33);
    return fract((q.x + q.y) * q.z);
  }
  float noise(vec2 p) {
    vec2 i = floor(p), f = fract(p);
    vec2 u = f * f * (3.0 - 2.0 * f);
    return mix(mix(hash(i), hash(i + vec2(1.0, 0.0)), u.x), mix(hash(i + vec2(0.0, 1.0)), hash(i + vec2(1.0)), u.x),
      u.y);
  }
  // Octaves of noise. Each has twice the frequency of the one before, and is turned so that their grids don't line
  // up.
  float fbm(vec2 p, int octaves) {
    float f = 0.0, a = 0.5;
    for (int i = 0; i < 5; i++) {
      if (i >= octaves) break;
      f += a * noise(p);
      p = mat2(1.6, 1.2, -1.2, 1.6) * p;
      a *= 0.5;
    }
    return f;
  }
  // Lays a layer over what is under it. Both are premultiplied.
  vec4 over(vec4 under, vec4 top) {
    return top + under * (1.0 - top.a);
  }

  // Smoke that rises and drifts to the right, and curls as it goes. It is noise whose coordinates are moved by more
  // noise, twice. It is lit from below, brighter where it is thicker than just under that place, and by the lights.
  vec4 smoke(vec2 p, float low, vec3 light) {
    float t = time;
    p -= vec2(0.015, 0.045) * t;
    vec2 q = vec2(fbm(p + vec2(0.0, 0.02 * t), 3), fbm(p + vec2(5.2, 1.3) - 0.015 * t, 3));
    vec2 r = vec2(fbm(p + 2.5 * q + vec2(1.7, 9.2) + 0.04 * t, 4), fbm(p + 2.5 * q + vec2(8.3, 2.8) - 0.03 * t, 4));
    vec2 s = p + 2.2 * r;
    float f = fbm(s, 5);
    float lit = clamp((f - fbm(s - vec2(0.0, 0.07), 5)) * 9.0 + 0.45, 0.0, 1.0);
    float a = smoothstep(0.4, 0.9, f) * (0.35 + 0.5 * low);
    vec3 c = vec3(0.6, 0.57, 0.55) * (0.55 + 0.6 * lit) + glow * lit * low * low * 0.8 + light * (0.4 + 0.8 * lit);
    return vec4(c * a, a);
  }
  // A bank of ground fog. k is 0 for the far bank and 2 for the near one. A bank lies low with an uneven top, is
  // stretched sideways, and rolls to the right. The near banks roll faster.
  vec4 fogBank(vec2 P, float k, vec3 light) {
    float t = time;
    vec2 p = vec2(P.x * (1.1 - 0.25 * k), P.y * (3.4 - 0.8 * k)) + vec2(13.1, 7.7) * k;
    p.x -= t * (0.014 + 0.016 * k);
    vec2 w = vec2(fbm(p * 0.8 + vec2(0.0, 0.03 * t), 3), fbm(p * 0.8 + vec2(4.2, 1.7) - vec2(0.0, 0.025 * t), 3));
    float n = fbm(p + 1.5 * w + vec2(0.012 * t, 0.0), 4);
    float top = 0.44 - 0.11 * k + 0.18 * (w.y - 0.5);
    float h = 1.0 - smoothstep(top - 0.12, top + 0.08, P.y);
    float a = smoothstep(0.26, 0.68, n) * h * (0.5 + 0.12 * k);
    vec3 c = vec3(0.62, 0.64, 0.66) * (0.7 + 0.8 * n) + glow * (0.1 + 0.6 * h * (1.0 - P.y)) * 0.35 + light * 0.9;
    return vec4(c * a, a);
  }
  // Jungle mist that hangs in wisps. k is 0 for the far layer, which is higher up, and 2 for the near one. The wisps
  // are thin ridges of noise whose coordinates are moved by more noise. They drift slowly and curl as they go. The
  // middle layer drifts the other way.
  vec4 mist(vec2 P, float k, vec3 light) {
    float t = time;
    vec2 p = vec2(P.x * (0.8 + 0.3 * k), P.y * (2.6 + 0.5 * k)) + vec2(5.3, 2.9) * k;
    p.x += t * (0.012 + 0.008 * k) * (k == 1.0 ? -1.0 : 1.0);
    vec2 w = vec2(fbm(p * 1.4 + vec2(0.0, 0.03 * t), 3), fbm(p * 1.4 + vec2(3.7, 1.1) - vec2(0.0, 0.025 * t), 3));
    float n = fbm(p + 1.6 * w, 4);
    float wisp = pow(1.0 - abs(2.0 * n - 1.0), 4.0);
    float away = (P.y - 0.55 + 0.15 * k) / 0.25;
    float band = exp(-away * away);
    float a = wisp * band * (0.35 + 0.1 * k);
    vec3 c = vec3(0.62, 0.6, 0.56) * (0.7 + 0.5 * n) + glow * 0.35 + light * 1.4;
    return vec4(c * a, a);
  }
  // Damp air that hangs over a swamp: a soft haze, thicker low down. It is in slow billows that rise off the water
  // and drift, and thicken and thin as they go.
  vec4 damp(vec2 P, vec3 light) {
    float t = time;
    vec2 p = vec2(P.x * 1.3, P.y * 1.8) - vec2(0.01, 0.012) * t;
    vec2 w = vec2(fbm(p * 0.9 + vec2(0.0, 0.02 * t), 3), fbm(p * 0.9 + vec2(6.1, 2.4) + vec2(0.015 * t, 0.0), 3));
    float n = fbm(p + 1.4 * w, 4);
    float low = 1.0 - smoothstep(-0.1, 1.0, P.y);
    float a = (0.05 + 0.27 * smoothstep(0.35, 0.8, n)) * (0.4 + 0.6 * low);
    vec3 c = vec3(0.56, 0.62, 0.56) * (0.75 + 0.5 * n) + glow * 0.3 * low + light * 0.8;
    return vec4(c * a, a);
  }
  // Sparks that rise, sway and flicker. A grid moves up, and a few of its cells have one spark each.
  float embers(vec2 p, float t) {
    float e = 0.0;
    for (int k = 0; k < 2; k++) {
      float n = float(k);
      vec2 c = p * (4.0 + 3.0 * n) - vec2(0.0, t * (0.45 + 0.2 * n));
      c.x += 0.3 * sin(0.8 * c.y + 0.5 * t + 2.0 * n);
      vec2 id = floor(c);
      float h = hash(id + 31.7 * n);
      if (h > 0.05) continue;
      vec2 at = 0.2 + 0.6 * vec2(hash(id + 4.1), hash(id + 9.3));
      float flicker = 0.6 + 0.4 * sin(t * (2.0 + 6.0 * hash(id + 2.3)) + 120.0 * h);
      e += (1.0 - smoothstep(0.0, 0.1, length(fract(c) - at))) * flicker;
    }
    return e;
  }
  // A flake: of snow, or a leaf. A grid falls at vel and sways from side to side, and some of its cells have one
  // flake each. Returns how much of the flake covers P, and how much light the flake catches as it turns. many is
  // the share of the cells that have a flake, from 0 to 1. r is how big the flake is in its cell, blur how soft its
  // edge is, squash how much longer than wide it is, and spin how fast it turns.
  vec2 flake(vec2 P, float cells, vec2 vel, float sway, float many, float r, float blur, float squash, float spin,
      float seed) {
    vec2 c = (P - vel * time) * cells;
    c.x += sway * sin(0.7 * c.y + 0.8 * time + seed);
    vec2 id = floor(c);
    float h = hash(id + seed);
    if (h > many) return vec2(0.0);
    vec2 f = fract(c) - (0.25 + 0.5 * vec2(hash(id + seed + 4.1), hash(id + seed + 9.3)));
    float turn = time * spin * (0.5 + hash(id + seed + 2.3)) + 50.0 * h;
    float cs = cos(turn), sn = sin(turn);
    f = vec2(cs * f.x - sn * f.y, sn * f.x + cs * f.y);
    f.y *= squash;
    return vec2(1.0 - smoothstep(r * (1.0 - blur), r * (1.0 + blur), length(f)), abs(cs));
  }
  // Light snow at three depths, blown a little to the right. The near flakes are bigger, faster and softer.
  vec4 snow(vec2 P, vec3 light) {
    vec4 acc = vec4(0.0);
    for (int k = 0; k < 3; k++) {
      float n = float(k);
      float a = flake(P, 15.0 - 4.0 * n, vec2(0.03 + 0.02 * n, -(0.06 + 0.04 * n)), 0.3, 0.28 - 0.06 * n,
        0.07 + 0.01 * n, 0.3 + 0.25 * n, 1.0, 0.0, 41.0 + 13.0 * n).x * (0.28 + 0.1 * n);
      acc = over(acc, vec4((vec3(0.8, 0.85, 0.9) + spark * 0.12 + light * 0.4) * a, a));
    }
    return acc;
  }
  // Leaves at two depths. They drift down slowly, sway wide and turn over. They are dark green, and catch the glow.
  vec4 leaves(vec2 P, vec3 light) {
    vec4 acc = vec4(0.0);
    for (int k = 0; k < 2; k++) {
      float n = float(k);
      vec2 f = flake(P, 6.0 - 2.0 * n, vec2(0.02 + 0.015 * n, -(0.035 + 0.025 * n)), 0.6, 0.14, 0.1, 0.2 + 0.3 * n,
        2.6, 0.8, 7.0 + 23.0 * n);
      float a = f.x * (0.6 + 0.15 * n);
      vec3 c = vec3(0.17, 0.25, 0.08) * (0.5 + f.y) + glow * 0.25 * f.y + light * 0.4;
      acc = over(acc, vec4(c * a, a));
    }
    return acc;
  }
  // A light drizzle at two depths: fine, faint streaks that fall steadily at a slight slant. The slant sways slowly.
  // Some places have more streaks than others, and that comes and goes. The near drops are longer and softer. Each
  // streak is brightest at its head and fades along its tail.
  float rain(vec2 P) {
    float t = time;
    float slant = 0.07 + 0.03 * sin(t * 0.05);
    float some = smoothstep(0.2, 0.8, noise(vec2(P.x * 0.8 - t * 0.04, P.y * 0.5 + t * 0.02)));
    float r = 0.0;
    for (int k = 0; k < 2; k++) {
      float n = float(k);
      vec2 size = vec2(60.0 - 25.0 * n, 4.0 - 1.2 * n); // cells to the height of the page, across and down
      vec2 c = (vec2(P.x + slant * P.y, P.y) + vec2(0.0, t * (0.55 + 0.35 * n))) * size + 17.0 * n;
      vec2 id = floor(c);
      if (hash(id + 13.0 * n) > 0.07 + 0.13 * some) continue;
      vec2 f = (fract(c) - vec2(0.2 + 0.6 * hash(id + 3.3), 0.25 + 0.5 * hash(id + 7.7))) / size;
      float streak = 0.016 + 0.014 * n;
      float along = (f.y + 0.5 * streak) / streak; // 0 at the head of the streak, 1 at the end of its tail
      if (along < 0.0 || along > 1.0) continue;
      float across = 1.0 - smoothstep(0.0, 0.0018 + 0.0008 * n, abs(f.x));
      r += across * pow(1.0 - along, 1.5) * (0.22 + 0.08 * n) * (0.5 + hash(id + 5.1));
    }
    return r;
  }
  // Fireflies that wander slowly about their cells, and glow now and then.
  float flies(vec2 P) {
    float t = time, g = 0.0;
    for (int k = 0; k < 2; k++) {
      float n = float(k);
      vec2 c = P * (4.5 + 3.5 * n) + vec2(t * 0.03, 0.0) + 3.7 * n;
      vec2 id = floor(c);
      float h = hash(id + 11.0 * n);
      if (h > 0.45) continue;
      vec2 to = 0.5 + 0.27 * vec2(sin(t * (0.25 + 0.35 * hash(id + 1.3)) + 6.28 * hash(id + 2.7)),
        sin(t * (0.2 + 0.35 * hash(id + 3.1)) + 6.28 * hash(id + 4.9)));
      vec2 d = fract(c) - to;
      float dd = dot(d, d);
      float on = smoothstep(0.3, 1.0, sin(t * (0.5 + 0.7 * hash(id + 5.3)) + 60.0 * h));
      g += on * (exp(-dd / 0.0005) + 0.35 * exp(-dd / 0.008));
    }
    return g;
  }
  // Motes of dust at three depths. They drift slowly every way, and twinkle as they turn.
  float dust(vec2 P) {
    float t = time, g = 0.0;
    for (int k = 0; k < 3; k++) {
      float n = float(k);
      vec2 c = (P - vec2(0.006 + 0.004 * n, 0.004 - 0.004 * n) * t) * (15.0 - 4.0 * n) + 7.3 * n;
      vec2 id = floor(c);
      float h = hash(id + 5.0 * n);
      if (h > 0.4) continue;
      vec2 to = 0.5 + 0.3 * vec2(sin(t * 0.13 * (1.0 + hash(id + 1.0)) + 6.28 * hash(id + 2.0)),
        cos(t * 0.11 * (1.0 + hash(id + 3.0)) + 6.28 * hash(id + 4.0)));
      float r = 0.045 + 0.02 * n;
      float twinkle = 0.5 + 0.5 * sin(t * (0.6 + hash(id + 6.0)) + 30.0 * h);
      g += (1.0 - smoothstep(r * 0.25, r, length(fract(c) - to))) * twinkle * (0.55 + 0.2 * n);
    }
    return g;
  }
  // An alarm light that turns, high in the middle and just out of sight. Its beam sweeps round, and glares as it
  // faces the viewer.
  vec3 alarm(vec2 P, float aspect) {
    vec2 v = P - vec2(aspect * 0.5, 1.12);
    float dist = length(v);
    float sweep = pow(max(cos(atan(v.y, v.x) - alarmAngle), 0.0), 8.0);
    float glare = exp(-dist * 4.0) * (0.2 + 0.8 * pow(max(-sin(alarmAngle), 0.0), 4.0));
    return spark * (sweep / (1.0 + 1.2 * dist) + glare);
  }
  // The faint beam of a projector, from high behind: a cone that widens down to the middle, with rays through it.
  float beam(vec2 P, float aspect) {
    vec2 v = P - vec2(aspect * 0.5, 1.4);
    float ang = atan(v.x, -v.y);
    float dist = length(v);
    float cone = 1.0 - smoothstep(0.13, 0.27, abs(ang));
    float rays = (0.45 + 0.55 * noise(vec2(ang * 70.0, time * 0.25))) *
      (0.6 + 0.4 * noise(vec2(ang * 12.0 + 3.0, time * 0.08)));
    return 0.55 * cone * rays * smoothstep(0.35, 0.7, dist) * exp(-(dist - 0.4) * 1.1);
  }
  // The rays of the eclipse: streaks that turn slowly round it, from just past its disc.
  float rays(vec2 P) {
    vec2 v = P - sunAt;
    float dist = length(v);
    vec2 way = v / max(dist, 0.001);
    float streaks = noise(way * 6.0 + vec2(time * 0.02, 0.0)) *
      (0.6 + 0.4 * noise(way * 13.0 - vec2(0.0, time * 0.03)));
    return (streaks * 0.7 * exp(-dist * 1.6) + 0.15 * exp(-dist * 6.0)) * smoothstep(0.04, 0.1, dist);
  }

  void main() {
    vec2 uv = gl_FragCoord.xy / res;
    vec2 P = gl_FragCoord.xy / res.y + shake; // in heights of the page
    float aspect = res.x / res.y;
    float t = time;
    float low = 1.0 - uv.y;
    // What lights the haze (light), and what glows of itself (add).
    vec3 light = vec3(0.0), add = vec3(0.0);
    if (withAlarm > 0.0) {
      vec3 a = alarm(P, aspect) * withAlarm;
      light += a * 0.8;
      add += a * 0.12;
    }
    if (lamp.r + lamp.g + lamp.b > 0.0) {
      vec3 l = lamp * (exp(-length(P - lampAt) * 1.6) + 0.25);
      light += l;
      add += l * 0.1;
    }
    // A rocket that launches out of sight, below the page. The light of its engines swells as they light, flickers a
    // little, then spreads and fades as the rocket climbs away. It lights the haze from under it, and the page a
    // little.
    if (launch.z > 0.0) {
      float age = launch.y;
      vec3 burn = vec3(1.0, 0.6, 0.26) * launch.z * smoothstep(0.0, 3.0, age) * (1.0 - smoothstep(7.0, 18.0, age)) *
        (0.92 + 0.08 * noise(vec2(t * 12.0, 0.0)));
      float dist = length(vec2((P.x - launch.x) / (1.0 + 0.08 * age), P.y + 0.3));
      add += burn * (exp(-dist * 2.5) * 0.5 + 0.03);
      light += burn * (exp(-dist * 1.8) * 1.4 + 0.1);
    }
    vec3 sun = vec3(0.0); // the rays of the eclipse, which light the mist
    if (withRays > 0.0) {
      sun = mix(vec3(1.0, 0.85, 0.65), spark, 0.4) * rays(P) * withRays;
      light += sun * 0.9;
    }
    // The layers, from back to front: the far fog and mist, the smoke, the near fog and mist, the damp air, the beam
    // and the rays, then what floats in front.
    vec4 acc = vec4(0.0);
    if (withFog > 0.0) {
      acc = over(acc, fogBank(P, 0.0, light) * withFog);
      acc = over(acc, fogBank(P, 1.0, light) * withFog);
    }
    if (withMist > 0.0) {
      acc = over(acc, mist(P, 0.0, light) * withMist);
      acc = over(acc, mist(P, 1.0, light) * withMist);
    }
    if (withSmoke > 0.0) acc = over(acc, smoke(P * 1.6, low, light) * withSmoke);
    if (withFog > 0.0) acc = over(acc, fogBank(P, 2.0, light) * withFog);
    if (withMist > 0.0) acc = over(acc, mist(P, 2.0, light) * withMist);
    if (withDamp > 0.0) acc = over(acc, damp(P, light) * withDamp);
    float beamed = 0.0; // how much the beam lights this place, which shows the dust in it
    if (withBeam + withRays > 0.0) {
      float haze = 0.3 + 0.7 * fbm(P * 2.2 + vec2(0.02, -0.012) * t, 4);
      vec3 b = vec3(0.0);
      if (withBeam > 0.0) b += mix(vec3(0.8, 0.88, 1.0), spark, 0.15) * beam(P, aspect) * withBeam;
      b += sun;
      beamed = dot(b, vec3(0.33));
      // Lit haze glows, and also veils a little of what is behind it.
      acc = over(acc, vec4(b * haze * 0.3, min(beamed * haze * 0.3, 0.4)));
      add += b * haze * 0.8;
    }
    if (withDust > 0.0) {
      float a = min(dust(P) * withDust * (withBeam > 0.0 ? 0.15 + 5.0 * beamed : 0.6), 1.0);
      acc = over(acc, vec4((mix(vec3(0.9, 0.88, 0.85), spark, 0.25) + light) * a, a));
    }
    if (withSnow > 0.0) acc = over(acc, snow(P, light) * withSnow);
    if (withRain > 0.0) {
      float a = min(rain(P) * withRain, 1.0);
      acc = over(acc, vec4((vec3(0.75, 0.8, 0.85) + glow * 0.2 + light * 1.5) * a, a));
    }
    if (withLeaves > 0.0) acc = over(acc, leaves(P, light) * withLeaves);
    if (withFlies > 0.0) {
      add += mix(vec3(0.75, 1.0, 0.35), spark, 0.4) * flies(P) * (1.0 - smoothstep(0.3, 0.85, P.y)) * withFlies * 0.9;
    }
    if (withEmbers > 0.0) {
      float e = min(embers(P * 2.2, t) * low * 0.8, 1.0) * withEmbers;
      acc = vec4(acc.rgb * (1.0 - e) + spark * e, acc.a + e * (1.0 - acc.a));
    }
    gl_FragColor = vec4(acc.rgb + add, acc.a);
  }`;

/**
 * Starts the atmosphere over the backdrop. It compiles the shader, and from then on draws the atmosphere of the map
 * that the backdrop shows, while the page shows. Does nothing in a browser without WebGL, or where the shader does
 * not link.
 */
export function atmosphereStart(): void {
  const el = $<HTMLCanvasElement>('atmosphere');
  const gl = el.getContext('webgl', { alpha: true, premultipliedAlpha: true, antialias: false, depth: false,
    stencil: false, powerPreference: 'low-power' });
  if (!gl) return;
  // Whether the browser draws WebGL without a GPU. A browser may refuse a context with failIfMajorPerformanceCaveat
  // there, which a context on a canvas of its own tests, and lets go at once. Chrome makes one anyway where it draws
  // with SwiftShader, and its renderer's name says so instead.
  const probe = document.createElement('canvas').getContext('webgl', { failIfMajorPerformanceCaveat: true });
  probe?.getExtension('WEBGL_lose_context')?.loseContext();
  const info = gl.getExtension('WEBGL_debug_renderer_info');
  const renderer = String(gl.getParameter(info ? info.UNMASKED_RENDERER_WEBGL : gl.RENDERER));
  const software = !probe || /swiftshader|llvmpipe|softpipe|software|basic render/i.test(renderer);
  // Where each uniform of the shader is. setUp fills it before anything draws.
  const uniforms = {} as Record<Uniform, WebGLUniformLocation | null>;
  // Compiles the shader, and sets the context up to draw with it. Returns false when the shader does not link, and
  // says why in the console, unless the browser took the context away.
  const setUp = (): boolean => {
    const program = gl.createProgram();
    for (const [type, source] of [[gl.VERTEX_SHADER, vertex], [gl.FRAGMENT_SHADER, fragment]] as const) {
      const shader = gl.createShader(type)!;
      gl.shaderSource(shader, source);
      gl.compileShader(shader);
      gl.attachShader(program, shader);
    }
    gl.bindAttribLocation(program, 0, 'pos');
    gl.linkProgram(program);
    if (!gl.getProgramParameter(program, gl.LINK_STATUS)) {
      if (!gl.isContextLost()) console.log('lobby: no atmosphere: ' + gl.getProgramInfoLog(program));
      return false;
    }
    gl.useProgram(program);
    // One triangle that covers the whole canvas.
    gl.bindBuffer(gl.ARRAY_BUFFER, gl.createBuffer());
    gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([-1, -1, 3, -1, -1, 3]), gl.STATIC_DRAW);
    gl.enableVertexAttribArray(0);
    gl.vertexAttribPointer(0, 2, gl.FLOAT, false, 0, 0);
    for (const name of UNIFORMS) uniforms[name] = gl.getUniformLocation(program, name);
    for (const layer of LAYERS) uniforms[layer] = gl.getUniformLocation(program, withName(layer));
    return true;
  };
  if (!setUp()) return;
  let ready = true; // the shader is set up: not while the context is lost, or when setting it up again failed
  const art = $<HTMLImageElement>('backdropimg'), dim = $('backdropdim'), grain = $('backdropgrain');
  const still = matchMedia('(prefers-reduced-motion: reduce)');
  // The clock of the atmosphere, in seconds. It runs only while the atmosphere draws. It starts somewhere in its
  // first 10 minutes, so that no two visits look the same. The shader gets it modulo 6 hours (draw), before the
  // coordinates of the noise grow too big for floats to keep the noise smooth.
  let clock = Math.random() * 600;
  let last = 0; // the time of the last moving frame, as requestAnimationFrame gives it
  let raf = 0; // the frame that the atmosphere has asked for, or 0

  // The atmosphere as it is drawn now: its colours, and how much there is of each layer and light. It goes over to
  // the atmosphere of the map in a second or so (ease). rgb turns a colour of the table into one for the shader.
  const rgb = (hex: string): Colour => [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255);
  const amounts = {} as Record<Amount, number>;
  for (const key of AMOUNTS) amounts[key] = 0;
  let glow: Colour | null = null, spark: Colour | null = null; // the colours now, or null before the first frame
  let sun: Pair = [0.5, 0.2]; // where the eclipse is in the art of the map, or of the last map while its rays fade
  // Moves the atmosphere a share k of the way to the atmosphere of the map, or all the way with 1. The first frame
  // takes the colours of the map as they are. An amount that is within 0.002 of where it goes is set to that, so a
  // layer that fades out gets to 0 and is no longer drawn.
  const ease = (k: number): void => {
    const want = Object.hasOwn(atmospheres, backdropMap) ? atmospheres[backdropMap] : plainAtmosphere;
    sun = want.sun || sun;
    const [g, s] = [want.glow, want.spark].map(rgb);
    glow = glow ? glow.map((c, i) => c + (g[i] - c) * k) : g;
    spark = spark ? spark.map((c, i) => c + (s[i] - c) * k) : s;
    for (const key of AMOUNTS) {
      const to = want[key] || 0;
      amounts[key] = Math.abs(to - amounts[key]) < 0.002 ? to : amounts[key] + (to - amounts[key]) * k;
    }
  };

  // The lights that come and go. Each is computed from the clock alone, with numbers from 0 to 1 that are the same
  // for the same n (hashed). A light is then where the clock is, and not where a timer got to.
  const hashed = (n: number): number => {
    const x = Math.sin(n * 91.3458 + 47.123) * 47453.5453;
    return x - Math.floor(x);
  };
  // Goes smoothly from 0 at a to 1 at b, as smoothstep does in the shader.
  const smooth = (a: number, b: number, x: number): number => {
    const k = Math.min(1, Math.max(0, (x - a) / (b - a)));
    return k * k * (3 - 2 * k);
  };
  // A burst of quick flickers that fades, now and then. The clock is cut into slots of a number of seconds (every),
  // and a share of the slots have a burst (chance). The burst starts at some point in its slot, and lasts 0.35 to
  // 1.25 s. Returns how bright the burst is now, 0 for none, and a number from 0 to 1 that stays the same through
  // the burst. Each light has its own bursts (seed).
  const burst = (every: number, chance: number, seed: number): [number, number] => {
    const slot = Math.floor(clock / every);
    for (const s of [slot, slot - 1]) {
      if (hashed(s + seed) > chance) continue;
      const dt = clock - (s * every + hashed(s + seed + 0.5) * (every - 1.5));
      const length = 0.35 + 0.9 * hashed(s + seed + 0.25);
      if (dt < 0 || dt > length) continue;
      const on = hashed(Math.floor(clock * 17) + s * 7.1 + seed) > 0.45 ? 1 : 0.12;
      return [on * Math.sqrt(1 - dt / length), hashed(s + seed + 0.75)];
    }
    return [0, 0];
  };
  // A rocket that launches out of sight, now and then. Most slots of 50 seconds have a launch. It starts somewhere
  // in the first 30 seconds of the slot, and lasts 18 seconds. Returns how far across the page the rocket is, as a
  // share of the width, the seconds since its engines lit, and how much of it there is: 1, or 0 for none.
  const launch = (): [number, number, number] => {
    const s = Math.floor(clock / 50);
    const age = clock - (s * 50 + hashed(s + 200.5) * 30);
    if (hashed(s + 200) > 0.85 || age < 0 || age > 18) return [0, 0, 0];
    return [0.2 + 0.6 * hashed(s + 200.25), age, 1];
  };
  // The rumble of the launch, a light one, as the engines light and the rocket lifts off. Returns how far it shakes
  // the page now, in pixels, across and down.
  const rumble = (): Pair => {
    const [, age, there] = launch();
    const k = 1.8 * there * amounts.launch * smooth(1, 3.5, age) * (1 - smooth(6, 12, age));
    return [k * (Math.sin(age * 41) + 0.6 * Math.sin(age * 67 + 1.3)) / 1.6,
      k * (Math.sin(age * 53 + 0.7) + 0.6 * Math.sin(age * 79 + 2.1)) / 1.6];
  };

  // Film grain, for Kino der Toten: a tile of noise that is laid over the atmosphere (#backdropgrain). It moves at
  // each frame of the film, 24 times a second (draw).
  let grainMade = false, grainFrame = -1;
  const makeGrain = (): void => {
    grainMade = true;
    const c = document.createElement('canvas');
    c.width = c.height = 256;
    const g = c.getContext('2d')!;
    const noise = g.createImageData(256, 256);
    for (let i = 0; i < noise.data.length; i += 4) {
      const v = 128 + ((Math.random() + Math.random() + Math.random()) / 3 - 0.5) * 255;
      noise.data[i] = noise.data[i + 1] = noise.data[i + 2] = v;
      noise.data[i + 3] = 255;
    }
    g.putImageData(noise, 0, 0);
    grain.style.backgroundImage = 'url(' + c.toDataURL() + ')';
  };
  // Sets the opacity of an element, and leaves its style alone when the opacity stays the same.
  const setOpacity = (e: HTMLElement, v: number): void => {
    const s = v.toFixed(3);
    if (e.style.opacity !== s) e.style.opacity = s;
  };

  // The camera drifts slowly over the art: in and out, and from side to side. Its rounds last a minute or more and
  // don't keep step, so the drift never quite comes back the same. The art is zoomed in enough that its edges never
  // show.
  let shift: Pair = [0, 0], zoom = 1, shaken: Pair = [0, 0]; // the drift now, and the rumble, in pixels
  const drift = (): void => {
    const turn = (seconds: number, from = 0): number => Math.sin(clock * Math.PI * 2 / seconds + from);
    shaken = amounts.launch ? rumble() : [0, 0];
    shift = [0.018 * art.clientWidth * turn(97) + shaken[0], 0.01 * art.clientHeight * turn(83, 1) + shaken[1]];
    zoom = 1.1 + 0.035 * turn(71);
    art.style.transform = 'translate(' + shift[0].toFixed(2) + 'px, ' + shift[1].toFixed(2) + 'px) scale(' +
      zoom.toFixed(5) + ')';
  };
  // Returns where a point of the art is on the page, in heights of the page from its bottom left. The point is
  // across and down the art, from 0 to 1. The art fills its box (object-fit: cover), and has drifted. Art that has
  // not loaded counts as 16 by 9, the shape of a loading screen.
  const onArt = ([x, y]: Pair): Pair => {
    const w = art.clientWidth, h = art.clientHeight;
    const iw = art.naturalWidth || 16, ih = art.naturalHeight || 9;
    const fit = Math.max(w / iw, h / ih) * zoom;
    const px = art.offsetLeft + w / 2 + shift[0] + (x - 0.5) * iw * fit;
    const py = art.offsetTop + h / 2 + shift[1] + (y - 0.5) * ih * fit;
    return [px / el.clientHeight, 1 - py / el.clientHeight];
  };

  // Whether the atmosphere shows: no game has the page, the tab is in sight, and the browser has not taken the
  // context away.
  const showing = (): boolean => !overlay.classList.contains('hidden') && !document.hidden && !gl.isContextLost();
  // Draws a frame. With moving false, it is the still frame: no light comes or goes, and the grain stays put.
  const draw = (moving: boolean): void => {
    // The canvas has half the size of the page, and at most 1000 pixels on its longer side.
    const scale = Math.min(1 / 2, 1000 / Math.max(el.clientWidth, el.clientHeight, 1));
    const width = Math.max(1, Math.round(el.clientWidth * scale));
    const height = Math.max(1, Math.round(el.clientHeight * scale));
    if (el.width !== width || el.height !== height) {
      el.width = width;
      el.height = height;
    }
    gl.viewport(0, 0, width, height);
    gl.uniform2f(uniforms.res, width, height);
    gl.uniform1f(uniforms.time, clock % 21600);
    // The colours are set: each frame eases before it draws (frame).
    gl.uniform3fv(uniforms.glow, glow!);
    gl.uniform3fv(uniforms.spark, spark!);
    for (const layer of LAYERS) gl.uniform1f(uniforms[layer], amounts[layer]);
    gl.uniform1f(uniforms.alarmAngle, (-clock * Math.PI * 2 / 4.5) % (Math.PI * 2));
    // The flashes of the teleporters, at one side of the page or the other.
    const aspect = width / height;
    const [flash, side] = moving && amounts.flashes ? burst(5, 0.8, 0) : [0, 0];
    const f = flash * amounts.flashes;
    gl.uniform3f(uniforms.lamp, 0.55 * f, 0.75 * f, f);
    gl.uniform2f(uniforms.lampAt, (side < 0.5 ? 0.1 : 0.9) * aspect, 0.3);
    // The launch of a rocket, and its rumble.
    const [from, age, there] = moving && amounts.launch ? launch() : [0, 0, 0];
    gl.uniform3f(uniforms.launch, from * aspect, age, there * amounts.launch);
    gl.uniform2f(uniforms.shake, -shaken[0] / el.clientHeight, shaken[1] / el.clientHeight);
    // The eclipse.
    if (amounts.rays) gl.uniform2fv(uniforms.sunAt, onArt(sun));
    gl.drawArrays(gl.TRIANGLES, 0, 3);
    el.classList.add('shown'); // the canvas fades in with its first frame (page.css)
    // What the page draws itself. The flicker of the film and the lights that falter darken the backdrop. The film
    // grain lies over it, and moves at each frame of the film.
    const flicker = moving ? (0.008 + 0.017 * hashed(Math.floor(clock * 24))) * amounts.film : 0;
    const falter = moving && amounts.faltering ? burst(9, 0.6, 100)[0] * 0.2 * amounts.faltering : 0;
    setOpacity(dim, Math.max(flicker, falter));
    if (amounts.film && !grainMade) makeGrain();
    setOpacity(grain, 0.3 * amounts.film);
    const filmFrame = Math.floor(clock * 24);
    if (amounts.film && moving && filmFrame !== grainFrame) {
      grainFrame = filmFrame;
      grain.style.transform = 'translate(' + Math.floor(hashed(filmFrame) * 256) + 'px, ' +
        Math.floor(hashed(filmFrame + 0.5) * 256) + 'px)';
    }
  };
  // Draws a frame, and asks for the next one while the atmosphere shows and moves.
  const frame = (now: number): void => {
    raf = 0;
    if (!ready || !showing()) return;
    if (still.matches || software) {
      // One still frame: the atmosphere of the map at once, and the art where it lies.
      ease(1);
      art.style.transform = '';
      shift = [0, 0];
      shaken = [0, 0];
      zoom = 1;
      draw(false);
      return;
    }
    const step = Math.min(now - last, 100) / 1000; // at most 0.1 s: the clock stops while nothing is drawn
    last = now;
    clock += step;
    ease(1 - Math.exp(-step / 0.6));
    drift();
    draw(true);
    raf = requestAnimationFrame(frame);
  };
  // Has the atmosphere draw again, when it shows and has not asked for a frame already.
  const wake = (): void => {
    if (raf || !ready || !showing()) return;
    raf = requestAnimationFrame(frame);
  };
  // The atmosphere wakes when the overlay shows again after a game, when the tab comes back into sight, when the
  // window changes size, when the player's wish for less motion changes, and when the map changes (setBackdrop).
  new MutationObserver(wake).observe(overlay, { attributes: true, attributeFilter: ['class'] });
  document.addEventListener('visibilitychange', wake);
  window.addEventListener('resize', wake);
  still.addEventListener && still.addEventListener('change', wake); // absent in Safari before 14
  // A browser gives a lost context back only to a page that prevents the default of its loss. The shader is then
  // set up again.
  el.addEventListener('webglcontextlost', (e) => {
    e.preventDefault();
    ready = false;
  });
  el.addEventListener('webglcontextrestored', () => {
    ready = setUp();
    wake();
  });
  atmosphereWake = wake;
  wake();
}
