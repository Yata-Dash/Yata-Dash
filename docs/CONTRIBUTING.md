# Contributing

Bug reports, tracker definitions and code are all welcome. This is the short
guide to the repo; the rules that matter most are the security ones at the
end.

## Reporting a problem

Open an issue with the **three versions** from Settings → General (app,
definitions, pathways — *Check for updates* says whether any is stale, which
is often the cause), the tracker and whether its API / scrape / Test work
(Settings → Trackers), and a snippet from Settings → Logs. The log has
credentials stripped; never attach `config.json` or a config export.

## Adding or fixing a tracker

Trackers are data, not code: one JSON file each in `defs/trackers/`.

1. Copy `defs/templates/tracker.template.jsonc` — every field is documented
   there — to `defs/trackers/<key>.json` and strip the comments.
2. **Settings → Trackers → Reload Definitions** picks it up live. A def that
   fails to parse is skipped and reported; it never crashes the app.
3. Run `go run ./tools/defsdoc` to regenerate the tracker table in the
   README from the definitions.

Only add scraping (`scrape` block) with the tracker's permission — record who
approved it in `approved_by`. Without it, leave the def API-only. Trackers on
`defs/optout.json` have asked not to be supported and must stay there.

## Building and running

Prerequisites: [Go 1.23+](https://go.dev/dl/) and [Node.js 18+](https://nodejs.org/).

```bash
make build && ./yata        # Linux / macOS — builds the frontend, then the binary
.\build.ps1 -Run            # Windows
```

For live development, two terminals:

```bash
go run ./cmd/yata           # backend on :8420
cd web && npm run dev       # Vite on :5173, proxying /api to the backend
```

`npm run watch` in `web/` rebuilds `static/dashboard.js` on change if you'd
rather serve everything from the Go binary.

## Repo layout

```
cmd/yata/            entry point (flags, env, startup)
internal/
  api/               HTTP handlers (chi), one file per route group
  config/            config.json — atomic writes, mutex-guarded
  defs/              definition loading, validation, override-chain resolution
  fetch/             API fetchers: unit3d, gazelle variants, custom (data-driven), demo
  scrape/            HTML profile scraper + rate-limit policy
  stats/             stats engine: api + scrape + manual layers → one merged view
  store/             SQLite: stat layers, history, checks, sessions, alerts
  notify/            alert rule engine + webhook senders
  pathways/          invite-route engine (community dataset + live stats)
  netguard/          where outbound requests may go
defs/                tracker definitions (data, not code)
web/                 TypeScript frontend (Vite → static/dashboard.js)
static/, templates/  served assets and the app shell
tools/               defsdoc (README table), pathsync (routes dataset)
```

## Tests and checks

```bash
go vet ./... && go test ./...          # backend
cd web && npx tsc --noEmit && npm run build   # frontend
```

CI runs the same on every pull request. There is no frontend test runner;
behaviour that matters is covered from the Go side where it can be, and
checked in the browser where it can't.

## Branches and pull requests

```
feature branch  --PR-->  dev  --PR-->  main  --tag-->  release
```

Review happens at feature → dev, one focused change per PR, squash-merged.
dev → main is a promotion of code that has already been reviewed. Releases are
date-stamped (`Beta-YYYYMMDD`), cut from main by the release workflow, and
their notes come from `CHANGELOG.md` — so add a line under *Unreleased* with
your change. Keep it to what a user would notice.

## Rules that are not negotiable

From [SECURITY.md](SECURITY.md), repeated here because they are the ones a
well-meaning change breaks:

- **Never log a credential, a URL query string or an API response body.**
  `internal/redact` catches most of it, but don't create the sink.
- **Never commit a real credential** — not in a test, a fixture, a
  definition or a screenshot. Once pushed, it is public for ever.
- **Never send a request to a tracker the user didn't configure.** Yata
  contacts nothing on its own initiative; the opt-in update check is the one
  exception and it says so.
- **New outbound HTTP goes through `internal/netguard`**, with the narrowest
  policy that works.
- HTML interpolation uses `esc()`; inline event handlers use `jsId()`. They
  are not interchangeable.
- Auth code keeps "no account" and "couldn't tell" distinct. Never fold them
  into one boolean.
