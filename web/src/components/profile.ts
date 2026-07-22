// components/profile.ts — unified merged-stats panel (expanded rows + cards)
// All values come from statsCache[id].fields (StatField objects). There is no
// separate profile cache anymore — API and scraped values arrive pre-merged.
import type { AppSettings, StatField, Tracker, TrackerStatsResponse } from '../types';
import { esc, fieldLabel, fmtRatio, fmtSeedTime, parseRatio, ratioColor, ratioColorFor, srcDot } from '../utils/format';
import { parseSeedTime } from '../utils/parse';
import { scrapeStatus } from '../state';

/** Known canonical fields in display order, with label/colour/formatting. */
interface StatRowDef {
  key: string;
  label: string;
  color: string | ((v: string) => string);
  fmt?: (v: string) => string;
}

const fmtDuration = (v: string): string => {
  const s = parseSeedTime(v);
  return s !== null ? fmtSeedTime(s) : v;
};

export const STAT_ROW_DEFS: StatRowDef[] = [
  { key: 'uploaded',        label: 'Uploaded',        color: 'green'  },
  { key: 'downloaded',      label: 'Downloaded',      color: 'purple' },
  { key: 'buffer',          label: 'Buffer',          color: 'blue'   },
  { key: 'ratio',           label: 'Ratio',           color: v => ratioColor(parseRatio(v)), fmt: v => fmtRatio(parseRatio(v)) },
  { key: 'required_ratio',  label: 'Required Ratio',  color: 'amber', fmt: v => fmtRatio(parseRatio(v)) },
  { key: 'real_ratio',      label: 'Real Ratio',      color: v => ratioColor(parseRatio(v)), fmt: v => fmtRatio(parseRatio(v)) },
  { key: 'bonus_points',    label: 'Bonus Points',    color: 'orange' },
  { key: 'seeding',         label: 'Seeding',         color: 'blue'   },
  { key: 'leeching',        label: 'Leeching',        color: 'amber'  },
  { key: 'hit_and_runs',    label: 'Hit & Runs',      color: v => (parseInt(v) || 0) >= 1 ? 'red' : 'green' },
  { key: 'seed_size',       label: 'Seed Size',       color: 'teal'   },
  { key: 'avg_seed_time',   label: 'Avg Seed Time',   color: 'pink',  fmt: fmtDuration },
  { key: 'total_seedtime',  label: 'Total Seed Time', color: 'pink',  fmt: fmtDuration },
  { key: 'snatched',        label: 'Snatched',        color: 'amber'  },
  { key: 'upload_snatches', label: 'Upload Snatches', color: 'green'  },
  { key: 'fl_tokens',       label: 'FL Tokens',       color: 'teal'   },
  { key: 'invites',         label: 'Invites',         color: 'blue'   },
  { key: 'warnings',        label: 'Warnings',        color: v => (parseInt(v) || 0) > 0 ? 'red' : 'text3' },
  { key: 'uploads_approved', label: 'Total Uploads',  color: 'green'  },
  { key: 'adoptions',       label: 'Adoptions',       color: 'teal'   },
  { key: 'requests_filled', label: 'Requests Filled', color: 'purple' },
];

/** Fields never shown as stat rows (rendered elsewhere: header, info, beacon).
 *  unread_* render as card icons + expanded-Info rows, not raw true/false. */
const NON_ROW_FIELDS = new Set([
  'username', 'group', 'join_date', 'active_event', 'active_event_ends_at',
  'user_id', 'unread_mail', 'unread_notifications',
]);

export interface StatRow {
  key: string;
  label: string;
  value: string;   // formatted display value
  color: string;   // CSS var name suffix
  field: StatField;
}

/**
 * Build display rows for ALL merged fields of a tracker — known canonical
 * fields first (in canonical order), then any tracker-specific extras
 * rendered generically. `exclude` lets the grid card skip fields it already
 * shows in its compact stats grid.
 */
export function buildStatRows(
  resp: TrackerStatsResponse | undefined,
  exclude: Set<string> = new Set(),
  minRatio?: number,
  settings?: AppSettings,
): StatRow[] {
  const fields = resp?.fields ?? {};
  const rows: StatRow[] = [];
  const seen = new Set<string>();
  const liveRequiredRatio = parseRatio(String(fields.required_ratio?.value ?? ''));
  const effectiveMinRatio = !isNaN(liveRequiredRatio) && liveRequiredRatio > 0
    ? liveRequiredRatio
    : minRatio;

  const push = (key: string, label: string, colorDef: StatRowDef['color'], fmt?: (v: string) => string) => {
    const f = fields[key];
    if (!f || f.value == null || f.value === '') return;
    const raw = String(f.value);
    rows.push({
      key, label,
      value: fmt ? fmt(raw) : raw,
      color: typeof colorDef === 'function' ? colorDef(raw) : colorDef,
      field: f,
    });
  };

  for (const def of STAT_ROW_DEFS) {
    seen.add(def.key);
    if (exclude.has(def.key)) continue;
    // min_ratio-aware colouring for the main ratio stat (item 7)
    const colorDef = def.key === 'ratio' && effectiveMinRatio && effectiveMinRatio > 0
      ? (v: string) => ratioColorFor(parseRatio(v), effectiveMinRatio)
      // Highlight toggle for hit & runs — neutral instead of red when off.
      : def.key === 'hit_and_runs' && settings?.highlight_hnr === false
      ? (v: string) => (parseInt(v) || 0) >= 1 ? 'text3' : 'green'
      : def.color;
    push(def.key, def.label, colorDef, def.fmt);
  }
  // Tracker-specific extras — render generically.
  for (const key of Object.keys(fields)) {
    if (seen.has(key) || NON_ROW_FIELDS.has(key) || exclude.has(key)) continue;
    push(key, fieldLabel(key), 'text2');
  }
  return rows;
}

/** Render rows as the .prof-list style stat list (used by expanded rows). */
export function renderStatRowsList(rows: StatRow[], settings: AppSettings): string {
  return rows.map(r => `<div class="prof-row">
    <span class="prof-label">${esc(r.label)}</span>
    <span class="prof-val" style="color:var(--${r.color})">${esc(r.value)}${srcDot(r.field, settings)}</span>
  </div>`).join('');
}

/** Scrape-status-aware refresh button (triggers a profile scrape). */
export function buildScrapeRefreshBtn(tracker: Tracker): string {
  if (!tracker.supports_html_scrape) return '';
  const ss = scrapeStatus[tracker.id];
  if (ss?.reason === 'no_scrape_support') return '';
  if (ss && !ss.allowed) {
    // Missing-credential reasons are setup states, not errors — muted hint.
    if (ss.reason === 'no_cookie' || ss.reason === 'no_username') {
      const tip = ss.reason === 'no_cookie'
        ? 'Profile scraping off — add your session cookie (Settings → Trackers)'
        : 'Profile scraping off — add your username (Settings → Trackers)';
      return `<button class="btn btn-ghost btn-sm prof-refresh prof-refresh--setup" disabled title="${esc(tip)}" style="opacity:.55;cursor:help">↻ Profile scraping off</button>`;
    }
    let tip: string;
    switch (ss.reason) {
      case 'opted_out':         tip = 'Operator opted out — Yata no longer contacts this tracker'; break;
      case 'api_only':          tip = 'API only mode — scraping disabled'; break;
      case 'scrape_disabled':   tip = 'Scrape disabled by tracker operator'; break;
      case 'daily_limit':       tip = 'Daily scrape limit reached'; break;
      case 'cooldown': {
        if (ss.next_allowed_at) {
          const d = new Date(ss.next_allowed_at * 1000);
          tip = `Next scrape at ${d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}`;
        } else { tip = 'Scrape cooldown active'; }
        break;
      }
      default: tip = 'Scraping blocked';
    }
    return `<button class="btn btn-ghost btn-sm prof-refresh" disabled title="${esc(tip)}" style="opacity:.45;cursor:not-allowed">↻ ${esc(tip)}</button>`;
  }
  if (!tracker.supports_html_scrape) return '';
  return `<button class="btn btn-ghost btn-sm prof-refresh" onclick="scrapeProfile('${esc(tracker.id)}')">↻ Scrape Now</button>`;
}

/**
 * Build the full unified stats panel for an expanded row: every merged field
 * plus a footer with field count and the scrape refresh button.
 */
export function buildStatsPanel(
  tracker: Tracker,
  resp: TrackerStatsResponse | undefined,
  settings: AppSettings,
): string {
  const rows = buildStatRows(resp, undefined, tracker.min_ratio, settings);

  if (!rows.length) {
    let hint: string;
    if (!tracker.has_key && tracker.type !== 'test') {
      hint = `No API key configured. <button class="btn btn-ghost btn-sm" onclick="openEditModal('${esc(tracker.id)}')">Configure</button>`;
    } else if (!resp) {
      hint = 'Loading…';
    } else {
      hint = 'No stats available yet.';
    }
    return `<div class="prof-hint">${hint}</div>`;
  }

  return `<div class="prof-list">${renderStatRowsList(rows, settings)}</div>
    <div class="prof-footer">
      <span class="prof-meta">${rows.length} fields</span>
      ${buildScrapeRefreshBtn(tracker)}
    </div>`;
}
