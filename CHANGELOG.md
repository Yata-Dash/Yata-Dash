# Changelog

All notable changes to Yata, newest first. Versions are date-based builds:
`Beta-YYYYMMDD[letter]`.

How this is used: jot changes under **Unreleased** as you work. When you cut a
release (dev.ps1 → *Cut a release*), move the Unreleased notes under a new
version heading — those notes become the GitHub Release body automatically.

## [Unreleased]

### Added
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
- UNIT3D API requests now use **Bearer auth**, keeping your API token out of the
  tracker's URL access logs, with an automatic `?api_token=` fallback for older
  instances.
- The **pathways version** now reflects the upstream *data* date rather than the
  date it was fetched, so it no longer looks newer than it is.

### Fixed
- The login **username is now case-insensitive** (the password stays exact).

### Security
- The README now prominently documents that tracker API keys and session cookies
  are stored in plain text in `config.json`, with guidance for shared/seedbox
  setups.

<!--
## [Beta-YYYYMMDD] - YYYY-MM-DD
### Added / Changed / Fixed / Security
- ...
-->
