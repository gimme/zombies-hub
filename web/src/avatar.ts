// web/src/avatar.ts - a player's picture, next to their name.
//
// The picture is one of the game's, which the lobby gives each player and they change in their menu
// (server/pictures.go), or their avatar on Discord, on a server where players sign in with Discord and they have one
// (server/discord.go). It is drawn over the initial of their name, which shows when the server has no pictures or
// theirs does not load. The page is cross-origin isolated, which Discord's CDN allows (CORS).
import { el } from './dom';

export const initial = (name: string): string => (String(name || '').trim().charAt(0) || '?').toUpperCase();

/**
 * A new element with a player's picture. In a room, place is the player's place in it (server/rooms.go: memberView),
 * which the colour of their ring goes by (.avatar[data-place]).
 */
export function avatar(name: string, url?: string, mine = false, place?: number): HTMLSpanElement {
  const a = el('span', 'avatar' + (mine ? ' mine' : ''));
  setAvatar(a, name, url, place);
  return a;
}

/** Shows a player's picture in an element that avatar made. The picture is loaded again only when it changes. */
export function setAvatar(a: HTMLElement, name: string, url?: string, place?: number): void {
  if (place === undefined) delete a.dataset.place;
  else a.dataset.place = String(place % 4);
  const was = name + '\n' + (url || '');
  if (a.dataset.was === was) return;
  a.dataset.was = was;
  a.textContent = initial(name);
  a.classList.toggle('pic', !!url);
  if (!url) return;
  const img = el('img');
  img.crossOrigin = 'anonymous';
  img.alt = '';
  img.onerror = () => {
    if (img.parentNode !== a) return; // an earlier picture of this element, which has another one since
    img.remove();
    a.classList.remove('pic');
  };
  img.src = url;
  a.append(img);
}
