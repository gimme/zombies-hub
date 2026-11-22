// web/src/mods.ts - the mods of a room's games (server/mods.go).
//
// A room names the mods it has on, by ID. A mod with options is named as id=value. While the room does not name such
// a mod, the mod is at its first option, which is the game's own value: it is off.
import type { Mod } from './api';

/** What a room has a mod at: whether it is on, or the value of a mod with options. on is the room's mods. */
export function modValue(on: string[], mod: Mod): boolean | number {
  if (!mod.options) return on.includes(mod.id);
  const named = on.find((name) => name.startsWith(mod.id + '='));
  return named ? Number(named.slice(mod.id.length + 1)) : mod.options[0].value;
}

/** Whether a room has a mod on. */
export const modOn = (on: string[], mod: Mod): boolean =>
  (mod.options ? modValue(on, mod) !== mod.options[0].value : on.includes(mod.id));

/**
 * The names of the mods that a room has on, in the server's order. A mod with options has its value after its name:
 * Zombie Cap 32.
 */
export const modNames = (mods: Mod[] | undefined, on: string[]): string[] => (mods || []).filter((m) => modOn(on, m))
  .map((m) => (m.options ? m.name + ' ' + modValue(on, m) : m.name));
