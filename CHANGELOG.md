# Changelog

All notable changes to Yata, newest first. Versions are date-based builds:
`Beta-YYYYMMDD[letter]`.

How this is used: jot changes under **Unreleased** as you work. When you cut a
release (dev.ps1 → *Cut a release*), move the Unreleased notes under a new
version heading — those notes become the GitHub Release body automatically.

## [Unreleased]

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
- **Long-range history foundation** (groundwork for the upcoming History view):
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
