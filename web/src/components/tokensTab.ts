// components/tokensTab.ts — Settings → Integrations → API Tokens.
// Read-only integration tokens: list / create (plaintext shown ONCE) / revoke.
// The token itself only unlocks the read-only endpoints (see docs/API.md).
import * as api from '../api';
import type { ApiTokenInfo } from '../types';
import { esc } from '../utils/format';
import { toast } from './toast';

function el(id: string): HTMLElement | null { return document.getElementById(id); }

function fmtDate(unixSec: number): string {
  return new Date(unixSec * 1000).toLocaleDateString();
}

function fmtLastUsed(unixSec: number): string {
  if (!unixSec) return 'never used';
  const mins = Math.floor((Date.now() / 1000 - unixSec) / 60);
  if (mins < 2) return 'used just now';
  if (mins < 60) return `used ${mins} min ago`;
  if (mins < 48 * 60) return `used ${Math.floor(mins / 60)} h ago`;
  return `used ${fmtDate(unixSec)}`;
}

/** Load + render the token list. Called when the Integrations tab opens. */
export async function loadApiTokens(): Promise<void> {
  const list = el('s-token-list');
  if (!list) return;
  const { ok, data } = await api.fetchApiTokens();
  if (!ok) {
    list.innerHTML = '<span style="font-size:12px;color:var(--text3);font-style:italic">Could not load tokens.</span>';
    return;
  }
  renderList(data);
}

function renderList(tokens: ApiTokenInfo[]) {
  const list = el('s-token-list');
  if (!list) return;
  if (!tokens.length) {
    list.innerHTML = '<span style="font-size:12px;color:var(--text3);font-style:italic">No tokens yet.</span>';
    return;
  }
  list.innerHTML = tokens.map(t => `
    <div class="token-row" data-token-id="${esc(t.id)}">
      <div class="token-row-main">
        <span class="token-row-name">${esc(t.name)}</span>
        <code class="token-row-prefix">${esc(t.prefix)}</code>
      </div>
      <span class="token-row-meta">created ${esc(fmtDate(t.created_at))} · ${esc(fmtLastUsed(t.last_used_at))}</span>
      <button type="button" class="btn btn-danger btn-sm" onclick="revokeApiToken('${esc(t.id)}', this)">Revoke</button>
    </div>`).join('');
}

/** "Create token" button — POSTs the name, reveals the plaintext once. */
export async function createApiToken(): Promise<void> {
  const input = el('s-token-name') as HTMLInputElement | null;
  const name = input?.value.trim() ?? '';
  if (!name) {
    toast('Give the token a name first (e.g. "Homepage widget")', 'error');
    input?.focus();
    return;
  }
  const { ok, data } = await api.createApiToken(name);
  if (!ok || !data.token) {
    toast(`Could not create token: ${(data as { error?: string }).error ?? 'unknown error'}`, 'error');
    return;
  }
  if (input) input.value = '';
  showNewToken(data.token, data.info.name);
  await loadApiTokens();
}

/** One-time plaintext reveal with a copy button. */
function showNewToken(token: string, name: string) {
  const box = el('s-token-new');
  if (!box) return;
  box.style.display = '';
  box.innerHTML = `
    <div class="token-new-box">
      <div class="token-new-head">
        <i class="fas fa-key" style="color:var(--amber)"></i>
        <span>Token for <strong>${esc(name)}</strong> — copy it now, it won't be shown again.</span>
        <button type="button" class="modal-close" style="margin-left:auto" onclick="dismissNewApiToken()">&times;</button>
      </div>
      <div class="token-new-value">
        <code id="s-token-new-value">${esc(token)}</code>
        <button type="button" class="btn btn-primary btn-sm" onclick="copyNewApiToken(this)">Copy</button>
      </div>
    </div>`;
}

export function dismissNewApiToken(): void {
  const box = el('s-token-new');
  if (box) { box.style.display = 'none'; box.innerHTML = ''; }
}

export async function copyNewApiToken(btn: HTMLButtonElement): Promise<void> {
  const value = el('s-token-new-value')?.textContent ?? '';
  if (!value) return;
  try {
    await navigator.clipboard.writeText(value);
    btn.textContent = 'Copied!';
    setTimeout(() => { btn.textContent = 'Copy'; }, 1500);
  } catch {
    // Clipboard API unavailable (http:// origin) — select the text instead.
    const range = document.createRange();
    const node = el('s-token-new-value');
    if (node) {
      range.selectNodeContents(node);
      const sel = window.getSelection();
      sel?.removeAllRanges();
      sel?.addRange(range);
      toast('Press Ctrl+C to copy');
    }
  }
}

export async function revokeApiToken(id: string, btn?: HTMLButtonElement): Promise<void> {
  if (!confirm('Revoke this token?\n\nAnything using it (dashboard widgets, scripts) stops working immediately.')) return;
  if (btn) btn.disabled = true;
  const { ok, data } = await api.revokeApiToken(id);
  if (!ok) {
    if (btn) btn.disabled = false;
    toast(`Could not revoke token: ${(data as { error?: string }).error ?? 'unknown error'}`, 'error');
    return;
  }
  toast('Token revoked', 'success');
  await loadApiTokens();
}
