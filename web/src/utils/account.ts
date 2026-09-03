// utils/account.ts — the two account deadlines a tracker can quietly miss.
//
// The backend derives login_days_remaining and api_key_expiry_days from a
// stored timestamp plus a clock (internal/stats/account.go) and OMITS them
// whenever it lacks the input. Everything here therefore treats "field absent"
// as "no deadline known" and shows nothing — never a reassuring zero.
import type { TrackerStatsResponse } from '../types';
import { esc } from './format';

/** How near a deadline has to be before the badge appears, in days. */
const LOGIN_WARN_DAYS = 14;
const KEY_WARN_DAYS = 30;

interface AccountWarning {
  kind: 'login' | 'api_key';
  /** Days left; negative means the deadline has passed. */
  days: number;
  /** 'red' once the deadline is past, 'amber' while it can still be met. */
  level: 'amber' | 'red';
  label: string;
  title: string;
}

function num(resp: TrackerStatsResponse | undefined, key: string): number | null {
  const v = resp?.fields?.[key]?.value;
  if (v === null || v === undefined || v === '') return null;
  const n = Number(v);
  return Number.isFinite(n) ? n : null;
}

function str(resp: TrackerStatsResponse | undefined, key: string): string {
  const v = resp?.fields?.[key]?.value;
  return v === null || v === undefined ? '' : String(v);
}

/** "in 5 days" / "today" / "5 days ago" — the badge's countdown. */
function fmtDays(days: number): string {
  if (days < 0) return `${-days}d overdue`;
  if (days === 0) return 'today';
  return `${days}d`;
}

/** A stored timestamp as a plain calendar date, or '' if it isn't one. */
export function statDate(raw: string): string {
  const s = raw.trim();
  if (!s) return '';
  const d = new Date(s.includes('T') || !s.includes(' ') ? s : s.replace(' ', 'T') + 'Z');
  return isNaN(d.getTime()) ? s : d.toISOString().slice(0, 10);
}

/**
 * The deadline badges for one tracker, nearest first. Empty when nothing is
 * near — which is the normal state, and the reason these are badges rather
 * than a permanent row.
 */
function accountWarnings(resp: TrackerStatsResponse | undefined): AccountWarning[] {
  const out: AccountWarning[] = [];

  const login = num(resp, 'login_days_remaining');
  if (login !== null && login <= LOGIN_WARN_DAYS) {
    const since = num(resp, 'days_since_login');
    const last = statDate(str(resp, 'last_login'));
    // The tracker's own gap in days is not sent to the client, so the tooltip
    // says what IS known — when you last logged in, and how long is left —
    // rather than paraphrasing a policy it would have to guess at.
    const parts = [
      last ? `Last login ${last}` : 'Last login unknown',
      since !== null ? `${since} days ago` : '',
      login < 0
        ? `${-login} days past this tracker's inactivity deadline`
        : `${login} days before this tracker's inactivity deadline`,
    ].filter(Boolean);
    out.push({
      kind: 'login',
      days: login,
      level: login < 0 ? 'red' : 'amber',
      label: login < 0 ? 'Login overdue' : `Login ${fmtDays(login)}`,
      title: parts.join(' · '),
    });
  }

  const key = num(resp, 'api_key_expiry_days');
  if (key !== null && key <= KEY_WARN_DAYS) {
    const on = statDate(str(resp, 'api_key_expires_at'));
    out.push({
      kind: 'api_key',
      days: key,
      level: key < 0 ? 'red' : 'amber',
      label: key < 0 ? 'Key expired' : `Key ${fmtDays(key)}`,
      title: key < 0
        ? `API key expired${on ? ` on ${on}` : ''} — stats stop until you issue a new one`
        : `API key expires${on ? ` on ${on}` : ''} — ${key} days left`,
    });
  }

  return out.sort((a, b) => a.days - b.days);
}

/** Badge markup for a card/row header. '' when nothing is near. */
export function accountWarningBadges(resp: TrackerStatsResponse | undefined): string {
  return accountWarnings(resp)
    .map(w => `<span class="account-warn account-warn--${w.level}" title="${esc(w.title)}">${esc(w.label)}</span>`)
    .join('');
}

/** Whole days from now until `raw`; negative once it has passed. null if the
 *  timestamp can't be read. Mirrors the backend's truncation toward zero. */
function daysUntil(raw: string): number | null {
  const s = raw.trim();
  if (!s) return null;
  const d = new Date(s.includes('T') || !s.includes(' ') ? s : s.replace(' ', 'T') + 'Z');
  if (isNaN(d.getTime())) return null;
  return Math.trunc((d.getTime() - Date.now()) / 86400000);
}

/** "2026-08-30 · 3 days ago" — the Last Login stat row. */
export function fmtLastLogin(raw: string): string {
  const date = statDate(raw);
  const ago = daysUntil(raw);
  if (ago === null) return raw;
  const rel = ago >= 0 ? 'today' : ago === -1 ? 'yesterday' : `${-ago} days ago`;
  return `${date} · ${rel}`;
}

/** "2027-01-01 · in 121 days" — the API Key Expires stat row. */
export function fmtKeyExpiry(raw: string): string {
  const date = statDate(raw);
  const left = daysUntil(raw);
  if (left === null) return raw;
  const rel = left < 0 ? `expired ${-left} days ago` : left === 0 ? 'expires today' : `in ${left} days`;
  return `${date} · ${rel}`;
}

/** Just the relative half — "today", "yesterday", "4 days ago". Used where the
 *  exact date adds nothing: under a tracker's login policy, the only question
 *  is how the gap so far compares to the gap allowed. */
export function fmtLoginAgo(raw: string): string {
  const ago = daysUntil(raw);
  if (ago === null) return raw;
  if (ago >= 0) return 'today';
  if (ago === -1) return 'yesterday';
  return `${-ago} days ago`;
}
