// web/src/settings.ts - the game's options menu, rebuilt in the page.
//
// The player changes their settings outside a game, in the lobby, as they would in the game's own menu (the Settings
// and Controls of its pause menu). The page shows the menu from the button at the top (main.ts: settingsInit). The
// next game that the player plays in this browser starts with what they set.
//
// The menu is the in-game menu of the SP game's Zombies, as the menus in code_post_gfx.ff have it (options_new_pc,
// options_controls_new_pc and the pages they open, which bo1_menudump reads). It has the same pages and tabs, and the
// same rows in the same places, with the same choices, ranges and text. The text is the game's own. The server reads
// it from the game files (GET /strings: server/mapart.go), so none of it is copied here. With a server that has no
// game files, the menu shows the names of the strings, made readable (text). The menu is laid out on the game's
// 640x480 screen, as at 1280x720, and scaled to the window (at, fit). It opens as the game's menu does, on a screen's
// tabs alone. A click on a tab opens its page. Esc, or the gamepad's B, closes the page, back to the tabs alone, and
// then closes the menu.
//
// What differs from the game's menu:
//   - A row whose setting does nothing in the browser, or in Zombies, is greyed out, as the game greys out what
//     can't be changed. The menu says why at the foot of the tabs' column while the mouse is over the row (OFF).
//     These rows are: v-sync (the browser shows the picture and paces it), texture filtering above anisotropy 1 (the
//     web client filters trilinear then), shader warming (the web client compiles its pipelines from the map's list
//     instead), the sound device (it is the browser's), voice chat (the web client has none), the screenshot key (a
//     screenshot is saved nowhere that the player sees), and accepting an invite and quick save (Zombies has
//     neither).
//   - The menu says things at the foot of the tabs' column (say): why a row is greyed out, what the aspect ratio's
//     Auto is, the key that sets Full Screen or Show FPS in a game (TIP), that a key's row changes at a click, and
//     what the menu waits for. The game's menu has a status line under the page instead, which says what a key's row
//     does (EXE_KEYCHANGE: Press ENTER or CLICK to change). Here that line would lie across the last two rows of
//     Graphics (brightness, field of view), and the rows here take no Enter.
//   - Anti-aliasing is Off or 4X, which is what the web client draws (WebGPU's two). The game's menu has 2X, 8X and
//     16X too.
//   - The video mode and the aspect ratio are the page's. The page sizes the picture when a game starts
//     (backBuffer). The size is the screen's resolution, halved each way on a screen of 1800 lines or more, or a
//     standard resolution in the same shape (bo1_web_resolution, which only the web client has). The shape is the
//     one that the aspect ratio picks, with black bars where it is not the screen's shape. The Auto of each row
//     shows what it picks: the resolution, or the shape. While the mouse is over the aspect ratio, the menu says how
//     its Auto picks.
//   - Full Screen is the browser's full screen (bo1_web_fullscreen, which only the web client has:
//     bo1-zombies/web/src/web_main.cpp). It says whether the page goes full screen when a click, a key or a press
//     of a gamepad takes the player into a game. The Full Screen of the game's own menu is the same setting. A
//     player who leaves full screen in a game, or goes back to it, sets it too.
//   - On mobile (mobile: a phone or a tablet), a new player's settings are lighter than the game's recommended
//     settings, which are a PC's. They have no anti-aliasing, no anisotropy and no shadows, and the video mode's
//     Auto is 720 lines (RECOMMENDED, MOBILE_LINES).
//   - The game changes some graphics settings only outside a game: it greys them out in a game, and applies them
//     with vid_restart. Here they change like the rest. Each change goes to the player's config at once, and the
//     next game starts with it.
//   - The Gamepad page is there while Chrome shows the page a gamepad. The game's is there while a gamepad is there
//     or on. The page shows all its rows. Enable Gamepad is greyed out, because the game turns it on and off itself.
//   - The Gamepad page has Controller Vibration: how strong the gamepad's rumble is (bo1_rumble, which the web
//     client and the Windows client have: bo1-zombies/src/win32/win_gamepad.cpp). It is in the space that the game's
//     PC menu leaves for a row there. The gamepad rumbles at the new strength when the row is set. Under the game's
//     rows the page has Aim Assist too, the gamepad's (bo1_aimassist: bo1-zombies/src/aim_assist/aim_assist.cpp).
//     The controller's picture is a row lower, to make room for it. The text of both rows is the game's, from its
//     console menus. The game's files have neither row. The game's menu has both, as here
//     (bo1-zombies/src/ui/ui_main.cpp: UI_GamepadRows_PatchMenus).
//   - The slider of the field of view shows its number, in whole degrees.
//   - Where the game's menu has Screen Refresh Rate, this menu has Frame Rate Limit (bo1_maxfps), as the game's menu
//     has too (bo1-zombies/src/ui/ui_main.cpp: UI_FrameLimit_PatchMenus). Screen Refresh Rate is a setting of an
//     exclusive full screen, which the browser does not have. The row's text is the page's own (OWN). Its choices
//     are limits of 30, 60 and more frames a second, and Unlimited, in the game's text, which is the display's rate
//     (frameLimits). Each limit says the most that the browser draws evenly within it at the display's refresh
//     rate: 48 for 60 at 144 Hz. The page measures the display's rate (measureRefresh). The game's menu uses that
//     rate too, until the game has measured its own (web/public/player.js: Module.bo1RefreshHz). The default is
//     Unlimited. On mobile it is 90, which is every second refresh of a faster display, to spare a phone's battery
//     and its heat (DEFAULTS).
//   - The Game page has Show FPS, the game's small frame rate counter (bo1_fpscounter:
//     bo1-zombies/src/client_mp/cl_scrn_mp.cpp), under the game's two rows. The game's menu has it too
//     (bo1-zombies/src/ui/ui_main.cpp: UI_ShowFps_PatchMenus). In a game F9 turns the counter on and off, and a
//     player with only a gamepad, as on mobile, has no key for that. The game has no text for the row, so the text
//     is the page's own (OWN), in English, as it is in the game's menu.
//
// The menu plays the game's sounds where the game's menu plays them (SOUND). The page plays them for it (sounds.ts).
//
// The player's settings are their config: players/config.cfg in the browser's store of game files
// (web/public/store.js). The engine reads the config when a game starts. It writes the config when the player
// changes a setting in the game, and when the game ends (bo1-zombies/web/src/web_main.cpp). The engine starts each
// game with its own defaults (bo1-zombies/src/qcommon/common.cpp: Com_ExecStartupConfigs,
// com_zombiesDefaultControls), and then runs the config. A setting that the config lacks is therefore at its
// default, and this menu shows it so (DEFAULTS, DEFAULT_BINDS: what a fresh game writes, which the game tests
// check: e2e/game/engine/solo.spec.ts). A player's first game sets the recommended settings after the config, and
// then runs the config again, so what the player set here before that game stays (Com_CheckSetRecommended).
//
// A change rewrites the config's line for the setting, or adds one, and leaves the rest as it is. A change of a key
// rewrites all the config's keys (unbindall, then each key's command), as the engine writes them. The menu changes
// nothing while a game runs in one of the player's tabs, because that game would write the config when it ends. A
// tab that plays holds the lock zombies-game (web/public/player.js). The menu then says to change the settings in
// the game's menu.
//
// What a page with the menu provides. The menu makes its own element, #options, at the end of the page's body
// (build). It takes no element of the page by its id. Any page has the menu with this module, the menu's style
// sheet, and the following.
//   - The style sheet, web/src/settings.css, which the page loads, with the font that the sheet loads, oswald.woff2.
//     A class of the sheet closes the menu (#options.hidden). No other element of the page has the id options.
//   - This module, which exports bo1Settings.
//   - bo1Settings.open('settings' or 'controls', closed) opens the menu, and closed() runs when it closes. close()
//     closes it. shown() says whether it is open. available is false where the browser has no private file system
//     for the config, and open does nothing then.
//   - The mouse and the keyboard are the menu's own while it is open: the arrows, Enter and Esc (keyWhileWaiting,
//     which hears a key before the page does). A gamepad is the page's to read, as it reads it for itself
//     (padnav.ts: padNav). The page gives the menu each press of the d-pad, A, B and the shoulder buttons, and each
//     repeat of one that is held: pad('up'), 'down', 'left', 'right', 'a', 'b', 'lb', 'rb'. The page closes the menu
//     at Start (close). The page's body has the class padnav while the gamepad has the page: from a press of the
//     gamepad until the mouse moves. The menu shows its cursor while the body has that class (padding). The menu
//     sets the class itself at an arrow key, and removes it when the mouse moves over the menu. gamepadsChanged()
//     says that the gamepads that the page reads came or went, which the page may know sooner than Chrome's events
//     say it. A page that reads no gamepad has the menu with the mouse and the keyboard.
//   - The game's sounds, where the page plays them: useSounds(play), where play(alias) plays uin_navigation_over,
//     uin_navigation_click or uin_navigation_submenu_over (SOUND). Without it the menu is silent. onVolume(fn) has
//     fn() run when the player changes a volume, for a page that plays at the player's volumes (sfxGain, musicGain).
//   - From the server, at the root of the page's origin: GET /strings?keys=<names, with commas between them>, a
//     JSON object of the game's text by those names (loadTexts). The menu asks for all its names in one request
//     when it first opens: some 240 names, in a URL of 4.5 KB. A name that the answer lacks shows as the name made
//     readable, and so does every name when there is no answer. And GET /images/<name>.png, the pictures of the
//     Gamepad page: xenon_controller_top, xenon_stick_move, xenon_stick_turn, xenon_stick_move_turn and
//     xenon_stick_move_look (padPicture). A picture that the server lacks is left out.
//   - From the browser: its private file system (navigator.storage.getDirectory), where the config is
//     players/config.cfg, as the engine has it (web/public/store.js). And Web Locks, by which the menu knows that a
//     game runs in one of the player's tabs (the lock zombies-game: web/public/player.js, LOCK). ?mobile=1 or
//     ?mobile=0 in the page's URL says whether the page is on mobile, in place of what the browser says (mobile).
//
// The engine's host takes the player's settings from the same object (web/public/player.js: settings), which the
// page hands it (main.ts: makePlayer). The host calls saved(), backBuffer(), cinematicGain(), fullScreen() and
// refreshHz(), and reads mobile, maxFpsDefault and recommended.
import { el } from './dom';

/** A screen of the menu. */
export type ScreenName = 'settings' | 'controls';

/** A button or a direction of a gamepad that the menu takes, by the name that the page gives it (padnav.ts). */
export type PadButton = 'up' | 'down' | 'left' | 'right' | 'a' | 'b' | 'lb' | 'rb';

/** A size in pixels. */
export interface Size {
  width: number;
  height: number;
}

/** The options menu and the player's settings, as the page and the engine's host use them. */
export interface Settings {
  /** Whether the browser has a private file system for the config. Without one, open does nothing. */
  readonly available: boolean;
  /**
   * Whether the page is on mobile, a phone or a tablet. The page tells the engine (web/public/player.js:
   * Module.bo1Mobile).
   */
  readonly mobile: boolean;
  /**
   * Mobile's recommended settings, by the dvars' names. The page gives them to the engine (web/public/player.js:
   * Module.bo1Recommended). The object is empty on a PC, where the recommended settings are the game's own.
   */
  readonly recommended: Record<string, string>;
  /**
   * Opens the menu on a screen, 'settings' when none is given. closed() runs when the menu closes. Returns false
   * where the menu is not available.
   */
  open(which?: ScreenName, closed?: () => void): Promise<boolean>;
  /** Closes the menu, if it is open. */
  close(): void;
  /**
   * Sets the page's player of the game's sounds: play(alias) (sounds.ts: playSound). Without one the menu is
   * silent.
   */
  useSounds(play: (alias: string) => void): void;
  /**
   * Returns the gain that the game plays a menu's sound at, to be multiplied by the volume of the sound's alias. It
   * is the square of the sound effects volume times the square of the master volume, as the player set them
   * (bo1-zombies/src/sound/snd.cpp). The volumes are those of the next game, or of a slider while the player drags
   * it. They are the defaults on a page whose config the menu has not read yet (load).
   */
  sfxGain(): number;
  /** Returns the gain that the game plays a menu's music at, as sfxGain does, from the music volume. */
  musicGain(): number;
  /**
   * Returns the gain that the game plays a movie at, to be multiplied by the movie's own volume. It is the square of
   * the cinematics volume times the square of the master volume (bo1-zombies/src/gfx_d3d/r_cinematic.cpp:
   * R_Cinematic_PlayerVolume). The page gives it to the engine for the loading movie, which the engine starts before
   * it reads the config (web/public/player.js: start).
   */
  cinematicGain(): number;
  /**
   * Has fn() run when the gains may have changed: while the player drags a volume's slider, and when the config is
   * saved or read.
   */
  onVolume(fn: () => void): void;
  /** Whether the menu is open. */
  shown(): boolean;
  /** Takes a press of a gamepad's button, as the page reads it while the menu is open. It moves the menu's cursor. */
  pad(button: PadButton): void;
  /** Says that the gamepads that the page reads changed (padnav.ts: padNav). The Gamepad tab goes live or greys out. */
  gamepadsChanged(): void;
  /**
   * Returns the display's refresh rate in hertz, as last measured. It is 0 before the first measurement, and when the
   * page draws too slowly to tell. The page gives it to the engine (web/public/player.js: Module.bo1RefreshHz).
   */
  refreshHz(): number;
  /**
   * The default of the Frame Rate Limit (bo1_maxfps). The page gives it to the engine (web/public/player.js:
   * Module.bo1MaxFpsDefault).
   */
  readonly maxFpsDefault: string;
  /**
   * Returns a promise that is done once the saves made so far are done. A game starts after that
   * (web/public/player.js: start).
   */
  saved(): Promise<void>;
  /**
   * Reads the config, and returns the back buffer that the player's settings pick. The page sizes the canvas to it
   * when a game starts (web/public/player.js: start).
   */
  backBuffer(): Promise<Size>;
  /**
   * Returns the player's Full Screen setting, as their config has it once it is read (load, which backBuffer does).
   * The page goes by it when a game starts, until the engine has read the config too (web/public/player.js:
   * Module.bo1FullScreen).
   */
  fullScreen(): boolean;
  /** Reads the player's config from the browser's store. The page's volumes follow it (onVolume). */
  load(): Promise<void>;
}

// Whether the browser has a private file system for the config.
const available = typeof navigator !== 'undefined' && !!navigator.storage && !!navigator.storage.getDirectory;

// The browser's client hints, which Chromium has (navigator.userAgentData) and TypeScript's DOM types lack.
interface UserAgentData {
  mobile?: boolean;
  platform?: string;
}

// Returns the browser's client hints, or undefined in a browser that is not Chromium.
const userAgentData = (): UserAgentData | undefined =>
  (navigator as unknown as { userAgentData?: UserAgentData }).userAgentData;

// Whether the page is on mobile, a phone or a tablet, as the browser says: Android, an iPhone or an iPad. ?mobile=1
// or ?mobile=0 in the page's URL says so instead, to see mobile's page on a PC, or a PC's page on mobile. Mobile
// starts with settings of its own (RECOMMENDED, MOBILE_LINES).
const mobile = (() => {
  const forced = new URLSearchParams(location.search).get('mobile');
  if (forced === '1' || forced === '0') return forced === '1';
  const hints = userAgentData();
  return !!(hints && hints.mobile) || /Android|iPhone|iPad|iPod/.test(navigator.userAgent);
})();

// Whether the page is on a Mac, as the browser says. A Mac has its own key for full screen, which the menu names
// (TIP).
const mac = (() => {
  const platform = userAgentData()?.platform;
  return platform ? platform === 'macOS' : /^Mac/.test(navigator.platform || '');
})();

// ---- The game's text ----

// The game's text, by the names of its strings, as the server sent it (loadTexts).
let texts: Record<string, string> = {};

// The game's text of two strings that the server can't read. The zone keeps each as the end of another string: ADS
// is the end of TOGGLE ADS. It points to the text by where that string is once the engine has loaded the zone, not
// by where it is in the file. Other strings that the zone keeps that way read as their names do
// (PLATFORM_MASTER_VOLUME_CAPS: MASTER VOLUME).
const TAILS: Record<string, string> = {
  MENU_AIM_DOWN_THE_SIGHT_CAPS: 'ADS',
  MENU_VOICE_CHAT_BUTTON_CAPS: 'VOICE CHAT',
};

// The page's own text, for the rows that the game has no text for. The game's menu has them in English too.
const OWN: Record<string, string> = { BO1_SHOW_FPS_CAPS: 'SHOW FPS', BO1_FRAME_RATE_LIMIT_CAPS: 'FRAME RATE LIMIT' };

// Returns a string's text: the game's, or the string's name made readable (MENU_INVERT_MOUSE_CAPS: INVERT MOUSE).
function text(name: string): string {
  if (texts[name]) return texts[name];
  if (TAILS[name]) return TAILS[name];
  if (OWN[name]) return OWN[name];
  const words = name.replace(/^(MENU|PLATFORM|PATCH|KEY|EXE|MPUI_RULES)_/, '');
  const caps = /_CAPS$/.test(words);
  const plain = words.replace(/_CAPS$/, '').replace(/_/g, ' ');
  return caps ? plain : plain.charAt(0) + plain.slice(1).toLowerCase();
}

// ---- The rows ----

// Why a row or a tab is greyed out. The menu says it while the mouse is over the item (say).
const OFF = {
  vsync: 'Always on in the browser',
  warming: 'Not available in the browser',
  device: 'Uses the system\'s sound device',
  voice: 'Not available in the browser',
  screenshot: 'Not available in the browser',
  invite: 'Not available in the browser',
  save: 'Not available in Zombies',
  gamepad: 'Connect a gamepad and press any button',
  gamepadOn: 'On while a gamepad is connected',
  filtering: 'Always trilinear with anisotropy above 1',
};

/** The value of a setting, as a row has it: a number, or one of the engine's words. */
type Value = number | string;

/**
 * A choice of a row: the name of its text, and its value. A choice may also have what it shows in place of that text,
 * and the size that it stands for (videoModes).
 */
type Choice = [label: string, value: Value, shows?: string, size?: Size];

// The choices of a row that is on or off.
const YES_NO: Choice[] = [['MENU_NO_CAPS', 0], ['MENU_YES_CAPS', 1]];
// The levels of the texture quality.
const LEVELS: Choice[] = [['PLATFORM_LOW_CAPS', 3], ['MENU_NORMAL_CAPS', 2], ['PLATFORM_HIGH_CAPS', 1],
  ['MENU_EXTRA_CAPS', 0]];

// Returns what the menu says of a row that a key sets in a game.
const shortcut = (key: string): string => 'In-game shortcut: ' + key;

// What a live row does. The menu says it while the mouse is over the row (say): what the row's Auto is, how a key's
// row changes, or the key that sets the row in a game (shortcut).
const TIP = {
  aspect: 'Auto is the nearest of these to the screen\'s shape, or a wider screen\'s own',
  key: 'CLICK to change',
  aimAssist: 'The view slows over a zombie and follows it, and aiming down the sight turns to one',
  // The key that sets Full Screen in a game, for a player with a keyboard. Mobile has none. The key is the page's own
  // (bo1-zombies/web/src/library_bo1.js: fullScreenKey): F11, or Control+Command+F on a Mac, where the system has
  // F11 for Show Desktop.
  fullScreen: mobile ? '' : shortcut(mac ? 'Control+Command+F' : 'F11'),
  // The key that sets Show FPS in a game: the key that the player's config has for it. That is F9, unless the player
  // bound another command to F9. The row then names no key.
  showFps: (): string => {
    const key = mobile ? undefined : keysFor('toggle bo1_fpscounter')[0];
    return key ? shortcut(keyText(key)) : '';
  },
};

// What the menu says while a game is on in another of the player's tabs, when nothing under the mouse says more.
const LOCKED = 'A game is on in another of your tabs: change your settings in its menu.';

// What every row has.
interface RowBase {
  /** Its place on the game's 640x480 screen, as the game's menu has it. */
  y: number;
  /** The name of its text. */
  label: string;
  /** Why it is greyed out. A row without it is live. */
  off?: string;
  /** What it does, which the menu says while the mouse is over it. */
  tip?: string;
}

/** A row with a choice of values. */
interface MultiRow extends RowBase {
  kind: 'multi';
  /** The dvar that it sets. picmip and invert are the menu's names for settings of several dvars (value, setValue). */
  dvar: string;
  choices: Choice[];
  /** The value that it shows while it is greyed out, where that is not the setting's. */
  shownOff?: Value;
  /** A command that a change adds to the config, which runs it when the game starts. */
  then?: string;
  /** The picture of the Gamepad page that shows while the mouse is over it (padPicture). */
  picture?: 'sticks' | 'buttons';
}

/** A row with a slider, from min to max. */
interface SliderRow extends RowBase {
  kind: 'slider';
  dvar: string;
  min: number;
  max: number;
  /** The least change of its value. */
  step: number;
  /** How far the gamepad's left and right move it. Without it, they move it a twentieth of its range. */
  by?: number;
  /** It shows its value as a number beside its bar. */
  number?: boolean;
  /** Its value is the strength of the gamepad's rumble, and the gamepad rumbles at it when it is set (feel). */
  feel?: boolean;
}

/** A row with the keys of a command. */
interface BindRow extends RowBase {
  kind: 'bind';
  cmd: string;
}

/** A row that shows a value, greyed out. */
interface TextRow extends RowBase {
  kind: 'text';
  value: () => string;
  /** A line of text under it. */
  below?: () => string;
}

/** A row of a page. Its kind says what it shows and how the player changes it. */
type Row = MultiRow | SliderRow | BindRow | TextRow;

/** A page of rows, which a tab opens. */
type PageName ='graphics' | 'sound' | 'voice' | 'game' | 'look' | 'move' | 'combat' | 'interact' | 'coop' | 'gamepad';

// The rows of each page.
const PAGES: Record<PageName, Row[]> = {
  graphics: [
    // The resolutions in the aspect ratio's shape, or in the screen's shape at Auto, as the next game would have
    // them.
    { y: -115, label: 'MENU_VIDEO_MODE_CAPS', kind: 'multi', dvar: 'bo1_web_resolution',
      get choices() { return videoModes(dvar('r_aspectRatio')); } },
    // The browser's full screen, a setting of the web client's own (bo1-zombies/web/src/web_main.cpp:
    // bo1_web_fullscreen). The game's row is its exclusive full screen (r_fullscreen), which is 0 here.
    { y: -96, label: 'MENU_FULLSCREEN_CAPS', kind: 'multi', dvar: 'bo1_web_fullscreen', choices: YES_NO,
      tip: TIP.fullScreen },
    // What the web client draws: 4 samples a pixel or 1, the two that WebGPU has
    // (bo1-zombies/web/src/d3d9_device.cpp: CheckDeviceMultiSampleType). The game's menu has 2X to 16X too. The
    // recommended settings pick 4 (DEFAULTS).
    { y: -77, label: 'MENU_ANTIALIASING_CAPS', kind: 'multi', dvar: 'r_aaSamples',
      choices: [['MENU_OFF_CAPS', 1], ['MENU_4X_CAPS', 4]] },
    // Auto shows the shape that it picks for the screen (autoShape).
    { y: -58, label: 'MENU_ASPECT_RATIO_CAPS', kind: 'multi', dvar: 'r_aspectRatio', tip: TIP.aspect,
      get choices(): Choice[] {
        return [['MENU_AUTO_CAPS', 'auto', text('MENU_AUTO_CAPS') + ' (' + autoShape() + ')'],
          ['MENU_STANDARD_4_3_CAPS', 'standard'], ['MENU_WIDE_16_10_CAPS', 'wide 16:10'],
          ['MENU_WIDE_16_9_CAPS', 'wide 16:9']];
      } },
    // The player's frame rate limit (bo1_maxfps). The choices say what each limit draws at the display's refresh
    // rate, and the last is Unlimited (frameLimits).
    { y: -39, label: 'BO1_FRAME_RATE_LIMIT_CAPS', kind: 'multi', dvar: 'bo1_maxfps',
      get choices() { return frameLimits(); } },
    { y: -20, label: 'MENU_SYNC_EVERY_FRAME_CAPS', kind: 'multi', dvar: 'r_vsync', choices: YES_NO, off: OFF.vsync },
    // Above anisotropy 1 the web client filters every texture trilinear, whatever this row says. WebGPU filters
    // anisotropically only when every filter is linear (bo1-zombies/web/src/d3d9_webgpu.cpp: SamplerFor). The row is
    // greyed out then and shows Trilinear. It keeps the player's choice for anisotropy 1.
    { y: 9, label: 'MENU_TEXTURE_MIPMAPS_CAPS', kind: 'multi', dvar: 'r_texFilterMipMode',
      choices: [['MENU_AUTOMATIC_CAPS', 'Unchanged'], ['MENU_BILINEAR_CAPS', 'Force Bilinear'],
        ['MENU_TRILINEAR_CAPS', 'Force Trilinear']],
      get off() { return Number(dvar('r_texFilterAnisoMin')) > 1 ? OFF.filtering : undefined; },
      shownOff: 'Force Trilinear' },
    { y: 28, label: 'MENU_TEXTURE_ANISOTROPY_CAPS', kind: 'slider', dvar: 'r_texFilterAnisoMin',
      min: 1, max: 16, step: 1 },
    // The game's ui_r_picmip: Automatic, or every kind of texture at one level (r_picmip_manual and the three
    // picmips).
    { y: 47, label: 'MENU_TEXTURE_QUALITY_CAPS', kind: 'multi', dvar: 'picmip',
      choices: [['MENU_AUTOMATIC_CAPS', -1], ...LEVELS] },
    { y: 76, label: 'PATCH_MENU_OPTION_SHADERWARMING_CAPS', kind: 'multi', dvar: 'r_shaderWarming', choices: YES_NO,
      off: OFF.warming },
    { y: 95, label: 'MENU_SHADOWS_CAPS', kind: 'multi', dvar: 'sm_enable', choices: YES_NO },
    { y: 114, label: 'MENU_OPTION_BULLET_IMPACTS_CAPS', kind: 'multi', dvar: 'fx_marks', choices: YES_NO },
    { y: 133, label: 'MENU_NUMBER_OF_CORPSES_CAPS', kind: 'multi', dvar: 'ai_corpseCount',
      choices: [['MENU_TINY_CAPS', 3], ['MENU_SMALL_CAPS', 5], ['MENU_MEDIUM_CAPS', 10], ['MENU_LARGE_CAPS', 16]] },
    { y: 152, label: 'MENU_BRIGHTNESS_CAPS', kind: 'slider', dvar: 'r_gamma', min: 0.5, max: 3, step: 0.01 },
    // The slider has its number beside it, which the game's lacks. It goes from 65 to 115, 5 a block, as the game's
    // does (bo1-zombies/src/ui/ui_main.cpp: UI_PlayerFov_PatchMenus). A value above 115 (up to 120, from the
    // console) shows the bar full. The gamepad moves it a block (by), as the arrow keys do in the game
    // (bo1-zombies/src/ui/ui_shared.cpp: Item_Slider_HandleKey).
    { y: 171, label: 'PLATFORM_FOV_CAPS', kind: 'slider', dvar: 'bo1_fov', min: 65, max: 115, step: 1, by: 5,
      number: true },
  ],
  sound: [
    { y: -115, label: 'PLATFORM_VOICE_VOLUME_CAPS', kind: 'slider', dvar: 'snd_menu_voice',
      min: 0, max: 1, step: 0.01 },
    { y: -96, label: 'PLATFORM_MUSIC_VOLUME_CAPS', kind: 'slider', dvar: 'snd_menu_music',
      min: 0, max: 1, step: 0.01 },
    { y: -77, label: 'PLATFORM_SFX_VOLUME_CAPS', kind: 'slider', dvar: 'snd_menu_sfx',
      min: 0, max: 1, step: 0.01 },
    { y: -58, label: 'PLATFORM_CINEMATICS_VOLUME_CAPS', kind: 'slider', dvar: 'snd_menu_cinematic',
      min: 0, max: 1, step: 0.01 },
    { y: -39, label: 'PLATFORM_MASTER_VOLUME_CAPS', kind: 'slider', dvar: 'snd_menu_master',
      min: 0, max: 1, step: 0.01 },
    { y: -1, label: 'MENU_SOUND_LOSOCCLUSION_CAPS', kind: 'multi', dvar: 'snd_losOcclusion', choices: YES_NO },
    { y: 18, label: 'MENU_LISTENING_LEVEL_CAPS', kind: 'multi', dvar: 'snd_menu_listen_level',
      choices: [['MENU_QUIET_CAPS', 0], ['MENU_LOUD_CAPS', 1]] },
    { y: 37, label: 'MENU_SPEAKER_SETUP_CAPS', kind: 'multi', dvar: 'snd_menu_speaker_setup',
      choices: [['PATCH_LAPTOP_OR_SMALL_SPEAKERS_CAPS', 0], ['PLATFORM_LARGE_EXTERNAL_SPEAKERS_CAPS', 1],
        ['PLATFORM_HEADPHONES_CAPS', 2]] },
    { y: 56, label: 'PLATFORM_SOUND_DEVICE_CAPS', kind: 'text', value: () => '',
      below: () => text('MENU_STEREO_SPEAKERS'), off: OFF.device },
  ],
  voice: [
    { y: -115, label: 'MENU_VOICECHAT_CAPS', kind: 'multi', dvar: 'cl_voice',
      choices: [['MENU_ON_CAPS', 1], ['MENU_OFF_CAPS', 0]], off: OFF.voice },
  ],
  game: [
    { y: -115, label: 'MENU_SUBTITLES_CAPS', kind: 'multi', dvar: 'cg_subtitles', choices: YES_NO },
    // monkeytoy restricts the console, so the Yes of Enable Console is monkeytoy 0.
    { y: -96, label: 'MENU_ENABLE_CONSOLE_CAPS', kind: 'multi', dvar: 'monkeytoy',
      choices: [['MENU_YES_CAPS', 0], ['MENU_NO_CAPS', 1]] },
    // The game's frame rate counter at the top left, which F9 toggles in a game
    // (bo1-zombies/src/client_mp/cl_scrn_mp.cpp: bo1_fpscounter). The row is for a player with no keyboard to press
    // F9 on. The game's files lack the row, and the game's menu has it under these two as well
    // (bo1-zombies/src/ui/ui_main.cpp: UI_ShowFps_PatchMenus).
    { y: -77, label: 'BO1_SHOW_FPS_CAPS', kind: 'multi', dvar: 'bo1_fpscounter', choices: YES_NO,
      get tip() { return TIP.showFps(); } },
  ],
  look: [
    // The game's ui_mousePitch, which sets the sign of m_pitch (bo1-zombies/src/ui/ui_main.cpp).
    { y: -115, label: 'MENU_INVERT_MOUSE_CAPS', kind: 'multi', dvar: 'invert', choices: YES_NO },
    { y: -96, label: 'MENU_LEAN_LEFT_CAPS', kind: 'bind', cmd: '+leanleft' },
    { y: -77, label: 'MENU_LEAN_RIGHT_CAPS', kind: 'bind', cmd: '+leanright' },
    { y: -58, label: 'MENU_LOOK_UP_CAPS', kind: 'bind', cmd: '+lookup' },
    { y: -39, label: 'MENU_LOOK_DOWN_CAPS', kind: 'bind', cmd: '+lookdown' },
    { y: -20, label: 'MENU_TURN_LEFT_CAPS', kind: 'bind', cmd: '+left' },
    { y: -1, label: 'MENU_TURN_RIGHT_CAPS', kind: 'bind', cmd: '+right' },
    { y: 18, label: 'MENU_MOUSE_LOOK_CAPS', kind: 'bind', cmd: '+mlook' },
    { y: 37, label: 'MENU_CENTER_VIEW_CAPS', kind: 'bind', cmd: 'centerview' },
    { y: 56, label: 'MENU_FREE_LOOK_CAPS', kind: 'multi', dvar: 'cl_freelook', choices: YES_NO },
    { y: 75, label: 'MENU_MOUSE_SENSITIVITY_CAPS', kind: 'slider', dvar: 'sensitivity', min: 1, max: 30, step: 0.1 },
  ],
  move: [
    { y: -115, label: 'MENU_FORWARD_CAPS', kind: 'bind', cmd: '+forward' },
    { y: -96, label: 'MENU_BACKPEDAL_CAPS', kind: 'bind', cmd: '+back' },
    { y: -77, label: 'MENU_MOVE_LEFT_CAPS', kind: 'bind', cmd: '+moveleft' },
    { y: -58, label: 'MENU_MOVE_RIGHT_CAPS', kind: 'bind', cmd: '+moveright' },
    { y: -29, label: 'MENU_STANDJUMP_CAPS', kind: 'bind', cmd: '+gostand' },
    { y: -10, label: 'MENU_GO_TO_CROUCH_CAPS', kind: 'bind', cmd: 'gocrouch' },
    { y: 9, label: 'MENU_GO_TO_PRONE_CAPS', kind: 'bind', cmd: 'goprone' },
    { y: 28, label: 'MENU_TOGGLE_CROUCH_CAPS', kind: 'bind', cmd: 'togglecrouch' },
    { y: 47, label: 'MENU_TOGGLE_PRONE_CAPS', kind: 'bind', cmd: 'toggleprone' },
    { y: 66, label: 'MENU_CROUCH_CAPS', kind: 'bind', cmd: '+movedown' },
    { y: 85, label: 'MENU_PRONE_CAPS', kind: 'bind', cmd: '+prone' },
    { y: 114, label: 'PLATFORM_CHANGE_STANCE_CAPS', kind: 'bind', cmd: '+stance' },
    { y: 133, label: 'MENU_STRAFE_CAPS', kind: 'bind', cmd: '+strafe' },
  ],
  combat: [
    { y: -115, label: 'MENU_ATTACK_CAPS', kind: 'bind', cmd: '+attack' },
    { y: -96, label: 'MENU_AIM_DOWN_THE_SIGHT_CAPS', kind: 'bind', cmd: '+speed_throw' },
    { y: -77, label: 'MENU_TOGGLE_AIM_DOWN_THE_SIGHT_CAPS', kind: 'bind', cmd: '+toggleads_throw' },
    { y: -58, label: 'MENU_MELEE_ATTACK_CAPS', kind: 'bind', cmd: '+melee' },
    { y: -39, label: 'MENU_SWITCH_WEAPON_CAPS', kind: 'bind', cmd: 'weapnext' },
    { y: -20, label: 'MENU_RELOAD_WEAPON_CAPS', kind: 'bind', cmd: '+reload' },
    { y: -1, label: 'MENU_SPRINT_CAPS', kind: 'bind', cmd: '+sprint' },
    { y: 18, label: 'MENU_SPRINT_HOLD_BREATH_CAPS', kind: 'bind', cmd: '+breath_sprint' },
    { y: 37, label: 'MENU_STEADY_SNIPER_RIFLE_CAPS', kind: 'bind', cmd: '+holdbreath' },
    { y: 66, label: 'MENU_THROW_FRAG_GRENADE_CAPS', kind: 'bind', cmd: '+frag' },
    { y: 85, label: 'MENU_THROW_SPECIAL_GRENADE_CAPS', kind: 'bind', cmd: '+smoke' },
    { y: 114, label: 'PLATFORM_USE_EQUIPMENT_CAPS', kind: 'bind', cmd: '+actionslot 1' },
    { y: 133, label: 'MENU_ACTION_INVENTORY_CAPS', kind: 'bind', cmd: '+actionslot 3' },
    { y: 152, label: 'MENU_GROUND_SUPPORT_CAPS', kind: 'bind', cmd: '+actionslot 4' },
  ],
  interact: [
    { y: -115, label: 'MENU_USE_CAPS', kind: 'bind', cmd: '+activate' },
    { y: -96, label: 'MENU_SCREENSHOT_CAPS', kind: 'bind', cmd: 'screenshotJPEG', off: OFF.screenshot },
    { y: -67, label: 'MENU_ACCEPT_INVITE_CAPS', kind: 'bind', cmd: 'acceptinvitation', off: OFF.invite },
    { y: -48, label: 'MENU_QUICK_SAVE_CAPS', kind: 'bind', cmd: 'savegame_lastcommit', off: OFF.save },
  ],
  coop: [
    { y: -115, label: 'MENU_VOICE_CHAT_BUTTON_CAPS', kind: 'bind', cmd: '+talk', off: OFF.voice },
    { y: -96, label: 'MENU_CHAT_CAPS', kind: 'bind', cmd: 'chatmodepublic' },
  ],
  // Enable Gamepad is the game's to set. A gamepad's first press turns it on, and it goes off when the gamepad goes
  // (bo1-zombies/src/win32/win_input.cpp: IN_GamepadsMove). So the page is that of a connected gamepad. A change of
  // a layout has the config run the layout's command when the game starts, as the game's menu runs it
  // (bo1-zombies/src/client/cl_gamepad.cpp: bindgpsticksconfigs, bindgpbuttonsconfigs).
  gamepad: [
    { y: -115, label: 'PLATFORM_ENABLE_GAMEPAD_CAPS', kind: 'text', off: OFF.gamepadOn,
      value: () => text(gamepadThere() ? 'MENU_YES_CAPS' : 'MENU_NO_CAPS') },
    { y: -96, label: 'MENU_CONTROLLER_VIBRATION_CAPS', kind: 'slider', dvar: 'bo1_rumble', min: 0, max: 1, step: 0.05,
      feel: true },
    { y: -77, label: 'PLATFORM_LOOK_INVERSION_CAPS', kind: 'multi', dvar: 'input_invertPitch',
      choices: [['MENU_DISABLED_CAPS', 0], ['MENU_ENABLED_CAPS', 1]] },
    // These two rows show their layout while the mouse is over them, as the game's do (PAD_PICTURE).
    { y: -58, label: 'PLATFORM_STICK_LAYOUT_CAPS', kind: 'multi', dvar: 'gpad_sticksConfig', picture: 'sticks',
      then: 'bindgpsticksconfigs', choices: [['MENU_DEFAULT_CAPS', 'thumbstick_default'],
        ['MENU_SOUTHPAW_CAPS', 'thumbstick_southpaw'], ['MENU_LEGACY_CAPS', 'thumbstick_legacy'],
        ['MENU_LEGACY_SOUTHPAW_CAPS', 'thumbstick_legacysouthpaw']] },
    { y: -39, label: 'PLATFORM_BUTTON_LAYOUT_CAPS', kind: 'multi', dvar: 'gpad_buttonsConfig', picture: 'buttons',
      then: 'bindgpbuttonsconfigs', choices: [['MENU_DEFAULT_CAPS', 'buttons_default'],
        ['MENU_TACTICAL_CAPS', 'buttons_experimental'], ['MENU_LEFTY_CAPS', 'buttons_lefty'],
        ['MENU_NOMAD', 'buttons_nomad'], ['MENU_CHARLIE_CAPS', 'buttons_charlie']] },
    { y: -20, label: 'PLATFORM_LOOK_SENSITIVITY_CAPS', kind: 'multi', dvar: 'input_viewSensitivity',
      choices: [['MENU_SENSITIVITY_LOW_CAPS', 0.6], ['MENU_SENSITIVITY_MEDIUM_CAPS', 1], ['3', 1.4],
        ['MENU_SENSITIVITY_HIGH_CAPS', 1.8], ['5', 2], ['6', 2.2], ['MENU_SENSITIVITY_VERY_HIGH_CAPS', 2.6], ['8', 3],
        ['9', 3.5], ['MENU_SENSITIVITY_INSANE_CAPS', 4]] },
    // The Aim Assist of the console games, which the game's PC files have no row for. It turns all of the gamepad's
    // aim assist on or off: the slowdown, the lock-on, and the auto aim when aiming down the sight (bo1_aimassist).
    { y: -1, label: 'MENU_AIM_ASSIST_CAPS', kind: 'multi', dvar: 'bo1_aimassist', tip: TIP.aimAssist,
      choices: [['MENU_DISABLED_CAPS', 0], ['MENU_ENABLED_CAPS', 1]] },
  ],
};

// The picture of the Gamepad page, as the game's menu has it (options_gamepad, which bo1_menudump reads). It shows
// the controller from above while the mouse is over Stick Layout or Button Layout. For the sticks it has the arrows
// and the labels of the layout. For the buttons it has the labels of the layout and the lines to them. The pictures
// are the game's (GET /images/<name>.png: server/mapart.go). Each item is placed on the game's screen from the
// screen's top (the menu's align 2 1): x from the middle, y from the top. Each is drawn a row lower than the game's
// files have it (PAD_DROP), under Aim Assist, the row that the files lack.

/** A place on the game's screen and a size there: x, y, width, height. */
type Box = [x: number, y: number, w: number, h: number];

// Where the controller's picture is (xenon_controller_top).
const PAD_PICTURE: Box = [0, 233, 190, 190];
// How much lower the picture is drawn than the game's files have it: a row.
const PAD_DROP = 19;

// The lines from the buttons to their labels (the game's owner draw 355), each from x, y by w, h. The game's line
// from Back, to the Scoreboard of MP, is not drawn in the SP game.
const PAD_LINES: Box[] = [[144, 331, 29, 16], [134, 318, 38, 14], [158, 318, 12, 2], [145, 303, 23, 1],
  [33, 287, 16, 3], [144, 290, 15, -3], [137, 271, 19, 0], [137, 271, 0, 7], [37, 271, 19, 0], [56, 271, 0, 7],
  [113, 310, 0, -43], [73, 341, 0, 48], [23, 320, 27, 0], [116, 347, 0, 26]];

// Where each label is, in a box of 100 by 12, and which way it reads from the line to it. A label left of the
// controller ends at its line ('right'). A label right of the controller starts at its line ('left'). The rest are
// centred.
const PAD_SPOTS: Record<string, [x: number, y: number, side?: 'left' | 'right']> = {
  A: [175, 341, 'left'], B: [172, 313, 'left'], X: [174, 327, 'left'], Y: [170, 299, 'left'], RB: [161, 279, 'left'],
  RT: [158, 265, 'left'], LB: [-70, 279, 'right'], LT: [-65, 265, 'right'], LS: [-82, 313, 'right'],
  DPAD: [21, 391], START: [88, 251], RS: [99, 375],
  LSTICK_UP: [-1, 269], LSTICK_DOWN: [-1, 355], LSTICK_SIDE: [-86, 312, 'right'],
  RSTICK_UP: [66, 295], RSTICK_DOWN: [66, 381], RSTICK_SIDE: [151, 339, 'left'],
};

// The labels of each button layout, by their spots.
const PAD_BUTTONS: Record<string, Record<string, string>> = (() => {
  const common = { A: 'MENU_JUMP', X: 'MENU_USE_RELOAD', Y: 'MENU_SWITCH_WEAPON', DPAD: 'MENU_INVENTORY',
    START: 'MENU_OBJECTIVES_MENU' };
  return {
    buttons_default: { ...common, B: 'MENU_CROUCH_PRONE', RB: 'MENU_THROW_FRAG_GRENADE',
      LB: 'MENU_THROW_SPECIAL_GRENADE', LT: 'MENU_AIM_DOWN_SIGHT', RT: 'MENU_FIRE_WEAPON',
      LS: 'MENU_SPRINT_HOLD_BREATH', RS: 'MENU_MELEE_ATTACK' },
    buttons_experimental: { ...common, B: 'MENU_MELEE_ATTACK', RB: 'MENU_THROW_FRAG_GRENADE',
      LB: 'MENU_THROW_SPECIAL_GRENADE', LT: 'MENU_AIM_DOWN_SIGHT', RT: 'MENU_FIRE_WEAPON',
      LS: 'MENU_SPRINT_HOLD_BREATH', RS: 'MENU_CROUCH_PRONE' },
    buttons_lefty: { ...common, B: 'MENU_CROUCH_PRONE', RB: 'MENU_THROW_SPECIAL_GRENADE',
      LB: 'MENU_THROW_FRAG_GRENADE', LT: 'MENU_FIRE_WEAPON', RT: 'MENU_AIM_DOWN_SIGHT', LS: 'MENU_MELEE_ATTACK',
      RS: 'MENU_SPRINT_HOLD_BREATH' },
    buttons_nomad: { ...common, B: 'MENU_MELEE_ATTACK', RB: 'MENU_THROW_FRAG_GRENADE',
      LB: 'MENU_THROW_SPECIAL_GRENADE', LT: 'MENU_TOGGLE_AIM_DOWN_THE_SIGHT', RT: 'MENU_FIRE_WEAPON',
      LS: 'MENU_SPRINT_HOLD_BREATH', RS: 'MENU_CROUCH_PRONE' },
    buttons_charlie: { ...common, B: 'MENU_CROUCH_PRONE', RB: 'MENU_TOGGLE_AIM_DOWN_THE_SIGHT',
      LB: 'MENU_THROW_SPECIAL_GRENADE', LT: 'MENU_THROW_FRAG_GRENADE', RT: 'MENU_FIRE_WEAPON',
      LS: 'MENU_SPRINT_HOLD_BREATH', RS: 'MENU_MELEE_ATTACK' },
  };
})();

// Where the arrows of a stick layout are drawn: over the left stick, and over the right one. Each picture is 62
// square.
const PAD_STICK_PICTURES: [x: number, y: number][] = [[19, 288], [86, 314]];

/** A stick layout: the pictures of the arrows over the left stick and the right one, and the labels by their spots. */
interface StickLayout {
  pictures: string[];
  labels: Record<string, string>;
}

// The arrows and the labels of each stick layout.
const PAD_STICKS: Record<string, StickLayout> = (() => {
  const move = { UP: 'MENU_MOVE_FORWARD', DOWN: 'MENU_MOVE_BACK' };
  const look = { UP: 'MENU_LOOK_UP', DOWN: 'MENU_LOOK_DOWN' };
  type Stick = typeof move;
  const layout = (pictures: string[], left: Stick, right: Stick, leftSide: string, rightSide: string): StickLayout => ({
    pictures,
    labels: { LSTICK_UP: left.UP, LSTICK_DOWN: left.DOWN, LSTICK_SIDE: leftSide, RSTICK_UP: right.UP,
      RSTICK_DOWN: right.DOWN, RSTICK_SIDE: rightSide },
  });
  return {
    thumbstick_default: layout(['xenon_stick_move', 'xenon_stick_turn'], move, look, 'MENU_STRAFE_LEFT_RIGHT',
      'MENU_ROTATE_LEFT_RIGHT'),
    thumbstick_southpaw: layout(['xenon_stick_turn', 'xenon_stick_move'], look, move, 'MENU_ROTATE_LEFT_RIGHT',
      'MENU_STRAFE_LEFT_RIGHT'),
    thumbstick_legacy: layout(['xenon_stick_move_turn', 'xenon_stick_move_look'], move, look,
      'MENU_ROTATE_LEFT_RIGHT', 'MENU_STRAFE_LEFT_RIGHT'),
    thumbstick_legacysouthpaw: layout(['xenon_stick_move_look', 'xenon_stick_move_turn'], look, move,
      'MENU_STRAFE_LEFT_RIGHT', 'MENU_ROTATE_LEFT_RIGHT'),
  };
})();

/** A tab at the left of a screen. It opens a page, or asks a question (reset). */
interface Tab {
  /** Its place on the game's screen. */
  y: number;
  label: string;
  /** The page that it opens. */
  page?: PageName;
  /** The screen whose settings its question resets. */
  reset?: ScreenName;
  /** Why it is greyed out. */
  off?: string;
  /** It is greyed out until a gamepad is there. */
  gamepad?: boolean;
}

// The two screens: the title of each, and the tabs at its left.
const SCREENS: Record<ScreenName, { title: string; tabs: Tab[] }> = {
  settings: {
    title: 'MENU_SETTINGS_CAPS',
    tabs: [
      { y: -134, label: 'MENU_GRAPHICS_CAPS', page: 'graphics' },
      { y: -115, label: 'MENU_SOUND_CAPS', page: 'sound' },
      { y: -96, label: 'MENU_VOICECHAT_CAPS', page: 'voice', off: OFF.voice },
      { y: -77, label: 'PATCH_GAME_CAPS', page: 'game' },
      { y: -39, label: 'PLATFORM_DEFAULT_SYSTEM_SETTINGS_CAPS', reset: 'settings' },
    ],
  },
  controls: {
    title: 'MENU_CONTROLS_CAPS',
    tabs: [
      { y: -134, label: 'MENU_LOOK_CAPS', page: 'look' },
      { y: -115, label: 'MENU_MOVE_CAPS', page: 'move' },
      { y: -96, label: 'MENU_COMBAT_CAPS', page: 'combat' },
      { y: -77, label: 'MENU_INTERACT_CAPS', page: 'interact' },
      { y: -58, label: 'PATCH_COOPERATIVE_CAPS', page: 'coop' },
      // As in the game, the tab is greyed out until a gamepad is there, or on.
      { y: -39, label: 'PLATFORM_GAMEPAD_CAPS', page: 'gamepad', gamepad: true },
      { y: -1, label: 'MENU_SET_DEFAULT_CONTROLS_CAPS', reset: 'controls' },
    ],
  },
};

// What a fresh game has, by the dvars' names in lower case. It is the engine's defaults, which the engine writes to
// the config (writeconfig, in a game with no config of its own), and the recommended settings that the engine picks
// for the browser. Those are the row of configure_mp.csv for all GPUs, with 4 samples of anti-aliasing, and for
// Zombies the best texture filtering (bo1-zombies/src/qcommon/common.cpp: Com_SetRecommended). The game tests check
// them against the config that a player's first game writes (e2e/game/engine/solo.spec.ts).
const DEFAULTS: Record<string, string> = {
  r_fullscreen: '0', r_aasamples: '4', r_aspectratio: 'auto', r_vsync: '0', r_texfiltermipmode: 'Force Trilinear',
  r_texfilteranisomin: '16', r_picmip_manual: '0', r_picmip: '0', r_picmip_bump: '0', r_picmip_spec: '0',
  r_shaderwarming: '0', sm_enable: '1', fx_marks: '1', r_gamma: '1', bo1_fov: '80', bo1_web_resolution: 'auto',
  snd_menu_voice: '1', snd_menu_music: '1', snd_menu_sfx: '1', snd_menu_cinematic: '1', snd_menu_master: '0.5',
  snd_losocclusion: '1', snd_menu_listen_level: '1', snd_menu_speaker_setup: '2', cl_voice: '1', cg_subtitles: '0',
  monkeytoy: '0', m_pitch: '0.022', ui_mousepitch: '0', cl_freelook: '1', sensitivity: '5', m_filter: '0',
  cl_mouseaccel: '0', gpad_enabled: '0', input_invertpitch: '0', gpad_sticksconfig: 'thumbstick_default',
  gpad_buttonsconfig: 'buttons_default', input_viewsensitivity: '1', bo1_rumble: '1', bo1_aimassist: '1',
  bo1_fpscounter: '0', bo1_web_fullscreen: '1',
  // Mobile's limit of 90 draws every refresh of a display of 60 or 90 Hz, and every second refresh of a faster one:
  // 60 at 120 Hz, 72 at 144 Hz. It spares a phone's battery and its heat, and never draws fewer than 60 frames a
  // second. A limit of 60 would draw 45 at 90 Hz and 48 at 144 Hz, which is slower than a 60 Hz phone draws.
  bo1_maxfps: mobile ? '90' : '0',
};
// The number of corpses that the game's recommended settings pick for the CPU, a step up for Zombies
// (bo1-zombies/src/qcommon/common.cpp: configure_mp.csv). It is Large from 4 GHz in all. The web client's CPU always
// has that: its cores count as 3 GHz each, and it has two at least (bo1-zombies/web/src/web_main.cpp).
DEFAULTS.ai_corpsecount = '16';

// What a fresh game has on mobile instead, by the dvars' names. The game's recommended settings are those of a PC's
// graphics card. Mobile has no anti-aliasing, no anisotropy and no shadows, at a video mode whose Auto is
// MOBILE_LINES high (screenSize). The sun's shadow map is over half of the draws at Kino's spawn. A Galaxy drew the
// PC's settings too slowly to play, and far better with every graphics setting at its lowest. How much each setting
// did is unmeasured. These are the ones that cost a mobile GPU most, by what the frame draws.
//
// The page gives them to the engine (web/public/player.js: Module.bo1Recommended). The engine sets them after the
// game's own recommended settings, wherever it sets those: in a player's first game, and at Default System Settings
// in the game's menu (bo1-zombies/web/src/web_main.cpp: Sys_ArchiveInfo). A player who has played keeps what they
// have.
const RECOMMENDED: Record<string, string> = mobile ? { r_aaSamples: '1', r_texFilterAnisoMin: '1', sm_enable: '0' }
  : {};
for (const [name, v] of Object.entries(RECOMMENDED)) DEFAULTS[name.toLowerCase()] = v;
// The height of the video mode's Auto on mobile.
const MOBILE_LINES = 720;

/** The command of each key, by the key's name as the config has it. */
type Binds = Record<string, string>;

// The keys that a fresh game has (bo1-zombies/src/qcommon/common.cpp: com_zombiesDefaultControls), as the engine
// writes them.
const DEFAULT_BINDS: Binds = {
  W: '+forward', S: '+back', A: '+moveleft', D: '+moveright', Q: 'weapnext', E: '+activate', SHIFT: '+breath_sprint',
  MOUSE1: '+attack', MOUSE2: '+speed_throw', V: '+melee', MOUSE4: '+melee', 1: '+actionslot 1', 2: '+actionslot 2',
  3: '+actionslot 3', 4: '+actionslot 4', MWHEELUP: 'weapprev', MWHEELDOWN: 'weapnext', MOUSE3: '+frag', G: '+frag',
  F: '+smoke', MOUSE5: '+smoke', R: '+reload', TAB: '+scores', T: 'chatmodepublic', ENTER: 'chatmodepublic',
  Z: '+talk', SPACE: '+gostand', CTRL: '+stance', C: '+stance', PAUSE: 'pause', ESCAPE: 'togglemenu',
  '~': 'toggleconsole', '`': 'toggleconsole', F12: 'screenshotJPEG', F10: 'acceptinvitation',
  F5: 'savegame_lastcommit', F9: 'toggle bo1_fpscounter',
};
// What Set Default Controls puts back besides the keys (the dvars of com_zombiesDefaultControls).
const CONTROL_DVARS = ['sensitivity', 'cl_freelook', 'ui_mousepitch', 'm_pitch', 'm_filter', 'cl_mouseaccel',
  'gpad_enabled', 'gpad_sticksconfig', 'gpad_buttonsconfig', 'input_invertpitch', 'input_viewsensitivity',
  'bo1_rumble', 'bo1_aimassist'];

// ---- Keys ----

// The engine's names of the keys, and their numbers (bo1-zombies/src/client/cl_keys.cpp: keynames). A row lists its
// keys in the order of their numbers. The text of a key is the string KEY_<name>. A printable key is its character,
// in upper case, and its number is that of the character in lower case.
const KEYNUMS: Record<string, number> = {
  TAB: 9, ENTER: 13, ESCAPE: 27, SPACE: 32, BACKSPACE: 127, UPARROW: 154, DOWNARROW: 155, LEFTARROW: 156,
  RIGHTARROW: 157, ALT: 158, CTRL: 159, SHIFT: 160, CAPSLOCK: 151, F1: 167, F2: 168, F3: 169, F4: 170, F5: 171,
  F6: 172, F7: 173, F8: 174, F9: 175, F10: 176, F11: 177, F12: 178, INS: 161, DEL: 162, PGDN: 163, PGUP: 164,
  HOME: 165, END: 166, MOUSE1: 200, MOUSE2: 201, MOUSE3: 202, MOUSE4: 203, MOUSE5: 204, MWHEELUP: 206,
  MWHEELDOWN: 205, KP_HOME: 182, KP_UPARROW: 183, KP_PGUP: 184, KP_LEFTARROW: 185, KP_5: 186, KP_RIGHTARROW: 187,
  KP_END: 188, KP_DOWNARROW: 189, KP_PGDN: 190, KP_ENTER: 191, KP_INS: 192, KP_DEL: 193, KP_SLASH: 194,
  KP_MINUS: 195, KP_PLUS: 196, KP_NUMLOCK: 197, KP_STAR: 198, KP_EQUALS: 199, PAUSE: 153, SEMICOLON: 59,
};
// The gamepad's buttons, as keys. The gamepad's layouts bind them (Settings > Controls > Gamepad;
// bo1-zombies/src/win32/win_input.cpp: a gamepad's first press binds them), and the engine writes them first. The
// rows of keys leave them out, and keep them when they bind a key.
const PAD_KEYNUMS: Record<string, number> = {
  BUTTON_A: 1, BUTTON_B: 2, BUTTON_X: 3, BUTTON_Y: 4, BUTTON_LSHLDR: 5, BUTTON_RSHLDR: 6, BUTTON_START: 14,
  BUTTON_BACK: 15, BUTTON_LSTICK: 16, BUTTON_RSTICK: 17, BUTTON_LTRIG: 18, BUTTON_RTRIG: 19,
  DPAD_UP: 20, DPAD_DOWN: 21, DPAD_LEFT: 22, DPAD_RIGHT: 23,
  APAD_UP: 28, APAD_DOWN: 29, APAD_LEFT: 30, APAD_RIGHT: 31,
};
Object.assign(KEYNUMS, PAD_KEYNUMS);
// Whether a key is one of the gamepad's buttons.
const padKey = (k: string): boolean => k in PAD_KEYNUMS;
// Returns a key's number.
const keyNum = (k: string): number => (k.length === 1 ? k.toLowerCase().charCodeAt(0) : KEYNUMS[k] || 999);
// Returns a key's text.
const keyText = (k: string): string => (k.length === 1 ? k : text('KEY_' + k));

// The engine's key for each KeyboardEvent.code. The web client sends the engine the key that a US keyboard has in
// that place (bo1-zombies/web/src/library_bo1.js: bo1KeyTable), so a key is named by where it is.
const CODES: Record<string, string> = {
  Tab: 'TAB', Enter: 'ENTER', Space: 'SPACE', ArrowUp: 'UPARROW', ArrowDown: 'DOWNARROW', ArrowLeft: 'LEFTARROW',
  ArrowRight: 'RIGHTARROW', AltLeft: 'ALT', AltRight: 'ALT', ControlLeft: 'CTRL', ControlRight: 'CTRL',
  ShiftLeft: 'SHIFT', ShiftRight: 'SHIFT', CapsLock: 'CAPSLOCK', Insert: 'INS', Delete: 'DEL', PageDown: 'PGDN',
  PageUp: 'PGUP', Home: 'HOME', End: 'END', Pause: 'PAUSE', Semicolon: 'SEMICOLON', Numpad7: 'KP_HOME',
  Numpad8: 'KP_UPARROW', Numpad9: 'KP_PGUP', Numpad4: 'KP_LEFTARROW', Numpad5: 'KP_5', Numpad6: 'KP_RIGHTARROW',
  Numpad1: 'KP_END', Numpad2: 'KP_DOWNARROW', Numpad3: 'KP_PGDN', NumpadEnter: 'KP_ENTER', Numpad0: 'KP_INS',
  NumpadDecimal: 'KP_DEL', NumpadDivide: 'KP_SLASH', NumpadSubtract: 'KP_MINUS', NumpadAdd: 'KP_PLUS',
  NumLock: 'KP_NUMLOCK', NumpadMultiply: 'KP_STAR', NumpadEqual: 'KP_EQUALS', Minus: '-', Equal: '=',
  BracketLeft: '[', BracketRight: ']', Backslash: '\\', Quote: '\'', Comma: ',', Period: '.', Slash: '/',
  Backquote: '`',
};
for (let i = 1; i <= 12; i++) CODES['F' + i] = 'F' + i;
for (let i = 0; i <= 9; i++) CODES['Digit' + i] = String(i);
for (const c of 'ABCDEFGHIJKLMNOPQRSTUVWXYZ') CODES['Key' + c] = c;
// The engine's key for each mouse button (MouseEvent.button).
const BUTTONS = ['MOUSE1', 'MOUSE3', 'MOUSE2', 'MOUSE4', 'MOUSE5'];

// ---- The player's config ----

/** The config, as the menu read it. */
interface Config {
  lines: string[];
  /** The dvars that it sets, by their names in lower case. The last line that sets a dvar counts. */
  dvars: Record<string, string>;
  /** The keys that it binds: the defaults, with its unbindall, bind and unbind lines applied in their order. */
  binds: Binds;
}

// The player's config. It is the defaults until the menu reads it (load).
let config: Config = { lines: [], dvars: {}, binds: { ...DEFAULT_BINDS } };

// Returns the words of a line, as the engine's command parser takes them. A word is in quotes, or goes up to a
// space.
function words(line: string): string[] {
  const out: string[] = [];
  for (let i = 0; i < line.length;) {
    while (line[i] === ' ' || line[i] === '\t') i++;
    if (i >= line.length || line.startsWith('//', i)) break;
    if (line[i] === '"') {
      let w = '';
      for (i++; i < line.length && line[i] !== '"'; i++) {
        if (line[i] === '\\' && line[i + 1] === '"') i++;
        w += line[i];
      }
      out.push(w);
      i++;
    } else {
      let w = '';
      for (; i < line.length && line[i] !== ' ' && line[i] !== '\t'; i++) w += line[i];
      out.push(w);
    }
  }
  return out;
}

// Returns a key as the config names it: in upper case, as the engine writes it (a character, or a name).
const keyName = (k: string): string => k.toUpperCase();

// Reads the text of a config. A player who has no config yet (null) has the defaults.
function parse(textOfConfig: string | null): Config {
  const lines = textOfConfig ? textOfConfig.split(/\r?\n/) : [];
  if (lines.length && lines[lines.length - 1] === '') lines.pop();
  const dvars: Record<string, string> = {};
  let binds: Binds = { ...DEFAULT_BINDS };
  for (const line of lines) {
    const w = words(line);
    const cmd = (w[0] || '').toLowerCase();
    if (/^set[asu]?$/.test(cmd) && w.length >= 3) dvars[w[1].toLowerCase()] = w[2];
    else if (cmd === 'unbindall') binds = {};
    else if (cmd === 'bind' && w.length >= 3) binds[keyName(w[1])] = w[2];
    else if (cmd === 'unbind' && w.length >= 2) delete binds[keyName(w[1])];
  }
  return { lines, dvars, binds };
}

// Returns a dvar's value: the config's, or the default.
function dvar(name: string): string {
  const n = name.toLowerCase();
  return n in config.dvars ? config.dvars[n] : DEFAULTS[n] !== undefined ? DEFAULTS[n] : '';
}

// Returns a row's value, as its choices or its range have it.
function value(row: MultiRow | SliderRow): Value {
  if (row.dvar === 'picmip') return dvar('r_picmip_manual') === '1' ? Number(dvar('r_picmip')) : -1;
  if (row.dvar === 'invert') return Number(dvar('m_pitch')) < 0 ? 1 : 0;
  // What the game draws with: 4x from 4 samples up (8X, 16X), and none below that (2X).
  if (row.dvar === 'r_aaSamples') return Number(dvar(row.dvar)) >= 4 ? 4 : 1;
  if (row.kind === 'multi' && row.dvar === 'bo1_web_resolution') {
    // A resolution that is not among the choices is Auto, as the game starts with it. It is of another shape, or of
    // a screen that has changed since.
    const v = dvar(row.dvar).toLowerCase();
    return row.choices.some((c) => c[1] === v) ? v : 'auto';
  }
  const v = dvar(row.dvar);
  return row.kind === 'multi' && typeof row.choices[0][1] === 'string' ? v : Number(v);
}

// Returns the keys that are bound to a command, in the engine's order (by their numbers), without the gamepad's
// buttons.
function keysFor(cmd: string): string[] {
  const c = cmd.toLowerCase();
  return Object.keys(config.binds).filter((k) => !padKey(k) && config.binds[k].toLowerCase() === c)
    .sort((a, b) => keyNum(a) - keyNum(b));
}

// Returns a file's bytes as a string, a character for each byte. The config is the engine's, in Latin-1, so the menu
// reads and writes it byte for byte. A page's UTF-8 would change the bytes that are not ASCII.
const fromBytes = (bytes: Uint8Array): string => {
  let s = '';
  for (const b of bytes) s += String.fromCharCode(b);
  return s;
};
// Returns a string's bytes, a byte for each character.
const toBytes = (s: string) => Uint8Array.from(s, (c) => c.charCodeAt(0) & 0xff);

// Returns the folder of the player's files in the browser's private file system, where the engine has it
// (web/public/store.js).
async function players(create: boolean): Promise<FileSystemDirectoryHandle> {
  const root = await navigator.storage.getDirectory();
  return root.getDirectoryHandle('players', { create });
}

// Reads one of the player's files. Returns null when there is none.
async function readFile(name: string): Promise<string | null> {
  try {
    const file = await (await (await players(false)).getFileHandle(name)).getFile();
    return fromBytes(new Uint8Array(await file.arrayBuffer()));
  } catch {
    return null; // none yet: the player hasn't played in this browser
  }
}

// Writes one of the player's files.
async function writeFile(name: string, contents: string): Promise<void> {
  const handle = await (await players(true)).getFileHandle(name, { create: true });
  const w = await handle.createWritable();
  await w.write(toBytes(contents));
  await w.close();
}

// Whether a game runs in one of the player's tabs (web/public/player.js: LOCK).
async function gameOn(): Promise<boolean> {
  if (!navigator.locks) return false;
  const locks = await navigator.locks.query();
  return locks.held!.some((l) => l.name === 'zombies-game');
}

// Reads the player's config. The page's volumes follow it.
async function load(): Promise<void> {
  if (!available) return;
  config = parse(await readFile('config.cfg'));
  volumeChanged();
}

// The saves made so far. They are written in order, one at a time.
let saving: Promise<void> = Promise.resolve();

// Saves changes to the config. dvars is the dvars to set, by their names as the engine has them. binds, when given,
// is all the keys: the config's keys are rewritten whole. run is commands for the config to run when the game
// starts, each of them once. Returns whether the changes were saved. They are not while a game is on in another
// tab: the menu then says so (locked).
function save(dvars: Record<string, Value>, binds: Binds | null, run?: string[]): Promise<boolean> {
  const job = saving.then(async () => {
    if (await gameOn()) throw new Error('game on');
    // The config as it is now. Another tab's menu, or a game, may have written it since this menu read it.
    const fresh = parse(await readFile('config.cfg'));
    const lines = fresh.lines.slice();
    for (const [name, v] of Object.entries(dvars)) {
      const line = `seta ${name} "${v}"`;
      let at = -1;
      lines.forEach((l, i) => {
        const w = words(l);
        if (w.length >= 2 && /^set[asu]?$/i.test(w[0]) && w[1].toLowerCase() === name.toLowerCase()) at = i;
      });
      if (at >= 0) lines[at] = line;
      else lines.push(line);
    }
    let out = lines;
    if (binds) {
      // The keys go whole where the config had them, as the engine writes them: unbindall, then each key by its
      // number.
      const isKeyLine = (l: string) => /^(unbindall|bind|unbind)$/i.test(words(l)[0] || '');
      const first = lines.findIndex(isKeyLine);
      const keyLines = ['unbindall', ...Object.keys(binds).sort((a, b) => keyNum(a) - keyNum(b))
        .map((k) => `bind ${k} "${binds[k].replace(/"/g, '\\"')}"`)];
      const rest = lines.filter((l) => !isKeyLine(l));
      const at = first >= 0 ? first : Math.min(rest.length, rest.length && /^\/\//.test(rest[0]) ? 1 : 0);
      out = [...rest.slice(0, at), ...keyLines, ...rest.slice(at)];
    }
    for (const c of run || []) if (!out.includes(c)) out.push(c);
    if (!out.length || !/^\/\//.test(out[0])) out.unshift('// generated by Call of Duty, do not modify');
    await writeFile('config.cfg', out.join('\n') + '\n');
    config = parse(out.join('\n'));
    volumeChanged();
  });
  saving = job.catch(() => {});
  return job.then(() => true, (e: Error) => {
    const why = e.message === 'game on' ? 'a game is on in another tab' : e.message;
    console.log('settings: not saved (' + why + ')');
    if (e.message === 'game on') locked = true;
    render();
    return false;
  });
}

// Sets a row to a new value, as the game's menu sets it, and draws the menu again.
function setValue(row: MultiRow | SliderRow, v: Value): Promise<void> {
  const dvars: Record<string, Value> = {};
  const run: string[] = [];
  if (row.dvar === 'picmip') {
    if (Number(v) < 0) dvars.r_picmip_manual = 0;
    else Object.assign(dvars, { r_picmip_manual: 1, r_picmip: v, r_picmip_bump: v, r_picmip_spec: v });
  } else if (row.dvar === 'invert') {
    Object.assign(dvars, { ui_mousePitch: v, m_pitch: v ? -0.022 : 0.022 });
  } else {
    dvars[row.dvar] = v;
  }
  if (row.kind === 'multi' && row.then) run.push(row.then);
  if (row.kind === 'slider' && row.feel) feel(Number(v));
  return save(dvars, null, run).then(render);
}

// Rumbles the gamepads that the game reads for a moment, at a strength from 0 to 1. That is the strongest that the
// game's rumble would be at this setting of Controller Vibration.
function feel(v: number): void {
  if (!(v > 0) || !navigator.getGamepads) return;
  for (const p of navigator.getGamepads()) {
    if (!p || !p.connected || p.mapping !== 'standard' || !p.vibrationActuator) continue;
    p.vibrationActuator.playEffect('dual-rumble', { duration: 200, strongMagnitude: v, weakMagnitude: v / 2 })
      .catch(() => {});
  }
}

// Binds a key to a row's command, as the game's menu does (Item_Bind_HandleKey), and draws the menu again. The key
// leaves the command that it had. A command that has two keys already is left with the new key alone. null clears
// the command's keys.
function bind(row: BindRow, key: string | null): Promise<void> {
  const binds = { ...config.binds };
  const had = keysFor(row.cmd);
  if (key === null || had.length >= 2) for (const k of had) delete binds[k];
  if (key !== null) binds[key] = row.cmd;
  return save({}, binds).then(render);
}

// Resets what a popup asked about, at its Yes. Default System Settings puts each setting of the Settings screen
// back to its default. Set Default Controls puts back every key, and the settings of the mouse and of the gamepad.
function reset(screen: ScreenName): Promise<boolean> {
  const dvars: Record<string, Value> = {};
  if (screen === 'settings') {
    for (const page of ['graphics', 'sound', 'voice', 'game'] as const) {
      for (const row of PAGES[page]) {
        if (!('dvar' in row)) continue;
        if (row.dvar === 'picmip') {
          for (const d of ['r_picmip_manual', 'r_picmip', 'r_picmip_bump', 'r_picmip_spec']) dvars[d] = DEFAULTS[d];
        } else dvars[row.dvar] = DEFAULTS[row.dvar.toLowerCase()];
      }
    }
    return save(dvars, null);
  }
  for (const d of CONTROL_DVARS) dvars[d] = DEFAULTS[d];
  return save(dvars, { ...DEFAULT_BINDS });
}

// ---- The game's sounds ----

// What the game's menu plays, as the scripts of its items in code_post_gfx.ff have it:
//   - uin_navigation_over when the mouse comes over a live row or a tab, and again at a click on a tab at the top
//     (Settings, Controls)
//   - uin_navigation_click at a click on a tab of the Settings screen
//   - uin_navigation_submenu_over at a popup's Yes
// What else the game's menu plays is uin_mouse_click_t, an alias of the multiplayer game that the game's Zombies
// lacks, so it plays nothing: at a click on a tab of the Controls screen, on a row's choice, or on a popup's No or
// OK. Back, a slider, a key and a greyed-out row play nothing.
const SOUND = { over: 'uin_navigation_over', click: 'uin_navigation_click', popup: 'uin_navigation_submenu_over' };

// The page's player of the game's sounds (useSounds).
let play: (alias: string) => void = () => {};

// Tells the page that the volumes that it plays at changed (onVolume). They change while a volume's slider is
// dragged, before the volume is set (previewed), and when the config is saved or read.
let volumeChanged: () => void = () => {};

// The volume of the slider that is being dragged, or null.
let previewed: { dvar: string; value: number } | null = null;

// Returns a volume as the player set it, or as its slider has it while it is dragged.
const volume = (name: string): number => Number(previewed && previewed.dvar === name ? previewed.value : dvar(name));

// What has the menu's focus, as the game's menu has it: the row or the tab that the mouse was last over, while the
// mouse is over it. A row is its label and its value. An item's sound plays when the item takes the focus.
let focused: string | null = null;

// Gives an item the focus while the mouse is over one of its elements. key names the item.
function focusOn(key: string, items: HTMLElement[]): void {
  const enter = () => {
    if (waiting || focused === key) return;
    focused = key;
    play(SOUND.over);
  };
  const leave = (e: MouseEvent) => {
    if (focused === key && !items.some((i) => i.contains(e.relatedTarget as Node | null))) focused = null;
  };
  for (const e of items) {
    e.addEventListener('mouseenter', enter);
    e.addEventListener('mouseleave', leave);
  }
}

// ---- The menu ----

// The menu's element, #options. The menu makes its elements when it first opens (build).
let root: HTMLDivElement | null = null;
// The game's screen in the menu, which the menu is laid out on (at, fit).
let stage: HTMLDivElement | null = null;

// The screen that shows.
let screen: ScreenName = 'settings';

// The page that is open, of the screen's pages, or null. The game's menu has it the same way. A screen opens with
// its tabs alone (options_new_pc, options_controls_new_pc). A click on a tab opens its page (render). Esc or B
// closes the page, back to the tabs alone (out; the page's onESC).
let opened: PageName | null = null;

// The key's row that waits for a key, or null.
let waiting: BindRow | null = null;

/** What a popup asks about: the reset of a screen's settings. It is 'done' once the controls are reset. */
type Question = ScreenName | 'done';
// What the popup that shows asks about, or null while there is no popup.
let question: Question | null = null;

// Whether a game is on in another tab.
let locked = false;

// What the item under the mouse says: what it does, or why it is greyed out (its data-tip).
let tip = '';

// Where the mouse is over the menu, to find what is under it when the menu is drawn again.
let pointer: [x: number, y: number] | null = null;

// What runs when the menu closes (open).
let onClose: (() => void) | null = null;

// Whether the next click is that of a mouse button that was just bound.
let swallowClick = false;

/** Something that the gamepad's cursor can be on: a tab, a row, or an answer of a popup. */
interface NavItem {
  /** 'tab:' and the tab's label, 'row:' and the row's label, or 'answer:' and the answer's number. */
  key: string;
  /** The column that it is in. */
  column: 'tabs' | 'rows' | 'answers';
  /** Its elements. */
  els: HTMLElement[];
  /** What A does on it. A tab and an answer always have it. A row has none where A does nothing (a slider). */
  act: (() => void) | null;
  /** What left (-1) and right (1) do on a row that they change. */
  step?: ((d: number) => void) | null;
  /** A tab's page, and its place on the game's screen. */
  page?: PageName;
  y?: number;
}

// The gamepad's cursor (pad): the key of what it is on, or null while the mouse has the menu.
let padAt: string | null = null;
// What the cursor can be on, as the menu is drawn (render).
let nav: NavItem[] = [];
// The tab whose popup the gamepad opened. The cursor goes back to it when the popup closes.
let padReturn: string | null = null;

// Whether the gamepad has the menu: the page's last input was a gamepad's. padNav marks the page then (padnav.ts:
// body.padnav), until the mouse moves. The cursor is always on something while it has the menu (render: padHome).
const padding = (): boolean => document.body.classList.contains('padnav');

// Returns where the gamepad's cursor goes when it is on nothing that is there. In a popup that is the last answer,
// the popup's No. The game's popup has No first, and the menu shows No lit at once (settings.css: .om-answer).
// Otherwise it is the tab that opened the popup, or the tab of the page that is open, or the first tab when no page
// is open.
function padHome(): string | null {
  if (question) {
    const answers = nav.filter((n) => n.column === 'answers');
    return answers.length ? answers[answers.length - 1].key : null;
  }
  if (padReturn && nav.some((n) => n.key === padReturn)) return padReturn;
  const tab = (opened && nav.find((n) => n.column === 'tabs' && n.page === opened)) ||
    nav.find((n) => n.column === 'tabs');
  return tab ? tab.key : null;
}

// Goes out, as Esc and B do in the game. With a page open, it goes back to the screen's tabs alone (a page's onESC).
// The gamepad's cursor, from a row, is then on the page's tab, as the game's focus is
// (bo1-zombies/src/ui/ui_shared.cpp: UI_PadKeyRuns). With no page open, it closes the menu. The focus of the row
// that the mouse was on goes with the row's page.
function out(): void {
  if (!opened) return close();
  const on = nav.find((n) => n.key === padAt);
  if (padding() && !(on && on.column === 'tabs')) {
    padAt = 'tab:' + SCREENS[screen].tabs.find((t) => t.page === opened)!.label;
  }
  opened = null;
  if (focused && focused.startsWith('row:')) focused = null;
  render();
}

// Places an item of the game's 640x480 screen on the 1280x720 stage, and returns it. x and y are from the screen's
// middle.
const at = <E extends HTMLElement | SVGElement>(e: E, x: number, y: number, w?: number, h?: number): E => {
  e.style.left = 640 + 1.5 * x + 'px';
  e.style.top = 360 + 1.5 * y + 'px';
  if (w !== undefined) e.style.width = 1.5 * w + 'px';
  if (h !== undefined) e.style.height = 1.5 * h + 'px';
  return e;
};

// The back buffer is what the engine draws at. It is the game's for good: the host sizes the canvas when the game
// starts (web/public/player.js: start), and the game goes full screen on it
// (bo1-zombies/web/src/library_bo1.js). Its size is the video mode's resolution.
//
// Auto is the screen in device pixels, whatever the page's zoom (screenScale), halved each way on a screen of
// HALVED_FROM lines or more. Half of such a screen is 900 lines at least, about the game's own 1080p. The browser
// scales the picture up by exactly 2, which keeps it even. The GPU draws a quarter of the pixels, which keeps the
// frame rate up on a laptop: a 14" MacBook drew Kino's heaviest room at 65-85 frames a second at its 3024x1964 with
// 4x anti-aliasing, and at the display's 120 at 1663x1080. The part of a frame's cost that does not shrink with the
// pixels is the page's and the engine's cost of each draw.
//
// Auto and the screen's full size are both cut to the aspect ratio's shape. Neither is cut at the aspect ratio's
// Auto: the engine's own Auto then picks the nearest of its three shapes for the view, or an ultrawide screen's own
// shape (bo1-zombies/src/gfx_d3d/r_init.cpp: R_StoreWindowSettings). A size is at least 800x600, the least that the
// game takes (R_AddValidResolution), at most 8192 a side, which is WebGPU's most, and even.
//
// The choices after Auto are the screen's full size, where Auto is half of it, and the standard resolutions of the
// screen's shape below the screen's size, by their heights (HEIGHTS). A shape that is not among them (an ultrawide
// screen's, at Auto) takes the heights of 16:9. In a window, or at a shape that is not the screen's, the picture
// keeps its own shape, with black bars at its sides or above and below (page.css: #canvas, object-fit).

// The shapes of the aspect ratio's choices, as width over height.
const SHAPES: Record<string, number> = { standard: 4 / 3, 'wide 16:10': 16 / 10, 'wide 16:9': 16 / 9 };
// The heights of the standard resolutions of each shape.
const HEIGHTS: [shape: number, heights: number[]][] = [[4 / 3, [1536, 1200, 960, 768, 600]],
  [16 / 10, [1600, 1200, 1050, 900, 800]], [16 / 9, [2160, 1800, 1440, 1080, 900, 720]]];
// The lines from which Auto halves a screen.
const HALVED_FROM = 1800;

// The zoom levels of Chromium.
const ZOOMS = [0.25, 1 / 3, 0.5, 2 / 3, 0.75, 0.8, 0.9, 1, 1.1, 1.25, 1.5, 1.75, 2, 2.5, 3, 4, 5];

// Returns the device pixels to one of the screen's pixels, as the page reads the screen's size.
//
// A page's devicePixelRatio is the display's scaling times the page's zoom (Ctrl + and -). Firefox gives the
// screen's size zoomed too, so the two multiply to the screen's pixels at every zoom. Chromium gives the screen's size
// unzoomed, as it does the window's outer size: at 150% a 3440x1440 screen read as 5160x2160, which Auto then halved
// to 2580x1080. Chromium tells a page the zoom apart from the display's scaling only through the Window Management
// permission, which asks the player (getScreenDetails). The device-width media query is unzoomed, as the screen is,
// and the resolution media query is zoomed, as the ratio is.
//
// So the zoom is taken as the window's outer width over its inner width, to the nearest of Chromium's levels
// (ZOOMS). A window that is not maximized has borders, which add a percent or two. The zoom is taken out of the
// ratio where what is left is a display's scaling: 100% or more. A side panel, or the dev tools docked at the side,
// narrow the inner width, and the page is then taken as unzoomed.
function screenScale(): number {
  const dpr = window.devicePixelRatio || 1;
  if (!userAgentData() || !window.outerWidth || !window.innerWidth) return dpr; // not Chromium, or headless
  const zoom = window.outerWidth / window.innerWidth;
  const level = ZOOMS.reduce((a, b) => (Math.abs(Math.log(b / zoom)) < Math.abs(Math.log(a / zoom)) ? b : a));
  return Math.abs(level / zoom - 1) < 0.03 && dpr / level >= 0.99 ? dpr / level : dpr;
}

// Returns the screen's size in device pixels, in the aspect ratio's shape. With halved, the size is halved each way
// where the screen has HALVED_FROM lines or more. A screen that is taller than wide (a mobile device held upright)
// counts on its side. The game's picture is wide, and the back buffer is the game's for good: a tall one drew the
// view squeezed, because the engine's Auto took it for 4:3, and still did once the device was turned. In full screen
// the page turns with the device (bo1-zombies/web/src/library_bo1.js: fullscreenchange).
function screenSize(aspect: string, halved: boolean): Size {
  const scale = screenScale();
  let w = window.screen.width * scale, h = window.screen.height * scale; // not screen, which is the menu's own
  if (h > w) [w, h] = [h, w];
  if (halved && mobile) {
    // Mobile's Auto is MOBILE_LINES high in the screen's shape, whatever the screen's own lines. Those are 1080 or
    // 1440, under HALVED_FROM, and half of 1080 is less than the game takes. MOBILE_LINES is the lowest of the
    // list's standard heights, and a Galaxy played at it.
    if (h > MOBILE_LINES) {
      w *= MOBILE_LINES / h;
      h = MOBILE_LINES;
    }
  } else if (halved && h >= HALVED_FROM) {
    w /= 2;
    h /= 2;
  }
  const shape = SHAPES[String(aspect).toLowerCase()];
  if (shape && w / h > shape) w = h * shape;
  else if (shape) h = w / shape;
  const s = Math.min(Math.max(1, 800 / w, 600 / h), 8192 / w, 8192 / h);
  return { width: Math.round(w * s) & ~1, height: Math.round(h * s) & ~1 };
}

// Returns the size that the video mode's Auto picks.
const autoSize = (aspect: string): Size => screenSize(aspect, true);

/** A choice of the video mode: the name of its text (Auto alone has one), 'auto' or WxH, what it shows, its size. */
type VideoMode = [label: string, value: string, shows: string, size: Size];

// Returns the choices of the video mode.
function videoModes(aspect: string): VideoMode[] {
  const auto = autoSize(aspect), full = screenSize(aspect, false);
  const res = (w: number, h: number) => w + 'x' + h;
  const out: VideoMode[] = [['MENU_AUTO_CAPS', 'auto',
    text('MENU_AUTO_CAPS') + ' (' + res(auto.width, auto.height) + ')', auto]];
  if (full.height !== auto.height) out.push(['', res(full.width, full.height), res(full.width, full.height), full]);
  const standard = HEIGHTS.find(([shape]) => Math.abs(full.width / full.height / shape - 1) < 0.01);
  const [shape, heights] = standard || [full.width / full.height, HEIGHTS[2][1]];
  for (const h of heights) {
    const w = Math.round(h * shape) & ~1;
    if (h < full.height && h !== auto.height && w <= full.width && w >= 800 && h >= 600) {
      out.push(['', res(w, h), res(w, h), { width: w, height: h }]);
    }
  }
  return out;
}

// The common names of the shapes wider than 16:9.
const WIDE: [name: string, shape: number][] = [['21:9', 64 / 27], ['32:10', 32 / 10], ['32:9', 32 / 9]];

// Returns the shape that the engine draws in at Auto, which the aspect ratio's Auto shows. The engine picks it from
// the picture's shape, which is the screen's then, by the height of that shape at 16 wide, rounded
// (bo1-zombies/src/gfx_d3d/r_init.cpp: R_StoreWindowSettings). 9 is 16:9, 10 is 16:10, and more is 4:3 (a 3:2 or
// 5:4 screen too). A screen wider than 16:9 keeps its own shape, which shows by its common name (WIDE: 3440x1440
// and 2560x1080 are 21:9), or as a ratio to 1.
function autoShape(): string {
  const { width, height } = screenSize('auto', false);
  const h16 = Math.round((height * 16) / width);
  if (h16 > 10) return '4:3';
  if (h16 >= 9) return h16 === 10 ? '16:10' : '16:9';
  const named = WIDE.find(([, shape]) => Math.abs(width / height / shape - 1) < 0.05);
  return named ? named[0] : (width / height).toFixed(2) + ':1';
}

// The display's refresh rate in hertz, and how many measurements have started (measureRefresh). The browser paces
// its animation frames at the display's rate, and the game draws on them. The page measures the rate as the game
// does (bo1-zombies/web/src/d3d9_webgpu.cpp: DisplayRate). It takes the intervals of 120 animation frames. Of those
// in which the page missed no refresh, it takes the median, rounded (59.94 Hz is 60). An interval counts as one of
// those when it is at most 1.5 times the interval at the end of the shortest tenth.
//
// The page measures when it loads, and again when the menu opens, which starts over a measurement that still runs.
// The rate is 0 until it is measured, and when the page draws slower than a display refreshes (a test's software
// renderer). The choices of Frame Rate Limit say what each limit draws at the rate, or at 60 Hz until it is known.
// Those of the game's menu do too, until the game has measured its own (web/public/player.js: Module.bo1RefreshHz).
// An adaptive display can show this page slower than a game: a phone's goes from 120 Hz down to 60 or less while
// nothing moves. The choices here then stop short of the game's. Each choice is the same limit in a game all the
// same.
let refreshHz = 0, measures = 0;

// Returns how many refreshes the browser holds each frame for at a limit, as the game has it
// (bo1-zombies/src/qcommon/common.cpp: Com_FrameLimitStep). The number is the same for every frame, because one
// that changes would look unsteady (60 on a 144 Hz display: 2, 3, 2, ...). It is the fewest refreshes that keep to the
// limit, or within a twentieth over it (60 at 144 Hz: 3, which is 48 a second). 1 is no limit.
const frameStep = (display: number, limit: number): number => (limit <= 0 || display <= 0 ? 1
  : Math.max(1, Math.ceil((display * 100) / (limit * 105))));

// Returns the choices of Frame Rate Limit, as the game's menu has them (bo1-zombies/src/ui/ui_main.cpp:
// UI_FrameLimit_Build). There is one for the step of each of the limits 30, 60, 120, 144 and 240. It says what the
// step draws: the frames in a second, rounded. It sets the limit that its step is of, and the lowest of them where
// two limits share a step, so that a choice is the same limit on another display. The choices are 30 and 60 as they
// are at 60, 120 and 240 Hz. They are 29, 48, 72 and 144 at 144 Hz, and 30, 45 and 90 at 90 Hz for the limits 30,
// 60 and 120. The last choice is Unlimited, 0, which is every refresh of whatever display. The player's own limit
// is the choice of its step, and that choice sets it while it is theirs: mobile's 90 shows as 60 at 120 Hz, and as
// 72 at 144 Hz.
function frameLimits(): Choice[] {
  const top = refreshHz || 60;
  const has = Number(dvar('bo1_maxfps')) || 0;
  const limits = new Map<number, number>(); // by step, the limit that its choice sets
  for (const fps of [30, 60, 120, 144, 240]) if (!limits.has(frameStep(top, fps))) limits.set(frameStep(top, fps), fps);
  if (has > 0) limits.set(frameStep(top, has), has);
  return [...[...limits].sort(([a], [b]) => b - a)
    .map(([step, limit]): Choice => [String(Math.floor((top + Math.floor(step / 2)) / step)), limit]),
  ['MPUI_RULES_UNLIMITED_CAPS', 0]];
}

// Measures the display's refresh rate (refreshHz). A new measurement stops one that still runs.
function measureRefresh(): void {
  const measure = ++measures;
  const gaps: number[] = [];
  let last = 0;
  const tick = (t: number) => {
    if (measure !== measures) return;
    if (last) gaps.push(t - last);
    last = t;
    if (gaps.length < 120) {
      requestAnimationFrame(tick);
      return;
    }
    gaps.sort((a, b) => a - b);
    const kept = gaps.filter((gap) => gap <= gaps[12] * 1.5);
    const period = kept[kept.length >> 1], hz = Math.round(1000 / period);
    refreshHz = hz >= 24 && hz <= 500 ? hz : 0;
    console.log('settings: ' + (refreshHz ? 'the display refreshes at ' + refreshHz + ' Hz'
      : 'the display\'s refresh is not known') + ' (of 120 frames, ' + period.toFixed(1) + ' ms)');
    // The choices of Frame Rate Limit go up to the rate.
    if (root && !root.classList.contains('hidden')) render();
  };
  requestAnimationFrame(tick);
}
if (typeof requestAnimationFrame === 'function') measureRefresh();

// Whether there is a gamepad that the game can read. Chrome shows the page a gamepad once one of its buttons is
// pressed, and the game takes those with the standard mapping (bo1-zombies/web/src/library_bo1.js: pollPads).
const gamepadThere = (): boolean => typeof navigator !== 'undefined' && !!navigator.getGamepads &&
  [...navigator.getGamepads()].some((p) => p && p.connected && p.mapping === 'standard');

// Whether a gamepad was there when the menu last looked, or null before it has looked.
let padWasThere: boolean | null = null;

// Draws the menu again when a gamepad has come or gone. The Gamepad tab goes live when a gamepad shows up, and greys
// out again when it goes, while the menu is open. The menu looks at Chrome's events, and when the page sees the
// last gamepad go (padnav.ts: padNav reads the gamepads each frame while one is there). Chrome may still list a
// gamepad at its gamepaddisconnected event, or say nothing of one that is switched off.
const padCheck = (): void => {
  const there = gamepadThere();
  if (there === padWasThere) return;
  padWasThere = there;
  render();
};
if (typeof window !== 'undefined') {
  for (const e of ['gamepadconnected', 'gamepaddisconnected']) {
    window.addEventListener(e, () => {
      padCheck();
      setTimeout(padCheck, 300);
    });
  }
}

// Scales the game's screen to the window.
function fit(): void {
  if (!stage) return;
  const scale = Math.min(window.innerWidth / 1280, window.innerHeight / 720);
  stage.style.transform = `translate(-50%, -50%) scale(${scale})`;
}

// Draws the menu as it is now: the screen, its tabs, the rows of the page that is open, and a question over them.
function render(): void {
  if (!root || !stage || root.classList.contains('hidden')) return;
  stage.textContent = '';
  nav = [];
  const frame = el('div', 'om-frame');
  stage.append(frame);
  const header = el('div', 'om-header');
  frame.append(header, el('div', 'om-body'));
  stage.append(at(el('div', 'om-title', text(SCREENS[screen].title)), -269.4, -182));
  // The tabs of the two screens are at the top right of the header, on its foot, which is on the body
  // (settings.css: .om-tabs). A click opens the screen with its tabs alone. A click on the tab of the screen that
  // shows does too, as the game's menu closes its pages for either.
  const tabs = el('div', 'om-tabs');
  for (const s of ['settings', 'controls'] as const) {
    const t = el('button', 'om-tab' + (s === screen ? ' on' : ''), text(SCREENS[s].title));
    t.type = 'button';
    t.onclick = () => {
      if (waiting) return;
      play(SOUND.over);
      if (screen === s && !opened) return;
      screen = s;
      opened = null;
      render();
    };
    focusOn('screen:' + s, [t]);
    tabs.append(t);
  }
  header.append(tabs);
  // The tabs at the left. A click opens a tab's page, or asks its question over the tabs alone, as the game's menu
  // closes its pages for the popup.
  for (const tab of SCREENS[screen].tabs) {
    const off = tab.off || (tab.gamepad && !gamepadThere() && OFF.gamepad);
    const b = at(el('button', 'om-side' + (off ? ' off' : ''), text(tab.label)), -266.47, tab.y, 170, 17);
    b.type = 'button';
    const click = () => {
      if (waiting) return;
      if (screen === 'settings') play(SOUND.click);
      if (tab.reset) question = tab.reset;
      opened = tab.page || null;
      render();
    };
    if (off) b.dataset.tip = off;
    else b.onclick = click;
    if (!off) {
      focusOn('tab:' + tab.label, [b]);
      nav.push({ key: 'tab:' + tab.label, column: 'tabs', els: [b], act: click, page: tab.page, y: tab.y });
    }
    stage.append(b);
  }
  // The page that is open: its title, then its rows.
  const tab = opened && SCREENS[screen].tabs.find((t) => t.page === opened);
  if (opened && tab) {
    const title = at(el('div', 'om-pagetitle', text(tab.label)), -75.47, -135, 170, 17);
    title.dataset.page = opened;
    stage.append(title);
    // The Gamepad page greys out when the gamepad goes, as its tab does.
    const pageOff = tab.off || (tab.gamepad && !gamepadThere() && OFF.gamepad);
    for (const row of PAGES[opened]) stage.append(...rowElements(row, pageOff));
  }
  stage.append(at(el('div', 'om-hint'), -266.47, 140, 170, 40));
  const back = at(el('button', 'om-back', text('MENU_BACK')), 14.7, 193.3, 32, 20);
  back.type = 'button';
  back.onclick = close;
  stage.append(back);
  if (question) stage.append(...questionElements());
  if (padding() && !waiting) {
    const n = nav.find((e) => e.key === padAt);
    if (!n || (question && n.column !== 'answers')) padAt = padHome();
    if (!question) padReturn = null;
  }
  const padOn = padMark();
  sayUnder(padOn || (pointer && document.elementFromPoint(pointer[0], pointer[1])));
}

/** Why an item is greyed out. An item that is live has a falsy value. */
type Off = string | false | undefined;

// Makes a row's elements: its label and its value, and for some rows a line under them. pageOff is why the row's
// page is greyed out.
function rowElements(row: Row, pageOff: Off): HTMLElement[] {
  const off = row.off || pageOff;
  // The label's box, and the value's. A slider's bar starts at 99 (789 at 1280x720), and a value's text at 115
  // (812).
  const label = at(el('div', 'om-label' + (off ? ' off' : ''), text(row.label)), -75.47, row.y, 170, 17);
  const val = at(el('div', 'om-value' + (off ? ' off' : '')), 99.3, row.y, 160, 17);
  const items: HTMLElement[] = [label, val];
  let act: (() => void) | null = null; // what the gamepad's A does on the row
  let step: ((d: number) => void) | null = null; // what its left and right do, -1 and 1
  if (row.kind === 'slider') {
    const v = Number(dvar(row.dvar));
    const frac = Math.max(0, Math.min(1, (v - row.min) / (row.max - row.min)));
    const bar = el('div', 'om-bar');
    for (let i = 0; i < 10; i++) {
      const seg = el('span');
      seg.style.setProperty('--fill', Math.max(0, Math.min(1, frac * 10 - i)) * 100 + '%');
      bar.append(seg);
    }
    const marker = el('i', 'om-marker');
    marker.style.left = frac * 100 + '%';
    bar.append(marker);
    val.classList.add('slider');
    val.append(bar);
    // The number is whole. The game's own slider leaves a fraction (97.27), which shows rounded.
    if (row.number) val.append(el('span', 'om-number', String(Math.round(v))));
    val.dataset.value = String(v);
    if (!off && !locked) {
      const setAt = (e: PointerEvent) => {
        const r = bar.getBoundingClientRect();
        const f = Math.max(0, Math.min(1, (e.clientX - r.left) / r.width));
        const raw = row.min + f * (row.max - row.min);
        const stepped = Math.round(raw / row.step) * row.step;
        return Number(stepped.toFixed(row.step < 1 ? 2 : 0));
      };
      let dragging = false;
      bar.onpointerdown = (e) => {
        if (e.button !== 0) return;
        dragging = true;
        bar.setPointerCapture(e.pointerId);
        preview(bar, row, setAt(e));
      };
      bar.onpointermove = (e) => {
        if (dragging) preview(bar, row, setAt(e));
      };
      bar.onpointerup = (e) => {
        if (!dragging) return;
        dragging = false;
        setValue(row, setAt(e)).then(() => {
          if (!previewed) return;
          previewed = null;
          volumeChanged();
        });
      };
      // The gamepad's left and right move the slider a twentieth of its range, as the game's slider moves at an
      // arrow key (Item_Slider_HandleKey). A row with a step of its own (by) goes to the next of its marks, and
      // from the slider's end for a value that is off it, as the game's field of view moves.
      step = (d) => {
        let to: number;
        if (row.by) {
          const marks = (Math.max(row.min, Math.min(row.max, v)) - row.min) / row.by;
          to = row.min + (d > 0 ? Math.floor(marks + 0.01) + 1 : Math.ceil(marks - 0.01) - 1) * row.by;
        } else {
          to = v + d * Math.max(row.step, (row.max - row.min) * 0.05);
        }
        to = Math.max(row.min, Math.min(row.max, Math.round(to / row.step) * row.step));
        if (to !== v) setValue(row, Number(to.toFixed(row.step < 1 ? 2 : 0)));
      };
    }
  } else if (row.kind === 'multi') {
    const v = off && row.shownOff !== undefined ? row.shownOff : value(row);
    const choices = row.choices;
    const i = choices.findIndex(([, c]) => (typeof c === 'number' ? Math.abs(c - Number(v)) < 1e-4
      : c.toLowerCase() === String(v).toLowerCase()));
    // The choice is in a box of its own. The arrows that the game draws either side of a choice that it can change
    // go round the box while the row is lit (settings.css: .om-choice).
    val.append(el('span', 'om-choice', i >= 0 ? choices[i][2] || text(choices[i][0]) : String(v)));
    val.dataset.value = String(v);
    if (!off && !locked) {
      const change = (d: number) => {
        const n = choices.length;
        setValue(row, choices[((i < 0 ? 0 : i) + d + n) % n][1]);
      };
      step = change;
      act = () => change(1);
      for (const e of [label, val]) {
        e.onclick = () => change(1);
        e.oncontextmenu = (ev) => {
          ev.preventDefault();
          change(-1);
        };
      }
    }
  } else if (row.kind === 'bind') {
    const keys = keysFor(row.cmd);
    val.textContent = waiting === row ? text('MENU_BIND_KEY_PENDING')
      : keys.length ? keys.slice(0, 2).map(keyText).join(' ' + text('KEY_OR') + ' ') : text('KEY_UNBOUND');
    val.dataset.keys = keys.join(' ');
    if (!off && !locked) {
      for (const e of [label, val]) {
        e.onclick = (ev) => {
          if (waiting) return;
          ev.stopPropagation();
          waiting = row;
          render();
        };
      }
      act = () => {
        waiting = row;
        render();
      };
    }
  } else {
    val.textContent = row.value();
    if (row.below) items.push(at(el('div', 'om-below off', row.below()), -65.47, row.y + 19, 170, 14));
  }
  if (!off && !locked) {
    label.classList.add('live');
    focusOn('row:' + row.label, [label, val]);
    nav.push({ key: 'row:' + row.label, column: 'rows', els: [label, val], act, step });
  }
  const says = off || row.tip || (row.kind === 'bind' && !locked && TIP.key);
  if (says) label.dataset.tip = val.dataset.tip = says;
  // The game highlights the row under the mouse: its label goes white.
  const lit = (on: boolean) => label.classList.toggle('lit', on && !off && !locked);
  for (const e of [label, val]) {
    e.addEventListener('mouseenter', () => lit(true));
    e.addEventListener('mouseleave', () => lit(false));
  }
  label.dataset.row = row.label;
  val.dataset.row = row.label;
  if (row.kind === 'multi' && row.picture && !off) label.dataset.picture = val.dataset.picture = row.picture;
  return items;
}

// Says what the menu has to say, at the foot of the tabs' column, where the game's menu has nothing. It says what
// the menu waits for. Otherwise it says what the item under the mouse does, or why that item is greyed out (its
// data-tip). Otherwise it says that a game in another tab holds the settings.
function say(): void {
  const h = stage && stage.querySelector('.om-hint');
  if (h) h.textContent = waiting ? text('EXE_KEYWAIT') : tip || (locked ? LOCKED : '');
}

// Says what the item under the mouse says, and shows its picture. target is an element of the item, or anything
// else when the mouse is over no item. The menu calls it when the mouse moves, and when it is drawn again under the
// mouse.
function sayUnder(target: EventTarget | null): void {
  const item = target instanceof Element ? target.closest<HTMLElement>('.om-stage [data-tip]') : null;
  tip = item ? item.dataset.tip || '' : '';
  say();
  padPicture(target);
}

// The picture of the Gamepad page that shows (PAD_PICTURE): 'sticks', 'buttons', or null for none.
let padShown: string | null = null;
// The elements of the picture that shows.
let padItems: (HTMLElement | SVGElement)[] = [];

// Shows the picture of the Gamepad page for the row that the mouse is over, or takes it away. target is as for
// sayUnder. The picture is drawn again when the menu is, with the layout that is chosen then.
function padPicture(target: EventTarget | null): void {
  const row = target instanceof Element ? target.closest<HTMLElement>('.om-stage [data-picture]') : null;
  const want = row?.dataset.picture ?? null;
  if (want === padShown && padItems.every((e) => e.isConnected)) return;
  for (const e of padItems) e.remove();
  padItems = [];
  padShown = want;
  if (!want || !stage) return;
  // Turns a place from the top of the game's screen into the place that at() takes, from the screen's middle, a row
  // lower.
  const fromTop = (x: number, y: number, w: number, h: number): Box => [x, y - 240 + PAD_DROP, w, h];
  const image = (name: string, x: number, y: number, w: number, h: number) => {
    const img = at(el('img', 'om-pad'), ...fromTop(x, y, w, h));
    img.alt = '';
    img.onerror = () => img.remove(); // a server without the game files
    img.src = '/images/' + name + '.png';
    return img;
  };
  const label = (spot: string, name: string) => {
    const [x, y, side] = PAD_SPOTS[spot];
    return at(el('div', 'om-pad om-pad-label ' + (side || 'centre'), text(name)), ...fromTop(x, y, 100, 12));
  };
  padItems.push(image('xenon_controller_top', ...PAD_PICTURE));
  if (want === 'buttons') {
    const NS = 'http://www.w3.org/2000/svg';
    const lines = at(document.createElementNS(NS, 'svg'), ...fromTop(...PAD_PICTURE));
    lines.setAttribute('class', 'om-pad om-pad-lines');
    lines.setAttribute('viewBox', PAD_PICTURE.join(' '));
    for (const [x, y, w, h] of PAD_LINES) {
      const line = document.createElementNS(NS, 'line');
      line.setAttribute('x1', String(x));
      line.setAttribute('y1', String(y));
      line.setAttribute('x2', String(x + w));
      line.setAttribute('y2', String(y + h));
      lines.append(line);
    }
    padItems.push(lines);
    const labels = PAD_BUTTONS[String(dvar('gpad_buttonsConfig')).toLowerCase()] || PAD_BUTTONS.buttons_default;
    for (const [spot, name] of Object.entries(labels)) padItems.push(label(spot, name));
  } else {
    const layout = PAD_STICKS[String(dvar('gpad_sticksConfig')).toLowerCase()] || PAD_STICKS.thumbstick_default;
    layout.pictures.forEach((name, i) => padItems.push(image(name, ...PAD_STICK_PICTURES[i], 62, 62)));
    for (const [spot, name] of Object.entries(layout.labels)) padItems.push(label(spot, name));
  }
  stage.append(...padItems);
}

// ---- The gamepad ----

// Moves the gamepad's cursor at a press of a button. The page reads the gamepad (padnav.ts: padNav), and the menu
// reads the keyboard: the arrows are the d-pad, Enter is A, and Esc is B (keyWhileWaiting). The cursor moves as the
// game's menu moves it (bo1-zombies/src/ui/ui_shared.cpp: UI_PadKeyRuns):
//   - Up and down go through the tabs' column, or through the page's rows.
//   - A on a tab opens its page with the cursor on the page's first row, as a click on the tab does.
//   - B goes out (out). With a page open, it goes back to the tabs alone. The cursor is then on the page's tab from
//     a row, or stays where it is on a tab. With no page open, B closes the menu.
//   - On a row, left and right change it: to the next or the last of its choices, or a slider by a twentieth or by
//     its own block. A does what a click on the row does: it picks the next choice, or a key's row waits for a key
//     of the keyboard. B stops the row waiting.
//   - Left and right on a tab, or on a row that has nothing to change (a key's), switch between Settings and
//     Controls, as the arrow keys do in the game. The shoulder buttons do so anywhere. The other screen shows its
//     tabs alone. The cursor is on the tab at the place of the one that it left, or on the first tab when it left
//     a row (the game's UI_PadFocusOnOpen).
//   - A popup's answers have the cursor alone, from the popup's No, and B is No. The cursor then goes back to the
//     tab that asked.
// A menu that the gamepad opened has the cursor on its first tab at once (render: padHome). After the mouse had the
// menu, the first press only shows the cursor there, or on the tab of the page that is open. B goes out then, as
// Esc does.
function pad(button: PadButton): void {
  if (!root || root.classList.contains('hidden')) return;
  if (waiting) {
    if (button === 'b') {
      waiting = null;
      render();
    }
    return;
  }
  const column = (c: NavItem['column']) => nav.filter((n) => n.column === c);
  const go = (key: string | null) => {
    if (!key || key === padAt) return;
    padAt = key;
    focused = key;
    play(SOUND.over);
    render();
  };
  const here = nav.find((n) => n.key === padAt);
  if (!here || (question && here.column !== 'answers')) {
    if (button !== 'b') {
      go(padHome());
    } else if (question) {
      question = null;
      render();
    } else {
      out();
    }
    return;
  }
  if (question) {
    const answers = column('answers');
    const i = answers.indexOf(here);
    if (button === 'b') {
      question = null;
      render();
    } else if (button === 'a') {
      here.act!();
    } else if (['up', 'down', 'left', 'right'].includes(button)) {
      const d = button === 'up' || button === 'left' ? -1 : 1;
      go(answers[(i + d + answers.length) % answers.length].key);
    }
    return;
  }
  const onTab = here.column === 'tabs';
  if (button === 'lb' || button === 'rb' || ((button === 'left' || button === 'right') && (onTab || !here.step))) {
    screen = screen === 'settings' ? 'controls' : 'settings';
    opened = null;
    const there = onTab && SCREENS[screen].tabs.find((t) => t.y === here.y);
    padAt = there ? 'tab:' + there.label : null;
    play(SOUND.over);
    render();
    if (!padAt) go(padHome());
    return;
  }
  const list = column(here.column);
  const i = list.indexOf(here);
  const rows = () => column('rows');
  if (button === 'up' || button === 'down') {
    go(list[(i + (button === 'up' ? -1 : 1) + list.length) % list.length].key);
  } else if (button === 'b') {
    if (opened) play(SOUND.over);
    out();
  } else if (onTab) {
    if (button === 'a') {
      // A page's tab opens the page, and the cursor goes to the page's first row. A reset's tab asks its question.
      if (!here.page) padReturn = here.key;
      here.act!();
      if (question) go(padHome());
      else if (rows().length) go(rows()[0].key);
    }
  } else if (button === 'a' && here.act) {
    here.act();
  } else if ((button === 'left' || button === 'right') && here.step) {
    here.step(button === 'left' ? -1 : 1);
  }
}

// Marks what the gamepad's cursor is on, as the mouse's highlight marks it: a tab goes white, and so does a row's
// label or a popup's answer. Returns the element that it marked, whose tip and picture the menu shows (sayUnder), or
// null when the cursor is on nothing.
function padMark(): HTMLElement | null {
  const n = padAt && nav.find((e) => e.key === padAt);
  if (!n) return null;
  n.els[0].classList.add(n.column === 'rows' ? 'lit' : 'pad');
  if (n.column === 'answers') n.els[0].parentElement!.classList.add('pad');
  return n.els[0];
}

// Shows a slider at a value while it is dragged, before the value is set.
function preview(bar: HTMLElement, row: SliderRow, v: number): void {
  const frac = (v - row.min) / (row.max - row.min);
  ([...bar.children] as HTMLElement[]).forEach((seg, i) => {
    if (seg.tagName === 'SPAN') seg.style.setProperty('--fill', Math.max(0, Math.min(1, frac * 10 - i)) * 100 + '%');
    else seg.style.left = frac * 100 + '%';
  });
  const number = bar.parentElement!.querySelector('.om-number');
  if (number) number.textContent = String(Math.round(v));
  // A volume: the page's sounds and music follow it while it is dragged, as the game's do.
  if (/^snd_menu_/.test(row.dvar)) {
    previewed = { dvar: row.dvar, value: v };
    volumeChanged();
  }
}

// Makes the game's popup: its title, its question, and Yes and No, or OK once the controls are reset.
function questionElements(): HTMLElement[] {
  const shade = el('div', 'om-shade');
  const box = at(el('div', 'om-popup'), -134.7, -82, 268, 162);
  const [title, body] = question === 'settings' ? ['PLATFORM_DEFAULT_SYSTEM_SETTINGS', 'PLATFORM_RESTORE_GRAPHICS']
    : question === 'controls' ? ['MENU_SET_DEFAULT_CONTROLS', 'PATCH_RESTORE_CONTROLS']
      : ['MENU_SET_DEFAULT_CONTROLS', 'PLATFORM_RESTORE_CONTROLS_CONFIRMATION'];
  box.append(el('div', 'om-popup-title', text(title)), el('div', 'om-popup-text', text(body)));
  const answers: [label: string, act: () => void][] = question === 'done'
    ? [['MENU_OK', () => { question = null; render(); }]]
    : [['MENU_YES', () => {
      const which = question;
      // The popup is stale when the menu was opened again under it. Its Yes then resets nothing.
      if (which !== 'settings' && which !== 'controls') return;
      play(SOUND.popup);
      question = which === 'controls' ? 'done' : null;
      if (locked) question = null;
      reset(which).then(render);
    }], ['MENU_NO', () => { question = null; render(); }]];
  const list = el('div', 'om-popup-answers');
  answers.forEach(([label, act], i) => {
    const b = el('button', 'om-answer', text(label));
    b.type = 'button';
    b.onclick = act;
    list.append(b);
    nav.push({ key: 'answer:' + i, column: 'answers', els: [b], act });
  });
  box.append(list);
  return [shade, box];
}

// The gamepad's button that each key stands for (keyWhileWaiting).
const PAD_KEYS: Record<string, PadButton> = { ArrowUp: 'up', ArrowDown: 'down', ArrowLeft: 'left',
  ArrowRight: 'right', Enter: 'a', Escape: 'b' };

// Whether an event is a key that goes down. keyWhileWaiting hears that, a mouse button that goes down, and a turn of
// the wheel.
const isKeyDown = (e: Event): e is KeyboardEvent => e.type === 'keydown';
// Whether an event is a turn of the wheel.
const isWheel = (e: Event): e is WheelEvent => e.type === 'wheel';

// Takes a key, a mouse button or the wheel. While a key's row waits for one, it binds it to the row: Esc cancels,
// and Backspace clears the row's keys. Otherwise the keyboard's arrows, Enter and Esc move the menu's cursor as the
// gamepad's d-pad, A and B do (pad). The first press after the mouse had the menu only shows the cursor. The page is
// the gamepad's from then (padnav.ts: padNav, body.padnav), until the mouse moves or is pressed. Esc while the mouse
// has the menu closes the menu's popup, or else the page that is open, or else the menu (out), as the game's Esc
// does.
function keyWhileWaiting(e: KeyboardEvent | MouseEvent): void {
  if (e.type === 'mousedown' && !waiting) swallowClick = false;
  if (!waiting) {
    const button = isKeyDown(e) ? PAD_KEYS[e.key] : undefined;
    if (button && !e.altKey && !e.ctrlKey && !e.metaKey && !e.shiftKey && root
      && !root.classList.contains('hidden')) {
      e.preventDefault();
      if (button === 'b' && !padding()) {
        if (question) {
          question = null;
          render();
        } else out();
        return;
      }
      e.stopPropagation();
      document.body.classList.add('padnav');
      pad(button);
    }
    return;
  }
  let key: string | null = null;
  if (isKeyDown(e)) {
    if (e.code === 'Escape') {
      e.preventDefault();
      waiting = null;
      return render();
    }
    if (e.code === 'Backspace') {
      e.preventDefault();
      const row = waiting;
      waiting = null;
      bind(row, null);
      return;
    }
    key = CODES[e.code];
  } else if (e.type === 'mousedown') {
    key = BUTTONS[e.button];
  } else if (isWheel(e)) {
    key = e.deltaY < 0 ? 'MWHEELUP' : 'MWHEELDOWN';
  }
  if (!key) return;
  e.preventDefault();
  e.stopPropagation();
  // The click that the bound mouse button makes is not the menu's: the row would wait for a key again.
  if (e.type === 'mousedown') swallowClick = true;
  const row = waiting;
  waiting = null;
  bind(row, key);
}

// Makes the menu's elements, and has the menu hear the mouse and the keyboard.
function build(): void {
  root = el('div', 'om hidden');
  root.id = 'options';
  root.setAttribute('role', 'dialog');
  root.setAttribute('aria-label', 'Game settings');
  stage = el('div', 'om-stage');
  root.append(stage);
  document.body.append(root);
  root.addEventListener('mousedown', keyWhileWaiting, true);
  root.addEventListener('click', (e) => {
    if (!swallowClick) return;
    swallowClick = false;
    e.preventDefault();
    e.stopPropagation();
  }, true);
  root.addEventListener('wheel', (e) => { if (waiting) keyWhileWaiting(e); }, { capture: true, passive: false });
  root.addEventListener('contextmenu', (e) => e.preventDefault());
  root.addEventListener('mousemove', (e) => {
    pointer = [e.clientX, e.clientY];
    // The browser makes up a move while the mouse is still, when the menu is drawn again under it. Such a move
    // leaves the menu to the gamepad. A move of the mouse itself takes the menu (padnav.ts: padNav, which this runs
    // before).
    if (!e.movementX && !e.movementY && padding()) return;
    document.body.classList.remove('padnav');
    if (padAt) {
      padAt = null;
      render();
    }
    sayUnder(e.target);
  });
  root.addEventListener('mouseleave', () => {
    pointer = null;
    sayUnder(null);
  });
  // A click outside the frame closes the menu, unless a row waits for a key or a popup asks. Outside is the page
  // round the game's screen, or the screen itself (the stage), which fills the window in its shape. A click that
  // was pressed in the menu and let go outside it does not close the menu: a slider dragged out of the frame.
  const outside = (e: MouseEvent) => e.target === root || e.target === stage;
  let pressedOutside = false;
  root.addEventListener('mousedown', (e) => { pressedOutside = outside(e); });
  root.addEventListener('click', (e) => {
    if (outside(e) && pressedOutside && !waiting && !question) close();
  });
  document.addEventListener('keydown', keyWhileWaiting, true);
  window.addEventListener('resize', () => {
    fit();
    render();
  });
}

// The request for the game's text, once the menu has made it.
let textsLoaded: Promise<void> | null = null;

// Gets the game's text for the menu from the server, or offline from the service worker's copy. It asks once.
function loadTexts(): Promise<void> {
  if (textsLoaded) return textsLoaded;
  const names = new Set(['MENU_SETTINGS_CAPS', 'MENU_CONTROLS_CAPS', 'MENU_BACK', 'KEY_OR', 'KEY_UNBOUND',
    'MENU_BIND_KEY_PENDING', 'EXE_KEYWAIT', 'MENU_YES', 'MENU_NO', 'MENU_OK', 'MENU_AUTO_CAPS',
    'MENU_STEREO_SPEAKERS', 'PLATFORM_DEFAULT_SYSTEM_SETTINGS', 'PLATFORM_RESTORE_GRAPHICS',
    'MENU_SET_DEFAULT_CONTROLS', 'PATCH_RESTORE_CONTROLS', 'PLATFORM_RESTORE_CONTROLS_CONFIRMATION']);
  for (const s of Object.values(SCREENS)) for (const t of s.tabs) names.add(t.label);
  for (const rows of Object.values(PAGES)) {
    for (const r of rows) {
      if (!OWN[r.label]) names.add(r.label);
      for (const [c] of r.kind === 'multi' ? r.choices : []) if (/^[A-Z]/.test(c)) names.add(c);
    }
  }
  for (const k of Object.keys(KEYNUMS)) names.add('KEY_' + k);
  for (const labels of Object.values(PAD_BUTTONS)) for (const n of Object.values(labels)) names.add(n);
  for (const { labels } of Object.values(PAD_STICKS)) for (const n of Object.values(labels)) names.add(n);
  textsLoaded = fetch('/strings?keys=' + [...names].sort().join(','))
    .then<Record<string, string> | null>((rsp) => (rsp.ok ? rsp.json() : {}))
    .then((t) => { texts = t || {}; }, () => {});
  return textsLoaded;
}

// Opens the menu on a screen, 'settings' or 'controls'. closed() runs when the menu closes.
async function open(which?: ScreenName, closed?: () => void): Promise<boolean> {
  if (!available) return false;
  if (!root) build();
  screen = which || 'settings';
  opened = null;
  question = null;
  waiting = null;
  focused = null;
  pointer = null;
  padAt = null;
  padReturn = null;
  onClose = closed || null;
  await Promise.all([loadTexts(), load()]);
  locked = await gameOn();
  root!.classList.remove('hidden'); // build made it
  fit();
  render();
  measureRefresh();
  padWasThere = gamepadThere();
  return true;
}

// Closes the menu, if it is open.
function close(): void {
  if (!root || root.classList.contains('hidden')) return;
  waiting = null;
  question = null;
  padAt = null;
  padReturn = null;
  root.classList.add('hidden');
  if (onClose) onClose();
}

/** The options menu and the player's settings (Settings). */
export const bo1Settings: Settings = {
  available,
  mobile,
  recommended: RECOMMENDED,
  open,
  close,
  useSounds: (fn) => { play = fn; },
  sfxGain: () => volume('snd_menu_sfx') ** 2 * volume('snd_menu_master') ** 2,
  musicGain: () => volume('snd_menu_music') ** 2 * volume('snd_menu_master') ** 2,
  cinematicGain: () => volume('snd_menu_cinematic') ** 2 * volume('snd_menu_master') ** 2,
  onVolume: (fn) => { volumeChanged = fn; },
  shown: () => !!root && !root.classList.contains('hidden'),
  pad,
  gamepadsChanged: () => padCheck(),
  refreshHz: () => refreshHz,
  maxFpsDefault: DEFAULTS.bo1_maxfps,
  saved: () => saving,
  backBuffer: async () => {
    await load();
    const modes = videoModes(dvar('r_aspectRatio'));
    const v = dvar('bo1_web_resolution').toLowerCase();
    return (modes.find((m) => m[1] === v) || modes[0])[3];
  },
  fullScreen: () => dvar('bo1_web_fullscreen') !== '0',
  load,
};
