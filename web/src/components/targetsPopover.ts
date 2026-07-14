// components/targetsPopover.ts — dashboard "Targets" quick-edit popover.
// The ONE edit that stays on the dashboard (also reached from the Tracker
// Detail page's Targets pencil): a small anchored popover with a "Load from
// group" dropdown listing the tracker's def groups plus "— manual —".
//   • A group → PUTs target_group + that group's BASE requirements mapped to
//     target keys (same mapping as the settings form).
//   • "— manual —" → opens an inline builder (one row per target + a stat
//     picker). It seeds from the tracker's CURRENT manual targets if it's
//     already manual, otherwise from the last manual targets you set here
//     (remembered per tracker) — never from the group you're leaving, so
//     switching group → manual gives a clean slate rather than the group's
//     numbers masquerading as your own.
import type { Tracker, TrackerGroupMap } from '../types';
import * as api from '../api';
import { esc } from '../utils/format';
import { groupRequirementsToTargets } from '../utils/group';
import { statsCache, strOf } from '../state';
import type { ToastType } from './toast';
import {
  buildAvailableTargetSpecs, normalizeTargetValue, targetDisplayValue,
} from './modals';
import type { TargetSpec } from './modals';

interface PopoverDeps {
  trackers: () => Tracker[];
  groupDefs: () => TrackerGroupMap;
  loadTrackers: () => Promise<void>;
  toast: (msg: string, type?: ToastType) => void;
  // Called after a successful apply (after loadTrackers) so an open Tracker
  // Detail page live-updates its targets + charts. No-op elsewhere.
  afterApply?: () => void;
}

const MANUAL_CACHE_KEY = 'yata.manualTargets'; // Record<trackerId, Record<key,value>>

let _deps: PopoverDeps | null = null;
let _el: HTMLDivElement | null = null;
let _trackerId = '';
let _specs: TargetSpec[] = []; // available target stats for the open tracker

export function initTargetsPopover(deps: PopoverDeps): void {
  _deps = deps;
}

// ── Last-manual cache (per tracker, survives group round-trips) ──────────────

function cachedManual(id: string): Record<string, string> {
  try {
    const all = JSON.parse(localStorage.getItem(MANUAL_CACHE_KEY) ?? '{}');
    const m = all?.[id];
    return m && typeof m === 'object' ? m : {};
  } catch { return {}; }
}

function cacheManual(id: string, targets: Record<string, string>): void {
  try {
    const all = JSON.parse(localStorage.getItem(MANUAL_CACHE_KEY) ?? '{}');
    all[id] = targets;
    localStorage.setItem(MANUAL_CACHE_KEY, JSON.stringify(all));
  } catch { /* private mode */ }
}

function ensureEl(): HTMLDivElement {
  if (_el) return _el;
  const el = document.createElement('div');
  el.id = 'targets-popover';
  el.className = 'targets-popover';
  el.style.display = 'none';
  el.addEventListener('mousedown', e => e.stopPropagation());
  el.addEventListener('click', e => e.stopPropagation());
  document.body.appendChild(el);

  // Close on outside click / Esc
  document.addEventListener('mousedown', e => {
    if (_el && _el.style.display !== 'none' && !_el.contains(e.target as Node)) closeTargetsPopover();
  });
  document.addEventListener('keydown', e => {
    if (e.key === 'Escape') closeTargetsPopover();
  });
  _el = el;
  return el;
}

/** Open the popover anchored to the pencil button that was clicked. */
export function openTargetsPopover(trackerId: string, anchor: HTMLElement): void {
  if (!_deps) return;
  const t = _deps.trackers().find(x => x.id === trackerId);
  if (!t) return;
  const groups = _deps.groupDefs()[t.def_key] ?? [];
  if (!groups.length && !buildAvailableTargetSpecs(t, statsCache[trackerId]).length) return;

  const el = ensureEl();
  _trackerId = trackerId;
  _specs = buildAvailableTargetSpecs(t, statsCache[trackerId]);

  // Live group from merged stats — highlighted so the user can see at a
  // glance where they currently sit when choosing a target group.
  const liveGroup = strOf(statsCache[trackerId], 'group');
  const startManual = !t.target_group;
  el.innerHTML = `
    <div class="targets-popover-title">Edit targets</div>
    <label class="form-label" style="font-size:11px" for="targets-popover-select">Load from group</label>
    <select class="form-input" id="targets-popover-select" style="margin:4px 0 10px">
      <option value="">&#8212; manual &#8212;</option>
      ${groups.map(g => {
        const isCurrent = !!liveGroup && g.name === liveGroup;
        const style = isCurrent ? ' style="color:var(--amber)"' : '';
        const label = isCurrent ? `★ ${esc(g.name)} (current)` : esc(g.name);
        return `<option value="${esc(g.name)}"${style} ${g.name === t.target_group ? 'selected' : ''}>${label}</option>`;
      }).join('')}
    </select>
    <div id="targets-popover-manual" style="display:${startManual ? 'block' : 'none'}">
      <div id="targets-popover-rows" class="targets-popover-rows"></div>
      <div class="targets-popover-add">
        <select class="form-input" id="targets-popover-add-select"></select>
        <button type="button" class="btn btn-ghost btn-sm" id="targets-popover-add-btn">+ Add</button>
      </div>
    </div>
    <div class="targets-popover-actions">
      <button type="button" class="btn btn-ghost btn-sm" id="targets-popover-cancel">Cancel</button>
      <button type="button" class="btn btn-primary btn-sm" id="targets-popover-apply">Apply</button>
    </div>`;

  const sel = el.querySelector('#targets-popover-select') as HTMLSelectElement;
  sel.addEventListener('change', () => onGroupSelectChange(t));
  el.querySelector('#targets-popover-cancel')?.addEventListener('click', closeTargetsPopover);
  el.querySelector('#targets-popover-apply')?.addEventListener('click', () => { void applyTargetsPopover(); });
  el.querySelector('#targets-popover-add-btn')?.addEventListener('click', () => addRow());
  el.querySelector('#targets-popover-rows')?.addEventListener('click', e => {
    const btn = (e.target as HTMLElement).closest<HTMLElement>('[data-remove]');
    if (btn) { btn.closest('.target-edit-row')?.remove(); refreshAddSelect(); }
  });

  // Seed the manual builder when opening already-manual (current targets).
  if (startManual) renderRows(seedManual(t));
  refreshAddSelect();

  positionPopover(el, anchor);
  (startManual
    ? (el.querySelector('#targets-popover-add-select') as HTMLElement | null)
    : sel)?.focus();
}

function positionPopover(el: HTMLDivElement, anchor: HTMLElement): void {
  el.style.display = 'block';
  el.style.visibility = 'hidden';
  const r = anchor.getBoundingClientRect();
  const w = el.offsetWidth, h = el.offsetHeight;
  let left = r.left;
  let top  = r.bottom + 6;
  if (left + w > window.innerWidth - 8)  left = Math.max(8, window.innerWidth - w - 8);
  if (top + h > window.innerHeight - 8)  top  = Math.max(8, r.top - h - 6);
  el.style.left = `${left}px`;
  el.style.top  = `${top}px`;
  el.style.visibility = '';
}

/** Which manual targets to show when entering manual mode: the tracker's own
 *  values if it's already manual, else the last manual set here, else none —
 *  deliberately NOT the group's requirement values. */
function seedManual(t: Tracker): Record<string, string> {
  if (!t.target_group) return { ...(t.targets ?? {}) };
  return cachedManual(t.id);
}

function onGroupSelectChange(t: Tracker): void {
  const sel = document.getElementById('targets-popover-select') as HTMLSelectElement | null;
  const manual = document.getElementById('targets-popover-manual');
  if (!sel || !manual) return;
  if (sel.value) {
    manual.style.display = 'none';
  } else {
    manual.style.display = 'block';
    // Reseed only when empty, so an accidental toggle doesn't wipe edits.
    if (!document.querySelector('#targets-popover-rows [data-target-key]')) renderRows(seedManual(t));
    refreshAddSelect();
    if (_el) positionPopover(_el, _el); // no-op reposition keeps it on-screen after growth
  }
}

function specFor(key: string): TargetSpec {
  return _specs.find(s => s.key === key)
    ?? { key, label: key, placeholder: 'e.g. 100' };
}

function rowHtml(key: string, value: string): string {
  const spec = specFor(key);
  return `<div class="target-edit-row" data-target-key="${esc(key)}">
    <span class="target-edit-label" title="${esc(spec.hint ?? '')}">${esc(spec.label)}</span>
    <input class="form-input" type="text" data-target-input placeholder="${esc(spec.placeholder)}" value="${esc(value)}"/>
    <button type="button" class="btn btn-ghost btn-icon btn-sm target-edit-remove" data-remove="${esc(key)}" title="Remove target">&times;</button>
  </div>`;
}

function renderRows(targets: Record<string, string>): void {
  const wrap = document.getElementById('targets-popover-rows');
  if (!wrap) return;
  wrap.innerHTML = Object.entries(targets)
    .filter(([k, v]) => v !== '' && !k.startsWith('count:'))
    .map(([k, v]) => rowHtml(k, targetDisplayValue(k, v)))
    .join('');
  refreshAddSelect();
}

function refreshAddSelect(): void {
  const sel = document.getElementById('targets-popover-add-select') as HTMLSelectElement | null;
  if (!sel) return;
  const added = new Set(
    [...document.querySelectorAll<HTMLElement>('#targets-popover-rows [data-target-key]')]
      .map(el => el.dataset['targetKey'] ?? ''));
  sel.innerHTML = `<option value="">&#8212; add a stat &#8212;</option>` +
    _specs.filter(s => !added.has(s.key))
      .map(s => `<option value="${esc(s.key)}">${esc(s.label)}</option>`).join('');
}

function addRow(): void {
  const sel = document.getElementById('targets-popover-add-select') as HTMLSelectElement | null;
  const wrap = document.getElementById('targets-popover-rows');
  if (!sel || !wrap || !sel.value) return;
  wrap.insertAdjacentHTML('beforeend', rowHtml(sel.value, ''));
  refreshAddSelect();
  wrap.querySelector<HTMLInputElement>(`[data-target-key="${CSS.escape(sel.value)}"] input`)?.focus();
}

function collectRows(): Record<string, string> {
  const out: Record<string, string> = {};
  for (const row of document.querySelectorAll<HTMLElement>('#targets-popover-rows [data-target-key]')) {
    const key = row.dataset['targetKey'] ?? '';
    const raw = row.querySelector<HTMLInputElement>('[data-target-input]')?.value ?? '';
    const norm = key ? normalizeTargetValue(key, raw) : null;
    if (norm != null) out[key] = norm;
  }
  return out;
}

export function closeTargetsPopover(): void {
  if (_el) _el.style.display = 'none';
  _trackerId = '';
}

async function applyTargetsPopover(): Promise<void> {
  if (!_deps || !_trackerId) return;
  const t = _deps.trackers().find(x => x.id === _trackerId);
  const sel = document.getElementById('targets-popover-select') as HTMLSelectElement | null;
  if (!t || !sel) return;
  const groupName = sel.value;

  let payload;
  if (!groupName) {
    // "— manual —": replace targets with the builder rows and clear the group.
    const targets = collectRows();
    cacheManual(t.id, targets); // remember for next time
    payload = { target_group: '', targets };
  } else {
    const g = (_deps.groupDefs()[t.def_key] ?? []).find(x => x.name === groupName);
    if (!g) return;
    payload = { target_group: groupName, targets: groupRequirementsToTargets(g.requirements) };
  }

  const btn = document.getElementById('targets-popover-apply') as HTMLButtonElement | null;
  if (btn) btn.disabled = true;
  const { ok } = await api.updateTracker(_trackerId, payload);
  if (btn) btn.disabled = false;
  if (ok) {
    closeTargetsPopover();
    _deps.toast(groupName ? `Targets loaded from ${groupName}` : 'Manual targets saved', 'success');
    await _deps.loadTrackers(); // re-renders the row/card with fresh targets
    _deps.afterApply?.();        // live-update an open Tracker Detail page
  } else {
    _deps.toast('Failed to update targets', 'error');
  }
}
