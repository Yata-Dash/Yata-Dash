// components/logs.ts — live rolling-log viewer for the Settings → Logs tab.
// The server captures EVERYTHING; this view just filters which levels are
// shown (the selection is a display filter, persisted in localStorage — it
// never changes what is logged to the file/buffer).
import * as api from '../api';
import type { LogEntry } from '../types';
import { esc } from '../utils/format';
import type { ToastType } from './toast';

const POLL_MS = 2500;
const LEVELS = ['trace', 'debug', 'info', 'warn', 'error'] as const;
const LS_KEY = 'yata-log-levels';
let timer: ReturnType<typeof setInterval> | null = null;
let paused = false;
let toastFn: ((msg: string, type?: ToastType) => void) | null = null;
let lastEntries: LogEntry[] = [];

// Which levels to DISPLAY (default: hide the noisy trace/debug). Everything is
// still captured server-side regardless of this choice.
let shown: Set<string> = loadShown();

function loadShown(): Set<string> {
  try {
    const raw = localStorage.getItem(LS_KEY);
    if (raw) return new Set(JSON.parse(raw));
  } catch { /* ignore */ }
  return new Set(['info', 'warn', 'error']);
}
function saveShown(): void {
  try { localStorage.setItem(LS_KEY, JSON.stringify([...shown])); } catch { /* ignore */ }
}

function fmtTime(ms: number): string {
  const d = new Date(ms);
  const p = (n: number) => String(n).padStart(2, '0');
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
}

function renderChips(): void {
  const box = document.getElementById('s-log-levels');
  if (!box) return;
  box.innerHTML = LEVELS.map(l =>
    `<button type="button" class="log-lvl-chip log-lvl-${l}${shown.has(l) ? ' selected' : ''}" onclick="toggleLogLevel('${l}')">${l.toUpperCase()}</button>`
  ).join('');
}

function renderEntries(): void {
  const view = document.getElementById('s-log-view');
  if (!view) return;
  const entries = lastEntries.filter(e => shown.has(String(e.level).toLowerCase()));
  if (!entries.length) {
    view.innerHTML = `<div class="log-empty">No log entries to display${lastEntries.length ? ' at the selected levels' : ' yet'}.</div>`;
    return;
  }
  const autoScroll = (document.getElementById('s-log-autoscroll') as HTMLInputElement | null)?.checked ?? true;
  const atBottom = view.scrollHeight - view.scrollTop - view.clientHeight < 40;
  view.innerHTML = entries.map(e => {
    const lvl = String(e.level).toLowerCase();
    return `<div class="log-line log-${esc(lvl)}"><span class="log-time">${fmtTime(e.time)}</span><span class="log-lvl">${esc(lvl.slice(0, 3).toUpperCase())}</span><span class="log-msg">${esc(e.msg)}</span></div>`;
  }).join('');
  if (autoScroll && atBottom) view.scrollTop = view.scrollHeight;
}

/** Fetch + render once. */
export async function refreshLogs(): Promise<void> {
  if (paused) return;
  const { ok, data } = await api.fetchLogs(2000);
  if (!ok) return;
  lastEntries = data.entries ?? [];
  renderEntries();
  const pathEl = document.getElementById('s-log-path');
  if (pathEl && data.file) pathEl.textContent = data.file;
}

/** Called when the Logs tab becomes active. */
export function startLogsAuto(deps?: { toast: (msg: string, type?: ToastType) => void }): void {
  if (deps) toastFn = deps.toast;
  renderChips();
  void refreshLogs();
  if (timer) clearInterval(timer);
  timer = setInterval(() => { void refreshLogs(); }, POLL_MS);
}

/** Called when leaving the Logs tab (or the settings page). */
export function stopLogsAuto(): void {
  if (timer) { clearInterval(timer); timer = null; }
}

/** Toggle a level on/off in the display filter (does not affect logging). */
export function toggleLogLevel(level: string): void {
  if (shown.has(level)) shown.delete(level); else shown.add(level);
  saveShown();
  renderChips();
  renderEntries();
}

export function toggleLogPause(): void {
  paused = !paused;
  const btn = document.getElementById('s-log-pause');
  if (btn) btn.textContent = paused ? 'Resume' : 'Pause';
  if (!paused) void refreshLogs();
}

export async function clearLogs(): Promise<void> {
  const { ok } = await api.clearLogs();
  if (ok) { toastFn?.('Logs cleared', 'success'); await refreshLogs(); }
}

export function downloadLogs(): void {
  window.open(api.logsDownloadUrl(), '_blank');
}
