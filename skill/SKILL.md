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
  --fields-file /tmp/spec.json [--asset shot.png]… [--ttl 24h]

# 2. Wait for the answer IN THE BACKGROUND (a background shell task, or under a
#    monitor). You are re-invoked when it exits; keep working meanwhile.
ask-form wait <id> --timeout 30m
```

Exit codes of `wait`: 0 submitted · 3 declined/revoked/expired · 4 timed out
(the form stays open: wait again, or `ask-form revoke <id>`).

## Many people, one form

When several people should answer (a team vote, a client's sign-off, beta
testers), add `--multi`: one link, any number of answers, each kept apart.
The user shares the link; the form takes answers until it expires (set
`--ttl`, e.g. `72h`), reaches `--max N`, or you run `ask-form close <id>`.

```bash
ask-form create --title "Onboarding: which one?" --multi --ttl 72h --fields-file /tmp/vote.json
ask-form wait <id> --timeout 0     # returns once it closes, with every answer
ask-form wait <id> --next          # or: returns at the next answer (still open)
ask-form wait <id> --after 4       # or: once it holds more than 4 answers
```

- To know who said what, ask for it: an `email` field (optional unless you
  make it `required`) and/or a `text` name. Nothing is added for you.
- Secret fields are refused in a multi form, and there's no "decline".
- Each browser answers once and then sees its own answers; a private window
  can answer again, so treat counts as a poll, not a ballot.
- `wait` exits 0 when new answers arrived (`--next`/`--after`) or when the
  form ended with at least one answer; 3 when it ended with none.

## Field spec

```json
[{"type":"markdown","text":"Context the user needs, **markdown**."},
 {"key":"name","label":"Project name","type":"text","required":true,"group":"Basics"},
 {"key":"stack","label":"Stack","type":"radio","required":true,"options":["Vite","Next",{"value":"astro","label":"Astro","description":"static first"}]},
 {"key":"features","label":"Features","type":"multiselect","options":["auth","db"]},
 {"key":"deploy","label":"Deploy after","type":"checkbox"},
 {"key":"polish","label":"Polish","type":"slider","min":1,"max":5,"default":3},
 {"key":"budget","label":"Budget (€)","type":"number","min":0},
 {"key":"when","label":"Deadline","type":"date"},
 {"key":"email","label":"Your email","type":"email"},
 {"key":"api_token","label":"API token","type":"secret"},
 {"key":"hero","label":"Hero image","type":"radio","options":[{"value":"a","label":"Fjord","image":"{{asset:a.jpg}}"},{"value":"b","label":"Peaks","html":"<div style=\"width:360px\">…</div>"}]},
 {"type":"html","label":"Current layout","html":"<svg …>…</svg>"}]
```

- Types: `text textarea number select multiselect radio checkbox slider date email secret`,
  plus the display blocks `markdown` and `html` (no scripts; inline styles are fine).
- Any field takes `required`, `default`, `help` and `group`. Past ~4 questions,
  give them `group`s: each group is one page.
- Every choice field gets an "Other…" free-text choice, and every form ends
  with an optional `anything_else` textarea. Don't add your own.
- Pictures only through `--asset <file>` plus `{{asset:<basename>}}`. Remote
  images are blocked.
- Put your recommendation in `default` and say why in `help`, so the user can
  accept it in one tap.

## Answers

`wait` prints one JSON object: `{"id","status","answers":{key: value},"closedAt",…}`.
A `--multi` form has no `answers`: it has `"answerCount":N` and
`"responses":[{"n":1,"at":"…","answers":{key: value}},…]`, oldest first.

- text, textarea, date, select, radio → string (an "Other…" answer is the user's own text)
- email → the address, trimmed and lowercased (checked to look like one)
- multiselect → array of strings · checkbox → bool · number, slider → number
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
