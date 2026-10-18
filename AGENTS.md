<!--
SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
SPDX-License-Identifier: AGPL-3.0-or-later
-->

# AGENTS.md

Rules for human and coding-agent contributors. `IMPLEMENTATION_PLAN.md` holds
the design; this file holds the working rules.

## Before pushing

Run `make check`. It must pass, because CI runs the same steps and a push that
fails them is wasted:

| Step | Target | Notes |
|---|---|---|
| formatting | `make fmt-check` | fix with `make fmt` (gofmt + goimports) |
| vet | `make vet` | |
| lint | `make lint` | golangci-lint **v2.14.0**, the version CI pins; v1 cannot read `.golangci.yml` |
| licences | `make reuse` | every new file needs an SPDX header (or a `REUSE.toml` entry) |
| dependency licences | `make licenses` | go-licenses against the AGPL-compatible allow-list; run it after any `go.mod` change, a new dependency can pull in a library it cannot classify |
| vulnerabilities | `make vulncheck` | `govulncheck ./...` |
| tests | `make test-race` | race detector; `make cover` is the quick variant |
| coverage | `make coverage-check` | **total coverage must stay at or above 85%** |

Also, when the change touches them:

- `docs/` or CLI help: `make docs` (Sphinx, warnings are errors).
- `debian/`, the unit files or the packaging: `dpkg-buildpackage -us -uc -b`
  and lintian, as the `deb` CI job does (debian:trixie).
- Btrfs snapshot and backup tests skip without a btrfs filesystem. Point
  `DZO_BTRFS_TESTDIR` at a writable btrfs mount to run them, as CI does.

## Coverage

- Keep total coverage at or above 85% (`COVERAGE_MIN` in the `Makefile`). Never
  lower the threshold or exclude code to reach it.
- Write the test first. A new feature or bug fix comes with a test that fails
  without it.
- Test through the public API of a package. Keep tests deterministic: no
  `time.Sleep` to wait for something, use `require.Eventually` or a fake clock.
- Tests write only under `t.TempDir()`. They never touch `$HOME`, the real
  `/srv`, or a running service.

## Formatting and linting

- Code is `gofmt` and `goimports` clean. Run `make fmt` before committing.
- `.golangci.yml` is authoritative (govet, staticcheck, errcheck, ineffassign,
  unused, gosec, bodyclose, misspell). Fix findings instead of suppressing them.
- If a suppression is unavoidable, write `//nolint:<linter> // <reason>` with
  one specific linter and a reason. Bare `//nolint` is not allowed.
- Do not bump the pinned golangci-lint version in `ci.yml` incidentally. Verify
  a new release locally first, in its own commit.

## Safe Go

- Handle and wrap every error (`fmt.Errorf("...: %w", err)`). Ignoring one
  needs `_ =` and a comment saying why.
- No `panic` in request, serve, or background paths.
- Functions doing I/O take `context.Context` as the first argument. Goroutines
  stop when the context is cancelled and are waited for on shutdown.
- Guard shared state with a mutex or channels, and keep tests race-clean.
- Avoid package-level mutable state.
- Keep external dependencies few. Justify a new one in the commit message,
  and check its licence (`make licenses`).

## Security

- Treat everything from outside as untrusted: HTTP requests, RCON and BattlEye
  traffic, player names and chat, mission/mod files, `serverDZ.cfg`, workshop
  content, command-line and config values.
- Validate and bound input. Resolve user-supplied paths and refuse any that
  escape their base directory (`..`, absolute paths, symlinks).
- Never build shell commands from strings. Use `exec.Command` with an argument
  list. Never build SQL by concatenation; use query parameters.
- Escape everything rendered into HTML (use `html/template`, never write
  untrusted text into markup or into `template.HTML`). Protect state-changing
  web requests against CSRF.
- Range-check integer conversions (`gosec` G115).
- Create files with restrictive modes (`0o600` files, `0o750` directories)
  unless they must be shared, and write secrets atomically.
- Never log or expose secrets: RCON and admin passwords, tokens, Steam
  credentials, session keys. Redact them from errors, the web UI, and metrics.
- Compare secrets in constant time (`crypto/subtle`).
- Set explicit timeouts on every HTTP client and server, and on network and
  RCON connections.
- Run containers and processes unprivileged. dzo runs as the `dayz` service
  user and needs no root.

## Commits

- One logical change per commit, with a conventional prefix naming the area:
  `admin:`, `web:`, `docs:`, `fix:`, and so on, as in `git log`.
- Packaging changes add an entry to `debian/changelog`.
- Keep `docs/` in step with behaviour changes in the same commit.
