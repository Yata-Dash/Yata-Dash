# Changelog

All notable changes to Yata, newest first. Versions are date-based builds:
`Beta-YYYYMMDD[letter]`.

How this is used: jot changes under **Unreleased** as you work. When you cut a
release (dev.ps1 → *Cut a release*), move the Unreleased notes under a new
version heading — those notes become the GitHub Release body automatically.

## [Unreleased]

### Changed
- **Untrackable target requirements now stay visible.** When a target's stat
  isn't reported by a tracker's API (e.g. an API-only tracker whose profile
  omits seed time or seed size), the requirement no longer silently vanishes
  from the TARGETS section — it shows with an eye-off icon, an italic *Not
  tracked* label, the required value, and a dashed placeholder bar, plus a
  tooltip explaining the stat can't be tracked but the requirement still
  applies. A stat is treated as untrackable only when the tracker has been
  fetched and returned other fields but not this one, so a not-yet-polled or
  failed tracker doesn't show a wall of false "not tracked" rows. Untrackable
  rows are excluded from the promotion-ETA / "Eligible now" maths, so those
  headlines are unchanged. Applies everywhere targets render — grid cards, the
  Detail page, and the Detail table.

## [Beta-20260716]

### Added
- **RetroFlix now uses its API instead of scraping.** RetroFlix finished their
  API expansion, so the def switched to their `/api/me` endpoint (Bearer/JWT
  auth), working on both **retroflix.net** and **retroflix.club**. Stats come
  straight from the API now — ratio, up/down (with computed buffer), seed
  bonus, snatched, average/total seed time, hit-and-runs, invites, join date,
  and your membership class as its named group. Two small, reusable def
  mechanisms landed with it: **`class_map`** (turn a numeric membership
  "class" into its named group) and **`bool_fields`** (turn a count like
  unread private messages into the unread-mail flag). The previous scrape
  setup is retained in the def as dormant reference, and the scrape-only
  tracker type stays supported for any future scrape-only tracker.
- **Tracker Detail page** — click any tracker's name (on a card, in the
  Detail table, or via the edit screen's new *Details* button) for a single
  page with everything Yata knows about it: identity header (group, member
  age, last update, refresh/profile/edit shortcuts), **mini-charts** picked
  from the tracker's set targets (falling back to ratio, seed size, upload,
  download, buffer and avg seed time — swap in any of the eleven recorded
  metrics from the Charts menu, up to ten, remembered per tracker) with
  target lines drawn in and a click-through into the full History view,
  every reported stat, targets progress, the account **rules**, **invite
  routes leaving this tracker** (with "reqs met" markers, same engine as
  Pathways — your Pathways favourites keep their ★ and sort first, and "not
  interested" targets are hidden), and a **group-change timeline** of
  recorded promotions ▲ and demotions ▼.
  **Active-event banner and unread flags.** A tracker's current event (freeleech/announcement) appears as the
  same amber banner with live countdown you get in the grid/table, and unread
  mail/notification icons sit in the header — each following its existing
  Settings → Display toggle.
- **Chart projection on the Tracker Detail page.** A *Projection* toggle
  extends every mini-chart's line at its recent rate (dashed), so you can see
  where a stat is heading. When a projected line rises to meet a target it's
  currently below, the tail turns **green** — a quick read on whether your
  current trajectory gets you there.
- **Tracker rules at a glance — min ratio + min seed time.** Definitions can
  now record the tracker's minimum per-torrent seed time in days (display-only
  reference — the fine print stays on the tracker's rules page). The Detail
  view gains a **Rules** section showing Min Ratio and Min Seed Time, and grid
  cards get a compact one-liner at the bottom ("Ratio ≥ 1 · Seed ≥ 10 days"),
  toggleable via Settings → Display → *Tracker rules on cards*. Seedpool
  (10 days) and InfinityHD (3 days) defs updated as the first examples.
- **Pathways picker: requirements-met markers, favourites, and "not
  interested".** The target list now shows a green **✓ reqs met** chip on
  every tracker whose listed requirements you already meet on a direct route
  (live stats vs the community data — as ever, meeting requirements never
  guarantees an invite). Filter chips at the top of the list switch between
  **All / Requirements met / ★ Favourites**. Star a target to pin it to the
  top of the list; mark one **not interested** (the eye-slash) to push it to
  the bottom — out of the way and excluded from the requirements-met filter
  (meeting a music or French tracker's bar doesn't mean you want in). Both
  lists are stored in your Yata settings, so they follow you across browsers
  and ride along in config export/import.

### Changed
- **Seed-time stats wrap instead of overflowing on cards.** Avg Seed Time and
  Total Seed Time now show the Y/M/W/D part on the main line with the h/m/s
  wrapped onto a smaller, dimmer second line — so a heavy seeder's long
  duration (e.g. "333Y 9M 3W 4D · 17h 30m 25s") stays fully visible without
  running off the edge of the stat box. Card view only; the detail page and
  table keep the single-line form.
- **Chart axis scaling reworked (Tracker Detail + History).** Flat lines with
  no target now sit centred with zero as a baseline instead of pinned to the
  top or bottom, so a steady stat reads at its real magnitude. When a target
  is on screen, the axis grounds at zero so the line's height is its true
  fraction of the target (9.8 of 15 TiB reads as two-thirds up, not flat on
  the floor), with a little headroom above the higher of value/target.
  Duration charts (avg seed time) now use whole day/month/year ticks that
  follow your duration setting and match the target label ("0 / 4M / 8M / 1Y"
  instead of "0m / 115.7d / …"), and charts fit more date labels along the
  bottom.

### Fixed
- **An infinite ratio now shows as ∞ everywhere, not a red 0.00.** Trackers
  that report ratio as "∞"/"Inf" (zero downloaded) were parsed as 0 — shown as
  a red "0.00" and counted as below-minimum. Ratio (and real ratio) now
  recognise the infinite forms across the grid, table, and Detail page:
  rendered as ∞, coloured green, sorted to the top, and no longer flagged as a
  low ratio or a portfolio "issue". (Custom-API trackers already normalised
  this; the fix covers every source.)
- **Editing targets from the Tracker Detail page now updates it live.**
  Changing a tracker's target group (or manual targets) via the Detail page's
  Targets pencil refreshes the page in place — the targets progress, the
  rules, and the mini-charts' target reference lines all update immediately,
  instead of looking unchanged until you left and re-entered the page.
- **The dashboard Targets pencil's "manual" mode no longer inherits the
  group's numbers.** Switching a tracker from a group to "— manual —" used to
  silently keep the group's requirement values as if they were your own
  targets. Manual mode now opens a small inline editor seeded from your *last
  manual targets* for that tracker (or empty if there were none) — never the
  group you're leaving — with add/remove so you can set exactly the targets
  you want without opening the full edit screen.
- **Chart y-axis scaling no longer over-zooms or invents fractional counts**
  (History + Tracker Detail). A series sitting just under its target used to
  get a scale spanning only that sliver — 14 of 15 uploads drew along the
  bottom of the chart as if barely started, with impossible ticks like
  "14.5" uploads. Whole-number metrics now get whole-number ticks (per-day
  rate mode stays fractional — 0.5 uploads/day is real), and a narrow band
  far above zero is widened so the line sits in context instead of hugging
  the floor.
- **A def's custom API block now wins the fetch dispatch regardless of the
  def's base type.** HUNO is typed `unit3d` (it IS a UNIT3D tracker) but its
  stats come from a bespoke `/api/profile` endpoint — previously the type
  alone chose the fetcher, so a unit3d-typed def with a custom `api` block
  silently ignored it and called the standard `/api/user`. The type keeps
  driving display and credential/scrape conventions; the `api` block decides
  how stats are fetched.
- **Max Scrapes Per Day now warns like Min Scrape Interval does.** Entering a
  daily cap above the tracker operator's maximum (e.g. 20 on a max-1/day
  tracker) flags the field red with the allowed maximum and blocks saving,
  instead of silently accepting a number the operator cap would override.

### Security
- **Cross-site requests can no longer change anything.** A malicious web page
  you happen to visit could previously fire blind POSTs at a reachable Yata
  (worst case: the recovery reset, wiping all data; on an instance with no
  login, any settings change). State-changing API requests that the browser
  marks as coming from another site are now rejected. Normal use, API tokens,
  and scripts/curl are unaffected.
- **The recovery reset now requires a recovery code.** The login screen's
  "reset login + wipe data" escape hatch needs the code Yata prints to its
  console and log at every start — so a reset proves access to the machine,
  not just to the port. Wrong codes count toward the login lockout.
- **Standard security headers** on all responses (`X-Content-Type-Options:
  nosniff`, `X-Frame-Options: SAMEORIGIN`, `Referrer-Policy: no-referrer`).
- **New Settings → General → Network option for reverse proxies**: "trust
  X-Forwarded-* headers" (default off). When enabled, login rate-limiting
  sees each real client address instead of lumping everyone behind the proxy
  into one lockout bucket, and the session cookie is marked Secure when the
  proxy terminates HTTPS.
- Login rate-limiter entries are now evicted once stale (minor unbounded
  memory growth under a slow trickle of failed attempts).

## [Beta-20260712]

### Added
- **History view** — a new dashboard tab graphing the months of stats Yata
  already records. Pick a metric, overlay one or many trackers in their own
  colors, choose a range from 48 h to all-time (clamped to the data you
  actually have), read exact values with a crosshair, and click to pin two
  points for an exact delta with per-day rate. Plus a Value↔Rate/day toggle,
  a Σ Portfolio line summing the selected trackers, dashed growth-rate
  projection tails, and — with a single tracker selected — the tracker's
  targets (manual or from its group, including either/or requirements) drawn
  as reference lines so distance-to-goal reads straight off the trajectory.
  The optional overlays (targets, **milestones** — dots where a stat first
  crossed a round number like 10 TiB, and a **group-change timeline** marking
  every promotion ▲ and demotion ▼) live in an Overlays menu, alongside a
  **Smoothing** toggle for noisy metrics. Select all / none, and **save the
  chart as PNG or SVG**.
- **Add Tracker search** — type to filter the tracker picker by name or
  abbreviation, so the list stays manageable as more trackers are supported.
- **Read-only API tokens + homelab endpoint** — create tokens in Settings →
  Integrations and point Homepage/Homarr/Grafana/scripts at the new
  `GET /api/summary` (totals, per-tracker one-liners, health) or
  `GET /api/history/series` (chart data). Tokens are read-only by
  construction — they only work on those endpoints, so they can never change
  anything or read tracker credentials — are stored hashed (shown once at
  creation), revocable, and show last-used in the list. Polling never
  contacts a tracker: both endpoints serve stored data. Full guide in
  [docs/API.md](docs/API.md).

### Changed
- **Group changes are now recorded** — when a tracker promotes or demotes you,
  Yata logs it, so the History timeline can mark exactly when you moved between
  ranks. (Recording starts from this release; there's no history to backfill.)
- Bulk stat refreshes are **concurrency-limited** (8 at a time) so a large
  tracker list no longer fans out into one simultaneous request per tracker.

### Fixed
- **History milestones are clearer and no longer misfire.** Each milestone now
  shows its value on the chart (e.g. "10 TiB") with a hover tooltip ("Reached
  10 TiB · Jun 3"); those tooltips work again (they were being swallowed by the
  chart's hover layer). Milestones now mark only genuine new highs, so a
  temporary dip that recovers (a data glitch, or removed-then-re-added
  torrents) no longer fires false markers on the way back up.
- **History projection always draws — and works on every metric.** A flat stat
  now projects a flat dashed tail and a shrinking one projects downward
  (previously nothing appeared unless the stat was growing, which looked
  broken). Growing stats keep using the same stable rate as the dashboard
  ETAs; everything else continues at the charted line's recent slope. The
  projection toggle is also no longer limited to growth-tracked stats — ratio,
  seeding, or avg seed time project too.

### Security

## [Beta-20260711]

### Added
- **Hawke-uno (HUNO) support** — API-only via their custom `/api/profile`
  endpoint (Bearer auth). Seed-division bracket counts (Vanguard → Legend +
  Guardian) show as HUNO-exclusive stats on cards and in the Detail view, and
  all six user groups are defined with their bracket promotion requirements.
- **`min_counts` group requirements** — defs can now express "N torrents in a
  per-tracker counter" promotion rules (e.g. HUNO's seed-time brackets), shown
  as live progress bars in Targets. Rendered straight from the def like
  `any_of`, in def order.
- **Unread mail/notification icons in the Detail view's collapsed rows**, next
  to the event beacon — same at-a-glance icons as grid cards, following the
  same Display toggles.
- Custom-API trackers that report a join date (like HUNO's `member_since`) no
  longer ask you to enter one manually; ISO datetimes are trimmed to a date and
  an infinite ratio (`"Inf"` at zero download) renders as ∞.
- **Long-range history foundation** (groundwork for the History view):
  daily history rollups are now kept ~2 years instead of 35 days (configurable
  via `history_daily_retention_days`; ~150 KB per tracker per year), and a new
  `GET /api/history/series` endpoint serves filtered per-tracker/per-field
  series with automatic fine-vs-daily granularity. The existing 48 h sparklines
  and 14-day fine history are unchanged.
- **PWA manifest** — Yata can now be installed as an app from the browser
  (mobile home screen / desktop). No offline caching: live stats stay live.
- Configurable idle **API auto-refresh interval** (Settings → Scraping; default
  30 min, floor 15) and a **qui bar refresh rate** (Settings → Integrations;
  default 10 s). A server-side min-age guard coalesces background refreshes,
  open dashboards, and page reloads into ~one API call per interval; the manual
  refresh button and Tracker Test always bypass it.
- **Runtime enforcement of tracker opt-outs** — a tracker added to
  `defs/optout.json` after it was configured now stops all API + scrape traffic
  immediately, with a clear badge in the Trackers list (previously only blocked
  at add-time).
- **UNIT3D extended-stats API support** — trackers that expose `/api/user/stats`
  (e.g. OldToonsWorld) now serve seed size, seed times, FL tokens, invites,
  warnings, real up/down/ratio, and unread flags via the API, so scraping can be
  turned off entirely for them.
- Developer helper **`dev.ps1`** — a menu to run the project tools (API probe,
  pathways sync/check, versions), bump the app version, and commit/cut releases.

### Changed
- **Add Tracker is now just the basics** — the Targets section moved out of the
  add flow (set targets from the dashboard's pencil or the edit screen), and
  the session-cookie field only appears when it's actually usable (hidden for
  API-only trackers unless their API authenticates with it).
- **Targets editor redesigned** (edit screen): selecting a target group shows a
  clean read-only chip summary of its requirements instead of greyed-out
  inputs; choosing "manual" opens a builder where you add one row per target,
  picking from every stat the tracker actually reports — including newer and
  tracker-specific stats (FL tokens, upload snatches, HUNO's seed brackets, …),
  which now render as progress bars on the dashboard too.
- UNIT3D API requests now use **Bearer auth**, keeping your API token out of the
  tracker's URL access logs, with an automatic `?api_token=` fallback for older
  instances.
- The **pathways version** now reflects the upstream *data* date rather than the
  date it was fetched, so it no longer looks newer than it is.

### Fixed
- **Gazelle trackers now show the API key and session cookie fields** in the
  add/edit forms (both were wrongly hidden — the Gazelle API needs a key +
  username, and profile scraping needs the cookie). Scrape-disabled Gazelle
  defs show only the API key, and the key hint points at Gazelle's
  Settings → Access Settings → API Keys.
- **Icons no longer render as boxes with a partial self-hosted Font Awesome
  kit.** If some `webfonts/*.woff2` files are missing (e.g. Light/Thin never
  copied), the affected styles are detected at load, their icons swap to the
  free fallback, and Settings → Display shows exactly which files to copy.
  A fully broken kit re-enables the bundled free icon set automatically.
- The login **username is now case-insensitive** (the password stays exact).
- Deleting a tracker now also removes its daily history rollups (previously
  they lingered in the database).

### Security
- The README now prominently documents that tracker API keys and session cookies
  are stored in plain text in `config.json`, with guidance for shared/seedbox
  setups.

<!--
## [Beta-YYYYMMDD] - YYYY-MM-DD
### Added / Changed / Fixed / Security
- ...
-->
