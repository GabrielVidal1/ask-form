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

## Printing

Printing an answer page (or saving it as a PDF) gives the whole form on paper,
blank or half answered: every section one after the other with its title as a
heading, light whatever the screen's theme, without the buttons. A question
never splits across two pages. What can't be clicked on paper is drawn to be
filled by hand: a dropdown lists its options with a circle each, a slider its
values to circle (a line to mark past 11 of them), a list three empty rows,
long answers a taller box, and "Other…" its line. A question shown only after
some answer prints too, with when it applies: *Only if “Needs login” is Yes*.
Collapsed blocks print open, mockups and html blocks whole with their colours,
diffs wrapped. There is no print button: it is the browser's own Print / Save
as PDF. The print styles come from the relay's `app.css` and the paper-only
parts from the machine's page, so it takes a relay and a machine from v0.10;
with an older one either side, the page prints as it did before.

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
