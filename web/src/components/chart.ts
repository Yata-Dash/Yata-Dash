// components/chart.ts — the History view's interactive line chart.
// Hand-rolled SVG (no deps, theme-var colors — the sparkline's grown-up
// sibling). One <path> per series, nice-tick axes, a crosshair that snaps to
// the nearest sample, and up to two pinned timestamps for delta reads.
//
// The chart draws; the VIEW owns the readout. Hover/pin changes surface via
// callbacks, and the crosshair/pin markers are updated in place (no full
// re-render per mousemove). Re-call renderChart() to change data or size.
// If dual-axis / small-multiples ever outgrow this, swap the internals for
// uPlot behind this same signature (HISTORY_VIEW_PLAN.md §6).
import { fmtAxisValue, fmtTimeTick, fmtUnitValue, nearestIndex, niceTicks, timeTicks } from '../utils/series';
import type { SeriesUnit } from '../utils/series';
import { esc, fmtEtaDays } from '../utils/format';

// ── Duration axis ticks ─────────────────────────────────────────────────────
// Seconds domains get whole, human durations (days → months → years) rather
// than nice-second steps that land on fractions like "115.7d". Labels use the
// app's duration setting so the axis matches the target line ("1Y", "3M").

const DURATION_STEPS_DAYS = [1, 2, 3, 5, 7, 10, 14, 21, 30, 45, 60, 90, 120, 180, 270, 365, 547, 730, 1095, 1825];

/** Tick values (in seconds) at a whole-day step chosen for ~4 ticks. */
function durationTicks(minSec: number, maxSec: number): number[] {
  const spanDays = Math.max((maxSec - minSec) / 86400, 1e-6);
  let stepDays = DURATION_STEPS_DAYS[DURATION_STEPS_DAYS.length - 1];
  for (const s of DURATION_STEPS_DAYS) { if (spanDays / s <= 4) { stepDays = s; break; } }
  const stepSec = stepDays * 86400;
  const first = Math.ceil(minSec / stepSec) * stepSec;
  const out: number[] = [];
  for (let v = first; v <= maxSec + stepSec * 1e-6; v += stepSec) out.push(v);
  if (minSec <= 0 && out[0] !== 0) out.unshift(0);
  return out;
}

/** Duration tick label — "0", "3M", "1Y" — following the duration setting. */
function fmtDurationTick(sec: number): string {
  if (sec <= 0) return '0';
  return fmtEtaDays(sec / 86400);
}

export interface ChartSeries {
  id: string;
  label: string;
  color: string; // CSS var name ("--teal") or raw color
  unit: SeriesUnit;
  points: [number, number][]; // [unixSec, value], oldest first
  // Ghost series draw dashed and are invisible to interaction: no crosshair
  // snap, no hover dot, no pin dot. Used for projection tails.
  ghost?: boolean;
}

/** Horizontal reference line (e.g. a tracker target) with a right-edge label. */
export interface ChartRefLine {
  value: number;
  label: string;
}

/** A timeline marker (e.g. a group change) — vertical line + axis flag. */
export interface ChartEvent {
  at: number;                                  // unix sec
  label: string;                               // short label, e.g. "PowerPool"
  detail: string;                              // full text for the hover title
  kind: 'promotion' | 'demotion' | 'neutral'; // drives the colour
}

/** A milestone marker (a threshold crossing) — dot at (at,value). */
export interface ChartMilestone {
  at: number;
  value: number;
  label: string; // e.g. "10 TiB"
}

export interface ChartOptions {
  series: ChartSeries[];
  from: number; // unix sec window (from the API's range echo)
  to: number;
  height?: number;
  pins: number[]; // 0–2 pinned timestamps (view-owned state)
  refLines?: ChartRefLine[];
  events?: ChartEvent[];
  milestones?: ChartMilestone[];
  // Floor the y-tick step at 1 — for whole-number metrics (uploads, seeding)
  // in value mode. Rate mode stays fractional (0.5 uploads/day is real).
  integerTicks?: boolean;
  // Show a built-in hover tooltip (date + value per series). The History view
  // has its own readout panel, so it leaves this off; the mini-charts use it.
  tooltip?: boolean;
  onHover?: (t: number | null) => void;
  onPin?: (pins: number[]) => void;
}

const M = { top: 12, right: 14, bottom: 24, left: 52 }; // plot margins

function resolveColor(color: string): string {
  if (color.startsWith('--')) {
    const v = getComputedStyle(document.documentElement).getPropertyValue(color).trim();
    return v || color;
  }
  return color;
}

const svgNS = 'http://www.w3.org/2000/svg';

function svgEl<K extends keyof SVGElementTagNameMap>(tag: K, attrs: Record<string, string | number>): SVGElementTagNameMap[K] {
  const el = document.createElementNS(svgNS, tag);
  for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, String(v));
  return el;
}

export function renderChart(container: HTMLElement, opts: ChartOptions): void {
  const width = Math.max(280, container.clientWidth || 600);
  const height = opts.height ?? 320;
  const plotW = width - M.left - M.right;
  const plotH = height - M.top - M.bottom;
  const { from, to } = opts;
  const span = Math.max(1, to - from);
  const drawable = opts.series.filter(s => s.points.length > 0);

  // ── Scales ────────────────────────────────────────────────────────────────
  // Data extent over every drawn series (projection ghosts included, so the
  // tail fits) — kept separate from the target lines so the two can drive the
  // domain differently.
  let dMin = Infinity, dMax = -Infinity;
  for (const s of drawable) {
    for (const [, v] of s.points) {
      if (v < dMin) dMin = v;
      if (v > dMax) dMax = v;
    }
  }
  const refs = opts.refLines ?? [];
  let rMin = Infinity, rMax = -Infinity;
  for (const r of refs) { if (r.value < rMin) rMin = r.value; if (r.value > rMax) rMax = r.value; }
  const hasRef = refs.length > 0;
  if (!isFinite(dMin)) { dMin = 0; dMax = 1; }

  // A near-flat line carries no shape of its own — the scale is all context.
  const flat = (dMax - dMin) <= Math.max(Math.abs(dMax), 1e-9) * 0.06;

  let yMin: number, yMax: number;
  if (hasRef) {
    // With a target on screen, height should read as "how close am I": ground
    // at zero (unless the data dips negative) so the value sits at its true
    // fraction of the target, and keep whichever of data/target is higher in
    // frame. 14 of 15 → ~90% up; 9.8 of 15 TiB → ~two-thirds up.
    yMin = Math.min(0, dMin);
    yMax = Math.max(dMax, rMax);
  } else if (flat) {
    // No target and no movement: centre the line with zero as the baseline, so
    // it reads at its real magnitude instead of pinned to an edge.
    if (dMax > 0)      { yMin = 0;        yMax = dMax * 2; }
    else if (dMax < 0) { yMin = dMax * 2; yMax = 0; }
    else               { yMin = 0;        yMax = 1; }
  } else {
    // Growing/varying, no target: ground at zero (or the negative floor) so the
    // climb reads against a fixed baseline.
    yMin = Math.min(0, dMin);
    yMax = dMax;
  }
  if (yMin === yMax) { yMin -= 1; yMax += 1; }
  // Headroom so lines and target labels don't kiss the frame. The centred-flat
  // case already has room built in, so skip it there.
  if (!(flat && !hasRef)) {
    const pad = (yMax - yMin) * 0.08;
    if (yMax > 0 || hasRef) yMax += pad;
    if (yMin < 0) yMin -= pad;
  }

  const x = (t: number) => M.left + ((t - from) / span) * plotW;
  const y = (v: number) => M.top + plotH - ((v - yMin) / (yMax - yMin)) * plotH;

  // Interactive (non-ghost) series drive the crosshair, hover dots, and pins.
  const interactive = drawable.filter(s => !s.ghost);

  // Distinct sorted sample times across series — what the crosshair snaps to.
  const timeSet = new Set<number>();
  for (const s of interactive) for (const [t] of s.points) timeSet.add(t);
  const times = [...timeSet].sort((a, b) => a - b);
  const timeTuples: [number, number][] = times.map(t => [t, 0]);

  // ── Static frame ──────────────────────────────────────────────────────────
  const svg = svgEl('svg', {
    viewBox: `0 0 ${width} ${height}`, width, height, class: 'history-chart-svg',
  });

  const unit = drawable[0]?.unit ?? opts.series[0]?.unit ?? 'count';
  // Durations get whole-unit ticks labelled to match the target line (and the
  // rest of the app's duration setting); everything else uses nice 1/2/5 ticks,
  // integer-stepped for whole-number metrics.
  const yticks = unit === 'seconds'
    ? durationTicks(yMin, yMax)
    : niceTicks(yMin, yMax, 5, opts.integerTicks ? 1 : 0);
  for (const v of yticks) {
    svg.appendChild(svgEl('line', {
      x1: M.left, x2: M.left + plotW, y1: y(v), y2: y(v),
      class: 'hc-grid',
    }));
    const lbl = svgEl('text', { x: M.left - 8, y: y(v) + 3, class: 'hc-ylabel' });
    lbl.textContent = unit === 'seconds' ? fmtDurationTick(v) : fmtAxisValue(unit, v);
    svg.appendChild(lbl);
  }
  for (const t of timeTicks(from, to, Math.max(5, Math.floor(plotW / 70)))) {
    const lbl = svgEl('text', { x: x(t), y: height - 8, class: 'hc-xlabel' });
    lbl.textContent = fmtTimeTick(t, span);
    svg.appendChild(lbl);
  }
  svg.appendChild(svgEl('line', {
    x1: M.left, x2: M.left + plotW, y1: M.top + plotH, y2: M.top + plotH, class: 'hc-axis',
  }));

  // ── Reference lines (targets) ─────────────────────────────────────────────
  for (const r of opts.refLines ?? []) {
    const ry = y(r.value);
    svg.appendChild(svgEl('line', { x1: M.left, x2: M.left + plotW, y1: ry, y2: ry, class: 'hc-ref' }));
    const lbl = svgEl('text', { x: M.left + plotW - 4, y: ry - 4, class: 'hc-ref-label' });
    lbl.textContent = r.label;
    svg.appendChild(lbl);
  }

  // Event / milestone markers are drawn LAST (after the hit layer) so their
  // hover tooltips work — see drawAnnotations() near the end.

  // ── Series paths ──────────────────────────────────────────────────────────
  for (const s of drawable) {
    const c = resolveColor(s.color);
    if (s.points.length === 1 && !s.ghost) {
      svg.appendChild(svgEl('circle', { cx: x(s.points[0][0]), cy: y(s.points[0][1]), r: 3, fill: c }));
      continue;
    }
    const d = s.points.map(([t, v], i) => `${i === 0 ? 'M' : 'L'}${x(t).toFixed(1)},${y(v).toFixed(1)}`).join(' ');
    svg.appendChild(svgEl('path', {
      d, fill: 'none', stroke: c, 'stroke-width': s.ghost ? 1.5 : 1.8,
      'stroke-linejoin': 'round', 'stroke-linecap': 'round',
      ...(s.ghost ? { 'stroke-dasharray': '5 4', opacity: 0.75 } : {}),
    }));
  }

  // ── Pins (view-owned, redrawn on every render) ────────────────────────────
  const pinLabels = ['A', 'B'];
  opts.pins.slice(0, 2).forEach((t, i) => {
    const px = x(t);
    svg.appendChild(svgEl('line', { x1: px, x2: px, y1: M.top, y2: M.top + plotH, class: 'hc-pin' }));
    const badge = svgEl('text', { x: px, y: M.top - 2, class: 'hc-pin-label' });
    badge.textContent = pinLabels[i];
    svg.appendChild(badge);
    for (const s of interactive) {
      const idx = nearestIndex(s.points, t);
      if (idx >= 0) {
        svg.appendChild(svgEl('circle', {
          cx: x(s.points[idx][0]), cy: y(s.points[idx][1]), r: 3,
          fill: resolveColor(s.color), class: 'hc-pin-dot',
        }));
      }
    }
  });

  // ── Crosshair (updated in place on pointer moves) ─────────────────────────
  const cross = svgEl('line', { x1: 0, x2: 0, y1: M.top, y2: M.top + plotH, class: 'hc-crosshair', visibility: 'hidden' });
  svg.appendChild(cross);
  const hoverDots = interactive.map(s => {
    const dot = svgEl('circle', { cx: 0, cy: 0, r: 3.5, fill: resolveColor(s.color), class: 'hc-hover-dot', visibility: 'hidden' });
    svg.appendChild(dot);
    return dot;
  });

  const hit = svgEl('rect', {
    x: M.left, y: M.top, width: plotW, height: plotH,
    fill: 'transparent', class: 'hc-hit',
  });
  svg.appendChild(hit);

  const snapT = (clientX: number): number | null => {
    if (!times.length) return null;
    const rect = svg.getBoundingClientRect();
    // The viewBox matches on-screen pixels 1:1 in width; still scale defensively.
    const sx = (clientX - rect.left) * (width / rect.width);
    const t = from + ((sx - M.left) / plotW) * span;
    return times[nearestIndex(timeTuples, t)];
  };

  // Optional built-in hover tooltip (date + value per interactive series).
  const tip = opts.tooltip ? document.createElement('div') : null;
  if (tip) { tip.className = 'hc-tip'; tip.style.display = 'none'; }
  const updateTip = (t: number) => {
    if (!tip) return;
    const rows = interactive.map(s => {
      const idx = nearestIndex(s.points, t);
      if (idx < 0) return '';
      const c = resolveColor(s.color);
      return `<div class="hc-tip-row"><span class="hc-tip-dot" style="background:${c}"></span>`
        + `${esc(s.label)} <b>${esc(fmtUnitValue(s.unit, s.points[idx][1]))}</b></div>`;
    }).join('');
    if (!rows) { tip.style.display = 'none'; return; }
    tip.innerHTML = `<div class="hc-tip-date">${esc(fmtTimeTick(t, span))}</div>${rows}`;
    tip.style.display = 'block';
    let lx = x(t) + 12;
    if (lx + tip.offsetWidth > width - 4) lx = x(t) - tip.offsetWidth - 12;
    tip.style.left = `${Math.max(4, lx)}px`;
    tip.style.top = `${M.top + 2}px`;
  };

  const showCross = (t: number) => {
    const cx = x(t);
    cross.setAttribute('x1', String(cx));
    cross.setAttribute('x2', String(cx));
    cross.setAttribute('visibility', 'visible');
    interactive.forEach((s, i) => {
      const idx = nearestIndex(s.points, t);
      const dot = hoverDots[i];
      if (idx < 0) { dot.setAttribute('visibility', 'hidden'); return; }
      dot.setAttribute('cx', String(x(s.points[idx][0])));
      dot.setAttribute('cy', String(y(s.points[idx][1])));
      dot.setAttribute('visibility', 'visible');
    });
    updateTip(t);
  };

  hit.addEventListener('pointermove', e => {
    const t = snapT(e.clientX);
    if (t == null) return;
    showCross(t);
    opts.onHover?.(t);
  });
  hit.addEventListener('pointerleave', () => {
    cross.setAttribute('visibility', 'hidden');
    hoverDots.forEach(d => d.setAttribute('visibility', 'hidden'));
    if (tip) tip.style.display = 'none';
    opts.onHover?.(null);
  });
  hit.addEventListener('click', e => {
    const t = snapT(e.clientX);
    if (t == null) return;
    // Pin cycle: none → A · A → A+B · A+B → fresh A. Re-clicking a pinned
    // time unpins it.
    let pins = opts.pins.slice(0, 2);
    if (pins.includes(t)) pins = pins.filter(p => p !== t);
    else if (pins.length >= 2) pins = [t];
    else pins = [...pins, t].sort((a, b) => a - b);
    opts.onPin?.(pins);
  });

  // ── Annotations (drawn on TOP of the hit layer so hover tooltips work) ─────
  const dateStr = (t: number) => new Date(t * 1000).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: '2-digit' });
  const eventColor = (k: ChartEvent['kind']) =>
    resolveColor(k === 'promotion' ? '--green' : k === 'demotion' ? '--red' : '--text3');
  for (const ev of opts.events ?? []) {
    if (ev.at < from || ev.at > to) continue;
    const ex = x(ev.at);
    const g = svgEl('g', { class: 'hc-event' });
    const title = svgEl('title', {});
    const verb = ev.kind === 'promotion' ? 'Promoted' : ev.kind === 'demotion' ? 'Demoted' : 'Group change';
    title.textContent = `${verb}: ${ev.detail} · ${dateStr(ev.at)}`;
    g.appendChild(title);
    g.appendChild(svgEl('line', { x1: ex, x2: ex, y1: M.top, y2: M.top + plotH, stroke: eventColor(ev.kind), 'stroke-width': 1, 'stroke-dasharray': '2 3', opacity: 0.85 }));
    // Widen the hover target with an invisible thicker line.
    g.appendChild(svgEl('line', { x1: ex, x2: ex, y1: M.top, y2: M.top + plotH, stroke: 'transparent', 'stroke-width': 8, 'pointer-events': 'stroke' }));
    const flag = svgEl('text', { x: ex + 3, y: M.top + 9, fill: eventColor(ev.kind), 'font-size': 9, 'font-weight': 700 });
    flag.textContent = (ev.kind === 'promotion' ? '▲ ' : ev.kind === 'demotion' ? '▼ ' : '') + ev.label;
    g.appendChild(flag);
    svg.appendChild(g);
  }

  const msColor = resolveColor('--amber');
  for (const ms of opts.milestones ?? []) {
    if (ms.at < from || ms.at > to) continue;
    const mx = x(ms.at), my = y(ms.value);
    const g = svgEl('g', { class: 'hc-milestone' });
    const title = svgEl('title', {});
    title.textContent = `Reached ${ms.label} · ${dateStr(ms.at)}`;
    g.appendChild(title);
    const r = 4;
    // Invisible larger hit circle so the tooltip is easy to trigger.
    g.appendChild(svgEl('circle', { cx: mx, cy: my, r: 9, fill: 'transparent', 'pointer-events': 'all' }));
    g.appendChild(svgEl('path', {
      d: `M${mx},${my - r} L${mx + r},${my} L${mx},${my + r} L${mx - r},${my} Z`,
      fill: msColor, stroke: resolveColor('--surface'), 'stroke-width': 1,
    }));
    const lbl = svgEl('text', { x: mx, y: my - r - 3, 'text-anchor': 'middle', fill: msColor, 'font-size': 8.5, 'font-weight': 700 });
    lbl.textContent = ms.label;
    g.appendChild(lbl);
    svg.appendChild(g);
  }

  container.replaceChildren(svg);
  if (tip) {
    if (!container.style.position) container.style.position = 'relative';
    container.appendChild(tip);
  }
}

// ── Export ────────────────────────────────────────────────────────────────

/** Copy the presentation styles the CSS supplies (labels, gridlines) onto the
 *  clone as inline attributes, so the exported file renders standalone. Orig
 *  and clone share structure, so we zip their descendant lists. */
function inlineComputedStyles(orig: SVGElement, clone: SVGElement): void {
  const props = ['fill', 'stroke', 'stroke-width', 'stroke-dasharray', 'opacity', 'font-size', 'font-weight', 'font-family', 'text-anchor'];
  const o = orig.querySelectorAll('*');
  const c = clone.querySelectorAll('*');
  for (let i = 0; i < o.length && i < c.length; i++) {
    const cs = getComputedStyle(o[i]);
    let style = '';
    for (const p of props) {
      const v = cs.getPropertyValue(p);
      if (v && v !== 'none' && v !== 'normal') style += `${p}:${v};`;
    }
    if (style) c[i].setAttribute('style', style);
  }
}

function downloadBlob(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

/** Save the chart currently in `container` as an SVG or PNG file. Interactive
 *  layers (crosshair, hover dots, hit area) are stripped and a solid theme
 *  background is added so the image reads on its own. */
export function exportChart(container: HTMLElement, format: 'svg' | 'png', filename: string): void {
  const svg = container.querySelector('svg');
  if (!svg) return;
  const clone = svg.cloneNode(true) as SVGSVGElement;
  inlineComputedStyles(svg, clone); // before removals — structures still match
  clone.querySelectorAll('.hc-hit, .hc-crosshair, .hc-hover-dot').forEach(e => e.remove());

  const vb = svg.viewBox.baseVal;
  const w = vb.width || svg.clientWidth || 800;
  const h = vb.height || svg.clientHeight || 320;
  const bg = document.createElementNS(svgNS, 'rect');
  for (const [k, v] of Object.entries({ x: 0, y: 0, width: w, height: h, fill: resolveColor('--surface') })) bg.setAttribute(k, String(v));
  clone.insertBefore(bg, clone.firstChild);
  clone.setAttribute('xmlns', svgNS);

  const xml = new XMLSerializer().serializeToString(clone);
  if (format === 'svg') {
    downloadBlob(new Blob([xml], { type: 'image/svg+xml;charset=utf-8' }), `${filename}.svg`);
    return;
  }
  const scale = 2;
  const img = new Image();
  img.onload = () => {
    const canvas = document.createElement('canvas');
    canvas.width = w * scale;
    canvas.height = h * scale;
    const ctx = canvas.getContext('2d');
    if (!ctx) return;
    ctx.scale(scale, scale);
    ctx.drawImage(img, 0, 0);
    canvas.toBlob(b => { if (b) downloadBlob(b, `${filename}.png`); }, 'image/png');
  };
  img.src = 'data:image/svg+xml;base64,' + btoa(unescape(encodeURIComponent(xml)));
}
