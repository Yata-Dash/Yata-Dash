// views/grid.ts — tracker card grid view (reads merged stats fields)
import type { AppSettings, Tracker, TrackerGroupMap, TrackerStatsResponse } from '../types';
import { appSettings, fieldOf, numOf, scrapeStatus, strOf } from '../state';
import { eventGlobeSvg } from '../utils/icons';
import { esc, errLabel, fieldLabel, fmtDueDate, fmtEtaDays, fmtGib, fmtGoalRate, fmtRatio, fmtSeedTime, fmtSeedTimeStacked, fmtTrackerName, parseRatio, rateTip, ratioColorFor, srcDot } from '../utils/format';
import { getFaviconUrl, memberDays, memberDur, parseAgeDays, parseSize, parseSeedTime } from '../utils/parse';
import { findGroupDef, groupRequirementsToTargets, renderGroupBadge, renderUsername } from '../utils/group';
import { computeGoalPacing } from '../utils/pacing';
import type { Pacing } from '../utils/pacing';
import { buildStatRows, buildScrapeRefreshBtn } from '../components/profile';

type ReorderFn = (srcId: string, dstId: string) => void;

let dragSrcId: string | null = null;

/** Format a date/time: today → HH:MM am/pm, other → yyyy-mm-dd.
 *  Accepts ISO8601 string or unix timestamp (seconds). */
export function fmtDateTime(raw: string | number | null | undefined): string {
  if (!raw) return '—';
  const d = typeof raw === 'number' ? new Date(raw * 1000) : new Date(raw as string);
  if (isNaN(d.getTime())) return '—';
  const now = new Date();
  const isToday = d.getFullYear() === now.getFullYear()
    && d.getMonth() === now.getMonth()
    && d.getDate() === now.getDate();
  if (isToday) return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  return d.toISOString().slice(0, 10);
}

/** Render the full grid — creates card shells and fills them */
export function renderGrid(
  trackers: Tracker[],
  statsCache: Record<string, TrackerStatsResponse>,
  onReorder: ReorderFn,
  settings?: AppSettings,
  groupDefs?: TrackerGroupMap,
): void {
  const grid = document.getElementById('tracker-grid')!;
  grid.innerHTML = '';

  if (!trackers.length) {
    grid.innerHTML = `<div style="grid-column:1/-1;text-align:center;padding:70px 20px;color:var(--text3)">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1" style="margin-bottom:16px;opacity:.3">
        <circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/>
      </svg>
      <p style="font-size:16px;font-weight:600;color:var(--text)">Welcome to Yata</p>
      <p style="font-size:13px;margin:6px auto 18px;max-width:420px">Track your ratio, buffer, seed size and promotion paths across all your private trackers — with alerts when something needs attention.</p>
      <button class="btn btn-primary" onclick="openAddModal()">+ Add your first tracker</button>
      <p style="font-size:12px;margin-top:14px">…or import everything at once from <strong>Prowlarr / Jackett</strong> in Settings → Trackers.<br>
      Just exploring? Add the credential-free <strong>Test Tracker</strong> to see the whole UI with demo data.</p>
    </div>`;
    return;
  }

  // Disabled trackers are hidden from the dashboard — they live in
  // Settings → Trackers until re-enabled. Distinct empty state so a fully
  // disabled set doesn't show the first-run welcome.
  const visible = trackers.filter(t => t.enabled !== false);
  if (!visible.length) {
    grid.innerHTML = `<div style="grid-column:1/-1;text-align:center;padding:50px 20px;color:var(--text3);font-size:13px">
      All trackers are disabled — re-enable them in Settings → Trackers.</div>`;
    return;
  }

  visible.forEach(t => {
    const el = document.createElement('div');
    el.className = 'tracker-card';
    el.id = `card-${t.id}`;
    el.draggable = false;
    attachDragEvents(el, t.id, onReorder);
    grid.appendChild(el);
    if (settings) renderCard(t, statsCache[t.id], settings, groupDefs);
  });
}

/** Core fields shown in the card's compact stats grid (excluded from More Stats). */
const CARD_CORE_FIELDS = new Set([
  'uploaded', 'downloaded', 'ratio', 'buffer', 'seed_size',
  'avg_seed_time', 'seeding', 'leeching', 'bonus_points', 'hit_and_runs',
  'warnings', 'uploads_approved',
]);

/** Re-render a single tracker card */
export function renderCard(
  tracker: Tracker,
  stats: TrackerStatsResponse | undefined,
  settings: AppSettings,
  groupDefs?: TrackerGroupMap,
): void {
  const el = document.getElementById(`card-${tracker.id}`);
  if (!el) return;
  el.classList.remove('loading', 'error-state');

  // def_key comes from the server-side defs registry (URL + alias match) —
  // the only source of tracker identity. "" = manual tracker, no group data.
  const tKey      = tracker.def_key;
  const liveGroup = strOf(stats, 'group');
  const groupDef  = groupDefs ? findGroupDef(groupDefs, tKey, liveGroup) : undefined;

  const joinDate = strOf(stats, 'join_date');
  const memberBadge = joinDate
    ? `<span class="badge-membership" title="Since ${esc(joinDate)}">${memberDur(joinDate)}</span>`
    : '';

  const activeEvent = strOf(stats, 'active_event');
  const eventBeacon = activeEvent
    ? `<span class="event-beacon" title="${esc(activeEvent)}">
        ${eventGlobeSvg()}
      </span>`
    : '';

  // Unread mail/notification flags (scraped header presence — freshness is
  // the scrape cadence, so at most once per interval). Each icon has its own
  // Display toggle: mail matters to most users, notifications vary.
  const unreadFlags =
    (settings.show_unread_mail !== false && strOf(stats, 'unread_mail') === 'true'
      ? `<span class="unread-flag" title="Unread mail on ${esc(tracker.name)} (as of the last scrape) — check your inbox"><i class="fas fa-envelope"></i></span>` : '') +
    (settings.show_unread_notifications !== false && strOf(stats, 'unread_notifications') === 'true'
      ? `<span class="unread-flag" title="Unread notifications on ${esc(tracker.name)} (as of the last scrape)"><i class="fas fa-bell"></i></span>` : '');

  // Favicon sits next to the tracker name in the header
  const favicon = settings.show_favicons && tracker.url
    ? `<img class="tracker-favicon" src="${getFaviconUrl(tracker.url)}" alt="" onerror="this.style.display='none'" style="width:14px;height:14px;flex-shrink:0">`
    : '';

  const header = `<div class="card-header">
    <div class="drag-handle" title="Drag to reorder">
      <svg width="12" height="16" viewBox="0 0 12 16" fill="currentColor">
        <circle cx="4" cy="3" r="1.5"/><circle cx="8" cy="3" r="1.5"/>
        <circle cx="4" cy="8" r="1.5"/><circle cx="8" cy="8" r="1.5"/>
        <circle cx="4" cy="13" r="1.5"/><circle cx="8" cy="13" r="1.5"/>
      </svg>
    </div>
    ${makeSdot(tracker, stats)}
    <div class="card-header-info">
      <div class="card-tracker-name" style="display:flex;align-items:center;gap:5px">
        ${favicon}
        <span class="tracker-name-link" title="Open tracker detail"
          onclick="event.stopPropagation();openTrackerDetail('${tracker.id}')"
          style="overflow:hidden;text-overflow:ellipsis;white-space:nowrap">${esc(fmtTrackerName(tracker.name, tracker.abbr, settings.tracker_name_mode))}</span><span style="flex-shrink:0">${tracker.type === 'test' ? '<span class="mock-badge">TEST</span>' : ''}${eventBeacon}${unreadFlags}</span>
      </div>
      <div class="card-header-meta">
        <a class="card-tracker-url" href="${esc(tracker.url)}" target="_blank" rel="noopener"
          onclick="event.stopPropagation()">${esc(tracker.url)}</a>
        ${memberBadge}
        ${tracker.profile_url
          ? `<a class="card-profile-link" href="${esc(tracker.profile_url)}" target="_blank" rel="noopener noreferrer" onclick="event.stopPropagation()">Open profile&nbsp;&#8599;</a>`
          : ''}
      </div>
    </div>
  </div>`;

  let body: string;
  const hasFields = !!stats && Object.keys(stats.fields ?? {}).length > 0;

  if (!stats && !tracker.has_key && tracker.type !== 'test') {
    body = `<div class="card-body"><div class="card-no-key">
      <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round">
        <rect x="3" y="11" width="18" height="11" rx="2" ry="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>
      </svg>
      <p>No API key configured</p>
      <button class="btn btn-ghost btn-sm" onclick="openEditModal('${tracker.id}')">Configure API Key</button>
    </div></div>`;
  } else if (!stats) {
    body = `<div class="card-body">${skelStats()}</div>`;
  } else if (!hasFields && !stats.ok) {
    // Error and nothing stored yet — only here may we show an error-only body.
    el.classList.add('error-state');
    body = `<div class="card-body">
      <div class="card-error-msg">${errLabel(stats.error_kind || stats.error || 'error')}</div>
      <button class="btn btn-ghost btn-sm" style="align-self:flex-start" onclick="refreshSingle('${tracker.id}')">Retry</button>
    </div>`;
  } else {
    // STALE DATA RULE: render fields exactly like fresh data even when
    // stats.ok is false — the offline state only adds a banner + dimmed dot.
    const ratio    = parseRatio(strOf(stats, 'ratio'));
    const requiredRatio = parseRatio(strOf(stats, 'required_ratio'));
    const effectiveMinRatio = !isNaN(requiredRatio) && requiredRatio > 0 ? requiredRatio : tracker.min_ratio;
    const rc       = ratioColorFor(ratio, effectiveMinRatio);
    const hnr      = parseInt(strOf(stats, 'hit_and_runs')) || 0;
    const updated  = fmtDateTime(stats.fetched_at);
    const ast      = parseSeedTime(strOf(stats, 'avg_seed_time'));
    const seeding  = parseInt(strOf(stats, 'seeding'))  || 0;
    const leeching = parseInt(strOf(stats, 'leeching')) || 0;
    const bonusRaw = strOf(stats, 'bonus_points');

    const displayUsername = strOf(stats, 'username') || tracker.username || '';
    const usernameHtml = renderUsername(displayUsername || '—', groupDef, settings, 'card-username private-blur');
    const groupHtml    = liveGroup ? renderGroupBadge(groupDef, liveGroup, settings, 'badge-group') : '';

    // Perk icons — small row of FA icons shown immediately after the group badge
    const perksIconHtml = buildPerkIcons(groupDef, settings);

    const targetsHtml = buildTargets(tracker, stats, settings, groupDefs, tKey);
    const moreHtml    = buildMoreStatsBoxes(tracker, stats, settings);

    const offlineBanner = !stats.ok
      ? `<div class="card-error-msg" style="padding:6px 10px;font-size:11px">
          ${errLabel(stats.error_kind || stats.error || 'error')} — showing last known stats
          <button class="btn btn-ghost btn-sm" style="margin-left:auto" onclick="event.stopPropagation();refreshSingle('${tracker.id}')">Retry</button>
        </div>`
      : '';
    if (!stats.ok) el.classList.add('error-state');

    const stat = (label: string, key: string, color: string, value: string, tip = '') =>
      `<div class="stat-item"${tip ? ` title="${esc(tip)}"` : ''}><div class="stat-label">${label}</div><div class="stat-value ${color}">${value}${srcDot(fieldOf(stats, key), settings)}</div></div>`;

    const warnings = numOf(stats, 'warnings');
    const minRatioTip = !isNaN(requiredRatio) && requiredRatio > 0
      ? `Your required ratio: ${requiredRatio}`
      : tracker.min_ratio && tracker.min_ratio > 0 ? `Tracker minimum: ${tracker.min_ratio}` : '';
    const totalUploads = strOf(stats, 'uploads_approved');
    const hnrColor = hnr < 1 ? 'green' : (settings.highlight_hnr !== false ? 'red' : 'text3');
    // Per-day trend rollover ("≈ 245.3 GiB per day") from the growth rates.
    const rTip = (f: string) => rateTip(stats?.rates, f, settings);

    body = `<div class="card-body">
      ${offlineBanner}
      <div class="card-user">
        ${usernameHtml}
        ${groupHtml}
        ${perksIconHtml}
      </div>
      <div class="stats-grid">
        ${stat('Uploaded',      'uploaded',        'green',  esc(strOf(stats, 'uploaded')   || '—'), rTip('uploaded'))}
        ${stat('Downloaded',    'downloaded',      'purple', esc(strOf(stats, 'downloaded') || '—'), rTip('downloaded'))}
        ${stat('Ratio',         'ratio',           rc,       fmtRatio(ratio), minRatioTip)}
        ${stat('Seeding',       'seeding',         'blue',   String(seeding))}
        ${stat('Leeching',      'leeching',        'amber',  String(leeching))}
        ${stat('Buffer',        'buffer',          'blue',   esc(strOf(stats, 'buffer')     || '—'), rTip('buffer'))}
        ${stat('Seed Size',     'seed_size',       'teal',   esc(strOf(stats, 'seed_size')  || '—'))}
        ${stat('Avg Seed Time', 'avg_seed_time',   'pink',   ast !== null ? fmtSeedTimeStacked(ast) : '—')}
        ${stat('Bonus',         'bonus_points',    'orange', esc(bonusRaw || '—'), rTip('bonus_points'))}
        ${stat('Total Uploads', 'uploads_approved','green',  esc(totalUploads || '—'), rTip('uploads_approved'))}
        ${stat('Warnings',      'warnings',        warnings !== null && warnings > 0 ? 'red' : 'text3', warnings !== null ? String(warnings) : '—')}
        ${stat('H&Rs',          'hit_and_runs',    hnrColor, String(hnr))}
      </div>
      ${targetsHtml ? `<div class="card-section-wrap">${targetsHtml}</div>` : ''}
      ${moreHtml ? `<div class="card-section-wrap">
        <div class="card-section-title">More Stats</div>
        ${moreHtml}
      </div>` : ''}
      ${buildRulesLine(tracker, settings)}
    </div>
    <div class="card-footer">
      <span class="card-last-updated">API ${updated}</span>
      ${(() => {
        const ss = scrapeStatus[tracker.id];
        if (!ss || ss.allowed) return '';
        let tip: string;
        let setup = false; // setup states (missing credentials) — muted, not amber
        if (ss.reason === 'opted_out')              tip = 'Operator opted out';
        // api_only / no_scrape_support / scrape_disabled differ only in CAUSE
        // (user toggle vs type can't scrape vs operator forbids) — to the user
        // they all mean the same thing here: stats come from the API alone.
        // One label; the precise cause stays in the edit-modal hint and the
        // connectivity-test detail.
        else if (ss.reason === 'api_only' || ss.reason === 'no_scrape_support' || ss.reason === 'scrape_disabled')
          tip = 'API only';
        else if (ss.reason === 'no_cookie')   { tip = 'Profile scraping off — add your session cookie (Settings → Trackers)'; setup = true; }
        else if (ss.reason === 'no_username') { tip = 'Profile scraping off — add your username (Settings → Trackers)'; setup = true; }
        else if (ss.reason === 'daily_limit')       tip = 'Daily limit reached';
        else if (ss.reason === 'cooldown' && ss.next_allowed_at) {
          const d = new Date(ss.next_allowed_at * 1000);
          tip = `Scrape at ${d.toLocaleTimeString([], {hour:'2-digit',minute:'2-digit'})}`;
        } else tip = 'Scraping blocked';
        return `<span class="scrape-limit-badge${setup ? ' setup' : ''}" title="${esc(tip)}">${esc(tip)}</span>`;
      })()}
    </div>`;
  }

  el.innerHTML = header + body;
  // Re-attach drag/drop listeners after innerHTML wipe (drop targets still needed)
  attachDragEvents(el, tracker.id, () => {});
}

/** Compact display-only rules line at the bottom of a card (def account-wide
 *  rules: min ratio / min seed time). Follows the Display toggle; the fine
 *  print stays on the tracker's rules page. */
function buildRulesLine(tracker: Tracker, settings: AppSettings): string {
  if (settings.show_tracker_rules === false) return '';
  const parts: string[] = [];
  if (tracker.min_ratio && tracker.min_ratio > 0) parts.push(`Ratio ≥ ${tracker.min_ratio}`);
  if (tracker.min_seed_days_episode && tracker.min_seed_days_episode > 0)
    parts.push(`Episode seed ≥ ${tracker.min_seed_days_episode} day${tracker.min_seed_days_episode === 1 ? '' : 's'}`);
  if (tracker.min_seed_days_season && tracker.min_seed_days_season > 0)
    parts.push(`Season seed ≥ ${tracker.min_seed_days_season} day${tracker.min_seed_days_season === 1 ? '' : 's'}`);
  if (tracker.min_seed_hours && tracker.min_seed_hours > 0)
    parts.push(`Seed ≥ ${tracker.min_seed_hours} hours`);
  if (!tracker.min_seed_hours && !tracker.min_seed_days_episode && !tracker.min_seed_days_season && tracker.min_seed_days && tracker.min_seed_days > 0)
    parts.push(`Seed ≥ ${tracker.min_seed_days} day${tracker.min_seed_days === 1 ? '' : 's'}`);
  if (tracker.rule_note) parts.push(tracker.rule_note);
  if (!parts.length) return '';
  return `<div class="card-rules" title="Tracker rules (reference) — full details on the tracker's rules page">
    <i class="fas fa-scale-balanced"></i><span>${parts.map(esc).join(' · ')}</span>
  </div>`;
}

/** Small row of perk icons (with custom tooltip) shown after the group badge */
export function buildPerkIcons(groupDef: ReturnType<typeof findGroupDef>, settings: AppSettings): string {
  if (!groupDef?.perks?.length) return '';
  const styled   = (settings.group_name_style || 'plain') === 'styled';
  const iconColor = styled ? (groupDef.style?.color ?? 'var(--text3)') : 'var(--text3)';
  const icons = groupDef.perks
    .filter(p => p.icon)
    .map(p => `<span class="perk-icon-wrap" data-tip="${esc(p.label)}"><i class="${esc(p.icon)}" style="color:${esc(iconColor)};font-size:11px;opacity:.85"></i><span class="perk-tip">${esc(p.label)}</span></span>`)
    .join('');
  if (!icons) return '';
  return `<span class="card-perk-icons">${icons}</span>`;
}

/** Extended merged fields rendered as stat-item boxes (card "More Stats"). */
function buildMoreStatsBoxes(
  tracker: Tracker,
  stats: TrackerStatsResponse,
  settings: AppSettings,
): string {
  const rows = buildStatRows(stats, CARD_CORE_FIELDS);

  if (!rows.length) {
    const refresh = buildScrapeRefreshBtn(tracker);
    if (!refresh) return '';
    return `<div class="prof-footer" style="margin-top:0;align-items:center">
      <span style="font-size:10px;color:var(--text3);font-style:italic">No extended data yet.</span>
      <div style="margin-left:auto">${refresh}</div>
    </div>`;
  }

  // Duration stats (total seed time) get the stacked Y/M/W/D + wrapped h/m/s
  // treatment here so long values don't overflow the card box; other surfaces
  // (detail page, table) keep the single-line form from buildStatRows.
  const DURATION_FIELDS = new Set(['total_seedtime', 'total_seed_time', 'avg_seed_time']);
  const grid = `<div class="stats-grid" style="margin-top:0">${rows.map(r => {
    const value = DURATION_FIELDS.has(r.key)
      ? fmtSeedTimeStacked(parseSeedTime(strOf(stats, r.key)))
      : esc(r.value);
    return `<div class="stat-item"><div class="stat-label">${esc(r.label)}</div><div class="stat-value ${esc(r.color)}">${value}${srcDot(r.field, settings)}</div></div>`;
  }).join('')}</div>`;

  const footer = `<div class="prof-footer" style="margin-top:6px;align-items:center">
    <span></span>
    <div style="display:flex;align-items:center;gap:6px;margin-left:auto">
      ${buildScrapeRefreshBtn(tracker)}
    </div>
  </div>`;

  return grid + footer;
}

type TgtRow = {
  label: string; cur: string; tgt: string; pct: number; color: string;
  etaDays?: number;   // projected/exact days to reach this target (only when unmet + projectable)
  etaApprox?: boolean; // true for projections that assume continuous activity (e.g. avg seed)
  etaExact?: boolean;  // true for account-age (exact countdown → "in X" not "≈ X")
  unavailable?: boolean; // requirement the tracker's API can't report — shown with an icon, no bar
  key?: string;        // canonical target key — drives the goal-pacing lookup below
  pacing?: Pacing;      // computed goal pacing, only when this row carries a deadline
  deadline?: string;    // the row's raw deadline (YYYY-MM-DD), for display
};

/**
 * Build progress rows for a targets map (canonical target keys → values).
 * A base target whose current stat is missing is either hidden (the tracker
 * has no data yet) or shown as UNAVAILABLE — an icon + tooltip, no bar — when
 * the tracker returns data but simply doesn't report that stat (e.g. an
 * API-only tracker whose profile omits seedtime). This keeps a real
 * requirement visible even though it can't be tracked. With alwaysShow=true a
 * still-trackable-but-empty row renders with "—" and an empty bar (used for
 * any_of alternatives so they stay visible even before stats arrive).
 */
function targetRowsFor(
  targets: Record<string, string>,
  stats: TrackerStatsResponse | undefined,
  minRatio: number | undefined,
  alwaysShow: boolean,
  deadlines: Record<string, string> = {},
): TgtRow[] {
  const rows: TgtRow[] = [];
  const rates = stats?.rates ?? {};
  // The tracker has been fetched and returned at least one field — so a stat
  // that's still absent is one this tracker's API genuinely can't report,
  // not merely one that hasn't arrived yet.
  const hasData = !!stats && Object.keys(stats.fields ?? {}).length > 0;
  const push = (label: string, cur: string, tgt: string, pct: number, color: string, eta?: Partial<TgtRow>) =>
    rows.push({ label, cur, tgt, pct: Math.min(100, Math.max(0, pct)), color, ...eta });
  const miss = (label: string, tgt: string, color: string) => {
    if (hasData) push(label, '—', tgt, 0, color, { unavailable: true });
    else if (alwaysShow) push(label, '—', tgt, 0, color);
  };

  // Projected ETA (days) for a rate-based target: remaining / per-day rate.
  // Returns undefined when already met, no rate, non-positive rate, or NaN.
  const rateEta = (curV: number, tgtV: number, rate: number | undefined): number | undefined => {
    if (rate == null || !(rate > 0)) return undefined;
    const remain = tgtV - curV;
    if (!(remain > 0)) return undefined;
    const d = remain / rate;
    return isFinite(d) && d > 0 ? d : undefined;
  };

  // Goal pacing (dated rows only) — computed once per key when the row
  // carries a deadline; folded into the eta partial alongside etaDays so
  // push() stores everything renderTargetRow/the detail pacing line need.
  // Never called for "days" (account age) — its own block below never wires
  // this in, matching the rule that age can't take a deadline.
  const pacingFor = (key: string, tgtRaw: string): Partial<TgtRow> => {
    const deadline = deadlines[key];
    if (!deadline) return {};
    const p = computeGoalPacing(key, tgtRaw, deadline, stats?.fields, rates);
    return p ? { key, pacing: p, deadline } : {};
  };

  // Uploaded
  if (targets['uploaded']) {
    const cur = strOf(stats, 'uploaded');
    const curV = parseSize(cur), tgtV = parseSize(targets['uploaded']);
    if (cur && curV !== null && tgtV && tgtV > 0) push('Uploaded', cur, targets['uploaded'], (curV / tgtV) * 100, 'green', { etaDays: rateEta(curV, tgtV, rates['uploaded']), ...pacingFor('uploaded', targets['uploaded']) });
    else miss('Uploaded', targets['uploaded'], 'green');
  }
  // Downloaded
  if (targets['downloaded']) {
    const cur = strOf(stats, 'downloaded');
    const curV = parseSize(cur), tgtV = parseSize(targets['downloaded']);
    if (cur && curV !== null && tgtV && tgtV > 0) push('Downloaded', cur, targets['downloaded'], (curV / tgtV) * 100, 'purple', { etaDays: rateEta(curV, tgtV, rates['downloaded']), ...pacingFor('downloaded', targets['downloaded']) });
    else miss('Downloaded', targets['downloaded'], 'purple');
  }
  // Ratio — no projection (can't sensibly project a ratio); pacingFor covers
  // the ratio_info needed-upload figure instead.
  if (targets['ratio']) {
    const curV = parseRatio(strOf(stats, 'ratio')), tgtV = parseFloat(targets['ratio']);
    if (strOf(stats, 'ratio') && !isNaN(curV) && !isNaN(tgtV) && tgtV > 0) push('Ratio', fmtRatio(curV), fmtRatio(tgtV), (curV / tgtV) * 100, ratioColorFor(curV, minRatio), pacingFor('ratio', targets['ratio']));
    else miss('Ratio', fmtRatio(parseFloat(targets['ratio'])), 'amber');
  }
  // Seed size
  if (targets['seed_size']) {
    const cur = strOf(stats, 'seed_size');
    const curV = parseSize(cur), tgtV = parseSize(targets['seed_size']);
    if (cur && curV !== null && tgtV && tgtV > 0) push('Seed Size', cur, targets['seed_size'], (curV / tgtV) * 100, 'teal', { etaDays: rateEta(curV, tgtV, rates['seed_size']), ...pacingFor('seed_size', targets['seed_size']) });
    else miss('Seed Size', targets['seed_size'], 'teal');
  }
  // Total uploads (uploads_approved from merged fields)
  if (targets['total_uploads']) {
    const curStr = strOf(stats, 'uploads_approved');
    const curV = parseInt(curStr.replace(/,/g, ''), 10);
    const tgtV = parseInt(targets['total_uploads'].replace(/,/g, ''), 10);
    if (curStr && !isNaN(curV) && !isNaN(tgtV) && tgtV > 0) push('Total Uploads', curStr, targets['total_uploads'], (curV / tgtV) * 100, 'orange', pacingFor('total_uploads', targets['total_uploads']));
    else miss('Total Uploads', targets['total_uploads'], 'orange');
  }
  // Account age
  const joinDate = strOf(stats, 'join_date');
  if (targets['days']) {
    const tgtDays = parseAgeDays(targets['days']) ?? 0;
    const tgtDLabel = (() => {
      const Y = Math.floor(tgtDays / 365), r1 = tgtDays % 365;
      const M = Math.floor(r1 / 30), r2 = r1 % 30;
      const W = Math.floor(r2 / 7), D = r2 % 7;
      return ([[Y,'Y'],[M,'M'],[W,'W'],[D,'D']] as [number,string][])
        .filter(([v]) => v).map(([v,u]) => `${v}${u}`).join(' ') || '0D';
    })();
    if (joinDate && tgtDays > 0) {
      const curDays = memberDays(joinDate) ?? 0;
      // Account age is an exact countdown (1 day/day), not a projection.
      const remainDays = tgtDays - curDays;
      push('Account Age', memberDur(joinDate), tgtDLabel, (curDays / tgtDays) * 100, 'blue',
        remainDays > 0 ? { etaDays: remainDays, etaExact: true } : undefined);
    } else if (tgtDays > 0) {
      miss('Account Age', tgtDLabel, 'blue');
    }
  }
  // Avg seed time — approximate projection (~1 day of seedtime per real day)
  if (targets['avg_seed']) {
    const curV = parseSeedTime(strOf(stats, 'avg_seed_time')), tgtV = parseSeedTime(targets['avg_seed']);
    if (curV !== null && tgtV && tgtV > 0) {
      const remainSec = tgtV - curV;
      push('Avg Seed Time', fmtSeedTime(curV), fmtSeedTime(tgtV), (curV / tgtV) * 100, 'pink',
        { ...(remainSec > 0 ? { etaDays: remainSec / 86400, etaApprox: true } : {}), ...pacingFor('avg_seed', targets['avg_seed']) });
    }
    else if (tgtV && tgtV > 0) miss('Avg Seed Time', fmtSeedTime(tgtV), 'pink');
  }
  // Bonus Points — all tracker types
  if (targets['bonus_points']) {
    const curStr = strOf(stats, 'bonus_points');
    const curV = parseInt(curStr.replace(/,/g, ''), 10);
    const tgtV = parseInt(targets['bonus_points'].replace(/,/g, ''), 10);
    if (curStr && !isNaN(curV) && !isNaN(tgtV) && tgtV > 0) push('Bonus Points', curStr, targets['bonus_points'], (curV / tgtV) * 100, 'orange', { etaDays: rateEta(curV, tgtV, rates['bonus_points']), ...pacingFor('bonus_points', targets['bonus_points']) });
    else miss('Bonus Points', targets['bonus_points'], 'orange');
  }
  // Adoptions — Gazelle adoption program (count; no rate to project)
  if (targets['adoptions']) {
    const curStr = strOf(stats, 'adoptions');
    const curV = parseInt(curStr.replace(/,/g, ''), 10);
    const tgtV = parseInt(targets['adoptions'].replace(/,/g, ''), 10);
    if (curStr && !isNaN(curV) && !isNaN(tgtV) && tgtV > 0) push('Adoptions', curStr, targets['adoptions'], (curV / tgtV) * 100, 'teal', pacingFor('adoptions', targets['adoptions']));
    else miss('Adoptions', targets['adoptions'], 'teal');
  }
  // Snatched — Gazelle
  if (targets['snatched']) {
    const curStr = strOf(stats, 'snatched');
    const curV = parseInt(curStr.replace(/,/g, ''), 10);
    const tgtV = parseInt(targets['snatched'].replace(/,/g, ''), 10);
    if (curStr && !isNaN(curV) && !isNaN(tgtV) && tgtV > 0) push('Snatched', curStr, targets['snatched'], (curV / tgtV) * 100, 'amber', pacingFor('snatched', targets['snatched']));
    else miss('Snatched', targets['snatched'], 'amber');
  }

  // Any other key targets a merged stat field directly (set from the manual
  // target builder — e.g. fl_tokens, upload_snatches, HUNO's *_seeds).
  // Sizes compare as sizes when both parse; everything else numerically.
  for (const [key, tgt] of Object.entries(targets)) {
    if (KNOWN_TARGET_KEYS.has(key)) continue;
    const label = fieldLabel(key);
    const curStr = strOf(stats, key);
    let curV = parseSize(curStr), tgtV = parseSize(tgt);
    if (curV === null || tgtV === null) {
      const c = parseFloat(curStr.replace(/,/g, ''));
      const g = parseFloat(tgt.replace(/,/g, ''));
      curV = isNaN(c) ? null : c;
      tgtV = isNaN(g) ? null : g;
    }
    if (curStr && curV !== null && tgtV !== null && tgtV > 0) {
      push(label, curStr, tgt, (curV / tgtV) * 100, 'teal', { etaDays: rateEta(curV, tgtV, rates[key]), ...pacingFor(key, tgt) });
    } else {
      miss(label, tgt, 'teal');
    }
  }

  return rows;
}

/** Target keys with dedicated rendering above — everything else is generic. */
const KNOWN_TARGET_KEYS = new Set([
  'uploaded', 'downloaded', 'ratio', 'seed_size', 'total_uploads',
  'days', 'avg_seed', 'bonus_points', 'adoptions', 'snatched',
]);

/** Amber ETA chip for a target row — gated by show_target_etas. Account age is
 *  exact ("in X"); rate-projected stats use "≈ X"; approximate ones add a title. */
function targetEtaChip(r: TgtRow): string {
  if (appSettings.show_target_etas === false) return '';
  if (r.etaDays == null || !(r.etaDays > 0)) return '';
  const prefix = r.etaExact ? 'in' : '≈';
  const title = r.etaApprox ? ' title="Approximate — assumes continuous seeding"' : '';
  return ` <span class="target-eta"${title}>${prefix} ${esc(fmtEtaDays(r.etaDays))}</span>`;
}

/** Eye-with-a-slash mark for a requirement this tracker's API can't report. */
const UNAVAIL_ICON =
  `<svg class="target-unavail-icon" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19m-6.72-1.07a3 3 0 1 1-4.24-4.24"/><path d="M1 1l22 22"/></svg>`;

const UNAVAIL_TIP = 'This tracker’s API doesn’t report this stat, so progress can’t be tracked — the requirement still applies.';

/** Compact on-track/behind/overdue/neutral chip after a dated row's ETA
 *  chip — gated by show_goal_chips (default on). "done" rows render nothing
 *  (the bar already reads met); no_rate/ratio_info fall back to a neutral
 *  "by <date>" chip since neither has a verdict to show. */
function goalChip(r: TgtRow, settings: AppSettings): string {
  if (settings.show_goal_chips === false) return '';
  const p = r.pacing;
  if (!p || p.state === 'done') return '';
  const due = r.deadline ? fmtDueDate(r.deadline) : '';
  const rateBits = () => `needs ${fmtGoalRate(r.key ?? '', p.required)} · doing ${fmtGoalRate(r.key ?? '', p.rate)}${due ? ` · due ${due}` : ''}`;

  switch (p.state) {
    case 'on_track':
      return ` <span class="goal-chip goal-chip--ontrack" title="${esc(rateBits())}">on track</span>`;
    case 'behind':
      return ` <span class="goal-chip goal-chip--behind" title="${esc(rateBits())}">behind</span>`;
    case 'overdue':
      return ` <span class="goal-chip goal-chip--overdue" title="${esc(due ? `was due ${due}` : 'overdue')}">overdue</span>`;
    case 'no_rate':
    case 'ratio_info': {
      if (!due) return '';
      const tip = p.state === 'ratio_info' && p.neededUploadGiB > 0
        ? `needs +${fmtGib(p.neededUploadGiB)} upload · due ${due}`
        : `due ${due}`;
      return ` <span class="goal-chip goal-chip--neutral" title="${esc(tip)}">by ${esc(due)}</span>`;
    }
    default:
      return '';
  }
}

/** Detail view's full pacing line under a dated row — gated by
 *  show_goal_pacing (default on). Spells out the same states as the compact
 *  chip: "needs X/day · doing Y/day · on track (≈ date)", "behind — needs
 *  X/day, doing Y/day", "overdue — was due date", or the ratio row's "needs
 *  +N GiB upload · by date". */
function goalPacingLine(r: TgtRow, settings: AppSettings): string {
  if (settings.show_goal_pacing === false) return '';
  const p = r.pacing;
  if (!p || p.state === 'done') return '';
  const due = r.deadline ? fmtDueDate(r.deadline) : '';
  let text = '';
  switch (p.state) {
    case 'on_track':
      text = `needs ${fmtGoalRate(r.key ?? '', p.required)} · doing ${fmtGoalRate(r.key ?? '', p.rate)} · on track${due ? ` (≈ ${due})` : ''}`;
      break;
    case 'behind':
      text = `behind — needs ${fmtGoalRate(r.key ?? '', p.required)}, doing ${fmtGoalRate(r.key ?? '', p.rate)}`;
      break;
    case 'overdue':
      text = due ? `overdue — was due ${due}` : 'overdue';
      break;
    case 'no_rate':
      text = `needs ${fmtGoalRate(r.key ?? '', p.required)}${due ? ` · by ${due}` : ''}`;
      break;
    case 'ratio_info':
      if (p.neededUploadGiB > 0) text = `needs +${fmtGib(p.neededUploadGiB)} upload${due ? ` · by ${due}` : ''}`;
      else if (due) text = `by ${due}`;
      break;
  }
  return text ? `<div class="target-goal-line">${esc(text)}</div>` : '';
}

const renderTargetRow = (r: TgtRow, settings: AppSettings = appSettings, mode: 'chip' | 'full' = 'chip'): string => {
  if (r.unavailable) return `<div class="target-row target-row--unavail" title="${esc(UNAVAIL_TIP)}">
  <div class="target-header">
    <span class="target-lbl">${esc(r.label)}</span>
    <span class="target-vals target-vals--unavail">${UNAVAIL_ICON}<span class="target-unavail-txt">Not available</span> <span class="tgt">/ ${esc(r.tgt)}</span></span>
  </div>
  <div class="progress-track progress-track--unavail"></div>
</div>`;
  return `<div class="target-row">
  <div class="target-header">
    <span class="target-lbl">${esc(r.label)}</span>
    <span class="target-vals">${esc(r.cur)} <span class="tgt">/ ${esc(r.tgt)}</span>${targetEtaChip(r)}${mode === 'chip' ? goalChip(r, settings) : ''}</span>
  </div>
  <div class="progress-track"><div class="progress-fill ${r.color}" style="width:${r.pct.toFixed(1)}%"></div></div>
  ${mode === 'full' ? goalPacingLine(r, settings) : ''}
</div>`;
};

/** Build the progress-bar targets section (shared by grid cards and table).
 *  mode drives goal pacing's two independent Display toggles: 'chip'
 *  (default — cards/table) shows the compact on-track/behind chip after the
 *  ETA chip; 'full' (Tracker Detail) shows the muted pacing line instead. */
export function buildTargets(
  tracker: Tracker,
  stats: TrackerStatsResponse | undefined,
  settings: AppSettings,
  groupDefs: TrackerGroupMap | undefined,
  tKey: string,
  mode: 'chip' | 'full' = 'chip',
): string {
  const rows = targetRowsFor(tracker.targets ?? {}, stats, tracker.min_ratio, false, tracker.target_deadlines ?? {});

  const defGroups = (groupDefs && tKey) ? (groupDefs[tKey] ?? []) : [];
  const hasGroups = defGroups.length > 0;

  // any_of alternatives of the target group — rendered dynamically from the
  // groupDefs data (NEVER stored in the targets map, which holds base
  // requirements only). Base must be met PLUS at least one alternative.
  const targetGroupDef = tracker.target_group && groupDefs ? findGroupDef(groupDefs, tKey, tracker.target_group) : undefined;

  // min_counts requirements (e.g. HUNO's seed-time brackets) — also rendered
  // live from the target group's def, in def order, never stored in the
  // targets map. Folded into `rows` so the promotion-ETA headline and
  // "Eligible now" state see them as base requirements.
  const hasData = !!stats && Object.keys(stats.fields ?? {}).length > 0;
  for (const mc of targetGroupDef?.requirements?.min_counts ?? []) {
    if (!mc.field || !(mc.count > 0)) continue;
    const label = mc.label || fieldLabel(mc.field);
    const curStr = strOf(stats, mc.field);
    const curV = parseInt(curStr.replace(/,/g, ''), 10);
    if (curStr && !isNaN(curV)) {
      rows.push({ label, cur: curStr, tgt: String(mc.count), pct: Math.min(100, Math.max(0, (curV / mc.count) * 100)), color: 'teal' });
    } else {
      // Present-but-empty: show as untrackable when the tracker returns data
      // yet omits this field, otherwise a plain pending "—".
      rows.push({ label, cur: '—', tgt: String(mc.count), pct: 0, color: 'teal', unavailable: hasData });
    }
  }
  const anyOf = targetGroupDef?.requirements?.any_of ?? [];
  const anyOfHtml = anyOf.length
    ? `<div class="anyof-wrap">
        <div class="anyof-label">One of</div>
        ${anyOf.map(req =>
          `<div class="anyof-alt">${targetRowsFor(groupRequirementsToTargets(req), stats, tracker.min_ratio, true).map(r => renderTargetRow(r, settings, mode)).join('')}</div>`
        ).join('<div class="anyof-or">or</div>')}
      </div>`
    : '';
  const requirementNoteHtml = targetGroupDef?.requirements?.note
    ? `<div class="target-group-desc">${esc(targetGroupDef.requirements.note)}</div>`
    : '';

  if (!rows.length && !anyOfHtml && !requirementNoteHtml && !hasGroups) return '';

  // Quick-edit pencil — opens the dashboard targets popover. Hidden for
  // trackers without def groups (nothing to load from).
  const pencil = hasGroups
    ? `<button type="button" class="btn btn-ghost btn-icon btn-sm targets-edit-btn" title="Edit targets"
        onclick="event.stopPropagation();openTargetsPopover('${esc(tracker.id)}', this)">
        <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
          <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
          <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
        </svg>
      </button>`
    : '';

  // Group badge shown under "TARGETS" title when targets were loaded from a group
  const targetPerksHtml = buildPerkIcons(targetGroupDef, settings);
  const groupBadgeHtml = tracker.target_group
    ? `<div class="targets-group-badge" style="display:flex;align-items:center;gap:6px;flex-wrap:wrap">${renderGroupBadge(targetGroupDef, tracker.target_group, settings)}${targetPerksHtml}</div>`
    : '';

  // Promotion ETA headline — the binding (slowest) base requirement decides
  // when the next group is reachable. "+" means at least this (some unmet
  // requirement can't be projected, e.g. ratio). any_of alternatives count:
  // "Eligible now" requires the base AND at least one fully-met alternative.
  const promoEtaHtml = (() => {
    if (!tracker.target_group || appSettings.show_target_etas === false || !rows.length) return '';
    let maxEta = 0;
    let unmetUnprojectable = false;
    let anyUnmet = false;
    for (const r of rows) {
      if (r.unavailable) {
        // Untrackable requirement — many trackers omit zero-valued stats
        // entirely, so the only safe assumption is zero: it counts as UNMET
        // (blocks "Eligible now") but can never be projected.
        anyUnmet = true;
        unmetUnprojectable = true;
        continue;
      }
      if (r.pct >= 100) continue; // met
      anyUnmet = true;
      if (r.etaDays != null && r.etaDays > 0) maxEta = Math.max(maxEta, r.etaDays);
      else unmetUnprojectable = true; // unmet but no projectable eta (e.g. ratio)
    }
    // Alternatives: eligible only when ONE is fully met; otherwise the
    // CLOSEST alternative (smallest projected eta) extends the headline.
    let altMet = anyOf.length === 0;
    let bestAltEta = Infinity;
    let bestAltUnprojectable = false;
    for (const req of anyOf) {
      // Untrackable rows stay in: they carry pct 0 and no eta, so an
      // alternative containing one reads as unmet-and-unprojectable —
      // assume-zero, same as the base rows above.
      const aRows = targetRowsFor(groupRequirementsToTargets(req), stats, tracker.min_ratio, true);
      const unmet = aRows.filter(r => r.pct < 100);
      if (aRows.length && !unmet.length) { altMet = true; break; }
      let eta = 0, unproj = !aRows.length;
      for (const r of unmet) {
        if (r.etaDays != null && r.etaDays > 0) eta = Math.max(eta, r.etaDays);
        else unproj = true;
      }
      const key = eta > 0 ? eta : Infinity;
      if (key < bestAltEta || (bestAltEta === Infinity && !unproj)) {
        bestAltEta = key;
        bestAltUnprojectable = unproj;
      }
    }
    if (!anyUnmet && altMet) return `<span class="targets-eta targets-eta--ready">Eligible now</span>`;
    if (!altMet) {
      if (bestAltEta !== Infinity) maxEta = Math.max(maxEta, bestAltEta);
      else unmetUnprojectable = true; // no alternative can be projected
      if (bestAltUnprojectable) unmetUnprojectable = true;
    }
    if (maxEta <= 0) return ''; // nothing projectable → no useful headline
    return `<span class="targets-eta">Next group ≈ ${esc(fmtEtaDays(maxEta))}${unmetUnprojectable ? '+' : ''}</span>`;
  })();

  const emptyHint = (() => {
    if (rows.length || anyOfHtml) return '';
    // A selected class with no stat requirements (uploader/staff/donor
    // classes, e.g. InfinityHD's "Runesmith" = their uploaders): show the
    // def's description so it's clear why there's nothing to work toward.
    if (tracker.target_group) {
      const desc = targetGroupDef?.requirements?.description ?? '';
      return `<div style="font-size:11px;color:var(--text3);font-style:italic">
        ${desc ? `${esc(desc)}<br>` : ''}No user reachable targets</div>`;
    }
    return `<div style="font-size:11px;color:var(--text3);font-style:italic">No targets set</div>`;
  })();

  return `<div class="exp-targets">
    <div class="exp-section-title" style="margin-bottom:${(groupBadgeHtml || promoEtaHtml) ? '4px' : '8px'}">Targets${pencil}${promoEtaHtml}</div>
    ${groupBadgeHtml}
    ${rows.map(r => renderTargetRow(r, settings, mode)).join('')}
    ${anyOfHtml}
    ${requirementNoteHtml}
    ${emptyHint}
  </div>`;
}

/** Generate status dot HTML.
 *  STALE DATA RULE: an ok=false response with stored fields gets a dimmed
 *  offline dot (with error tooltip) — the stats themselves stay rendered. */
export function makeSdot(tracker: Tracker, stats: TrackerStatsResponse | undefined): string {
  if ((!tracker.has_key && tracker.type !== 'test') || !tracker.enabled) return `<div class="sdot amber"></div>`;
  if (!stats) return `<div class="sdot amber pulse"></div>`;
  if (!stats.ok) {
    const tip = errLabel(stats.error_kind || stats.error || 'error');
    const hasFields = Object.keys(stats.fields ?? {}).length > 0;
    return `<div class="sdot red${hasFields ? ' sdot--offline' : ''}" title="${esc(tip)}"></div>`;
  }
  return `<div class="sdot green"></div>`;
}

function skelStats(): string {
  return `<div class="card-user">
    <div class="skeleton" style="width:120px;height:15px"></div>
    <div class="skeleton" style="width:60px;height:15px"></div>
  </div>
  <div class="stats-grid">${Array(12).fill('<div class="stat-item"><div class="skeleton" style="height:30px"></div></div>').join('')}</div>`;
}

function attachDragEvents(el: HTMLElement, id: string, onReorder: ReorderFn): void {
  // Only start dragging when the pointer is down on the drag handle
  el.draggable = false;
  const handle = el.querySelector<HTMLElement>('.drag-handle');
  if (handle) {
    handle.addEventListener('mousedown', () => { el.draggable = true; });
    handle.addEventListener('mouseleave', () => { if (!dragSrcId) el.draggable = false; });
  }

  el.addEventListener('dragstart', e => {
    if (!el.draggable) { e.preventDefault(); return; }
    dragSrcId = id;
    el.classList.add('dragging');
    e.dataTransfer!.effectAllowed = 'move';
    e.dataTransfer!.setData('text/plain', id);
  });
  el.addEventListener('dragend', () => {
    el.draggable = false;
    el.classList.remove('dragging');
    document.querySelectorAll('.tracker-card').forEach(c => c.classList.remove('drag-over'));
    dragSrcId = null;
  });
  el.addEventListener('dragover', e => {
    e.preventDefault();
    e.dataTransfer!.dropEffect = 'move';
    if (id !== dragSrcId) {
      document.querySelectorAll('.tracker-card').forEach(c => c.classList.remove('drag-over'));
      el.classList.add('drag-over');
    }
  });
  el.addEventListener('dragleave', () => el.classList.remove('drag-over'));
  el.addEventListener('drop', e => {
    e.preventDefault();
    el.classList.remove('drag-over');
    if (!dragSrcId || dragSrcId === id) return;
    onReorder(dragSrcId, id);
    dragSrcId = null;
  });
}
