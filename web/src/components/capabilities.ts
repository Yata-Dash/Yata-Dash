// components/capabilities.ts — "what does this tracker actually give me?"
//
// A tracker can fail to show a stat for three completely different reasons:
// Yata is broken, its API doesn't expose the stat, or its operator forbids
// scraping. Without saying which, the first explanation is the one users
// reach for. These icons say which, in the two places the question comes up:
// choosing a tracker to add, and looking at one you already have.
import type { TrackerCapabilities } from '../types';
import { esc, fieldLabel } from '../utils/format';

/** Capabilities worth their own icon, in display order. Canonical stat field
 *  names — the same vocabulary as everything else, so there is no second list
 *  to keep in step with the defs. */
const NOTABLE_ICONS: { field: string; icon: string; label: string }[] = [
  { field: 'unread_mail',          icon: 'fa-envelope', label: 'Unread mail' },
  { field: 'unread_notifications', icon: 'fa-bell',     label: 'Unread notifications' },
  { field: 'active_events',        icon: 'fa-bullhorn', label: 'Site events (freeleech, contests)' },
];

/** How a capability is obtained, in words. Rendering "not available" as an
 *  absence would read as "we didn't check". */
function sourceText(source: string, label: string): string {
  switch (source) {
    case 'api':    return `${label} — reported by the tracker's API`;
    case 'scrape': return `${label} — only from the profile page, so it needs scraping to be on`;
    default:       return `${label} — this tracker doesn't report it`;
  }
}

/**
 * The compact capability row: ladder coverage, then one icon per notable
 * capability. Returns '' when nothing is known, because "we have no record"
 * and "this tracker reports nothing" are very different claims and only one
 * of them is ours to make.
 */
export function capabilityRow(caps: TrackerCapabilities | undefined): string {
  if (!caps || !caps.known) return '';
  const parts: string[] = [];

  if (caps.ladder_total > 0) {
    // Two figures rather than one: scraping is off by default on unapproved
    // trackers and needs a session cookie, so an API-only number understates
    // an approved tracker and a combined one flatters everything else.
    const extra = caps.scrape_possible && caps.met_scrape > caps.met_api;
    const cls = caps.met_api === caps.ladder_total ? 'cap-full'
      : caps.met_api === 0 ? 'cap-none' : 'cap-part';
    const missing = caps.missing?.length
      ? ` Not reported: ${caps.missing.map(fieldLabel).join(', ')}.`
      : '';
    const tip = `${caps.met_api} of ${caps.ladder_total} promotion requirements can be tracked from this tracker's API`
      + (extra ? `, ${caps.met_scrape} of ${caps.ladder_total} if profile scraping is on.` : '.')
      + missing;
    // The fraction stays whole and the scrape bonus follows it — "3/6 +1"
    // rather than "3+1/6", which reads as a broken fraction.
    parts.push(`<span class="cap-chip ${cls}" title="${esc(tip)}">
      <i class="fas fa-bullseye"></i>${caps.met_api}/${caps.ladder_total}${
        extra ? `<span class="cap-plus">+${caps.met_scrape - caps.met_api}</span>` : ''}</span>`);
  }

  for (const n of NOTABLE_ICONS) {
    const source = caps.notables?.[n.field] ?? '';
    const cls = source === 'api' ? 'cap-on' : source === 'scrape' ? 'cap-scrape' : 'cap-off';
    parts.push(`<span class="cap-icon ${cls}" title="${esc(sourceText(source, n.label))}">
      <i class="fas ${n.icon}"></i></span>`);
  }

  if (!caps.scrape_possible) {
    parts.push(`<span class="cap-icon cap-apionly" title="${esc('API only — this tracker\'s operator has asked not to be scraped, so everything comes from its API')}">
      <i class="fas fa-plug"></i></span>`);
  }
  return `<span class="cap-row">${parts.join('')}</span>`;
}

const CAPS_OPEN_KEY = 'yata.detail.caps-open';

/** Whether the breakdown was left open. Collapsed by default: this answers a
 *  question people ask occasionally, not daily. */
function capsOpen(): boolean {
  return localStorage.getItem(CAPS_OPEN_KEY) === '1';
}

export function setCapsOpen(open: boolean): void {
  try { localStorage.setItem(CAPS_OPEN_KEY, open ? '1' : '0'); } catch { /* private mode */ }
}

/**
 * The Detail page's collapsible capability card. Closed by default with the
 * headline figure still on the summary line, so the answer to "how much of
 * this can Yata even see?" is one glance away and the full breakdown is one
 * click away — without either taking up room every day.
 */
export function capabilityCard(caps: TrackerCapabilities | undefined): string {
  if (!caps) return '';
  const chip = caps.known && caps.ladder_total > 0
    ? `<span class="cap-card-count">${caps.met_api}/${caps.ladder_total}${
        caps.scrape_possible && caps.met_scrape > caps.met_api
          ? `<span class="cap-plus">+${caps.met_scrape - caps.met_api}</span>` : ''}</span>`
    : '';
  return `<details class="cap-card"${capsOpen() ? ' open' : ''} ontoggle="capsToggled(this)">
    <summary class="cap-card-summary" title="What this tracker's definition says it can report — why some requirements above can't be tracked">
      <span>What this tracker reports</span>${chip}
    </summary>
    ${capabilityPanel(caps)}
  </details>`;
}

/**
 * The full breakdown: every stat this tracker reports, and every promotion
 * requirement it can't. Prose rather than icons because there is room here for
 * the actual answer.
 */
export function capabilityPanel(caps: TrackerCapabilities | undefined): string {
  if (!caps || !caps.known) {
    return `<div class="cap-panel-empty">No capability record for this tracker yet —
      what its API reports hasn't been declared in its definition.</div>`;
  }
  const rows: string[] = [];

  if (caps.ladder_total > 0) {
    const pct = Math.round((caps.met_api / caps.ladder_total) * 100);
    const extra = caps.scrape_possible && caps.met_scrape > caps.met_api;
    rows.push(`<div class="cap-panel-row">
      <span class="cap-panel-label">Promotion tracking</span>
      <span class="cap-panel-val">${caps.met_api} of ${caps.ladder_total} requirements${
        extra ? ` <span class="cap-plus-txt">(${caps.met_scrape} with scraping)</span>` : ''}</span>
      <div class="cap-bar"><div class="cap-bar-fill" style="width:${pct}%"></div></div>
    </div>`);
  }
  if (caps.missing?.length) {
    rows.push(`<div class="cap-panel-note">
      <strong>Not reported by this tracker:</strong> ${esc(caps.missing.map(fieldLabel).join(', '))}.
      Those requirements still apply — Yata just can't measure your progress toward them.
    </div>`);
  }
  for (const n of NOTABLE_ICONS) {
    const source = caps.notables?.[n.field] ?? '';
    rows.push(`<div class="cap-panel-row cap-panel-row--flag">
      <span class="cap-panel-label"><i class="fas ${n.icon}"></i> ${esc(n.label)}</span>
      <span class="cap-panel-val cap-${source || 'off'}">${
        source === 'api' ? 'From API' : source === 'scrape' ? 'From Scrape' : 'Not reported'}</span>
    </div>`);
  }
  if (caps.api_stats?.length) {
    rows.push(`<details class="cap-panel-details">
      <summary>${caps.api_stats.length} stats from the API</summary>
      <div class="cap-stat-list">${caps.api_stats.map(f =>
        `<span>${esc(fieldLabel(f))}</span>`).join('')}</div>
    </details>`);
  }
  return `<div class="cap-panel">${rows.join('')}</div>`;
}
