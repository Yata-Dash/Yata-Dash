# Setup guide

Everything past the README's Quick start: running Yata each way, keeping it
updated, reaching it by a hostname, login protection, proxies and backups.

- [Docker](#docker)
- [Linux](#linux)
- [Windows](#windows)
- [macOS](#macos)
- [From source](#from-source)
- [Port, address and file paths](#port-address-and-file-paths)
- [Your data: where it lives, backing it up, moving it](#your-data)
- [Login protection and 2FA](#login-protection-and-2fa)
- [Reaching Yata by a hostname](#reaching-yata-by-a-hostname)
- [Reverse proxies](#reverse-proxies)
- [Outbound proxy for tracker traffic](#outbound-proxy-for-tracker-traffic)
- [Updating](#updating)

Security questions — what's in the files, what Yata protects and what it
doesn't — are in [SECURITY.md](SECURITY.md).

## Docker

Put this `docker-compose.yml` wherever you want Yata's data to live:

```yaml
services:
  yata:
    image: ghcr.io/yata-dash/yata-dash:main
    container_name: yata
    ports:
      - "8420:8420"          # then open http://<host>:8420
    volumes:
      - ./data:/data         # config.json + database — back up this folder
    environment:
      - TZ=Etc/UTC           # optional: your timezone, e.g. Australia/Brisbane
    restart: unless-stopped
```

```bash
docker compose up -d        # start (in the background)
docker compose logs -f      # watch what it's doing
docker compose down         # stop — ./data stays
```

You don't need Go, Node or a checkout of the repo; Docker pulls a ready-made
image. Only `./data` has to persist. Mount `./defs` and `./static/themes` as
well if you want to edit tracker definitions or drop in custom themes without
rebuilding.

**Build the image yourself** instead of pulling it: clone the repo and use the
bundled compose file, which has `build: .`:

```bash
git clone https://github.com/Yata-Dash/Yata-Dash && cd Yata-Dash && docker compose up -d
```

## Linux

Download the zip for your architecture from the
[latest release](https://github.com/Yata-Dash/Yata-Dash/releases/latest)
(`linux-amd64` or `linux-arm64`), unpack it, and run the binary from inside
that folder — it reads `defs/`, `static/` and `templates/` from the working
directory:

```bash
unzip yata-*-linux-amd64.zip && cd yata-*-linux-amd64
./yata
# → http://localhost:8420
```

To run it as a service, a minimal systemd unit:

```ini
[Unit]
Description=Yata
After=network-online.target

[Service]
User=yata
WorkingDirectory=/opt/yata
ExecStart=/opt/yata/yata
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

Run it as its own user: `config.json` holds your tracker credentials, and a
dedicated account keeps every other user on the box away from it (Yata sets
the file permissions itself, but permissions only help if the user is yours).

## Windows

Download `yata-<version>-windows-amd64.zip` from the
[latest release](https://github.com/Yata-Dash/Yata-Dash/releases/latest),
extract it, and double-click `yata.exe` (or run it from a terminal to see the
log). Then open http://localhost:8420.

To have it start with Windows, create a shortcut to `yata.exe` in
`shell:startup`, or a Task Scheduler task that runs at log-on. Flags such as
`--port` go in the shortcut's target line.

## macOS

There is a `darwin-arm64` build in each release. Unpack and run `./yata` as on
Linux. macOS may block an unsigned binary on first run — allow it under System
Settings → Privacy & Security.

## From source

Prerequisites: [Go 1.23+](https://go.dev/dl/) and [Node.js 18+](https://nodejs.org/).

```bash
# Linux / macOS
make build && ./yata
```

```powershell
# Windows
.\build.ps1 -Run
```

Contributor setup (dev server, layout, tests) is in
[CONTRIBUTING.md](CONTRIBUTING.md).

## Port, address and file paths

Flags win over environment variables, which win over `config.json`:

```
yata --port 9000 --host 127.0.0.1        # flags
YATA_PORT=9000 YATA_HOST=127.0.0.1 yata  # environment
config.json → { "server": { "host": "127.0.0.1", "port": 9000 } }
```

| Flag | Env | Default | What |
|---|---|---|---|
| `--port` | `YATA_PORT` | `8420` | Listen port |
| `--host` | `YATA_HOST` | `0.0.0.0` | Listen address — `127.0.0.1` to keep it to this machine |
| `--config` | `YATA_CONFIG` | `./config.json` | Trackers, settings, credentials |
| `--data` | `YATA_DATA` | `./yata.db` | SQLite database (stats, history, sessions) |
| `--defs` | `YATA_DEFS` | `./defs` | Tracker definitions |
| `--log` | `YATA_LOG` | `yata.log` next to the database | Log file |
| `--base` | `YATA_BASE` | `.` | Directory holding `static/` and `templates/`, if not the working directory |
| `--allowed-hosts` | `YATA_ALLOWED_HOSTS` | — | See [hostnames](#reaching-yata-by-a-hostname) |
| `-reset-auth` | — | — | Remove the login account and exit (see [locked out](#locked-out)) |

Under Docker these live in `/data`; the compose file above already points them there.

## Your data

Two files, next to the binary (or in `./data` under Docker):

- **`config.json`** — trackers, settings and **credentials** (API keys and
  session cookies, in plain text).
- **`yata.db`** — stats, history and login sessions.

Yata creates both owner-only and re-applies the permissions at every start.
If it warns at startup that permissions could not be set, the filesystem
doesn't support them (some NAS shares, some Windows setups) and the folder
itself needs locking down.

**Backing up** — copy the folder, or use Settings → General → *Export config*
for `config.json` (it asks for your password: the export *is* your
credentials). Automatic scheduled backups of `config.json` can be turned on in
the same place. History lives in `yata.db` and can be exported as CSV from the
History view.

**Moving to another machine** — copy both files (stop Yata first so the
database is consistent), or import the config export on the new install and
let stats rebuild.

**Sharing for support** — send `yata.log`. Credentials are stripped from it.
Never send `config.json`, a config export or `yata.db`.

## Login protection and 2FA

Yata starts with no account: on `127.0.0.1` there's nothing to protect and
nobody to protect it from. But it binds `0.0.0.0` by default, so **anyone who
can reach the port has full access until you set up a login** — Yata warns at
startup and in the UI while that's true.

Set one up in **Settings → General → Account**. Passwords are 12 characters
minimum; five failed attempts lock the source IP for fifteen minutes.

Reachable from outside your network? Turn on **two-factor authentication** in
the same place. Any authenticator app works. Enrolment shows ten single-use
recovery codes **once** — save them.

### Locked out

Use a recovery code. With none left, stop Yata and run it once on the host:

```bash
./yata -reset-auth          # or: docker exec yata /app/yata -reset-auth
```

That removes the account and its sessions and nothing else — trackers, stats,
settings and API tokens are kept. It needs a shell on the machine on purpose;
there is no way to reset the login over the network.

## Reaching Yata by a hostname

**If you browse to an IP address or to `localhost`, skip this.**

Yata answers to IP addresses and `localhost` out of the box. Any other
hostname — a domain, a Tailscale MagicDNS name — must be named first. That
one rule is what blocks DNS-rebinding attacks (a page you visit re-pointing
its own domain at your machine; see [SECURITY.md](SECURITY.md)). You'll know
if you need it: the page says so and names the exact setting. More than one
name is fine.

**Already able to reach the dashboard?** Settings → General → Network →
*Allowed hostnames*. Applies immediately, no restart. Use this to add your domain
before you next travel, from wherever you are.

**A new install you'll only reach by its domain**, or a name you can't get in
to add — set it before Yata starts:

Docker — in the `environment:` block (works on the very first start, before a
config file exists):

```yaml
environment:
  - YATA_ALLOWED_HOSTS=yata.example.com,box.tailnet-name.ts.net
```

Binary / Windows shortcut — `--allowed-hosts=yata.example.com`, or in
`config.json` under `settings` (not `server`):

```json
"settings": {
  "allowed_hosts": ["yata.example.com", "box.tailnet-name.ts.net"]
}
```

systemd — `Environment=YATA_ALLOWED_HOSTS=yata.example.com` in the unit.

Names from every source combine. The one exception is `*`, which turns the
check off entirely and is accepted only from the flag or the environment
variable — never from Settings or an imported config — so disabling it takes
access to the machine, not a browser session.

## Reverse proxies

Whether the hostname setting above applies depends on the proxy:

- **Caddy** (`reverse_proxy`) passes your domain through → **set it**.
- **nginx** with `proxy_set_header Host $host;` → **set it**. Without that
  line nginx sends the upstream address and nothing is needed.
- **Traefik**, **Nginx Proxy Manager** → pass the domain through → **set it**.
- **Tailscale** — the `100.x.y.z` address needs nothing; MagicDNS names and
  `tailscale serve` put a hostname in front → **set it**.

Behind a proxy, turn on **Settings → General → Network → Trust proxy headers** so
rate limiting sees the real client address — and only then, because it makes
`X-Forwarded-For` authoritative. Put TLS on the proxy if Yata leaves your LAN;
Yata itself serves plain HTTP.

## Outbound proxy for tracker traffic

Yata honours the standard `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY`
environment variables for its outbound requests. Tracker API calls and
profile scrapes follow the proxy; `localhost` and `127.0.0.1` bypass it, so
Prowlarr, Jackett and qui on the same box keep working. Add LAN addresses to
`NO_PROXY` if those run elsewhere on your network.

```yaml
environment:
  - HTTPS_PROXY=http://proxy.lan:3128
  - NO_PROXY=localhost,127.0.0.1,192.168.0.0/16
```

Per-tracker proxy selection isn't a feature yet — it's all-or-nothing through
the environment.

## Updating

- **Docker:** `docker compose pull && docker compose up -d`.
- **Binary:** download the new zip, stop Yata, replace the unpacked folder
  (keeping your `config.json` and `yata.db`), start it again.

Yata can check for new versions for you: Settings → General → *Check for
updates*, with an optional daily check (off by default — it contacts GitHub,
and Yata contacts nothing you haven't asked it to). A new version also shows
up as an alert with a link to the release. Tracker definitions and the
pathways dataset have their own versions on the same screen.
