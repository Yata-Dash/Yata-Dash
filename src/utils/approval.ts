// utils/approval.ts — def staff-approval status → UI warning bits.
// Status comes from the def's approved_by block (server-derived):
// approved | informal | pending | unknown. Anything but "approved" warns;
// official refusals never reach here (they live in the opt-out list, which
// blocks instead of warning).
import { esc } from './format';

export function approvalWarns(status?: string): boolean {
  return (status ?? 'unknown') !== 'approved';
}

/** Tooltip text for a non-approved def (or manual tracker). */
export function approvalTitle(status?: string, note?: string): string {
  if (status === 'informal') {
    return `Staff gave an informal OK — not an official approval.${note ? ` "${note}"` : ''} Use at your own risk.`;
  }
  if (status === 'pending') {
    return 'Approval has been requested from this tracker’s staff — awaiting a reply. Use at your own risk.';
  }
  return 'Scraping this tracker has not been officially approved by its staff — use at your own risk.';
}

/** Small inline warning icon (FA triangle) with the tooltip. */
export function approvalIcon(status?: string, note?: string): string {
  if (!approvalWarns(status)) return '';
  return `<i class="fas fa-triangle-exclamation approval-warn${status === 'informal' ? ' informal' : ''}" title="${esc(approvalTitle(status, note))}"></i>`;
}
