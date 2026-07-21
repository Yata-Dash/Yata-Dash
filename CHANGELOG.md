# Changelog

All notable changes to Yata, newest first. Versions are date-based builds:
`Beta-YYYYMMDD[letter]`.


## [Unreleased]

### Added
- **Qui seed-size fallback.** Qui's torrents endpoint reports, per announce
  host, the total size each tracker's torrents are currently seeding. A new
  three-way setting (Settings → Integrations → "Seed Size Fallback") feeds
  that into the seed_size stat as a fourth merge layer: **off** (default),
  **only fill in missing data** (used when neither the tracker's API nor a
  scrape has a value — API-only trackers without a seed-size endpoint, or
  scrapes returning zero/nothing), or **prefer qui over scrapes**. In every
  mode the tracker's own API wins — qui's figure is a client-side calculation
  over the instances it can see, so multi-client and seedbox setups
  undercount, and the tracker-reported number is the truth for progressions.
  Announce hosts map to trackers by domain — including the def's alias
  domains, so RetroFlix configured as retroflix.net still matches its
  peer.retroflix.club announce host (subdomains match; a tracker's mirror
  announce domains aren't double-counted; unrelated/public hosts never
  match). Values sum across enabled qui instances, and a tracker qui stops
  reporting is cleared rather than left stale. The per-stat source dot shows
  a pink "qui" origin, and the layers refresh with the background cycle
  (plus immediately on enabling).
- **Unregistered count in the qui bar.** Qui now reports how many torrents
  the tracker no longer recognises; the bar shows it in red next to Error.
  Hidden on qui versions that predate the counter.

- **Expired session-cookie warnings + scrape health.** Scrape attempts now
  record their outcome, so a dead tracker cookie is noticed before the data
  gap hurts: an amber dismissible banner names the trackers whose session
  cookies have expired ("re-copy them in Settings → Trackers"), grid-card
  footers show a "Cookie expired" badge, table expanded rows gain a Scrape
  Health line (failure streak + cause), and an optional extended "Scrape"
  column (hidden by default, column customizer) shows ✓ / ✗ streaks across
  all trackers at a glance. Explicit login signals (session_expired,
  user_id_not_found) flag immediately; an empty scrape only counts as a
  cookie problem after two in a row, so a one-off anti-bot or maintenance
  page doesn't cry wolf. Everything clears itself on the next successful
  scrape.

### Fixed
- **Spurious logouts.** Yata logins were never actually expiring (sessions
  last 30 days and survive restarts), but the login screen re-appeared
  whenever a tracker or integration returned an auth error: a profile scrape
  hitting an expired *tracker* cookie answered the browser with 401, and the
  app read any 401 as "session expired". Upstream 401/403s (tracker scrapes,
  Prowlarr/Jackett/qui credential checks) are now relayed as 502 with the
  real cause in the body, and the app only shows the login screen for its
  own session check. One login per browser per 30 days, as designed.

## [Beta-20260721]

### Added

- **"Don't warn me again" for the login-protection banner.** The dashboard
  banner shown when no login is configured now offers a persistent opt-out
  alongside the session-only ×, for users who deliberately run without login
  (trusted LAN, password-protected reverse proxy, etc.). It stays a warning
  by default — enabling login is still the safer posture — but the choice to
  hide it is remembered server-side and is reversible from Settings →
  General → Account ("Warn me on the dashboard while login protection is
  off"). This replaces the earlier ask for a way to disable session
  expiration: rather than weakening the auth model, it just quiets the
  reminder for those who've decided they don't need it.
- **Weekly digest.** A scheduled webhook summary — Settings → Alerts gets a
  "Weekly digest" card with its own enable toggle, weekday + hour picker
  (server-local, default Monday 09:00), and a destination multi-select
  (empty = all enabled, same searchable picker the rules use). Each digest
  covers the trailing 7 days: per-tracker deltas (uploaded/downloaded/buffer,
  ratio old→new — a tracker with no movement just says "no change"), how many
  targets are currently met, a goal-pacing verdict for any dated target
  (behind/overdue/on track), this week's group promotions and demotions, and
  any pathway target that's newly gone requirements-met since the last
  digest. A week with nothing to report still sends a short "all quiet"
  heartbeat rather than staying silent, so silence never gets confused with
  "the digest broke". If Yata was offline at the scheduled moment, it catches
  up and sends on the next check after boot instead of skipping the week
  entirely. "Preview" builds the text against live stats without sending
  anything (inline, like a rule's dry run); "Send now" delivers immediately,
  independent of the schedule. Long digests split across multiple messages on
  line boundaries to respect Discord's 2000-character limit.
- **Goal pacing: "reach it by" deadlines on targets.** Any target row —
  manually set or loaded from a group — can now carry an optional deadline
  date. Yata compares the rate you NEED (what's left, divided by the days
  remaining) against the rate you HAVE (your existing growth rate) and shows
  an on-track/behind verdict, both as a compact chip on grid cards and the
  table's expanded targets, and as a full "needs 8.2 GiB/day · doing 11.4
  GiB/day · on track" line on the Tracker Detail page — each behind its own
  Display toggle (*Goal pacing on Detail*, *Goal chips on cards/table*, both
  on by default). A deadline that's passed with the row still unmet reads as
  overdue; a flat stat with time left reads as "needs X/day" with no verdict,
  since no rate is neither proven on-track nor proven behind. Setting a date
  stays out of the way: target rows carry only a small calendar icon button
  (accented once a goal is set, tooltip naming the date) that pops a compact
  date editor on demand, so the target value itself stays readable — and a
  GROUP target set carries ONE optional "whole group" goal date (in both the
  dashboard popover and the edit screen) that applies to every requirement
  in the set, rather than asking for a date per stat. Ratio targets
  get their own honest treatment — there's no meaningful "ratio rate" to pace
  against, so a dated ratio row instead shows the extra upload needed to hit
  it (e.g. "1.56 / 2.00 — needs +64 GiB upload") and never participates in
  the behind-pace alert. Account age can't take a deadline at all — reaching
  an age by a date isn't something you control. Setting a date for the first
  time defaults to today plus whichever is longer: 30 days, or the time left
  on an unmet account-age target (the common goal is beating your age
  requirement before it completes on its own). A new alert condition,
  *Behind goal pace*, fires when any dated target is behind or overdue.
- **Standing guards: predictive decline alerts.** Four new polled conditions
  in the rule builder project a stat's current trend forward instead of just
  comparing its live value: *Ratio hits tracker min within (days)* and
  *Buffer runs out within (days)* answer "at this rate, when do I cross the
  line" (e.g. "your ratio will cross LST's minimum in ~9 days at this rate"),
  while *Seed size drop over 7d (%)* and *Seeding count drop over 7d (%)*
  catch a sudden mass deletion or a client going quiet before it shows up as
  a ratio problem. All four are silent (never match) until there's enough
  history to trust — a flat or rising stat, or a tracker too young to have a
  week of data, reads as "not declining"/"not dropping" rather than a false
  positive. A new starter rule, *Ratio approaching minimum* (fires within 14
  days, a day's cooldown to damp rate noise), is added to the two existing
  seeded rules for fresh installs.
- **Event notifications: promoted, demoted, and target-met alerts.** The
  rule builder gains three one-shot conditions alongside the existing
  polled fields — *Promoted*, *Demoted*, and *Target met* — that fire the
  moment a tracker's group moves up or down its def's ladder, or one of its
  target rows (base targets, a group's min-counts brackets, or an any-of
  alternative) crosses from unmet into met. Unlike the rest of the rule
  builder these aren't polled on a schedule; they fire at the instant the
  transition is detected, so they can be combined with a normal numeric
  condition on the same rule ("promoted AND ratio < 1") without waiting for
  the next refresh cycle. The target-met message reports progress as
  `m/T` — the count of currently-met target rows out of the total, e.g.
  "Seedpool — Met target 3/5 — Ratio" — with 5/5 being how an all-met
  account reads (there's no separate all-met condition). Fresh installs
  that have never touched the Alerts tab get two starter rules out of the
  box (*Promotions & demotions*, *Target met*) so the feature isn't
  invisible until someone discovers Settings → Alerts; anyone who already
  had a destination or rule configured keeps their setup untouched.
- **SpeedApp support (API-only).** New `speedapp` definition using the
  site's Bearer-token `/api/me` endpoint: transfer totals, buffer, snatch
  count, hit & runs, average seed time, invites, FL/double-upload tokens,
  need-seed count, and join date. SpeedApp is API-only by operator policy,
  so scraping is disabled in the def. Includes the full class ladder —
  Peasant through Legend User with age/upload/ratio promotion requirements.
  - **`ratio_from_bytes` custom-API option.** Custom tracker definitions can
  now derive ratio from the raw uploaded/downloaded byte counts when the API
  doesn't return a ratio field (SpeedApp is the first such tracker). A ratio
  mapped directly from the API still wins; nothing downloaded yet renders as
  ∞, and a 0/0 account shows no ratio rather than a misleading 0.
  
### Changed
- **Top aggregate cards reworked onto the 7-day history series feed.** The 6
  headline cards (grid + table views) and the table's expanded-row
  sparklines now read `/api/history/series` (7-day window) instead of the
  retired `/api/history` list endpoint, and each card except Tracker Health
  gets a small signed "+X · 7d" change chip next to its value. The Overall
  Ratio card's chip and sparkline use the pooled ratio (total up ÷ total
  down) — the same quantity its big number shows — rather than an average of
  each tracker's individual ratio, so the change reads consistently with the
  value instead of a figure that could look unrelated to it. The Tracker
  Health card, which has no chip, reserves the same blank line so its
  sparkline stays aligned with the others. The legacy `GET /api/history`
  endpoint has been removed.
- **Scrape-limit fields untangled (edit tracker + Settings → Scraping).**
  Users read the red "This tracker operator requests ≥ N min between
  scrapes" banner as an error blocking their save — it never blocked
  anything, it's information. It's now an amber info notice (ⓘ) and says so
  outright: "Applied automatically — your values below can only add further
  limits." Red is reserved for the actual blocking validation messages
  under the fields. The fields themselves lost their double hints: units
  now sit beside the input ("min", "per UTC day") and each field keeps ONE
  helper line, reworded to say what 0 really does — the per-tracker
  interval follows the global setting or the tracker's limit *whichever is
  longer*, while the per-tracker cap follows the global cap or the
  tracker's limit *whichever is lower* (an interval is a floor, a cap is a
  ceiling — the previous wording implied they merged the same way). The
  global Scraping page gets the same treatment, plus its interval field no
  longer fights the keyboard: it used to clamp to 60 on every keystroke, so
  typing "120" snapped to 60 at the "1" — it now shows a soft red state
  while a value is under the minimum and only clamps when you leave the
  field.

### Fixed
- **History-driven charts no longer flat-line when a tracker's ratio is
  infinite.** A tracker with downloaded=0 reports its ratio as "∞"; recording
  that wrote a literal +Inf into the history table, and a later
  `/api/history` read failed to JSON-encode it — the resulting `http 0`
  silently emptied every top aggregate card and table sparkline for the whole
  install, not just that one tracker. Non-finite values are no longer
  recorded, and any already-stored +Inf/NaN rows are skipped on read instead
  of breaking the response.
- **Goal-date picker now has a clear way to close.** Setting a target's goal
  date left the little date pop open with no obvious "done" — you had to
  click the calendar icon again. It now has a ✓ button, and Enter or Escape
  close it too (Enter no longer leaks through to submit the surrounding
  editor). The value is still applied when you save the target, same as
  before.

## [Beta-20260717]

### Added

- **Highlight hit & runs toggle (Settings → Display).** H&R counts colour red
  by default across cards, the Detail table, and expanded stat rows. Some
  trackers' H&Rs are permanent (never clear once recorded), so the red reads
  as a false ongoing alarm — the new *Highlight hit & runs* toggle switches a
  nonzero count to a neutral colour instead (zero still shows green either
  way).
- **`min_monthly_uploads` groundwork for uploader-class requirements
  (RocketHD/Aither-style).** Group definitions can now record a required
  uploads-per-rolling-month figure. There's no live stat to track it against
  yet, so it maps to a `monthly_uploads` target that renders through the
  existing untrackable-target mechanism (eye-off icon, "Not available", the
  required value) rather than silently disappearing or showing false
  progress — the manual target builder also lists it. Pathway/promotion ETA
  evaluation ignores the field entirely, the same way it already ignores
  `min_counts`.

- **Per-stat change over the selected range on the Tracker Detail page.**
  Every value in the Stats section now carries a small muted delta chip
  showing how much it moved across the selected range chip — e.g.
  "(+2.30 TiB /30d) 4.25 TiB", sitting BEFORE the value so the coloured
  values stay flush on the right — switching 7d/30d/90d/1y/All updates the
  window live. A **Changes** toggle next to Projection turns the chips on or
  off (remembered, on by default). Reuses the series data already fetched
  for the mini-charts (no extra network calls); stats with no recorded
  history or a zero delta simply show no chip.

### Changed
- **One "API only" label for every no-scraping tracker.** Cards and the
  Detail table used to show three different footers — "API only mode",
  "Scrape disabled", "No scrape support" — depending on *why* scraping is off
  (your per-tracker toggle, an operator's def-level request, or a type that
  can't scrape). To the reader they all mean the same thing: stats come from
  the API alone. All three now display **API only**; the precise cause still
  shows in the edit modal's hint and the connectivity-test detail. Also fixed
  the quirk that made the labels inconsistent between identical trackers in
  the first place: saving any edit to a tracker whose def forbids scraping
  silently persisted the display-locked "API only" toggle as a real user
  setting — which would also have kept scraping off if the tracker's def ever
  re-allowed it. The locked toggle is display-only now.
- **Untrackable target requirements now stay visible.** When a target's stat
  isn't reported by a tracker's API (e.g. an API-only tracker whose profile
  omits seed time or seed size), the requirement no longer silently vanishes
  from the TARGETS section — it shows with an eye-off icon, an italic *Not
  available* label, the required value, and a dashed placeholder bar, plus a
  tooltip explaining the stat can't be tracked but the requirement still
  applies. A stat is treated as untrackable only when the tracker has been
  fetched and returned other fields but not this one, so a not-yet-polled or
  failed tracker doesn't show a wall of false "not tracked" rows. In the
  promotion-ETA / "Eligible now" maths an unavailable requirement is assumed
  to be ZERO — many trackers simply omit zero-valued stats — so it counts as
  unmet: "Eligible now" never shows while any requirement can't be verified,
  and the ETA headline gets a "+" (or stays hidden) instead of a false
  all-clear. Applies everywhere targets render — grid cards, the Detail page,
  and the Detail table.

### Fixed
- **A scrape that hits a login page now says so instead of silently finding
  nothing.** When a session cookie expires, most trackers don't return an
  auth error — they redirect the profile URL to their login page, which
  arrives as a clean 200. The scraper extracted zero stats from it and
  reported "ok — 0 fields", leaving stats quietly frozen with no visible
  problem. The scraper now recognises the login-page redirect and reports
  **session cookie expired**; and any 200 page that yields zero recognisable
  stats (anti-bot interstitial, maintenance page) is reported as an error
  rather than a successful empty scrape. Found via DarkPeers, whose saved
  profile page extracts 21 fields — the def was fine; the cookie wasn't.
- **Disabled trackers are hidden from the dashboard.** Disabling a tracker
  already stopped its refreshes, but its card and table row (with stats going
  stale) stayed on the dashboard. Disabled trackers now disappear from the
  grid, the Detail table (the "N / M active" line follows), and the aggregate
  totals/health cards — they stay listed in Settings → Trackers, which is
  where you re-enable them. If every tracker is disabled the dashboard says
  so instead of showing the first-run welcome screen.
- **The daily-scrape-limit notice is a warning now, not an error.** "N
  trackers have hit the daily maximum scrapes" showed in red — but red means
  something is broken, and this is expected behaviour (the cap is often the
  tracker operator's, and there's nothing to fix). It's now amber, and
  dismissible for the session with an × — same treatment as the
  login-protection nudge.
- **Several actions in Settings → Trackers left the UI stale until the next
  full refresh.** Importing from Prowlarr/Jackett, saving a tracker's
  cookie/key, and toggling or deleting a tracker all updated the backend
  immediately but left the "profile scraping off" badges (grid cards, table
  rows) showing the old scrape-status until the next 5-minute cycle. Those
  actions — plus Reload Definitions — now also miss the fresh state they
  produce: a reload only cleared the internal defs cache, leaving the
  settings table's approval badges, the import picker's opt-out list, and
  group data all showing pre-reload values. Reload Definitions is now a full
  refresh (re-fetches trackers, the opt-out cache, and group defs, same as
  boot); the other actions all re-fetch scrape status and re-render. An open
  Tracker Detail page is included in this: it now redraws after a stats
  refresh, a profile scrape, or a toggle, and closes itself (instead of
  showing "Tracker not found") if you delete the tracker it's showing.
- **The daily update-check and reverse-proxy-trust toggles no longer lie
  about being saved.** Both persisted silently and never checked whether the
  save actually succeeded — a failed request left the checkbox showing the
  new state while the backend kept the old one. They now revert the
  checkbox and the setting, and show an error toast, on failure — matching
  the topbar privacy-eye toggle's existing behaviour.
- **The Test button tested the wrong thing, or nothing at all.** In the edit
  panel, Test always hit the tracker with whatever was last *saved* — pasting
  a new cookie or key and testing before Save silently tested the OLD
  credentials, telling you nothing about the change you were about to make.
  It also had no way to distinguish "I tested my saved config" from "I
  tested an edit I then cancelled": either way the result landed in the
  trackers table's status pill, so a test→cancel could leave a misleading
  pill behind for a tracker whose real saved credentials were never tried.
  Test now runs against the values currently in the form; the table pill
  only updates when those values match what's actually saved (test→save
  shows it, test→cancel doesn't — a pending result is promoted or discarded
  based on what you actually save). Add mode had no Test button at all — you
  couldn't check a key/cookie until after adding the tracker. Test is now
  available there too, running an ad-hoc check against a synthetic,
  never-persisted tracker built from the form (its own throwaway ID keeps it
  fully isolated from any real tracker's rate limits and scrape history).
- **Escape now closes the History view's Overlays/Save menus and the Alerts
  tab's tracker/destination multi-selects**, matching the Tracker Detail
  page's Charts menu.

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
