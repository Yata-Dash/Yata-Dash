// views/table.ts — sortable tracker table view (reads merged stats fields)
import type { AppSettings, ColDef, ColPref, HistoryPoint, Tracker, TrackerGroupMap, TrackerStatsResponse } from '../types';
import { esc, errLabel, fmtRatio, fmtSeedTime, fmtTrackerName, parseRatio, rateTip, ratioColor, ratioColorFor, srcDot } from '../utils/format';
import { getFaviconUrl, memberDur, parseSeedTime } from '../utils/parse';
import { getSortedTrackers } from '../utils/sort';
import { fieldOf, getVisibleCols, numOf, scrapeStatus, strOf } from '../state';
import { findGroupDef, renderGroupBadge, renderUsername } from '../utils/group';
import { renderSparkline } from '../components/sparkline';
import { buildStatsPanel } from '../components/profile';
import { buildTargets, fmtDateTime, makeSdot } from './grid';
import { trackerSeries } from '../utils/history';
import { eventGlobeSvg } from '../utils/icons';

interface TableCallbacks {
  onSort: (col: string) => void;
  onToggleRow: (id: string) => void;
}

export function renderTable(
  trackers: Tracker[],
  statsCache: Record<string, TrackerStatsResponse>,
  historyData: HistoryPoint[],
  settings: AppSettings,
  expandedRows: Set<string>,
  sortCol: string,
  sortDir: 'asc' | 'desc',
  colPrefs: ColPref[],
  callbacks: TableCallbacks,
  groupDefs: TrackerGroupMap = {},
): void {
  const cols = getVisibleCols(colPrefs);

  // Rebuild header
  const head = document.getElementById('table-head');
  if (head) {
    head.innerHTML = buildTableHeader(cols, sortCol, sortDir);
    head.querySelectorAll<HTMLElement>('th.sortable').forEach(th => {
      th.addEventListener('click', () => callbacks.onSort(th.dataset['col']!));
    });
  }

  const tbody = document.getElementById('tracker-tbody');
  if (!tbody) return;

  // Disabled trackers are hidden from the dashboard (they stay listed in
  // Settings → Trackers); the "N / M active" line follows the visible set.
  const visible = trackers.filter(t => t.enabled !== false);
  if (trackers.length && !visible.length) {
    tbody.innerHTML = `<tr class="table-empty-row"><td colspan="${2 + cols.length + 1}"
      style="color:var(--text3)">All trackers are disabled — re-enable them in Settings → Trackers.</td></tr>`;
    const statusEl = document.getElementById('t-sum-status');
    if (statusEl) statusEl.textContent = `0 / 0 active`;
    return;
  }
  const sorted = getSortedTrackers(visible, sortCol, sortDir, statsCache);

  if (!sorted.length) {
    tbody.innerHTML = `<tr class="table-empty-row"><td colspan="${2 + cols.length + 1}">
      <svg width="36" height="36" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1"
        style="margin:0 auto 10px;display:block;opacity:.3">
        <circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/>
      </svg>No trackers configured.<br>
      <button class="btn btn-primary btn-sm" style="margin-top:12px" onclick="openAddModal()">+ Add your first tracker</button>
      <div style="font-size:12px;margin-top:10px;color:var(--text3)">…or import from <strong>Prowlarr / Jackett</strong> in Settings → Trackers.</div>
      </td></tr>`;
    return;
  }

  let active = 0;
  tbody.innerHTML = sorted.map(t => {
    if (statsCache[t.id]?.ok) active++;
    return buildTableRow(t, statsCache, historyData, settings, expandedRows, cols, groupDefs);
  }).join('');

  const statusEl = document.getElementById('t-sum-status');
  if (statusEl) statusEl.textContent = `${active} / ${sorted.length} active`;

  // Render sparklines for expanded rows
  sorted.filter(t => expandedRows.has(t.id)).forEach(t => {
    const up = trackerSeries(historyData, t.id, 'uploaded');
    const dn = trackerSeries(historyData, t.id, 'downloaded');
    if (up.length > 1 || dn.length > 1) {
      const sid = t.id.slice(0, 8);
      renderSparkline(`spark-t-up-${sid}`, up, '--green');
      renderSparkline(`spark-t-dn-${sid}`, dn, '--purple');
    }
  });
}

function buildTableHeader(cols: ColDef[], sortCol: string, sortDir: string): string {
  const sortIcon = () => `<span class="sort-icon"><span class="up"></span><span class="dn"></span></span>`;
  let html = `<tr><th style="min-width:28px"></th><th></th>`;
  cols.forEach(col => {
    const sorted = sortCol === col.key;
    const cls = ['sortable', sorted ? 'sorted' : '', sorted ? `sort-${sortDir}` : '', col.center ? 'c-center' : '']
      .filter(Boolean).join(' ');
    html += `<th class="${cls}" data-col="${col.key}" style="min-width:${col.minWidth}px">${col.label}${sortIcon()}</th>`;
  });
  html += `<th style="text-align:right;padding-right:6px;min-width:36px">
    <button class="btn btn-ghost btn-icon btn-sm" title="Customize columns" onclick="openColCustomizer()" style="font-size:10px;padding:4px 6px">
      <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
        <line x1="4" y1="6" x2="16" y2="6"/><line x1="8" y1="12" x2="20" y2="12"/><line x1="4" y1="18" x2="16" y2="18"/>
        <circle cx="19" cy="6" r="3"/><circle cx="3" cy="12" r="3"/><circle cx="19" cy="18" r="3"/>
      </svg>
    </button>
  </th>`;

  // Set min-width on the table itself: sum of all column min-widths + fixed chrome
  // Fixed: 28px (chevron) + ~8px (status dot) + 36px (gear) + 40px padding buffer
  const FIXED_PX = 112;
  const tableMinW = cols.reduce((sum, c) => sum + c.minWidth, FIXED_PX);
  const table = document.querySelector<HTMLElement>('.tracker-table');
  if (table) table.style.minWidth = `${tableMinW}px`;

  return html + '</tr>';
}

function buildTableRow(
  t: Tracker,
  statsCache: Record<string, TrackerStatsResponse>,
  historyData: HistoryPoint[],
  settings: AppSettings,
  expandedRows: Set<string>,
  cols: ColDef[],
  groupDefs: TrackerGroupMap = {},
): string {
  const s = statsCache[t.id];
  const hnr = numOf(s, 'hit_and_runs') ?? 0;
  const isExp = expandedRows.has(t.id);
  const colspan = 2 + cols.length + 1;

  // def_key comes from the server-side defs registry (URL + alias match) —
  // the only source of tracker identity. "" = manual tracker, no group data.
  const tKey = t.def_key;
  const userGroupDef = findGroupDef(groupDefs, tKey, strOf(s, 'group'));

  let cells = '';
  cols.forEach(col => {
    cells += buildCell(col.key, t, s, settings, userGroupDef);
  });

  const hnrHighlight = settings.highlight_hnr !== false;
  const mainTr = `<tr class="tr-main${hnr >= 1 && hnrHighlight ? ' hnr-row' : ''}" id="trow-${t.id}" onclick="toggleRow('${t.id}')">
    <td style="text-align:center;padding-left:10px">
      <svg class="expand-chev ${isExp ? 'open' : ''}" width="14" height="14" viewBox="0 0 24 24"
        fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round">
        <polyline points="9 18 15 12 9 6"/>
      </svg>
    </td>
    <td style="text-align:center">${makeSdot(t, s)}</td>
    ${cells}
    <td style="text-align:right;padding-right:6px"></td>
  </tr>`;

  const expTr = `<tr class="tr-expanded ${isExp ? 'visible' : ''}" id="trow-exp-${t.id}">
    <td colspan="${colspan}"><div class="tr-expanded-inner">${isExp ? buildExpanded(t, s, historyData, settings, groupDefs, userGroupDef, tKey) : ''}</div></td>
  </tr>`;

  return mainTr + expTr;
}

// STALE DATA RULE: cells render whatever fields exist regardless of resp.ok.
// A missing field shows '—'; an error never blanks previously stored values.
function buildCell(
  key: string,
  t: Tracker,
  s: TrackerStatsResponse | undefined,
  settings: AppSettings,
  userGroupDef?: ReturnType<typeof findGroupDef>,
): string {
  const dash = '<span style="color:var(--text3)">—</span>';
  const dot = (k: string) => srcDot(fieldOf(s, k), settings);
  // Per-day trend rollover as a title attribute ("≈ 245.3 GiB per day").
  const rtip = (f: string) => {
    const tip = rateTip(s?.rates, f, settings);
    return tip ? ` title="${esc(tip)}"` : '';
  };

  switch (key) {
    case 'name': {
      const activeEvent = strOf(s, 'active_event');
      // Unread mail/notification flags — same at-a-glance icons as the grid
      // cards, sitting next to the event beacon. Each follows its own
      // Display toggle; only rendered when the flag is actually "true".
      const unreadFlags =
        (settings.show_unread_mail !== false && strOf(s, 'unread_mail') === 'true'
          ? `<span class="unread-flag" title="Unread mail on ${esc(t.name)} (as of the last scrape) — check your inbox"><i class="fas fa-envelope"></i></span>` : '') +
        (settings.show_unread_notifications !== false && strOf(s, 'unread_notifications') === 'true'
          ? `<span class="unread-flag" title="Unread notifications on ${esc(t.name)} (as of the last scrape)"><i class="fas fa-bell"></i></span>` : '');
      return `<td>
      <div class="td-tracker-wrap">
        ${settings.show_favicons && t.url ? `<img class="tracker-favicon" src="${getFaviconUrl(t.url)}" alt="" onerror="this.style.display='none'">` : ''}
        <span class="td-tracker-name"><span class="td-name-text tracker-name-link" title="Open tracker detail" onclick="event.stopPropagation();openTrackerDetail('${t.id}')">${esc(fmtTrackerName(t.name, t.abbr, settings.tracker_name_mode))}</span>${t.type === 'test' ? '<span class="mock-badge">TEST</span>' : ''}${activeEvent ? `<span class="event-beacon event-beacon-tip">${eventGlobeSvg()}<span class="event-tip">${esc(activeEvent)}</span></span>` : ''}${unreadFlags}</span>
        <a class="td-tracker-url" href="${esc(t.url)}" target="_blank" rel="noopener" onclick="event.stopPropagation()">${esc(t.url)}</a>
      </div></td>`;
    }
    case 'username': {
      const groupName = strOf(s, 'group');
      // Use stats username when available; fall back to tracker.username from config
      // so it displays even when the API is down or stats haven't loaded yet.
      const displayUsername = strOf(s, 'username') || t.username || '';
      const usernameEl = displayUsername
        ? renderUsername(displayUsername, userGroupDef, settings, 'td-username private-blur')
        : dash;

      // Perk icons popover on group badge hover
      const perkPopover = (() => {
        if (!userGroupDef?.perks?.length) return '';
        const iconColor = userGroupDef.style?.color ?? 'var(--text3)';
        const icons = userGroupDef.perks
          .filter(p => p.icon)
          .map(p => `<span class="perk-icon-wrap" data-tip="${esc(p.label)}"><i class="${esc(p.icon)}" style="color:${esc(iconColor)};font-size:11px;opacity:.85"></i><span class="perk-tip">${esc(p.label)}</span></span>`)
          .join('');
        return icons ? `<span class="card-perk-icons td-perk-icons">${icons}</span>` : '';
      })();

      const groupEl = groupName
        ? `<span class="td-group-wrap" style="margin-top:2px">${renderGroupBadge(userGroupDef, groupName, settings, 'badge-group')}${perkPopover}</span>`
        : '';
      return `<td>
        <div class="td-user-wrap">
          ${usernameEl}
          ${groupEl}
        </div></td>`;
    }
    case 'uploaded': {
      const v = strOf(s, 'uploaded');
      return `<td class="td-mono"${rtip('uploaded')} style="color:var(--green)">${v ? esc(v) + dot('uploaded') : dash}</td>`;
    }
    case 'downloaded': {
      const v = strOf(s, 'downloaded');
      return `<td class="td-mono"${rtip('downloaded')} style="color:var(--purple)">${v ? esc(v) + dot('downloaded') : dash}</td>`;
    }
    case 'ratio': {
      const r = parseRatio(strOf(s, 'ratio'));
      const rc = ratioColorFor(isNaN(r) ? 0 : r, t.min_ratio);
      const tip = t.min_ratio && t.min_ratio > 0 ? ` title="Tracker minimum: ${t.min_ratio}"` : '';
      return `<td class="td-mono"${tip} style="color:var(--${rc})">${!isNaN(r) ? fmtRatio(r) + dot('ratio') : dash}</td>`;
    }
    case 'buffer': {
      const v = strOf(s, 'buffer');
      return `<td class="td-mono"${rtip('buffer')} style="color:var(--blue)">${v ? esc(v) + dot('buffer') : dash}</td>`;
    }
    case 'seed_size': {
      const v = strOf(s, 'seed_size');
      return `<td class="td-mono td-center" style="color:var(--teal)">${v ? esc(v) + dot('seed_size') : dash}</td>`;
    }
    case 'avg_seed_time': {
      const ast = parseSeedTime(strOf(s, 'avg_seed_time'));
      return `<td class="td-mono td-center" style="color:var(--pink)">${ast !== null ? fmtSeedTime(ast) + dot('avg_seed_time') : dash}</td>`;
    }
    case 'seeding': {
      const v = numOf(s, 'seeding');
      return `<td class="td-mono td-center" style="color:var(--blue)">${v !== null ? String(v) + dot('seeding') : dash}</td>`;
    }
    case 'leeching': {
      const v = numOf(s, 'leeching');
      return `<td class="td-mono td-center" style="color:var(--amber)">${v !== null ? String(v) + dot('leeching') : dash}</td>`;
    }
    case 'hit_and_runs': {
      const v = numOf(s, 'hit_and_runs');
      const hnr = v ?? 0;
      const color = hnr < 1 ? 'var(--green)' : (settings.highlight_hnr !== false ? 'var(--red)' : 'var(--text3)');
      return `<td class="td-center td-mono" style="color:${color}">
      ${v !== null ? String(hnr) + dot('hit_and_runs') : dash}</td>`;
    }
    case 'account_age': {
      const jd = strOf(s, 'join_date');
      return `<td class="td-mono" style="color:var(--text2)">${jd ? memberDur(jd) + dot('join_date') : dash}</td>`;
    }
    case 'bonus_points': {
      const v = strOf(s, 'bonus_points');
      return `<td class="td-mono td-center"${rtip('bonus_points')} style="color:var(--orange)">${v ? esc(v) + dot('bonus_points') : dash}</td>`;
    }
    case 'snatched': {
      const v = strOf(s, 'snatched');
      return `<td class="td-mono td-center" style="color:var(--amber)">${v ? esc(v) + dot('snatched') : dash}</td>`;
    }
    case 'upload_snatches': {
      const v = strOf(s, 'upload_snatches');
      return `<td class="td-mono td-center" style="color:var(--green)">${v ? esc(v) + dot('upload_snatches') : dash}</td>`;
    }
    case 'real_ratio': {
      const rr = parseRatio(strOf(s, 'real_ratio'));
      const rrc = !isNaN(rr) ? ratioColor(rr) : 'text3';
      return `<td class="td-mono td-center" style="color:var(--${rrc})">${!isNaN(rr) ? fmtRatio(rr) + dot('real_ratio') : dash}</td>`;
    }
    case 'fl_tokens': {
      const v = strOf(s, 'fl_tokens');
      return `<td class="td-mono td-center" style="color:var(--teal)">${v ? esc(v) + dot('fl_tokens') : dash}</td>`;
    }
    case 'invites': {
      const v = strOf(s, 'invites');
      return `<td class="td-mono td-center" style="color:var(--blue)">${v ? esc(v) + dot('invites') : dash}</td>`;
    }
    case 'warnings': {
      const w = numOf(s, 'warnings');
      return `<td class="td-mono td-center" style="color:${w !== null && w > 0 ? 'var(--red)' : 'var(--text3)'}">${w !== null ? String(w) + dot('warnings') : '—'}</td>`;
    }
    case 'total_uploads': {
      const v = strOf(s, 'uploads_approved');
      return `<td class="td-mono td-center"${rtip('uploads_approved')} style="color:var(--green)">${v ? esc(v) + dot('uploads_approved') : dash}</td>`;
    }
    case 'adoptions': {
      const v = strOf(s, 'adoptions');
      return `<td class="td-mono td-center" style="color:var(--teal)">${v ? esc(v) + dot('adoptions') : dash}</td>`;
    }
    case 'reqs_filled': {
      const v = strOf(s, 'requests_filled');
      return `<td class="td-mono td-center" style="color:var(--purple)">${v ? esc(v) + dot('requests_filled') : dash}</td>`;
    }
    default: return '<td>—</td>';
  }
}

function buildExpanded(
  tracker: Tracker,
  stats: TrackerStatsResponse | undefined,
  historyData: HistoryPoint[],
  settings: AppSettings,
  groupDefs: TrackerGroupMap = {},
  userGroupDef?: ReturnType<typeof findGroupDef>,
  tKey = '',
): string {
  const hasFields = !!stats && Object.keys(stats.fields ?? {}).length > 0;

  if (!stats && !tracker.has_key && tracker.type !== 'test') {
    return `<div style="padding:8px 0;color:var(--text3);font-size:13px">No API key. <button class="btn btn-ghost btn-sm" onclick="openEditModal('${tracker.id}')">Configure</button></div>`;
  }
  if (!stats) return `<div style="padding:8px 0;color:var(--text3);font-size:13px">Loading…</div>`;

  // STALE DATA RULE: on error show an inline banner but render everything else
  // from the last stored fields — never hide previously displayed stats.
  const errorBanner = !stats.ok
    ? `<div style="padding:6px 0 10px;display:flex;align-items:center;gap:10px">`
    + `<span style="color:var(--red);font-size:13px">${errLabel(stats.error_kind || stats.error || 'error')}${hasFields ? ' — showing last known stats' : ''}</span>`
    + `<button class="btn btn-ghost btn-sm" onclick="refreshSingle('${tracker.id}')">Retry</button></div>`
    : '';

  const up  = trackerSeries(historyData, tracker.id, 'uploaded');
  const dn  = trackerSeries(historyData, tracker.id, 'downloaded');
  const sid = tracker.id.slice(0, 8);

  const joinDate = strOf(stats, 'join_date');
  const ss = scrapeStatus[tracker.id];
  const infoList = [
    { l: 'Join Date',    v: joinDate || '—' },
    { l: 'Account Age',  v: joinDate ? memberDur(joinDate) : '—' },
    { l: 'Last API Update', v: stats.fetched_at ? fmtDateTime(stats.fetched_at) : '—' },
    { l: 'Last Scrape Update', v: (() => {
        if (ss?.reason === 'opted_out') return 'Operator opted out';
        // Same unified label as the grid footer — cause doesn't matter here.
        if (ss?.reason === 'api_only' || ss?.reason === 'scrape_disabled' || ss?.reason === 'no_scrape_support')
          return 'API only';
        if (ss?.last_scrape_at) return fmtDateTime(ss.last_scrape_at);
        return '—';
      })() },
    { l: 'Next Scrape Update', v: (() => {
        if (!ss || ss.reason === 'opted_out' || ss.reason === 'api_only' || ss.reason === 'scrape_disabled' || ss.reason === 'no_scrape_support')
          return '—';
        if (ss.allowed) return 'Now';
        if (ss.next_allowed_at) return fmtDateTime(ss.next_allowed_at);
        return '—';
      })() },
  ];
  if (ss && ss.effective_max_per_day > 0) {
    infoList.push({ l: 'Scrapes Today', v: `${ss.scrapes_today} / ${ss.effective_max_per_day}` });
  }

  // Unread mail/notification flags (scraped header presence). Only rendered
  // when the flag was actually scraped — unknown is omitted, never "No".
  // Each row follows its own Display toggle.
  const unreadRow = (label: string, field: string, icon: string, enabled: boolean) => {
    if (!enabled) return '';
    const v = strOf(stats, field);
    if (v !== 'true' && v !== 'false') return '';
    return `<div class="exp-stat">
      <span class="exp-stat-label">${label}</span>
      <span class="exp-stat-value">${v === 'true'
        ? `<span class="unread-flag" style="margin-left:0;margin-right:4px"><i class="fas fa-${icon}"></i></span>Yes`
        : 'No'}</span>
    </div>`;
  };
  const unreadRows =
    unreadRow('Unread Mail', 'unread_mail', 'envelope', settings.show_unread_mail !== false)
    + unreadRow('Unread Notifications', 'unread_notifications', 'bell', settings.show_unread_notifications !== false);

  // Profile link — direct link to the user's profile page when known
  const profileLinkRow = tracker.profile_url
    ? `<div class="exp-stat">
        <span class="exp-stat-label">Profile</span>
        <span class="exp-stat-value"><a class="exp-profile-link" href="${esc(tracker.profile_url)}" target="_blank" rel="noopener noreferrer" onclick="event.stopPropagation()">Open profile&nbsp;&#8599;</a></span>
      </div>`
    : '';

  // Missing-credential scrape hints — setup states, muted (not errors)
  const scrapeSetupHint = (ss?.reason === 'no_cookie' || ss?.reason === 'no_username')
    ? `<div class="scrape-setup-hint">${esc(ss.reason === 'no_cookie'
        ? 'Profile scraping off — add your session cookie (Settings → Trackers)'
        : 'Profile scraping off — add your username (Settings → Trackers)')}</div>`
    : '';

  // Perks for current group — icon-tooltip style matching grid view
  const currentGroup = strOf(stats, 'group');
  const perksHtml = (() => {
    if (!userGroupDef?.perks?.length) return '';
    const grouped = settings.group_name_style === 'styled' && userGroupDef.style?.color;
    const groupColor  = grouped ? userGroupDef.style!.color : undefined;
    const iconColor   = groupColor ?? 'var(--text3)';
    const groupIcon   = userGroupDef.style?.icon ?? '';
    const groupIconHtml = groupIcon ? `<i class="${esc(groupIcon)}" style="margin-right:4px;font-size:10px"></i>` : '';
    const icons = userGroupDef.perks
      .filter(p => p.icon)
      .map(p => `<span class="perk-icon-wrap" data-tip="${esc(p.label)}"><i class="${esc(p.icon)}" style="color:${esc(iconColor)};font-size:12px;opacity:.85"></i><span class="perk-tip">${esc(p.label)}</span></span>`)
      .join('');
    if (!icons) return '';
    return `<div style="margin-top:10px">
      <div class="exp-section-title" style="margin-bottom:6px">Perks <span style="font-size:10px;color:${groupColor ?? 'var(--text3)'};font-weight:400">${groupIconHtml}${esc(currentGroup)}</span></div>
      <span class="card-perk-icons" style="flex-wrap:wrap">${icons}</span>
    </div>`;
  })();

  const sparkHTML = (up.length > 1 || dn.length > 1) ? `
    <div style="margin-top:12px">
      <div class="exp-section-title">Upload Trend (48h)</div>
      <div id="spark-t-up-${sid}" style="height:44px;width:100%"></div>
    </div>
    <div style="margin-top:10px">
      <div class="exp-section-title">Download Trend (48h)</div>
      <div id="spark-t-dn-${sid}" style="height:44px;width:100%"></div>
    </div>` : '';

  // Event banner — from merged fields (active_event / active_event_ends_at)
  const evText   = strOf(stats, 'active_event') || null;
  const evEndsAt = numOf(stats, 'active_event_ends_at');
  const endsLabel = evEndsAt ? new Date(evEndsAt * 1000).toLocaleString(undefined, { month: 'short', day: 'numeric', year: 'numeric', hour: 'numeric', minute: '2-digit' }) : '';
  const eventBanner = evText ? `<div class="exp-event-banner">
    ${eventGlobeSvg('flex-shrink:0')}
    <span class="exp-event-text">${esc(evText)}</span>
    ${evEndsAt ? `<span class="exp-event-timer-wrap"><span class="event-countdown" data-ends-at="${evEndsAt}">…</span><span class="exp-event-ends">ends ${esc(endsLabel)}</span></span>` : ''}
  </div>` : '';

  const targetsHtml = buildTargets(tracker, stats, settings, groupDefs, tKey);

  // Rules — display-only reference from the def (account-wide rules page).
  // Always shown in the detail view; the grid card's compact line follows the
  // Display toggle instead.
  const rulesRows: Array<[string, string]> = [];
  if (tracker.min_ratio && tracker.min_ratio > 0) rulesRows.push(['Min Ratio', String(tracker.min_ratio)]);
  if (tracker.min_seed_days_episode && tracker.min_seed_days_episode > 0)
    rulesRows.push(['Episode Seed Time', `${tracker.min_seed_days_episode} day${tracker.min_seed_days_episode === 1 ? '' : 's'}`]);
  if (tracker.min_seed_days_season && tracker.min_seed_days_season > 0)
    rulesRows.push(['Season Seed Time', `${tracker.min_seed_days_season} day${tracker.min_seed_days_season === 1 ? '' : 's'}`]);
  if (!tracker.min_seed_days_episode && !tracker.min_seed_days_season && tracker.min_seed_days && tracker.min_seed_days > 0)
    rulesRows.push(['Min Seed Time', `${tracker.min_seed_days} day${tracker.min_seed_days === 1 ? '' : 's'}`]);
  const rulesHtml = rulesRows.length ? `<div style="margin-top:10px">
      <div class="exp-section-title" title="Reference from the tracker's rules page — full details stay on the tracker">Rules</div>
      <div class="exp-stat-list">${rulesRows.map(([l, v]) => `<div class="exp-stat">
        <span class="exp-stat-label">${esc(l)}</span>
        <span class="exp-stat-value">${esc(v)}</span>
      </div>`).join('')}</div>
    </div>` : '';

  return `${errorBanner}${eventBanner}<div class="expanded-cols">
    <div>
      <div class="exp-section-title">Stats</div>
      ${buildStatsPanel(tracker, stats, settings)}
    </div>
    <div>
      <div class="exp-section-title">Info</div>
      <div class="exp-stat-list">${infoList.map(r => `<div class="exp-stat">
        <span class="exp-stat-label">${esc(r.l)}</span>
        <span class="exp-stat-value">${esc(r.v)}</span>
      </div>`).join('')}${unreadRows}${profileLinkRow}</div>
      ${scrapeSetupHint}
      ${rulesHtml}
      ${perksHtml}
      ${sparkHTML}
    </div>
    ${targetsHtml ? `<div>${targetsHtml}</div>` : ''}
  </div>`;
}
