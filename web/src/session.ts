// web/src/session.ts - the page's session with the lobby: saying who the player is, the lobby's events, signing in,
// and going offline.
//
// hello tells the lobby who the player is: by the token that the lobby gave this browser before, or with none, and
// the lobby then gives a new one. After it, the page reads the lobby's events (listen). Each event is a view of the
// lobby, which the page shows (show.ts).
//
// A server may have its players sign in with Discord (server/discord.go). hello's answer then says so, and the page
// asks the player to sign in, in place of the lobby (showSignIn).
//
// When the server can't be reached, the page goes offline (goOffline), and says hello again every 5 s.
import type { Hello, MapInfo } from './api';
import { $ } from './dom';
import { started } from './engine';
import { downloadAgain, readyNow, reportAgain, whenMap } from './files';
import { act, auth, me, offline, page, setMe, setOffline, view } from './lobby';
import { musicShown } from './music';
import { tellGamepad } from './padnav';
import { params } from './params';
import { openPicker, showName, showProfile } from './profile';
import { mayLeaveGame } from './question';
import { tellHand } from './room';
import { setStatus } from './screen';
import { loadedServer } from './server';
import { show } from './show';
import { store } from './storage';

// Why the player's sign-in failed, when the server sent them back to the page with ?signin= (server/discord.go). The
// sign-in shows it. The parameter is then taken off the URL, which keeps the rest of what it said.
const signinWhy = ({
  cancelled: 'Sign-in cancelled.',
  member: "You're not in the Discord yet. Once you've joined, sign in again.",
  role: "You don't have the role needed to play yet. Once you have it, sign in again.",
  failed: "Couldn't sign in with Discord. Try again.",
} as Record<string, string>)[params.get('signin') || ''] || '';
if (params.has('signin')) {
  const url = new URL(location.href);
  url.searchParams.delete('signin');
  history.replaceState(null, '', url);
}

let events: AbortController | null = null; // stops the event stream that the page reads (listen)

/** Tells the lobby who the player is, and then reads the lobby's events. */
export async function hello(): Promise<void> {
  let answer: Hello;
  try {
    const rsp = await fetch('/lobby/hello', {
      method: 'POST', headers: { 'Content-Type': 'application/json', ...auth() },
      body: JSON.stringify({ name: me.name }),
    });
    answer = await rsp.json();
  } catch (e) {
    if (!offline) console.log('lobby: the server did not answer (' + (e as Error).message + ')');
    goOffline();
    setTimeout(hello, 5000);
    return;
  }
  if (offline) {
    setOffline(false);
    if (!started) setStatus(''); // while a game has the page, the status line is the engine's
    downloadAgain();
  }
  if (answer.signIn) {
    showSignIn(answer.signIn, !!answer.member);
    return;
  }
  document.body.classList.remove('signin');
  $('signin').classList.add('hidden');
  $('lobby').classList.remove('hidden');
  musicShown(true);
  // The answer has no picture. The player's picture stays as the page had it until the lobby's next view.
  setMe({ token: answer.token, name: answer.name || '', id: answer.id, key: answer.key, discord: !!answer.discord,
    avatar: me.avatar });
  reportAgain(); // the lobby may have started again
  await loadedServer; // the profile's line for the Windows client has the server's port (windows.ts: playLine)
  store.set('zombies-token', me.token || '');
  store.set('zombies-name', me.name);
  showName(me.name, $<HTMLInputElement>('name').disabled);
  showProfile();
  listen();
  whatTheUrlAsks();
}

page.hello = hello;

// Asks the player to sign in with Discord, in place of the lobby: they are not signed in, or no longer are. With
// member, the server takes only members of its Discord server. The button leaves the page for Discord's, which sends
// the player back to the URL that the page has now, such as a room's link.
function showSignIn(url: string, member: boolean): void {
  const stream = events;
  events = null; // the stream that is stopped then starts no other (listen)
  if (stream) stream.abort();
  setMe({ name: me.name });
  store.set('zombies-token', '');
  document.body.classList.add('signin');
  musicShown(false);
  $('lobby').classList.add('hidden');
  $('me').classList.add('hidden');
  openPicker(false);
  $('signin').classList.remove('hidden');
  $('signintext').textContent = member ? 'You need to be a member of the Discord to play.'
    : 'Sign in with Discord to play.';
  $('signinwhy').textContent = signinWhy;
  $('signinbutton').onclick = () => {
    location.href = url + '?then=' + encodeURIComponent(location.pathname + location.search);
  };
  if (!started) setStatus('');
  console.log('lobby: sign in with Discord' + (signinWhy ? ' (' + signinWhy + ')' : ''));
}

// Shows the lobby of a page that can't reach the server. The player can still play a map alone that the browser's
// store has, because the service worker kept the page and the engine (web/public/sw.js). The view has the maps that
// the lobby last sent, which the browser's storage kept (show.ts), and no rooms: the list of rooms says that there is
// no connection. No one signs in offline.
function goOffline(): void {
  let kept: { maps: MapInfo[]; defaultMap?: string; size?: number } | null = null;
  try {
    kept = JSON.parse(store.get('zombies-maps') || 'null');
  } catch {
    kept = null;
  }
  setOffline(true);
  if (started) return; // while a game has the page, the status line is the engine's
  const had = kept || { maps: [] };
  document.body.classList.remove('signin');
  $('signin').classList.add('hidden');
  $('lobby').classList.remove('hidden');
  musicShown(true);
  console.log('lobby: offline, with the maps kept: ' + had.maps.map((m) => m.name).join(' '));
  if (!me.name) me.name = 'Player';
  // The player's picture is the one that the service worker kept, if it is one of the game's (cache.ts). There are
  // no pictures to pick from, and no games to tell apart: the maps kept are Black Ops' alone.
  show({ you: { id: me.id || '', name: me.name, avatar: store.get('zombies-avatar') || '' }, titles: [],
    maps: had.maps, mods: [], defaultMap: had.defaultMap || '', pictures: [], rooms: [], size: had.size || 4,
    minPlayers: 1, full: false, room: null });
}

// Reads the lobby's events (GET /lobby/events) and shows each view as it comes. It is a fetch, because an EventSource
// sends no Authorization header. When the stream ends, the page listens again after a second, as an EventSource does.
// It says hello again instead when the lobby does not know the token, because its session ended, or when the stream
// was lost or refused, as by a proxy's 502. hello then finds out whether the server can be reached.
async function listen(): Promise<void> {
  if (events) events.abort();
  const stream = events = new AbortController();
  let unknown = false, lost = false;
  try {
    const rsp = await fetch('/lobby/events', { headers: auth(), signal: stream.signal, cache: 'no-store' });
    unknown = rsp.status === 401;
    lost = !rsp.ok && !unknown;
    if (rsp.ok) {
      tellGamepad(true); // the lobby knows of the page's gamepad only while it sends the page events
      tellHand(); // and forgets that the player joins by hand when it starts again
    }
    const reader = rsp.ok && rsp.body ? rsp.body.pipeThrough(new TextDecoderStream()).getReader() : null;
    let text = '';
    while (reader) {
      const read = await reader.read();
      if (read.done) break;
      text += read.value;
      // An event ends at a blank line, and its view is on its "data: " lines. The lines that start with ":" only
      // keep a proxy from closing the stream.
      let end;
      while ((end = text.indexOf('\n\n')) >= 0) {
        const data = text.slice(0, end).split('\n').filter((l) => l.startsWith('data: ')).map((l) => l.slice(6));
        text = text.slice(end + 2);
        if (data.length) show(JSON.parse(data.join('\n')));
      }
    }
  } catch {
    lost = true; // the connection was lost, or another stream stopped this one
  }
  if (stream === events) setTimeout(unknown || lost ? hello : listen, 1000);
}

// Does what the URL asks for, once: ?room= joins that room, and ?autostart=1 makes a private room and says that the
// player is ready. A link to another room than the player's takes them out of their room's game, which asks first.
let urlDone = false;
async function whatTheUrlAsks(): Promise<void> {
  if (urlDone) return;
  urlDone = true;
  const room = (params.get('room') || '').toUpperCase().replace(/[^A-Z]/g, '');
  const wait = () => new Promise((r) => setTimeout(r, 100));
  if (room) {
    for (let waited = 0; !view && waited < 100; waited++) await wait();
    if (view && view.room && view.room.code !== room && !(await mayLeaveGame())) return;
    await act('join', { room });
  } else if (params.get('autostart') === '1' && await act('create', { private: true })) {
    // The room's map is downloaded first, as the room's button would.
    for (let waited = 0; !(view && view.room) && waited < 100; waited++) await wait();
    if (view && view.room && await whenMap(view.room.map)) await readyNow(true);
  }
}
