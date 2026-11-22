// web/src/storage.ts - what the browser keeps for the page between visits (localStorage).

export const store = {
  /** The value kept under the key, or '' when there is none or the browser gives the page no storage. */
  get(key: string): string {
    try {
      return localStorage.getItem(key) || '';
    } catch {
      return '';
    }
  },
  set(key: string, value: string): void {
    try {
      localStorage.setItem(key, value);
    } catch {
      // The browser gives the page no storage. The lobby then knows the player for this visit only.
    }
  },
};
