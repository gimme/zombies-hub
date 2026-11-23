// web/src/show.ts - draws the lobby from a view, and follows the page's URL.
//
// The server describes the lobby to the player as a view (api.ts: View), and sends a new one each time something
// changes (session.ts: listen). show takes a view and draws every part of the page from it: the player's name and
// picture, the list of rooms, the page to play offline, the room, how its last game went, and the leaderboards. It
// also does what the view asks of the page: join the room's game, or leave it.
//
// The URL says which room the player is in (?room=), for a reload or a link, and whether the page to play offline
// (?offline=1) or the leaderboards (?boards=1) show. Going into each of them is a step in the browser's history, which
// Back goes back from (the popstate listener below).
import type { View } from './api';
import { setBackdrop } from './backdrop';
import {
  boardReport, boardsFollowUrl, boardsOpen, loadBoards, openBoards,
} from './boards';
import { cacheArt, keepAvatar } from './cache';
import { $ } from './dom';
import { canRun, player, started } from './engine';
import { knowMap, reportFiles, stopDownload } from './files';
import {
  ending, firstLook, gameEnded, joinUnlessElsewhere, leaveSoon, lookAgain, otherTab, showMapAlone, solo,
} from './game';
import { act, me, offline, page, setView, view } from './lobby';
import { soloMapName, t5Maps } from './maps';
import { musicOutside } from './music';
import { lobbyMode } from './params';
import { showName, showPictures } from './profile';
import { leaveQuestionGone, mayLeaveGame } from './question';
import { resultToShow, showResult } from './result';
import { showRoom, showRoomButton, showRoomMaps, showT6 } from './room';
import { showRooms } from './rooms';
import { loadedServer } from './server';
import { closeSolo, openSolo, showSolo, soloFollowUrl, soloOpen } from './solo';
import { menuSound, roomSounds } from './sounds';
import { store } from './storage';
import { onWindows } from './windows';

let shownRoom = ''; // the code of the room that the page last showed
let roomPushed = false; // going into the room added a step to the browser's history, which leaving it goes back from
const urlRoom = (url: URL): string => (url.searchParams.get('room') || '').toUpperCase();

/** Shows a view of the lobby: keeps it as the page's view, and draws the page from it. */
export function show(v: View): void {
  setView(v);
  leaveQuestionGone(v);
  const room = v.room;
  const opened = firstLook();
  const windows = onWindows();
  // The page keeps the maps for when it is offline (session.ts: goOffline), and the service worker caches their
  // pictures and lists.
  if (!offline) {
    store.set('zombies-maps', JSON.stringify({ maps: t5Maps(v.maps), defaultMap: v.defaultMap, size: v.size }));
    cacheArt(t5Maps(v.maps));
  }
  // The player's game runs outside the page, in their own Black Ops II or in the Windows client, until the room's game
  // is over. The lobby's music waits for it.
  musicOutside(!!room && room.state !== 'open' && !!room.seat && !room.left && (room.of === 't6' || windows));
  me.name = v.you.name;
  me.avatar = v.you.avatar || '';
  if (!offline) keepAvatar(me.avatar);
  // The player can't change their name while their room's game runs, because the game knows them by it. Offline, no
  // lobby takes a new name.
  showName(v.you.name, offline || (!!room && room.state !== 'open'));
  showPictures(v);
  // In a room, the page to play offline closes. Another tab of the player's may have joined the room.
  if (room) closeSolo();
  roomSounds(room);
  if ((room ? room.code : '') !== shownRoom) {
    const from = shownRoom;
    shownRoom = room ? room.code : '';
    console.log(room ? 'lobby: in room ' + room.code : 'lobby: in no room');
    // The URL says the room, and keeps the rest of what it said. Going into a room from the lobby is a step that the
    // browser's Back goes back from, which leaves the room (popstate). Leaving the room any other way takes that step
    // back.
    const url = new URL(location.href);
    const said = urlRoom(url);
    if (room) url.searchParams.set('room', room.code);
    else url.searchParams.delete('room');
    if (room) url.searchParams.delete('offline');
    url.searchParams.delete('autostart');
    if (room && !from && said !== room.code) {
      history.pushState(null, '', url);
      roomPushed = true;
    } else if (!room && roomPushed && !boardsOpen) {
      roomPushed = false;
      history.back();
    } else {
      roomPushed = roomPushed && !!room;
      history.replaceState(null, '', url);
    }
    if (room && !windows && room.of !== 't6') prefetchEngine();
  }
  const result = resultToShow(room);
  $('outside').classList.toggle('hidden', !!room || soloOpen || boardsOpen);
  $('solo').classList.toggle('hidden', !!room || !soloOpen || boardsOpen);
  $('inside').classList.toggle('hidden', !room || !!result || boardsOpen);
  const report = boardsOpen ? boardReport : null;
  $('results').classList.toggle('hidden', boardsOpen ? !report : !result);
  $('boards').classList.toggle('hidden', !boardsOpen || !!report);
  $('boardsopen').classList.toggle('hidden', offline);
  $('boardsopen').setAttribute('aria-pressed', String(boardsOpen));
  $('resultsback').textContent = report ? 'Back to the leaderboards' : 'Back to the room';
  if (boardsOpen) loadBoards();
  if (report) showResult(report);
  setBackdrop(report ? report.map : result ? result.map : room ? room.map : soloMapName(v));
  // The store says what it has of the map of the view's button. A download of another map stops.
  const buttonMap = windows || (room && room.of === 't6') ? '' : room ? room.map : soloMapName(v);
  if (player.store.downloading && player.store.downloading !== buttonMap) stopDownload();
  knowMap(buttonMap);
  if (!room) {
    showRooms(v);
    showSolo(v);
    for (const id of ['make', 'joincode', 'code']) $<HTMLButtonElement | HTMLInputElement>(id).disabled = offline;
    // The room is gone while the engine still loads or plays its game, because the lobby started again. The page
    // leaves the game.
    if (started && !solo && !ending) leaveSoon();
    return;
  }
  if (result && !boardsOpen) showResult(result);
  showRoom(v, room);
  reportFiles();

  // The page joins the room's game when the lobby says it is to be in it. It does not while another tab of the
  // player's plays the game, or played it before this page opened on it, or while the player plays on Windows.
  if (room.join && !started && !windows && !otherTab) joinUnlessElsewhere(opened && room.state === 'playing');
  // The page plays a map alone, and another tab of the player's joined the room.
  if (solo) return;
  // The player's Windows client asks for their seat while this page has it. The page leaves the game to it.
  if (started && v.you.windows && !ending) gameEnded('windows', 'Your game moved to the Windows client.', true);
  // The game ended while the engine still loads or joins, and the page goes back to the room. Not while the game
  // over still runs: the room is ending, with how the game went (server/rooms.go), for the 18 s of the end
  // scoreboard, which the engine leaves by itself.
  if (started && !room.join && !ending && !(room.state === 'ending' && room.result)) leaveSoon();
}

page.redraw = () => {
  if (view) show(view);
};

// Fetches the engine as the player enters a room, while they wait for the others. The browser then has it in its
// cache when their game starts. The server sends it compressed, about 3.4 MB, and a cached copy is only checked for
// changes.
function prefetchEngine(): void {
  if (!started && canRun) player.prefetch();
}

/**
 * Draws again what depends on the store of game files: the view's button and its grid of maps. The lobby then hears
 * what the page has of its room's map. On a page that plays a map alone, the status line says it.
 */
export function showFiles(): void {
  if (!lobbyMode) return showMapAlone();
  if (!view) return;
  if (view.room) {
    showRoomButton(view, view.room);
    showRoomMaps(view, view.room);
  } else showSolo(view);
  reportFiles();
}

// What the server says of itself changes what the views say: whether it sends game files, and where the launcher is.
loadedServer.then(() => {
  showFiles();
  if (view && view.room) showT6(view, view.room);
});

/**
 * Shows the lobby as the page showed it before it reloaded at its game's end (game.ts: gameEnded), from the first
 * paint, until the lobby's first view says how it is now. Without this, the front page showed for a moment before
 * the room. The game is no longer the page's to join. The lobby's first view is still the page's first look at it.
 * A view that a page of an earlier build kept may lack what the view has had since: the games the server hosts.
 */
export function showKept(v: View): void {
  v.titles ||= [];
  me.id = v.you.id;
  if (v.room) v.room.join = false;
  show(v);
  lookAgain();
  $('backdropimg').classList.add('shown'); // the map's picture shows at once, without fading in (backdrop.ts)
}

window.addEventListener('popstate', () => {
  const url = new URL(location.href);
  const was = { solo: soloOpen, boards: boardsOpen };
  soloFollowUrl(url);
  boardsFollowUrl(url);
  // The view that the step closed or opened plays its sound: the leaderboards, which are over whatever shows, or the
  // page to play offline, which a room does not show.
  if (boardsOpen !== was.boards) menuSound(boardsOpen);
  else if (soloOpen !== was.solo && !(view && view.room)) menuSound(soloOpen);
  // Back from a room leaves the room, as Leave room does, and asks first when the player has a seat in its game. A
  // player whose page plays the game stays, because the game has its own menu to leave by, and so does a player who
  // answers Stay: the room's step is added again. Forward to a room that the player has left is the lobby's step.
  const room = view && view.room;
  if (room && urlRoom(url) !== room.code) {
    roomPushed = false;
    const stay = () => {
      if (!view || !view.room || view.room.code !== room.code || urlRoom(new URL(location.href)) === room.code) return;
      const back = new URL(location.href);
      back.searchParams.set('room', room.code);
      history.pushState(null, '', back);
      roomPushed = true;
    };
    if (started) stay();
    else {
      mayLeaveGame().then((yes) => {
        if (yes) act('leave');
        else stay();
      });
    }
  } else if (!room && urlRoom(url)) {
    url.searchParams.delete('room');
    history.replaceState(null, '', url);
  }
  page.redraw();
});

/**
 * Goes to the lobby's front, at a click on the page's title: out of the leaderboards, out of the page to play
 * offline, or out of the room, as Leave room does. A click with Ctrl or another modifier opens the front in a new
 * tab, as a link does.
 */
export function openHome(e: MouseEvent): void {
  if (e.ctrlKey || e.metaKey || e.shiftKey || e.altKey) return;
  e.preventDefault();
  if (boardsOpen) return openBoards(false);
  if (!(view && view.room)) return openSolo(false);
  mayLeaveGame().then((yes) => yes && act('leave'));
}
