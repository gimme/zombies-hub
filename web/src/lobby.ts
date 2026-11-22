// web/src/lobby.ts - the lobby, as the page knows it: who the player is to it, the lobby as the server last described
// it to them, and the calls that the page makes to it as the player.
//
// The parts of the page that talk to the server set the state (session.ts: hello sets the player; show.ts: show sets
// the view). The other parts read it.
import type { View } from './api';
import { showRefusal } from './notice';
import { params } from './params';
import { setStatus } from './screen';
import { store } from './storage';

/** The player. A page that has not said hello to the lobby yet has only what the browser kept: a token and a name. */
export interface Me {
  /** The lobby's token for this browser, which each call to the lobby sends (auth). */
  token?: string;
  name: string;
  id?: string;
  /** The key that the Windows client and the launcher join the player's room with. */
  key?: string;
  /** The player signed in with Discord, where they change their name. */
  discord?: boolean;
  avatar?: string;
}

export let me: Me = { token: store.get('zombies-token'), name: params.get('name') || store.get('zombies-name') };

export function setMe(next: Me): void {
  me = next;
}

/** The lobby as the player sees it, or null before the page has a view. */
export let view: View | null = null;

export function setView(next: View | null): void {
  view = next;
}

/**
 * Whether the page is offline: the server can't be reached (session.ts: goOffline). The page then shows the last
 * maps that the lobby sent, with no rooms, and the player can play a map that the browser has.
 */
export let offline = false;

export function setOffline(on: boolean): void {
  offline = on;
}

/** The header that tells the lobby who the player is, for each call to it. */
export const auth = (): Record<string, string> => (me.token ? { Authorization: 'Bearer ' + me.token } : {});

/**
 * What a part asks of the parts above it. The parts are layered, and a part imports only from the parts below it.
 * These are the two things that a part needs from above.
 */
export const page = {
  /** Draws the lobby again from the view that the page has. show.ts sets it. */
  redraw: (): void => {},
  /** Says hello to the lobby again: as the player with a new name, or because the lobby no longer knows the token. */
  hello: (): void => {},
};

/** Makes a call to the lobby as the player: POST /lobby/<action>. Returns the error that the lobby answers, or ''. */
export async function ask(action: string, body: object = {}): Promise<string> {
  try {
    const rsp = await fetch('/lobby/' + action, {
      method: 'POST', headers: { 'Content-Type': 'application/json', ...auth() }, keepalive: true,
      body: JSON.stringify(body),
    });
    const answer = await rsp.json();
    if (rsp.status === 401) page.hello();
    return answer.error || '';
  } catch (e) {
    console.log('lobby: no answer to ' + action + ' (' + (e as Error).message + ')');
    return 'No connection to the server.';
  }
}

/**
 * Does something in the lobby that the player asked for. Returns whether the lobby took it. A refusal shows over the
 * page (notice.ts). When the lobby takes the action, the last refusal and the status line's text go.
 */
export async function act(action: string, body: object = {}): Promise<boolean> {
  const error = await ask(action, body);
  showRefusal(error);
  if (!error) setStatus('');
  return !error;
}
