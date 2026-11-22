// web/src/params.ts - what the page's URL asks for (main.ts lists the parameters).

export const params = new URLSearchParams(location.search);

/** The map of ?map=, which the page plays alone in the browser instead of showing the lobby. */
export const map = (params.get('map') || 'zombie_theater').replace(/[^\w]/g, '');

/** &dedicated=1: the engine runs as a dedicated server, with no window (testing). */
export const dedicated = params.get('dedicated') === '1';

/** Whether the page shows the lobby. It does unless the URL asks for a map alone or a dedicated server. */
export const lobbyMode = !params.has('map') && !dedicated;
