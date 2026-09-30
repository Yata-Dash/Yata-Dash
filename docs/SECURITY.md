# Security

What Yata protects, what it doesn't, and how to run it safely.

## The short version

- **`config.json` holds your tracker API keys and session cookies in plain
  text.** Anyone who can read it can act as you on every tracker you've added.
  Treat it like a password file.
- **Yata locks its own files down** (owner-only permissions, re-applied at
  every start). That protects you from other accounts on the same machine —
  not from root or whoever runs the box.
- **Login protection is off until you set it up.** A Yata that other devices
  can reach, with no account configured, is fully open. Yata warns when this is
  the case.
- **The config export is a full copy, credentials included.** Nobody legitimate
  will ever ask you for it. To get help, send the **log** — credentials are
  stripped from it.
- **Found a security problem?** Open a GitHub issue. This is a small
  self-hosted project; a public fix that lands quickly serves users better than
  a private process.

## What Yata stores

Everything lives in two files next to the binary (or in `./data` under Docker).

| What | Where | If someone gets it |
|---|---|---|
| Tracker API keys | `config.json` | Acts as you on that tracker's API |
| Tracker session cookies | `config.json` | Acts as you on the tracker's **site** — broader than an API key |
| Prowlarr / Jackett / qui credentials | `config.json` | Gets into your other self-hosted services |
| Notification webhooks and bot tokens | `config.json` | Posts as you to your Discord / Telegram |
| Your Yata password hash and 2FA secret | `yata.db` | Your dashboard account |
| Live login sessions | `yata.db` | **Is logged in as you** — no password needed |
| Read-only API token hashes | `yata.db` | Nothing directly (only hashes are kept); a live token reads stats, never credentials |
| Stats and history | `yata.db` | Your ratio, buffer and account age per tracker — personal, not secret |

Two things are worse than they look: a **session cookie** is site access, not
just API access; and the **sessions in `yata.db` are bearer tokens** — reading
that file is the same as being logged in.

## What's safe to share

| File | Safe to share? |
|---|---|
| `config.json` / *Export config* | **No.** The export is the file byte for byte, credentials included. Exporting asks for your password (and 2FA code) so a borrowed session can't take it. |
| Automatic backups (`backups/`) | **No** — same content as `config.json`. |
| `yata.db` | **No.** Contains live login sessions. |
| `yata.log` | **Yes.** Credentials are stripped before anything is written: URL query strings, the user part of a URL, and API response bodies. A failed API response is logged as its *shape* — field names and types — never the values, because a tracker's user endpoint carries your email and IRC key. The one piece of response text kept is the tracker's own error message ("Unauthenticated."). Skim it before posting all the same. |
| *Export alerts* (`yata-alerts.json`) | **Yes.** Webhook URLs, tokens and chat IDs are blanked. |

## Who could get at it

In order of how likely each is to matter.

**1. Another account on the same machine** — the main one. Seedboxes and home
servers are shared. Every file Yata writes is owner-only (`0600`, backup
directory `0700`), and files left loose by an older version are tightened at
startup where the filesystem allows it (Windows and some Docker volumes
don't). *Not covered:* root, and the seedbox operator. If you
don't trust the provider, use API-only setups — a leaked API key is a smaller
loss than a leaked session cookie.

**2. A website you visit while logged in.** Binding to `127.0.0.1` keeps the
network out, but not your own browser, which runs scripts from every site you
open. A page can't read Yata's responses, but it can *cause* requests — and a
trick called DNS rebinding can even make its requests look same-origin. Yata
blocks cross-site requests that change anything, scopes the session cookie
(`SameSite=Lax`, `HttpOnly`), and refuses requests under hostnames it wasn't
told to answer to (`--allowed-hosts`). *Not covered:* an instance with no
account has nothing behind these checks. Set one up if the port is reachable
by anything but you.

**3. Something else on your network.** Gets whatever the login state allows.
With no account, that's everything, including the config export.

**4. A file someone gave you.** A shared config export or a tracker definition
is data, and imports are validated so it can't run script in the dashboard or
point Yata at addresses it shouldn't reach.

**5. A hostile or hacked tracker.** It can send Yata anything; Yata parses it
and never runs it or renders it as HTML. Worst case is wrong numbers.

## What Yata does about it

| Protection | Where in the code |
|---|---|
| Owner-only file permissions, re-applied at startup | `internal/fsperm` |
| Credentials stripped from every log line | `internal/redact` |
| Outbound requests limited to `http(s)`, checked at connect time, redirects re-checked | `internal/netguard` |
| Saved credentials only ever sent to the site they were saved for | `internal/api/{qui,prowlarr,jackett}.go` |
| Cross-site requests blocked | `internal/api/security.go` |
| Hostname check against DNS rebinding | `internal/api/hostguard.go` |
| bcrypt passwords (cost 12, 12-character minimum); TOTP with single-use recovery codes; 15-minute lockout after 5 failed attempts | `internal/api/{auth,totp}.go` |
| Config export re-asks for your password (and 2FA code) | `internal/api/config.go` |
| Login fails closed if the account can't be read | `internal/api/auth.go` |

**Where Yata is allowed to send requests.** Trackers are wherever you said they
are — including a LAN or Tailscale address, since some people reach trackers
that way. Prowlarr, Jackett and qui are normally on `localhost` or the LAN, so
those are allowed too. A tracker *definition's* own API address is held to the
public internet only: definitions are contributed data, and a tracker's API is
never inside your network. Link-local addresses (where cloud instance metadata
lives) are always blocked. No endpoint hands back a raw response from somewhere
it was pointed at, so the most a logged-in caller can learn about your LAN is
whether something answered — and a logged-in caller can already read your
credentials, so that's not the cheap attack.

If you set `HTTP_PROXY` / `HTTPS_PROXY`, tracker traffic follows the proxy and
these address checks are weaker (they run before the connection rather than
during it). Treat that as extra depth over a proxy you trust, not a boundary
against one you don't.

## What Yata does NOT protect against

Stated plainly, because a list of protections without this is marketing.

- **Root, or whoever administers the machine.** File permissions don't apply to them.
- **Anyone who can read `yata.db`.** The sessions in it are bearer tokens.
- **A reachable instance with no account.** Open by design until you set one up.
- **`config.json` at rest.** It is plain text. Encrypting it with a key Yata
  would have to hold to run unattended would hide it, not protect it — the
  same person who can read the file can read the key.
- **Traffic on the wire.** Yata serves plain HTTP. Put it behind a TLS proxy if
  it leaves your LAN.
- **A compromised browser or a malicious extension.** They act as you.
- **Dependencies.** Trusted as published.

## For contributors

- Never log a credential, a URL query string, or an API response body — even
  though `internal/redact` would catch it, don't create the sink.
- Never put a real credential in a test, fixture or definition. Once committed,
  it is committed forever.
- HTML interpolation needs `esc()`; inline event handlers need `jsId()`. They
  are not interchangeable.
- A new outbound HTTP client goes through `netguard`, with the narrowest policy
  that still works.
- Auth code distinguishes "no account" from "couldn't tell". Never collapse
  them into one boolean.

## How to run it safely

| Your setup | Do this |
|---|---|
| **Only on this machine** | Bind `127.0.0.1`. An account is optional. |
| **LAN or Docker** | Set up an account. Add 2FA if anything untrusted shares the network. |
| **Reachable from the internet** | Account + 2FA, behind a reverse proxy with TLS. Turn on *Trust proxy headers* only when a proxy really is in front — it makes `X-Forwarded-For` authoritative for rate limiting. |
| **Shared host or seedbox** | File permissions are handled; the operator isn't. Prefer API-only trackers (no session cookies). Watch for the startup warning that permissions couldn't be set. |
| **Locked out** | Use a recovery code. With none left, stop Yata and run it once with `-reset-auth` on the host — that needs a shell on the machine on purpose, and removes only the account. |

And whatever the setup: never commit, paste or share `config.json` or a config
export. Send the log instead.
