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
- **with a registration key**, open or closed: a machine that presents one
  (`ask-form start --key`) registers past the per-IP cap, up to
  `--register-per-key` a day (20), and is forgotten after
  `--forget-ephemeral` unseen (48 h). That is Claude's code sandbox, next
  section. Keys come from the relay's owner (`ask-form-relay key add`), from a
  connected machine for itself (`ask-form skill export --claude-ai`), or, on
  an open relay, from the landing page's download button (3 a day per
  address). The relay keeps their SHA-256 only, in `keys.json`.

Behind a Cloudflare-proxied domain, `--trust-cloudflare` takes the visitor
from `Cf-Connecting-Ip`, but only when the request comes from one of
Cloudflare's edge ranges, so the per-IP limits count real visitors and a
forged header on another route is ignored. `--public-url` is the origin the
relay announces: machines set up without an explicit one use it for their
links, and the landing page, `/install.sh` and `/agent-install` name it.
Setting one up, with proxy configs: [running your own relay](self-hosting.md).

## In Claude's code sandbox

claude.ai and the Claude apps run code in a sandbox: a container for the
chat, whose network reaches package managers and the domains its settings
allow, and which goes away after a while. ask-form runs there for one chat:

1. The user uploads a skill once (Customize → Skills): the landing page's
   *Download the skill*, or `ask-form skill export --claude-ai` on a computer
   with ask-form. It is a zip with `ask-form/SKILL.md`, which tells Claude how
   to run ask-form there, `reference.md` (the field types), and a
   registration key.
2. Claude installs ask-form from npm (`npm install -g ask-form`: the launcher
   plus the Go binary for the sandbox's platform, the same build as the
   GitHub release) and runs `ask-form start --key …`. That registers an
   ephemeral machine (see above), starts `serve` as a detached process with
   no local listener (every link goes through the relay), no service and no
   self-upgrade, and returns once the relay connection is up. Run again, it
   does nothing while serve runs, and brings it back after the sandbox was
   paused.
3. Claude creates the form, puts the link in its reply, then waits in the
   same turn (`ask-form wait --timeout 4m`, three times). Unanswered by then,
   it ends its turn; the user says when they've answered and Claude reads the
   answers with `ask-form get`.

The sandbox must reach the relay: its settings (Settings → Capabilities →
Code execution → "Allow network egress", set to package managers and specific
domains) list the relay's host; on a Team or Enterprise plan the organization
owner sets them. The forms and answers live in the sandbox, as on any machine:
once it is gone, so are they, and a link then shows "offline". Secret fields
don't fit there (the value would land in the chat), and the skill says so.

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
heading, light whatever the screen's theme, without the buttons. No question
or block (a diff, a mockup) splits across two pages unless it is taller than
one. What can't be clicked on paper is drawn to be
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

## Looking back at the form

Once a form takes no more answers from a browser, its link shows the thanks
(or closed) page: a recap of the answers, one line per question. A **View the
full form** button under it opens `/f/<id>/answers`: the whole form again,
read-only, with the answers in it — the mockups and pictures the respondent
chose from, a mockup full size on a tap. A declined, withdrawn or expired
form (or a multi form that closed before this browser answered) shows its
questions empty, a decline's note above them. A multi form shows the answer
this browser sent, never anyone else's. Still open: the address is the form.

It is a [snapshot](#snapshots) drawn live rather than the stored file: the
frames and pictures load from the form's links, as on the answer page, so it
weighs what that page did and goes through any relay; uploads show as
thumbnails while they are kept (7 days), then as their name and size. The
same cookie as the form's guards it, and it works with snapshots off.

## Snapshots

A form that stops taking answers (submitted, declined, revoked, expired, or a
multi form closed) gets a snapshot: one HTML page written to
`<data>/snapshots/<id>.html` once and kept forever, like the form itself.
It is the answer page drawn read-only: every question as the respondent saw
it (the ones a condition hid from them left out), with the answers filled
in. A declined, withdrawn or expired form shows its questions empty (a
decline's note, when the respondent left one, above them). A multi
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
allow-scripts` as a header — the script hashes the page itself pins, so a
snapshot an older version wrote keeps its mockups fitted after an upgrade
changed the frame script. The form's link never shows the file: the
respondent gets the [same page drawn live](#looking-back-at-the-form).

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

The same binaries go to npm with each release (`ask-form`, plus
`ask-form-<os>-<arch>` per platform, published with provenance). A binary npm
installed leaves upgrades to npm: `ask-form upgrade` says to run
`npm install -g ask-form@latest`, and serve's auto-upgrade defaults to
`notify` there.

## In another app's frame

`ask-form serve --embed-origins "https://hub.example.com …"` (or
`ASK_FORM_EMBED_ORIGINS` in `serve.env`; `ask-form setup --embed-origins …`
writes it, `-` clears it) lets those sites show this machine's answer pages
in an `<iframe>` — an agent hub opening the form over the conversation that
asked it. Without it no site may frame them (`frame-ancestors 'none'`,
`X-Frame-Options: DENY`). With it:

- the form's pages (the form, its thanks page, its messages, the answers
  view) send `frame-ancestors <the origins>` and no `X-Frame-Options`, which
  can't name a site; a mockup's or widget's frame sends `'self' <the
  origins>`, since `frame-ancestors` checks every ancestor;
- a page loading in a frame (`Sec-Fetch-Dest: iframe`) trades the token for
  a cookie that is `SameSite=None; Partitioned` (CHIPS): inside the frame
  every request is cross-site to the browser, so a Lax cookie never comes
  back, and the partition keeps the cookie to that embedder only. A browser
  that refuses even partitioned third-party cookies gets the 404 page in the
  frame: the embedder should offer the link in a new tab;
- that page's `<html data-embed>` lists the origins, and `app.js` posts the
  parent one message per page load:

  ```json
  {"type": "ask-form:page", "page": "form", "id": "project-setup_k3f9q2", "status": "pending"}
  ```

  `page` is `form`, `closed` (sent or declined: `status` says which), `answers`
  or `message`. The embedder closes its frame on the first `closed` after a
  `form`. It may answer with its theme, which the page applies over its own
  (and the machine's brand):

  ```json
  {"type": "ask-form:theme", "scheme": "dark", "tokens": {"--primary": "24 95% 55%", "--background": "0 0% 4%"}}
  ```

  Tokens are the custom properties of `theme.css` (`H S% L%` triplets,
  `--radius`); the page takes only `--name: value` pairs made of letters,
  digits, spaces and `.%#,-` — nothing that loads a file — and only from its
  parent, at one of the origins. In a frame the brand's logo line hides: the
  embedder's panel names the form. html blocks and mockups keep the machine's
  brand, not the embedder's tokens, but follow the scheme: the page tells each
  of them the colour behind it, and one whose text would be unreadable there
  (a mockup written with light-page colours, or a brand with no dark palette,
  on a dark page) is drawn on a light sheet of its own.

## Security

- **Tokens**: 32 random bytes, base64url. Only the SHA-256 is stored, and it's
  compared in constant time.
- **First visit**: the token is traded for an `HttpOnly; Secure; SameSite=Lax`
  cookie scoped to `/f/<id>`, followed by a 302 to the clean URL (in the
  frame of an `--embed-origins` site: `SameSite=None; Partitioned`, see
  above). A `--multi`
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
- **Uploads** (`file` fields, see [uploads](uploads.md), and the files
  attached to a textarea or an "Other…" answer: any type, 10 MB each, 5 per
  box): chunked through the relay (1 MiB, paced per visitor), sniffed against
  `accept`, hashed, stored 0600 on the machine and never served back; not in a
  `--multi` form; 50 MB and 20 files per form; purged when the form closes or
  expires (attachments of an "Other…" left unpicked go too), and 7 days after
  a submit.
- **Hardening**: timeouts on every listener. The admin API is loopback-only
  and refuses browser requests. The relay runs as a non-root static binary on
  `scratch`, read-only, with no capabilities.
