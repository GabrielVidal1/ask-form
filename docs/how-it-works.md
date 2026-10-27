# How ask-form works, and how it keeps forms private

## How it fits together

```
 your browser ──HTTPS /f/…─────▶ ask-form-relay ◀──long-poll (dials out)── ask-form serve ◀── ask-form create/wait (Claude)
                                  (public, stateless)                          (your machine: forms + answers on disk)
```

- **`ask-form serve`** runs once per machine. It keeps the forms on disk:
  `forms/<id>.json` is written once and never changed; `responses/<id>.jsonl`
  is append-only. It serves the answer pages and a loopback admin API, which
  the client commands reach with their own admin secret (never a form token).
- **`ask-form-relay`** is the public face. It serves a landing page at `/`
  and forwards `/f/*` to the machine that created each form. Machines dial
  *out* to it and long-poll for requests, so nothing listens on them. Several
  machines can share one relay, and each form id is claimed by one machine.
  The relay stores only the SHA-256 hashes of the machines' tokens.
- No relay at all also works: point a reverse proxy or `cloudflared` at
  `serve --listen` and set `--public-url`, or `ask-form setup --local` to
  answer on the same machine only.

## Relay access

A relay runs with `--register closed` (the default) or `--register open`.

- **closed**: only machines whose token the relay's owner minted with
  `ask-form-relay machine add <name>` can connect. That token goes in the
  machine's `~/.config/ask-form/relay-token`, or `ask-form setup --token`.
- **open**: `ask-form setup` registers the machine itself
  (`POST /_relay/v1/register`) and gets a token of its own. Such a machine:
  - is limited to `--max-forms` open forms (50),
  - sees only itself in the relay's health,
  - is forgotten after `--forget-after` without a poll (90 days),
  - can delete itself (`ask-form uninstall --purge`).
  Registrations are capped per address per day (`--register-per-ip`, 5) and
  in total (`--max-machines`, 1000). The machine API must then be reachable
  from the internet; `--machine-listen ""` serves it on the public port.

## Upgrades

Releases are GitHub releases: one binary per platform and a
`checksums.txt`. `ask-form upgrade` and the auto-upgrade in `serve`:

1. read the latest tag from the `/releases/latest` redirect (no API call),
2. download the binary and `checksums.txt`, and compare the SHA-256,
3. run the new binary's `version` and check it reports the expected tag,
4. rename it over the running one (same directory, so the swap is atomic),
5. re-exec `serve` in place (same PID, so systemd and launchd don't notice).

`serve --auto-upgrade` (or `ASK_FORM_AUTO_UPGRADE`): `all` (default: every
release), `minor` (minor and patch releases only; a new major waits for
`ask-form upgrade`), `notify` (log it and show it in `ask-form status`), `off`. It checks a few minutes after start and then
every 6 hours. Dev builds and pre-releases never upgrade by themselves.
Each release also carries a build provenance attestation:
`gh attestation verify ask-form-linux-amd64 -R GabrielVidal1/ask-form`.

## Security

- **Tokens**: 32 random bytes, base64url. Only the SHA-256 is stored, and it's
  compared in constant time.
- **First visit**: the token is traded for an `HttpOnly; Secure; SameSite=Lax`
  cookie scoped to `/f/<id>`, followed by a 302 to the clean URL.
- **One answer, then closed**: the first submission closes the form. It
  expires after 24 h by default.
- **Identical 404s** for an unknown form and for a bad or missing token, with
  the same work done in both cases.
- **Headers** on every response: `Referrer-Policy: no-referrer`,
  `Cache-Control: no-store`, `X-Robots-Tag: noindex, nofollow`, HSTS, and a
  strict CSP (same-origin script and styles plus a per-page nonce'd
  `<style>`). No third-party assets.
- **html blocks and mockups** are separate sandboxed documents with their own
  CSP. Inline styles are allowed; scripts are not, except our hash-pinned
  sizing beacon.
- **Logs** never contain a query string.
- **Submissions**:
  - per-IP limits on failed token attempts, on submissions, and overall;
  - a 64 KB body cap (`http.MaxBytesReader`, at the relay and at the machine);
  - strict server-side validation;
  - a honeypot field;
  - a CSRF MAC plus `Sec-Fetch-Site`/`Origin` checks.
  Everything is escaped by `html/template` or by the markdown renderer.
- **Secrets**: `secret` fields stay out of the answers. They're written with
  `ask-form secret … --env` and purged 24 h after the submit.
- **Hardening**: timeouts on every listener. The admin API is loopback-only
  and refuses browser requests. The relay runs as a non-root static binary on
  `scratch`, read-only, with no capabilities.
