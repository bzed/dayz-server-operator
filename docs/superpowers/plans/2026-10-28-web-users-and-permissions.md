<!--
SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
SPDX-License-Identifier: AGPL-3.0-or-later
-->

# Web users, login and per-instance permissions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace "every web user is the same operator token" with real people (Steam ID), a two-factor login, and per-instance roles enforced by `dzo serve`.

**Architecture:** `dzo serve` owns people, grants, credentials and sessions in the existing moderation SQLite database and runs every login step; a new `web` token kind lets `dzo web` relay those steps and then act for a user session (`X-Dzo-Session`), capped by the token. The API's `Principal` becomes per-instance (`RoleOn(instance)`), and every handler checks against the instance it touches. `dzo web` serves one installation, stores nothing but a cookie, and gains login, account and users pages.

**Tech Stack:** Go 1.27, `modernc.org/sqlite`, `github.com/go-webauthn/webauthn`, `golang.org/x/crypto/argon2`, `rsc.io/qr` (or an equivalent small QR encoder, builder's call), cobra, `html/template` + htmx, chromedp for e2e.

**Spec:** `docs/superpowers/specs/2026-10-28-web-users-and-permissions-design.md` (decisions U1–U21 are referenced by number below).

## Global Constraints

- Login is always (password or Steam) AND (TOTP, passkey or recovery code). A passkey is never a first factor. (U4)
- Permissions are enforced only in `dzo serve`; the web UI hides controls from `GET /api/v1` but every action is checked by the API. (U12, U13)
- Every piece of login and session state is in the database, in portable SQL; nothing that decides a login lives only in process memory. Failed-login counters are the one exception. ("Later" section of the spec)
- Used TOTP steps, used recovery codes, used Steam nonces and session touches are single conditional statements (check rows affected), never read-then-write in Go.
- Grants: roles `viewer`, `moderator`, `operator`, `admin` only; instance name or `*`. Installation-wide permissions (`audit.view`, `ban.global`, `ban.revoke_any`, `users.manage`) need `admin` on `*`. (U5, U12)
- A user's effective role on an instance = min(max(grant on instance, grant on `*`), token role), and nothing on instances the token does not allow. (U6)
- No secret (password, TOTP secret or code, session, pending-login id, token, recovery code) in a log line, error string, audit entry or metric.
- Secrets compared in constant time; stored session / pending / recovery secrets are SHA-256 hashes.
- Password: argon2id, m=64 MiB, t=3, p=2, 16-byte salt, 32-byte key, PHC string (`$argon2id$v=19$m=65536,t=3,p=2$<salt>$<key>`); length 12–256 bytes.
- TOTP: RFC 6238, SHA-1, 6 digits, 30 s, ±1 step, 20-byte secret, a step is accepted once per person.
- Recovery codes: 10 per set, `xxxxx-xxxxx` from the base32 alphabet (lower case), compared case- and dash-insensitively; a new set replaces the old one.
- Session secret and pending-login id: 32 random bytes, hex, prefixes `dzs_` and `dzp_`. Pending logins live 5 minutes.
- Defaults: `serve.login.session_idle_hours` 12, `serve.login.session_max_days` 7 (key names change from the spec's `serve.session.idle/max` to the `_days` style `config.go` already uses; Task 2 updates the spec). Stream re-check every 60 s (U15).
- Brute force: per account and per client address, 5 free failures, then the wait doubles from 1 s to at most 15 min; a success resets the account's counter; counters are forgotten after 1 h without failures. Refusals answer 429 with `Retry-After`, and the same message whether or not the username exists. (U18)
- Every new config key, flag and endpoint is documented in the same commit: YAML example with `# default` and prose in `docs/configuration.rst`, `docs/cli.rst`, `docs/api.rst`, `docs/web.rst`. `make docs` passes. (AGENTS.md)
- Every task ends with `make check` passing; total coverage ≥ 85 %. Tests write only under `t.TempDir()`, never sleep, use fake clocks.
- Commits: one logical change, area prefix as in `git log`, trailer `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Work on branch `feature/web-users` (worktree via superpowers:using-git-worktrees).
- `dzo user …` writes the serve database directly while `dzo serve` may run (WAL + `busy_timeout(5000)` already in `dsn`). This is a documented exception to B3 of the ban design, like `dzo token`.

## Review Focus

1. **Steam's redirect back is a cross-site navigation**, so a `SameSite=Strict` cookie is not sent on it. The login-state cookie for the Steam round trip is `SameSite=Lax`; after a successful callback the web UI answers 200 with a page that moves on to `/` itself (meta refresh), so the Strict session cookie is in place for the next, same-site request. Test in Task 12.
2. **Login CSRF on the Steam callback**: the callback carries a random `state` that must match the Lax `dzo_login` cookie, or an attacker could log a victim into the attacker's account. Test in Task 12.
3. **A grant for an instance that no longer exists, or a person disabled mid-session**: `RoleOn` just finds nothing, the next request is 403 and open streams close within 60 s; nothing crashes or shows another instance. Test in Task 6 and Task 9.
4. **Two requests using the same TOTP code or recovery code at once** (double submit): exactly one succeeds. Test in Task 5 (store, concurrent goroutines) and Task 7.
5. **`X-Forwarded-For` with a forged left part, IPv6 with zone, garbage, or a chain where every hop is trusted**: the right-most untrusted entry wins, an unparsable value falls back to the proxy's address, all trusted means the left-most entry. Test and fuzz in Task 4.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/tlsfiles/tlsfiles.go` (new) | Certificate pair and CA pool that reload when their files change; moved out of `internal/exporter/serve.go`. |
| `internal/config/config.go` | New keys `serve.tls`, `serve.web_url`, `serve.login.*`, `web.tls`, `web.api`, `web.trusted_proxies`; refuses `web.backends`, `web.user_header`. |
| `internal/auth/` (new): `password.go`, `totp.go`, `secret.go`, `steam.go`, `clientaddr.go`, `backoff.go` | Pure login primitives, no database, no HTTP routing. |
| `internal/moderation/migrations/sqlite/0004_accounts.sql` (new) | People, grants, credentials, passkeys, recovery codes, sessions, pending logins, Steam nonces. |
| `internal/moderation/accounts.go`, `credentials.go`, `sessions.go` (new) | Store methods for the tables above. |
| `internal/api/principal.go` (new) | `Principal`, `User`, per-instance role resolution and capping. |
| `internal/api/tokens.go` | `Web` flag on tokens. |
| `internal/api/server.go`, `bans.go`, `evidence.go`, `players.go`, `ips.go`, `geo.go` | Per-instance checks (U12), session resolution, stream re-check, `Info.User`. |
| `internal/api/login.go`, `passkeys.go`, `account.go`, `users.go` (new) | Login, WebAuthn, account and user-management endpoints. |
| `internal/admin/audit.go` | `Client` field on `AuditEntry`. |
| `internal/apiclient/client.go`, `auth.go`, `users.go` (new) | Session and client headers; client methods for the new endpoints. |
| `cmd/dzo/serve.go`, `cmd/dzo/user.go` (new), `cmd/dzo/web.go` | `--web` tokens, `dzo user …`, single-installation web, TLS wiring. |
| `internal/serve/serve.go` | TLS on the API listener, accounts wiring, session pruning in upkeep. |
| `internal/units/units.go` | `ExecReload` for `dzo-web.service`. |
| `internal/web/server.go`, `auth.go`, `account.go`, `users.go` (new), templates `login.html`, `account.html`, `users.html` (new), `static/passkey.js` (new) | One backend, session middleware, per-session CSRF, login/account/users pages. |
| `docs/configuration.rst`, `web.rst`, `api.rst`, `cli.rst`, `migration.rst`, `operations.rst`, `quickstart.rst`, `debian/changelog` | Documentation and packaging notes. |

---

### Task 1: Shared reloading TLS files

**Files:**
- Create: `internal/tlsfiles/tlsfiles.go`, `internal/tlsfiles/tlsfiles_test.go`
- Modify: `internal/exporter/serve.go:24-100,170-200` (use the new package; delete `certs`)
- Move tests: the certificate-reload tests of `internal/exporter` to `internal/tlsfiles`

**Interfaces:**
- Produces:
  - `func NewPair(certFile, keyFile string, logf func(string, ...any)) (*Pair, error)`; `(*Pair).GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error)`; `(*Pair).Reload()`
  - `func NewPool(caFile string, logf func(string, ...any)) (*Pool, error)`; `(*Pool).Get() *x509.CertPool`; `(*Pool).Reload()`
  - `func ServerConfig(p *Pair, clientCA *Pool) *tls.Config`: MinVersion TLS 1.2; with a pool, `RequireAndVerifyClientCert` and a `GetConfigForClient` that uses the pool's current certificates
  - `func ClientConfig(ca *Pool) *tls.Config`: `RootCAs` from the pool on every handshake (via `VerifyConnection`, or a fresh `tls.Config` per dial), for `web.api.ca_file`
  - `const Recheck = 5 * time.Second` (the existing `certChecks`)

- [ ] **Step 1: Write the failing tests** `TestPairReloadsChangedFiles` (write pair A, get it, write pair B with a newer mtime, advance the fake clock past `Recheck`, get returns B), `TestPairKeepsOldOnBadFiles` (corrupt key → old pair still served, one log line), `TestPoolReloads` (CA A then CA B: a client cert signed by B is accepted only after the reload), `TestReloadOnSignal` (`Reload()` without mtime change re-reads). Use a `now func() time.Time` field set by an unexported test helper in `export_test.go`.
- [ ] **Step 2: Run** `go test ./internal/tlsfiles/` and see it fail (package missing).
- [ ] **Step 3: Implement** by moving `certs` from `internal/exporter/serve.go` and generalising it to N files; `Pool` reuses the mtime check.
- [ ] **Step 4: Switch the exporter** to `tlsfiles.NewPair`, `NewPool`, `ServerConfig`; its reload goroutine calls both `Reload`s. Existing exporter tests stay green.
- [ ] **Step 5: Run** `go test ./internal/tlsfiles/ ./internal/exporter/` → PASS; `make check`.
- [ ] **Step 6: Commit** `tls: move the reloading certificate files into internal/tlsfiles, reload the client CA too` (the docs line for `exporter.tls.client_ca_file` now says it reloads).

### Task 2: Configuration

**Files:**
- Modify: `internal/config/config.go:89-170,330-436`, `internal/config/config_test.go`
- Modify: `docs/configuration.rst` (examples + prose), `docs/superpowers/specs/2026-10-28-web-users-and-permissions-design.md` (U16 key names)

**Interfaces:**
- Produces:
  - `type TLS struct { CertFile string \`yaml:"cert_file"\`; KeyFile string \`yaml:"key_file"\`; ClientCAFile string \`yaml:"client_ca_file"\` }` (shared by `Exporter.TLS`, `Serve.TLS`, `Web.TLS`)
  - `Serve.TLS TLS`, `Serve.WebURL string \`yaml:"web_url"\``, `Serve.Login struct { SessionIdleHours *int \`yaml:"session_idle_hours"\`; SessionMaxDays *int \`yaml:"session_max_days"\` } \`yaml:"login"\``
  - `func (s Serve) SessionIdle() time.Duration` (default 12h), `func (s Serve) SessionMax() time.Duration` (default 7 days); 0 or negative refused at load
  - `Web.TLS TLS`, `Web.API struct { URL string \`yaml:"url"\`; TokenFile string \`yaml:"token_file"\`; CAFile string \`yaml:"ca_file"\` } \`yaml:"api"\``, `Web.TrustedProxies []string \`yaml:"trusted_proxies"\``
  - `func CheckListen(addr string, insecure, tls bool) error`: non-loopback allowed when `tls` or `insecure`
- Removes: `Backend`, `Web.Backends`, `Web.UserHeader`. Keep yaml fields `backends` and `user_header` as unexported sentinel fields so that `Load` can refuse them with: `web.backends is no longer supported: one web interface serves one installation, see the migration notes (docs/migration.rst)` and `web.user_header is no longer supported: dzo web has its own login now, see the migration notes (docs/migration.rst)`.

- [ ] **Step 1: Write failing tests**: `TestLoadRefusesWebBackends`, `TestLoadRefusesUserHeader` (error contains `migration`), `TestServeWebURLMustBeHTTPS` (`http://dzo.example` refused, `http://127.0.0.1:8081` and `https://dzo.example` accepted, path/query/fragment refused), `TestSessionDefaults` (12h, 168h; `0` refused), `TestTLSPairsGoTogether` for serve and web, `TestWebAPIRequiresURLAndToken`, `TestTrustedProxiesParse` (address or CIDR, garbage refused naming the entry), `TestCheckListenWithTLS`.
- [ ] **Step 2: Run** `go test ./internal/config/` → FAIL.
- [ ] **Step 3: Implement** the fields and checks in `validate`. `serve.web_url` is optional at load (logins are then off); the https rule applies when set.
- [ ] **Step 4: Run** → PASS.
- [ ] **Step 5: Docs**: every new key in the `config.yaml` example with `# default` / `# default: none`, prose per AGENTS.md (what, values, default, errors, needs, interactions, when it takes effect: all at restart except certificate files, which reload). Remove `backends`/`user_header` from the example and say in prose they are refused. Fix U16 in the spec to the new key names. `make docs`.
- [ ] **Step 6: Commit** `config: TLS for serve and web, web.api, trusted proxies, login settings; refuse web.backends and web.user_header`.

### Task 3: HTTPS on `dzo serve` and `dzo web`, one installation per web UI

**Files:**
- Modify: `internal/serve/serve.go:280-300` (API listener), `cmd/dzo/web.go`, `internal/units/units.go:233-240`, `internal/apiclient/client.go:36-39`
- Modify: `internal/web/server.go:40-100` (`New(b *Backend, opts Options)`), `internal/web/bans.go` (no fan-out to other backends), tests constructing `web.New`
- Docs: `docs/web.rst` (replace "Several installations" by `web.api`; HTTPS section with the port-443 subsection), `docs/operations.rst` (link), `docs/migration.rst` (several backends → one web UI each)

**Interfaces:**
- Consumes: Task 1 `tlsfiles`, Task 2 config.
- Produces: `func New(b *Backend, opts Options) (*Server, error)` in `internal/web` (the URL prefix `/b/{backend}/` stays, with the installation's name; changing URLs is out of scope); `func apiclient.NewTLS(base, token string, tlsConf *tls.Config) *Client`.

- [ ] **Step 1: Write failing tests**: `TestServeAPIOverTLS` in `internal/serve` (self-signed pair in `t.TempDir()`, client with that CA gets `/healthz`), `TestWebUnitHasExecReload` in `internal/units`, `TestWebNewOneBackend` in `internal/web` (ban form lists no other installations), `TestWebAPIClientUsesCAFile` in `cmd/dzo` (a `web.api` with `ca_file` reaches an `httptest.NewTLSServer`).
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement**: serve wraps the API listener with `tls.NewListener(ln, tlsfiles.ServerConfig(...))` when `serve.tls` is set; `mod_listen` stays plain (local game servers). `dzo web` uses `web.api` or `http(s)://serve.listen` + `${paths.secrets}/web.token`; `web.tls` the same way as serve; SIGHUP reloads all its files via `reloadOnSIGHUP`. `dzo-web.service` gets `ExecReload=/bin/kill -HUP $MAINPID` like `dzo-serve.service`.
- [ ] **Step 4: Run** → PASS; `make check`; `make e2e`.
- [ ] **Step 5: Docs**: `docs/web.rst` HTTPS section: certificate files or a reverse proxy, no ACME and why; automatic reload; port 443 without a proxy: `/etc/sysctl.d/50-dzo.conf` with `net.ipv4.ip_unprivileged_port_start = 443` (every local user may then bind ≥ 443) or an nftables `redirect` from 443 to `web.listen` (example ruleset), `setcap` unsuitable (lost on upgrade, applies to every user), user units cannot get `CAP_NET_BIND_SERVICE`. `make docs`.
- [ ] **Step 6: Commit** `web: one installation per web interface; HTTPS for serve and web from reloading certificate files`. Add a `debian/changelog` entry (unit change).

### Task 4: Login primitives (`internal/auth`)

**Files:**
- Create: `internal/auth/doc.go`, `password.go`, `totp.go`, `secret.go`, `steam.go`, `clientaddr.go`, `backoff.go`, each with `_test.go`; `fuzz_test.go`
- Modify: `go.mod`/`go.sum` (`golang.org/x/crypto`)

**Interfaces:**
- Produces:
  - `func HashPassword(pw string) (string, error)`; `func CheckPassword(hash, pw string) bool`; `var ErrPasswordLength` (outside 12–256 bytes)
  - `func NewTOTPSecret() ([]byte, error)`; `func TOTPURI(secret []byte, issuer, account string) string` (`otpauth://totp/<issuer>:<account>?secret=<base32 no padding>&issuer=<issuer>&algorithm=SHA1&digits=6&period=30`); `func TOTPStep(secret []byte, code string, now time.Time) (step int64, ok bool)`
  - `func NewSecret(prefix string) (secret, hash string, err error)`; `func HashSecret(secret string) string`
  - `func NewRecoveryCodes() (codes, hashes []string, err error)`; `func HashRecoveryCode(code string) string`
  - `type Steam struct { Endpoint string; HTTP *http.Client }` (zero `Endpoint` = `https://steamcommunity.com/openid/login`); `func (s Steam) RedirectURL(returnTo, realm string) string`; `func (s Steam) Verify(ctx context.Context, q url.Values, returnTo string) (steamID, nonce string, err error)`
  - `type Proxies []netip.Prefix`; `func ParseProxies(entries []string) (Proxies, error)`; `func (p Proxies) ClientAddr(r *http.Request) netip.Addr`
  - `type Backoff`; `func NewBackoff(now func() time.Time) *Backoff`; `func (b *Backoff) Wait(keys ...string) time.Duration`; `Fail(keys ...string)`; `Success(key string)`

- [ ] **Step 1: Write failing tests**:
  - `TestPasswordRoundTrip`, `TestPasswordWrong`, `TestPasswordLength` (11 and 257 bytes → `ErrPasswordLength`), `TestPasswordHashFormat` (prefix `$argon2id$v=19$m=65536,t=3,p=2$`).
  - `TestTOTPRFC6238` with the RFC's SHA-1 vectors truncated to 6 digits (secret `12345678901234567890`; T=59 → `287082`, T=1111111109 → `081804`, T=1234567890 → `005924`), `TestTOTPWindow` (±1 step ok, ±2 refused), `TestTOTPRejectsNonDigits`.
  - `TestRecoveryCodes` (10 unique, format `^[a-z2-7]{5}-[a-z2-7]{5}$`, `HashRecoveryCode("ABCDE-fghij") == HashRecoveryCode("abcdefghij")`).
  - `TestSteamVerify` against an `httptest` server: valid `is_valid:true` → steam ID; `is_valid:false` → error; `claimed_id` not `https://steamcommunity.com/openid/id/<17 digits>` → error; `openid.return_to` ≠ `returnTo` → error; `openid.op_endpoint` ≠ Endpoint → error; server timeout → error. The check request is a POST of the received fields with `openid.mode=check_authentication`.
  - `TestClientAddr` table: no proxies → `RemoteAddr`; trusted proxy + `X-Forwarded-For: 6.6.6.6, 1.2.3.4` → `1.2.3.4`; forged left part ignored; all hops trusted → left-most; `X-Real-IP` used only without XFF; garbage → proxy address; untrusted peer sending XFF → peer. `FuzzClientAddr` never panics and always returns a valid address.
  - `TestBackoff` with a fake clock: 5 failures free, 6th → 1 s, doubling, capped at 15 min; `Success` resets the account key; forgotten after 1 h; two keys checked together return the larger wait.
- [ ] **Step 2: Run** `go test ./internal/auth/` → FAIL.
- [ ] **Step 3: Implement** each file (TOTP on `crypto/hmac` + `crypto/sha1`; `Backoff` guarded by a mutex, a map pruned on access).
- [ ] **Step 4: Run** → PASS; `make fuzz`; `make licenses` (x/crypto).
- [ ] **Step 5: Commit** `auth: password hashing, TOTP, recovery codes, Steam OpenID, client address and login backoff` (commit message justifies `golang.org/x/crypto`: argon2id, no stdlib equivalent).

### Task 5: Accounts in the serve database

**Files:**
- Create: `internal/moderation/migrations/sqlite/0004_accounts.sql`, `internal/moderation/accounts.go`, `credentials.go`, `sessions.go`, with `_test.go` each

**Interfaces:**
- Produces (all methods on `*moderation.Store`, `ctx` first; `ErrNotFound` for missing rows, new `ErrUsernameTaken`):
  - `type Grant struct { Instance, Role string }`; `type Person struct { SteamID, Name, Username string; Disabled bool; Created time.Time; Grants []Grant }`
  - `People(ctx) ([]Person, error)`; `Person(ctx, steamID string) (Person, error)`; `PersonByUsername(ctx, username string) (Person, error)`; `PutPerson(ctx, p Person, now time.Time) error` (upsert name, disabled, replace grants; credentials untouched; disabling or an empty grant list deletes the person's sessions); `DeletePerson(ctx, steamID string) error` (cascade); `ResetCredentials(ctx, steamID string) error` (password, username, TOTP, passkeys, recovery, sessions)
  - `type Passkey struct { ID []byte; Name string; Data []byte; Created, LastUsed time.Time }`; `type Credentials struct { PasswordHash string; TOTPSecret []byte; Passkeys []Passkey; RecoveryLeft int }`
  - `Credentials(ctx, steamID string) (Credentials, error)`; `SetPassword(ctx, steamID, username, hash string) error`; `SetTOTP(ctx, steamID string, secret []byte) error`; `ClearTOTP(ctx, steamID string) error`; `UseTOTPStep(ctx, steamID string, step int64) (bool, error)`; `AddPasskey(ctx, steamID string, p Passkey) error`; `UpdatePasskey(ctx, steamID string, id, data []byte, used time.Time) error`; `DeletePasskey(ctx, steamID string, id []byte) error`; `SetRecoveryCodes(ctx, steamID string, hashes []string) error`; `UseRecoveryCode(ctx, steamID, hash string) (bool, error)`
  - `type Session struct { SteamID, Client string; Created, LastUsed time.Time }`; `CreateSession(ctx, hash, steamID, client string, now time.Time) error`; `Session(ctx, hash string, now time.Time, idle, max time.Duration) (Session, error)` (expired → `ErrNotFound`; touches `last_used` with `UPDATE … WHERE hash=? AND last_used < ?` at most once a minute); `DeleteSession(ctx, hash string) error`; `DeleteSessions(ctx, steamID, keepHash string) error`; `PruneLogins(ctx, now time.Time, idle, max time.Duration) error` (sessions, pending logins, nonces older than 1 h)
  - `type PendingLogin struct { SteamID, Via string; Enrol bool; Ceremony, TOTPSecret []byte; Expires time.Time }` (`Via`: `password`, `steam`, `account`); `CreatePending(ctx, hash string, p PendingLogin) error`; `Pending(ctx, hash string, now time.Time) (PendingLogin, error)`; `UpdatePending(ctx, hash string, p PendingLogin) error`; `DeletePending(ctx, hash string) error`
  - `UseSteamNonce(ctx, nonce string, now time.Time) (bool, error)`

- [ ] **Step 1: Write failing tests**: `TestPutPersonReplacesGrants`, `TestDisableDropsSessions`, `TestUsernameUnique` (`ErrUsernameTaken`, case-insensitive), `TestResetCredentials` (everything gone, person and grants stay), `TestDeletePersonCascades`, `TestUseTOTPStepOnce` and `TestUseRecoveryCodeOnce` each with 8 goroutines racing on the same value → exactly one `true`, `TestSessionIdleAndMax` (fake times), `TestSessionTouchThrottled`, `TestPendingExpires`, `TestSteamNonceOnce`, `TestPruneLogins`, `TestMigration0004FromV3` (open a database at schema 3 from the existing test fixtures, migrate, old rows intact).
- [ ] **Step 2: Run** `go test ./internal/moderation/` → FAIL.
- [ ] **Step 3: Implement** the migration (tables `people`, `grants`, `passkeys`, `recovery_codes`, `login_sessions`, `pending_logins`, `steam_nonces`; TOTP secret, last used step, username and password hash as columns of `people`; `ON DELETE CASCADE`; no SQLite-only syntax beyond what 0001–0003 already use) and the methods.
- [ ] **Step 4: Run** → PASS (`-race`).
- [ ] **Step 5: Commit** `moderation: people, grants, credentials and login sessions`.

### Task 6: Per-instance principals and the `web` token

**Files:**
- Create: `internal/api/principal.go`, `internal/api/principal_test.go`
- Modify: `internal/api/tokens.go` (`Web` flag, `PermUsersManage`), `internal/api/server.go:100-230,330-380,400,532`, `bans.go:70-100,210`, `evidence.go`, `players.go:35-130`, `ips.go:17`, `geo.go:44`, `cmd/dzo/serve.go:55-90` (`--web`), `internal/serve/serve.go:241-280` (wire `Accounts`)
- Docs: `docs/api.rst` (authentication: `web` tokens, `X-Dzo-Session`, per-instance rules), `docs/cli.rst` (`dzo token create --web`)

**Interfaces:**
- Consumes: Task 5 `moderation.Person`, `Grant`, `Session`; Task 4 `auth.HashSecret`.
- Produces:
  - `Token.Web bool \`json:"web,omitempty"\``; `func (s *TokenStore) Create(name string, role Role, instances []string, delegate, web bool, ttl time.Duration) (string, Token, error)` (both flags → error; `web` needs role viewer/moderator/operator/admin)
  - `const PermUsersManage = "users.manage"` (admin only); `func installationWide(perm string) bool`
  - `type User struct { SteamID, Name string; Grants []moderation.Grant }`; `type Principal struct { Token Token; User *User }`
  - `func (p Principal) RoleOn(instance string) Role` (zero `Role` = none); `Can(instance, perm string) bool`; `CanAny(perm string) bool`; `CanAll(perm string) bool`; `Allowed(instance string) bool`; `Instances(all []string) []string` (the instances with any role, in `all`'s order); `Key() string` (rate-limit key: token ID, plus `:<steamid>`)
  - `type Accounts interface` on `api.Server` (field `Accounts Accounts`), the subset of Task 5 methods the API calls; nil = user logins off (web tokens get 503 "logins are not configured: set serve.web_url").
  - `authorize`: a `web` token with `X-Dzo-Session` resolves session → person (disabled, or no effective role anywhere → 403; unknown/expired session → 401 `session expired`); a `web` token without a session → 403 `this token needs a user session` on every non-login route; `X-Dzo-Actor` is ignored for `web` tokens; the audit actor is `<token>:<steamid>`.
  - Instance routes check `Can(instance, perm)`; non-instance routes check `CanAll(perm)` for installation-wide perms, else `CanAny(perm)`, and the handler then checks the instances it touches (U12): `createBan`/`updateBan`/`revokeBan` need the permission on every instance of the ban (global → `CanAll(PermBanGlobal)`); ban, evidence and player lists keep only records on instances where the permission holds; `seesIPs` and `geo` check `player.ip`/`ip.history` on the record's instance; records with no instance need `CanAny`. Markers and layers use `RoleOn(hub)`.

- [ ] **Step 1: Write failing tests** in `principal_test.go`: table over (token role, token instances, grants, instance) → effective role, including: viewer on `*` + operator on A → operator on A, viewer on B; admin grant with operator token → operator; token limited to A → nothing on B despite `*` grant; installation perms only with admin on `*` and an unlimited admin token; plain token unchanged from today. In `api_test.go` (through the HTTP handler): `TestWebTokenNeedsSession`, `TestSessionExpired401`, `TestDisabledPerson403`, `TestModeratorOnANotOnB` (ban scoped to B → 403, ban list shows only A's bans, player detail hides B's sessions), `TestIPsPerInstance`, `TestGlobalBanNeedsAdminStar`, `TestGrantForRemovedInstance` (no panic, instance not listed), `TestDelegateTokenUnchanged` (old `X-Dzo-Actor` behaviour).
- [ ] **Step 2: Run** `go test ./internal/api/` → FAIL.
- [ ] **Step 3: Implement** `principal.go`; replace every `c.who.Token.Role` and `who.Allowed` use listed in Files; `Token.Instances` checks become `Principal` methods. List every non-instance route and the rule it got in `docs/api.rst` (one table).
- [ ] **Step 4: Run** → PASS; `make check`; `make e2e`.
- [ ] **Step 5: Run** `make test-client` and `make test-client ARGS=--exp` (player and vehicle actions are touched) → both pass.
- [ ] **Step 6: Commit** `api: per-instance permissions and web tokens acting for a user session` (message states that test-client passed on stable and exp).

### Task 7: Login endpoints

**Files:**
- Create: `internal/api/login.go`, `internal/api/login_test.go`
- Modify: `internal/api/server.go` (routes, `Login` settings), `internal/admin/audit.go` (`Client string \`json:"client,omitempty"\``), `internal/serve/serve.go` (settings from config; `PruneLogins` in the per-minute upkeep)
- Docs: `docs/api.rst` (login endpoints)

**Interfaces:**
- Consumes: Tasks 4–6.
- Produces:
  - `type LoginSettings struct { WebURL string; SessionIdle, SessionMax time.Duration; Steam auth.Steam }` as field `Login` on `api.Server`
  - Login routes (only `web` tokens, no session; client address from `X-Dzo-Client`, only from `web` tokens, else the connection):
    - `POST /api/v1/auth/password` `{"username","password"}` → `200 {"pending":"dzp_…","factors":["totp","passkey","recovery"],"enrol":false}`
    - `GET /api/v1/auth/steam?return_to=<url under web_url>` → `{"url": "<steam redirect>"}`; `POST /api/v1/auth/steam` `{"params":{…openid fields…},"return_to":"…"}` → same as password
    - `POST /api/v1/auth/totp` `{"pending","code"}`, `POST /api/v1/auth/recovery` `{"pending","code"}` → `200 {"session":"dzs_…","expires":"…"}`
    - `POST /api/v1/auth/logout` (with session) → 204
  - A person without a second factor gets `enrol:true` after Steam and only the enrol endpoints of Task 8; a password login for a person without a second factor is impossible by construction (a password can only be set from the account page).
  - Logging: one line per step: `login: <step> <ok|failed|refused> steamid=<id|unknown> client=<addr> proxy=<addr|->`. Audit: `auth.login` and `auth.logout` entries with the client.

- [ ] **Step 1: Write failing tests**: `TestPasswordThenTOTP` (session works on `GET /api/v1`), `TestPasswordAloneGivesNoSession`, `TestTOTPReplayRefused`, `TestRecoveryCodeOnce`, `TestSteamLoginKnownPerson`, `TestSteamLoginUnknownNoRow` (no `people` row created), `TestSteamNonceReplay`, `TestSteamReturnToOutsideWebURL`, `TestPendingExpires` (fake clock, 5 min), `TestBackoff429` (6th failure → 429 with `Retry-After`; same body for unknown and known usernames), `TestClientHeaderOnlyFromWebToken`, `TestLoginLogHasNoSecrets` (capture `Logf`, assert no password, code, `dzs_`, `dzp_` substrings), `TestLogout`, `TestLoginsOffWithoutWebURL` (503).
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement**.
- [ ] **Step 4: Run** → PASS; `make check`.
- [ ] **Step 5: Commit** `api: login with password or Steam and a second factor`.

### Task 8: Passkeys, enrolment and the account endpoints

**Files:**
- Create: `internal/api/passkeys.go`, `internal/api/account.go`, tests
- Modify: `go.mod` (`github.com/go-webauthn/webauthn`), `internal/api/server.go` (routes)
- Docs: `docs/api.rst`

**Interfaces:**
- Consumes: Tasks 4–7.
- Produces (WebAuthn: RP ID = host of `web_url`, origin = `web_url`'s scheme+host, display name "dzo <installation>", user verification `preferred`, credentials listed in `allowCredentials`; ceremony data in `PendingLogin.Ceremony`):
  - Login: `POST /api/v1/auth/passkey/begin` `{"pending"}` → WebAuthn request options JSON; `POST /api/v1/auth/passkey/finish` `{"pending","response":{…}}` → session.
  - Enrolment (pending with `enrol:true`): `POST /api/v1/auth/enrol/totp/begin` `{"pending"}` → `{"secret":"<base32>","uri":"otpauth://…"}`; `…/totp/finish` `{"pending","code"}`; `…/enrol/passkey/begin`, `…/passkey/finish` `{"pending","response","name"}`. Each finish → `{"session","expires","recovery_codes":[10]}`.
  - Account (session): `GET /api/v1/account` → `{"steam_id","name","username","has_password","totp":bool,"passkeys":[{"id","name","created","last_used"}],"recovery_left":n}`; `PUT /api/v1/account/password` `{"username","current","new"}` (`current` required when a password exists; `ErrUsernameTaken` → 409); `DELETE /api/v1/account/password`; `POST /api/v1/account/totp/begin|finish`; `DELETE /api/v1/account/totp`; `POST /api/v1/account/passkeys/begin|finish`; `DELETE /api/v1/account/passkeys/{id}`; `POST /api/v1/account/recovery` → new codes; `DELETE /api/v1/account/sessions` (all others). Removing the last TOTP/passkey → 409 `the last second factor cannot be removed`.

- [ ] **Step 1: Write failing tests**: with a software authenticator in the test (go-webauthn's `protocol` types plus `crypto/ecdsa` to sign the assertion; a helper in `passkeys_test.go`): `TestEnrolPasskeyThenLogin`, `TestPasskeyWrongOrigin`, `TestPasskeyCounterUpdated`, `TestEnrolTOTPGivesRecoveryCodes`, `TestEnrolOnlyWhenPendingSaysSo`, `TestAccountPasswordNeedsCurrent`, `TestUsernameTaken409`, `TestLastSecondFactorKept`, `TestLogoutOtherSessions`.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement**; the webauthn `User` adapter wraps `moderation.Person` + its passkeys (`Passkey.Data` is the JSON of `webauthn.Credential`).
- [ ] **Step 4: Run** → PASS; `make licenses` (the dependency's tree must classify); `make check`.
- [ ] **Step 5: Commit** `api: passkeys, second-factor enrolment and account settings` (message justifies go-webauthn: WebAuthn verification is not something to write by hand).

### Task 9: User management, `Info.User` and stream re-checks

**Files:**
- Create: `internal/api/users.go`, `internal/api/users_test.go`
- Modify: `internal/api/server.go` (`Info`, `stream`), docs `docs/api.rst`

**Interfaces:**
- Consumes: Tasks 5, 6.
- Produces:
  - `Info.User *UserInfo \`json:"user,omitempty"\``; `type UserInfo struct { SteamID, Name string; Roles map[string]Role; Installation []string }` (`Roles` per instance, effective; `Installation` the installation-wide perms held)
  - Behind `users.manage`: `GET /api/v1/users` → `[]Person`; `PUT /api/v1/users/{steamid}` `{"name","disabled","grants":[{"instance","role"}]}` (unknown role or instance name not matching `^[A-Za-z0-9_.-]{1,64}$|^\*$` → 400); `DELETE /api/v1/users/{steamid}`; `POST /api/v1/users/{steamid}/reset`. On their own Steam ID: any change of grants (up or down), disabling, deleting or resetting → 409 `you cannot change your own access; another admin or dzo user on the serve host can`. Changing only their own display name is allowed. Each change: audit `users.put|delete|reset` with the target Steam ID and the grants as detail.
  - `Server.Recheck time.Duration` (0 = 60 s); the stream loop re-resolves the principal on each tick and returns when the session is gone or `RoleOn(instance)` has lost `view`.

- [ ] **Step 1: Write failing tests**: `TestUsersNeedAdminStar`, `TestPutUserValidates`, `TestCannotDemoteSelf`, `TestCannotPromoteSelf` (an admin on `*` adding a grant on an instance for themselves → 409), `TestCannotDisableDeleteResetSelf`, `TestResetEndsSessions`, `TestInfoReportsRolesPerInstance`, `TestStreamClosesAfterRevoke` (Recheck 10 ms via the field, revoke grant, `require.Eventually` the stream ends), `TestStreamClosesAfterLogout`.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement**.
- [ ] **Step 4: Run** → PASS; `make check`.
- [ ] **Step 5: Commit** `api: user management, grants in /api/v1 and re-checked streams`.

### Task 10: `dzo user` on the command line

**Files:**
- Create: `cmd/dzo/user.go`, `cmd/dzo/user_test.go`
- Modify: `cmd/dzo/root.go` (register), docs `docs/cli.rst`, `docs/web.rst` (first admin)

**Interfaces:**
- Consumes: Task 5 store methods via `moderation.Open(ctx, serve.BanDB(cfg))`.
- Produces: `dzo user list`; `dzo user grant <steamid> --role <r> [--instance <n>…] [--name <s>]` (no `--instance` = `*`; adds or replaces the grant on those instances, creates the person); `dzo user revoke <steamid> [--instance <n>…]` (no flag = every grant); `dzo user disable|enable|delete <steamid>`; `dzo user reset <steamid|username>`. Steam ID validated as 17 digits starting `7656119`. Each prints what changed; writes an audit entry with source `cli` and actor `cli:$USER`.

- [ ] **Step 1: Write failing tests** (config and database under `t.TempDir()`): `TestUserGrantCreatesPerson`, `TestUserGrantStarDefault`, `TestUserRevokeOneInstance`, `TestUserResetByUsername`, `TestUserBadSteamID`, `TestUserListFormat`.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement**.
- [ ] **Step 4: Run** → PASS; `make docs`; `make check`.
- [ ] **Step 5: Commit** `cli: dzo user to manage people and grants on the serve host`.

### Task 11: API client for logins, account and users

**Files:**
- Create: `internal/apiclient/auth.go`, `internal/apiclient/users.go`, tests
- Modify: `internal/apiclient/client.go:26-75`

**Interfaces:**
- Consumes: Tasks 7–9 endpoints.
- Produces: `Client.Session, Client.ClientAddr string` sent as `X-Dzo-Session`, `X-Dzo-Client`; `func (c *Client) ForUser(session, clientAddr string) *Client` (replaces `WithActor` for the web UI; `WithActor` stays for other delegate tools); one method per endpoint, named after it: `LoginPassword`, `SteamURL`, `LoginSteam`, `LoginTOTP`, `LoginRecovery`, `PasskeyLoginBegin`, `PasskeyLoginFinish`, `EnrolTOTPBegin`, `EnrolTOTPFinish`, `EnrolPasskeyBegin`, `EnrolPasskeyFinish`, `Logout`, `Account`, `SetPassword`, `RemovePassword`, `TOTPBegin`, `TOTPFinish`, `RemoveTOTP`, `PasskeyBegin`, `PasskeyFinish`, `RemovePasskey`, `NewRecoveryCodes`, `LogoutOthers`, `Users`, `PutUser`, `DeleteUser`, `ResetUser`. WebAuthn options and responses pass through as `json.RawMessage`. Types `Pending`, `SessionGrant`, `Account` mirror the JSON above; `api.UserInfo`, `moderation.Person` reused.

- [ ] **Step 1: Write failing tests** against `httptest` (the real `api.Server` with an in-memory setup as the existing client tests do): headers sent, each method round-trips, 429 surfaces as `*Error{Status:429}`.
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement.** **Step 4: Run** → PASS.
- [ ] **Step 5: Commit** `apiclient: login, account and user management calls`.

### Task 12: Web login, sessions and per-session CSRF

**Files:**
- Create: `internal/web/auth.go`, `internal/web/auth_test.go`, `internal/web/templates/login.html`, `internal/web/static/passkey.js`
- Modify: `internal/web/server.go` (middleware, `client()`, `csrfOK`, `page`), `cmd/dzo/web.go` (trusted proxies)
- Docs: `docs/web.rst` (replace the "Access control" warning by the login section)

**Interfaces:**
- Consumes: Task 11; `auth.Proxies` (Task 4).
- Produces:
  - Cookies: session `__Host-dzo_session` (or `dzo_session` on plain-HTTP loopback), `HttpOnly`, `Secure` when https, `SameSite=Strict`, `Path=/`, `Max-Age` = session max; login state `dzo_login` (pending id + Steam `state`), `HttpOnly`, `SameSite=Lax`, 5 min.
  - Routes: `GET /login`, `POST /login` (password), `GET /login/steam` (sets `state`, redirects), `GET /login/steam/return` (checks `state`, relays, answers 200 with a meta-refresh page to the next step), `GET|POST /login/second` (TOTP / recovery form, passkey button), `POST /login/passkey/begin|finish` (JSON for `passkey.js`), `GET|POST /login/enrol` (TOTP QR + passkey, then recovery codes once), `POST /logout`.
  - Every other route except `/static/`, `/vendor/`, `/docs/`, `/flags/` requires a session: no cookie → 303 to `/login?next=<path>` (only local paths accepted for `next`); API 401 → cookie cleared, same redirect; API 403 → 403 page.
  - `func (s *Server) client(r *http.Request) *apiclient.Client` = backend client `.ForUser(session, clientAddr)`.
  - CSRF: `hex(HMAC-SHA256(session secret, "dzo csrf"))` for logged-in pages; on login forms a random token in `dzo_login`; `csrfOK` compares in constant time plus the existing Origin check. CSP unchanged (`passkey.js` is `'self'`).

- [ ] **Step 1: Write failing tests** (web + real `api.Server` + fake Steam, as `web_test.go` sets up): `TestRedirectsToLogin`, `TestNextOnlyLocal` (`next=https://evil` → `/`), `TestPasswordTOTPLogin`, `TestSteamReturnNeedsState` (missing/wrong `state` → 400, no session), `TestSteamReturnSetsSessionViaRefreshPage`, `TestCSRFPerSession` (token of session A refused for B), `TestExpiredSessionRedirects`, `TestLogoutClearsCookie`, `TestClientAddrForwarded` (trusted proxy XFF reaches the audit entry's `client`).
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement.** **Step 4: Run** → PASS; `make check`.
- [ ] **Step 5: Commit** `web: login with password or Steam and a second factor, sessions, per-session CSRF`.

### Task 13: Account and users pages, buttons by role

**Files:**
- Create: `internal/web/account.go`, `internal/web/users.go`, templates `account.html`, `users.html`, tests
- Modify: `internal/web/handlers.go:55-80,265-335`, `events.go:140`, `bans.go:78`, `players.go:395-400`, `layout.html` (account/users/logout links; instance switcher shows only granted instances), `cmd/dzo/web.go` (QR dependency)
- Docs: `docs/web.rst` (account page, users page, roles per instance)

**Interfaces:**
- Consumes: Tasks 11, 12; `Info.User` (Task 9).
- Produces: `GET /account` and its POST actions (password, TOTP setup with QR as inline SVG or PNG data URL, passkeys via `passkey.js`, recovery codes, log out others); `GET /users`, `POST /users/{steamid}`, `POST /users/{steamid}/delete|reset` (shown only when `Info.User.Installation` holds `users.manage`). Views read `Info.User.Roles[instance]` instead of `info.Role`; `info.Role` remains the fallback for non-user tokens in tests.

- [ ] **Step 1: Write failing tests**: `TestViewerSeesNoActionButtons` (instance A), `TestOperatorOnASeesButtonsOnlyOnA`, `TestUsersPageHiddenWithoutManage`, `TestUsersPagePutsGrants`, `TestAccountRemoveLastFactorShowsError`, `TestAccountTOTPSetupShowsQR`.
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement.** **Step 4: Run** → PASS; `make licenses` (QR); `make check`.
- [ ] **Step 5: Commit** `web: account and users pages; controls follow the role per instance`.

### Task 14: Browser end-to-end, migration notes, packaging

**Files:**
- Create: `internal/web/e2e_login_test.go`
- Modify: existing e2e helpers in `internal/web/e2e_test.go` (log in a fixture user before each page test), `docs/migration.rst`, `docs/quickstart.rst`, `docs/web.rst` (setup block: `dzo token create web --role admin --web`, `dzo user grant`), `debian/changelog`, spec status line

- [ ] **Step 1: Write the e2e tests** (chromedp): `TestE2EPasswordTOTPLogin`; `TestE2ESteamLogin` (fake Steam on `httptest`); `TestE2EPasskeyEnrolAndLogin` using `cdproto/webauthn` `Enable` + `AddVirtualAuthenticator` (ctap2, internal, resident key, user verification); `TestE2EViewerCannotKick` (no button; a forged POST gets the API's 403); `TestE2ELogout`.
- [ ] **Step 2: Run** `make e2e` → new tests FAIL until the helpers log in, then PASS.
- [ ] **Step 3: Docs**: migration steps from the spec's Migration section (refused keys, new token, first admin, one web UI per installation), quickstart updated, changelog entry naming the breaking changes. `make docs`.
- [ ] **Step 4: Verify** `make check`; `dpkg-buildpackage -us -uc -b` and lintian as the `deb` job does (unit file changed in Task 3; changelog here).
- [ ] **Step 5: Commit** `docs: migration to web logins; e2e tests for password, Steam and passkey logins`; set the spec's status to implemented in the same commit.

---

## Self-review notes

- Spec coverage: U1 T5; U2 T3; U3/U8 T7–T8; U4 T4, T7, T8; U5 T5; U6 T6; U7 T7, T11, T12; U9 T4, T7; U10 T8; U11 T5, T12; U12 T6; U13 T9, T13; U14 T9, T13; U15 T9; U16 T2; U17 T10; U18 T4, T7, T12; U19 T6, T7; U20 T4, T8, T13; U21 T1–T3; Migration T2, T3, T14; Later section: Global Constraints + T5.
- My calls beyond the spec, for review: `serve.login.session_idle_hours` / `session_max_days` key names; `/b/{installation}/` URLs kept; `dzo user` writes the database directly; the meta-refresh step after Steam; self-protection (no change of one's own grants, up or down, nor disabling, deleting or resetting oneself) enforced in the API, not only the web UI; TOTP secrets stored unencrypted in the 0600 database; audit client addresses kept as long as the audit log (retention left open in the spec discussion).
