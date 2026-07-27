// components/toast.ts — non-blocking toast notifications
import { esc } from '../utils/format';

export type ToastType = 'success' | 'error' | 'warning';

const TOAST_ICONS: Record<ToastType, string> = {
  success: `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="var(--green)" stroke-width="2.5" stroke-linecap="round"><polyline points="20 6 9 17 4 12"/></svg>`,
  error:   `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="var(--red)" stroke-width="2" stroke-linecap="round"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>`,
  // Warning: something needs attention but the action still succeeded — a def
  // that loaded with one key ignored, say. Red would overstate it.
  warning: `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="var(--amber)" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z"/><line x1="12" y1="9" x2="12" y2="13"/><line x1="12" y1="17" x2="12.01" y2="17"/></svg>`,
};

/** Show a transient toast notification */
export function toast(msg: string, type: ToastType = 'success'): void {
  const container = document.getElementById('toast-container');
  if (!container) return;

  const el = document.createElement('div');
  el.className = `toast ${type}`;
  el.innerHTML = (TOAST_ICONS[type] ?? TOAST_ICONS.error) + esc(msg);
  container.appendChild(el);

  setTimeout(() => {
    el.style.cssText = 'opacity:0;transition:opacity .3s';
    setTimeout(() => el.remove(), 300);
  }, 3500);
}
