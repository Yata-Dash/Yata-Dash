// utils/optout.ts — tracker opt-out matching (defs/optout.json via /api/defs)
// Trackers on the opt-out list have asked not to be supported by Yata;
// adding them is blocked in the UI (and 403'd by the backend as a backstop).
import type { OptOutEntry } from '../types';

/** Bare lowercase hostname of a URL — ignores scheme and a leading "www.". */
export function bareHost(url: string): string {
  const s = String(url ?? '').trim();
  if (!s) return '';
  let host = '';
  try {
    host = new URL(/^[a-z][a-z0-9+.-]*:\/\//i.test(s) ? s : `https://${s}`).hostname;
  } catch {
    host = s.replace(/^[a-z][a-z0-9+.-]*:\/\//i, '').split(/[/?#:]/)[0] ?? '';
  }
  return host.toLowerCase().replace(/^www\./, '');
}

/** Find the opt-out entry matching a tracker URL, if any. */
export function findOptOut(optOuts: OptOutEntry[] | undefined, url: string): OptOutEntry | undefined {
  const host = bareHost(url);
  if (!host || !optOuts?.length) return undefined;
  return optOuts.find(o => bareHost(o.host) === host);
}

/** "{name} has asked not to be supported by Yata ({date}). {note}" */
export function optOutMessage(entry: OptOutEntry): string {
  const date = entry.date ? ` (${entry.date})` : '';
  const note = entry.note ? ` ${entry.note}` : '';
  return `${entry.name} has asked not to be supported by Yata${date}.${note}`;
}
