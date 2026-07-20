// components/goalDate.ts — the compact goal-date control shared by the
// targets popover and the edit-modal target builder. The date is OPTIONAL,
// so it must not compete with the target value for space: rows carry only a
// small calendar icon button, and the actual <input type="date"> lives in a
// tiny anchored pop that appears on demand. A set date shows as an accented
// icon whose tooltip names the date.
import { esc, fmtDueDate } from '../utils/format';

const CAL_SVG =
  `<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">` +
  `<rect x="3" y="4" width="18" height="18" rx="2"/><line x1="16" y1="2" x2="16" y2="6"/>` +
  `<line x1="8" y1="2" x2="8" y2="6"/><line x1="3" y1="10" x2="21" y2="10"/></svg>`;

function goalTitle(deadline: string): string {
  return deadline
    ? `Goal: reach this by ${fmtDueDate(deadline)} — click to change`
    : 'Add a goal date — aim to reach this target by a chosen date (optional)';
}

/**
 * The icon button + hidden anchored date editor for one target row (or a
 * group's single whole-set goal). Returns '' for the account-age row — a
 * date can't govern something uncontrollable. `defaultDate`, when given, is
 * what an empty input jumps to on first open (used where the smart default
 * is computable at render time, e.g. from a group's min_age); otherwise the
 * container's focusin prefill handles it.
 */
export function goalDateControlHtml(key: string, deadline: string, defaultDate = ''): string {
  if (key === 'days') return '';
  return `<span class="goal-date-wrap">
    <button type="button" class="btn btn-ghost btn-icon btn-sm goal-date-btn${deadline ? ' goal-date-btn--set' : ''}" data-goal-toggle title="${esc(goalTitle(deadline))}">${CAL_SVG}</button>
    <span class="goal-date-pop" hidden>
      <input class="form-input goal-date-input" type="date" data-target-deadline${defaultDate ? ` data-goal-default="${esc(defaultDate)}"` : ''} value="${esc(deadline)}"/>
      <button type="button" class="btn btn-ghost btn-icon btn-sm" data-goal-clear title="Remove goal date">&times;</button>
    </span>
  </span>`;
}

/**
 * Delegated open/clear/icon-state handling for every goal-date control under
 * `container`. Wire ONCE per persistent element (both editors rebuild row
 * innerHTML; the container survives) — guarded by a dataset flag.
 */
export function wireGoalDateUI(container: HTMLElement): void {
  if (container.dataset['goalWired']) return;
  container.dataset['goalWired'] = '1';
  container.addEventListener('click', e => {
    const tgt = e.target as HTMLElement;
    const toggle = tgt.closest<HTMLElement>('[data-goal-toggle]');
    if (toggle) {
      const wrap = toggle.closest<HTMLElement>('.goal-date-wrap');
      const pop = wrap?.querySelector<HTMLElement>('.goal-date-pop');
      if (!pop) return;
      pop.hidden = !pop.hidden;
      if (!pop.hidden) {
        const input = pop.querySelector<HTMLInputElement>('[data-target-deadline]');
        if (input) {
          if (!input.value && input.dataset['goalDefault']) input.value = input.dataset['goalDefault'];
          input.focus();
          // The editors' focusin prefill covers the no-default case — dispatch
          // explicitly too, since .focus() doesn't raise focusin when the
          // document itself lacks focus (background tab); double prefill is
          // impossible (the handler only fills an EMPTY input).
          input.dispatchEvent(new Event('focusin', { bubbles: true }));
          syncGoalBtn(wrap);
        }
      }
      return;
    }
    const clear = tgt.closest<HTMLElement>('[data-goal-clear]');
    if (clear) {
      const wrap = clear.closest<HTMLElement>('.goal-date-wrap');
      const input = wrap?.querySelector<HTMLInputElement>('[data-target-deadline]');
      const pop = wrap?.querySelector<HTMLElement>('.goal-date-pop');
      if (!wrap || !input || !pop) return;
      input.value = '';
      pop.hidden = true;
      syncGoalBtn(wrap);
    }
  });
  container.addEventListener('change', e => {
    const input = (e.target as HTMLElement).closest<HTMLInputElement>('[data-target-deadline]');
    if (input) syncGoalBtn(input.closest<HTMLElement>('.goal-date-wrap'));
  });
}

function syncGoalBtn(wrap: HTMLElement | null): void {
  if (!wrap) return;
  const input = wrap.querySelector<HTMLInputElement>('[data-target-deadline]');
  const btn = wrap.querySelector<HTMLElement>('[data-goal-toggle]');
  if (!input || !btn) return;
  btn.classList.toggle('goal-date-btn--set', !!input.value);
  btn.title = goalTitle(input.value);
}

/** The single date all of a map's deadlines share, or '' when unset/mixed —
 *  seeds the group-mode "whole set" control from stored per-key deadlines. */
export function commonDeadline(map: Record<string, string> | undefined): string {
  const vals = Object.values(map ?? {});
  return vals.length && vals.every(v => v === vals[0]) ? vals[0]! : '';
}

/** Fan a group's single goal date out to every one of its target keys (the
 *  per-row storage the pacing/alert layer already understands). Account age
 *  never takes one. */
export function fanDeadline(targets: Record<string, string>, date: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const k of Object.keys(targets)) {
    if (k !== 'days') out[k] = date;
  }
  return out;
}
