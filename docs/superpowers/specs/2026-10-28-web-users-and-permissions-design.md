<!--
SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
SPDX-License-Identifier: AGPL-3.0-or-later
-->

# Web users, login and per-instance permissions: design

Status: brainstormed on 2026-10-28, waiting for approval, then to be turned into an implementation plan.

## Why

Today everybody who reaches `dzo web` is the same person. The web interface holds one delegate token per
backend (`dzo token create web --role operator --delegate`), and every browser acts with that token's
role. The only notion of "who" is `web.user_header`, a name a reverse proxy may set, which ends up in the
audit log and decides nothing. So the web UI cannot give one person less than operator, cannot give anyone
admin, and the audit log trusts whatever the proxy wrote.

We want real people with their own login and a second factor, and permissions that differ per managed
instance, the way DayZ admin frameworks do it ("operator on chernarus, nothing on livonia").

## Goals

- Every web user is a person identified by their **Steam ID**. This also links an admin to their own
  player profile.
- Login is always two factors: **(username + password, or Sign in through Steam) AND (TOTP or passkey)**.
  A user may register TOTP, one or more passkeys, or both; either one satisfies the second step.
- **Roles per instance**, from the roles that already exist (`viewer`, `moderator`, `operator`, `admin`).
- **`dzo serve` owns users and enforces everything**: it checks the login, holds the sessions and decides
  every permission. The web UI only shows or hides controls according to what the API tells it.
- **One web interface serves one installation.** It may run on another host than `dzo serve` and reaches
  it with a token.
- Admins manage users and their grants in the web UI. Lock-outs are fixed on the command line.
- The audit log names the real person (Steam ID) behind every web action.

## Non-goals

- **Several installations in one web interface.** `web.backends` is removed (U2). Several sites mean
  several web interfaces with separate logins. If several hosts ever need to be one community, the way
  there is a shared database that makes them one installation (see "Later" below), not a web UI that
  merges installations.
- Scripts, bots and the CLI keep using API tokens as today; user logins are for the web UI only.
- External identity providers (OIDC, LDAP), email, self-service password reset by mail.
- Custom roles or per-permission grants. Roles stay the fixed sets in `internal/api/tokens.go`.

## Anti-goals (a failure even if it works)

- Any way to log in with one factor, including a passkey alone.
- A permission that is only enforced by hiding a button in the web UI.
- Secrets (passwords, TOTP seeds, session ids, tokens, recovery codes) in logs, errors, the audit log or
  metrics.

## Decisions

| # | Decision |
|---|---|
| U1 | **Everything lives in `dzo serve` and its database** (`paths.db`, the existing moderation SQLite, new numbered migrations): people, grants, password hashes, TOTP secrets, passkeys, recovery codes and sessions. `dzo web` keeps no state of its own besides the cookie it hands the browser. One database to back up, and the CLI for users runs on the serve host. |
| U2 | **The web UI talks to exactly one installation.** `web.backends` is removed. The web UI uses this host's API (`serve.listen`) with `${paths.secrets}/web.token` as today, or, when it runs elsewhere, the new `web.api` (`url` and `token_file`, both required). The `/b/{backend}/` URL prefix, the backend picker and the ban fan-out to several backends go; how far the templates and routes are simplified is the builder's call. |
| U3 | **Identity is the 64-bit Steam ID.** A person always has one, proved by Sign in through Steam at least once (U8). The username is only the login name for password logins: optional, unique, set by the person after enrolment. |
| U4 | **Login = first factor + second factor, no exceptions.** First: password (argon2id), or Steam OpenID. Second: TOTP (RFC 6238, 6 digits, 30 s, ±1 step, a code is accepted only once) or a WebAuthn passkey used as a second factor. People may register TOTP and several passkeys. One-time recovery codes are generated when the first second factor is registered and count as a second factor; each works once. *(my call)* |
| U5 | **Grants**: a person (Steam ID, display name, created, disabled) has grants (instance or `*`, role). `*` means every instance of the installation, including ones added later. |
| U6 | **A web token.** A new token kind, `dzo token create web --role admin --web` (a `web` flag in the token file, next to `delegate`), is the only kind that may call the login endpoints and send a user session. Plain `--delegate` tokens keep today's behaviour (`X-Dzo-Actor`, acting with their own role), so bots and scripts are not broken. A `web` token on its own, without a user session, may only call the login endpoints (U7); it never acts by itself. The token's role and instance list are a **ceiling**: a user's effective role on an instance is the lower of their grant and the token's role, and only on the token's instances. *(my call)* |
| U7 | **Login runs through the API.** `dzo web` relays each step to `dzo serve` with its `web` token: password check, Steam OpenID verification, TOTP check, WebAuthn begin/finish for login and registration, recovery codes, logout, and the account settings. Serve answers a successful second step with a **user session**: a random secret that `dzo web` puts into the browser cookie and sends back as `X-Dzo-Session` on every API call, together with its token. A session is only valid together with a `web` token, so a session stolen from a browser is useless against the API directly. Half-finished logins (first factor done) are short-lived serve-side state of their own, not a session. |
| U8 | **Enrolment through Steam.** An admin grants a Steam ID (web UI or `dzo user grant`). The person opens the web UI and uses Sign in through Steam. If the Steam ID has a grant, the person has to register a second factor before the session is issued, and gets the recovery codes once. They can then set a username and password. A Steam ID without any grant gets "no access"; nothing is stored for it. |
| U9 | **Steam OpenID 2.0.** `dzo web` redirects the browser to `https://steamcommunity.com/openid/login` with `return_to` under the public URL (U16), and passes the returned query to serve. Serve verifies it with `check_authentication` against Steam (with a timeout), checks `return_to` and the nonce, takes the Steam ID from `claimed_id` (`https://steamcommunity.com/openid/id/<17 digits>`) and refuses anything else. |
| U10 | **Passkeys** use `github.com/go-webauthn/webauthn` in serve. RP ID and origin come from the public URL (U16). |
| U11 | **Sessions** are stored hashed in the serve database with the Steam ID, created and last-used times. Idle and absolute timeouts are configurable (U16). A new session is created at every login. Logout, `dzo user reset`, disabling a person and removing their last grant delete their sessions. The browser cookie is `HttpOnly`, `Secure` (unless the public URL is plain HTTP on loopback), `SameSite=Strict`, host-only. The CSRF token becomes per-session instead of per-process. |
| U12 | **Permissions are always checked per instance.** The principal becomes a map from instance to effective role (a plain token is the same role on each of its allowed instances). Installation-wide permissions (`audit.view`, `ban.global`, `ban.revoke_any`, and the new `users.manage`) need the role `admin` on `*`. Routes without an instance in the path (bans, evidence, players, IPs, geo, tiles, the instance list) check the permission on every instance the request or record names (a ban for A and B needs `ban.instance` on both), and lists return only what the user may see per instance, with IP data shown only where `player.ip` / `ip.history` hold for that instance. Being moderator on A never unlocks anything on B. Records not tied to an instance (a player's history, evidence without an instance) need the permission on at least one instance. The builder lists every non-instance route and its rule in the plan. |
| U13 | **`GET /api/v1` reports the user's grants**: the effective role per instance and the installation-wide permissions, so the web UI shows or hides controls per instance. The existing `role` field remains for tokens without a user. |
| U14 | **User management** in the API, behind `users.manage`: list people with their grants, add or change a person and their grants, disable, delete, reset (U17). Everything goes to the audit log. Nobody can change their own grants (no demoting and no promoting yourself), nor disable, delete or reset themselves; the API refuses it, and the web UI does not offer it. Otherwise there is no lock-out guard. Today only an admin on `*` holds `users.manage`, so promoting oneself could not raise anything, but the rule holds anyway, so that it stays safe if `users.manage` is ever given to a lower role. An installation without an admin is fixed on the command line (`dzo user grant <steamid> --role admin`). |
| U15 | **Open streams are re-checked.** The SSE relays (`/stream`, `/feed`) re-check the session and grant at least every 60 s and close when they are gone, so a logged-out, expired, disabled or revoked user loses the live view within a minute. *(my call)* |
| U16 | **Configuration** (documented in the `docs/configuration.rst` example and prose, `docs/web.rst` and the quickstart, as AGENTS.md requires): `serve.web_url`, the address people open the web UI at (required for user logins; it fixes the Steam `return_to`, the WebAuthn RP ID and origin; `https` unless the host is loopback; `dzo web` learns it from `GET /api/v1`, so it is set once, on the serve host). `serve.login.session_idle_hours` (default 12) and `serve.login.session_max_days` (default 7). `web.api` (U2). `web.trusted_proxies` (U18). **Removed**: `web.backends` and `web.user_header`; a config that still sets either is refused with a message pointing to the migration notes. *(key names are my call)* |
| U17 | **CLI on the serve host** (local database, like `dzo token`): `dzo user list`, `dzo user grant <steamid> --role <r> [--instance <n>…]` (no `--instance` = `*`), `dzo user revoke <steamid> [--instance <n>…]`, `dzo user disable\|enable\|delete <steamid>`, `dzo user reset <steamid\|username>` (removes password, TOTP, passkeys, recovery codes and sessions; the person enrols again through Steam). `dzo user grant` is how the first admin is made. |
| U18 | **Client address and brute force.** `dzo web` works out the browser's address and passes it to serve in `X-Dzo-Client` (accepted from a `web` token only). Without `web.trusted_proxies` (addresses or CIDRs, default none) it is the connection's address and every forwarding header is ignored. When the connection comes from a trusted proxy, `X-Forwarded-For` is used: the right-most entry that is not itself a trusted proxy (so a client cannot inject an address by sending the header itself). `X-Real-IP` is used only when `X-Forwarded-For` is missing, for nginx setups that set just that. A header that does not parse as an address is ignored, and the proxy's address is used. `X-Forwarded-Proto` is not used: whether the site is HTTPS follows from `serve.web_url`, which a request cannot change. *(my call)* **Logged**: every login step (success or failure, which factor, Steam ID or "unknown user", never a secret) with the client address and, behind a proxy, the proxy's address; every audit entry from the web UI gets the client address as a new field. Failed first or second factors are counted per account and per client address, with growing delays and no hard lock per account, so that nobody can lock an admin out by guessing. Answers do not say whether a username exists. |
| U19 | **Audit.** A web action is logged with the actor `<token name>:<steamid>`, the person's display name and the client address (U18), source `web`. **The client addresses expire**: `serve.audit_ip_retention_days` (default 90; `0` keeps them for ever; negative refused) says how long an entry keeps its address. Once a day `dzo serve` blanks the address of older entries; the entries themselves stay. A change takes effect when `dzo serve` starts. The login lines in the service log (journald) are the host's log retention, which the docs point out. |
| U20 | **Dependencies**: `github.com/go-webauthn/webauthn` and `golang.org/x/crypto` (argon2id). TOTP is written on the standard library (HMAC-SHA1). The TOTP enrolment page needs a QR code: a QR library or a small encoder is the builder's choice. Each new dependency is justified in its commit and checked with `make licenses`. |
| U21 | **HTTPS: existing certificates or a reverse proxy, no ACME.** `serve` and `web` get a `tls:` block with the same keys and behaviour as `exporter.tls` (`cert_file` + `key_file` switch on HTTPS; `client_ca_file` requires client certificates, for example so that only the web host may reach the API). **Changed certificates are picked up automatically, without a restart and without dropping connections**: as in the exporter, the files are checked on TLS handshakes (at most every few seconds) and on SIGHUP / `systemctl reload`; a new pair that does not load is logged and the old one stays active. The shared code is extended so that `client_ca_file` and `web.api.ca_file` reload the same way. *(my call)* So certbot or any other renewal needs no deploy hook beyond writing the files. **Port 443 without a proxy** is documented in `docs/web.rst` (and linked from `docs/operations.rst`): dzo runs as `dayz` in user units, which cannot be given `CAP_NET_BIND_SERVICE`, so the page shows the two ways that work and their trade-offs: lowering `net.ipv4.ip_unprivileged_port_start` to 443 with a `/etc/sysctl.d/` file (simple, but lets every local user bind 443 and up), or an nftables `redirect` from 443 to `web.listen` (keeps the limit, needs a firewall rule). `setcap` on the binary is mentioned as unsuitable (every package upgrade drops it, and it applies to every user). dzo itself needs no change for this. The exporter's certificate code is shared, not copied. Listening on a non-loopback address needs `tls` or `allow_insecure_http`, as now. A reverse proxy in front stays supported (`web.trusted_proxies`, U18). dzo gets no Let's Encrypt/ACME client of its own: it would need ports 80/443, privileges and certificate state that the host's own tooling already handles. User logins refuse to start unless `serve.web_url` is `https`, or plain HTTP on loopback. `dzo web` reaching serve over `web.api` checks the server certificate as usual; a CA file for a private CA is `web.api.ca_file`. *(key name of the CA file is my call)* |

## How it looks to the people using it

- **Login page**: username + password, or a "Sign in through Steam" button. Then a second page asks for a
  TOTP code, offers "use a passkey", or takes a recovery code, depending on what the account has.
- **First visit after being granted**: Sign in through Steam, then "set up your second factor" (scan a QR
  code for TOTP, register a passkey, or both), then the recovery codes, shown once.
- **Account page** for everyone: set or change username and password, add or remove passkeys, set up or
  replace TOTP, new recovery codes, log out other sessions. Removing the last second factor is refused.
- **Users page** for admins: the people with grants, their role per instance, add a Steam ID, change
  roles, disable, delete, reset.
- **Everywhere else**: buttons follow the effective role on that instance (U13). An instance without a
  grant is not listed.

## Migration

Existing installations have a `web` operator delegate token and maybe `web.user_header` or
`web.backends`. After the upgrade:

- `dzo web` refuses to start with `web.user_header` or `web.backends` set, and says why. Someone with
  several backends sets up one web interface per installation (with `web.api` where it runs elsewhere).
- `dzo serve` needs `serve.web_url`; without it the web UI shows that logins are not configured.
- The old delegate token no longer gives the web UI anything. Recreate it with
  `dzo token create web --role admin --web`.
- Before anyone can log in, someone runs `dzo user grant <their steamid> --role admin` on the serve host.
- `docs/migration.rst` and `debian/changelog` describe these steps.

## Later: several hosts as one installation

Not built now, but this design must not rule it out. The idea: several `dzo serve` hosts share one
database (the PostgreSQL step, D26) and become **one installation**: one list of players, bans, people
and grants, and one web interface with one login. The web UI then still talks to "one installation", so
nothing in the user-facing design changes. What the build has to respect now:

- **All login and session state is in the database**, behind the moderation store interface, in
  portable SQL like the ban tables: people, grants, credentials, sessions, half-finished logins,
  WebAuthn ceremony data, used TOTP steps, recovery codes. Nothing that decides a login or a permission
  lives only in one process's memory or in a local file, so that any serve host sharing the database
  can check any session. Failed-login counters may stay in memory per process for now; they are a
  speed bump, not a decision.
- **Writes are safe with several writers**: a used TOTP step, a used recovery code and a session renewal
  are single conditional statements (or transactions), not read-then-write in Go.
- **Grants name instances by name**, with `*` for the whole installation. Instance names would then have to
  be unique across the shared installation; nothing here may assume an instance runs on the host that
  answers the request.
- **The web UI reaches the installation through one place** (`apiclient`, configured by `web.api`). Live
  instance calls go through the API by instance name, so a later change can send them to the host that
  runs the instance (or have the API forward them) without touching the pages.
- **`serve.web_url` and the session timeouts are installation settings**, read by serve, not by the web UI,
  so a shared installation sets them once.
- Tokens stay a per-host JSON file for now. A shared installation would need the `web` token accepted by
  every host. That is part of the later work, noted here so nobody is surprised.

## Testing

- Unit tests through the public API of each package: grant resolution and capping by the token, U12 for
  every non-instance route, user management, TOTP (RFC 6238 test vectors, replay), password hashing,
  sessions and timeouts with a fake clock, Steam OpenID verification against an `httptest` server
  standing in for Steam (the Steam endpoint is an internal option, not configuration), stream re-checks.
- Browser e2e (chromedp): password + TOTP login, Steam login against the fake Steam, passkey
  registration and login with the Chrome DevTools virtual authenticator (`WebAuthn.addVirtualAuthenticator`),
  buttons hidden and API calls refused for a viewer on one instance and an operator on another.
- Fuzz the OpenID response parser and the session and client-address headers.
- The authorization change touches the player and vehicle actions in `internal/api`, so `make test-client`
  (stable and `ARGS=--exp`) runs before that commit, as AGENTS.md asks.

## Left to the builder

- Table layouts and migration numbering; the exact JSON of the new endpoints (documented in
  `docs/api.rst`).
- Page design within the existing web UI style; how far the backend-prefixed routes are simplified.
- Delay values for U18, recovery code count and format, the lifetime of half-finished logins.
- How the web UI caches `GET /api/v1` per session (stale for at most one page load after a change).
