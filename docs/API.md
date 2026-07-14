# Yata API reference

Yata exposes a small **read-only HTTP API** for integrations: homelab
dashboards ([Homepage](https://gethomepage.dev/), [Homarr](https://homarr.dev/)),
monitoring, Grafana, or your own scripts. It serves the same numbers the
dashboard shows, straight from Yata's local database.

Two things to know up front:

- **Polling Yata never contacts a tracker.** The integration endpoints serve
  stored data; Yata's own background refresh loop keeps it fresh (default every
  30 minutes). Poll as often as you like — you're only talking to your own box.
- **API tokens are read-only by construction.** A token only works on the
  endpoints documented here. It cannot add/edit/delete anything, and it can
  never read tracker credentials (API keys, session cookies) — those endpoints
  require the login session.

Base URL: `http://<your-yata-host>:8420` (default port).

---

## Authentication

Create a token in **Settings → Integrations → API Tokens**. The full token
(`yata_` + 40 hex characters) is shown **once** at creation — copy it then.
Tokens don't expire; revoke them from the same screen at any time. The list
shows each token's name, prefix, and when it was last used.

Send the token with every request, one of three ways (in order of preference):

```
Authorization: Bearer yata_e0f5…            # recommended
X-Api-Token: yata_e0f5…                     # for tools without header control over Authorization
GET /api/summary?token=yata_e0f5…           # last resort — query strings end up in logs
```

Notes:

- If **login protection is off** (no account configured), the whole app —
  including these endpoints — is open, and no token is needed. Tokens matter
  once you've set up a login; create them either way so your integrations keep
  working when you do.
- A normal browser login session also works on these endpoints.
- Unauthorized requests get `401 {"error": "unauthorized"}`.

---

## Endpoints

### `GET /api/version` — public

No auth required.

```json
{ "version": "Beta-20260711" }
```

### `GET /api/summary` — the homelab endpoint

Everything a dashboard widget needs in one call: overall totals, per-tracker
one-liners, and health.

```json
{
  "version": "Beta-20260711",
  "generated_at": 1783958400,
  "totals": {
    "trackers": 5,
    "enabled": 5,
    "ok": 4,
    "issues": 1,
    "uploaded_gib": 10543.2,
    "downloaded_gib": 2811.7,
    "buffer_gib": 7731.5,
    "ratio": 3.75
  },
  "trackers": [
    {
      "id": "71bf71f14e7e02c3",
      "name": "Hawke-uno",
      "abbr": "HUNO",
      "url": "https://hawke.uno",
      "enabled": true,
      "status": "ok",
      "username": "someuser",
      "group": "Iron Fleet",
      "uploaded": "1.00 TiB",
      "downloaded": "512.00 GiB",
      "buffer": "512.00 GiB",
      "uploaded_gib": 1024,
      "downloaded_gib": 512,
      "buffer_gib": 512,
      "ratio": 2,
      "seeding": 421,
      "bonus_points": 1500,
      "unread_mail": false,
      "unread_notifications": false,
      "updated_at": 1783958100
    }
  ]
}
```

Field notes:

| Field | Meaning |
|---|---|
| `status` | `ok` \| `error` \| `disabled` \| `opted_out` \| `unknown`. `unknown` = not refreshed since Yata started; stats are still the stored last-known values. |
| `error_kind` | Present when `status` is `error`: `auth_error`, `connection_error`, `parse_error`, … |
| `uploaded` / `downloaded` / `buffer` / `seed_size` | Display strings exactly as the tracker reports them (`"1.40 TiB"`) — template these directly. |
| `*_gib` | The same sizes as numbers, in GiB — use these for math and thresholds. Same conversion the history charts use. |
| `ratio`, `seeding`, `leeching`, `bonus_points`, `hit_and_runs`, `warnings` | Numbers. A field the tracker doesn't report is omitted. `totals.ratio` is omitted when nothing has been downloaded yet. |
| `unread_mail` / `unread_notifications` | Booleans, updated by API/scrape — at most one refresh interval stale. |
| `updated_at` | Unix seconds of the newest stored stat for that tracker; `0` = no data yet. |

### `GET /api/history/series` — chart data

Per-tracker, per-field time series — the same data behind Yata's growth
charts. Useful for Grafana (JSON API datasource) or custom graphs.

Query parameters (all optional):

| Param | Values | Default |
|---|---|---|
| `trackers` | comma-separated tracker IDs (from `/api/summary`) | all |
| `fields` | comma-separated field names (below) | all recorded |
| `range` | `48h`, `7d`, `14d`, `30d`, `90d`, `365d`, `all` | `30d` |
| `granularity` | `auto`, `fine`, `daily` | `auto` |

Recorded fields: `uploaded`, `downloaded`, `buffer`, `seed_size` (GiB) ·
`ratio` · `seeding`, `leeching`, `hit_and_runs`, `bonus_points`,
`uploads_approved` (count) · `avg_seed_time` (seconds).

Granularity `auto` picks fine points (5-minute cadence, kept 14 days) for
ranges up to 14 days and daily rollups (kept ~2 years by default) beyond.

```json
{
  "range": { "from": 1781366400, "to": 1783958400, "granularity": "daily" },
  "series": [
    {
      "tracker_id": "71bf71f14e7e02c3",
      "field": "uploaded",
      "unit": "GiB",
      "points": [[1781366400, 980.5], [1781452800, 991.2]]
    }
  ]
}
```

Points are `[unixSeconds, value]` tuples, oldest first.

---

## Errors

Errors are JSON with an `error` key and a matching HTTP status:

| Status | Body | When |
|---|---|---|
| `401` | `{"error": "unauthorized"}` | Missing/invalid/revoked token (and no login session). |
| `404` | `{"error": "not_found"}` | Unknown resource. |
| `500` | `{"error": "store_error"}` | Database problem — check Settings → Logs. |

---

## Examples

### curl

```bash
curl -H "Authorization: Bearer yata_e0f5…" http://yata.local:8420/api/summary
curl -H "Authorization: Bearer yata_e0f5…" \
  "http://yata.local:8420/api/history/series?fields=uploaded,ratio&range=90d"
```

### Homepage ([custom API widget](https://gethomepage.dev/widgets/services/customapi/))

```yaml
- Yata:
    icon: mdi-chart-line
    href: http://yata.local:8420
    widget:
      type: customapi
      url: http://yata.local:8420/api/summary
      headers:
        Authorization: Bearer yata_e0f5…
      mappings:
        - field:
            totals: uploaded_gib
          label: Uploaded
          format: number
          suffix: " GiB"
        - field:
            totals: ratio
          label: Ratio
          format: float
        - field:
            totals: ok
          label: Healthy
```

### Homarr

Use an API/custom widget pointing at `/api/summary` with the
`Authorization: Bearer …` header, or an iframe of the dashboard itself.

### Uptime Kuma (health check)

Monitor `http://yata.local:8420/api/summary?token=yata_e0f5…` as an HTTP(s)
keyword check — e.g. alert when the body contains `"issues": 1` (or use a JSON
query on `totals.issues` > 0 in tools that support it).

---

## Guarantees & scope

- Tokens are stored **hashed** (SHA-256) in Yata's database; the plaintext
  exists only in the creation response. `config.json` never contains tokens.
- Token scope is exactly: `/api/summary`, `/api/history/series` (plus the
  public `/api/version`). Everything else — trackers, settings, config
  export, scraping — rejects tokens and requires the login session.
- Revocation is immediate.
- A recovery reset (Sign in → "Reset login & erase all data") deletes all
  tokens along with everything else.

Want more endpoints exposed to tokens (or write access for a specific
integration)? Open an issue — the surface is deliberately small until real
use-cases ask for more.
