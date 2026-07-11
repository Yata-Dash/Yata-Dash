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
import { fmtAxisValue, fmtTimeTick, nearestIndex, niceTicks, timeTicks } from '../utils/series';
import type { SeriesUnit } from '../utils/series';

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
  let yMin = Infinity, yMax = -Infinity;
  for (const s of drawable) {
    for (const [, v] of s.points) {
      if (v < yMin) yMin = v;
      if (v > yMax) yMax = v;
    }
  }
  // Reference lines are part of the story — keep them on screen.
  for (const r of opts.refLines ?? []) {
    if (r.value < yMin) yMin = r.value;
    if (r.value > yMax) yMax = r.value;
  }
  if (!isFinite(yMin)) { yMin = 0; yMax = 1; }
  if (yMin === yMax) { yMin -= 1; yMax += 1; }
  // Ground near-zero domains at 0 (a count going 0→4 should start at 0),
  // then pad 5% so lines don't kiss the frame.
  if (yMin > 0 && yMin < (yMax - yMin) * 0.5) yMin = 0;
  const pad = (yMax - yMin) * 0.05;
  yMin = yMin === 0 ? 0 : yMin - pad;
  yMax += pad;

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
  for (const v of niceTicks(yMin, yMax, 5)) {
    svg.appendChild(svgEl('line', {
      x1: M.left, x2: M.left + plotW, y1: y(v), y2: y(v),
      class: 'hc-grid',
    }));
    const lbl = svgEl('text', { x: M.left - 8, y: y(v) + 3, class: 'hc-ylabel' });
    lbl.textContent = fmtAxisValue(unit, v);
    svg.appendChild(lbl);
  }
  for (const t of timeTicks(from, to, Math.max(3, Math.floor(plotW / 90)))) {
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

  // ── Event markers (group changes) — vertical line + label + hover title ────
  const eventColor = (k: ChartEvent['kind']) =>
    resolveColor(k === 'promotion' ? '--green' : k === 'demotion' ? '--red' : '--text3');
  for (const ev of opts.events ?? []) {
    if (ev.at < from || ev.at > to) continue;
    const ex = x(ev.at);
    const g = svgEl('g', { class: 'hc-event' });
    const title = svgEl('title', {});
    title.textContent = ev.detail;
    g.appendChild(title);
    g.appendChild(svgEl('line', { x1: ex, x2: ex, y1: M.top, y2: M.top + plotH, stroke: eventColor(ev.kind), 'stroke-width': 1, 'stroke-dasharray': '2 3', opacity: 0.85 }));
    const flag = svgEl('text', { x: ex + 3, y: M.top + 9, fill: eventColor(ev.kind), 'font-size': 9, 'font-weight': 700 });
    flag.textContent = (ev.kind === 'promotion' ? '▲ ' : ev.kind === 'demotion' ? '▼ ' : '') + ev.label;
    g.appendChild(flag);
    svg.appendChild(g);
  }

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

  // ── Milestone markers (threshold crossings) — diamond + hover title ────────
  const msColor = resolveColor('--amber');
  for (const ms of opts.milestones ?? []) {
    if (ms.at < from || ms.at > to) continue;
    const mx = x(ms.at), my = y(ms.value);
    const g = svgEl('g', { class: 'hc-milestone' });
    const title = svgEl('title', {});
    title.textContent = ms.label;
    g.appendChild(title);
    const r = 3.5;
    g.appendChild(svgEl('path', {
      d: `M${mx},${my - r} L${mx + r},${my} L${mx},${my + r} L${mx - r},${my} Z`,
      fill: msColor, stroke: resolveColor('--surface'), 'stroke-width': 1,
    }));
    svg.appendChild(g);
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

  container.replaceChildren(svg);
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
