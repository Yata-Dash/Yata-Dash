// components/modals.ts — Add/Edit/Delete tracker panel + Settings page logic (v2 API)
// The tracker form and settings UI live on the full-page Settings view
// (#settings-page); the historical "modal" naming is kept for the element IDs
// and exported function names so existing wiring keeps working.
import type {
  AppSettings, DefInfo, DefsPayload, GroupDef, GroupRequirements, OptOutEntry, StatsMap,
  Tracker, TrackerPayload, TrackerStatsResponse, TrackerTestOverrides, UpdateStatus,
} from '../types';
import { MASKED_KEY } from '../types';
import * as api from '../api';
import { approvalIcon, approvalWarns } from '../utils/approval';
import { eventGlobeSvg } from '../utils/icons';
import { esc, fieldLabel, fmtAgeDays, fmtBytes, fmtEtaDays, fmtSeedTime, fmtTrackerName } from '../utils/format';
import { parseAgeDays, parseSeedTime } from '../utils/parse';
import { findGroupDef, groupRequirementsToTargets, renderGroupBadge, renderUsername } from '../utils/group';
import { findOptOut, optOutMessage } from '../utils/optout';
import { renderTestDetail } from './trackerTest';
import type { ToastType } from './toast';
import { appSettings, groupDefs, strOf } from '../state';

// ─────────────────────────────────────────────────────────────────────────────
// Interfaces
// ─────────────────────────────────────────────────────────────────────────────

interface ModalDeps {
  loadTrackers: () => Promise<void>;
  refreshSingle: (id: string) => Promise<void>;
  loadScrapeStatus: () => Promise<void>;
  // Re-fetches the settings table's cached test-status pills — needed after
  // a save so a just-promoted pending test result (see modalTestTracker /
  // backend promoteOrDiscardPendingTest) shows up without a manual refresh.
  loadTestStatus: () => Promise<void>;
  toast: (msg: string, type?: ToastType) => void;
}

interface EditDeps extends ModalDeps {
  scrapeProfile: (id: string) => Promise<void>;
  loadSettings: () => Promise<void>;
  renderTable: () => void;
  renderGrid: () => void;
  renderAggCards: () => void;
  updateSummary: () => void;
}

interface SettingsDeps {
  loadSettings: () => Promise<void>;
  loadQUIInstances: () => Promise<void>;
  renderQuiBarsWrapper: () => void;
  refreshQuiStatsWrapper: () => Promise<void>;
  renderQUIInstanceChecklist: () => Promise<void>;
  renderTable: () => void;
  renderGrid: () => void;
  applyQuiInstances: (ids: number[]) => void;
  applyFavicon: (on: boolean) => void;
  applyNameMode: (mode: string) => void;
  applyQuiBarsVisible: (on: boolean) => void;
  applyStatSources: (on: boolean) => void;
  rescheduleTimers: () => void;
  toast: (msg: string, type?: ToastType) => void;
}

// ─────────────────────────────────────────────────────────────────────────────
// Module-level state
// ─────────────────────────────────────────────────────────────────────────────

let _modalEnabled  = true;
let _defsCache: DefsPayload | null = null;
let _selectedDef: DefInfo | null   = null;
let _selectedTypeKey: string       = '';
let _requiredFields: string[]      = [];
// Per-tracker scrape-section state (edit mode).
let _scrapeFloor   = 60;     // effective minimum interval = max(60, def request)
let _scrapeCap     = 0;      // operator daily cap from the def (0 = none)
let _apiOnlyLocked = false;  // def forbids scraping → API-only forced on

// ─────────────────────────────────────────────────────────────────────────────
// Tracker definition helpers
// ─────────────────────────────────────────────────────────────────────────────

async function ensureDefsLoaded(): Promise<DefsPayload> {
  if (_defsCache) return _defsCache;
  const { ok, data } = await api.fetchDefs();
  if (ok) _defsCache = data;
  return _defsCache ?? { types: [], trackers: [], issues: [] };
}

function populateAddSelect(defs: DefsPayload, filter = '') {
  const sel = document.getElementById('modal-add-select') as HTMLSelectElement;
  if (!sel) return;
  const q = filter.trim().toLowerCase();
  const match = (td: DefInfo) =>
    !q || td.name.toLowerCase().includes(q) || (td.abbr ?? '').toLowerCase().includes(q) || td.key.toLowerCase().includes(q);

  const byType = new Map<string, DefInfo[]>();
  for (const td of defs.trackers) {
    if (!match(td)) continue;
    if (!byType.has(td.type)) byType.set(td.type, []);
    byType.get(td.type)!.push(td);
  }
  const typeLabels = new Map(defs.types.map(t => [t.key, t.label]));
  const sortedTypes = [...byType.keys()].sort((a, b) =>
    (typeLabels.get(a) ?? a).localeCompare(typeLabels.get(b) ?? b)
  );

  const matched = defs.trackers.filter(match).length;
  let html = `<option value="">${q ? `— ${matched} match${matched === 1 ? '' : 'es'} —` : '— Choose a tracker —'}</option>`;
  html += `<optgroup label="Manual"><option value="__manual__">Add Manually…</option></optgroup>`;
  for (const typeKey of sortedTypes) {
    const label    = typeLabels.get(typeKey) ?? typeKey;
    const trackers = byType.get(typeKey)!.sort((a, b) => a.name.localeCompare(b.name));
    html += `<optgroup label="${esc(label)}">`;
    for (const td of trackers) {
      const abbr = td.abbr ? ` (${esc(td.abbr)})` : '';
      html += `<option value="${esc(td.key)}">${esc(td.name)}${abbr}</option>`;
    }
    html += `</optgroup>`;
  }
  sel.innerHTML = html;
}

/** Live-filter the tracker picker as the user types in the search box. */
export function onAddTrackerFilter() {
  if (!_defsCache) return;
  const q = (document.getElementById('modal-add-search') as HTMLInputElement | null)?.value ?? '';
  populateAddSelect(_defsCache, q);
}

function populateTypeSelect(defs: DefsPayload) {
  const sel = document.getElementById('modal-add-type') as HTMLSelectElement;
  if (!sel) return;
  const visible = defs.types.sort((a, b) => a.label.localeCompare(b.label));
  sel.innerHTML =
    `<option value="">— Select type —</option>` +
    visible.map(t => `<option value="${esc(t.key)}">${esc(t.label)}</option>`).join('');
}

/** Populate the mock-scenario <select> from GET /api/mock/scenarios. */
function populateScenarioSelect(selected?: string, onChange?: () => void) {
  api.fetchMockScenarios().then(({ ok, data }) => {
    if (!ok || !Array.isArray(data)) return;
    const sel = document.getElementById('modal-mock-scenario') as HTMLSelectElement;
    if (!sel) return;
    sel.innerHTML = data.map(k =>
      `<option value="${esc(k)}" ${k === selected ? 'selected' : ''}>${esc(prettyScenario(k))}</option>`
    ).join('');
    sel.onchange = onChange ?? null;
  });
}

function prettyScenario(key: string): string {
  return key.split(/[_-]/).map(w => w.charAt(0).toUpperCase() + w.slice(1)).join(' ');
}

// ─────────────────────────────────────────────────────────────────────────────
// Required fields (e.g. gazelle requires a username for the API call)
// ─────────────────────────────────────────────────────────────────────────────

const DEFAULT_USERNAME_HINT =
  'Used to scrape extended stats from your profile page. Auto-filled when API key is set.';

/** required_fields of a type from the defs registry. */
function typeRequiredFields(typeKey: string): string[] {
  return _defsCache?.types.find(t => t.key === typeKey)?.required_fields ?? [];
}

const DEFAULT_JOINDATE_LABEL = 'Join Date <span class="opt">(optional)</span>';

/** Mark/unmark fields the selected tracker type requires (username for the
 *  API call; join_date for API-only trackers that report no join date). */
function applyRequiredFieldsUI(required: string[]) {
  _requiredFields = required;

  // Username
  const isUser = required.includes('username');
  const uInput = document.getElementById('modal-username') as HTMLInputElement | null;
  const uLabel = document.getElementById('modal-username-label');
  const uHint  = document.getElementById('modal-username-hint');
  if (uInput) uInput.required = isUser;
  if (isUser) {
    if (uLabel) uLabel.innerHTML = 'Username <span style="color:var(--red)">*</span>';
    if (uHint)  uHint.textContent = 'Required by this tracker type — the API call needs it.';
  } else if (uHint) {
    uHint.textContent = DEFAULT_USERNAME_HINT;
  }

  // Join date — required for API-only types that report none (e.g. MAM).
  const isJoin = required.includes('join_date');
  const jInput = document.getElementById('modal-joindate') as HTMLInputElement | null;
  const jLabel = document.getElementById('modal-joindate-label');
  const jHint  = document.getElementById('modal-joindate-hint');
  if (jInput) jInput.required = isJoin;
  if (jLabel) {
    jLabel.innerHTML = isJoin
      ? 'Join Date <span style="color:var(--red)">*</span>'
      : DEFAULT_JOINDATE_LABEL;
  }
  if (jHint && isJoin) {
    jHint.textContent = 'This tracker doesn\'t report a join date, so enter it here for account-age tracking. Set once — it never changes.';
  }
}

// ─────────────────────────────────────────────────────────────────────────────
// Tracker opt-outs (defs/optout.json — adding these trackers is blocked)
// ─────────────────────────────────────────────────────────────────────────────

/**
 * Check the URL currently in the form against the opt-out list: show the
 * blocking notice and disable Save when it matches. Wired to the URL input's
 * oninput and called whenever the URL is set programmatically.
 */
export function checkTrackerOptOut(): void {
  const notice  = document.getElementById('modal-optout-notice');
  const saveBtn = document.getElementById('modal-save-btn') as HTMLButtonElement | null;
  const entry   = findOptOut(_defsCache?.opt_outs, getVal('modal-url'));
  if (entry) {
    if (notice) { notice.textContent = optOutMessage(entry); notice.style.display = ''; }
    if (saveBtn) saveBtn.disabled = true;
  } else {
    if (notice) notice.style.display = 'none';
    if (saveBtn) saveBtn.disabled = false;
  }
}

// ─────────────────────────────────────────────────────────────────────────────
// Add-modal: dropdown handlers (called via window from HTML onchange)
// ─────────────────────────────────────────────────────────────────────────────

export function onAddTrackerSelect() {
  const val = (document.getElementById('modal-add-select') as HTMLSelectElement)?.value ?? '';

  _selectedDef     = null;
  _selectedTypeKey = '';
  hide('modal-add-type-group');
  hide('modal-def-header');
  hide('modal-add-divider');
  hide('modal-name-url-group');
  hideFormSections();

  if (!val) return;

  if (val === '__manual__') {
    show('modal-add-type-group');
    const tp = document.getElementById('modal-add-type') as HTMLSelectElement;
    if (tp) tp.value = '';
    return;
  }

  const def = _defsCache?.trackers.find(d => d.key === val);
  if (def) applyPredefinedDef(def);
}

export function onAddTypeSelect() {
  const typeKey = (document.getElementById('modal-add-type') as HTMLSelectElement)?.value ?? '';
  _selectedTypeKey = typeKey;

  if (!typeKey) {
    hide('modal-name-url-group');
    hideFormSections();
    return;
  }

  show('modal-name-url-group');
  show('modal-add-divider');
  showFormForType(typeKey);
  setEl('modal-save-btn', 'Add Tracker');
  setTimeout(() => (document.getElementById('modal-name') as HTMLInputElement)?.focus(), 50);
}

// ─────────────────────────────────────────────────────────────────────────────
// Add-modal: internal helpers
// ─────────────────────────────────────────────────────────────────────────────

function applyPredefinedDef(def: DefInfo) {
  _selectedDef = def;
  setVal('modal-name', def.name);
  setVal('modal-url',  def.url);
  checkTrackerOptOut();

  const typeLabel = _defsCache?.types.find(t => t.key === def.type)?.label ?? def.type;
  setEl('modal-def-name-text', def.name);
  setEl('modal-def-abbr-text', def.abbr || '');
  setEl('modal-def-type-text', typeLabel);
  setEl('modal-def-url-text',  def.url);
  const abbrEl = document.getElementById('modal-def-abbr-text');
  if (abbrEl) abbrEl.style.display = def.abbr ? '' : 'none';

  // Staff-approval warning — anything but "approved" gets the callout.
  const apEl = document.getElementById('modal-def-approval');
  if (apEl) {
    if (approvalWarns(def.approval_status)) {
      apEl.innerHTML = `${approvalIcon(def.approval_status, def.approval_note)} Not officially approved by this tracker's staff — use at your own risk.`;
      apEl.style.display = '';
    } else {
      apEl.style.display = 'none';
    }
  }

  show('modal-def-header');
  show('modal-name-url-group');
  show('modal-add-divider');
  showFormForType(def.type);
  if (def.type === 'custom') applyCustomCredentialLabels(def);
  // Def-level required fields beat the type default showFormForType applied —
  // a field the def's API provides (e.g. HUNO's join_date) isn't demanded.
  applyRequiredFieldsUI(def.required_fields ?? typeRequiredFields(def.type));
  // API-only def → the session cookie does nothing (no profile scraping),
  // unless the def's API itself authenticates with it (auth_method
  // session_cookie) — then the field IS the credential and stays.
  if (def.scrape_disabled && !def.needs_session_cookie) hide('modal-session-cookie-group');
  setEl('modal-save-btn', `Add ${def.name}`);
}

function showFormForType(typeKey: string) {
  show('modal-credentials-section');
  hide('modal-mock-group');
  hide('modal-scrape-section');
  // Add mode keeps to the basics — targets are set later from the dashboard
  // pencil or the edit screen (this function only runs in the add flows).
  hide('modal-targets-section');

  // Join date applies to every real tracker; only demo trackers hide it.
  if (typeKey === 'test') hide('modal-joindate-group'); else show('modal-joindate-group');

  if (typeKey === 'test') {
    hide('modal-username-group');
    hide('modal-key-group');
    hide('modal-session-cookie-group');
    show('modal-mock-group');
    populateScenarioSelect();
  } else if (typeKey === 'gazelle') {
    // Gazelle needs the full set: API key + username for the ajax.php API,
    // session cookie for profile scraping (applyPredefinedDef hides the
    // cookie afterwards when the def disables scraping).
    resetStandardCredentialLabels(typeKey);
    show('modal-username-group');
    show('modal-key-group');
    show('modal-session-cookie-group');
  } else if (typeKey === 'custom') {
    show('modal-username-group');
    show('modal-key-group');
    show('modal-session-cookie-group');
    resetCustomCredentialLabels();
  } else {
    // unit3d + unknown defaults — full credential set
    resetStandardCredentialLabels();
    show('modal-username-group');
    show('modal-key-group');
    show('modal-session-cookie-group');
  }

  // Mark inputs required by this type (e.g. gazelle → username)
  applyRequiredFieldsUI(typeRequiredFields(typeKey));
}

function hideFormSections() {
  hide('modal-credentials-section');
  hide('modal-targets-section');
  hide('modal-mock-group');
  hide('modal-scrape-section');
}

// ─────────────────────────────────────────────────────────────────────────────
// Enabled toggle
// ─────────────────────────────────────────────────────────────────────────────

export function toggleEnabled() {
  _modalEnabled = !_modalEnabled;
  const track = document.getElementById('modal-toggle-track');
  const label = document.getElementById('modal-toggle-label');
  if (track) track.className = `toggle-track ${_modalEnabled ? 'on' : ''}`;
  if (label) label.textContent = _modalEnabled ? 'Enabled' : 'Disabled';
}

// ─────────────────────────────────────────────────────────────────────────────
// Open Add Modal
// ─────────────────────────────────────────────────────────────────────────────

export async function openAddModal(deps: ModalDeps) {
  clearModal();
  _selectedDef     = null;
  _selectedTypeKey = '';
  _modalEnabled    = true;
  void deps;

  setEl('modal-title',    'Add Tracker');
  setEl('modal-save-btn', 'Add Tracker');

  hide('modal-enabled-group');
  hide('modal-delete-btn');
  // Test is available in Add mode too (ad-hoc, unsaved) — modalTestTracker
  // branches on the absence of modal-tracker-id to hit /test-adhoc instead.
  show('modal-test-btn');
  hide('modal-detail-btn');
  hide('modal-test-result');

  show('modal-add-selection');
  hide('modal-add-type-group');
  hide('modal-def-header');
  hide('modal-add-divider');
  hide('modal-name-url-group');
  hideFormSections();

  applyRequiredFieldsUI([]);

  const searchEl = document.getElementById('modal-add-search') as HTMLInputElement | null;
  if (searchEl) searchEl.value = ''; // fresh, unfiltered list each open

  const defs = await ensureDefsLoaded();
  populateAddSelect(defs);
  populateTypeSelect(defs);
  checkTrackerOptOut(); // URL empty → hides any stale notice, re-enables Save

  const sel = document.getElementById('modal-add-select') as HTMLSelectElement;
  if (sel) sel.value = '';

  openTrackerPanel();
  setTimeout(() => sel?.focus(), 80);
}

// ─────────────────────────────────────────────────────────────────────────────
// Open Edit Modal
// ─────────────────────────────────────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────────────────────
// Target builder (edit mode)
//
// Selecting a target group shows a read-only chip summary of that group's
// requirements (the values are loaded from the def on save — no inputs to
// keep in sync). "— manual —" shows the builder: one row per target, plus a
// picker offering every stat this tracker actually reports.
// ─────────────────────────────────────────────────────────────────────────────

export interface TargetSpec {
  key: string;         // targets-map key
  label: string;
  placeholder: string;
  hint?: string;
}

/** The classic targets with friendly labels/formats. Availability is still
 *  gated on the tracker reporting the backing stat. */
const CORE_TARGET_SPECS: TargetSpec[] = [
  { key: 'uploaded',      label: 'Uploaded',      placeholder: 'e.g. 500 GiB' },
  { key: 'downloaded',    label: 'Downloaded',    placeholder: 'e.g. 100 GiB' },
  { key: 'ratio',         label: 'Ratio',         placeholder: 'e.g. 5.00' },
  { key: 'seed_size',     label: 'Seed Size',     placeholder: 'e.g. 5 TiB' },
  { key: 'total_uploads', label: 'Total Uploads', placeholder: 'e.g. 50' },
  { key: 'avg_seed',      label: 'Avg Seed Time', placeholder: 'e.g. 3M or 90D' },
  { key: 'days',          label: 'Account Age',   placeholder: 'e.g. 1Y 6M or 365', hint: 'Y M W D or plain days — needs a join date' },
  { key: 'bonus_points',  label: 'Bonus Points',  placeholder: 'e.g. 25000' },
  { key: 'snatched',      label: 'Snatched',      placeholder: 'e.g. 100' },
  { key: 'adoptions',     label: 'Adoptions',     placeholder: 'e.g. 10' },
  // No backing stat exists yet (see GroupRequirements.min_monthly_uploads) —
  // always offered; renders as an untrackable "Not available" target row.
  { key: 'monthly_uploads', label: 'Monthly Uploads', placeholder: 'e.g. 4', hint: 'No live stat yet — the requirement still shows, just without progress tracking' },
];

/** Merged stat field that backs each core target key. */
const TARGET_BACKING_FIELD: Record<string, string> = {
  uploaded: 'uploaded', downloaded: 'downloaded', ratio: 'ratio',
  seed_size: 'seed_size', total_uploads: 'uploads_approved',
  avg_seed: 'avg_seed_time', days: 'join_date', bonus_points: 'bonus_points',
  snatched: 'snatched', adoptions: 'adoptions',
};

/** Fields that never make sense as numeric targets (identity/meta/duration
 *  strings the generic compare can't handle). */
const TARGET_EXCLUDED_FIELDS = new Set([
  'username', 'group', 'join_date', 'user_id', 'active_event',
  'active_event_ends_at', 'unread_mail', 'unread_notifications',
  'total_seedtime',
]);

let _targetSpecs: TargetSpec[] = [];             // available for the open tracker
let _editTargets: Record<string, string> = {};   // targets when the modal opened

function targetSpecFor(key: string): TargetSpec {
  return _targetSpecs.find(s => s.key === key)
    ?? CORE_TARGET_SPECS.find(s => s.key === key)
    ?? { key, label: fieldLabel(key), placeholder: 'e.g. 100' };
}

/** Stats this tracker can target: the core set (where the backing stat is
 *  reported — or all of it before any stats exist) plus every numeric
 *  tracker-specific extra (fl_tokens, upload_snatches, HUNO's *_seeds, …). */
export function buildAvailableTargetSpecs(t: Tracker, resp: TrackerStatsResponse | undefined): TargetSpec[] {
  const fields = resp?.fields ?? {};
  const noStats = Object.keys(fields).length === 0;
  const have = (f: string) =>
    fields[f]?.value != null || (f === 'join_date' && !!t.join_date);

  const out: TargetSpec[] = [];
  for (const spec of CORE_TARGET_SPECS) {
    // monthly_uploads has no backing stat to gate on — always offered.
    if (spec.key === 'monthly_uploads' || noStats || have(TARGET_BACKING_FIELD[spec.key])) out.push(spec);
  }
  const coreBacked = new Set(Object.values(TARGET_BACKING_FIELD));
  for (const key of Object.keys(fields).sort()) {
    if (coreBacked.has(key) || TARGET_EXCLUDED_FIELDS.has(key)) continue;
    const n = parseFloat(String(fields[key]?.value ?? '').replace(/,/g, ''));
    if (isNaN(n)) continue;
    out.push({ key, label: fieldLabel(key), placeholder: 'e.g. 100' });
  }
  return out;
}

/** Stored value → what the input shows (days/avg_seed are stored normalized). */
export function targetDisplayValue(key: string, stored: string): string {
  if (key === 'days') {
    const d = parseAgeDays(stored);
    return d != null ? fmtAgeDays(d) : stored;
  }
  if (key === 'avg_seed') {
    const s = parseSeedTime(stored);
    return s !== null ? fmtSeedTime(s) : stored;
  }
  return stored;
}

function targetRowHtml(key: string, value: string): string {
  const spec = targetSpecFor(key);
  return `<div class="target-edit-row" data-target-key="${esc(key)}">
    <span class="target-edit-label" title="${esc(spec.hint ?? '')}">${esc(spec.label)}</span>
    <input class="form-input" type="text" data-target-input placeholder="${esc(spec.placeholder)}" value="${esc(value)}"/>
    <button type="button" class="btn btn-ghost btn-icon btn-sm target-edit-remove" title="Remove target" onclick="modalRemoveTargetRow('${esc(key)}')">&times;</button>
  </div>`;
}

/** (Re)build the manual rows from a targets map. */
function renderTargetRows(targets: Record<string, string>) {
  const wrap = document.getElementById('modal-target-rows');
  if (!wrap) return;
  wrap.innerHTML = Object.entries(targets)
    .filter(([k, v]) => v !== '' && !k.startsWith('count:'))
    .map(([k, v]) => targetRowHtml(k, targetDisplayValue(k, v)))
    .join('');
  refreshTargetAddSelect();
}

/** Rebuild the "+ Add target" picker: available specs minus rows already added. */
function refreshTargetAddSelect() {
  const sel = document.getElementById('modal-target-add-select') as HTMLSelectElement | null;
  if (!sel) return;
  const added = new Set(
    [...document.querySelectorAll<HTMLElement>('#modal-target-rows [data-target-key]')]
      .map(el => el.dataset['targetKey'] ?? ''));
  sel.innerHTML = `<option value="">&#8212; choose a stat &#8212;</option>` +
    _targetSpecs.filter(s => !added.has(s.key))
      .map(s => `<option value="${esc(s.key)}">${esc(s.label)}</option>`).join('');
}

export function modalAddTargetRow(): void {
  const sel = document.getElementById('modal-target-add-select') as HTMLSelectElement | null;
  const wrap = document.getElementById('modal-target-rows');
  if (!sel || !wrap || !sel.value) return;
  wrap.insertAdjacentHTML('beforeend', targetRowHtml(sel.value, ''));
  refreshTargetAddSelect();
  wrap.querySelector<HTMLInputElement>(`[data-target-key="${sel.value}"] input`)?.focus();
}

export function modalRemoveTargetRow(key: string): void {
  document.querySelector(`#modal-target-rows [data-target-key="${CSS.escape(key)}"]`)?.remove();
  refreshTargetAddSelect();
}

/** Normalize a raw target input to its stored form (days/avg_seed parse to a
 *  number string; everything else is passed through). Returns null when the
 *  value is empty or unparseable, so the caller can drop the row. Shared with
 *  the dashboard targets popover. */
export function normalizeTargetValue(key: string, raw: string): string | null {
  const v = raw.trim();
  if (!v) return null;
  if (key === 'days')     { const d = parseAgeDays(v);  return d != null ? String(d) : null; }
  if (key === 'avg_seed') { const s = parseSeedTime(v); return s !== null ? String(s) : null; }
  return v;
}

/** Read the builder rows back into a targets map (normalizing days/avg_seed). */
function collectTargetRows(): Record<string, string> {
  const out: Record<string, string> = {};
  for (const row of document.querySelectorAll<HTMLElement>('#modal-target-rows [data-target-key]')) {
    const key = row.dataset['targetKey'] ?? '';
    const raw = row.querySelector<HTMLInputElement>('[data-target-input]')?.value ?? '';
    const norm = key ? normalizeTargetValue(key, raw) : null;
    if (norm != null) out[key] = norm;
  }
  return out;
}

/** Switch the targets section between group-summary and manual-builder mode. */
function setTargetsMode(groupName: string, trackerKey: string) {
  const builder = document.getElementById('modal-target-builder');
  if (groupName) {
    renderGroupHint(trackerKey, groupName);
    if (builder) builder.style.display = 'none';
  } else {
    renderGroupHint('', '');
    if (builder) builder.style.display = '';
    // Only seed rows when the builder is empty, so toggling group ↔ manual
    // doesn't wipe in-progress edits.
    if (!document.querySelector('#modal-target-rows [data-target-key]')) {
      renderTargetRows(_editTargets);
    } else {
      refreshTargetAddSelect();
    }
  }
}

export function openEditModal(
  id: string,
  trackers: Tracker[],
  statsMap: StatsMap,
  settings: AppSettings,
  deps: EditDeps,
) {
  const t = trackers.find(x => x.id === id);
  if (!t) return;
  void settings;
  clearModal();

  setEl('modal-title',    'Edit Tracker');
  setEl('modal-save-btn', 'Save Changes');
  setVal('modal-tracker-id', t.id);
  setVal('modal-name', t.name);
  setVal('modal-url',  t.url);
  setVal('modal-username', t.username ?? '');
  setVal('modal-joindate', t.join_date ?? '');

  // Required fields (e.g. gazelle username) + opt-out check for this URL.
  // Once defs load, also settle session-cookie visibility for custom defs:
  // API-only + cookie-less auth (e.g. MAM's mam_id) → the cookie field is
  // pointless and hides; a session_cookie-auth def keeps it (it's the login).
  applyRequiredFieldsUI(t.required_fields ?? typeRequiredFields(t.type));
  void ensureDefsLoaded().then(() => {
    checkTrackerOptOut();
    if (t.supports_html_scrape === false) {
      const editDefInfo = _defsCache?.trackers.find(dd => dd.key === t.def_key);
      if (!editDefInfo?.needs_session_cookie) hide('modal-session-cookie-group');
    }
  });

  // API key — the mask sentinel means "unchanged"; clearing the field removes the key.
  setVal('modal-key', t.has_key ? (t.api_key_masked || MASKED_KEY) : '');
  setPlaceholder('modal-key', t.has_key ? `${MASKED_KEY} = keep current key` : 'Your API token');

  // For API-only trackers, hide the session cookie field entirely
  if (t.supports_html_scrape === false && t.type !== 'custom') {
    hide('modal-session-cookie-group');
  } else {
    show('modal-session-cookie-group');
    setVal('modal-session-cookie', t.has_session ? MASKED_KEY : '');
    setPlaceholder('modal-session-cookie',
      t.has_session ? `${MASKED_KEY} = keep current cookie` : 'Paste cookie string from browser DevTools');
  }

  // Tracker-specific API key label/hint — only for custom-type trackers.
  if (t.type === 'custom') {
    const editDef = _defsCache?.trackers.find(d => d.key === t.def_key);
    if (editDef) applyCustomCredentialLabels(editDef, t.api_key_hint);
    else resetCustomCredentialLabels(t.api_key_hint);
  } else {
    resetStandardCredentialLabels(t.type);
  }

  // Per-tracker scraping section (interval, daily cap, auto-calc, API-only).
  setupScrapeSection(t);

  // Targets — group summary vs manual builder. The builder's stat picker
  // offers whatever this tracker actually reports.
  const tKey = t.def_key;
  _editTargets = { ...(t.targets ?? {}) };
  _targetSpecs = buildAvailableTargetSpecs(t, statsMap[t.id]);
  const rowsWrap = document.getElementById('modal-target-rows');
  if (rowsWrap) rowsWrap.innerHTML = ''; // fresh per tracker — setTargetsMode reseeds

  const tKeyGroups = tKey ? (groupDefs[tKey] ?? []) : [];
  const groupRow = document.getElementById('modal-target-group-row');
  const groupSel = document.getElementById('modal-target-group-select') as HTMLSelectElement | null;
  if (groupRow && groupSel && tKeyGroups.length) {
    groupRow.style.display = '';
    groupRow.dataset['trackerKey'] = tKey;
    // Resolve live group from the merged stats fields
    const liveGroup = strOf(statsMap[t.id], 'group');
    // Rebuild options
    while (groupSel.options.length > 1) groupSel.remove(1);
    for (const g of tKeyGroups) {
      const opt = document.createElement('option');
      opt.value = g.name;
      const isCurrent = !!(liveGroup && g.name === liveGroup);
      opt.style.color = isCurrent ? 'var(--amber)' : '';
      opt.textContent = isCurrent ? `★ ${g.name} (current)` : g.name;
      if (g.name === t.target_group) opt.selected = true;
      groupSel.appendChild(opt);
    }
    if (!t.target_group) groupSel.value = '';
    groupSel.onchange = () => setTargetsMode(groupSel.value, tKey);
    setTargetsMode(t.target_group ?? '', tKey);
  } else {
    // No group defs — manual builder only.
    if (groupRow) groupRow.style.display = 'none';
    setTargetsMode('', tKey);
  }

  // Edit mode: hide add-selection, show everything directly
  hide('modal-add-selection');
  show('modal-name-url-group');
  show('modal-credentials-section');
  show('modal-targets-section');
  show('modal-enabled-group');
  show('modal-delete-btn');
  show('modal-test-btn');
  show('modal-detail-btn');
  hide('modal-test-result'); // clear any stale result from a previous tracker

  _modalEnabled = t.enabled;
  const track = document.getElementById('modal-toggle-track');
  const label = document.getElementById('modal-toggle-label');
  if (track) track.className = `toggle-track ${_modalEnabled ? 'on' : ''}`;
  if (label) label.textContent = _modalEnabled ? 'Enabled' : 'Disabled';

  if (t.type === 'test') {
    show('modal-mock-group');
    hide('modal-username-group');
    hide('modal-joindate-group');
    hide('modal-key-group');
    hide('modal-session-cookie-group');
    populateScenarioSelect(t.mock_scenario, () => switchMockScenario(t.id, trackers, deps));
  } else {
    hide('modal-mock-group');
    show('modal-username-group');
    show('modal-joindate-group');
    // Re-show the key field explicitly — a prior add-flow visit (test type,
    // or a def that hid it) leaves it display:none otherwise.
    show('modal-key-group');
  }

  openTrackerPanel();
  setTimeout(() => (document.getElementById('modal-name') as HTMLInputElement)?.focus(), 80);
}

/** Configure the per-tracker scraping section for the tracker being edited. */
function setupScrapeSection(t: Tracker) {
  // Demo/test trackers never scrape — no section.
  if (t.type === 'test') { hide('modal-scrape-section'); return; }
  show('modal-scrape-section');

  // Effective floor the user can't go below = max(60, operator request).
  _scrapeFloor = Math.max(60, t.tracker_min_interval || 0);
  _scrapeCap   = t.tracker_max_per_day || 0;
  // A def that forbids scraping (skip/disable) makes the tracker API-only —
  // force the toggle on and lock it.
  _apiOnlyLocked = t.supports_html_scrape === false;

  setVal('modal-min-scrape-interval', t.min_scrape_interval_minutes ? String(t.min_scrape_interval_minutes) : '');
  setVal('modal-max-scrapes', t.max_scrapes_per_day ? String(t.max_scrapes_per_day) : '');
  const autoCb = document.getElementById('modal-auto-interval') as HTMLInputElement | null;
  if (autoCb) autoCb.checked = !!t.auto_interval;

  const intervalInput = document.getElementById('modal-min-scrape-interval') as HTMLInputElement | null;
  if (intervalInput) intervalInput.min = String(_scrapeFloor);
  const maxScrapesInput = document.getElementById('modal-max-scrapes') as HTMLInputElement | null;
  if (maxScrapesInput) {
    if (_scrapeCap > 0) maxScrapesInput.max = String(_scrapeCap);
    else maxScrapesInput.removeAttribute('max');
  }

  const apiOnlyOn = _apiOnlyLocked || !!t.api_only;
  const track = document.getElementById('modal-api-only-track');
  if (track) track.className = `toggle-track ${apiOnlyOn ? 'on' : ''}`;
  const apiHint = document.getElementById('modal-api-only-hint');
  if (apiHint) {
    apiHint.textContent = _apiOnlyLocked
      ? 'This tracker is API-only — its definition does not support profile scraping.'
      : 'Only use the tracker API for this tracker — never scrape the profile page.';
  }
  const apiLabel = document.getElementById('modal-api-only-label');
  if (apiLabel) apiLabel.style.opacity = _apiOnlyLocked ? '0.7' : '';

  renderScrapeReq(t);
  applyScrapeLockState();
  modalValidateInterval();
  modalValidateMaxScrapes();
}

/** Operator-requested limits, emphasised in red above the fields. */
function renderScrapeReq(t: Tracker) {
  const el = document.getElementById('modal-scrape-req');
  if (!el) return;
  const parts: string[] = [];
  if (t.tracker_min_interval) parts.push(`requests ≥ ${t.tracker_min_interval} min between scrapes`);
  if (t.tracker_max_per_day)  parts.push(`max ${t.tracker_max_per_day} scrapes/day`);
  if (!parts.length) { el.style.display = 'none'; return; }
  el.innerHTML = `<i class="fas fa-triangle-exclamation" style="margin-right:6px"></i>This tracker operator ${parts.join(' · ')}.`;
  el.style.display = '';
}

/** Grey out + disable the scrape options when API-only is on; lock the interval
 *  input when auto-calculate is checked. */
function applyScrapeLockState() {
  const apiOnly = document.getElementById('modal-api-only-track')?.classList.contains('on') ?? false;
  const opts = document.getElementById('modal-scrape-opts');
  if (opts) opts.style.opacity = apiOnly ? '0.45' : '';

  const autoCb = document.getElementById('modal-auto-interval') as HTMLInputElement | null;
  const autoOn = !!autoCb?.checked;
  const intervalInput = document.getElementById('modal-min-scrape-interval') as HTMLInputElement | null;
  const maxInput = document.getElementById('modal-max-scrapes') as HTMLInputElement | null;
  const lock = document.getElementById('modal-min-scrape-lock');

  if (maxInput) maxInput.disabled = apiOnly;
  if (autoCb) autoCb.disabled = apiOnly;
  if (intervalInput) intervalInput.disabled = apiOnly || autoOn;
  if (lock) lock.style.display = (autoOn && !apiOnly) ? '' : 'none';

  if (autoOn && !apiOnly) recomputeAutoInterval();
}

/** Derive the interval from the per-tracker daily cap (1440/cap, floored). */
function recomputeAutoInterval() {
  const max = parseInt(getVal('modal-max-scrapes'), 10) || 0;
  const intervalInput = document.getElementById('modal-min-scrape-interval') as HTMLInputElement | null;
  if (!intervalInput) return;
  if (max > 0) intervalInput.value = String(Math.max(_scrapeFloor, Math.floor(1440 / max)));
  modalValidateInterval();
}

/** Toggle the per-tracker API-only setting (no-op when locked by the def). */
export function modalToggleApiOnly() {
  if (_apiOnlyLocked) return;
  const track = document.getElementById('modal-api-only-track');
  if (!track) return;
  track.className = `toggle-track ${track.classList.contains('on') ? '' : 'on'}`;
  applyScrapeLockState();
  modalValidateInterval();
  modalValidateMaxScrapes();
}

export function modalOnAutoIntervalChange() { applyScrapeLockState(); }
export function modalOnMaxScrapesChange() {
  const autoOn = (document.getElementById('modal-auto-interval') as HTMLInputElement | null)?.checked;
  if (autoOn) recomputeAutoInterval();
  modalValidateMaxScrapes();
}

/** Disable Save while either scrape field is red-flagged. */
function updateScrapeSaveLock() {
  const saveBtn = document.getElementById('modal-save-btn') as HTMLButtonElement | null;
  if (!saveBtn) return;
  saveBtn.disabled = ['modal-min-scrape-interval', 'modal-max-scrapes']
    .some(id => document.getElementById(id)?.classList.contains('input-error'));
}

/** Validate the interval against the floor; red + blocks save when too low.
 *  Returns true when valid (or not applicable). */
export function modalValidateInterval(): boolean {
  const input = document.getElementById('modal-min-scrape-interval') as HTMLInputElement | null;
  const errEl = document.getElementById('modal-min-scrape-error');
  if (!input) return true;
  const apiOnly = document.getElementById('modal-api-only-track')?.classList.contains('on') ?? false;
  const v = parseInt(input.value, 10);
  // Invalid only when a non-zero interval below the floor is entered while
  // scraping is active (0 = use global, API-only = not applicable).
  const invalid = !apiOnly && !isNaN(v) && v > 0 && v < _scrapeFloor;
  input.classList.toggle('input-error', invalid);
  if (errEl) {
    if (invalid) { errEl.textContent = `Must be at least ${_scrapeFloor} min (tracker minimum). Use 0 for the global default.`; errEl.style.display = ''; }
    else errEl.style.display = 'none';
  }
  updateScrapeSaveLock();
  return !invalid;
}

/** Validate the daily cap against the operator maximum; red + blocks save when
 *  above it. Returns true when valid (or not applicable). */
export function modalValidateMaxScrapes(): boolean {
  const input = document.getElementById('modal-max-scrapes') as HTMLInputElement | null;
  const errEl = document.getElementById('modal-max-scrapes-error');
  if (!input) return true;
  const apiOnly = document.getElementById('modal-api-only-track')?.classList.contains('on') ?? false;
  const v = parseInt(input.value, 10);
  // Invalid only when a non-zero cap above the operator maximum is entered
  // while scraping is active (0 = no extra cap, API-only = not applicable).
  const invalid = !apiOnly && _scrapeCap > 0 && !isNaN(v) && v > _scrapeCap;
  input.classList.toggle('input-error', invalid);
  if (errEl) {
    if (invalid) { errEl.textContent = `Must be at most ${_scrapeCap} per day (tracker maximum). Use 0 to follow the tracker's cap.`; errEl.style.display = ''; }
    else errEl.style.display = 'none';
  }
  updateScrapeSaveLock();
  return !invalid;
}

// ─────────────────────────────────────────────────────────────────────────────
// Connectivity test (edit panel "Test connection" button)
// ─────────────────────────────────────────────────────────────────────────────

/** Build the test-body overrides from the CURRENT edit-panel form values, so
 *  Test checks what's about to be saved rather than only what's stored. A
 *  field still showing the MASKED_KEY sentinel means the user hasn't
 *  touched it — omit it so the backend falls back to the stored value; any
 *  other value (including "") is a live override. */
function gatherTestOverrides(): TrackerTestOverrides {
  const overrides: TrackerTestOverrides = {
    username: getVal('modal-username'),
    url: getVal('modal-url'),
  };
  const key = getVal('modal-key');
  if (key !== MASKED_KEY) overrides.api_key = key;
  const cookie = getVal('modal-session-cookie');
  if (cookie !== MASKED_KEY) overrides.session_cookie = cookie;
  return overrides;
}

/** Build the /test-adhoc payload from the Add-tracker form — no tracker
 *  exists yet, so there's no masked sentinel to worry about; everything
 *  currently typed is sent as-is. */
function gatherAdhocPayload(): TrackerPayload {
  const type = _selectedDef?.type || _selectedTypeKey || undefined;
  return {
    url: getVal('modal-url'),
    api_key: getVal('modal-key'),
    session_cookie: getVal('modal-session-cookie'),
    username: getVal('modal-username'),
    ...(type ? { type } : {}),
  };
}

/** Run a live API + profile-scrape test and render the detailed per-method
 *  result inline above the footer, so the user can see exactly whether the
 *  API, the scrape cookie, or both are working. Edit mode tests the CURRENT
 *  form values against the saved tracker (gatherTestOverrides); Add mode
 *  (no modal-tracker-id yet) runs an ad-hoc test of the whole form — nothing
 *  is saved either way. */
export async function modalTestTracker(): Promise<void> {
  const id = getVal('modal-tracker-id');
  const panel = document.getElementById('modal-test-result');
  const btn = document.getElementById('modal-test-btn') as HTMLButtonElement | null;
  if (!panel) return;

  if (!id && !getVal('modal-url')) {
    panel.style.display = '';
    panel.innerHTML = `<div class="trk-test-row fail"><i class="fas fa-circle-xmark"></i> <span class="trk-test-word">Enter a URL first.</span></div>`;
    return;
  }

  panel.style.display = '';
  panel.innerHTML = `<div class="trk-test-row"><i class="fas fa-spinner fa-spin"></i> <span class="trk-test-word">Testing API &amp; profile scrape…</span></div>`;
  if (btn) btn.disabled = true;
  const { ok, data } = id
    ? await api.testTracker(id, gatherTestOverrides())
    : await api.testTrackerAdhoc(gatherAdhocPayload());
  if (btn) btn.disabled = false;
  panel.innerHTML = (ok && data)
    ? renderTestDetail(data)
    : `<div class="trk-test-row fail"><i class="fas fa-circle-xmark"></i> <span class="trk-test-word">Could not run the test.</span></div>`;
}

// ─────────────────────────────────────────────────────────────────────────────
// Save Tracker
// ─────────────────────────────────────────────────────────────────────────────

export async function saveTracker(deps: ModalDeps) {
  const id    = getVal('modal-tracker-id');
  const isNew = !id;
  const isDemo = isNew
    ? (_selectedDef?.type === 'test' || _selectedTypeKey === 'test')
    : false;

  const name = getVal('modal-name') || (_selectedDef?.name ?? '');
  let url  = getVal('modal-url');
  if (isDemo && !url) url = '//test.local'; // backend requires a URL; demo trackers never call it

  if (!name)           { deps.toast('Name is required', 'error'); return; }
  if (!isNew && !url)  { deps.toast('URL is required',  'error'); return; }
  if (isNew && !url)   { deps.toast('URL is required',  'error'); return; }

  // Required fields enforcement (e.g. gazelle: the API call needs a username)
  if (!isDemo && _requiredFields.includes('username') && !getVal('modal-username')) {
    deps.toast('Username is required by this tracker type', 'error');
    return;
  }
  if (!isDemo && _requiredFields.includes('join_date') && !getVal('modal-joindate')) {
    deps.toast('Join date is required — this tracker doesn\'t report one', 'error');
    return;
  }

  // Opt-out backstop — the notice/disabled Save already block this normally
  const optEntry = findOptOut(_defsCache?.opt_outs, url);
  if (optEntry) { deps.toast(optOutMessage(optEntry), 'error'); return; }

  // Build the targets map. Group selected → the group's base requirements
  // from the def (nothing to type); manual → the builder rows. In add mode
  // the section is hidden and targets start empty — set them later from the
  // dashboard pencil or the edit screen.
  const targetsVisible = document.getElementById('modal-targets-section')?.style.display !== 'none';
  const groupRowEl = document.getElementById('modal-target-group-row');
  const groupSelEl = document.getElementById('modal-target-group-select') as HTMLSelectElement | null;
  const chosenGroup = (targetsVisible && groupRowEl?.style.display !== 'none')
    ? (groupSelEl?.value ?? '') : '';
  let targets: Record<string, string> | undefined = {};
  if (chosenGroup) {
    const g = findGroupDef(groupDefs, groupRowEl?.dataset?.['trackerKey'] ?? '', chosenGroup);
    // Def group missing (renamed/removed) → omit targets, keeping the stored
    // values rather than wiping them.
    targets = g ? groupRequirementsToTargets(g.requirements) : undefined;
  } else if (targetsVisible) {
    targets = collectTargetRows();
  }

  const cookieVal   = getVal('modal-session-cookie');
  const trackerType = isNew ? (_selectedDef?.type || _selectedTypeKey || undefined) : undefined;
  const minScrape   = parseInt(getVal('modal-min-scrape-interval'), 10);

  // Block a too-low scrape interval (also red-flagged on the field).
  const scrapeVisible = document.getElementById('modal-scrape-section')?.style.display !== 'none';
  if (scrapeVisible && !modalValidateInterval()) {
    deps.toast(`Scrape interval must be at least ${_scrapeFloor} min`, 'error');
    return;
  }
  // Block a daily cap above the tracker operator's maximum.
  if (scrapeVisible && !modalValidateMaxScrapes()) {
    deps.toast(`Max scrapes must be at most ${_scrapeCap} per day`, 'error');
    return;
  }

  const payload: TrackerPayload = {
    name, url, enabled: _modalEnabled,
    api_key:  getVal('modal-key'),        // MASKED_KEY sentinel = unchanged
    username: getVal('modal-username'),
    ...(cookieVal ? { session_cookie: cookieVal } : {}),
    min_scrape_interval_minutes: !isNaN(minScrape) && minScrape > 0 ? minScrape : 0,
    ...(targets !== undefined ? { targets } : {}),
    target_group: chosenGroup,
    join_date: getVal('modal-joindate'),
    ...(trackerType ? { type: trackerType } : {}),
  };

  // Per-tracker scrape options (only when the section is shown — edit mode).
  if (scrapeVisible) {
    const maxScrape = parseInt(getVal('modal-max-scrapes'), 10);
    payload.max_scrapes_per_day = !isNaN(maxScrape) && maxScrape > 0 ? maxScrape : 0;
    payload.auto_interval = !!(document.getElementById('modal-auto-interval') as HTMLInputElement | null)?.checked;
    // When the def forbids scraping the toggle is forced ON for display only —
    // don't persist that visual state (omit → backend keeps the stored value).
    // Persisting it would bake api_only=true into the tracker, which outlives
    // a later def change that re-allows scraping.
    if (!_apiOnlyLocked) {
      payload.api_only = document.getElementById('modal-api-only-track')?.classList.contains('on') ?? false;
    }
  }

  if (isDemo || (!isNew && document.getElementById('modal-mock-group')?.style.display !== 'none')) {
    payload.mock_scenario =
      (document.getElementById('modal-mock-scenario') as HTMLSelectElement)?.value || 'healthy';
  }

  if (!isNew) {
    const { ok } = await api.updateTracker(id, payload);
    if (ok) {
      deps.toast(`${name} updated`, 'success');
      closeModal();
      await deps.loadTrackers();
      await deps.refreshSingle(id);
      await deps.loadScrapeStatus(); // a saved cookie/key/enabled change can flip scrape-blocked badges
      await deps.loadTestStatus(); // a pending test (unsaved-value test, see modalTestTracker) may have just been promoted
    }
    else    { deps.toast('Failed to update tracker', 'error'); }
  } else {
    const { ok, status, data } = await api.addTracker(payload);
    if (ok) {
      deps.toast(`${name} added`, 'success');
      closeModal();
      await deps.loadTrackers();
      if (data?.id && (payload.api_key || isDemo)) await deps.refreshSingle(data.id);
      await deps.loadScrapeStatus();
    } else if (status === 403 && (data as unknown as { error?: string })?.error === 'tracker_opted_out') {
      // Backend backstop for opted-out trackers
      const entry = (data as unknown as { opt_out?: OptOutEntry }).opt_out;
      deps.toast(entry ? optOutMessage(entry) : 'This tracker has asked not to be supported by Yata', 'error');
    } else {
      deps.toast('Failed to add tracker', 'error');
    }
  }
}

// ─────────────────────────────────────────────────────────────────────────────
// Delete
// ─────────────────────────────────────────────────────────────────────────────

let _pendingDeleteId: string | null = null;

/** Inline confirm strip inside the tracker panel (no popup). */
export function showDeleteConfirm() {
  _pendingDeleteId = getVal('modal-tracker-id');
  setEl('delete-popup-name', getVal('modal-name') || 'this tracker');
  show('tracker-delete-confirm');
  hide('modal-delete-btn');
  document.getElementById('tracker-delete-confirm')?.scrollIntoView({ block: 'nearest' });
}

export function closeDeletePopup() {
  hide('tracker-delete-confirm');
  // Only re-show the delete button when we're in edit mode (it has an id).
  if (getVal('modal-tracker-id')) show('modal-delete-btn');
}

export async function confirmDeletePopup(
  trackers: Tracker[],
  statsMap: StatsMap,
  expandedRows: Set<string>,
  deps: { loadTrackers: () => Promise<void>; loadScrapeStatus: () => Promise<void>; toast: (msg: string, type?: ToastType) => void },
) {
  if (!_pendingDeleteId) return;
  const deletedId = _pendingDeleteId;
  const t = trackers.find(x => x.id === deletedId);
  const { ok } = await api.deleteTracker(deletedId);
  if (ok) {
    deps.toast(`${t?.name ?? 'Tracker'} removed`, 'success');
    delete statsMap[deletedId];
    expandedRows.delete(deletedId);
    closeDeletePopup();
    closeModal();
    await deps.loadTrackers();
    await deps.loadScrapeStatus();
    // Redraw an open detail page for this tracker — closing it rather than
    // rendering a "not found" ghost, since it no longer exists in state.
    void import('../views/detail').then(m => m.detailTrackerDeleted(deletedId));
  } else {
    deps.toast('Failed to remove tracker', 'error');
    closeDeletePopup();
  }
}

// ─────────────────────────────────────────────────────────────────────────────
// Open / close the inline tracker panel (Settings → Trackers tab)
// ─────────────────────────────────────────────────────────────────────────────

function openTrackerPanel() {
  hide('tracker-delete-confirm');
  hide('trackers-list-section');
  show('tracker-panel');
  document.getElementById('settings-page')?.scrollIntoView({ block: 'start' });
}

/** Close the inline tracker form panel and return to the trackers table. */
export function closeModal() {
  hide('tracker-panel');
  show('trackers-list-section');
  closeDeletePopup();
}

/** True while the inline add/edit tracker panel is open. */
export function isTrackerPanelOpen(): boolean {
  const el = document.getElementById('tracker-panel');
  return !!el && el.style.display !== 'none';
}

// ─────────────────────────────────────────────────────────────────────────────
// Settings page
// ─────────────────────────────────────────────────────────────────────────────

let _sd: SettingsDeps | null = null;
let _selectedThemeId: string  = '';   // tracks the live selection in the settings modal

// Swatch fallback used when a theme CSS file has no /* swatches: */ comment.
const SWATCH_FALLBACK: [string, string, string, string] = ['#111', '#222', '#6d56eb', '#a855f7'];

function renderThemeGrid(currentThemeId: string) {
  _selectedThemeId = currentThemeId || 'default';
  const container = document.getElementById('s-theme-grid');
  if (!container) return;

  container.innerHTML = '<span style="font-size:12px;color:var(--text3);font-style:italic">Loading themes…</span>';

  api.fetchThemes().then(({ ok, data }) => {
    if (!ok || !data.length) return;

    container.innerHTML = data.map(t => {
      // Prefer swatches declared in the CSS file; fall back to a neutral default.
      const sw: [string, string, string, string] =
        (t.swatches && t.swatches.length === 4)
          ? t.swatches as [string, string, string, string]
          : SWATCH_FALLBACK;
      const active = (t.id === _selectedThemeId || (!_selectedThemeId && t.id === 'default')) ? 'active' : '';
      return `
        <div class="theme-option ${active}" data-theme-id="${esc(t.id)}" onclick="selectTheme('${esc(t.id)}')">
          <div class="theme-swatch" style="background:${sw[0]}">
            <div class="theme-swatch-col" style="background:${sw[1]}"></div>
            <div class="theme-swatch-col" style="background:${sw[2]}"></div>
            <div class="theme-swatch-col" style="background:${sw[3]}"></div>
          </div>
          <span class="theme-option-name">${esc(t.name)}</span>
        </div>`;
    }).join('');
  });
}

export function selectTheme(themeId: string) {
  _selectedThemeId = themeId;
  // Update active state in grid
  document.querySelectorAll<HTMLElement>('.theme-option').forEach(el => {
    el.classList.toggle('active', el.dataset['themeId'] === themeId);
    const nameEl = el.querySelector('.theme-option-name');
    if (nameEl) (nameEl as HTMLElement).style.color = el.dataset['themeId'] === themeId ? 'var(--accent)' : '';
  });
  // Live preview
  api.applyTheme(themeId);
}

// ── Theme / display live preview ──────────────────────────────────────────────

// Fixed sample group for the preview card — a sparkling top-tier "Elite".
const PREVIEW_GROUP: GroupDef = {
  name: 'Elite',
  style: { color: '#f5b942', icon: 'fas fa-crown', sparkle: true },
  requirements: {},
};

// Event globe used by the preview event banner (matches the detail view).
const PREVIEW_EVENT_ICON = eventGlobeSvg('flex-shrink:0');

/** Read the live (possibly-unsaved) Display form state into a settings object
 *  so the preview reflects choices before they're saved. */
function previewSettings(): AppSettings {
  const radio = (name: string, fb: string) =>
    document.querySelector<HTMLInputElement>(`input[name="${name}"]:checked`)?.value || fb;
  const on = (id: string) => !!document.getElementById(id)?.classList.contains('on');
  return {
    ...appSettings,
    tracker_name_mode: radio('s-name-mode', 'name'),
    group_name_style:  radio('s-group-name-style', 'plain'),
    username_style:    radio('s-username-style', 'group'),
    duration_format:   radio('s-duration-format', 'ym'),
    private_mode:      on('s-private-track'),
    show_stat_sources: on('s-stat-src-track'),
  } as AppSettings;
}

/** Render the live theme/display preview card from current form state. */
export function renderThemePreview(): void {
  const card = document.getElementById('theme-preview-card');
  if (!card) return;
  const s = previewSettings();

  const nameTxt    = fmtTrackerName('Tracker Name', 'TrN', s.tracker_name_mode || 'name');
  const groupBadge = renderGroupBadge(PREVIEW_GROUP, 'Elite', s, 'badge-group');
  // Generic dummy username — matches the app's card-user styling (this is a
  // public app, so no real username here).
  const username   = renderUsername('Username', PREVIEW_GROUP, s, 'card-username private-blur');
  const dot = (src: 'api' | 'scrape') =>
    s.show_stat_sources ? `<span class="stat-src stat-src--${src}" style="margin-left:5px"></span>` : '';
  const eta = (days: number) =>
    `<span class="target-eta" style="margin-left:6px">≈ ${esc(fmtEtaDays(days, s.duration_format))}</span>`;

  card.innerHTML = `
    <div class="theme-preview-head">
      <span class="theme-preview-name"><span class="theme-preview-dot"></span>${esc(nameTxt)}</span>
      <span class="badge-membership" title="Account age">6M 5W 4D</span>
    </div>
    <div class="card-user" style="margin-bottom:8px">
      ${username}
      ${groupBadge}
    </div>
    <div class="exp-event-banner" style="margin:8px 0 0">
      ${PREVIEW_EVENT_ICON}
      <span class="exp-event-text">Global Freeleech</span>
      <span class="exp-event-ends">ends in 2d 4h</span>
    </div>
    <div class="theme-preview-stats" style="margin-top:10px">
      <div class="stat-item"><div class="stat-label">Uploaded</div><div class="stat-value green">8.24 TB${dot('api')}</div></div>
      <div class="stat-item"><div class="stat-label">Ratio</div><div class="stat-value red">2.41${dot('api')}</div></div>
      <div class="stat-item"><div class="stat-label">Buffer</div><div class="stat-value blue">3.10 TB${dot('scrape')}</div></div>
      <div class="stat-item"><div class="stat-label">Avg Seed Time</div><div class="stat-value pink">88d${dot('scrape')}</div></div>
      <div class="stat-item"><div class="stat-label">Bonus</div><div class="stat-value orange">14,208${dot('api')}</div></div>
    </div>
    <div class="targets-section" style="margin-top:10px">
      <div class="target-row">
        <div class="target-header">
          <span class="target-lbl">Ratio target</span>
          <span class="target-vals">2.41 <span class="tgt">/ 3.00</span>${eta(44)}</span>
        </div>
        <div class="progress-track"><div class="progress-fill green" style="width:80%"></div></div>
      </div>
      <div class="target-row">
        <div class="target-header">
          <span class="target-lbl">Seed time</span>
          <span class="target-vals">88d <span class="tgt">/ 120d</span>${eta(32)}</span>
        </div>
        <div class="progress-track"><div class="progress-fill amber" style="width:43%"></div></div>
      </div>
      <div class="target-row">
        <div class="target-header">
          <span class="target-lbl">Upload target</span>
          <span class="target-vals">1.8 TB <span class="tgt">/ 10 TB</span>${eta(852)}</span>
        </div>
        <div class="progress-track"><div class="progress-fill red" style="width:18%"></div></div>
      </div>
    </div>
    <div class="theme-preview-actions">
      <span class="btn btn-primary btn-sm" role="presentation">Refresh</span>
      <span class="btn btn-ghost btn-sm" role="presentation">Details</span>
      <span class="btn btn-danger btn-sm" role="presentation">Remove</span>
      <span class="theme-preview-srcs">
        <span class="stat-src stat-src--api"></span>API
        <span class="stat-src stat-src--scrape" style="margin-left:8px"></span>Scrape
      </span>
    </div>`;
}

// ── Account / login protection (Settings → General) ──────────────────────────

function acctVal(id: string): string {
  return (document.getElementById(id) as HTMLInputElement | null)?.value ?? '';
}

function acctError(msg: string) {
  const el = document.getElementById('s-acct-error');
  if (el) { el.textContent = msg; el.style.display = msg ? 'block' : 'none'; }
}

/** Render the Account section based on whether login protection is configured. */
export async function renderAccountSection(): Promise<void> {
  const box = document.getElementById('s-account-section');
  if (!box) return;
  const { ok, data } = await api.fetchAuthStatus();
  if (!ok) {
    box.innerHTML = `<div style="font-size:12px;color:var(--text3)">Could not load account status.</div>`;
    return;
  }
  const errBox = `<div id="s-acct-error" class="login-error" style="display:none;margin-top:10px"></div>`;

  if (!data.configured) {
    box.innerHTML = `
      <div style="font-size:12px;color:var(--text3);margin-bottom:12px">Protect this instance with a username and password. Recommended if Yata is reachable beyond <code>localhost</code> (an open port). Once enabled, signing in is required.</div>
      <div style="display:flex;flex-direction:column;gap:8px;max-width:320px">
        <input class="form-input" type="text" id="s-acct-username" placeholder="Username" autocomplete="username"/>
        <input class="form-input" type="password" id="s-acct-password" placeholder="Password (min 8 characters)" autocomplete="new-password"/>
        <input class="form-input" type="password" id="s-acct-password2" placeholder="Confirm password" autocomplete="new-password"/>
      </div>
      ${errBox}
      <button type="button" class="btn btn-primary btn-sm" style="margin-top:12px" onclick="accountSetup()">Enable login protection</button>`;
    return;
  }

  box.innerHTML = `
    <div style="font-size:12px;color:var(--text);margin-bottom:4px"><i class="fas fa-lock" style="margin-right:6px;color:var(--green)"></i>Login protection is <strong>on</strong> — signed in as <strong>${esc(data.username ?? '')}</strong>.</div>
    <div style="font-size:13px;font-weight:600;color:var(--text);margin:14px 0 8px">Change password</div>
    <div style="display:flex;flex-direction:column;gap:8px;max-width:320px">
      <input class="form-input" type="password" id="s-acct-curpw" placeholder="Current password" autocomplete="current-password"/>
      <input class="form-input" type="password" id="s-acct-newpw" placeholder="New password (min 8 characters)" autocomplete="new-password"/>
      <input class="form-input" type="password" id="s-acct-newpw2" placeholder="Confirm new password" autocomplete="new-password"/>
    </div>
    ${errBox}
    <div style="display:flex;gap:8px;margin-top:12px;flex-wrap:wrap">
      <button type="button" class="btn btn-primary btn-sm" onclick="accountChangePassword()">Change password</button>
      <button type="button" class="btn btn-ghost btn-sm" onclick="doLogout()">Log out</button>
    </div>
    <div style="font-size:13px;font-weight:600;color:var(--text);margin:18px 0 6px">Disable protection</div>
    <div style="font-size:11px;color:var(--text3);margin-bottom:8px">Removes the account and turns login off. Enter your current password to confirm.</div>
    <div style="display:flex;gap:8px;align-items:center;flex-wrap:wrap;max-width:420px">
      <input class="form-input" type="password" id="s-acct-disablepw" placeholder="Current password" autocomplete="current-password" style="max-width:200px"/>
      <button type="button" class="btn btn-danger btn-sm" onclick="accountDisable()">Disable login protection</button>
    </div>`;
}

/** Create the first account (enable login protection). */
export async function accountSetup(): Promise<void> {
  acctError('');
  const username = acctVal('s-acct-username').trim();
  const pw = acctVal('s-acct-password');
  const pw2 = acctVal('s-acct-password2');
  if (!username) { acctError('Enter a username.'); return; }
  if (pw.length < 8) { acctError('Password must be at least 8 characters.'); return; }
  if (pw !== pw2) { acctError('Passwords do not match.'); return; }
  const { ok, data } = await api.authSetup(username, pw);
  if (ok && data.ok) {
    _sd?.toast('Login protection enabled', 'success');
    await renderAccountSection();
  } else {
    acctError(data.error === 'password_too_short' ? 'Password must be at least 8 characters.' : 'Could not enable login protection.');
  }
}

/** Change the account password. */
export async function accountChangePassword(): Promise<void> {
  acctError('');
  const cur = acctVal('s-acct-curpw');
  const nw = acctVal('s-acct-newpw');
  const nw2 = acctVal('s-acct-newpw2');
  if (nw.length < 8) { acctError('New password must be at least 8 characters.'); return; }
  if (nw !== nw2) { acctError('New passwords do not match.'); return; }
  const { ok, data, status } = await api.authChangePassword(cur, nw);
  if (ok && data.ok) {
    _sd?.toast('Password changed', 'success');
    await renderAccountSection();
  } else {
    acctError(status === 401 ? 'Current password is incorrect.' : 'Could not change password.');
  }
}

/** Disable login protection (remove the account). */
export async function accountDisable(): Promise<void> {
  acctError('');
  const pw = acctVal('s-acct-disablepw');
  if (!pw) { acctError('Enter your current password to confirm.'); return; }
  const { ok, data, status } = await api.authDisable(pw);
  if (ok && data.ok) {
    _sd?.toast('Login protection disabled', 'success');
    await renderAccountSection();
  } else {
    acctError(status === 401 ? 'Current password is incorrect.' : 'Could not disable login protection.');
  }
}

// ── Config backups (Settings → General → Data) ───────────────────────────────

/** Render the list of existing config backups. */
export async function renderBackupList(): Promise<void> {
  const el = document.getElementById('s-backup-list');
  if (!el) return;
  const { ok, data } = await api.fetchBackups();
  if (!ok) { el.textContent = ''; return; }
  const list = data.backups ?? [];
  if (!list.length) {
    el.innerHTML = `<span style="font-style:italic">No backups yet.</span>`;
    return;
  }
  const rows = list.slice(0, 10).map(b =>
    `<div>${esc(b.name)} <span style="color:var(--text3)">· ${fmtBytes(b.size)} · ${new Date(b.mod_time * 1000).toLocaleString()}</span></div>`
  ).join('');
  el.innerHTML =
    `<div style="margin-bottom:5px;color:var(--text2)">${list.length} backup${list.length > 1 ? 's' : ''} in <code>${esc(data.dir)}</code></div>${rows}`;
}

/** Create an on-demand backup now, then refresh the list. */
export async function backupNow(): Promise<void> {
  const { ok } = await api.createBackup();
  if (ok) { _sd?.toast('Backup created', 'success'); await renderBackupList(); }
  else _sd?.toast('Backup failed', 'error');
}

// ── Version / update check (General tab) ─────────────────────────────────────

function renderUpdateStatus(s: UpdateStatus, checked: boolean): void {
  const rows: [string, keyof UpdateStatus][] = [['app', 'app'], ['defs', 'defs'], ['pathways', 'pathways']];
  for (const [id, key] of rows) {
    const comp = s[key] as { current: string; latest?: string; update_available: boolean };
    setVal2(`s-ver-${id}`, comp.current || '—');
    const badge = document.getElementById(`s-ver-${id}-badge`);
    if (!badge) continue;
    if (comp.update_available) {
      badge.className = 'ver-badge out';
      badge.textContent = `update available → ${comp.latest}`;
    } else if (checked && comp.latest) {
      badge.className = 'ver-badge ok';
      badge.textContent = 'up to date';
    } else {
      badge.className = 'ver-badge';
      badge.textContent = '';
    }
  }
  const status = document.getElementById('s-update-status');
  if (status) {
    if (s.error) status.textContent = `Check failed: ${s.error}`;
    else if (s.checked_at) status.textContent = `Last checked ${new Date(s.checked_at * 1000).toLocaleString()}`;
    else status.textContent = '';
  }
}

function setVal2(id: string, text: string) { const el = document.getElementById(id); if (el) el.textContent = text; }

/** Load cached update status (no network) on tab open. */
async function loadUpdateStatus(): Promise<void> {
  const { ok, data } = await api.fetchUpdateStatus();
  if (ok) renderUpdateStatus(data, !!data.checked_at);
}

/** "Check for updates" — contacts GitHub now. */
export async function checkForUpdates(): Promise<void> {
  const btn = document.getElementById('s-update-check') as HTMLButtonElement | null;
  const status = document.getElementById('s-update-status');
  if (btn) { btn.disabled = true; btn.textContent = 'Checking…'; }
  if (status) status.textContent = 'Contacting GitHub…';
  const { ok, data } = await api.runUpdateCheck();
  if (btn) { btn.disabled = false; btn.textContent = 'Check for updates'; }
  if (ok) {
    renderUpdateStatus(data, true);
    const any = data.app.update_available || data.defs.update_available || data.pathways.update_available;
    _sd?.toast(any ? 'Updates available — see About' : 'Everything is up to date', any ? 'success' : 'success');
  } else {
    _sd?.toast('Update check failed', 'error');
  }
}

/** Persist the daily-auto-check opt-in immediately (like the other toggles).
 *  On failure, revert appSettings AND the checkbox — mirrors togglePrivacyQuick
 *  (main.ts) — so the UI never claims a setting persisted when it didn't. */
export async function toggleAutoUpdate(): Promise<void> {
  if (!_sd) return;
  const cb = document.getElementById('s-update-auto') as HTMLInputElement | null;
  const on = cb?.checked ?? false;
  appSettings.update_check_auto = on;
  const { ok } = await api.saveSettings({ ...appSettings, update_check_auto: on });
  if (ok) {
    _sd.toast(on ? 'Daily update check on' : 'Daily update check off', 'success');
  } else {
    appSettings.update_check_auto = !on;
    if (cb) cb.checked = !on;
    _sd.toast('Failed to save — daily update check unchanged', 'error');
  }
}

/** Persist the reverse-proxy header opt-in immediately (like the other toggles).
 *  On failure, revert appSettings AND the checkbox — mirrors togglePrivacyQuick
 *  (main.ts) — so the UI never claims a setting persisted when it didn't. */
export async function toggleTrustProxy(): Promise<void> {
  if (!_sd) return;
  const cb = document.getElementById('s-trust-proxy') as HTMLInputElement | null;
  const on = cb?.checked ?? false;
  appSettings.trust_proxy_headers = on;
  const { ok } = await api.saveSettings({ ...appSettings, trust_proxy_headers: on });
  if (ok) {
    _sd.toast(on ? 'Proxy headers trusted' : 'Proxy headers ignored', 'success');
  } else {
    appSettings.trust_proxy_headers = !on;
    if (cb) cb.checked = !on;
    _sd.toast('Failed to save — proxy header trust unchanged', 'error');
  }
}

/** Populate the settings page form from current settings (no modal — full page). */
export function openSettingsPage(settings: AppSettings, _meta: unknown[], deps: SettingsDeps) {
  _sd = deps;
  (document.getElementById('s-qui-url') as HTMLInputElement).value = settings.qui_url ?? 'http://localhost:7476';
  // Mask round-trip: show the mask sentinel (NOT a blank field) when a key is
  // stored — saving sends it back unchanged; typing replaces; clearing clears.
  (document.getElementById('s-qui-key') as HTMLInputElement).value = settings.qui_api_key ?? '';
  setPlaceholder('s-qui-key', settings.qui_api_key ? `${MASKED_KEY} = keep current key` : 'Your QUI API key');

  const syncTrack    = document.getElementById('s-profile-sync-track');
  const faviconTrack = document.getElementById('s-favicon-track');
  const privateTrack = document.getElementById('s-private-track');
  const quiBarsTrack = document.getElementById('s-qui-bars-track');
  const statSrcTrack = document.getElementById('s-stat-src-track');
  if (syncTrack)    syncTrack.className    = `toggle-track ${settings.profile_auto_sync !== false ? 'on' : ''}`;
  if (faviconTrack) faviconTrack.className = `toggle-track ${settings.show_favicons ? 'on' : ''}`;
  if (privateTrack) privateTrack.className = `toggle-track ${settings.private_mode ? 'on' : ''}`;
  if (quiBarsTrack) quiBarsTrack.className = `toggle-track ${settings.qui_bars_visible !== false ? 'on' : ''}`;
  applyQuiOptionsEnabled(settings.qui_bars_visible !== false);
  if (statSrcTrack) statSrcTrack.className = `toggle-track ${settings.show_stat_sources ? 'on' : ''}`;
  const pwEtaTrack = document.getElementById('s-pw-eta-track');
  if (pwEtaTrack) pwEtaTrack.className = `toggle-track ${settings.show_pathway_etas !== false ? 'on' : ''}`;
  const trendTrack = document.getElementById('s-trend-est-track');
  if (trendTrack) trendTrack.className = `toggle-track ${settings.show_trend_estimates !== false ? 'on' : ''}`;
  const targetEtaTrack = document.getElementById('s-target-eta-track');
  if (targetEtaTrack) targetEtaTrack.className = `toggle-track ${settings.show_target_etas !== false ? 'on' : ''}`;
  const rateHoverTrack = document.getElementById('s-rate-hover-track');
  if (rateHoverTrack) rateHoverTrack.className = `toggle-track ${settings.show_rate_hovers !== false ? 'on' : ''}`;
  const trackerRulesTrack = document.getElementById('s-tracker-rules-track');
  if (trackerRulesTrack) trackerRulesTrack.className = `toggle-track ${settings.show_tracker_rules !== false ? 'on' : ''}`;
  const unreadMailTrack = document.getElementById('s-unread-mail-track');
  if (unreadMailTrack) unreadMailTrack.className = `toggle-track ${settings.show_unread_mail !== false ? 'on' : ''}`;
  const unreadNotifTrack = document.getElementById('s-unread-notif-track');
  if (unreadNotifTrack) unreadNotifTrack.className = `toggle-track ${settings.show_unread_notifications !== false ? 'on' : ''}`;
  const hnrHighlightTrack = document.getElementById('s-hnr-highlight-track');
  if (hnrHighlightTrack) hnrHighlightTrack.className = `toggle-track ${settings.highlight_hnr !== false ? 'on' : ''}`;
  const durFmt = settings.duration_format || 'ym';
  document.querySelectorAll<HTMLInputElement>('input[name="s-duration-format"]').forEach(r => {
    r.checked = r.value === durFmt;
  });

  // Scrape rate-limit settings
  const apiOnlyTrack = document.getElementById('s-api-only-track');
  if (apiOnlyTrack) apiOnlyTrack.className = `toggle-track ${settings.api_only_mode ? 'on' : ''}`;
  const maxScrapes = document.getElementById('s-max-scrapes') as HTMLInputElement | null;
  if (maxScrapes) maxScrapes.value = String(settings.max_scrapes_per_day ?? 0);
  const autoIntervalCb = document.getElementById('s-auto-interval') as HTMLInputElement | null;
  if (autoIntervalCb) autoIntervalCb.checked = settings.auto_interval ?? false;
  const intervalInput = document.getElementById('s-scrape-interval') as HTMLInputElement | null;
  if (intervalInput) {
    if (settings.auto_interval && (settings.max_scrapes_per_day ?? 0) > 0) {
      intervalInput.value = String(Math.max(60, Math.floor(1440 / (settings.max_scrapes_per_day ?? 1))));
      intervalInput.disabled = true;
    } else {
      intervalInput.value = String(settings.scrape_interval_minutes ?? 120);
      intervalInput.disabled = false;
    }
  }
  const refreshInput = document.getElementById('s-refresh-interval') as HTMLInputElement | null;
  if (refreshInput) refreshInput.value = String(settings.refresh_interval_minutes || 30);
  const quiRefreshInput = document.getElementById('s-qui-refresh') as HTMLInputElement | null;
  if (quiRefreshInput) quiRefreshInput.value = String(settings.qui_refresh_seconds || 10);

  // Populate theme grid
  renderThemeGrid(settings.theme ?? '');

  // Set tracker name mode radio
  const nameMode = settings.tracker_name_mode || 'name';
  document.querySelectorAll<HTMLInputElement>('input[name="s-name-mode"]').forEach(r => {
    r.checked = r.value === nameMode;
    r.onchange = () => {
      if (r.checked) _sd?.applyNameMode(r.value);
      renderThemePreview();
    };
  });

  // Set group name mode radio
  const groupNameMode = settings.group_name_style || 'plain';
  document.querySelectorAll<HTMLInputElement>('input[name="s-group-name-style"]').forEach(r => {
    r.checked = r.value === groupNameMode;
    r.onchange = renderThemePreview;
  });

  // Set username style radio
  const usernameStyle = settings.username_style || 'plain';
  document.querySelectorAll<HTMLInputElement>('input[name="s-username-style"]').forEach(r => {
    r.checked = r.value === usernameStyle;
    r.onchange = renderThemePreview;
  });

  // Duration format radios also drive the preview ETAs.
  document.querySelectorAll<HTMLInputElement>('input[name="s-duration-format"]').forEach(r => {
    r.onchange = renderThemePreview;
  });

  // Versions + update check (General tab). Loads cached status (no network).
  void loadUpdateStatus();
  const auto = document.getElementById('s-update-auto') as HTMLInputElement | null;
  if (auto) auto.checked = settings.update_check_auto ?? false;

  // Account / login protection (General tab).
  void renderAccountSection();
  const trustProxy = document.getElementById('s-trust-proxy') as HTMLInputElement | null;
  if (trustProxy) trustProxy.checked = settings.trust_proxy_headers ?? false;

  // Data: automatic backup settings + backup list (General tab).
  const backupTrack = document.getElementById('s-backup-track');
  if (backupTrack) backupTrack.className = `toggle-track ${settings.backup_enabled ? 'on' : ''}`;
  const freqSel = document.getElementById('s-backup-frequency') as HTMLSelectElement | null;
  if (freqSel) freqSel.value = settings.backup_frequency || 'weekly';
  const keepInput = document.getElementById('s-backup-keep') as HTMLInputElement | null;
  if (keepInput) keepInput.value = String(settings.backup_keep || 5);
  (window as unknown as { _applyBackupLock?: () => void })._applyBackupLock?.();
  void renderBackupList();

  // Initial preview render reflecting the saved settings.
  renderThemePreview();

  // Reflect API-only / auto-calc lock state on the scrape inputs (defined as
  // an inline fallback in index.html and registered on window by the bundle).
  (window as unknown as { _applyScrapeLockouts?: () => void })._applyScrapeLockouts?.();

  setTimeout(() => deps.renderQUIInstanceChecklist(), 80);
}

// ── Custom tracker credential helpers ─────────────────────────────────────────

/** Reset API key label/hint to generic custom-tracker language. */
function resetCustomCredentialLabels(apiKeyHint?: string) {
  const lbl  = document.getElementById('modal-key-label');
  const hint = document.getElementById('modal-key-hint');
  const uLbl = document.getElementById('modal-username-label');
  if (lbl)  lbl.textContent = 'API Key / Session Token';
  if (hint) hint.textContent = apiKeyHint || 'Session token or API key as required by this tracker';
  if (uLbl) uLbl.innerHTML  = 'Username <span class="opt">(optional — auto-filled from API)</span>';
}

/** Reset to standard Unit3D labels when switching away from custom type. */
function resetStandardCredentialLabels(typeKey?: string) {
  const lbl  = document.getElementById('modal-key-label');
  const hint = document.getElementById('modal-key-hint');
  const uLbl = document.getElementById('modal-username-label');
  if (lbl)  lbl.textContent = 'API Key';
  if (hint) hint.textContent = typeKey === 'gazelle'
    ? 'Settings → Access Settings → API Keys (user profile scope)'
    : 'Profile → API Token settings';
  if (uLbl) uLbl.innerHTML  = 'Username';
}

/** Adapt credential labels for a custom tracker def using its api_key_hint. */
function applyCustomCredentialLabels(def: DefInfo, fallbackHint?: string) {
  const lbl  = document.getElementById('modal-key-label');
  const hint = document.getElementById('modal-key-hint');
  const uLbl = document.getElementById('modal-username-label');
  if (uLbl) uLbl.innerHTML = 'Username <span class="opt">(optional — auto-filled from API)</span>';
  if (lbl)  lbl.textContent = 'API Key / Session Token';
  const h = def.api_key_hint || fallbackHint;
  if (hint) hint.textContent = h
    ? `${def.name}: ${h}`
    : `Session token or API key required by ${def.name} — check tracker preferences/security settings`;
}

export function toggleSettingsSync() {
  const t = document.getElementById('s-profile-sync-track');
  if (t) t.className = `toggle-track ${t.classList.contains('on') ? '' : 'on'}`;
}

export function toggleSettingsFavicon() {
  const t = document.getElementById('s-favicon-track');
  if (!t) return;
  const on = !t.classList.contains('on');
  t.className = `toggle-track ${on ? 'on' : ''}`;
  _sd?.applyFavicon(on);
}

export function toggleSettingsPrivate() {
  const t = document.getElementById('s-private-track');
  if (!t) return;
  const on = !t.classList.contains('on');
  t.className = `toggle-track ${on ? 'on' : ''}`;
  document.body.classList.toggle('private-mode', on);
  renderThemePreview();
}

export function toggleSettingsQuiBars() {
  const t = document.getElementById('s-qui-bars-track');
  if (!_sd) return;
  const on = !t?.classList.contains('on');
  if (t) t.className = `toggle-track ${on ? 'on' : ''}`;
  applyQuiOptionsEnabled(on);
  _sd.applyQuiBarsVisible(on);
}

/** Grey out + deactivate the qui connection/instance options while the
 *  integration is toggled off (they'd have no visible effect anyway). */
function applyQuiOptionsEnabled(on: boolean) {
  document.getElementById('s-qui-options')?.classList.toggle('qui-options-disabled', !on);
}

export function toggleSettingsStatSources() {
  const t = document.getElementById('s-stat-src-track');
  if (!t) return;
  const on = !t.classList.contains('on');
  t.className = `toggle-track ${on ? 'on' : ''}`;
  _sd?.applyStatSources(on);
  renderThemePreview();
}

export function onQuiInstanceToggle() {
  if (!_sd) return;
  const enabledIds: number[] = [];
  document.querySelectorAll<HTMLInputElement>('.qui-inst-toggle:checked')
    .forEach(cb => enabledIds.push(parseInt(cb.value)));
  _sd.applyQuiInstances(enabledIds);
}

/** POST /api/defs/reload — re-read tracker definition files at runtime.
 *  Returns whether the reload succeeded, so the caller (main.ts) knows
 *  whether it's safe to chain the follow-up full refresh. */
export async function reloadDefs(toastFn?: (msg: string, type?: ToastType) => void): Promise<boolean> {
  const notify = toastFn ?? _sd?.toast;
  const btn = document.getElementById('s-reload-defs-btn') as HTMLButtonElement | null;
  if (btn) btn.disabled = true;
  const { ok, data } = await api.reloadDefs();
  if (btn) btn.disabled = false;
  const success = ok && data.ok;
  if (success) {
    _defsCache = null; // force re-fetch next time the add modal opens
    notify?.(`Definitions reloaded — ${data.trackers} trackers, ${data.types} types`, 'success');
  } else {
    notify?.('Failed to reload definitions', 'error');
  }
  for (const issue of data?.issues ?? []) {
    notify?.(`${issue.file}: ${issue.error}`, 'error');
  }
  return success;
}

// ── Auto-save (qui-style): main settings persist on modify ──────────────────
// The General / Display / Scraping / Integrations tabs save automatically a
// moment after any control changes — no Save button. Tabs whose content is an
// EDITOR (Trackers, Alerts) keep their explicit actions. Debounced so a burst
// of toggles becomes one PUT.
let _autoSaveTimer: ReturnType<typeof setTimeout> | null = null;

export function scheduleSettingsAutoSave(): void {
  if (!_sd) return; // settings page not initialised yet
  if (_autoSaveTimer) clearTimeout(_autoSaveTimer);
  _autoSaveTimer = setTimeout(() => {
    _autoSaveTimer = null;
    if (_sd) void saveSettings(_sd);
  }, 700);
}

/** Attach the auto-save listeners once (the panels are static DOM). */
export function wireSettingsAutoSave(): void {
  const relevant = (el: HTMLElement | null): boolean => {
    if (!el) return false;
    if (el.closest('[data-no-autosave]')) return false;
    if ((el as HTMLInputElement).type === 'file') return false; // config import picker
    if (el.id.startsWith('s-acct-')) return false;              // login/password fields
    return true;
  };
  for (const tab of ['general', 'display', 'scraping', 'qui']) {
    const panel = document.getElementById(`settings-tab-${tab}`);
    if (!panel) continue;
    panel.addEventListener('change', e => {
      if (relevant(e.target as HTMLElement)) scheduleSettingsAutoSave();
    });
    panel.addEventListener('click', e => {
      const hit = (e.target as HTMLElement).closest<HTMLElement>('.toggle-track, .theme-option');
      if (hit && relevant(hit)) scheduleSettingsAutoSave();
    });
  }
}

export async function saveSettings(deps: SettingsDeps) {
  const btn = document.getElementById('s-save-btn') as HTMLButtonElement | null;
  if (btn) { btn.disabled = true; btn.textContent = 'Saving…'; }

  const enabledIds: number[] = [];
  document.querySelectorAll<HTMLInputElement>('.qui-inst-toggle:checked')
    .forEach(cb => enabledIds.push(parseInt(cb.value)));

  const isOn = (id: string, fallback: boolean) => {
    const t = document.getElementById(id);
    return t ? t.classList.contains('on') : fallback;
  };

  // QUI key mask semantics: the field shows the mask sentinel when a key is
  // stored — sending it back unchanged keeps the stored key, a typed value
  // replaces it, and an EMPTY field clears it. Send the field value as-is.
  const quiKey = (document.getElementById('s-qui-key') as HTMLInputElement).value.trim();

  // PUT /api/settings is a FULL REPLACE — start from the current settings so
  // nothing gets zeroed, then overlay every form value.
  const payload: AppSettings = {
    ...appSettings,
    qui_url:               (document.getElementById('s-qui-url') as HTMLInputElement).value.trim(),
    qui_api_key:           quiKey,
    qui_enabled_instances: enabledIds,
    qui_bars_visible:      isOn('s-qui-bars-track', true),
    profile_auto_sync:     isOn('s-profile-sync-track', true),
    show_favicons:         isOn('s-favicon-track', false),
    private_mode:          isOn('s-private-track', false),
    show_stat_sources:     isOn('s-stat-src-track', false),
    show_pathway_etas:     isOn('s-pw-eta-track', true),
    show_trend_estimates:  isOn('s-trend-est-track', true),
    show_target_etas:      isOn('s-target-eta-track', true),
    show_rate_hovers:      isOn('s-rate-hover-track', true),
    show_unread_mail:          isOn('s-unread-mail-track', true),
    show_unread_notifications: isOn('s-unread-notif-track', true),
    show_tracker_rules:        isOn('s-tracker-rules-track', true),
    highlight_hnr:             isOn('s-hnr-highlight-track', true),
    update_check_auto:     (document.getElementById('s-update-auto') as HTMLInputElement | null)?.checked ?? false,
    trust_proxy_headers:   (document.getElementById('s-trust-proxy') as HTMLInputElement | null)?.checked ?? false,
    duration_format:       (document.querySelector<HTMLInputElement>('input[name="s-duration-format"]:checked')?.value ?? 'ym'),
    api_only_mode:         isOn('s-api-only-track', false),
    theme:                 _selectedThemeId === 'default' ? '' : _selectedThemeId,
    tracker_name_mode:     (document.querySelector<HTMLInputElement>('input[name="s-name-mode"]:checked')?.value ?? 'name'),
    group_name_style:      (document.querySelector<HTMLInputElement>('input[name="s-group-name-style"]:checked')?.value ?? 'plain'),
    username_style:        (document.querySelector<HTMLInputElement>('input[name="s-username-style"]:checked')?.value ?? 'plain'),
    backup_enabled:          isOn('s-backup-track', false),
    backup_frequency:        ((document.getElementById('s-backup-frequency') as HTMLSelectElement | null)?.value ?? 'weekly'),
    backup_keep:             Math.min(99, Math.max(1, parseInt((document.getElementById('s-backup-keep') as HTMLInputElement | null)?.value ?? '5', 10) || 5)),
    auto_interval:           ((document.getElementById('s-auto-interval') as HTMLInputElement | null)?.checked ?? false),
    scrape_interval_minutes: Math.max(60, parseInt((document.getElementById('s-scrape-interval') as HTMLInputElement | null)?.value ?? '120', 10) || 120),
    max_scrapes_per_day:     Math.max(0, parseInt((document.getElementById('s-max-scrapes') as HTMLInputElement | null)?.value ?? '0', 10) || 0),
    refresh_interval_minutes: Math.max(15, parseInt((document.getElementById('s-refresh-interval') as HTMLInputElement | null)?.value ?? '30', 10) || 30),
    qui_refresh_seconds:      Math.max(1,  parseInt((document.getElementById('s-qui-refresh') as HTMLInputElement | null)?.value ?? '10', 10) || 10),
  };

  const { ok } = await api.saveSettings(payload);
  if (btn) { btn.disabled = false; btn.textContent = 'Save Settings'; }
  if (ok) {
    deps.toast('Settings saved', 'success');
    await deps.loadSettings();
    // Re-sync the key field with the (re)masked value so a second save
    // round-trips safely instead of wiping the stored key.
    setVal('s-qui-key', appSettings.qui_api_key ?? '');
    setPlaceholder('s-qui-key', appSettings.qui_api_key ? `${MASKED_KEY} = keep current key` : 'Your QUI API key');
    await deps.loadQUIInstances();
    deps.renderQuiBarsWrapper();
    deps.renderTable();
    deps.renderGrid();
    // Re-arm the auto-refresh + qui timers so a changed cadence applies now.
    deps.rescheduleTimers();
    // Full-page settings: stay on the page — the toast is the confirmation.
    await deps.refreshQuiStatsWrapper();
  } else {
    deps.toast('Failed to save settings', 'error');
  }
}

// ─────────────────────────────────────────────────────────────────────────────
// Mock scenario switcher
// ─────────────────────────────────────────────────────────────────────────────

export async function switchMockScenario(
  trackerId: string,
  trackers: Tracker[],
  deps: {
    refreshSingle: (id: string) => Promise<void>;
    toast: (msg: string, type?: ToastType) => void;
  },
) {
  const sel = document.getElementById('modal-mock-scenario') as HTMLSelectElement;
  if (!sel) return;
  const scenario = sel.value;
  const { ok } = await api.updateTracker(trackerId, { mock_scenario: scenario });
  if (ok) {
    deps.toast(`Scenario → ${prettyScenario(scenario)}`, 'success');
    const t = trackers.find(x => x.id === trackerId);
    if (t) t.mock_scenario = scenario;
    await deps.refreshSingle(trackerId);
  } else {
    deps.toast('Failed to switch scenario', 'error');
  }
}

// ─────────────────────────────────────────────────────────────────────────────
// DOM helpers
// ─────────────────────────────────────────────────────────────────────────────

const MODAL_INPUT_IDS = [
  'modal-tracker-id', 'modal-name', 'modal-url', 'modal-username', 'modal-joindate',
  'modal-key', 'modal-session-cookie', 'modal-min-scrape-interval', 'modal-max-scrapes',
];

function clearModal() {
  MODAL_INPUT_IDS.forEach(id => {
    const el = document.getElementById(id) as HTMLInputElement;
    if (el) el.value = '';
  });
  // Reset the target builder + group summary between trackers.
  _editTargets = {};
  _targetSpecs = [];
  const rows = document.getElementById('modal-target-rows');
  if (rows) rows.innerHTML = '';
  renderGroupHint('', '');
  hide('modal-target-builder');
}

function setEl(id: string, text: string)        { const el = document.getElementById(id);                     if (el) el.textContent = text; }
function setVal(id: string, val: string)         { const el = document.getElementById(id) as HTMLInputElement; if (el) el.value = val; }
function getVal(id: string): string             { return (document.getElementById(id) as HTMLInputElement)?.value?.trim() ?? ''; }
function setPlaceholder(id: string, ph: string) { const el = document.getElementById(id) as HTMLInputElement; if (el) el.placeholder = ph; }
function show(id: string) { const el = document.getElementById(id); if (el) el.style.display = ''; }
function hide(id: string) { const el = document.getElementById(id); if (el) el.style.display = 'none'; }

// ── Target group summary (chips) ──────────────────────────────────────────

/** [label, value] pairs for a requirements set — drives the chip summary. */
function requirementPairs(req: GroupRequirements): [string, string][] {
  const pairs: [string, string][] = [];
  if (req.min_uploaded)     pairs.push(['Uploaded', req.min_uploaded]);
  if (req.min_downloaded)   pairs.push(['Downloaded', req.min_downloaded]);
  if (req.min_ratio)        pairs.push(['Ratio', String(req.min_ratio)]);
  if (req.min_seedtime)     pairs.push(['Seedtime', req.min_seedtime]);
  if (req.min_seed_size)    pairs.push(['Seed Size', req.min_seed_size]);
  if (req.min_uploads)      pairs.push(['Uploads', String(req.min_uploads)]);
  if (req.min_adoptions)    pairs.push(['Adoptions', String(req.min_adoptions)]);
  if (req.min_bonus_points) pairs.push(['Bonus', req.min_bonus_points.toLocaleString()]);
  if (req.min_age)          pairs.push(['Age', req.min_age]);
  for (const mc of req.min_counts ?? []) {
    if (mc.count > 0) pairs.push([mc.label || fieldLabel(mc.field), String(mc.count)]);
  }
  return pairs;
}

function chipRow(pairs: [string, string][]): string {
  if (!pairs.length) return '';
  return `<div class="target-chip-row">${pairs.map(([k, v]) =>
    `<span class="target-chip"><span class="target-chip-k">${esc(k)}</span><span class="target-chip-v">${esc(v)}</span></span>`
  ).join('')}</div>`;
}

/** Render the group hint panel for a given trackerKey + groupName.
 *  Pass groupName='' to clear the hint. */
function renderGroupHint(trackerKey: string, groupName: string): void {
  const hintEl = document.getElementById('modal-target-group-hint');
  if (!hintEl) return;

  if (!groupName) {
    hintEl.textContent = '';
    hintEl.style.display = 'none';
    return;
  }

  const groups = groupDefs[trackerKey] ?? [];
  const g = groups.find(gd => gd.name === groupName);
  if (!g) { hintEl.textContent = ''; hintEl.style.display = 'none'; return; }

  const req = g.requirements;
  const color   = g.style?.color   ?? '';
  const icon    = g.style?.icon    ?? '';
  const sparkle = g.style?.sparkle ?? false;
  const colorStyle   = color ? ` style="color:${color}"` : '';
  const iconHtml     = icon  ? `<i class="${icon}" aria-hidden="true"></i> ` : '';
  // sparkle class goes on the NAME SPAN only, not the outer flex div
  const sparkleAttr  = sparkle ? ' class="group-sparkle"' : '';

  // Perk icons with tooltips shown inline next to the group name
  const iconColor = color || 'var(--text3)';
  const perkIconsHtml = (g.perks ?? [])
    .filter(p => p.icon)
    .map(p =>
      `<span class="perk-icon-wrap" data-tip="${esc(p.label)}">` +
      `<i class="${esc(p.icon)}" style="color:${esc(iconColor)};font-size:11px;opacity:.85"></i>` +
      `<span class="perk-tip">${esc(p.label)}</span></span>`
    ).join('');

  const namePart =
    `<div class="target-group-hint-name" style="display:flex;align-items:center;gap:8px;flex-wrap:wrap">` +
    `<span${sparkleAttr}${colorStyle}>${iconHtml}${esc(g.name)}</span>` +
    (perkIconsHtml ? `<span class="card-perk-icons">${perkIconsHtml}</span>` : '') +
    `</div>`;

  let bodyPart = '';
  if (req.description) {
    bodyPart = `<div class="target-group-hint-desc">${esc(req.description)}</div>`;
  } else {
    const pairs = requirementPairs(req);
    bodyPart = pairs.length
      ? chipRow(pairs)
      : `<div class="target-group-hint-desc">No stat requirements for this group.</div>`;
    // any_of alternatives — base requirements above PLUS at least one of these
    if (req.any_of?.length) {
      bodyPart += `<div class="target-chip-anyof"><span class="target-chip-anyof-label">plus one of</span>${
        req.any_of.map(a => chipRow(requirementPairs(a)))
          .join(`<span class="target-chip-anyof-label">or</span>`)
      }</div>`;
    }
  }
  hintEl.innerHTML = `<div class="target-group-hint-wrap">${namePart}${bodyPart}</div>`;
  hintEl.style.display = '';
}
