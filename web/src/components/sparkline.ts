// components/sparkline.ts — SVG sparkline rendering

/**
 * Resolve a color that may be a CSS variable name (e.g. "--green") or a
 * raw hex/rgb string.  Reading getComputedStyle at call-time means the value
 * always matches the active theme — no stale hex strings baked into SVGs.
 */
function resolveColor(color: string): string {
  if (color.startsWith('--')) {
    const val = getComputedStyle(document.documentElement)
      .getPropertyValue(color)
      .trim();
    return val || color;
  }
  return color;
}

/** Renders a sparkline SVG into the element with the given id */
export function renderSparkline(containerId: string, values: number[], color: string): void {
  const el = document.getElementById(containerId);
  if (!el) return;

  const c = resolveColor(color);
  const w = el.offsetWidth || 200;
  const h = 36;

  // Drop holes and non-finite entries before anything measures the series.
  // A sparse array reads as populated to .length and .every() (both skip
  // holes) but collapses to undefined on indexed access, so without this a
  // caller passing one would throw here and abort ITS whole render pass —
  // which is exactly how one bad card once blanked every sparkline on the
  // dashboard. Cheap insurance at the boundary.
  values = values.filter(v => typeof v === 'number' && Number.isFinite(v));

  if (!values.length || values.every(v => v === 0)) {
    el.innerHTML = `<svg viewBox="0 0 ${w} ${h}" preserveAspectRatio="none">
      <line x1="0" y1="${h / 2}" x2="${w}" y2="${h / 2}"
        stroke="${c}" stroke-width="1" stroke-dasharray="4 4" opacity="0.3"/>
    </svg>`;
    return;
  }

  const min = Math.min(...values);
  const max = Math.max(...values);
  const range = max - min || 1;
  const pad = 4;

  const pts = values.map((v, i) => {
    const x = values.length === 1 ? w / 2 : (i / (values.length - 1)) * (w - pad * 2) + pad;
    const y = h - pad - ((v - min) / range) * (h - pad * 2);
    return [x, y] as [number, number];
  });

  const pathD = pts.map(([x, y], i) => (i === 0 ? `M${x},${y}` : `L${x},${y}`)).join(' ');
  const areaD = pathD + ` L${pts[pts.length - 1][0]},${h} L${pts[0][0]},${h} Z`;
  const last = pts[pts.length - 1];
  const uid = containerId.replace(/[^a-zA-Z0-9]/g, '_');

  el.innerHTML = `<svg viewBox="0 0 ${w} ${h}" preserveAspectRatio="none" xmlns="http://www.w3.org/2000/svg">
    <defs><linearGradient id="sg_${uid}" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0%" stop-color="${c}" stop-opacity="0.25"/>
      <stop offset="100%" stop-color="${c}" stop-opacity="0"/>
    </linearGradient></defs>
    <path d="${areaD}" fill="url(#sg_${uid})"/>
    <path d="${pathD}" fill="none" stroke="${c}" stroke-width="1.5"
      stroke-linejoin="round" stroke-linecap="round"/>
    <circle cx="${last[0]}" cy="${last[1]}" r="2.5" fill="${c}"/>
  </svg>`;
}
