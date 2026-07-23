// components/trackerTest.ts — shared rendering for the tracker connectivity
// test (API + scrape). Used by the trackers table (compact pills) and the
// edit-tracker panel (detailed result rows). Pure helpers only — the actual
// test requests live in trackersTab.ts (table) and modals.ts (edit panel).
import type { CheckResult, TrackerTestResult } from '../types';
import { esc } from '../utils/format';

/** Human-readable explanation for a check's detail code (error or reason). */
export function friendlyDetail(detail?: string): string {
  if (!detail) return '';
  const map: Record<string, string> = {
    // Not configured
    no_key: 'No API key set',
    no_cookie: 'No session cookie set',
    no_username: 'No username set',
    // Not applicable
    scrape_only: 'This tracker has no API — profile scrape only',
    no_scrape_support: 'This tracker type can’t be scraped',
    opted_out: 'The tracker operator opted out — Yata no longer contacts this tracker',
    scrape_disabled: 'The tracker operator disabled scraping',
    api_only: 'API-only mode is on for this tracker',
    // Blocked (rate limits — no request was sent)
    cooldown: 'Scrape cooldown active — testing now would exceed the tracker’s rate limit',
    daily_limit: 'Daily scrape cap reached — try again tomorrow',
    // Failures
    session_expired: 'Session cookie expired — log in again and re-copy it',
    empty_scrape: 'Profile page returned no recognisable stats — likely an anti-bot or maintenance page',
    forbidden: 'Forbidden (403) — cookie likely invalid or expired',
    user_not_found: 'Profile page not found — check the username',
    timeout: 'Request timed out',
    connection_error: 'Could not connect to the tracker',
    parse_error: 'Unexpected response — could not parse',
    api_error: 'The tracker API returned an error',
    read_error: 'Could not read the response',
    no_def: 'No matching tracker definition',
  };
  if (map[detail]) return map[detail];
  const http = detail.match(/^http_(\d+)$/);
  if (http) return `HTTP ${http[1]} from the tracker`;
  return detail.replace(/_/g, ' ');
}

interface PillMeta { cls: string; icon: string; word: string; }

function pillMeta(c: CheckResult): PillMeta {
  switch (c.status) {
    case 'ok':             return { cls: 'ok',   icon: 'fa-circle-check',  word: 'Working' };
    case 'fail':           return { cls: 'fail', icon: 'fa-circle-xmark',  word: 'Failed' };
    case 'not_configured': return { cls: 'cfg',  icon: 'fa-circle-minus',  word: 'Not set up' };
    case 'blocked':        return { cls: 'cfg',  icon: 'fa-clock',         word: 'Rate-limited' };
    default:               return { cls: 'na',   icon: 'fa-minus',         word: 'N/A' };
  }
}

/** A compact labelled pill ("API ✓" / "Scrape ✗") for the trackers table. */
function pill(label: string, c: CheckResult): string {
  const m = pillMeta(c);
  const detail = friendlyDetail(c.detail);
  const title = `${label}: ${m.word}${detail ? ` — ${detail}` : ''}`;
  return `<span class="trk-test-pill ${m.cls}" title="${esc(title)}"><i class="fas ${m.icon}"></i>${esc(label)}</span>`;
}

/** Render the two-pill status indicator shown in the trackers table cell. */
export function renderTestPills(res: TrackerTestResult | undefined): string {
  if (!res) return `<span class="trk-test-untested">Not tested</span>`;
  return `<span class="trk-test-pills">${pill('API', res.api)}${pill('Scrape', res.scrape)}</span>`;
}

/** A detailed result row ("API — Working / Failed — reason") for the edit panel. */
function detailRow(label: string, c: CheckResult): string {
  const m = pillMeta(c);
  const detail = friendlyDetail(c.detail);
  const fields = c.status === 'ok' && c.fields ? ` <span class="trk-test-fields">(${c.fields} fields)</span>` : '';
  const reason = detail ? ` <span class="trk-test-reason">— ${esc(detail)}</span>` : '';
  return `<div class="trk-test-row ${m.cls}">
    <i class="fas ${m.icon}"></i>
    <span class="trk-test-label">${esc(label)}</span>
    <span class="trk-test-word">${m.word}</span>${fields}${reason}
  </div>`;
}

/** Render the detailed two-row result block shown in the edit-tracker panel. */
export function renderTestDetail(res: TrackerTestResult): string {
  return `${detailRow('API', res.api)}${detailRow('Profile scrape', res.scrape)}`;
}
