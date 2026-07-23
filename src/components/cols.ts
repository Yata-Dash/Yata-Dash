// components/cols.ts — column visibility & order customizer
import type { ColPref } from '../types';
import { COL_DEFS, saveColPrefs } from '../state';
import { esc } from '../utils/format';

let dragSrcKey: string | null = null;

/** Open the column customizer modal and populate the drag-sortable list */
export function openColCustomizer(colPrefs: ColPref[]): void {
  const list = document.getElementById('col-customize-list');
  if (!list) return;

  list.innerHTML = '';

  colPrefs.forEach(pref => {
    const def = COL_DEFS.find(d => d.key === pref.key);
    if (!def) return;

    const item = document.createElement('div');
    item.className = 'col-item';
    item.dataset['key'] = pref.key;
    item.draggable = true;
    item.innerHTML = `
      <span class="col-item-drag">
        <svg width="12" height="16" viewBox="0 0 12 16" fill="currentColor" style="opacity:.4">
          <circle cx="4" cy="3" r="1.5"/><circle cx="8" cy="3" r="1.5"/>
          <circle cx="4" cy="8" r="1.5"/><circle cx="8" cy="8" r="1.5"/>
          <circle cx="4" cy="13" r="1.5"/><circle cx="8" cy="13" r="1.5"/>
        </svg>
      </span>
      <span class="col-item-label">${esc(def.label)}${def.group === 'extended' ? '<span class="col-profile-dot"></span>' : ''}</span>
      <div class="col-item-toggle ${pref.visible ? 'on' : ''}" data-key="${pref.key}"
        onclick="toggleColVisible('${pref.key}', this)"
        ${def.always ? 'style="opacity:.4;pointer-events:none"' : ''}></div>`;

    // Drag-and-drop reorder
    item.addEventListener('dragstart', e => {
      dragSrcKey = pref.key;
      item.classList.add('dragging-col');
      e.dataTransfer!.effectAllowed = 'move';
    });
    item.addEventListener('dragend', () => {
      item.classList.remove('dragging-col');
      list.querySelectorAll('.col-item').forEach(i => i.classList.remove('drag-over-col'));
    });
    item.addEventListener('dragover', e => {
      e.preventDefault();
      if (pref.key !== dragSrcKey) {
        list.querySelectorAll('.col-item').forEach(i => i.classList.remove('drag-over-col'));
        item.classList.add('drag-over-col');
      }
    });
    item.addEventListener('dragleave', () => item.classList.remove('drag-over-col'));
    item.addEventListener('drop', e => {
      e.preventDefault();
      item.classList.remove('drag-over-col');
      if (!dragSrcKey || dragSrcKey === pref.key) return;
      const fi = colPrefs.findIndex(c => c.key === dragSrcKey);
      const ti = colPrefs.findIndex(c => c.key === pref.key);
      if (fi < 0 || ti < 0) return;
      const [moved] = colPrefs.splice(fi, 1);
      colPrefs.splice(ti, 0, moved);
      saveColPrefs(colPrefs);
      openColCustomizer(colPrefs);
    });

    list.appendChild(item);
  });

  document.getElementById('col-modal')?.classList.add('open');
}

/** Toggle a column's visibility in-place */
export function toggleColVisible(key: string, el: Element, colPrefs: ColPref[]): void {
  const pref = colPrefs.find(c => c.key === key);
  if (!pref) return;
  pref.visible = !pref.visible;
  el.classList.toggle('on', pref.visible);
  saveColPrefs(colPrefs);
}
