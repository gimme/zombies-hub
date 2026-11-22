// web/src/rooms.ts - the list of rooms, on the lobby's front.
//
// A room takes players while it has no game running and has a seat free. Its host sets how many seats it has. A room
// that is private to the player comes without its code (server/rooms.go: roomItem), and is joined by its code alone:
// a private room, or one that was just made. On a server that hosts two games, a room's line says its game first.
import type { View } from './api';
import { $, el } from './dom';
import { act, offline } from './lobby';
import { mapInfo, mapPicture } from './maps';
import { modNames } from './mods';
import { titleName } from './titles';

/** Draws the list of rooms from a view of the lobby. */
export function showRooms(v: View): void {
  const list = $('rooms');
  list.textContent = '';
  for (const r of v.rooms) {
    const li = el('li', 'panel');
    const text = el('div', 'grow');
    text.append(el('div', 'title', mapInfo(r.map).title));
    const game = v.titles.length > 1 ? titleName(v.titles, r.of) : '';
    const sub = el('div', 'sub', (game ? game + ' · ' : '') + r.host + '\'s room');
    if (!r.private) sub.append(' · ', el('span', 'mono', r.code));
    // The room's games have mods. The tag says so before a player joins, and its tooltip names them.
    if (r.mods && r.mods.length) {
      const tag = el('span', 'tag');
      tag.append(el('span', 't', 'Mods'));
      tag.title = modNames(v.mods, r.mods).join(', ');
      sub.append(' ', tag);
    }
    text.append(sub);
    const pips = el('div', 'pips');
    pips.setAttribute('aria-label', r.players + ' of ' + r.size + ' players');
    const bars = el('div', 'row');
    bars.style.gap = '4px';
    bars.setAttribute('aria-hidden', 'true');
    for (let i = 0; i < r.size; i++) bars.append(el('span', i < r.players ? 'taken' : ''));
    pips.append(bars, el('b', '', r.players + '/' + r.size));
    li.append(mapPicture(el('img', 'thumb'), r.map), text, pips);
    if (r.private) {
      li.append(el('div', 'private', 'Private'));
    } else {
      const join = el('button', '', r.state !== 'open' ? 'In game' : r.players >= r.size ? 'Full' : 'Join');
      join.type = 'button';
      join.disabled = r.state !== 'open' || r.players >= r.size;
      join.classList.toggle('primary', !join.disabled);
      join.onclick = () => act('join', { room: r.code });
      li.append(join);
    }
    list.append(li);
  }
  if (!v.rooms.length) list.append(el('li', 'panel none', offline ? 'No connection to the server.' : 'No rooms yet.'));
}
