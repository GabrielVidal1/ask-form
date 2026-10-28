# CLAUDE.md — ask-form

Web forms for agents: an agent creates a form, the user answers it in a
browser from anywhere (`https://ask-form.xyz/f/<name>_<hash>?token=…`), the
agent reads the answers as JSON. One Go binary (`ask-form`: `serve`, `setup`,
`upgrade` and the client commands) and a relay (`ask-form-relay`). Stdlib
only, Go 1.22+.

How an agent uses it: [`skill/SKILL.md`](skill/SKILL.md) (installed as the
Claude Code skill by `ask-form setup`; `ask-form skill print --body` for other
agents). How it works and why it's safe: [`docs/how-it-works.md`](docs/how-it-works.md).

## Layout

- `cmd/ask-form`: CLI, `serve`, `setup`/`uninstall` (service units, skill),
  `upgrade` and the auto-upgrade loop. `cmd/ask-form-relay`: the relay.
- `internal/form` (spec, answers, markdown), `internal/store` (files),
  `internal/server` (answer pages under `/f/`, admin API, security),
  `internal/tunnel` (relay ⇄ machine long-poll), `internal/relay` (public
  side, machine API, open registration in `registry.go`), `internal/update`
  (releases: latest tag, download, checksum, atomic swap), `internal/webhook`
  (`serve --events-url`: every form event POSTed to one URL — the ai-agent
  hub's feed; `Server.Events`, `AnnounceExpiries`).
- `web/` (templates, CSS, JS, embedded), `landing/` (the relay's `/` and
  `/agent-install`; `{{ORIGIN}}` and `{{HOST}}` are filled in by the relay),
  `install.sh`
  (root, embedded via `assets.go`, served at `/install.sh`), `skill/`,
  `examples/` (demo forms served at `/examples`).

## Working on it

- `make test` (vet + tests), `make build`, `make install` (→ `~/go/bin`),
  `make dist` (every release binary + `checksums.txt` into `dist/`).
- CI (`.github/workflows/ci.yml`): gofmt, vet, `go test -race`, `make dist`.
  Releasing: push a `vX.Y.Z` tag; `release.yml` builds, attests and publishes
  the GitHub release that `install.sh`, `ask-form upgrade` and the
  auto-upgrade read. Asset names (`ask-form-<os>-<arch>`, `checksums.txt`) are
  a contract with already-installed binaries: don't rename them.
- The answer page must work with JavaScript off; `web/static/app.js` only
  adds. Strict CSP: no inline script or style (per-page CSS goes in the
  nonce'd `<style>`), no third-party assets. Never log a query string.
- html blocks and mockups stay script-free (frame CSP with one pinned hash). A
  `widget` field's frame is the single exception (`internal/server/widget.go`:
  `script-src 'unsafe-inline'`, still no network); don't widen it, and keep
  `widget.js` accepting messages only from the field's own iframe.
- `/forms/` is the old prefix: it must keep answering (links already sent).
- `examples/<name>/spec.json` (+ images): adding one = a new folder plus a
  relay rebuild; `internal/server/examples_test.go` checks each renders.
- The landing page is a React + Vite app in `landing/app/` (GSAP, lucide,
  `@gabvdl/ui` from public npm for `PhonePreview`/`ProgressiveText`). Its
  build in `landing/dist/` is **committed**, so the relay and CI build with Go
  alone: after editing `app/`, run `npm ci && npm run build` there and commit
  `dist/` with it. The build prerenders the page (it reads without JS) and the
  relay serves `dist/assets/*` at `/assets/` under a `script-src 'self'` CSP.
  The demo's examples (`app/src/examples/data.tsx`) mirror `examples/`.
- `ASK_FORM_BENCH=1 go test -run TestTunnelLatency -v ./internal/relay`
  prints the tunnel latency CSV.
- Testing upgrades without GitHub: `ASK_FORM_UPDATE_URL` points the updater
  and `install.sh` at any server laid out like a GitHub repo's releases.
