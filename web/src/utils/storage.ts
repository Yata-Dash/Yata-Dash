// utils/storage.ts — Web Storage that never throws.
// With site data blocked, touching `window.localStorage` throws, and some reads run at
// module load, so one throw blanked the app. Blocked or full reads as empty.

type Area = 'localStorage' | 'sessionStorage';

function store(area: Area): Storage | null {
  // The property access itself is what throws when storage is blocked.
  try { return window[area]; } catch { return null; }
}

function wrap(area: Area) {
  return {
    get(key: string): string | null {
      try { return store(area)?.getItem(key) ?? null; } catch { return null; }
    },
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
