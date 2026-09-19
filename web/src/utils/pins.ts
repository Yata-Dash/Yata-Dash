// utils/pins.ts — pinned invite paths (PINNED_PATHWAYS_PLAN.md).
//
// A pin is a chain of dataset tracker names, start first and destination
// last, stored in settings and re-resolved by the server on every read. Both
// places that can pin — the Pathways view's path cards and Tracker Detail's
// "pathways from here" — go through here, so there is exactly one copy of the
// rules: one pin per destination, and saves serialised so a failed save can
// only ever roll back the change it belonged to.
import * as api from '../api';
import { toast } from '../components/toast';
import { appSettings } from '../state';
import type { PathwayPin } from '../types';

export function pinList(): PathwayPin[] { return appSettings.pathway_pins ?? []; }
export const chainKey = (hops: string[]) => hops.join(' → ');
export function isPinned(hops: string[]): boolean {
  const k = chainKey(hops);
  return pinList().some(p => chainKey(p.hops) === k);
}
/** Whether some OTHER chain to this destination is already pinned. */
export function hasPinTo(dest: string, exceptHops?: string[]): boolean {
  const k = exceptHops ? chainKey(exceptHops) : '';
  return pinList().some(p => p.hops[p.hops.length - 1] === dest && chainKey(p.hops) !== k);
}

/** Tooltip for a pin button, from the same three states everywhere. */
export function pinTitle(hops: string[]): string {
  const dest = hops[hops.length - 1];
  if (isPinned(hops)) return 'Unpin this path';
  if (hasPinTo(dest)) return `Pin this path (replaces your pinned path to ${dest})`;
  return 'Pin this path — track it at the top of Pathways';
}

let queue: Promise<boolean> = Promise.resolve(true);

/** Pin or unpin an exact chain. Resolves true when the save went through.
 *  One pin per destination: pinning a different path to a destination
 *  already pinned replaces it — "choose a path per destination" — and two
 *  near-identical cards for one place would be the list arguing with itself.
 *  Calls are queued: two clicks inside one save's round trip would otherwise
 *  let a failed first save restore a snapshot taken before the second, and
 *  undo a change the server had accepted. */
export function togglePin(hops: string[]): Promise<boolean> {
  queue = queue.then(() => doToggle(hops), () => doToggle(hops));
  return queue;
}

async function doToggle(hops: string[]): Promise<boolean> {
  const k = chainKey(hops);
  const dest = hops[hops.length - 1];
  const wasPinned = isPinned(hops);
  const before = pinList();
  const kept = before.filter(p => chainKey(p.hops) !== k && p.hops[p.hops.length - 1] !== dest);
  appSettings.pathway_pins = wasPinned ? kept : [...kept, { hops }];
  const { ok } = await api.saveSettings({ ...appSettings });
  if (!ok) {
    // Put the list back exactly as it was, or the buttons would claim a pin
    // the server never heard about and the next reload would contradict them.
    appSettings.pathway_pins = before;
    toast(wasPinned ? 'Could not unpin — settings did not save' : 'Could not pin — settings did not save', 'error');
  }
  return ok;
}
