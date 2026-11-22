// web/src/dom.ts - helpers for the page's elements.

/** The element of index.html with this id. Every id that the page's code names is in index.html. */
export const $ = <T extends HTMLElement = HTMLElement>(id: string) => document.getElementById(id) as T;

/** A new element, with its class and text. */
export function el<K extends keyof HTMLElementTagNameMap>(tag: K, cls = '', text?: string): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

/**
 * Whether a button is off: disabled, or aria-disabled. A view's main button is aria-disabled while it only says
 * something (controls.ts: setButton). Such a button keeps the focus and takes a click, which does nothing.
 */
export function isOff(b: HTMLButtonElement): boolean {
  return b.disabled || b.getAttribute('aria-disabled') === 'true';
}

/**
 * Places a tip over the middle of what it is of. r is that thing's place in the window. The tip goes under it when
 * there is no room above, and stays within the window's sides.
 */
export function placeTip(tip: HTMLElement, r: DOMRect): void {
  const w = tip.offsetWidth, h = tip.offsetHeight;
  const top = r.top - h - 8;
  tip.style.top = (top < 4 ? r.bottom + 8 : top) + 'px';
  tip.style.left = Math.max(4, Math.min(innerWidth - w - 4, r.left + r.width / 2 - w / 2)) + 'px';
}
