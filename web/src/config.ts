// config.ts — build-time feature flags.
//
// History shipped enabled 2026-07-12 (HISTORY_VIEW_PLAN.md phase 5). It was
// dev-gated during the build-out; the flag stays as a kill switch. To force it
// OFF on a specific install without a rebuild:
//
//   localStorage.setItem('yata.features.history', '0'); location.reload();
//
// Remove the key (or set '1') to return to the shipped default.

function lsFlagDefault(key: string, dflt: boolean): boolean {
  try {
    const v = localStorage.getItem(key);
    if (v === '1') return true;
    if (v === '0') return false;
  } catch { /* storage blocked (private mode) → fall through to default */ }
  return dflt;
}

export const FEATURES = {
  history: lsFlagDefault('yata.features.history', true),
};
