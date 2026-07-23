// components/toast.ts — non-blocking toast notifications
import { esc } from '../utils/format';

export type ToastType = 'success' | 'error';

/** Show a transient toast notification */
export function toast(msg: string, type: ToastType = 'success'): void {
  const container = document.getElementById('toast-container');
  if (!container) return;

  const el = document.createElement('div');
  el.className = `toast ${type}`;

  const icon = type === 'success'
    ? `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="var(--green)" stroke-width="2.5" stroke-linecap="round"><polyline points="20 6 9 17 4 12"/></svg>`
    : `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="var(--red)" stroke-width="2" stroke-linecap="round"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>`;

  el.innerHTML = icon + esc(msg);
  container.appendChild(el);

  setTimeout(() => {
    el.style.cssText = 'opacity:0;transition:opacity .3s';
    setTimeout(() => el.remove(), 300);
  }, 3500);
}
