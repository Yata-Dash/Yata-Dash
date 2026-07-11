// README / forum screenshot harness. Run against a DEMO instance (tools/demoseed):
//
//   go run ./tools/demoseed -config <demo.json> -db <demo.db>
//   <start yata against those files>
//   node tools/screenshots.mjs http://localhost:8425
//
// Captures docs/screenshots/*.png. Cosmetic-only tweaks are injected at
// capture time (hide the "no API key" banners the credential-less demo
// trackers produce, steady green status dots, synthetic qui bar values) —
// all data shown is the synthetic demoseed data.
import puppeteer from '../web/node_modules/puppeteer-core/lib/esm/puppeteer/puppeteer-core.js';
import { mkdirSync } from 'node:fs';

const BASE = process.argv[2] || 'http://localhost:8425';
const OUT = new URL('../docs/screenshots/', import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, '$1');
mkdirSync(OUT, { recursive: true });

// Chrome for Testing (npx puppeteer browsers install chrome), overridable via
// CHROME_PATH. Edge's headless mode hands off to a proxy process and exits,
// so it can't be driven.
const CHROME = process.env.CHROME_PATH
  || `${process.env.USERPROFILE?.replace(/\\/g, '/')}/.cache/puppeteer/chrome/win64-148.0.7778.97/chrome-win64/chrome.exe`;

const COSMETIC_CSS = `
  .card-error-msg { display: none !important; }
  .sdot { background: var(--green) !important; animation: none !important; }
  .scrape-limit-badge { display: none !important; }
  #auth-nudge { display: none !important; }
`;

const sleep = ms => new Promise(r => setTimeout(r, ms));

/** The demo trackers carry no credentials (the app must never contact the
 *  real sites), so connection-state indicators would all read "down", and the
 *  qui bar (no reachable qui) would read "not reachable" — pure demo-
 *  environment noise. Neutralise them; every stat shown is synthetic. */
async function polish() {
  await page.evaluate(() => {
    for (const pfx of ['g', 't']) {
      const n = document.getElementById(`${pfx}-agg-health-num`);
      const s = document.getElementById(`${pfx}-agg-health-sub`);
      if (n) n.textContent = '6';
      if (s) s.textContent = 'all healthy';
      document.getElementById(`${pfx}-health-card`)?.style.setProperty('--card-accent', 'var(--green)');
    }
    const st = document.getElementById('t-sum-status');
    if (st) st.textContent = '6 / 6 active';
    document.querySelectorAll('span').forEach(sp => {
      if (sp.textContent.startsWith('API key not configured')) {
        const strip = sp.parentElement;
        if (strip && strip.children.length <= 2) strip.style.display = 'none';
      }
    });

    // Synthetic qui (qBittorrent) bar — the demo has no reachable qui, so fill
    // the real bar component with representative values for the capture.
    document.querySelectorAll('.qui-inst-bar').forEach(bar => {
      const set = (cls, val, color) => {
        const el = bar.querySelector(cls);
        if (!el) return;
        el.textContent = val;
        if (color) el.style.color = `var(--${color})`;
      };
      const label = bar.querySelector('.qui-bar-label');
      if (label && label.lastChild) label.lastChild.textContent = 'seedbox';
      const dot = bar.querySelector('.qi-dot');
      if (dot) dot.className = 'sdot green qi-dot';
      set('.qi-conn-label', 'connected');
      set('.qi-dl-speed', '1.2 MiB/s');
      set('.qi-ul-speed', '18.4 MiB/s');
      set('.qi-free-space', '312.6 GiB');
      set('.qi-global-ratio', '3.12', 'green');
      const rIcon = bar.querySelector('.qi-ratio-icon');
      if (rIcon) rIcon.style.fill = 'var(--green)';
      set('.qi-total-size', '18.42 TiB');
      set('.qi-total-torrents', '1,420');
      set('.qi-seeding', '1,376');
      set('.qi-downloading', '2');
      set('.qi-checking', '0');
      set('.qi-paused', '40');
      set('.qi-error', '2');
    });
  });
}

const browser = await puppeteer.launch({
  executablePath: CHROME,
  headless: 'new',
  args: [
    '--window-size=1520,980', '--hide-scrollbars',
    '--no-first-run', '--no-default-browser-check',
    `--user-data-dir=${process.env.TEMP || '/tmp'}/yata-cft-profile`,
  ],
});
const page = await browser.newPage();
await page.setViewport({ width: 1440, height: 900, deviceScaleFactor: 2 });

async function fresh(hash = '#/') {
  await page.goto(`${BASE}/${hash}`, { waitUntil: 'networkidle2' });
  await page.addStyleTag({ content: COSMETIC_CSS });
  await sleep(1200);
}

async function shot(name, opts = {}) {
  await polish();
  await page.screenshot({ path: `${OUT}${name}.png`, ...opts });
  console.log('captured', name);
}

// 1. Dashboard — grid view (hero). Keep the hero clean: hide the qui bar here
//    (it's showcased on the Detail view + its own shot).
await fresh('#/');
await page.evaluate(() => {
  window.setView('grid');
  document.getElementById('qui-bars-grid')?.style.setProperty('display', 'none');
});
await sleep(800);
await shot('dashboard-grid');

// 2. One card close-up (targets with the any_of "One of" block + group badge).
// The card is taller than the viewport — the sticky topbar would get
// composited over its header during the stitched element capture. Grow the
// viewport and hide the topbar for this one shot.
await page.setViewport({ width: 1440, height: 2200, deviceScaleFactor: 2 });
await page.evaluate(() => { document.querySelector('.topbar').style.display = 'none'; });
const antCard = await page.evaluateHandle(() => {
  const c = [...document.querySelectorAll('.tracker-card')].find(x => x.innerText.includes('Anthelion'));
  c?.scrollIntoView({ block: 'start' });
  return c;
});
await sleep(600);
if (antCard && antCard.asElement()) await antCard.asElement().screenshot({ path: `${OUT}card-targets.png` });
console.log('captured card-targets');
await page.evaluate(() => { document.querySelector('.topbar').style.display = ''; });
await page.setViewport({ width: 1440, height: 900, deviceScaleFactor: 2 });

// 3. Detail (table) view — enter collapsed first for the qui-bar + private-mode
//    crops, then expand a row for the full detail shot.
await page.evaluate(() => window.setView('table'));
await sleep(800);

// 3a. qui bar close-up — top strip (topbar + aggregate cards + qui bar).
await polish();
await page.screenshot({ path: `${OUT}qui-bar.png`, clip: { x: 0, y: 0, width: 1440, height: 336 } });
console.log('captured qui-bar');

// 3b. Private mode — usernames blurred. Crop the top-left of the detail table.
await page.evaluate(() => document.body.classList.add('private-mode'));
await sleep(300);
const tbl = await page.$('#view-table table');
if (tbl) {
  const bb = await tbl.boundingBox();
  await page.screenshot({
    path: `${OUT}private-mode.png`,
    clip: { x: Math.max(0, bb.x), y: bb.y, width: Math.min(720, bb.width), height: Math.min(600, bb.height) },
  });
  console.log('captured private-mode');
}
await page.evaluate(() => document.body.classList.remove('private-mode'));

// 3c. Full detail shot with one row expanded (sparklines + full stat panel).
await page.evaluate(() => {
  const row = document.querySelector('tr[id^="trow-"], tbody tr');
  row?.click();
});
await sleep(900);
await shot('dashboard-table');

// 4. Pathways — pick a target, expand the first step chip.
await page.evaluate(() => window.setView('pathways'));
await sleep(1000);
const target = await page.evaluate(async () => {
  const input = document.getElementById('pw-target-input');
  if (!input) return 'no-input';
  input.focus();
  input.value = 'Blutopia';
  input.dispatchEvent(new Event('input', { bubbles: true }));
  await new Promise(r => setTimeout(r, 500));
  const opt = [...document.querySelectorAll('#pw-combo-list *')].find(e => e.innerText?.trim().startsWith('Blutopia'));
  opt?.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }));
  opt?.click();
  return opt ? 'ok' : 'no-option';
});
console.log('pathways target:', target);
await sleep(1500);
await page.evaluate(() => document.querySelector('.pw-chip-step')?.click());
await sleep(700);
await shot('pathways');

// 5–8. Settings tabs.
for (const [tab, name] of [
  ['trackers', 'settings-trackers'],
  ['scraping', 'settings-scraping'],
  ['display', 'settings-display'],
  ['alerts', 'settings-alerts'],
]) {
  await fresh('#/settings');
  await page.evaluate(t => window.switchSettingsTab(t), tab);
  await sleep(tab === 'alerts' ? 1500 : 1000);
  await shot(name);
}

// 9–10. History view (flag-gated — set the dev flag + preset UI state in
// localStorage, then full-load so boot reads them). Captured for forums /
// future use; the feature still ships dormant.
async function historyShot(name, ui) {
  await page.evaluate(u => {
    localStorage.setItem('yata.features.history', '1');
    localStorage.setItem('u3d-view', 'history');
    localStorage.setItem('yata.history.ui', JSON.stringify(u));
  }, ui);
  await page.goto(`${BASE}/`, { waitUntil: 'networkidle2' });
  await page.addStyleTag({ content: COSMETIC_CSS });
  await sleep(1800); // history fetch + chart render
  await polish();
  const el = await page.$('#view-history');
  if (el) { await el.screenshot({ path: `${OUT}${name}.png` }); console.log('captured', name); }
}

// history.png — multi-tracker overlay (the headline read).
await historyShot('history', {
  metric: 'uploaded', range: '365d', trackers: null,
  mode: 'value', portfolio: false, projection: false,
});
// history-targets.png — single tracker with the projection tail crossing its
// target line (seedpool seed size approaching the 1 TiB target).
await historyShot('history-targets', {
  metric: 'seed_size', range: '90d', trackers: ['demoseedpool0001'],
  mode: 'value', portfolio: false, projection: true,
});

await browser.close();
console.log('done →', OUT);
