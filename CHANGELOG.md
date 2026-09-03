# Changelog

All notable changes to Yata, newest first. Versions are date-based builds:
`Beta-YYYYMMDD[letter]`.


## [Unreleased]

## [Beta-20260903]

### Fixed

- **Manual-entry trackers can record a login again.** The "I've logged in"
  button and the opt-in link-click were both gated on the tracker declaring an
  inactivity policy, which got it exactly backwards: a manual tracker has no
  def and so no policy, yet those are the sites Yata never contacts, where a
  recorded login is the *only* possible source of a "last login 40 days ago".
  Both now work on any tracker that doesn't report its own login time. Without
  a policy there is still no countdown, but the elapsed days alone are enough
  to prompt a visit — which is what they're for.

- **Manual trackers no longer claim an API update that never happened.** The
  Last API Update column showed the time of the last dashboard poll, and the
  grid card and Detail header dated the numbers to the same moment — so
  hand-entered figures from weeks ago read as minutes old. A tracker Yata never
  contacts now reports no fetch time at all: the column shows "—" with an
  explanation, the Detail header drops its "updated" chip, and the card footer
  says "Entered by hand" instead of pairing a label with a meaningless clock.

  Not the manual layer's own write time either, which was the obvious fix: that
  layer is rewritten on every startup, so it means "last restart" as often as
  "last save" — a subtler version of the same lie.

- **The Scraping section is hidden when editing a manual tracker.** A greyed-out
  API-only toggle above two scrape-limit boxes implied there was traffic to
  limit on a tracker that is never contacted.

- **ReelFLiX is now fully covered — 6/6.** It added a `recent_uploads` counter,
  which is the figure its Min. Uploads rungs are measured against; mapping it
  to `monthly_uploads` closes the last gap in its ladder. The README table's
  hand-written "Rolling monthly uploads not retrievable" note is gone, as is
  Aither's equivalent — both had gone stale the same way the group notes below
  did.

- **ReelFLiX no longer claims stats it is showing you.** Its group targets
  carried a note saying seed-size and average-seedtime progress was
  unavailable in API-only mode — printed directly beneath the working progress
  bars for both. A reprobe confirmed its API now reports `seed_size` and
  `avg_seed_time`, so the notes were simply out of date and are gone.

  They were also redundant: Yata already marks a requirement it cannot track
  with an icon and a tooltip, derived from what the tracker actually returns.
  A hand-written note saying the same thing can only ever go stale, which is
  exactly what happened. ReelFLiX's one genuine gap — rolling monthly uploads
  — now shows through that mechanism instead.

## [Beta-20260902]

### Added

- **Record your own logins, so the inactivity countdown works everywhere.**
  The deadline warnings above only counted down where a tracker's API reported
  a login time — and across 22 configured trackers, exactly one does. Yata can
  now take your word for it instead.

  A **sign-in button** on the Tracker Detail page records that you logged in
  just now. That feeds the same `last_login` stat the API half writes, so the
  badge, the alert rule and the Last Login row all work identically whether the
  tracker told Yata or you did. An ✕ beside it clears a mistaken tap — without
  that, one stray click would drive the countdown until the deadline passed.

  Optionally, **opening a tracker's link from Yata can count as a login**
  (Settings → Display). Off by default and deliberately so: a click quietly
  meaning something is the part people object to, and the explicit button works
  with it off. The setting covers every way Yata sends you to a tracker — the
  URL and the Open Profile link on cards, expanded table rows and Detail.

  Yata still never observes a login and could not: every value here is either
  what a tracker reported or what you told it. That is the standing position on
  issue [#32](https://github.com/Yata-Dash/Yata-Dash/issues/32) and what makes
  this a countdown you own rather than tracking.

  **The tracker's own answer always wins.** `last_login` takes no special case
  in the merge: where a tracker reports a login time its API is the source of
  truth about its own account, stale or not, and your record fills in only
  where the API says nothing — which is nearly everywhere. On the two trackers
  that do report one, the sign-in button is hidden and the link-click setting
  does nothing, rather than appearing to work and changing nothing.

- **Account deadline warnings — inactivity pruning and API key expiry.** Two
  ways a private-tracker account fails silently: you stop logging in and the
  site prunes you, or an expiring API token lapses and the stats simply stop
  arriving. Neither announces itself, both are trivial to avoid before the
  date and impossible to undo after it.

  Yata now derives three fields wherever a tracker supplies the input —
  `days_since_login`, `login_days_remaining` and `api_key_expiry_days` — and
  shows an amber badge on the card, table row and Detail page as a deadline
  nears, red once it has passed.

  **Last Login** and **API Key Expires** are rendered as account information
  rather than stats: in the expanded row's Info column (last login beneath the
  profile link, the key expiry directly under Last API Update, since a lapsed
  key is what will silently end it) and on the Detail page's identity line.
  Both say how long is left instead of printing the raw
  `2027-01-01T00:00:00+00:00` the tracker sent, which was readable and useless
  for judging whether you needed to act. Detail also repeats the elapsed time
  under **Login Required**, where the gap so far sits against the gap allowed.

  `login_days_remaining` counts down to *each tracker's own* policy, taken
  from a new `max_login_gap_days` def rule. That is the difference between one
  alert rule and one per tracker: `days_since_login > 21` is wrong on every
  site whose policy is not 30 days, so a 90-day tracker and a 30-day tracker
  could not share a rule. Both raw numbers stay available for the trackers
  whose policy Yata does not know.

  Every one of these fields is **omitted** when its input is missing, never
  emitted as a zero — an absent field cannot match an alert condition, so the
  trackers that report no login time stay silent automatically. A
  `days_since_login` of 0 would have meant "logged in today" on all of them.

  Two alert rules are seeded — **Login required soon** (7 days out) and **API
  key expiring** (14 days) — and, unlike the original starter rules, they are
  added to existing installs too. Alerts seeding now tracks which batches have
  run rather than a single "seeded" flag, because a rule added later otherwise
  reached new installs and nobody else: exactly backwards, since long-standing
  accounts are the ones with something to lose. Deleting a seeded rule still
  sticks.

  The policy itself is shown in the **Rules** panel (Detail, expanded table row
  and the grid card's rules line) as "Login Required — every N days", whether
  or not the tracker reports a login time. On the trackers that don't report
  one, that number is the entire answer to "how often do I need to visit?".

  Def work in this release: **sixteen trackers** now declare
  `max_login_gap_days`. Stock UNIT3D disables an account after 90 days without
  a login and deletes it after 120, so most read 90 — the *first* consequence,
  being the last point a login still saves the account — while forks that
  changed it read 60. It is recorded per tracker and deliberately not
  inherited from the UNIT3D type, because an inherited default would turn
  "nobody has checked this install" into a confident claim.
  **Anthelion/Nebulance** now map `LastAccess` to `last_login` (it arrives
  without a timezone and is read as UTC — up to 14 hours out, which cannot
  move a warning measured in days). **LST** declares the API-key expiry it is
  alone in reporting.

  Known limitation, worth stating rather than discovering: some policies carry
  **exemptions this field cannot express** — a rank perk (AnimeBytes, BTN,
  Redacted, GazelleGames, Anthelion all grant inactivity immunity high enough
  up the ladder) or a condition that varies per user (OldToonsWorld exempts
  anyone seeding at least one torrent). Where the exemption is the whole
  story the def is left unset; where the number still holds for most people
  it is recorded with a `rules.note` saying so, and that note renders beneath
  the policy in the Rules panel.

- **Trackers that shut down are now retired rather than deleted.** When a site
  closes, its definition stays — trimmed to just the name and URL — and is
  marked `retired`. Yata never contacts it again, and everything already
  collected stays exactly where it is: history, charts, group timeline. The
  tracker shows a **Retired** label with the shutdown date instead of a
  permanent connection error, and it disappears from the Add Tracker picker.

  Deleting the definition, which is the obvious move, quietly does the wrong
  thing twice over: it does not stop the requests — the API kind comes from the
  tracker TYPE, which is stored against your tracker, so Yata keeps calling a
  dead host forever — and it removes the name and group ladder that the stored
  history still refers to. The user keeping a tracker for its history was the
  one being punished for it.

  This is deliberately not the opt-out list, which records a live operator
  asking not to be supported: that is a decision Yata enforces on your behalf
  and you may never override, where a shutdown is simply a fact and fine to
  reverse if a site comes back. **Aura4K** is retired under the new flag.

- **Custom-API trackers can report site events.** A def can point `event_list`
  at an array of running events — sitewide freeleech, an upload contest, a
  themed week — and they render as the same banner and countdown every other
  tracker's events use. Until now the field map handled single values only, so
  an API reporting events as a list had them silently dropped: a custom-API
  tracker could not show a freeleech banner at all, however plainly its API
  said one was running. An event that gives only a `type` slug is titled from
  it ("global_freeleech" → "Global Freeleech"), and one with no announced end
  shows without a countdown rather than not at all.

- **Aither's newly-exposed stats are mapped**, and **uploads-per-month is a
  tracked stat for the first time.** Aither now returns average seed time,
  seeding size, total seed time, approved uploads and uploads-this-month — but
  under its own field names, so none of them were reaching Yata. Seeding size
  was the visible one: it arrived as a raw byte count rather than a size,
  because the rename ran *after* the byte conversion that is keyed on canonical
  names. Renaming now happens first, which fixes the whole class for any fork
  that calls a stat something of its own.

  `monthly_uploads` had been a requirement Yata could record but never follow —
  six of Aither's classes gate on it. It is now a real stat: the target row
  tracks progress, and it counts toward the ladder-coverage figure on trackers
  that report it (still an untrackable row on those that don't).

  While mapping it: Aither's `/api/user` reports **no join date**, though every
  one of its 25 classes has a minimum age. Yata assumed the UNIT3D baseline and
  so never asked for one, leaving every age requirement permanently unmeasured.
  Aither now asks for it at setup, like MyAnonamouse does. Existing setups keep
  whatever the last scrape found — a join date never changes.

- **Aither is now fully API-capable and can be switched to API-only.** Two
  mechanisms got it the rest of the way:

  **Unread counts become flags.** Several UNIT3D forks report unread mail and
  notifications as COUNTS (`"unread_pms": 0`, `"unread_notifications": 1`)
  where Yata's fields are true/false flags, so the envelope and bell never lit.
  `bool_fields` — which custom defs have always had — now works on the UNIT3D
  path too, converting any truthy value and dropping the raw count so it
  doesn't also appear as a stat of its own. (Gazelle-family defs already went
  through the custom path, so ANT's count was being handled correctly.)

  **Site events from their own endpoint.** A def can declare an `events` block
  pointing at UNIT3D's `/api/events/global-free-leech`, which was the last
  thing on Aither that only the profile scrape could see. The event renders
  exactly as the scraped banner did, wording included. Opt-in per tracker —
  not every install has the endpoint — and best-effort, so a promotion nobody
  can read never fails a fetch that otherwise worked.

  Its end time arrives as `"09/05/2026 1:00 AM EST"`, which needed care: Go's
  time parser does not fail on a zone abbreviation it can't resolve, it invents
  a zone with a **zero** offset. Read naively that timestamp is five hours out
  and every countdown with it, silently. Named zones are now resolved to real
  offsets, and the ambiguous ones (CST is both US Central and China Standard)
  are left alone rather than guessed at.


## [Beta-20260901]

### Added

- **Manual trackers — for the sites Yata can't reach.** A new **Manual entry**
  tracker type that is never contacted: no API call, no scrape, no request of
  any kind. You type the numbers in, and they stand until you change them. For
  trackers with no usable stats API, or whose operators would rather Yata
  stayed away — TorrentLeech being the case that prompted it.

  Pick it from the type list when adding a tracker by hand, or when a Prowlarr
  or Jackett import lands something Yata has no definition for. The edit form
  then grows a **Manual Stats** section: add a row per stat, type the value,
  save. Sizes and durations are tidied into the same shapes a fetch produces
  ("12.5 tb" → "12.50 TB"), so a typed figure and a fetched one are
  indistinguishable everywhere downstream — dashboard totals, sorting, targets,
  group ladders, charts and alert rules all work exactly as they do on a
  tracker Yata polls.

  Each save is recorded as a real datapoint, so a hand-maintained tracker still
  builds history and trend lines — just at whatever pace you update it. Nothing
  claims a contact that never happened: the card footer reads **Entered**
  rather than "API", the badge says **Manual**, and the detail panel reports
  the source as entered by hand instead of a last-fetch time.

  Typed values are also available on ANY tracker as a last resort, filling only
  the stats its API and profile page both leave empty — the same slot the
  hand-entered join date has always used.
  
  ### Fixed

- **A zero the API actually reported now shows as 0, not "—".** Yata treated
  `0`, `0 B` and `0.00 B` as "no value" from every source alike. That is right
  for a scraped profile page, where a stat the page doesn't render looks
  exactly like a genuine nought — but wrong for an API that answered plainly.
  Trackers reporting no approved uploads, or nothing seeding, showed those
  stats as unknown, which reads as Yata failing over a number it was given.

  Scrapes keep the cautious reading, and a scrape that finds a real number
  still overrides a zero-ish API value — some APIs answer 0 for fields they
  never populate, and a page that can read the figure is the better evidence.

- **Pathways showed "Could not load paths" for every target when one of your
  trackers had an infinite ratio** ([#40](https://github.com/Yata-Dash/Yata-Dash/issues/40)).
  An account with uploads and nothing downloaded has a ratio Yata records as
  `Infinity` — a real, deliberate value. Go parses that string to `+Inf`, and
  JSON cannot represent `+Inf` at all, so the entire pathways response failed
  to serialise. An infinite ratio now stays infinite where it matters — it
  still satisfies any ratio requirement, and still reads as ∞ — without
  breaking the response that carries it.

  The failure was invisible, which is why it took a bug report to find. The
  response was encoded straight to the connection, so the `200` header had
  already gone out before the encoder gave up: the browser saw a successful,
  empty reply, the error was discarded, and the access log's only trace was a
  status of `0`. Responses are now built before the status is committed, so an
  unencodable value returns a real `500` and says why in the log.

## [Beta-20260830]

### Added

- **HHD (homiehelpdesk.net) is supported**, API-only. Its `/api/user` covers
  every stat its ten-rung ladder is measured against — seed size, average seed
  time, upload counts and the rest — so nothing is scraped and the def declares
  no scrape at all.

  It is also the first UNIT3D tracker to report a **join date** from the API.
  Stock UNIT3D doesn't, which is why other UNIT3D defs ask you to type one at
  setup; HHD's is read automatically. That exposed a latent bug worth naming:
  the join date arrived as a full timestamp, and the pathway engine parses
  dates strictly, so an established account would have read as brand new and
  quietly failed every account-age requirement in its ladder. Timestamps are
  now normalised for every fetcher rather than only the custom ones.

  Its API also reports a **last login time**, which Yata now records. Nothing
  displays it yet — it is there so history accumulates before the login-recency
  warnings that need it.

### Changed

- **Group and class alert conditions are now one field with three options.**
  Building "tell me when I'm promoted" meant knowing that *Group / class →
  changed* and the separate *Promoted* and *Demoted* fields were three
  different things. They are now one **Group / class** condition offering
  **changed**, **promotions** and **demotions**.

  This also closes a trap. "changed" is checked on every refresh while
  promotions and demotions fire once at the moment they happen, so a rule
  selecting both sent two notifications for the same promotion; one condition
  row can now only pick one of them. Existing rules are untouched — the stored
  form hasn't changed, so nothing needs migrating and rules written before this
  simply display the new way.

- **Pathway data refreshed** to the August 2026 community dataset (591 routes).

### Fixed

- **LST stopped reporting any stats after they changed their API.** The stats
  moved inside a `data` wrapper, and every field Yata looks for is read from
  the top level — so the request succeeded, the response parsed, and the
  tracker reported *nothing*, with no error anywhere to notice. Wrapped
  responses are now unwrapped for any UNIT3D tracker that adopts the same
  shape, so this doesn't have to be fixed again one tracker at a time.

  LST also began issuing **expiring API keys** and reports the expiry date
  alongside the stats. Yata records it, ready for the warnings that come next.
  The rest of that block — which describes the credential itself — is
  deliberately discarded at the boundary, so nothing about a key can ever end
  up in stored stats or their history.

- **A pathway requirement was cut off mid-word.** The YuScene → MidnightScene
  route read "6 months, 3TB uplo". The pathways file is generated from the
  community dataset, so correcting the file alone would have been silently
  undone by the next refresh; the correction now lives in the generator, keyed
  to that one route so it cannot affect any other. When the upstream dataset is
  fixed, the next refresh reports that the workaround is no longer needed
  instead of leaving it in place forever.

## [Beta-20260804]

### Added

- **Tracker capability indicators — "what does this tracker actually give me?"**
  A tracker can fail to show a stat for three completely different reasons:
  Yata is broken, its API doesn't expose the stat, or its operator forbids
  scraping. Only one of those is worth reporting as a bug, and until now they
  looked identical. Anthelion is the worked example — until this month its API
  covered three of the six stats its own promotion ladder is based on.

  Each tracker now carries a capability row: **how much of its own ladder can
  be tracked** ("3/6 +1"), and whether it reports unread mail, notifications
  and site events. It appears **while you're choosing a tracker to add**, in
  Settings → Trackers, and in full on the Tracker Detail page, right beneath
  the targets it explains — naming the exact stats that can't be tracked and
  noting that the requirement still applies.

  Account age never counts against a tracker: one that doesn't report a join
  date declares it as a required field at setup, so it is always trackable.

  API and scrape coverage are counted **separately** ("3/6, 4 with scraping"),
  because scraping starts disabled on unapproved trackers and needs a session
  cookie the user may not have added — a single combined figure would flatter
  most trackers and understate the approved ones.

  Capabilities are declared in each definition, so a tracker's own staff can
  state them. Two things resolve automatically rather than being written
  twice: a def that describes its own API is read directly from its field map,
  and `api.required_fields` (which already means "the API doesn't supply
  this") is subtracted. And because a hand-written declaration can quietly
  stop being true, **every successful fetch checks it** — a tracker returning
  something undeclared is reported once, naming the field and the file, and
  never changes what was stored.

- **The README's tracker table is generated** (`go run ./tools/defsdoc`).
  Platform, approval, limits and capability coverage all come from the defs,
  so the table can't drift from what the app does — it already had, listing
  Anthelion as "possibly adding API stats" months after they shipped them.
  The hand-written Notes column is preserved across regeneration; `-check`
  reports staleness without writing.

- **Connection timeline on the Tracker Detail page.** A second timeline
  beside Group timeline, listing when Yata stopped being able to reach the
  tracker and when it could again, with the reason for each outage. These are
  recorded only when the state actually changes, so an empty list is the good
  outcome — it says so rather than looking like missing data.


- **Grid view gets Order, Show and Even heights controls.**
  - **Order** sorts the cards by any of the table's sortable columns, or keeps
    **My order** — your own drag arrangement, which is preserved untouched
    while you look at something else and comes back exactly as you left it.
    Dragging is disabled while a sort is applied (a drop there could only be
    discarded or silently rewrite an order you can't see) and the handle says
    why.
  - **Show** hides trackers from the grid alone. They stay in the table, the
    detail pages, the aggregate cards and every refresh — this is
    decluttering, not disabling.
  - **Even heights** lines the cards up. Each card's Targets and More Stats
    collapse behind a per-card toggle, and the layout switches from masonry
    columns to a real grid — so cards read left-to-right in aligned rows
    instead of flowing top-to-bottom down a ragged wall, which is what made
    wide screens look messy. Both halves are needed: the grid alone would
    stretch every card to the tallest in its row. Expanding a card affects
    only its own row, and nothing is hidden that a click doesn't reach.

  All three are per-browser view state, saved alongside the custom order.

- **Rich active events on the Tracker Detail page.** Zenith's expanded UNIT3D
  user stats (now proposed upstream) return a structured list of what's running
  — global freeleech, upload contests and the like — with names, descriptions,
  links, windows and your own standing in them. Each gets its own banner:
  the tracker's own icon, a link to the event page where there is one, "Rank 2
  · Uploads 6" where the tracker reports your progress, and a live countdown.
  Events that haven't started are listed but dimmed and badged, and count down
  to their **start** rather than their end. Grid cards and table rows keep the
  compact one-line summary, derived from the same list.

### Changed

- **Anthelion and Nebulance share one tracker type, `Gazelle (ANT/NEB)`.**
  They run the same software from the same developers, on the same `api.php`
  endpoint with the same field names, and Anthelion's expanded user stats are
  expected to reach Nebulance too. The endpoint, auth and field map now live on
  the **type**, so a field arriving on both sites is one edit rather than two,
  and a third tracker from the same developers needs only a name and a URL.
  Each def keeps what genuinely differs — the path through its own settings UI
  to find an API key.

  Its key is now sent as `api_key` rather than `apikey`, as their developers
  asked. Both spellings work today, but only `api_key` works for the family.

  **The old `Gazelle (api.php)` type is retired.** We adopted it as a generic
  Gazelle base before learning that each fork invents its own query grammar;
  the three Gazelle trackers added since went to their own types, leaving it
  describing precisely the Anthelion/Nebulance family it now names properly.
  Configs storing the old key are migrated on load, so nothing needs doing.
  Going with it: the bespoke Go fetcher that existed solely for Anthelion, and
  a *second* copy of the same `api.php` call buried in the scraper — still on
  the old parameter, requiring an API key just to look up a user id, and
  re-fetching invites, join date and snatches to override scraped values. All
  three now arrive through the stats API, which already outranks the scrape
  layer, so the override was redoing what the merge does. The user id is
  discovered from the page like every other tracker's.

  Anthelion's new stats are mapped: Orbs (bonus points), Uploads, Adoptions,
  Seed Size, seeding count, unread mail, invites, user id, and Grabbed and
  Snatched alongside each other. That takes its promotion requirements from
  three of six measurable to all six, with no scraping.

  **The response also carries an IRC key, an email address and the handle of
  whoever invited you, and none of them are mapped — deliberately.** Every
  mapped field is written to the stats database and rendered on the profile
  panel, so mapping the IRC key would put a live credential on screen and into
  a file users are asked to attach to bug reports. The type def says so, and a
  test asserts it.

- **A tracker Yata has no definition for no longer claims to be UNIT3D.**
  Adding or importing a tracker that matched no definition silently typed it as
  UNIT3D — so a Prowlarr import of, say, PassThePopcorn arrived labelled as
  UNIT3D software it does not run, was probed with endpoints it does not have,
  and reported the resulting failures as though the tracker were simply broken.
  Unmatched trackers now land in a **No definition** type that fetches and
  scrapes nothing, and say so: a "needs type" badge in the trackers table, and
  a type picker in the edit panel with a **Detect** button that tries each
  candidate with your own API key and adopts the first that returns real stats.
  Detection is a button and never automatic — it means several deliberately
  failing requests to a tracker whose operator has agreed to nothing.

  The picker appears only for trackers without a definition, and can be changed
  as often as you like. A tracker *with* a definition takes its type from that
  definition, and the backend re-asserts it regardless of what a request says.

  Two more silent UNIT3D assumptions went with it: the ad-hoc "Test" before a
  tracker is saved, and the registry's own fallback for an unrecognised type.
  A fetcher kind no handler recognises is now a loud error rather than a
  UNIT3D attempt that fails in a plausible-looking way.

- **Connection state is tracked per channel.** A tracker whose API is down
  while its profile scrape still works was recording a "went down" and a "came
  back" on every single refresh — the two channels disagreed, and one shared
  state machine flipped between them forever. The API and the scrape now keep
  their own state, so that week records one event ("API unreachable: Server
  error (500)") instead of hundreds, and it says which half is broken. The
  reverse case — a good API with an expired scrape cookie — reads the same way
  from the other side. Timelines and History markers name the channel;
  events recorded before this change keep their original wording.

- **Pathways from here is its own card on the Tracker Detail page**, no longer
  sharing one with the timelines, and lists up to twelve routes before
  "+N more" now that it has the room. It's hidden entirely when a tracker has
  no routes rather than leaving an empty card in the row.

- **The detail timelines no longer run off the page.** A tracker whose API is
  failing while its profile scrape still works records a down/up pair on every
  single refresh, which piled up hundreds of rows and buried the group changes
  underneath them. The card now shows the twelve most recent changes across
  both timelines, split so neither can starve the other — ten of each gives six
  and six, not twelve and none, while two group changes beside two hundred
  connection changes gives two and ten. Each list says how many it left out,
  and the History chart still marks every one of them.

- **Connection changes on the History chart.** A new Overlays toggle draws
  outages and recoveries as ▼/▲ markers alongside the group-change ones. The
  flag stays short ("Down" / "Up") with the reason in the hover text, and
  because only state changes are recorded, a tracker that has been up all year
  adds no clutter at all.

- **Uptime as a History metric.** Chart how reliably Yata could reach a
  tracker, day by day, next to everything else you track. It behaves like the
  bounded quantity it is: the axis is pinned to 0–100% instead of being fitted
  to the data (so a steady 100% reads as steady, and a 100%-vs-98% week doesn't
  look like a collapse), and Rate/day and Projection are switched off, since
  neither means anything for a percentage. Days Yata never contacted the
  tracker leave a **gap** in the line rather than a flat stretch claiming
  uptime nobody measured. Also available as a Tracker Detail mini-chart.

- **A tracker's API can lose priority once it goes stale.** Stats were merged
  in strict source order — the tracker's own API always beat a profile scrape,
  which is right while the API is answering. When one stops (a fork breaking
  `/api/user`, a key quietly losing scope), its layer just sat there and every
  refresh kept serving values from whenever it last worked, while a scrape had
  succeeded minutes earlier. After **three days** without a successful API
  fetch, a *newer* scrape takes over until the API answers again — at which
  point it wins back immediately, with no state to unwind. Three days is well
  beyond any refresh interval, so a bad night never moves anything. The swap
  only happens when the scrape genuinely has something fresher: if it's older
  still, or absent, the API keeps the field, and a field only the API reports
  keeps coming from the API however old it is. Provenance dots show which
  source each value actually came from, so the change is visible where it
  matters.

### Security

The automated review in
[#18](https://github.com/Yata-Dash/Yata-Dash/issues/18), plus three credential
leaks found alongside it that the review didn't cover.

- **The QUI instance lookup was handing out the stored API key.** It was a
  `GET` that took its destination from a query parameter and attached the
  saved QUI key to whatever it named. Safe methods skip the cross-site check,
  and a `SameSite=Lax` cookie still travels on a top-level navigation — so any
  page a logged-in user visited could open
  `/api/qui/instances?url=http://…/` and read the key out of its own logs. No
  reply needed to come back; the attacker's server had already received it.

  The lookup is now a `POST`, which the cross-site check covers. This is the
  sharpest issue in the set and the review left it unrated.

- **Stored credentials no longer travel to an address they weren't saved
  for.** The Prowlarr, Jackett and QUI endpoints all let the settings form
  test an address before saving it, and all three fell back to the saved
  credential when the caller left it blank — so "test this unsaved URL" also
  meant "send my saved key, or Jackett admin password, to any host named".
  A credential now only goes to the origin it was stored against; testing a
  different address requires supplying its own credential, which whoever owns
  it has and a forged request does not.

- **Server-side requests are constrained centrally** (`internal/netguard`).
  Only `http` and `https` destinations are allowed, redirects are re-checked
  at every hop, and link-local addresses are refused everywhere — that range
  holds the cloud instance-metadata endpoint, which gives credentials to
  anything that can reach it.

  Checks run in the dialer, at connect time, rather than against the URL.
  Validating a hostname and connecting afterwards leaves a gap for DNS to
  answer differently the second time, and misses redirect targets entirely;
  the dialer sees the address actually being connected to, on every hop.

  Private and loopback destinations stay **allowed**, deliberately. Prowlarr,
  Jackett, QUI and a self-hosted Gotify normally run on localhost or the LAN —
  the standard advice to "reject private destinations" would break the
  ordinary deployment rather than protect it. For those four, redirects are
  additionally pinned to the configured origin, which is what stops a named
  host from answering `302` to somewhere inside the network.

- **DNS rebinding is blocked.** Binding to `127.0.0.1` never made Yata
  private: it keeps the network out, but not the browser on the same machine,
  which runs scripts from every site its user visits. Rebinding is how those
  scripts get in — an attacker's domain resolves to their server, the page
  loads, the DNS record is re-pointed at `127.0.0.1`, and the page then
  fetches `http://attacker.example:8420/…`, which the browser treats as
  same-origin and therefore lets it **read** the responses.

  Nothing already in place caught it. The cross-site check sees
  `Sec-Fetch-Site: same-origin` and allows the request, honestly, because to
  the browser that is what it is. The session cookie does not travel, so a
  configured instance answers 401 — but an instance with no account is open by
  design, and that is where first-run installs sit.

  Yata now only answers to hostnames it was told about. IP addresses and
  `localhost` always work and need no configuration, because rebinding
  fundamentally requires a *name* the attacker controls.

  **If you reach Yata by a hostname — a domain, a Tailscale MagicDNS name,
  anything behind Caddy or Traefik or an nginx that forwards `Host` — you need
  to name it once.** Three places, whichever suits how you run it:
  `YATA_ALLOWED_HOSTS` (Docker), `"allowed_hosts"` in `config.json`'s `server`
  block (Windows or a bare binary), or `--allowed-hosts`. Flag beats
  environment beats file, as everywhere else; comma-separate or list several;
  `*` disables the check.

  You will not have to guess: a refused request returns a page naming the
  exact hostname and showing all three ways to add it, and each unknown
  hostname is logged once rather than on every retry. Access by IP or
  localhost is unaffected, so most installs need nothing.

- **Exporting the config now asks for your password.** That file is the one
  thing Yata produces that is never safe to share — every tracker API key and
  session cookie, in plain text — and until now a live session was enough to
  take it. It now re-checks the account password, and the authenticator code
  too when 2FA is on, which is the bar disabling 2FA and changing the password
  already met. Failed attempts feed the same lockout as login, so the export
  can't be used as a password oracle that sidesteps the login limiter.

  It is also a `POST` rather than a `GET`. As a `GET` it was reachable by
  cross-site navigation — safe methods skip the cross-site check, and a
  `SameSite=Lax` cookie still travels on a top-level navigation — so any page
  a logged-in user visited could make their credentials download themselves.
  The attacker couldn't read the response, but a file of keys landing in
  Downloads on cue is a workable opening for "your backup is ready, send it
  over".

  The confirmation is worded for that case specifically, because a password
  prompt does nothing when the user is the one being asked: nobody legitimate
  will ever want this file, and a log — which has credentials stripped — is
  what to send when asking for help.

  An instance with no account configured is unaffected: there is no password
  to check, and the app is open by design in that state.

- **Dependencies updated, guided by reachability rather than version
  matching.** `golang.org/x/net` → 0.56.0, `golang.org/x/crypto` → 0.53.0,
  `go-chi/chi` → 5.3.0, `vite` → 6.4.3 (which brings `esbuild` 0.25 and
  `postcss` 8.5.24). `go vet`, the full Go suite and the browser build all
  pass on the new versions; `npm audit` reports zero.

  Only one module was actually reachable: **`x/net/html`**, which parses
  tracker profile pages during a scrape, and which had a denial-of-service on
  arbitrary HTML input — a tracker serving hostile markup could hang the
  scraper. `govulncheck` puts the affected count at 14 before and 9 after,
  with every remaining item in the Go standard library rather than a
  dependency.

  The much larger `x/crypto` list — a dozen of them critical — is entirely
  `ssh`, `ssh/agent`, `ssh/knownhosts` and `openpgp`. Yata uses `x/crypto` for
  `bcrypt` and nothing else, so none of it was ever callable. The same goes
  for chi's `RealIP` advisories: Yata doesn't use that middleware, and derives
  the client IP itself with proxy headers trusted only when explicitly
  enabled.

  Release builds now pin a Go **minor line** (`1.26.x`) rather than reading
  `go.mod`. That directive is a minimum language version, so building against
  it would have pinned releases to the oldest toolchain that still compiles —
  and the remaining nine findings are standard-library fixes that ship only in
  newer patch releases.

- **A password typed into the username field is no longer written to the log.**
  Found in a follow-up pass of our own rather than by the review. A failed
  login recorded the submitted username verbatim, and the commonest way an
  unrecognised one arrives is a password entered a box too high. The name is
  now echoed only when it matches the account, in which case it is a username
  by definition; there is exactly one account, so nothing diagnostic is lost.

- **Tracker API keys and Telegram bot tokens no longer reach the log file.**
  Go's `*url.Error` renders the full request URL in its message, so any
  timeout or connection error from a tracker whose definition authenticates
  with `api_key_query` carried that tracker's API key in its text — and two
  handlers logged it verbatim with `%v`. Telegram does the same with the bot
  token, which lives in the URL path. The log is explicitly meant to be
  attached to GitHub issues, so this was a key waiting to be published.

  Redaction is applied centrally, at the single point every log line passes
  through, rather than asked of each caller: the leak is invisible at the call
  site, which just sees an error value. Query strings are dropped whole rather
  than matched against known parameter names, because the name is set by each
  definition and a definition added tomorrow can choose anything.

  A failed API response no longer logs the response **body** either. It logs
  the body's *shape* — every key kept, every value replaced by its type —
  which is what diagnosing a parse failure actually needs. The values were the
  account's own details: a Gazelle user endpoint carries the email address,
  IRC key and inviter, none of which Yata stores anywhere else.

- **State on disk is now private to the account running Yata.** Config
  backups were written `0644` in a `0755` directory, and the database `0644`
  by the SQLite driver. Backups are verbatim copies of `config.json` — every
  tracker API key and session cookie — and the database holds live bearer
  session tokens and the TOTP secret. On a shared host, a seedbox especially,
  any other local account could read all of it. `config.json` itself was
  already private, so the backups had been routing around protection the main
  file already had.

  Backups, the database, its WAL/SHM sidecars and the log file are now `0600`,
  the backup directory `0700`. Files written by an earlier version are
  tightened at startup, since those are the ones with a history in them. A
  filesystem that can't express Unix modes logs a warning rather than
  refusing to start.

- **A database error no longer unlocks every protected route.** Yata is
  deliberately open before an account is set up, and the check for "is an
  account configured?" scored a failed lookup the same as "no account exists"
  — so any error reading the account table, and a lock timeout under load is
  enough, silently switched authentication off. Setup was affected the same
  way, which would have let a database error hand the account to whoever asked
  first.

  Configured, unconfigured and unreadable are now three distinct states.
  Unreadable answers `503`, not `401`: the caller's credentials aren't the
  problem, and logging in wouldn't work either, so saying "unauthorized" would
  send someone to re-enter a password that can't be checked.

- **Imported tracker IDs can no longer inject markup or script.** Tracker IDs
  are generated by the server, but a config import accepted any string, and
  the value reached five places in the dashboard unguarded — two HTML `id`
  attributes and three inline event handlers. One of those handlers applied
  HTML escaping, which does nothing there: an attribute's character references
  are decoded before the JavaScript is parsed, so `&#39;` arrives at the
  parser as a quote. A hostile config was therefore a route to script running
  in the dashboard, which can read `/api/config/export`.

  Import now enforces the ID grammar, rejecting rather than rewriting: an ID
  is the key every stat layer, history row and scrape-log entry is filed
  under, so silently fixing one would orphan that tracker's stored history
  while appearing to succeed. All five sinks were corrected independently of
  the validation.

- **Two-factor authentication.** An optional TOTP second factor, in Settings →
  General → Account, working with any authenticator app — Google, Microsoft,
  Aegis, 1Password. Enrolment shows a QR to scan and the same key in typeable
  form for anyone entering it by hand, and **nothing is switched on until a code
  generated from the secret has been verified**, so a mistyped key or a phone
  with a wrong clock can't lock you out of your own dashboard. Ten single-use
  recovery codes are issued at enrolment and shown exactly once; only their
  hashes are kept. Turning 2FA off needs the password *and* a current code —
  otherwise anyone who got past the first factor could simply remove the second.

  Codes can't be replayed: the time step a code belongs to is recorded when it
  is spent, so a code glimpsed over a shoulder or sitting in a proxy log is
  already dead. Wrong codes count toward the same lockout as wrong passwords.

  Implemented on the standard library — RFC 6238 and a small QR encoder — so
  2FA adds no dependency to a project that deliberately has four. Both were
  checked against independent implementations: the codes against the RFC's
  published test vectors and a WebCrypto implementation in the browser, and the
  QR output against jsQR across every symbol version it can emit.

- **The minimum password length is now 12, and hashes are stronger.** Eight was
  too short for anything in 2026, let alone a store of tracker API keys. Length
  is the only rule — composition requirements reliably produce short predictable
  passwords that satisfy every class and resist nothing — and a meter shows
  where you stand as you type. Passwords over bcrypt's 72-byte limit are now
  refused rather than silently truncated at 72 while appearing to be honoured
  in full. New hashes use a higher work factor, and existing ones are quietly
  upgraded at the next sign-in.

  **Existing accounts keep working.** A password below the new floor is flagged
  at sign-in and prompts a nudge in Settings; it is never forcibly reset, since
  locking someone out of their dashboard over a policy change would be worse
  than the password.

- **The log-printed recovery code is gone, along with the wipe it unlocked.**
  `POST /api/auth/reset` erased the account, trackers, stats and settings, and
  was gated on a code printed to the console *and the log file* — so anyone
  handed a log for debugging held the ability to erase the instance remotely.
  Recovery is now a 2FA recovery code, or, for an account with no second
  factor, running the binary once with `-reset-auth` on its host. That requires
  access to the machine by construction, and unlike the reset it replaces it
  removes only the login: every tracker, stat and setting survives.

- **Inline click handlers validate their arguments instead of escaping them.**
  `esc()` now escapes single quotes too, which matters for quoted attributes —
  but it cannot help an inline handler, because a browser decodes an
  attribute's character references *before* the JavaScript is parsed, so an
  escaped quote arrives at the JS parser as a real one. The values these
  handlers carry have always been server-generated IDs and canonical field
  keys; that is now enforced rather than assumed, and anything outside
  `[A-Za-z0-9_-]` makes the button inert instead of an injection point.

- **Links supplied by a tracker are checked before they become clickable.**
  Yata renders URLs it didn't author — the active-events list carries a link
  per event, Prowlarr/Jackett imports bring tracker URLs, and the pathways
  dataset has a source link. HTML-escaping makes those safe as text but says
  nothing about the *scheme*, so a hostile or compromised tracker returning
  `{"url": "javascript:…"}` would have had a script URL sitting in a
  logged-in dashboard waiting for a click. Only `http` and `https` now
  survive; anything else renders as plain text with no link. Event icon
  classes are likewise restricted to class-name characters, so a tracker
  can't borrow Yata's own styling to reshape the page.

### Fixed

- **"As of the last scrape" no longer appears on trackers that are never
  scraped.** The unread mail and notification tooltips said it unconditionally,
  across the grid cards, table rows and Detail header — wrong for every
  API-only tracker, and actively confusing for one whose operator forbids
  scraping. Each now reads from the source that supplied that particular
  value, so a flag from the API says so, one from a profile page says so, and
  a tracker whose API provides it until the API fails and the scrape takes
  over is right in both states. The three copies of the markup that let one
  wrong phrase reach four places are now one.

- **The Detail page's cards no longer leave a hole when they wrap.** They were
  laid out on a grid, so a card that wrapped to the next row started below the
  *tallest* card in the row above — on a narrow screen the timelines card sat
  alone with a stretch of empty space beside the short Stats card. Balanced
  columns tuck each card under the one above it in its own column instead.

- **A UNIT3D scrape label never fired.** `defs/types/unit3d.json` mapped
  `"Total uploads (Non-Anonymous)"` with capitals, but page text is lowercased
  before matching, so it could never match — and it mapped to `uploads`, which
  is not a canonical field (`uploads_approved` is). The scraper's own base
  vocabulary already handles that label correctly, so the entry was doing
  nothing but skewing the capability figures; removing it restores the right
  behaviour.

- **A misspelled key in a tracker definition is no longer silent.** Unknown
  fields are tolerated so a def written for a different Yata version still
  loads — but they were tolerated *silently*, so a typo like `filed_map` made
  that whole section vanish while the def loaded looking perfectly healthy.
  The tracker then collected nothing and nothing said why. Ignored keys are
  now reported at startup and in Settings → Definitions, naming the field,
  while the def still loads.

  Turning this on immediately found three live cases, all of them data that
  had been quietly discarded:
  - **UNIT3D's API key hint never reached anyone.** `defs/types/unit3d.json`
    has set `api_key_hint` since it was written, but no Go field read it, so
    every plain UNIT3D tracker showed the generic hint instead of
    "Settings → API". Type-level hints now work, with a tracker's own hint
    overriding.
  - **Six defs lost their approval notes** to `notes` where the field is
    `note` — including the informal-approval context the UI shows in its
    tooltip, which is exactly where that wording matters.
  - Def files can now carry a `notes` field of their own for whoever edits
    them next.

- **Long event banners no longer push the countdown off the edge.** In table
  rows and grid cards the whole announcement lives in one text span, and a
  regression earlier in this release stopped it shrinking, so trackers running
  several events at once lost the end date off the right-hand side. The message
  truncates again; the countdown and end time never do — a shortened headline
  still says what's running and the tracker has the detail, but a missing end
  time can't be recovered by reading harder.

- **The top bar no longer forces the whole page to scroll sideways.** Below
  ~800px the logo, badge, four view buttons, refresh time and four actions
  added up to more than the viewport, dragging every view under it off-centre.
  Nothing was removed — decoration goes first (badge, separator, "updated N
  ago"), then the view-button labels below 640px leaving titled icons, then the
  wordmark below 420px. All four views stay reachable down to 320px.

- **Grid drag-reorder now puts the card where you dropped it.** Two faults
  compounded, which is why it looked random. The destination was read from the
  list *before* the dragged card was removed, so dragging forward landed it one
  slot past the target while dragging backward worked — the same gesture
  behaving differently by direction, and a neighbour appearing to move on its
  own. And the move was applied to the full tracker list while the grid shows
  only enabled ones, so any disabled tracker lying between source and target
  displaced the card by however many hidden entries were in the way. Dropping
  onto the last card also used to land you second-to-last, forever; direction
  now decides whether the card lands before or after the target, so every
  position is reachable.

- **A tracker def can now require a join date on its own.** Whether the setup
  form demands one was decided entirely by the tracker TYPE, so a def's
  `api.required_fields` parsed into nothing and was silently ignored — Aura4K
  had asked for a join date since July and no user was ever prompted. It now
  unions with the type's list (and still drops anything the def's own
  `field_map` provides, so declaring a mapped field stays a no-op). Zenith
  needs this: it's API-only now, and its API is the one thing that doesn't
  report a join date, so without the prompt account-age tracking silently
  never works.

- **The join-date hint no longer sticks.** Selecting a tracker that requires
  one and then switching to a tracker that doesn't left "This tracker doesn't
  report a join date" standing over a tracker that does. The label and the
  asterisk already reverted; only the hint had no path back.

- **Seed size no longer displays as a raw byte count.** The expanded UNIT3D
  stats send sizes as integer bytes from `/api/user`; only three core fields
  were being converted there, because everything else used to arrive via a
  supplementary endpoint that did its own conversion. A seed size showed as
  `3005578784855` instead of `2.73 TiB`. `seed_size`, `real_uploaded` and
  `real_downloaded` now convert alongside the core three — safely, since only
  JSON numbers are converted and forks that pre-format them as strings pass
  through untouched.

- **Active events no longer render as `[object Object]`.** The new structured
  list had no handler, so it fell through to the generic stat row and was
  stringified. It now drives the existing event banner and countdown
  everywhere, and renders in full on the Detail page.

- **Absolute dates read year-first everywhere the connection surfaces show
  one.** The freshness tooltips were rendering through the browser's locale,
  which for most people means `7/22/2026` — the least widely used ordering, and
  ambiguous against `22/7/2026` for everyone else. Tooltips, timeline rows and
  chart-marker hovers now use `2026-07-22` (with `2026-07-22 15:55` where a
  time is needed), matching the dates the table cells already showed. The
  dashboard's date formatter also builds those from local time rather than UTC:
  it decides "is this today" locally, so answering in UTC meant an evening east
  of Greenwich could stop counting as today and jump straight to tomorrow's
  date.

- **"Last API Update" now shows the last time the API actually returned
  data**, not the last time Yata tried. On a tracker whose API had been failing
  for a week while its profile scrape carried the stats, the column read "5
  minutes ago" — describing the attempt, not the numbers. The attempted time
  moves to the hover text, the value turns amber while the API is currently
  failing, and sorting by the column now genuinely brings the stalest trackers
  to the top instead of ordering every tracker by the same polling cycle. A
  tracker whose API has never worked reads "—" and says so on hover.


## [Beta-20260725]

### Added

- **Sortable tracker settings table.** Settings → Trackers can now be sorted
  by any column — clicking a header cycles ascending, descending, then back
  to the order you added trackers in, so nothing is lost. Test Status sorts
  by severity rather than alphabetically, putting failures and unconfigured
  trackers at the top where you want them, and Def groups manually-added
  trackers together at the end instead of filing them under "m".
  - **Nine new trackers** (contributed by [@gizzlepox](https://github.com/gizzlepox)):
  AnimeBytes, Blutopia, BroadcastTheNet, GazelleGames, Nebulance, Orpheus,
  Redacted, ReelFliX and Upload.cx. All are API-only for now — none has been
  approved by its staff yet, so Yata warns about their definitions until
  approval comes through.
  - **Combined upload + download requirements.** Trackers that promote on total
  traffic rather than upload alone can now express that, and it shows up in
  targets and Pathways like any other requirement.

### Changed

- **Tracker types now read as names, not codes.** The Type column showed the
  internal key (`gazelle_json`); it now shows the same label the Add Tracker
  list uses, with the key kept in the tooltip.

- **Clearer names for the Gazelle types.** "Gazelle JSON API" and "Gazelle"
  are now **Gazelle (ajax.php)** and **Gazelle (api.php)**, named for the
  endpoint each one actually talks to. The old names suggested one was a
  modern replacement for the other, which had it backwards: `ajax.php` is
  the standard endpoint shipped by Gazelle itself and present on virtually
  every fork, while an `api.php` is a site-specific addition that each fork
  invented independently. So when adding a new Gazelle tracker, reach for
  **Gazelle (ajax.php)** first. GazelleGames' own `api.php` — unrelated to
  Anthelion's beyond the filename — is now just **GazelleGames**, and Unit3D
  is now **UNIT3D**, matching that project's own capitalisation. Existing
  trackers are unaffected: only the display names changed.

- **Bonus points are rounded down and abbreviated.** The table and grid card
  showed whatever the tracker reported — "98432.50" — even though no tracker
  lets you spend part of a point. They now floor, never round to nearest, so
  49,999.63 reads "49,999" rather than claiming a 50,000-point requirement is
  met. Past ten million the figure abbreviates (219,664,390 → "219M",
  1,234,567,890 → "1.23B"); below that the exact count is kept, since most
  balances live in that range and it's where you're saving toward a specific
  purchase — "9,999,999" still fits the narrowest card comfortably. Abbreviation truncates too —
  the number shown is never more than you hold — and the exact figure, decimals
  included, is on hover. History, Tracker Detail and the expanded row are
  unchanged and still show the full value.

- **Six new table columns.** **Last API Update** and **Last Scrape** show when
  each tracker was last contacted (compact time, full timestamp on hover;
  trackers Yata never scrapes read "API only" rather than a bare dash), and
  sorting them ascending brings the stalest to the top. **Total Seed Time**,
  **Real Uploaded**, **Real Downloaded** and **Forum Posts** cover fields
  several trackers already report but the table had no home for. All six are
  off by default — enable them in Customise Columns.

- **The dashboard uses the whole window.** Grid and table were capped at
  1800px, so on an ultrawide roughly half the screen was empty margin while
  the top bar ran edge to edge. The cap is gone: the grid packs in as many
  card columns as fit (eight on a 3440px display), and the table finally shows
  every column without sideways scrolling. Settings, the expanded table row
  and the Tracker Detail info columns stay capped — they're forms and
  label→value lists, which only get harder to read stretched — while the
  charts, grid and table take the full width. The aggregate cards' sparklines
  now grow taller as their cards widen, so a week of data isn't drawn as a
  flat line across half a metre.

- **Customise Columns drops the "Extended" label.** Columns were dotted and
  described as populating "from profile scrapes or extra API fields", a split
  that stopped being true once trackers began exposing those same fields over
  their APIs — and which said nothing useful anyway, since what any given
  tracker reports varies enormously. The dot is gone, replaced by a plain
  note that not all fields are available or applicable for every tracker.

### Fixed

- **The API token "Copy" button now actually copies.** Over plain `http://`
  (any LAN address, so most self-hosted setups) browsers withhold the modern
  clipboard API, and the button quietly fell back to selecting the token and
  telling you to press Ctrl+C. It now copies for real on those origins too,
  and confirms with "Copied to clipboard". The Ctrl+C message survives only
  for browsers that refuse both routes.


## [Beta-20260723]

### Added

- **Connection health.** The dashboard's Tracker Health card is now
  **Connection Health**: how many trackers Yata could actually reach, rather
  than how many had a ratio above 1. Every contact attempt — API fetch or
  profile scrape — is recorded, so the card's number, its sparkline and a new
  **Connection (7d)** strip in each table row's expanded view all read from
  real history instead of standing in for it (the old sparkline plotted the
  overall upload/download ratio, which had nothing to do with health). The
  strip shows one block per day: green for a clean day, amber when some
  contacts failed, red when none got through, and a faint outline for days
  with no contact at all — a paused or newly-added tracker never reads as an
  outage.

  The two ways Yata reaches a tracker are judged separately. A tracker whose
  API has been failing for days but whose profile scrape still works is not
  dark, yet its stats are running on the fallback — so it now reports
  "API failing … using scrape fallback" in amber rather than counting as
  healthy just because something got through. Red is reserved for trackers
  nothing reaches at all, so it stays meaningful. Expired session cookies
  count the same way, as the scrape half being broken.

  Trackers with no API key configured are left out entirely: a scrape-only
  tracker was never going to answer an API call, so counting that as an
  outage would hold the card red forever. Ratio and hit-and-run problems
  remain visible per tracker and through alert rules. Recording starts with
  this build, so the strip fills in over the first week.

### Changed

- **Expanded table rows: 48-hour upload/download charts replaced.** Those two
  sparklines duplicated the History view and the Tracker Detail page, which
  both chart the same numbers over longer ranges with axes and tooltips. Their
  space now holds the connection strip, which shows something neither view
  does. Removing them also evens out the row's column heights, closing the
  large blank area that used to sit under the Stats column, and the expanded
  row now stacks into a single column on narrow screens and phones instead of
  leaving one short column beside a tall one.

### Fixed

- **UNIT3D trackers reporting raw byte counts showed nonsense sizes.** A stock
  UNIT3D install returns uploaded, downloaded and buffer from its API as plain
  byte numbers, while several forks pre-format them as "620.01 GiB" text. Only
  the text form was ever handled, so any tracker sending numbers had them read
  as though they were already gigabytes. Byte counts are now converted, and
  pre-formatted values still pass through untouched.

- **Expanded table rows could collapse to a narrow strip.** The row's width is
  driven by a CSS variable measured from the table, which was being written as
  `0` when the measurement ran while the table view was still hidden — as it is
  at startup. Opening a row before anything else re-measured left its contents
  crushed into a sliver. The zero measurement is now ignored.

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

- **Pathways: optionally include disabled trackers.** A new opt-in toggle
  (Settings → Display → Pathways → "Include disabled trackers") lets trackers
  you've disabled still show the invite routes leading out of them — for
  trackers imported from Prowlarr/Jackett that Yata has no definition for
  yet, kept around as a record that you're a member. Their paths are badged
  **Disabled**, sort below every enabled tracker's path, and never show a
  time estimate: a disabled tracker's stats are frozen, so they're treated as
  unknown rather than risking a stale "Ready now" (this holds even on routes
  listing no requirements at all). While the toggle is on they also count as
  trackers you already have, so the target picker and weekly digest stop
  suggesting them. Related fix: trackers with **no definition** are now
  matched to the pathways dataset by their name or website address, so they
  appear at all — previously only trackers with a Yata definition could.

- **Spurious logouts.** Yata logins were never actually expiring (sessions
  last 30 days and survive restarts), but the login screen re-appeared
  whenever a tracker or integration returned an auth error: a profile scrape
  hitting an expired *tracker* cookie answered the browser with 401, and the
  app read any 401 as "session expired". Upstream 401/403s (tracker scrapes,
  Prowlarr/Jackett/qui credential checks) are now relayed as 502 with the
  real cause in the body, and the app only shows the login screen for its
  own session check. One login per browser per 30 days, as designed.


## [Beta-20260722]

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
