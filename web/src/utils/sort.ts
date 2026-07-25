// utils/sort.ts — table sorting (reads from the merged stats fields)
import type { SortDir, StatsMap, Tracker } from '../types';
import { numOf, scrapeStatus, strOf } from '../state';
import { parseRatio } from './format';
import { parseSize, parseSeedTime, memberDays } from './parse';

/** Ratio sort key: an infinite ratio ("∞") sorts highest; missing → 0. */
function ratioSort(raw: string): number {
  const r = parseRatio(raw);
  return isNaN(r) ? 0 : r;
}

export function sortKey(
  tracker: Tracker,
  col: string,
  statsCache: StatsMap,
): number | string {
  const s = statsCache[tracker.id];

  switch (col) {
    case 'name':          return tracker.name.toLowerCase();
    case 'username':      return (strOf(s, 'username') || tracker.username || '').toLowerCase();
    case 'uploaded':      return parseSize(strOf(s, 'uploaded'))   ?? 0;
    case 'downloaded':    return parseSize(strOf(s, 'downloaded')) ?? 0;
    case 'ratio':         return ratioSort(strOf(s, 'ratio'));
    case 'buffer':        return parseSize(strOf(s, 'buffer'))     ?? 0;
    case 'seed_size':     return parseSize(strOf(s, 'seed_size'))  ?? 0;
    case 'avg_seed_time': return parseSeedTime(strOf(s, 'avg_seed_time')) ?? 0;
    case 'total_seedtime': return parseSeedTime(strOf(s, 'total_seedtime')) ?? 0;
    case 'seeding':       return numOf(s, 'seeding')      ?? 0;
    case 'leeching':      return numOf(s, 'leeching')     ?? 0;
    case 'hit_and_runs':  return numOf(s, 'hit_and_runs') ?? 0;
    case 'account_age':   return memberDays(strOf(s, 'join_date')) ?? 0;
    case 'bonus_points':  return numOf(s, 'bonus_points')    ?? 0;
    case 'snatched':      return numOf(s, 'snatched')        ?? 0;
    case 'upload_snatches': return numOf(s, 'upload_snatches') ?? 0;
    case 'real_ratio':    return ratioSort(strOf(s, 'real_ratio'));
    case 'real_uploaded':   return parseSize(strOf(s, 'real_uploaded'))   ?? 0;
    case 'real_downloaded': return parseSize(strOf(s, 'real_downloaded')) ?? 0;
    case 'fl_tokens':     return numOf(s, 'fl_tokens')       ?? 0;
    case 'invites':       return numOf(s, 'invites')         ?? 0;
    case 'warnings':      return numOf(s, 'warnings')        ?? 0;
    case 'total_uploads': return numOf(s, 'uploads_approved') ?? 0;
    case 'adoptions':     return numOf(s, 'adoptions')        ?? 0;
    case 'reqs_filled':   return numOf(s, 'requests_filled')  ?? 0;
    case 'forum_posts':   return numOf(s, 'forum_posts')      ?? 0;
    // Freshness columns sort by raw unix seconds, so ascending is oldest-first
    // — which is the useful direction: the trackers that haven't updated in a
    // while come to the top. Never contacted sorts as 0, i.e. oldest of all.
    case 'last_api_update': return s?.fetched_at ?? 0;
    case 'last_scrape':     return scrapeStatus[tracker.id]?.last_scrape_at ?? 0;
    case 'scrape_health': {
      // Worst first when descending: dead cookies above plain failure
      // streaks, healthy trackers at 0.
      const ss = scrapeStatus[tracker.id];
      return (ss?.consecutive_failures ?? 0) + (ss?.cookie_expired ? 10000 : 0);
    }
    default:              return 0;
  }
}

export function getSortedTrackers(
  trackers: Tracker[],
  col: string,
  dir: SortDir,
  statsCache: StatsMap,
): Tracker[] {
  if (!col) return trackers;
  return [...trackers].sort((a, b) => {
    const ka = sortKey(a, col, statsCache);
    const kb = sortKey(b, col, statsCache);
    const cmp = typeof ka === 'string' ? ka.localeCompare(String(kb)) : Number(ka) - Number(kb);
    return dir === 'asc' ? cmp : -cmp;
  });
}
