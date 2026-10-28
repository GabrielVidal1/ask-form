---
name: ask-form
description: Ask the user structured questions through a private web form they can answer from any browser, phone included — text, choices, picture or mockup choices, sliders, dates, emails, secrets — and get the answers back as JSON. Use when you need several decisions or values from the user at once, want them to compare options visually, need a credential without it passing through the chat, the user may be away from the terminal, or several people should answer the same form (a team vote, beta testers' feedback).
---

<!-- Installed by `ask-form setup`; `ask-form upgrade` rewrites this file. -->

# ask-form

`ask-form` puts your questions in a private web form. You show the user a
link, they answer in a browser, you read the answers as JSON.

## Ask

```bash
# 1. Write the field spec (a JSON array) to a file, then create the form.
#    It returns at once. Show the user the "url": it holds the form's token,
#    so don't log it anywhere else.
ask-form create --title "Project setup" --description "Two choices before I scaffold." \
  --fields-file /tmp/spec.json [--asset shot.png]… [--ttl 24h] [--public] [--open]

# 2. Wait for the answer IN THE BACKGROUND (a background shell task, or under a
#    monitor). You are re-invoked when it exits; keep working meanwhile.
ask-form wait <id> --timeout 30m
```

Exit codes of `wait`: 0 submitted · 3 declined/revoked/expired · 4 timed out
(the form stays open: wait again, or `ask-form revoke <id>`).

The `url` opens on this computer (no relay in between). `publicUrl` is the
same form from any device: give that one when the user is away from this
computer or asks for their phone, or pass `--public` to make it the `url`.

**On the user's desktop, pass `--open`**: the form opens in their default
browser right away (`"opened": true`); tell them it's there and still show the
url. It opens nothing over SSH or on a machine without a display
(`"opened": false`): show the url as usual. Leave it off for a `--multi` form,
which is for other people to answer.

## Many people, one form

When several people should answer (a team vote, a client's sign-off, beta
testers), add `--multi`: one link (the public one), any number of answers,
each kept apart. The user shares the link; the form takes answers until it
expires (set `--ttl`, e.g. `72h`), reaches `--max N`, or you run
`ask-form close <id>`.

```bash
ask-form create --title "Onboarding: which one?" --multi --ttl 72h [--max 20] [--hide-count] \
  --fields-file /tmp/vote.json
ask-form wait <id> --timeout 0     # in the background: returns once it closes, with every answer
ask-form get <id>                  # the answers so far, any time
```

- To know who said what, ask for it: an `email` field (optional unless you
  make it `required`) and/or a `text` name. Nothing is added for you.
- Secret fields are refused in a multi form, and there's no "decline".
- Respondents see how many have answered; `--hide-count` keeps it off.
- Each browser answers once and then sees its own answers; a private window
  can answer again, so treat counts as a poll, not a ballot.
- `wait` exits 0 when the form ended with at least one answer, 3 with none.

## Field spec

```json
[{"type":"markdown","text":"Context the user needs, **markdown**."},
 {"key":"name","label":"Project name","type":"text","required":true,"group":"Basics"},
 {"key":"stack","label":"Stack","type":"radio","required":true,"options":["Vite","Next",{"value":"astro","label":"Astro","description":"static first"}]},
 {"key":"features","label":"Features","type":"multiselect","options":["auth","db"]},
 {"key":"deploy","label":"Deploy after","type":"checkbox"},
 {"key":"polish","label":"Polish","type":"slider","min":1,"max":5,"default":3,"minLabel":"rough","maxLabel":"pixel-perfect"},
 {"key":"budget","label":"Budget (€)","type":"number","min":0},
 {"key":"when","label":"Deadline","type":"date"},
 {"key":"email","label":"Your email","type":"email"},
 {"key":"api_token","label":"API token","type":"secret"},
 {"key":"hero","label":"Hero image","type":"radio","options":[{"value":"a","label":"Fjord","image":"{{asset:a.jpg}}"},{"value":"b","label":"Peaks","html":"<div style=\"width:360px\">…</div>"}]},
 {"type":"html","label":"Current layout","html":"<svg …>…</svg>"}]
```

- Types: `text textarea number select multiselect radio checkbox slider date email secret widget`,
  plus the display blocks `markdown` and `html` (no scripts; inline styles are fine).
  A `widget` is a control you write yourself: see "Widgets" below.
- A slider's `minLabel` / `maxLabel` name its two ends (shown beside the numbers).
- Any field takes `required`, `default`, `help` and `group`. Past ~4 questions,
  give them `group`s: each group is one page.
- Every choice field gets an "Other…" free-text choice, and every form ends
  with an optional `anything_else` textarea. Don't add your own.
- Pictures only through `--asset <file>` plus `{{asset:<basename>}}`. Remote
  images are blocked.
- Picture and mockup choices show one card per row on a phone, two from 560 px
  (`columns` 1–6 changes that count, never the phone's). Draw a mockup at a
  fixed width (240–360 px): it is scaled to fill its card.
- Put your recommendation in `default` and say why in `help`, so the user can
  accept it in one tap.

## Widgets: a control you draw yourself

When no field type fits (drag items onto a grid, tap a mockup, tune a colour,
sketch), write the control as HTML plus script. What it reports is the answer.

```json
{"key":"spot","label":"Where should the button go?","type":"widget","schema":"object","required":true,"height":320,
 "html":"<div id=p style=\"height:300px\"></div><script>p.onclick=e=>askForm.setValue({x:e.offsetX,y:e.offsetY})</script>"}
```

- It runs in a locked frame: its own origin, **no network** (no fetch, no remote
  images, fonts or scripts: inline everything; pictures through `--asset` and
  `{{asset:<basename>}}` as in an html block), no access to the page.
- `askForm.setValue(v)`: report the answer, any JSON value, as often as it
  changes (the last call wins; `null` clears it). ≤ 16 KB.
- `askForm.onInit(cb)` / `askForm.value`: the answer already given (the person
  came back, or a send failed), `null` when none. Redraw your control from it.
- `askForm.setHeight(px)` and `askForm.theme` (`"light"`/`"dark"`); the html
  block's theme variables (`hsl(var(--primary))`, `--border`, `--muted`…) work.
- `schema` (`string number boolean object array`) makes the page refuse any
  other kind of value. `default` (JSON) is where the value starts. `required`
  blocks the send until the widget has reported a value.
- Build it for a thumb: pointer events, `touch-action:none` on what you drag,
  a fixed `height` when your layout has a fixed size (else it follows the content).
- Without JavaScript the person gets a plain textarea for the JSON.
- Allowed in `--multi` forms, in the same locked frame. Never put a secret in
  a widget: ask credentials with a `secret` field.

## Answers

`wait` prints one JSON object: `{"id","status","answers":{key: value},"closedAt",…}`.
A `--multi` form has no `answers`: it has `"answerCount":N` and
`"responses":[{"n":1,"at":"…","answers":{key: value}},…]`, oldest first.

- text, textarea, date, select, radio → string (an "Other…" answer is the user's own text)
- email → the address, trimmed and lowercased (checked to look like one)
- multiselect → array of strings · checkbox → bool · number, slider → number
- widget → the JSON value it reported, decoded (an object, a string, a number…)
- secret → `{"secret":true,"chars":N}`. The value never reaches your output.
  Write it where it's needed and never print or `cat` it:
  `ask-form secret <id> <key> --env .env VAR_NAME` (or `--file PATH`, or `--stdout | cmd`).
- Unanswered optional fields are absent.

## Other commands

| command | does |
|---|---|
| `ask-form get <id> [--fields]` | the form's current state |
| `ask-form list [--status pending]` | your forms |
| `ask-form revoke <id>` | close an open form |
| `ask-form close <id>` | a `--multi` form stops taking answers; the ones in stay |
| `ask-form status` | is the local server up, connected to its relay, up to date? |
| `ask-form upgrade` | install the latest release |

Every command prints JSON on stdout; an error is `{"error":"…"}` with exit 1.
If a command says the server is unreachable, run `ask-form setup` (it starts
the background service) or `ask-form serve`.

`ASK_FORM_NOTIFY_CMD` (or `--notify-cmd` on `create`) runs a shell command
after each `create`, with `ASK_FORM_ID`, `ASK_FORM_TITLE`, `ASK_FORM_URL` and
`ASK_FORM_EXPIRES_AT` in its environment. Use it to push the link to the
user's phone, e.g. `curl -d "$ASK_FORM_URL" ntfy.sh/<topic>`.
