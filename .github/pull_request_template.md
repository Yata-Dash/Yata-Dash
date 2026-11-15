<!-- Base branch: dev. GitHub defaults to main; please change it before opening. -->

## What and why

## Checklist
- [ ] Based on `dev`, and this PR targets `dev`
- [ ] `go vet ./... && go test ./...` pass; `npm run build` in `web/` if the frontend changed
- [ ] A line under *Unreleased* in `CHANGELOG.md`
- [ ] Tracker definitions: API-only unless the tracker approved scraping (`approved_by`); nothing on `defs/optout.json`
