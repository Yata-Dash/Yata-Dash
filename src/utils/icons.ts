// utils/icons.ts — Font Awesome Pro fallback.
//
// Tracker defs use the EXACT icon classes the tracker sites use, many of
// which are FA Pro-only (fa-lobster, fa-squirrel, fa-whale, …). Users who
// drop Font Awesome Pro into static/fontawesome/ see the real icons; for
// everyone else those classes have no CSS rule and render as blank space.
//
// This module detects missing glyphs at runtime (an icon class with no
// matching stylesheet rule has no ::before content) and swaps them for a
// free fallback icon. A MutationObserver covers every render path — grid,
// table, modals, popovers — with no per-render wiring. Start it ONLY after
// the stylesheet situation is settled (free CDN loaded, self-hosted Pro
// probe finished), or Pro icons would be "fixed" before their CSS arrives.

/** Free icon substituted for any icon class that has no glyph. */
const FALLBACK = 'fas fa-star';

/** Amber globe marking an active tracker event (freeleech/announcement) —
 *  echoes the 🌐 most trackers use for "global" events. Inline SVG (not an
 *  FA class) so it renders regardless of icon-set state, stroked with
 *  var(--amber) so it follows themes. Replaced the old bell, which now
 *  exclusively means "unread notifications". */
export function eventGlobeSvg(extraStyle = ''): string {
  return `<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="var(--amber)" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"${extraStyle ? ` style="${extraStyle}"` : ''}><circle cx="12" cy="12" r="10"/><path d="M2 12h20"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/></svg>`;
}

/** Eye-with-a-slash mark for a requirement that can't be tracked — the
 *  tracker doesn't report the stat, or it was never a stat to begin with.
 *  Shared by the targets rows (grid/detail) and the pathways requirement
 *  rows so "can't measure this" looks the same everywhere. */
export function unavailEyeSvg(cls: string): string {
  return `<svg class="${cls}" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19m-6.72-1.07a3 3 0 1 1-4.24-4.24"/><path d="M1 1l22 22"/></svg>`;
}

/** class string → has a renderable glyph (cached — one probe per class). */
const glyphCache = new Map<string, boolean>();

let probeBox: HTMLElement | null = null;
let started = false;

function ensureProbeBox(): HTMLElement {
  if (!probeBox) {
    probeBox = document.createElement('div');
    // Rendered but invisible — display:none would suppress ::before styles.
    probeBox.style.cssText =
      'position:absolute;left:-9999px;top:-9999px;visibility:hidden;pointer-events:none';
    document.body.appendChild(probeBox);
  }
  return probeBox;
}

/** True when the class string produces an actual ::before glyph.
 *  Handles both FA mechanisms: direct `content:"\fxxx"` rules (FA5-style)
 *  and FA6's `--fa` custom property consumed by `.fas::before{content:
 *  var(--fa)}`. A missing icon has neither: computed content is "none"
 *  (var() invalid at computed-value time) and --fa is unset.
 *
 *  A rule alone isn't enough: with a PARTIAL self-hosted kit (css/ copied but
 *  some webfonts/ files missing) the codepoint is set but the font never
 *  loads, and the browser draws a tofu box. probeIconFonts() records those
 *  dead family/weight pairs so this treats them as glyph-less too. */
function hasGlyph(cls: string): boolean {
  const cached = glyphCache.get(cls);
  if (cached !== undefined) return cached;
  const el = document.createElement('i');
  el.className = cls;
  ensureProbeBox().appendChild(el);
  const style = getComputedStyle(el);
  const content = getComputedStyle(el, '::before').content;
  const fa = style.getPropertyValue('--fa').trim();
  const fontKey = `${style.fontFamily}|${style.fontWeight}`;
  el.remove();
  const ok = ((content !== 'none' && content !== 'normal') || fa !== '')
    && !deadFontKeys.has(fontKey);
  glyphCache.set(cls, ok);
  return ok;
}

// ── Dead font detection (partial self-hosted kits) ──────────────────────────

/** One icon style whose font file failed to load. */
export interface DeadFontStyle {
  prefix: string; // fas | far | fal | fat | fad | fab
  family: string; // computed font-family, e.g. "Font Awesome 6 Pro"
  weight: string; // 900 | 400 | 300 | 100
  label: string;  // human-readable, e.g. "Light 300 (Font Awesome 6 Pro)"
}

const WEIGHT_STYLE: Record<string, string> = {
  '100': 'Thin', '300': 'Light', '400': 'Regular', '900': 'Solid',
};

/** `${family}|${weight}` pairs whose font file failed to load. */
const deadFontKeys = new Set<string>();

/** First family name, unquoted/lowercased, for FontFace comparisons. */
const famNorm = (s: string) => s.split(',')[0].trim().replace(/^['"]|['"]$/g, '').toLowerCase();

/** Force-load the font behind each FA style prefix and record the ones that
 *  fail (missing/corrupt .woff2 in a self-hosted kit). A prefix with no CSS
 *  rule at all is skipped — the content probe in hasGlyph covers that. */
async function probeIconFonts(): Promise<DeadFontStyle[]> {
  const dead: DeadFontStyle[] = [];
  const seen = new Set<string>();
  for (const prefix of ['fas', 'far', 'fal', 'fat', 'fad', 'fab']) {
    const el = document.createElement('i');
    el.className = `${prefix} fa-star`; // fa-star exists in every style
    ensureProbeBox().appendChild(el);
    const style = getComputedStyle(el);
    const content = getComputedStyle(el, '::before').content;
    const fa = style.getPropertyValue('--fa').trim();
    const family = style.fontFamily;
    const weight = style.fontWeight;
    el.remove();
    if (content === 'none' || content === 'normal') {
      if (fa === '') continue; // no CSS rule for this style — not a font issue
    }
    const key = `${family}|${weight}`;
    if (seen.has(key)) continue;
    seen.add(key);
    // Find the exact @font-face declarations for this family+weight and
    // load them directly. (document.fonts.check() is unusable here: CSS font
    // matching happily substitutes the nearest weight, so a loaded Solid-900
    // face makes a check for the missing Light-300 come back true.)
    const target = famNorm(family);
    const w = parseInt(weight, 10) || 400;
    const faces: FontFace[] = [];
    document.fonts.forEach(f => {
      if (famNorm(f.family) !== target) return;
      const parts = String(f.weight).split(' ').map(p => parseInt(p, 10)).filter(n => !isNaN(n));
      if (parts.length) {
        const min = Math.min(...parts), max = Math.max(...parts);
        if (w < min || w > max) return;
      }
      faces.push(f);
    });
    let ok: boolean;
    if (faces.length) {
      await Promise.allSettled(faces.map(f => f.load()));
      ok = faces.some(f => f.status === 'loaded');
    } else {
      // No face declared at all -- fall back to a coarse check.
      const spec = `${weight} 16px ${family}`;
      try {
        await document.fonts.load(spec, '');
      } catch { /* treated as unavailable below */ }
      ok = document.fonts.check(spec, '');
    }
    if (!ok) {
      deadFontKeys.add(key);
      dead.push({
        prefix, family, weight,
        label: `${WEIGHT_STYLE[weight] ?? ''} ${weight} (${family.replace(/"/g, '')})`.trim(),
      });
    }
  }
  return dead;
}

/** Swap glyph-less fa-* icons under root for the free fallback icon. */
export function fixupIcons(root: ParentNode): void {
  root.querySelectorAll<HTMLElement>('i[class*="fa-"]').forEach(el => {
    if (el.dataset['faFallback']) return;
    const cls = el.className;
    if (!cls || hasGlyph(cls)) return;
    el.className = FALLBACK;
    el.dataset['faFallback'] = '1'; // don't re-probe; keeps inline color/style
  });
}

let _probePromise: Promise<DeadFontStyle[]> | null = null;

/**
 * Start the fallback engine: one full-document sweep now, then a
 * MutationObserver keeps fixing icons as the app re-renders. Also probes
 * the icon FONTS themselves — a partial self-hosted kit (missing .woff2
 * files) renders tofu boxes that the content probe can't see; once found,
 * the affected styles are re-swept into fallbacks.
 * Call AFTER the final icon stylesheet (free or self-hosted Pro) has loaded.
 * Resolves with the styles whose fonts failed to load ([] when all is well).
 */
export function startIconFallback(): Promise<DeadFontStyle[]> {
  if (started) return _probePromise ?? Promise.resolve([]);
  started = true;
  fixupIcons(document);
  const mo = new MutationObserver(muts => {
    for (const m of muts) {
      m.addedNodes.forEach(n => {
        if (n.nodeType === Node.ELEMENT_NODE) fixupIcons(n as Element);
      });
    }
  });
  mo.observe(document.body, { childList: true, subtree: true });

  _probePromise = probeIconFonts().then(dead => {
    if (dead.length) {
      // Styles found dead after the first sweep — re-probe everything.
      glyphCache.clear();
      fixupIcons(document);
      console.warn(
        `Yata: Font Awesome style(s) failed to load: ${dead.map(d => d.label).join(', ')} — ` +
        `affected icons show a fallback. If you self-host Font Awesome, copy the missing ` +
        `.woff2 files into static/fontawesome/webfonts/ (next to css/).`);
    }
    return dead;
  });
  return _probePromise;
}
