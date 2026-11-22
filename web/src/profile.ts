// web/src/profile.ts - the player, at the top of the page: their name and picture, on a button that opens the profile.
//
// The profile is a menu of what is the player's: their name, their picture with a button to pick another, their game
// folder (folder.ts), the game files that this browser keeps, and the line that plays their room's games in the
// Windows client. A click outside it, or Esc, closes it.
//
// The player changes their name in the profile's field. Enter, or leaving the field, keeps the new name. Esc does
// not. The name can't be changed while their room's game runs, because the game knows them by it. Where the server
// has its players sign in with Discord, the name is the one they have there, and they change it on Discord. A button
// in the field then has the server look the name up again, and the profile signs them out.
//
// The picker shows the pictures that the server has to pick from, over the page: the game's own, each kind under its
// heading (server/pictures.go), after the player's picture on Discord where they have one. The picture that shows
// for the player is checked. A click picks another at once, and the lobby's next view shows it to everyone.
import type { Picture, View } from './api';
import { setAvatar } from './avatar';
import { copy } from './controls';
import { $, el, placeTip } from './dom';
import { folder, folderNote, storeUsage } from './files';
import { act, ask, me, offline, page } from './lobby';
import { asking, mayLeaveGame } from './question';
import { store } from './storage';
import { playLine } from './windows';

// ---- The name ----

let editingName = false; // the player is typing a name, and what the lobby says does not overwrite it

/** Shows the player's name, at the top and in the profile. With locked, the player can't change it now. */
export function showName(name: string, locked: boolean): void {
  locked = locked || !!me.discord;
  const field = $<HTMLInputElement>('name');
  $('me').classList.remove('hidden');
  $('namelabel').textContent = name;
  setAvatar($('nameinitial'), name, me.avatar);
  $('namechip').setAttribute('aria-label', 'You: ' + name);
  field.disabled = locked;
  $('namenote').textContent = me.discord ? 'Synced from Discord.' : locked && !offline ? 'Locked while in game.' : '';
  $('namefield').classList.toggle('synced', !!me.discord && !offline);
  $('namerefresh').classList.toggle('hidden', !me.discord || offline);
  $('signout').classList.toggle('hidden', !me.discord);
  if (locked) editingName = false;
  if (!editingName) field.value = name;
}

// Has the server look the player up on Discord again (server/discord.go: POST /lobby/refresh). The lobby's next view
// has the name. The server does this at most every 15 s, and the button waits as long.
async function refreshName(): Promise<void> {
  const button = $<HTMLButtonElement>('namerefresh');
  if (button.disabled) return;
  button.disabled = true;
  button.classList.add('busy');
  await ask('refresh');
  button.classList.remove('busy');
  setTimeout(() => {
    button.disabled = false;
  }, 15000);
}

// Takes the name in the field as the player's, and says hello to the lobby with it.
function saveName(): void {
  editingName = false;
  const field = $<HTMLInputElement>('name');
  const name = field.value.trim();
  if (!name || field.disabled) {
    field.value = me.name;
    return;
  }
  if (name === me.name) return;
  me.name = name;
  $('namelabel').textContent = name;
  setAvatar($('nameinitial'), name, me.avatar);
  page.hello();
}

// ---- The pictures ----

let pickable: Picture[] = []; // the pictures to pick from, as the lobby last said
let builtFrom: Picture[] | null = null; // the pictures that the picker's buttons were made from
let builtDiscord: string | null = null; // the player's picture on Discord that the buttons were made with

/**
 * Shows the player's picture in the profile, and the pictures to pick from in the picker.
 *
 * The lobby names the pictures in its first view, and again after the server has others (server/rooms.go: events).
 * The page keeps the list. The picker's buttons are made again only when the list changes, so that a button keeps
 * the focus as it is picked. The pictures load as the picker shows them.
 */
export function showPictures(v: View): void {
  if (v.pictures && JSON.stringify(v.pictures) !== JSON.stringify(pickable)) pickable = v.pictures;
  const game = pickable;
  $('picturechange').classList.toggle('hidden', !game.length);
  if (!game.length) openPicker(false);
  setAvatar($('mypicture'), v.you.name, v.you.avatar);
  const box = $('pictures');
  const discord = v.you.discordAvatar || '';
  if (builtFrom !== game || builtDiscord !== discord) {
    builtFrom = game;
    builtDiscord = discord;
    delete box.dataset.checked;
    const list = game.map((p) => ({ id: p.id, name: p.name, group: p.group || 'Icons',
      url: '/pictures/' + encodeURIComponent(p.id) + '.png' }));
    if (discord) list.unshift({ id: '', name: 'Discord avatar', group: 'Discord', url: discord });
    box.textContent = '';
    let group: HTMLElement | null = null;
    for (const p of list) {
      if (!group || group.dataset.group !== p.group) {
        group = el('div', 'group');
        group.dataset.group = p.group;
        box.append(el('div', 'label', p.group), group);
      }
      const b = el('button');
      b.type = 'button';
      b.setAttribute('role', 'radio');
      b.setAttribute('aria-label', p.name);
      b.dataset.id = p.id;
      const img = el('img');
      img.crossOrigin = 'anonymous';
      img.alt = '';
      img.loading = 'lazy';
      img.src = p.url;
      b.append(img);
      b.onclick = async () => {
        if (b.getAttribute('aria-checked') === 'true') return;
        checkPicture(p.id);
        if (!(await act('picture', { picture: p.id }))) page.redraw();
      };
      group.append(b);
    }
  }
  checkPicture(v.you.picture || '');
}

const pictureButtons = (): HTMLButtonElement[] => [...$('pictures').querySelectorAll('button')];

// Checks the picture with the ID among the picker's buttons, when it is another than was checked. The keyboard's
// focus stops at the checked picture alone (Tab). Where no button is the player's picture, it stops at the first:
// the server no longer has their picture, and they have none on Discord.
function checkPicture(id: string): void {
  const box = $('pictures');
  if (box.dataset.checked === id) return;
  box.dataset.checked = id;
  const all = pictureButtons();
  for (const b of all) {
    const checked = b.dataset.id === id;
    b.setAttribute('aria-checked', String(checked));
    b.tabIndex = checked ? 0 : -1;
  }
  if (all.length && !all.some((b) => b.tabIndex === 0)) all[0].tabIndex = 0;
}

// Moves the focus in the picker with the keyboard's arrows: to the next picture left or right, or to the nearest
// picture in the row above or below.
function pictureKey(e: KeyboardEvent): void {
  const from = (e.target as Element).closest<HTMLButtonElement>('#pictures button');
  const ways: Record<string, [number, number]> = {
    ArrowLeft: [-1, 0], ArrowRight: [1, 0], ArrowUp: [0, -1], ArrowDown: [0, 1],
  };
  const way = ways[e.key];
  if (!from || !way) return;
  e.preventDefault();
  const all = pictureButtons();
  let to: HTMLButtonElement | undefined = all[all.indexOf(from) + way[0]];
  if (way[1]) {
    const at = (b: Element) => b.getBoundingClientRect();
    const r = at(from);
    const rows = all.filter((b) => (at(b).top - r.top) * way[1] > 1);
    const row = rows.length && (way[1] > 0 ? Math.min : Math.max)(...rows.map((b) => at(b).top));
    to = rows.filter((b) => Math.abs(at(b).top - row) < 1)
      .sort((a, b) => Math.abs(at(a).left - r.left) - Math.abs(at(b).left - r.left))[0];
  }
  if (!to) return;
  to.focus();
  to.scrollIntoView({ block: 'nearest' });
}

// The tip: the name of a picture in the picker, over the picture, or under it at the top of the window.
//
// The tip is of the picture that the player pointed at last, with the pointer or with the focus, while the pointer or
// the focus is still on it. The pointer points when it moves on a picture, not when the list scrolls under it. The
// focus points when it comes to a picture and shows, as the keyboard's and the gamepad's focus does. The focus of a
// click does not show, nor does the focus that the picker opens with at a click. So no tip shows while the pointer is
// between two pictures or off them, whichever picture has the focus. Otherwise the tip of the checked picture would
// come and go as the pointer crosses the others.
//
// The tip waits TIP_WAIT for the pointer or the focus to stay, and then fades in, so the pictures that the pointer
// only passes show none. Once a tip has shown, the tip of the next picture comes at once, with no wait and no fade,
// if the picture is pointed at within TIP_NEXT of a tip last showing. The names of a row can then be read one after
// another. A tip that had not shown yet when the pointer moved on does not make the next one come sooner.
//
// The tip follows its picture as the list scrolls, and goes when the picture leaves what the list shows.
const tipped = {
  over: null as HTMLElement | null, // the picture that the pointer is on
  focus: null as HTMLElement | null, // the picture that has the focus
  last: 'over' as 'over' | 'focus', // which of the two pointed last
  seen: -Infinity, // when a tip last showed
  of: null as HTMLElement | null, // the picture that the tip is of
  comes: 0, // when that tip is to show, after its wait
};
const TIP_WAIT = 250, TIP_FADE = 100, TIP_NEXT = 500; // ms
const focusShows = (b: Element): boolean => b.matches(':focus-visible, body.padnav :focus');

/**
 * Shows, moves or hides the tip of the picker's pictures. by says what pointed at a picture just now: the pointer
 * ('over') or the focus ('focus').
 */
export function pictureTip(by?: 'over' | 'focus' | ''): void {
  const tip = $('picturetip');
  if (by) tipped.last = by;
  const b = tipped.last === 'focus' && tipped.focus && focusShows(tipped.focus) ? tipped.focus : tipped.over;
  const list = $('pictures').getBoundingClientRect();
  const r = b && b.isConnected ? b.getBoundingClientRect() : null;
  const shows = !!b && !!r && pickerOpen() && r.bottom > list.top && r.top < list.bottom;
  tip.classList.toggle('hidden', !shows);
  const was = tipped.of, now = performance.now();
  if (was && now >= tipped.comes) tipped.seen = now; // the tip of that picture had come, and showed until now
  tipped.of = shows ? b : null; // a tip of the same picture is a new tip once this one went
  if (!shows) return;
  tip.textContent = b.getAttribute('aria-label');
  placeTip(tip, r);
  if (b === was) return;
  for (const a of tip.getAnimations()) a.cancel();
  const wait = now - tipped.seen < TIP_NEXT ? 0 : TIP_WAIT;
  tipped.comes = now + wait;
  if (wait) tip.animate({ opacity: [0, 1] }, { delay: wait, duration: TIP_FADE, fill: 'backwards' });
}

/** Whether the picker shows. */
export const pickerOpen = (): boolean => !$('picker').classList.contains('hidden');

/**
 * Opens or closes the picker. It opens from the button in the profile, with the focus on the checked picture, and
 * closes with Done, Esc or a click beside it, with the focus back on that button. The profile stays open under it.
 * It also closes when the lobby goes from under it: for a game (game.ts: startEngine) or for the sign-in.
 */
export function openPicker(open: boolean): void {
  if (open === pickerOpen()) return;
  $('picker').classList.toggle('hidden', !open);
  const checked = pictureButtons().find((b) => b.tabIndex === 0);
  if (open) (checked || $('pickerdone')).focus();
  else if (profileOpen()) $('picturechange').focus();
  if (open && checked) checked.scrollIntoView({ block: 'center' });
  if (!open) tipped.over = tipped.focus = null;
  pictureTip();
}

// Keeps Tab inside the picker, either way: from Done to the checked picture, and from a picture to Done. It does not
// go on to the page under the picker.
function pickerTab(e: KeyboardEvent): void {
  e.preventDefault();
  const picture = pictureButtons().find((b) => b.tabIndex === 0);
  (document.activeElement === $('pickerdone') && picture ? picture : $('pickerdone')).focus();
}

// ---- The profile ----

const profileOpen = (): boolean => !$('profile').classList.contains('hidden');

/** Opens or closes the profile. Closing it keeps the name that the player typed, and closes the picker. */
export function openProfile(open: boolean): void {
  if (!open) openPicker(false);
  if (open === profileOpen()) return;
  $('profile').classList.toggle('hidden', !open);
  $('namechip').setAttribute('aria-expanded', String(open));
  if (open) {
    showProfile();
    storeUsage();
  } else {
    saveName();
  }
}

/**
 * Sets what the profile says of the game folder, and its line for the Windows client. How much the browser's store
 * holds comes from the store (main.ts: makePlayer, onUsage).
 */
export function showProfile(): void {
  $('foldername').textContent = folder ? folder.name : '';
  $('folderhelp').textContent = folderNote || (folder ? '' : 'Import your own copy of the game files.');
  $('folderpick').textContent = folder ? 'Change' : 'Choose folder';
  $('folderforget').classList.toggle('hidden', !folder);
  $('playbat').textContent = playLine();
}

/** Makes the profile and the picker work: sets what their controls do. The page calls it once, when it starts. */
export function profileInit(): void {
  const field = $<HTMLInputElement>('name');
  $('namechip').onclick = () => openProfile(!profileOpen());
  field.addEventListener('input', () => {
    editingName = true;
  });
  field.addEventListener('keydown', (e) => e.key === 'Enter' && saveName());
  field.addEventListener('change', saveName);
  // Esc closes the profile, and leaves the name as it was.
  $('me').addEventListener('keydown', (e) => {
    if (e.key !== 'Escape' || !profileOpen()) return;
    field.value = me.name;
    editingName = false;
    openProfile(false);
    $('namechip').focus();
  });
  // A click outside the profile closes it. A click on the lobby's question does not, which the profile's Remove
  // asks, nor does a click in the picker, which the profile's Change opens.
  document.addEventListener('pointerdown', (e) => {
    const at = e.target as Node;
    if (profileOpen() && !$('me').contains(at) && !$('ask').contains(at) && !$('picker').contains(at)) {
      openProfile(false);
    }
  });
  $('picturechange').onclick = () => openPicker(true);
  $('pickerdone').onclick = () => openPicker(false);
  $('picker').addEventListener('click', (e) => e.target === $('picker') && openPicker(false));
  $('picker').addEventListener('keydown', (e) => (e.key === 'Escape' ? openPicker(false) : pictureKey(e)));
  // Tab is caught wherever the focus is: a click on the picker's panel leaves the focus on none of its buttons.
  document.addEventListener('keydown', (e) => e.key === 'Tab' && pickerOpen() && !asking() && pickerTab(e));
  const pictures = $('pictures');
  const tipOf = (which: 'over' | 'focus') => (e: Event) => {
    const b = e.type === 'mouseout' || e.type === 'focusout' ? null
      : (e.target as Element).closest<HTMLElement>('#pictures button');
    tipped[which] = b;
    pictureTip(which === 'focus' && b && focusShows(b) ? 'focus' : '');
  };
  for (const type of ['mouseover', 'mouseout']) pictures.addEventListener(type, tipOf('over'));
  for (const type of ['focusin', 'focusout']) pictures.addEventListener(type, tipOf('focus'));
  // Only a move of the mouse itself points. The same move takes the page back from the gamepad (padnav.ts).
  pictures.addEventListener('mousemove', (e) => {
    if (tipped.last !== 'over' && tipped.over && (e.movementX || e.movementY)) pictureTip('over');
  });
  pictures.addEventListener('scroll', () => pictureTip(), { passive: true });
  addEventListener('resize', () => pictureTip());
  $('copyplay').onclick = () => copy($('copyplay'), playLine());
  $('namerefresh').onclick = refreshName;
  $('signout').onclick = async () => {
    if (!(await mayLeaveGame())) return;
    await ask('signout');
    store.set('zombies-token', '');
    location.reload();
  };
}
