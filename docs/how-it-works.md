# How ask-form works, and how it keeps forms private

## How it fits together

```
 your browser ──HTTPS /f/…─────▶ ask-form-relay ◀──long-poll (dials out)── ask-form serve ◀── ask-form create/wait (Claude)
                                  (public, stateless)                          (your machine: forms + answers on disk)
```

- **`ask-form serve`** runs once per machine. It keeps the forms on disk:
  `forms/<id>.json` is written once and never changed; `responses/<id>.jsonl`
  is append-only; `snapshots/<id>.html` is the form as it closed
  ([snapshots](#snapshots)). It serves the answer pages and a loopback admin API, which
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

Behind a Cloudflare-proxied domain, `--trust-cloudflare` takes the visitor
from `Cf-Connecting-Ip`, but only when the request comes from one of
Cloudflare's edge ranges, so the per-IP limits count real visitors and a
forged header on another route is ignored. `--public-url` is the origin the
relay announces: machines set up without an explicit one use it for their
links, and the landing page, `/install.sh` and `/agent-install` name it.
Setting one up, with proxy configs: [running your own relay](self-hosting.md).

## Languages

A form's pages are in English or French: the buttons, the errors, the confirm
step, the thanks and closed pages, and what ask-form adds by itself ("Other…",
"Anything else?"). The agent writes the questions; `ask-form create --lang fr`
puts the rest in the same language. A form without `--lang` takes the
machine's default: `ask-form serve --lang fr`, or `ASK_FORM_LANG` in
`serve.env`, which `ask-form setup --lang fr` writes. With neither, it's
English. The language is fixed when the form is created: it is the form's
`lang` in `ask-form get`. The machine renders every string and hands the
page's script its share in the page itself. A relay older than v0.9 serves
scripts that ignore it, so the confirm step and the page counter stay in
English there. The examples are at `/examples` and `/fr/examples`.

## Events

`ask-form serve --events-url URL` (or `ASK_FORM_EVENTS_URL` in `serve.env`,
`ask-form setup --events-url URL` writes it) tells another program what
happens to the forms on this machine, without that program polling the
admin API. Every event is one `POST` with a JSON body:

```json
{"event": "created", "at": "2026-10-28T15:00:00.000Z", "version": "v0.5.0",
 "form": {"id": "project-setup_k3f9q2", "title": "Project setup", "status": "pending",
          "url": "http://127.0.0.1:7462/f/project-setup_k3f9q2?token=…",
          "publicUrl": "https://ask-form.xyz/f/project-setup_k3f9q2?token=…", "meta": {"session": "…"}, …}}
```

`form` is the form as `ask-form get` prints it. The events are `created`,
`submitted`, `declined`, `revoked`, `expired` and, for a multi form,
`answered` (each response) and `closed`. **Only `created` carries the links**:
the token is never stored, so the receiver must keep the link itself if it
wants to show it later. `expired` is announced by a once-a-minute check while
serve runs; forms that lapsed while it was down are not replayed.

Delivery is best effort: one request per event, in order, three tries with a
short backoff, a 10 s timeout, a queue of 256 while the receiver is down —
then the event is dropped and the drop logged (never the body). The receiver
should treat the stream as a hint and reconcile with `ask-form list` /
`ask-form get` when it matters. The [ai-agent](ai-agent-integration.md) hub
is the first receiver: it mirrors the forms, pushes the link to the phone
and lists open forms.

## Snapshots

A form that stops taking answers (submitted, declined, revoked, expired, or a
multi form closed) gets a snapshot: one HTML page written to
`<data>/snapshots/<id>.html` once and kept forever, like the form itself.
It is the answer page drawn read-only: every question as the respondent saw
it (the ones a condition hid from them left out), with the answers filled
in. A declined, withdrawn or expired form shows its questions empty. A multi
form shows its questions once, then each answer as a numbered, dated recap.
A line on top says how and when the form ended.

The page needs nothing else to open, now or in ten years: the stylesheets of
the version that wrote it, the machine's brand, the form's pictures and its
mockups and html blocks (as `srcdoc` frames) are all inside. Its own CSP
(in a `<meta>`, since the file travels without headers) loads nothing and
runs only two hash-pinned scripts: the one that sizes the frames and the
frames' own beacon. Secrets never go in it, only their length. A widget
shows the JSON it returned. An uploaded picture comes as a thumbnail (480 px
at most, scaled down when the file is a JPEG, PNG or GIF, inlined as it is
under 256 KB for WebP, AVIF and SVG), any other file as its name and size.
Uploads go 7 days after the submit, so the snapshot is where the pictures stay.

`serve` writes the snapshot as the form closes, before the event goes out,
so `submitted`, `declined`, `revoked` and `closed` events carry
`"snapshot": true`. Once a minute (and at start, which backfills every form
that closed before this version) it writes the missing ones: expiries,
forms closed while snapshots were off. Each form is tried once per run.

The admin API serves a snapshot at `GET /v1/forms/<id>/snapshot` (404 while
the form is open or when there is none), with the page's CSP plus `sandbox
allow-scripts` as a header. The form's link never shows it: the respondent
still sees the thanks page.

Turning them off: `serve --no-snapshots` or `ASK_FORM_SNAPSHOTS=off` (`ask-form
setup --no-snapshots` writes it to `serve.env`; `--snapshots` turns them back
on) for the machine, `create --no-snapshot` for one form. The snapshots
already taken stay readable.

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
  cookie scoped to `/f/<id>`, followed by a 302 to the clean URL. A `--multi`
  form keeps the token in the address bar instead (its link is meant to be
  shared): a link copied from there opens in any browser.
- **One answer, then closed**: the first submission closes the form. It
  expires after 24 h by default. A `--multi` form shares one link between
  everyone who should answer: it takes one answer per submission (up to
  `--max`, at most 1000) until it expires or is closed. It can't hold secret
  fields, and a signed cookie shows each browser its own answers, never
  anyone else's.
- **Identical 404s** for an unknown form and for a bad or missing token, with
  the same work done in both cases.
- **Headers** on every response: `Referrer-Policy: no-referrer`,
  `Cache-Control: no-store`, `X-Robots-Tag: noindex, nofollow`, HSTS, and a
  strict CSP (same-origin script and styles plus a per-page nonce'd
  `<style>`). No third-party assets.
- **Brands** (`ask-form brand set`) are the machine's own: its CSS goes in the
  page's nonce'd `<style>`, its logo and name in the header, so another
  machine's forms on the same relay never see them. The page policy stays
  the same apart from `font-src data:`: a brand's images and fonts are
  inlined as data: URIs when it is set, `@import` and remote `url()`s are
  refused up front, and so is `</` (it could close the `<style>`). 512 KB at
  most, all included.
- **html blocks and mockups** are separate sandboxed documents with their own
  CSP. Inline styles are allowed; scripts are not, except our hash-pinned
  sizing beacon.
- **widgets** are the one frame that runs the asking agent's own script. Same
  sandbox as an html block (`sandbox allow-scripts` alone, so an opaque origin
  with no cookie, no top navigation, no popups, no forms) and
  `default-src 'none'`, so the script can't fetch, open a socket or load
  anything remote; the page's `frame-src 'self'` keeps the frame from
  navigating elsewhere. The page takes a value only from the frame it put in
  that field, as JSON of at most 16 KB checked against the field's `schema`.
  The script is the agent's own, so treat a widget as code the agent chose to
  show: never put a secret in one.
- **Logs** never contain a query string.
- **Submissions**:
  - per-IP limits on failed token attempts, on submissions, and overall;
  - a 64 KB body cap (`http.MaxBytesReader`, at the relay and at the machine;
    1 MiB on the upload path only);
  - strict server-side validation;
  - a honeypot field;
  - a CSRF MAC plus `Sec-Fetch-Site`/`Origin` checks.
  Everything is escaped by `html/template` or by the markdown renderer.
- **Snapshots** live on the machine only (0600), in the admin API, never on
  the relay or behind a form link. They hold no secret, only its length, and
  load nothing: a strict CSP of their own, frames sandboxed, two pinned scripts.
- **Secrets**: `secret` fields stay out of the answers. They're written with
  `ask-form secret … --env` and purged 24 h after the submit.
- **Uploads** (`file` fields, see [uploads](uploads.md)): chunked through the
  relay (1 MiB, paced per visitor), sniffed against `accept`, hashed, stored
  0600 on the machine and never served back; not in a `--multi` form; 50 MB
  and 20 files per form; purged when the form closes or expires, and 7 days
  after a submit.
- **Hardening**: timeouts on every listener. The admin API is loopback-only
  and refuses browser requests. The relay runs as a non-root static binary on
  `scratch`, read-only, with no capabilities.
