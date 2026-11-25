// web/src/controls.ts - the kinds of control that the lobby's views share: a copy button, a switch, a row of options
// (a room's size, its game), a badge, and a view's main button.
import { el } from './dom';
import { act, page } from './lobby';
import { showToast } from './screen';

const copiedTimers = new Map<HTMLElement, number>();

/**
 * Copies the text to the clipboard, and has the button say so for 2 s. A page that is not served securely has no
 * clipboard: the text is then copied the old way, through a selected text field. Where that fails too, the text
 * shows, for the player to copy by hand.
 */
export function copy(button: HTMLElement, text: string): void {
  const copied = () => {
    button.classList.add('copied');
    clearTimeout(copiedTimers.get(button));
    copiedTimers.set(button, window.setTimeout(() => button.classList.remove('copied'), 2000));
  };
  const byField = () => {
    const field = el('textarea');
    field.value = text;
    field.setAttribute('readonly', '');
    field.style.cssText = 'position: fixed; top: 0; left: 0; opacity: 0;';
    document.body.append(field);
    field.select();
    let ok = false;
    try {
      ok = document.execCommand('copy');
    } catch {
      // The browser does not allow it.
    }
    field.remove();
    if (ok) copied();
    else showToast(text);
  };
  if (!navigator.clipboard) return byField();
  navigator.clipboard.writeText(text).then(copied, byField);
}

// How many changes of a control the lobby is yet to answer. Until it has answered, the control stays as the player
// set it, whatever a view says. A view from before the lobby heard of the change would flip the control back for a
// moment, and such views come every other second while another member downloads.
const changing = new WeakMap<Element, number>();

// Asks the lobby to change the room, for a control that already shows the change. When the lobby refuses, the page
// draws the room again as the lobby has it.
async function changeRoom(control: Element, change: object): Promise<void> {
  changing.set(control, (changing.get(control) || 0) + 1);
  const ok = await act('room', change);
  changing.set(control, (changing.get(control) || 0) - 1);
  if (!ok) page.redraw();
}

/** Makes a switch (.switch): a button that says whether it is on, with the word beside it. */
export function switchButton(label: string): HTMLButtonElement {
  const b = el('button', 'switch');
  b.type = 'button';
  b.setAttribute('role', 'switch');
  b.setAttribute('aria-label', label);
  const track = el('span', 'track');
  track.append(el('span', 'thumb'));
  b.append(el('span', 'word'), track);
  return b;
}

function switchSet(b: HTMLElement, on: boolean): void {
  b.setAttribute('aria-checked', String(on));
  b.querySelector('.word')!.textContent = on ? 'On' : 'Off';
}

export const switchOn = (b: HTMLElement): boolean => b.getAttribute('aria-checked') === 'true';

/** Sets a switch to what a view says of it. */
export function switchShow(b: HTMLElement, on: boolean): void {
  if (!changing.get(b)) switchSet(b, on);
}

/**
 * Flips a switch of the room, at the player's click. The switch shows its new state at once, and the lobby is asked
 * for the change that change(on) describes.
 */
export function flipSwitch(b: HTMLElement, change: (on: boolean) => object): Promise<void> {
  const on = !switchOn(b);
  switchSet(b, on);
  return changeRoom(b, change(on));
}

const CHECK_ICON = '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" ' +
  'stroke-width="3" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M20 6 9 17l-5-5"/>' +
  '</svg>';

/**
 * Sets what a badge says, after a tick where it has one. The words are in a box of their own, which the style sheet
 * trims to the height of their capitals, so that they sit in the badge's middle (text-box).
 */
export function badgeSay(badge: HTMLElement, words: string, tick = false): void {
  badge.innerHTML = tick ? CHECK_ICON : '';
  badge.append(el('span', 't', words));
}

/** Shows On or Off on a badge, for a player who can't flip the switch. */
export function showOnOff(badge: HTMLElement, on: boolean): void {
  badge.classList.toggle('on', on);
  badge.classList.toggle('off', !on);
  badgeSay(badge, on ? 'On' : 'Off', on);
}

/**
 * What a row of options picks from: numbers, such as a room's size, or names, such as a room's game. An option's
 * button says its label, or its value where it has none.
 */
export interface Options<V extends number | string = number> {
  name: string;
  options: { value: V; label?: string }[];
}

/**
 * Makes a row of options (.seg): a button for each, of which one is checked. A click shows its pick at once, and the
 * lobby is asked for the change that set(value) describes.
 */
export function optionButtons<V extends number | string>(of: Options<V>, set: (value: V) => object): HTMLDivElement {
  const box = el('div', 'seg');
  box.setAttribute('role', 'radiogroup');
  box.setAttribute('aria-label', of.name);
  for (const o of of.options) {
    const b = el('button', '', o.label ?? String(o.value));
    b.type = 'button';
    b.setAttribute('role', 'radio');
    b.dataset.value = String(o.value);
    b.onclick = () => {
      optionSet(box, o.value);
      changeRoom(box, set(o.value));
    };
    box.append(b);
  }
  return box;
}

function optionSet(box: Element, value: number | string): void {
  for (const b of box.children) {
    b.setAttribute('aria-checked', String((b as HTMLElement).dataset.value === String(value)));
  }
}

/** Sets a row of options to what a view says of it. */
export function optionShow(box: Element, value: number | string): void {
  if (!changing.get(box)) optionSet(box, value);
}

/** What a view's main button says (setButton). */
export interface MainButton {
  label: string;
  title?: string;
  /** The button only says something, and does nothing. */
  disabled?: boolean;
  /** How much of a download the browser has, from 0 to 1: the button fills that far. */
  got?: number;
}

/**
 * Sets a view's main button: the room's Ready, or Play on the page to play locally. A button that is off is
 * aria-disabled, not disabled (dom.ts: isOff). It looks the same and does nothing, but it can keep the focus while
 * it says how far a download is, and still has it when it turns to Ready. A disabled button can't have the focus:
 * the browser took it away as the download began, and the player had to find the button again.
 */
export function setButton(b: HTMLElement, m: MainButton, primary = false): void {
  b.textContent = m.label;
  b.title = m.title || '';
  if (m.disabled) b.setAttribute('aria-disabled', 'true');
  else b.removeAttribute('aria-disabled');
  b.classList.toggle('primary', primary);
  b.classList.toggle('fill', m.got !== undefined);
  if (m.got !== undefined) b.style.setProperty('--got', (m.got * 100).toFixed(1) + '%');
}
