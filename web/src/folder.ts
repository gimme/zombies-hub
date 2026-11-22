// web/src/folder.ts - the player's game folder: their own copy of the game, which a map's button imports maps from.
//
// A player who has Black Ops can bring their own game files. They choose the game's folder in their profile. A map's
// button then imports what the store lacks of the map from the folder (web/public/store.js: importing), where the
// folder has all of that: each file at its path, with the server's size. Where it does not, the button downloads the
// map, or says that the map is missing when the server sends no game files. A game without the map packs lacks their
// maps, for one.
//
// The store keeps a piece only if it is the server's, by its hash in the map's list. A folder with another version of
// the game has other bytes, so its maps come from the server too, and the line under the button says so. An import
// takes nothing from the server: the base data files that no install has (data/main) come with the engine's module.
//
// Where the server sends no game files, a map's button imports even with no folder chosen: the player chooses one as
// they press it.
//
// The browser's folder picker picks the folder, and the page keeps its handle in IndexedDB for the next visit. On a
// later visit, the browser asks the player again to let the page read the folder, at their first import, unless they
// allowed it for good. Until then the page takes the folder to have every map. A browser without the picker picks the
// folder with a file input, for that visit only.
//
// The store's worker reads the folder, which the page gives it. It says whether the folder holds an install, and with
// each map's state whether the folder has the map.
import { $ } from './dom';
import { type GameFolder, type MapState, player, started } from './engine';
import { folder, folderOk, setFolder, setFolderNote, startDownload } from './files';
import { openProfile, showProfile } from './profile';
import { showFiles } from './show';

/**
 * What the store has of a map changed, or of every map (player.js: onStore). The views that show it are drawn again.
 * When the store says that the folder no longer holds an install, the player has to choose another.
 */
export function storeSaid(_map: string, s: MapState | undefined): void {
  if (s && s.noInstall) {
    setFolderNote('Your game folder no longer holds a Black Ops install.');
    forgetFolder();
    openProfile(true);
    return;
  }
  showFiles();
}

// Makes a request of the IndexedDB store that keeps the folder for the next visit (database zombies, store kept).
// Resolves to the request's result.
function keptDb<T>(write: boolean, request: (kept: IDBObjectStore) => IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    const open = indexedDB.open('zombies', 1);
    open.onupgradeneeded = () => open.result.createObjectStore('kept');
    open.onerror = () => reject(open.error);
    open.onsuccess = () => {
      const db = open.result;
      const tx = db.transaction('kept', write ? 'readwrite' : 'readonly');
      const req = request(tx.objectStore('kept'));
      tx.oncomplete = () => {
        db.close();
        resolve(req.result);
      };
      tx.onerror = tx.onabort = () => {
        db.close();
        reject(tx.error);
      };
    };
  });
}

// Keeps the folder's handle for the next visit, or keeps none.
function keepFolder(handle: FileSystemDirectoryHandle | null): void {
  const kept: Promise<unknown> = handle ? keptDb(true, (kept) => kept.put(handle, 'folder'))
    : keptDb(true, (kept) => kept.delete('folder'));
  kept.catch((e: Error) => console.log('lobby: the game folder is not kept for the next visit: ' + e.message));
}

// The File System Access API's calls that Chromium has and TypeScript's types of the DOM lack.
interface HandlePermissions {
  queryPermission(options: { mode: 'read' }): Promise<PermissionState>;
  requestPermission(options: { mode: 'read' }): Promise<PermissionState>;
}
const permissions = (handle: FileSystemDirectoryHandle) => handle as unknown as HandlePermissions;
const pickers = window as unknown as {
  showDirectoryPicker?: (options: { id: string; mode: 'read' }) => Promise<FileSystemDirectoryHandle>;
};

// Sets the folder that the page imports from, or none with null. With ok, the page may read it, and the store gets
// it. Resolves to whether the folder holds an install (player.js: store.useFolder). The store then says again what it
// has of each map, with whether the folder has the map. An import from the folder before stops.
function useFolder(f: GameFolder | null, ok: boolean): Promise<boolean> {
  setFolder(f, ok);
  const said = player.store.useFolder(folderOk ? f : null);
  showProfile();
  showFiles();
  return said;
}

export function forgetFolder(): void {
  keepFolder(null);
  useFolder(null, false);
}

/** Takes the folder that an earlier visit kept. The page may read it at once if the player allowed that for good. */
export async function loadFolder(): Promise<void> {
  let handle: FileSystemDirectoryHandle | undefined;
  try {
    handle = await keptDb<FileSystemDirectoryHandle | undefined>(false, (kept) => kept.get('folder'));
  } catch {
    return; // no IndexedDB, so none is kept
  }
  if (!handle || folder) return;
  const ok = (await permissions(handle).queryPermission({ mode: 'read' }).catch(() => 'denied')) === 'granted';
  if (!(await useFolder(handle, ok)) && ok) {
    console.log('lobby: ' + handle.name + ' holds no Black Ops install now');
    forgetFolder();
  }
}

// Has the player pick a folder. Resolves to a directory handle, to the files of the file input, or to null for none.
async function pickFolder(): Promise<GameFolder | null> {
  if (pickers.showDirectoryPicker) {
    try {
      return await pickers.showDirectoryPicker({ id: 'zombies-game', mode: 'read' });
    } catch (e) {
      if ((e as Error).name !== 'AbortError') console.log('lobby: no game folder: ' + (e as Error).message);
      return null;
    }
  }
  const input = $<HTMLInputElement>('folderinput');
  input.value = '';
  return new Promise((resolve) => {
    input.onchange = () => {
      const files = Array.from(input.files || []);
      resolve(files.length ? { name: files[0].webkitRelativePath.split('/')[0], files } : null);
    };
    input.oncancel = () => resolve(null);
    input.click();
  });
}

/**
 * Has the player choose their game folder: from the profile, or from a map's button where the page has no folder.
 * The page keeps a folder that holds an install, and then gets the map, if one is given. Of a folder that holds none,
 * the profile says so, and the folder before stays.
 */
export async function chooseFolder(map: string): Promise<void> {
  const picked = await pickFolder();
  if (!picked) return;
  const before = [folder, folderOk] as const;
  if (!(await useFolder(picked, true))) {
    console.log('lobby: ' + picked.name + ' holds no Black Ops install');
    setFolderNote('That folder holds no Black Ops install.');
    useFolder(...before);
    openProfile(true);
    return;
  }
  console.log('lobby: the game folder is ' + picked.name);
  setFolderNote('');
  if ('kind' in picked) keepFolder(picked);
  showProfile();
  if (map) startDownload(map);
}

/** Gets the map into the store, as a map's button does: an import from the player's folder, or a download. */
export function getMap(map: string, gets: string): void {
  if (gets === 'import') importMap(map);
  else startDownload(map);
}

// Imports the map from the folder that the page has, or from one that the player chooses when it has none. On a
// later visit, the player first allows the page to read the folder again.
async function importMap(map: string): Promise<void> {
  if (!map || started) return;
  if (!folder) return chooseFolder(map);
  if (!folderOk) {
    let state: PermissionState = 'denied';
    try {
      if ('kind' in folder) state = await permissions(folder).requestPermission({ mode: 'read' });
    } catch {
      // The browser has no permission to ask for, which counts as denied.
    }
    if (state !== 'granted') {
      console.log('lobby: the player did not let the page read ' + folder.name);
      // The browser does not ask again once the player denied it, so they choose a folder again.
      if (state === 'denied') forgetFolder();
      return;
    }
    if (!(await useFolder(folder, true))) {
      console.log('lobby: ' + folder.name + ' holds no Black Ops install now');
      setFolderNote('Your game folder no longer holds a Black Ops install.');
      forgetFolder();
      openProfile(true);
      return;
    }
  }
  startDownload(map);
}
