// web/src/link.ts - the links that open the launcher on the player's PC, as the launcher reads them
// (internal/launcherapi, launcher/link.go: parseLink). A button of the page follows one (windows.ts: openLauncher).
// Nothing here reads the page, so its tests run it as it is (link.test.ts).

/** What a link to the launcher asks for. */
export interface LauncherAsk {
  /** The page's host, with its port where it has one, and whether the page is on HTTPS. */
  host: string;
  tls: boolean;
  /** The player's key on the server, or '' from a page that can't reach its server. */
  key: string;
  /**
   * The launcher starts a game for the player alone, under their name (name). Otherwise it gets the PC ready, and
   * starts nothing until a room's game does.
   */
  solo: boolean;
  name?: string;
  /**
   * The game, by its title's id, and a map of it. A room's button names neither: the launcher goes by the player's
   * room. Play local names the game, and the map that the launcher is to download, where it asks for one.
   */
  of?: string;
  map?: string;
}

/**
 * Returns the link. Black Ops II is named by no word: a launcher from before it started another game knows none. A
 * link to play alone has the key only where the page has one, and the launcher then calls no server. The name is
 * encoded down to letters, digits and %: the script that runs the launcher on a Mac takes no other characters of a
 * link (launcher/mac.sh: cmd_link).
 */
export function launcherLink(a: LauncherAsk): string {
  const name = encodeURIComponent(a.name || '').replace(/[!'()*~]/g, (c) => '%' + c.charCodeAt(0).toString(16).toUpperCase());
  const game = (a.of && a.of !== 't6' ? '&of=' + encodeURIComponent(a.of) : '') +
    (a.map ? '&map=' + encodeURIComponent(a.map) : '');
  const asks = a.solo ? 'solo=1&name=' + name + game + (a.key ? '&key=' + a.key : '') : 'key=' + a.key + game;
  return 'zombies-launcher://' + a.host + '/play?' + asks + (a.tls ? '' : '&tls=0');
}
