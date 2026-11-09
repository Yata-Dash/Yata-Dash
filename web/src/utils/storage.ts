// utils/storage.ts — Web Storage that never throws.
//
// Every preference the dashboard keeps in the browser (view, sort, column
// layout, open sections, …) is a convenience: losing one is harmless, but an
// exception from reading one is not. When site data is blocked (a privacy
// setting, some embedded or private-browsing contexts) merely touching
// `window.localStorage` throws a SecurityError, and several of those reads
// run at module load — so one of them threw before anything rendered and the
// whole app came up blank. Everything goes through here instead: a blocked or
// full store reads as empty and swallows writes.

type Area = 'localStorage' | 'sessionStorage';

function store(area: Area): Storage | null {
  // The property access itself is what throws when storage is blocked.
  try { return window[area]; } catch { return null; }
}

function wrap(area: Area) {
  return {
    /** The stored string, or null when absent or storage is unavailable. */
    get(key: string): string | null {
      try { return store(area)?.getItem(key) ?? null; } catch { return null; }
    },
    /** Store a value; silently dropped when storage is unavailable or full. */
    set(key: string, value: string): void {
      try { store(area)?.setItem(key, value); } catch { /* blocked or quota — the preference just isn't kept */ }
    },
    remove(key: string): void {
      try { store(area)?.removeItem(key); } catch { /* blocked */ }
    },
  };
}

/** localStorage: survives reloads and browser restarts. */
export const localStore = wrap('localStorage');
/** sessionStorage: this tab, until it closes. */
export const sessionStore = wrap('sessionStorage');
