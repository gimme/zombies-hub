// web/src/server.ts - the server that served the page, as it describes itself (server/web.go: GET /server).
import type { ServerInfo } from './api';

/** Asks the server about itself. The answer is {} when no server answers. */
export async function fetchServer(): Promise<ServerInfo> {
  try {
    return await (await fetch('/server', { cache: 'no-store' })).json();
  } catch {
    return {};
  }
}

/** The server as it was when the page loaded. Its web client then is the one this page runs. */
export const loadedServer = fetchServer();

/** What loadedServer answered, or {} until it has. */
export let served: ServerInfo = {};
loadedServer.then((server) => {
  served = server;
});

/** Whether the server serves another web client now than the one this page loaded. */
export async function serverUpdated(): Promise<boolean> {
  const [then, now] = await Promise.all([loadedServer, fetchServer()]);
  return !!(then.web && now.web && then.web !== now.web);
}

/**
 * Loads the page again, as the server serves it now. With play, the player had started a map in the browser alone,
 * and the new page starts it at once. A lobby's page needs no such flag: the room's game is still the player's to
 * join, and the new page joins it.
 */
export function reload(play: boolean): void {
  try {
    if (play) sessionStorage.setItem('zombies-start', '1');
  } catch {
    // Without storage, the player clicks to play again.
  }
  location.reload();
}
