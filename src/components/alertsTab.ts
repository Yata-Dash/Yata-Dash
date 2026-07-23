// components/alertsTab.ts — Settings → Alerts editor: webhook destinations
// and rules, both as collapsible rows (toggle/edit/delete, plus Dry run and a
// trackers→destinations scope callout on rules) that expand into edit forms.
// Tracker scope and "send to" both use a searchable multi-select (qui-style:
// chips + dropdown with search) so 100+ trackers stay manageable.
// State is held in memory and saved as a whole via PUT /api/notifications.
import * as api from '../api';
import { esc } from '../utils/format';
import { trackers } from '../state';
import type { AlertCondition, AlertRule, DigestConfig, DryRunResult, NotifyDestination } from '../types';
import type { ToastType } from './toast';

type Toast = (msg: string, type?: ToastType) => void;
let toast: Toast = () => {};

let destinations: NotifyDestination[] = [];
let rules: AlertRule[] = [];
let digest: DigestConfig = { enabled: false, weekday: 1, hour: 9, destinations: [] };
let editingDest = -1;   // index of the destination shown in its edit form (-1 = none)
let editingRule = -1;   // index of the rule shown in its edit form (-1 = none)
let pendingDelete: string | null = null; // dest/rule id awaiting inline delete confirm
let dryRunFor: string | null = null;          // rule id whose dry-run box is shown
let dryRunResults: DryRunResult[] | null = null; // null while the request is in flight
let digestPreviewText: string | null = null; // null = no preview shown; '' is a valid (quiet-week) preview
let wired = false;

// ── Field & operator catalogs (mirror internal/notify/engine.go) ────────────
type FieldType = 'numeric' | 'size' | 'bool' | 'string' | 'event';
interface FieldDef { value: string; label: string; type: FieldType; }

const FIELDS: FieldDef[] = [
  { value: 'ratio',            label: 'Ratio',              type: 'numeric' },
  { value: 'buffer',           label: 'Buffer (size)',      type: 'size' },
  { value: 'uploaded',         label: 'Uploaded (size)',    type: 'size' },
  { value: 'seed_size',        label: 'Seed size (size)',   type: 'size' },
  { value: 'warnings',         label: 'Warnings',           type: 'numeric' },
  { value: 'hit_and_runs',     label: 'Hit & runs',         type: 'numeric' },
  { value: 'bonus_points',     label: 'Bonus points',       type: 'numeric' },
  { value: 'seeding',          label: 'Seeding',            type: 'numeric' },
  { value: 'leeching',         label: 'Leeching',           type: 'numeric' },
  { value: 'avg_seed_time',    label: 'Avg seed time (days)', type: 'numeric' },
  // Standing guards — predictive decline signals (internal/stats's
  // DeclineSignals). Silent (never match) until enough history exists.
  { value: 'ratio_min_eta_days',   label: 'Ratio hits tracker min within (days)', type: 'numeric' },
  { value: 'buffer_zero_eta_days', label: 'Buffer runs out within (days)',        type: 'numeric' },
  { value: 'seed_size_drop_7d_pct', label: 'Seed size drop over 7d (%)',          type: 'numeric' },
  { value: 'seeding_drop_7d_pct',  label: 'Seeding count drop over 7d (%)',       type: 'numeric' },
  { value: 'freeleech_active', label: 'Active event (freeleech / announcement)', type: 'bool' },
  { value: 'unread_mail',          label: 'Unread mail (inbox)', type: 'bool' },
  { value: 'unread_notifications', label: 'Unread notifications (bell)', type: 'bool' },
  { value: 'reachable',        label: 'Tracker reachable',  type: 'bool' },
  { value: 'goal_behind_pace', label: 'Behind goal pace',   type: 'bool' },
  { value: 'group',            label: 'Group / class',      type: 'string' },
  // One-shot events (fire at the moment they happen, not polled) — see
  // notify.EvaluateEvent/EvaluateTargets.
  { value: 'promoted',   label: 'Promoted (group moved up)',   type: 'event' },
  { value: 'demoted',    label: 'Demoted (group moved down)',  type: 'event' },
  { value: 'target_met', label: 'Target met (any target row)', type: 'event' },
];

const NUM_OPS = [
  { value: 'gt', label: '>' }, { value: 'gte', label: '≥' },
  { value: 'lt', label: '<' }, { value: 'lte', label: '≤' },
  { value: 'eq', label: '=' }, { value: 'ne', label: '≠' },
];
const BOOL_OPS = [{ value: 'is_true', label: 'is true' }, { value: 'is_false', label: 'is false' }];
const STR_OPS = [{ value: 'changed', label: 'changed' }];
const EVENT_OPS = [{ value: 'is_true', label: 'occurs' }];

function fieldDef(name: string): FieldDef { return FIELDS.find(f => f.value === name) ?? FIELDS[0]; }
function opsFor(type: FieldType) {
  return type === 'bool' ? BOOL_OPS : type === 'string' ? STR_OPS : type === 'event' ? EVENT_OPS : NUM_OPS;
}
function genId(): string { return Math.random().toString(16).slice(2, 10) + Date.now().toString(16).slice(-6); }
function opt(value: string, label: string, sel: string): string {
  return `<option value="${esc(value)}"${value === sel ? ' selected' : ''}>${esc(label)}</option>`;
}
const DEST_TYPE_LABEL: Record<string, string> = { discord: 'Discord', telegram: 'Telegram', gotify: 'Gotify', generic: 'Generic JSON' };

// ── Reusable searchable multi-select (chips + dropdown) ─────────────────────
// 'digest-dest' reuses the exact same component as a rule's 'dest' select,
// but reads/writes the standalone digest.destinations array instead of a
// rule's — ri is unused for it (callers pass -1).
type MsKind = 'tracker' | 'dest' | 'digest-dest';
interface MsOption { id: string; label: string; }

function msOptions(kind: MsKind): MsOption[] {
  return kind === 'tracker'
    ? trackers.map(t => ({ id: t.id, label: t.name }))
    : destinations.map(d => ({ id: d.id, label: d.name || DEST_TYPE_LABEL[d.type] || d.type }));
}
function msArr(kind: MsKind, ri: number): string[] {
  if (kind === 'tracker') return rules[ri].tracker_ids;
  if (kind === 'digest-dest') return digest.destinations;
  return rules[ri].destinations;
}

function msChips(kind: MsKind, ri: number, placeholder: string): string {
  const arr = msArr(kind, ri);
  const opts = msOptions(kind);
  if (!arr.length) return `<span class="ms-placeholder">${esc(placeholder)}</span>`;
  return arr.map(id => {
    const o = opts.find(x => x.id === id);
    const label = o ? o.label : id;
    return `<span class="ms-chip">${esc(label)}<button type="button" data-action="ms-remove" data-kind="${kind}" data-rule="${ri}" data-id="${esc(id)}">✕</button></span>`;
  }).join('');
}

function msHtml(kind: MsKind, ri: number, placeholder: string): string {
  const arr = new Set(msArr(kind, ri));
  const opts = msOptions(kind);
  const optsHtml = opts.length
    ? opts.map(o => `<div class="ms-option${arr.has(o.id) ? ' selected' : ''}" data-action="ms-toggle" data-kind="${kind}" data-rule="${ri}" data-id="${esc(o.id)}" data-label="${esc(o.label.toLowerCase())}"><span>${esc(o.label)}</span><span class="ms-check">✓</span></div>`).join('')
    : `<div class="ms-empty">${kind === 'tracker' ? 'No trackers configured' : 'Add a destination first'}</div>`;
  return `<div class="ms" data-kind="${kind}" data-rule="${ri}">
    <div class="ms-control" data-action="ms-open"><div class="ms-chips">${msChips(kind, ri, placeholder)}</div><span class="ms-caret">▾</span></div>
    <div class="ms-dropdown" hidden>
      <input class="ms-search" type="text" placeholder="Search…" data-action="ms-search"/>
      <div class="ms-options">${optsHtml}</div>
    </div>
  </div>`;
}

/** Refresh one multi-select's chips + option highlights in place (keeps the
 *  dropdown open and the search text intact). */
function msRefresh(msEl: HTMLElement): void {
  const kind = msEl.dataset['kind'] as MsKind;
  const ri = Number(msEl.dataset['rule']);
  const arr = new Set(msArr(kind, ri));
  const chips = msEl.querySelector('.ms-chips');
  if (chips) chips.innerHTML = msChips(kind, ri, kind === 'tracker' ? 'All trackers' : 'All enabled destinations');
  msEl.querySelectorAll<HTMLElement>('.ms-option').forEach(o =>
    o.classList.toggle('selected', arr.has(o.dataset['id'] ?? '')));
}

// ── Load / save / export / import ───────────────────────────────────────────
let loaded = false; // fetched from the server at least once this page-load

/** Called on every Alerts-tab activation. Fetches only the FIRST time — the
 *  panel's DOM persists across tab switches, and re-fetching would silently
 *  discard unsaved in-memory edits (rules/destinations live here until the
 *  user hits Save alerts). Import uses reload() to force a re-fetch. */
export async function loadAlerts(deps: { toast: Toast }): Promise<void> {
  toast = deps.toast;
  wire();
  if (loaded) return;
  await reload();
}

/** Fetch destinations/rules from the server and re-render, dropping any
 *  in-memory edits and editor/dry-run state. */
async function reload(): Promise<void> {
  const { ok, data } = await api.fetchNotifications();
  if (ok) {
    destinations = data.destinations ?? [];
    rules = (data.rules ?? []).map(r => ({
      ...r,
      tracker_ids: r.tracker_ids?.length ? r.tracker_ids : (r.tracker_id ? [r.tracker_id] : []),
      tracker_mode: r.tracker_mode || 'include',
      destinations: r.destinations ?? [],
      conditions: r.conditions ?? [],
    }));
    digest = data.digest ?? { enabled: false, weekday: 1, hour: 9, destinations: [] };
    digest.destinations = digest.destinations ?? [];
    loaded = true;
  }
  editingDest = -1;
  editingRule = -1;
  pendingDelete = null;
  dryRunFor = null;
  dryRunResults = null;
  digestPreviewText = null;
  render();
}

/** Inline delete confirmation (same pattern as the trackers table) — a
 *  one-click Delete is too easy to fat-finger on a rule built up over time. */
function confirmDeleteHtml(kind: 'dest' | 'rule', idx: number, name: string): string {
  return `<span class="trk-del-confirm">Delete <strong>${esc(name)}</strong>?</span>
    <button type="button" class="btn btn-danger btn-sm" data-action="remove-${kind}" data-${kind === 'dest' ? 'dest' : 'rule'}="${idx}">Delete</button>
    <button type="button" class="btn btn-ghost btn-sm" data-action="cancel-remove">Cancel</button>`;
}

export async function saveAlerts(): Promise<void> {
  collectFromDOM();
  const { ok } = await api.saveNotifications({ destinations, rules, digest });
  toast(ok ? 'Alerts saved' : 'Failed to save alerts', ok ? 'success' : 'error');
}

export function exportAlerts(): void {
  window.open(api.notificationsExportUrl(), '_blank');
}

export async function importAlertsFile(input: HTMLInputElement): Promise<void> {
  const file = input.files?.[0];
  if (!file) return;
  const text = await file.text();
  input.value = '';
  let parsed: { destinations?: NotifyDestination[]; rules?: AlertRule[] };
  try { parsed = JSON.parse(text); } catch { toast('Import failed: not valid JSON', 'error'); return; }
  if (!parsed || (!Array.isArray(parsed.destinations) && !Array.isArray(parsed.rules))) {
    toast('Import failed: not an alerts file', 'error'); return;
  }
  if (!confirm('Import alerts?\n\nThis REPLACES your current destinations and rules. Any new rule already met will notify immediately.')) return;
  // digest isn't part of the share-safe export/import format (it has no
  // secrets to strip) — carry the CURRENT schedule forward so importing an
  // alerts file never resets the weekly digest as a side effect.
  const { ok } = await api.saveNotifications({ destinations: parsed.destinations ?? [], rules: parsed.rules ?? [], digest });
  if (ok) { toast('Alerts imported', 'success'); await reload(); }
  else toast('Import failed', 'error');
}

// ── Rendering ───────────────────────────────────────────────────────────────
function render(): void {
  const root = document.getElementById('alerts-content');
  if (!root) return;
  root.innerHTML = `
    <div class="alerts-subhead">Destinations</div>
    <div style="font-size:11px;color:var(--text3);margin-bottom:8px">Where alerts are sent. Webhook URLs can contain secrets, so they're hidden until you Edit.</div>
    ${destinations.map((d, i) => i === editingDest ? renderDestEdit(d, i) : renderDestRow(d, i)).join('') || '<div style="font-size:12px;color:var(--text3);font-style:italic;margin-bottom:6px">No destinations yet.</div>'}
    <button type="button" class="btn btn-ghost btn-sm" data-action="add-dest">+ Add destination</button>

    ${renderDigestCard()}

    <div class="alerts-subhead" style="margin-top:20px">Rules</div>
    <div style="font-size:11px;color:var(--text3);margin-bottom:8px">Each rule fires when its conditions become true for a tracker (it won't re-fire until the condition clears). A newly-added rule that's already true notifies immediately.</div>
    ${rules.map((r, ri) => ri === editingRule ? renderRule(r, ri) : renderRuleRow(r, ri)).join('') || '<div style="font-size:12px;color:var(--text3);font-style:italic;margin-bottom:6px">No rules yet.</div>'}
    <button type="button" class="btn btn-ghost btn-sm" data-action="add-rule">+ Add rule</button>`;
}

// ── Weekly digest card ───────────────────────────────────────────────────────
const WEEKDAY_LABELS = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];

function renderDigestCard(): string {
  const dayOpts = WEEKDAY_LABELS.map((label, i) => opt(String(i), label, String(digest.weekday))).join('');
  const hourOpts = Array.from({ length: 24 }, (_, h) => opt(String(h), `${String(h).padStart(2, '0')}:00`, String(digest.hour))).join('');
  return `<div class="alerts-subhead" style="margin-top:20px">Weekly digest</div>
    <div style="font-size:11px;color:var(--text3);margin-bottom:8px">A weekly summary of per-tracker deltas, target/goal progress, promotions/demotions, and newly requirements-met pathway targets. A quiet week still sends a short heartbeat so silence never means "it broke".</div>
    <div class="alerts-card">
      <div class="alerts-row" style="align-items:center;gap:12px">
        <label class="alerts-inline"><input type="checkbox" id="digest-enabled" ${digest.enabled ? 'checked' : ''}/> Enabled</label>
        <select class="form-input" id="digest-weekday" style="width:auto">${dayOpts}</select>
        <select class="form-input" id="digest-hour" style="width:auto">${hourOpts}</select>
      </div>
      <div class="alerts-row" style="margin-top:8px;align-items:center;gap:8px">
        <span style="font-size:11px;color:var(--text3);width:60px">Send to</span>
        ${msHtml('digest-dest', -1, 'All enabled destinations')}
      </div>
      <div class="alerts-row" style="margin-top:8px;justify-content:flex-end;gap:6px;align-items:center">
        <button type="button" class="btn btn-ghost btn-sm" data-action="digest-preview">Preview</button>
        <button type="button" class="btn btn-primary btn-sm" data-action="digest-send">Send now</button>
      </div>
    </div>
    ${digestPreviewText !== null ? `<div class="alerts-dryrun"><pre style="white-space:pre-wrap;margin:0;font:inherit">${esc(digestPreviewText)}</pre></div>` : ''}`;
}

// ── Rule scope callouts (collapsed rows) ─────────────────────────────────────
// "ALL", a short name list ("Seedpool, Aither"), or a count ("6 trackers").
function scopeSummary(r: AlertRule): string {
  const names = r.tracker_ids.map(id => trackers.find(t => t.id === id)?.name ?? id);
  const list = names.length <= 3 ? names.join(', ') : `${names.length} trackers`;
  if (r.tracker_mode === 'exclude') return names.length ? `All except ${list}` : 'ALL';
  return names.length ? list : 'ALL';
}

function destSummary(r: AlertRule): string {
  const names = r.destinations.map(id => {
    const d = destinations.find(x => x.id === id);
    return d ? (d.name || DEST_TYPE_LABEL[d.type] || d.type) : id;
  });
  if (!names.length) return 'all destinations';
  return names.length <= 2 ? names.join(', ') : `${names.length} destinations`;
}

function renderRuleRow(r: AlertRule, ri: number): string {
  const n = r.conditions.length;
  const actions = pendingDelete === r.id
    ? confirmDeleteHtml('rule', ri, r.name || '(unnamed rule)')
    : `<button type="button" class="btn btn-ghost btn-sm" data-action="dryrun-rule" data-rule="${ri}" title="Evaluate against current stats without sending anything">Dry run</button>
      <button type="button" class="btn btn-ghost btn-sm" data-action="edit-rule" data-rule="${ri}">Edit</button>
      <button type="button" class="btn btn-danger btn-sm" data-action="ask-remove-rule" data-rule="${ri}">Delete</button>`;
  return `<div class="alerts-dest-row alerts-rule-row${r.enabled ? '' : ' rule-row-disabled'}" data-rule="${ri}">
    <div class="toggle-track ${r.enabled ? 'on' : ''}" data-action="rule-toggle" data-rule="${ri}" title="Enabled"><div class="toggle-thumb"></div></div>
    <span class="dest-row-name">${esc(r.name || '(unnamed rule)')}</span>
    <span class="dest-row-type">${n} condition${n === 1 ? '' : 's'}</span>
    <span class="rule-row-scope" title="Trackers → destinations">${esc(scopeSummary(r))} <span class="rule-row-arrow">→</span> ${esc(destSummary(r))}</span>
    <div style="margin-left:auto;display:flex;gap:6px;flex-shrink:0;align-items:center">${actions}</div>
  </div>${dryRunFor === r.id ? renderDryRunBox(r) : ''}`;
}

function renderDryRunBox(r: AlertRule): string {
  // Dry run deliberately works on disabled rules (test before enabling) —
  // but say so, or "would fire" reads as if the rule is live.
  const disabledNote = r.enabled ? ''
    : `<div class="dryrun-disabled-note">This rule is currently <strong>disabled</strong> — it will not actually fire until you enable it.</div>`;
  if (!dryRunResults) return `<div class="alerts-dryrun">Evaluating…</div>`;
  if (!dryRunResults.length) return `<div class="alerts-dryrun">${disabledNote}No enabled trackers are in this rule's scope.</div>`;
  const fired = dryRunResults.filter(x => x.matched);
  const head = fired.length
    ? `Would fire now for <strong>${esc(fired.map(f => f.tracker_name).join(', '))}</strong>. Nothing was sent.`
    : `Would not fire for any tracker right now. Nothing was sent.`;
  const rows = dryRunResults.map(x =>
    `<div class="dryrun-row${x.matched ? ' hit' : ''}"><span class="dryrun-mark">${x.matched ? '✓' : '–'}</span><span class="dryrun-name">${esc(x.tracker_name)}</span><span class="dryrun-detail">${esc(x.detail)}</span></div>`).join('');
  return `<div class="alerts-dryrun">${disabledNote}${head}${rows}</div>`;
}

function renderDestRow(d: NotifyDestination, i: number): string {
  const actions = pendingDelete === d.id
    ? confirmDeleteHtml('dest', i, d.name || '(unnamed)')
    : `<button type="button" class="btn btn-ghost btn-sm" data-action="test-dest" data-dest="${i}">Test</button>
      <button type="button" class="btn btn-ghost btn-sm" data-action="edit-dest" data-dest="${i}">Edit</button>
      <button type="button" class="btn btn-danger btn-sm" data-action="ask-remove-dest" data-dest="${i}">Delete</button>`;
  return `<div class="alerts-dest-row" data-dest="${i}">
    <div class="toggle-track ${d.enabled ? 'on' : ''}" data-action="dest-toggle" data-dest="${i}" title="Enabled"><div class="toggle-thumb"></div></div>
    <span class="dest-row-name">${esc(d.name || '(unnamed)')}</span>
    <span class="dest-row-type">${esc(DEST_TYPE_LABEL[d.type] || d.type)}</span>
    <div style="margin-left:auto;display:flex;gap:6px;align-items:center">${actions}</div>
  </div>`;
}

function renderDestEdit(d: NotifyDestination, i: number): string {
  return `<div class="alerts-card" data-dest="${i}">
    <div class="alerts-row">
      <input class="form-input dest-name" placeholder="Name (e.g. My Discord)" value="${esc(d.name)}" style="flex:2"/>
      <select class="form-input dest-type" data-action="dest-type" data-dest="${i}" style="flex:1">
        ${opt('discord', 'Discord', d.type)}${opt('telegram', 'Telegram', d.type)}${opt('gotify', 'Gotify', d.type)}${opt('generic', 'Generic JSON', d.type)}
      </select>
      <label class="alerts-inline"><input type="checkbox" class="dest-enabled" ${d.enabled ? 'checked' : ''}/> Enabled</label>
    </div>
    <div class="alerts-row" style="margin-top:6px">
      <input class="form-input dest-url" placeholder="${d.type === 'gotify' ? 'Gotify base URL (e.g. https://gotify.example)' : 'Webhook URL'}" value="${esc(d.url)}" style="flex:3;${d.type === 'telegram' ? 'display:none' : ''}"/>
      <input class="form-input dest-token" placeholder="${d.type === 'gotify' ? 'App token' : 'Bot token'}" value="${esc(d.token)}" style="flex:2;${(d.type === 'gotify' || d.type === 'telegram') ? '' : 'display:none'}"/>
      <input class="form-input dest-chatid" placeholder="Chat ID" value="${esc(d.chat_id)}" style="flex:1;${d.type === 'telegram' ? '' : 'display:none'}"/>
    </div>
    <div class="alerts-row" style="margin-top:6px;justify-content:flex-end;gap:6px;align-items:center">
      ${pendingDelete === d.id
        ? confirmDeleteHtml('dest', i, d.name || '(unnamed)')
        : `<button type="button" class="btn btn-ghost btn-sm" data-action="test-dest" data-dest="${i}">Test</button>
      <button type="button" class="btn btn-danger btn-sm" data-action="ask-remove-dest" data-dest="${i}">Delete</button>
      <button type="button" class="btn btn-primary btn-sm" data-action="done-dest" data-dest="${i}">Done</button>`}
    </div>
  </div>`;
}

function renderRule(r: AlertRule, ri: number): string {
  const mode = r.tracker_mode === 'exclude' ? 'exclude' : 'include';
  const scopeHint = mode === 'exclude'
    ? 'All trackers except those selected.'
    : (r.tracker_ids.length === 0 ? 'No trackers selected = all trackers.' : 'Only the selected trackers.');
  return `<div class="alerts-card" data-rule="${ri}">
    <div class="alerts-row">
      <input class="form-input rule-name" placeholder="Rule name (e.g. Low ratio)" value="${esc(r.name)}" style="flex:2"/>
      <label class="alerts-inline"><input type="checkbox" class="rule-enabled" ${r.enabled ? 'checked' : ''}/> Enabled</label>
    </div>
    <div class="alerts-row" style="margin-top:8px;align-items:center;gap:8px">
      <span style="font-size:11px;color:var(--text3);width:60px">Trackers</span>
      <div class="seg">
        <button type="button" class="seg-btn${mode === 'include' ? ' active' : ''}" data-action="rule-mode" data-mode="include">Include</button>
        <button type="button" class="seg-btn${mode === 'exclude' ? ' active' : ''}" data-action="rule-mode" data-mode="exclude">Exclude</button>
      </div>
      ${msHtml('tracker', ri, 'All trackers')}
    </div>
    <div style="font-size:11px;color:var(--text3);margin:4px 0 8px 68px">${scopeHint}</div>
    <div class="alerts-row" style="align-items:center;gap:8px">
      <span style="font-size:11px;color:var(--text3);width:60px">Match</span>
      <select class="form-input rule-match" style="width:auto">${opt('all', 'all (AND)', r.match)}${opt('any', 'any (OR)', r.match)}</select>
      <span style="font-size:11px;color:var(--text3)">of these conditions</span>
    </div>
    <div class="alerts-conds">
      ${r.conditions.map((c, ci) => renderCond(c, ri, ci)).join('')}
      <button type="button" class="btn btn-ghost btn-sm" data-action="add-cond" data-rule="${ri}">+ Add condition</button>
    </div>
    <div class="alerts-row" style="margin-top:8px;align-items:center;gap:8px">
      <span style="font-size:11px;color:var(--text3);width:60px">Send to</span>
      ${msHtml('dest', ri, 'All enabled destinations')}
      <span style="font-size:11px;color:var(--text3);margin-left:auto">Cooldown (min)</span>
      <input class="form-input rule-cooldown" type="number" min="0" value="${r.cooldown_minutes || 0}" style="width:70px"/>
    </div>
    <div class="alerts-row" style="margin-top:8px;justify-content:flex-end;gap:6px;align-items:center">
      ${pendingDelete === r.id
        ? confirmDeleteHtml('rule', ri, r.name || '(unnamed rule)')
        : `<button type="button" class="btn btn-ghost btn-sm" data-action="dryrun-rule" data-rule="${ri}" title="Evaluate against current stats without sending anything">Dry run</button>
      <button type="button" class="btn btn-danger btn-sm" data-action="ask-remove-rule" data-rule="${ri}">Delete</button>
      <button type="button" class="btn btn-primary btn-sm" data-action="done-rule" data-rule="${ri}">Done</button>`}
    </div>
  </div>${dryRunFor === r.id ? renderDryRunBox(r) : ''}`;
}

function renderCond(c: AlertCondition, ri: number, ci: number): string {
  const fd = fieldDef(c.field);
  const fieldSel = `<select class="form-input cond-field" data-action="cond-field" data-rule="${ri}" data-cond="${ci}" style="flex:2">${FIELDS.map(f => opt(f.value, f.label, c.field)).join('')}</select>`;
  const opSel = `<select class="form-input cond-op" style="flex:1">${opsFor(fd.type).map(o => opt(o.value, o.label, c.op)).join('')}</select>`;
  const needsValue = fd.type === 'numeric' || fd.type === 'size';
  const valInput = `<input class="form-input cond-value" placeholder="${fd.type === 'size' ? 'e.g. 1 TiB' : 'value'}" value="${esc(c.value)}" style="flex:1;${needsValue ? '' : 'visibility:hidden'}"/>`;
  return `<div class="alerts-row cond-row" data-rule="${ri}" data-cond="${ci}" style="margin-top:6px;gap:6px">
    ${fieldSel}${opSel}${valInput}
    <button type="button" class="btn btn-ghost btn-sm" data-action="remove-cond" data-rule="${ri}" data-cond="${ci}">✕</button>
  </div>`;
}

// ── Collect DOM → model (selections live in the model, not the DOM) ──────────
function collectFromDOM(): void {
  const root = document.getElementById('alerts-content');
  if (!root) return;
  // Destinations: read fields only from the edit form; keep model for rows.
  destinations = [...root.querySelectorAll<HTMLElement>('[data-dest]')]
    .filter(el => el.classList.contains('alerts-dest-row') || el.classList.contains('alerts-card'))
    .map((el, i) => {
      const nameInput = el.querySelector<HTMLInputElement>('.dest-name');
      if (!nameInput) return destinations[i]; // collapsed row — model is current
      return {
        id: destinations[i]?.id || genId(),
        name: nameInput.value.trim(),
        type: el.querySelector<HTMLSelectElement>('.dest-type')?.value ?? 'discord',
        url: el.querySelector<HTMLInputElement>('.dest-url')?.value.trim() ?? '',
        token: el.querySelector<HTMLInputElement>('.dest-token')?.value.trim() ?? '',
        chat_id: el.querySelector<HTMLInputElement>('.dest-chatid')?.value.trim() ?? '',
        enabled: el.querySelector<HTMLInputElement>('.dest-enabled')?.checked ?? false,
      };
    });

  // Rules: read fields only from the edit card; collapsed rows keep the model.
  rules = [...root.querySelectorAll<HTMLElement>('.alerts-rule-row[data-rule], .alerts-card[data-rule]')].map((el, i) => {
    const q = <T extends HTMLElement>(s: string) => el.querySelector<T>(s);
    if (!q<HTMLInputElement>('.rule-name')) return rules[i]; // collapsed row — model is current
    const conds: AlertCondition[] = [...el.querySelectorAll<HTMLElement>('.cond-row')].map(cr => ({
      field: cr.querySelector<HTMLSelectElement>('.cond-field')?.value ?? 'ratio',
      op: cr.querySelector<HTMLSelectElement>('.cond-op')?.value ?? 'lt',
      value: cr.querySelector<HTMLInputElement>('.cond-value')?.value.trim() ?? '',
    }));
    const mode = el.querySelector<HTMLElement>('[data-action="rule-mode"].active')?.dataset['mode'] ?? 'include';
    return {
      id: rules[i]?.id || genId(),
      name: q<HTMLInputElement>('.rule-name')?.value.trim() ?? '',
      enabled: q<HTMLInputElement>('.rule-enabled')?.checked ?? false,
      tracker_ids: rules[i]?.tracker_ids ?? [],   // managed by the multi-select
      tracker_mode: mode,
      destinations: rules[i]?.destinations ?? [],  // managed by the multi-select
      match: q<HTMLSelectElement>('.rule-match')?.value ?? 'all',
      conditions: conds,
      cooldown_minutes: parseInt(q<HTMLInputElement>('.rule-cooldown')?.value ?? '0', 10) || 0,
    };
  });

  // Digest: destinations are managed in place by the multi-select (see
  // msArr's 'digest-dest' case) — only the plain fields need reading here.
  const digestEnabled = root.querySelector<HTMLInputElement>('#digest-enabled');
  if (digestEnabled) {
    digest = {
      ...digest,
      enabled: digestEnabled.checked,
      weekday: Number(root.querySelector<HTMLSelectElement>('#digest-weekday')?.value ?? digest.weekday),
      hour: Number(root.querySelector<HTMLSelectElement>('#digest-hour')?.value ?? digest.hour),
    };
  }
}

// ── Event delegation ─────────────────────────────────────────────────────────
function wire(): void {
  if (wired) return;
  wired = true;
  const panel = document.getElementById('settings-tab-alerts');

  panel?.addEventListener('click', e => {
    const el = (e.target as HTMLElement).closest<HTMLElement>('[data-action]');
    if (!el) return;
    const action = el.dataset['action'];
    const ri = Number(el.dataset['rule']);
    const ci = Number(el.dataset['cond']);
    const di = Number(el.dataset['dest']);
    const kind = el.dataset['kind'] as MsKind;
    const id = el.dataset['id'] ?? '';
    switch (action) {
      // ── multi-select (in place; no re-render so the dropdown stays open) ──
      case 'ms-open': {
        const ms = el.closest<HTMLElement>('.ms'); if (!ms) return;
        const dd = ms.querySelector<HTMLElement>('.ms-dropdown'); if (!dd) return;
        const willOpen = dd.hidden;
        document.querySelectorAll<HTMLElement>('.ms-dropdown').forEach(d => { d.hidden = true; });
        dd.hidden = !willOpen;
        if (willOpen) ms.querySelector<HTMLInputElement>('.ms-search')?.focus();
        return;
      }
      case 'ms-toggle': {
        const arr = msArr(kind, ri);
        const idx = arr.indexOf(id);
        if (idx >= 0) arr.splice(idx, 1); else arr.push(id);
        const ms = el.closest<HTMLElement>('.ms'); if (ms) msRefresh(ms);
        return;
      }
      case 'ms-remove': {
        const arr = msArr(kind, ri);
        const idx = arr.indexOf(id);
        if (idx >= 0) arr.splice(idx, 1);
        const ms = el.closest<HTMLElement>('.ms'); if (ms) msRefresh(ms);
        return;
      }
      // ── tracker include/exclude segment (in place) ──
      case 'rule-mode':
        el.closest('.alerts-card')?.querySelectorAll<HTMLElement>('[data-action="rule-mode"]')
          .forEach(b => b.classList.toggle('active', b === el));
        return;
      // ── destinations ──
      case 'dest-toggle':
        if (destinations[di]) { destinations[di].enabled = !destinations[di].enabled; el.classList.toggle('on', destinations[di].enabled); }
        return;
      case 'edit-dest': collectFromDOM(); editingDest = di; render(); break;
      case 'done-dest': collectFromDOM(); editingDest = -1; render(); break;
      case 'add-dest': collectFromDOM(); destinations.push({ id: genId(), name: '', type: 'discord', url: '', token: '', chat_id: '', enabled: true }); editingDest = destinations.length - 1; render(); break;
      case 'ask-remove-dest': collectFromDOM(); pendingDelete = destinations[di]?.id ?? null; render(); break;
      case 'cancel-remove': pendingDelete = null; render(); break;
      case 'remove-dest': collectFromDOM(); pendingDelete = null; destinations.splice(di, 1); if (editingDest === di) editingDest = -1; else if (editingDest > di) editingDest--; render(); break;
      case 'test-dest': void testDest(di); break;
      // ── rules / conditions ──
      case 'rule-toggle':
        if (rules[ri]) {
          rules[ri].enabled = !rules[ri].enabled;
          el.classList.toggle('on', rules[ri].enabled);
          el.closest('.alerts-rule-row')?.classList.toggle('rule-row-disabled', !rules[ri].enabled);
        }
        return;
      case 'edit-rule': collectFromDOM(); editingRule = ri; render(); break;
      case 'done-rule': collectFromDOM(); editingRule = -1; render(); break;
      case 'add-rule': collectFromDOM(); rules.push({ id: genId(), name: '', enabled: true, tracker_ids: [], tracker_mode: 'include', match: 'all', conditions: [{ field: 'ratio', op: 'lt', value: '1.0' }], destinations: [], cooldown_minutes: 0 }); editingRule = rules.length - 1; render(); break;
      case 'ask-remove-rule': collectFromDOM(); pendingDelete = rules[ri]?.id ?? null; render(); break;
      case 'remove-rule': {
        collectFromDOM();
        pendingDelete = null;
        if (dryRunFor === rules[ri]?.id) { dryRunFor = null; dryRunResults = null; }
        rules.splice(ri, 1);
        if (editingRule === ri) editingRule = -1; else if (editingRule > ri) editingRule--;
        render();
        break;
      }
      case 'dryrun-rule': void runDryRun(ri); break;
      case 'add-cond': collectFromDOM(); rules[ri]?.conditions.push({ field: 'ratio', op: 'lt', value: '1.0' }); render(); break;
      case 'remove-cond': collectFromDOM(); rules[ri]?.conditions.splice(ci, 1); render(); break;
      case 'save-alerts': void saveAlerts(); break;
      // ── weekly digest ──
      case 'digest-preview': void previewDigest(); break;
      case 'digest-send': void sendDigestNowClick(); break;
    }
  });

  panel?.addEventListener('input', e => {
    const el = (e.target as HTMLElement).closest<HTMLElement>('[data-action="ms-search"]');
    if (!el) return;
    const q = (el as HTMLInputElement).value.trim().toLowerCase();
    el.closest('.ms')?.querySelectorAll<HTMLElement>('.ms-option').forEach(o => {
      o.style.display = !q || (o.dataset['label'] ?? '').includes(q) ? '' : 'none';
    });
  });

  panel?.addEventListener('change', e => {
    const el = (e.target as HTMLElement).closest<HTMLElement>('[data-action]');
    if (!el) return;
    const action = el.dataset['action'];
    if (action === 'dest-type') { collectFromDOM(); render(); return; }
    if (action === 'cond-field') {
      collectFromDOM();
      const ri = Number(el.dataset['rule']); const ci = Number(el.dataset['cond']);
      const c = rules[ri]?.conditions[ci];
      if (c) { const ops = opsFor(fieldDef(c.field).type); if (!ops.some(o => o.value === c.op)) c.op = ops[0].value; }
      render();
    }
  });

  // Close any open multi-select dropdown when clicking outside it.
  document.addEventListener('click', e => {
    const t = e.target as HTMLElement;
    const own = t.closest?.('.ms');
    document.querySelectorAll<HTMLElement>('.ms-dropdown').forEach(dd => {
      if (dd.parentElement !== own) dd.hidden = true;
    });
  });
  // Escape closes any open multi-select dropdown too — matches detail.ts's
  // Charts menu.
  document.addEventListener('keydown', e => {
    if (e.key !== 'Escape') return;
    document.querySelectorAll<HTMLElement>('.ms-dropdown').forEach(dd => { dd.hidden = true; });
  });
}

/** Dry run: evaluate the rule (as currently edited, saved or not) against
 *  every in-scope tracker's current stats server-side. Nothing is sent. */
let dryRunSeq = 0; // discards responses from superseded dry-run requests

async function runDryRun(ri: number): Promise<void> {
  collectFromDOM();
  const r = rules[ri];
  if (!r) return;
  if (!r.conditions.length) { toast('Add at least one condition first', 'error'); return; }
  const seq = ++dryRunSeq;
  dryRunFor = r.id;
  dryRunResults = null;
  render();
  const { ok, data } = await api.dryRunRule(r);
  if (seq !== dryRunSeq) return; // a newer dry run started while this one ran
  if (!ok) {
    toast(`Dry run failed: ${data.error ?? 'error'}`, 'error');
    dryRunFor = null;
    render();
    return;
  }
  dryRunResults = data.results ?? [];
  const fired = dryRunResults.filter(x => x.matched).length;
  toast(fired ? `Dry run: would fire for ${fired} tracker${fired === 1 ? '' : 's'}` : 'Dry run: no tracker matches right now', 'success');
  render();
}

async function testDest(i: number): Promise<void> {
  collectFromDOM();
  const d = destinations[i];
  if (!d) return;
  toast('Sending test…', 'success');
  const { ok, data } = await api.testNotification(d);
  toast(ok && data.ok ? `Test sent to ${d.name || d.type}` : `Test failed: ${data.error ?? 'error'}`, ok && data.ok ? 'success' : 'error');
}

/** Builds the digest text server-side (against LIVE stats, not the unsaved
 *  form) and shows it in an inline <pre> box, like the rule dry-run box.
 *  Sends nothing and touches no server state. */
async function previewDigest(): Promise<void> {
  collectFromDOM();
  const { ok, data } = await api.fetchDigestPreview();
  if (!ok) { toast('Preview failed', 'error'); return; }
  digestPreviewText = data.text;
  render();
}

/** Builds and sends the digest right now, independent of the schedule. */
async function sendDigestNowClick(): Promise<void> {
  collectFromDOM();
  toast('Sending digest…', 'success');
  const { ok, data } = await api.sendDigestNow();
  toast(ok && data.ok
    ? `Digest sent to ${data.sent_to} destination${data.sent_to === 1 ? '' : 's'}`
    : `Send failed: ${data.error ?? 'error'}`, ok && data.ok ? 'success' : 'error');
}
