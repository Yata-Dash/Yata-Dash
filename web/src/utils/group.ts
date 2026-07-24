// utils/group.ts — helper functions for group styling and target resolution

import type { GroupDef, GroupRequirements, TrackerGroupMap, AppSettings } from '../types';
import { parseAgeDays, parseSeedTime } from './parse';

/**
 * Map a group's BASE requirements to tracker target keys — the same mapping
 * the settings form uses (Load from Group → save): min_age is normalised to
 * plain days, min_seedtime to plain seconds. any_of alternatives are NEVER
 * included (the targets map holds base requirements only).
 */
export function groupRequirementsToTargets(req: GroupRequirements): Record<string, string> {
  const ageDays = parseAgeDays(req.min_age ?? '');
  const seedSec = parseSeedTime(req.min_seedtime ?? '');
  const raw: Record<string, string> = {
    uploaded:      req.min_uploaded ?? '',
    downloaded:    req.min_downloaded ?? '',
    total_transfer:req.min_total_transfer ?? '',
    ratio:         req.min_ratio != null ? String(req.min_ratio) : '',
    seed_size:     req.min_seed_size ?? '',
    total_uploads: req.min_uploads != null ? String(req.min_uploads) : '',
    adoptions:     req.min_adoptions != null ? String(req.min_adoptions) : '',
    days:          ageDays != null ? String(ageDays) : '',
    avg_seed:      seedSec != null ? String(seedSec) : '',
    bonus_points:  req.min_bonus_points != null ? String(req.min_bonus_points) : '',
    // No live stat backs this yet — renders as an untrackable ("Not
    // available") target row via grid.ts's generic-key loop until Yata can
    // estimate uploads/month from history.
    monthly_uploads: req.min_monthly_uploads != null ? String(req.min_monthly_uploads) : '',
  };
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(raw)) if (v) out[k] = v;
  return out;
}

/**
 * Finds a group definition by tracker key and group name.
 * Tracker key is derived from the tracker URL if not directly available.
 */
export function findGroupDef(
  groupMap: TrackerGroupMap,
  trackerKey: string,
  groupName: string
): GroupDef | undefined {
  const groups = groupMap[trackerKey];
  if (!groups || !groupName) return undefined;
  const lc = groupName.toLowerCase();
  return groups.find(g => g.name.toLowerCase() === lc);
}

/**
 * Renders a username — either plain or styled with the user's group appearance.
 * The blur class is always on the innermost span so it overrides everything.
 */
export function renderUsername(
  username: string,
  groupDef: GroupDef | undefined,
  settings: AppSettings,
  classes = ''
): string {
  // Strip blur class out — we'll re-apply it on the inner content span
  const blurClasses = classes.split(' ').filter(c => c === 'private-blur');
  const outerClasses = classes.split(' ').filter(c => c !== 'private-blur').join(' ');
  const blurClass = blurClasses.length ? ' private-blur' : '';

  const useGroupStyle = (settings.username_style || 'plain') === 'group';
  if (!useGroupStyle || !groupDef) {
    return `<span class="${outerClasses}${blurClass}">${escHtml(username)}</span>`;
  }
  const { color, icon, sparkle } = groupDef.style;
  const colorStyle = color ? ` style="color:${color}"` : '';
  const iconHtml = icon ? `<i class="${icon}" aria-hidden="true"></i> ` : '';
  const sparkleClass = sparkle ? ' group-sparkle' : '';
  // Color on outer, blur on inner content so blur is never overridden
  return `<span class="${outerClasses}${sparkleClass}"${colorStyle}><span class="username-inner${blurClass}">${iconHtml}${escHtml(username)}</span></span>`;
}

/**
 * Renders a styled group badge HTML string.
 * In "styled" mode: colored icon + colored name.
 * In "plain" mode: plain text only using the accent badge style.
 */
export function renderGroupBadge(
  groupDef: GroupDef | undefined,
  groupName: string,
  settings: AppSettings,
  classes = ''
): string {
  const styled = (settings.group_name_style || 'plain') === 'styled';
  if (!styled || !groupDef) {
    return `<span class="${classes}">${escHtml(groupName)}</span>`;
  }
  const { color, icon, sparkle } = groupDef.style;
  const colorStyle = color ? ` style="color:${color}"` : '';
  const iconHtml = icon ? `<i class="${icon}" aria-hidden="true"></i> ` : '';
  const sparkleClass = sparkle ? ' group-sparkle' : '';
  return `<span class="badge-group-styled ${classes}${sparkleClass}"${colorStyle}>${iconHtml}${escHtml(groupName)}</span>`;
}

function escHtml(s: string): string {
  return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}
