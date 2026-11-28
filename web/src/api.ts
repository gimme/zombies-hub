// web/src/api.ts - the types of what the server sends the page as JSON. Each one mirrors a Go type of the server, which
// its comment names. A field that the server leaves out when it is empty is optional here.

/** The server's answer to GET /server (server/web.go). */
export interface ServerInfo {
  /** The UDP port that players join the server's games at. */
  port?: number;
  /** The version of the web client that the server serves. */
  web?: string;
  /** The server is a dev server (docker/server.sh). */
  dev?: boolean;
  /** The server sends game files to browsers. */
  downloads?: boolean;
  /** The address of the launcher's latest release, for the player to install it. */
  launcher?: string;
  /** The address, in that release, of the script that installs the launcher on a Mac. */
  launcherMac?: string;
}

/** The lobby as the player sees it (server/rooms.go: view). */
export interface View {
  you: You;
  /** The games the server hosts, which the code calls titles. A room plays one of them. */
  titles: Title[];
  /** All the Zombies maps, whether the server has them or not. */
  maps: MapInfo[];
  /** The mods a room's games can have. */
  mods: Mod[];
  /** The pictures a player may pick. An event stream sends them in its first event, not in every event. */
  pictures?: Picture[];
  /** The map of a new room, unless its maker picks one. */
  defaultMap: string;
  rooms: RoomItem[];
  /** The player's room, or null. */
  room: Room | null;
  /** The most players a room may take. */
  size: number;
  /** The players a room's game needs. */
  minPlayers: number;
  /** All of the server's games are running, so no one may be ready. */
  full: boolean;
}

/** A game the server hosts (server/rooms.go: titleView). */
export interface Title {
  /** t5 (Black Ops), t6 (Black Ops II) or t7 (Black Ops III). */
  id: string;
  /** Its name on the page: Black Ops. */
  name: string;
  /** The server sends its game files to a player's launcher, for a PC that has none, or lacks a map. */
  downloads?: boolean;
  /**
   * The size of what the launcher then downloads for every map of it, once the server has listed its files: all of
   * Black Ops II, and of Black Ops III what every map needs, where each map has more of its own (MapInfo.bytes).
   */
  bytes?: number;
}

/** A Zombies map (server/rooms.go: mapView). */
export interface MapInfo {
  name: string;
  title: string;
  /** The title it belongs to: t5 (Black Ops), t6 (Black Ops II) or t7 (Black Ops III). */
  of?: string;
  /** The game files have it, so a room may play it. */
  have?: boolean;
  /** The server has the list of files it reads, so a browser may play it. */
  list?: boolean;
  /** The size of what a launcher downloads for the map alone, where the server sends its game a map at a time. */
  bytes?: number;
  desc?: string;
  /**
   * The server has no pictures of it: a Black Ops III map, on a server without that game's image packs
   * (server/mapart.go: noMapArt).
   */
  noPictures?: boolean;
  /** Where a Black Ops II map is (Green Run), which its card in the game's lobby has over its name (maps.ts: mapShot). */
  place?: string;
  /** The mode that a Black Ops II map has alone, which its card has after its name (Survival, Grief, Turned). */
  mode?: string;
  /**
   * How its players play against each other: grief (in two teams) or turned (each for themselves). Missing on a co-op
   * map, where they play together against the zombies. A room shows the maps of its versus alone (titles.ts).
   */
  versus?: string;
}

/** A picture a player may pick (server/rooms.go: pictureView). The page loads GET /pictures/<id>.png. */
export interface Picture {
  id: string;
  name: string;
  group: string;
}

/** A mod a room's games can have (server/mods.go: mod). */
export interface Mod {
  id: string;
  name: string;
  desc: string;
  options?: { value: number }[];
  /** The ids of the titles whose games can have it. */
  titles: string[];
}

/** The player who receives the view (server/rooms.go: youView). */
export interface You {
  id: string;
  name: string;
  avatar?: string;
  picture?: string;
  discordAvatar?: string;
  windows?: boolean;
  ready?: boolean;
  launcher?: boolean;
  launcherReady?: boolean;
  /** The game runs that their launcher started: their room then offers no launch of it (room.ts: showLaunch). */
  launcherGame?: boolean;
  launcherFailed?: string;
  launcherDoing?: string;
  /**
   * The server refused their launcher as too old for it, and takes only a newer one; or their launcher said that the
   * script that runs it on their Mac is too old for it.
   */
  launcherOld?: boolean;
  /**
   * Their launcher downloads the game files, and has this much of them, from 0 to 1. A room shows it on their member
   * too (Member.downloading); Play local shows it from here.
   */
  launcherDownloading?: boolean;
  launcherGot?: number;
  /**
   * The game that their launcher is at, by its title's id, while it runs; and, once the launcher has found the game's
   * folder on their PC (launcherFolder), the maps of that game that the folder has. launcherOwn: the folder is their
   * own copy of the game, into which the launcher downloads nothing. Play local shows them (windows.ts: onPC).
   */
  launcherOf?: string;
  launcherFolder?: boolean;
  launcherMaps?: string[];
  launcherOwn?: boolean;
}

/** A room in the list of rooms (server/rooms.go: roomItem). A room listed as private has no code. */
export interface RoomItem {
  code?: string;
  private?: boolean;
  map: string;
  /** The title of its map, as Room has it. */
  of: string;
  mods?: string[];
  host: string;
  players: number;
  size: number;
  state: string;
}

/** The player's room (server/rooms.go: roomView). */
export interface Room {
  code: string;
  map: string;
  /** The title of its map. A page can't play a t6 or a t7 room's game (titles.ts: ownPC). */
  of: string;
  private: boolean;
  size: number;
  /** The largest size its host may set on its map: more on a map of Grief. */
  maxSize: number;
  mods?: string[];
  /** open (no game), loading (players load in), playing, or ending (the game quits). */
  state: string;
  members: Member[];
  note?: string;
  host: boolean;
  seat?: boolean;
  join?: boolean;
  left?: boolean;
  startsIn?: number;
  canEnd?: boolean;
  result?: GameResult;
  t6?: { up: boolean; password?: string; name: string };
  up?: boolean;
  password?: string;
}

/** A player in a room (server/rooms.go: memberView). */
export interface Member {
  id: string;
  name: string;
  avatar?: string;
  /** Their place in the room, from 0. */
  place: number;
  team?: number;
  host?: boolean;
  inGame?: boolean;
  away?: boolean;
  windows?: boolean;
  ready?: boolean;
  gamepad?: boolean;
  /** They lack the room's map: their page its game files, or their own copy of Black Ops II the map's pack. */
  missing?: boolean;
  downloading?: boolean;
  got?: number;
  mark?: string;
}

/** How a game went (server/rooms.go: gameResult). */
export interface GameResult {
  id: string;
  map: string;
  mods?: string[];
  round?: number;
  seconds: number;
  players: PlayerStats[];
}

/** A player's line of a game's scoreboard (server/rooms.go: playerStats). */
export interface PlayerStats {
  id: string;
  name: string;
  score: number;
  cash: number;
  team?: number;
  /** The game has a winner, which it named at game over, and they won it: on a map of Grief or Turned. */
  won?: boolean;
  kills: number;
  headshots: number;
  downs: number;
  revives: number;
  /** The doors they bought. It is missing when the game reported no number. */
  doors?: number;
  avatar?: string;
}

/** The server's answer to POST /lobby/hello (server/rooms.go: hello). */
export interface Hello {
  token?: string;
  id?: string;
  name?: string;
  key?: string;
  discord?: boolean;
  /** The URL where the player has to sign in first. */
  signIn?: string;
  member?: boolean;
}

/** A player's record on a map with a party of one size (server/leaderboards.go: record). */
export interface BoardRecord {
  map: string;
  party: number;
  round?: number;
  seconds?: number;
  at: string;
  /** The ID of the report of the game that set it, in Leaderboards.games. */
  game: string;
}

/** A player's row on a map's board (server/leaderboards.go: boardRow). */
export interface BoardRow {
  id: string;
  name: string;
  avatar?: string;
  round?: number;
  seconds?: number;
  party: number;
  game: string;
}

/** A map's boards, one for each size of party (server/leaderboards.go: mapBoards). */
export interface MapBoards {
  map: string;
  duo: BoardRow[];
  squad: BoardRow[];
  bigSquad: BoardRow[];
}

/**
 * A player's row on a board of all the maps: the sum of their bests, and each best
 * (server/leaderboards.go: allRow).
 */
export interface AllRow {
  id: string;
  name: string;
  avatar?: string;
  round?: number;
  seconds?: number;
  bests: BoardRecord[];
}

/** A board of all the maps (server/leaderboards.go: allBoard). mine is the row of the player who asks. */
export interface AllBoard {
  rows: AllRow[];
  mine: AllRow;
}

/** The boards of one kind, the rounds or the easter eggs, of one title (server/leaderboards.go: kindBoards). */
export interface KindBoards {
  /** A map's own, for each map of the kind that the server has, in the page's order. */
  maps: MapBoards[];
  /** The board of all of those maps. */
  all: AllBoard;
}

/** The boards of one title, with its id and name as View.titles has them (server/leaderboards.go: titleBoards). */
export interface TitleBoards {
  id: string;
  name: string;
  rounds: KindBoards;
  /** Its maps that have an easter egg, where the server has any. A Black Ops II map has one in its classic mode. */
  eggs?: KindBoards;
}

/** The server's answer to GET /lobby/leaderboards (server/leaderboards.go: leaderboards). */
export interface Leaderboards {
  /** The titles the server hosts, in View.titles' order, each with its boards. */
  titles: TitleBoards[];
  /** The reports of the games that the boards name, by ID. */
  games: Record<string, GameResult>;
}
