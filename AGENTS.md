# For agents

Yata: a self-hosted dashboard for private-tracker stats. A Go backend (`cmd/`, `internal/`) serves a
TypeScript frontend (`web/`) and a JSON API. Start with [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md);
the contributor workflow is in [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md).

## Build and test
- Build: `go build ./cmd/yata`, and the frontend with `npm ci && npm run build` in `web/`.
- Test: `go vet ./... && go test ./...`; `npx tsc --noEmit` in `web/`. CI runs both.
- Format: `gofmt -w` on the Go files you touched.
- Run locally against a throwaway config and database, never your own:
  `go run ./cmd/yata --port 9788 --config test/config.json --data test/yata.db` (`test/` is gitignored).

## Layout
- `internal/`: one package per concern (fetch, scrape, stats, store, defs, notify, pathways, api…).
- `defs/`: tracker definitions (`trackers/`), platform types (`types/`), templates documenting every field.
- `web/src/`: the frontend, built into `static/`.
- `docs/`: setup, security, API, contributing and architecture.

## Conventions
- One branch per change, based on `dev`; pull requests go into `dev`.
- **Tracker knowledge is data.** Everything specific to one tracker lives in its def. Never branch on a
  tracker's name or URL in Go; add a generic def field instead, and test it with a made-up def written
  in the test (see `unit3dForkRegistry` in `internal/fetch/fetch_test.go`).
- Keep `CHANGELOG.md` entries short: one precise sentence usually does.
- Test data and examples use made-up names: alice, bob, `example.com`, `192.0.2.1`.
  Never real usernames, trackers' user data, hosts, IP addresses or local paths.
- Never commit secrets, `config.json`, databases, logs or `.env`; `.gitignore` lists what stays out.
- Contact with trackers: API requests only. Profile-page scraping is governed by each def's limits and
  the operator's approval; never add or loosen scraping without it.
