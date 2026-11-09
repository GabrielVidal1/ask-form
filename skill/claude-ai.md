---
name: ask-form
description: Ask the user structured questions in a private web form they answer from any browser or phone (choices, mockups, sliders, dates, tables) and read the answers back as JSON.
---

# ask-form, in this chat's sandbox

ask-form puts your questions in a private web form. You show the user a link,
they answer in a browser (their phone too), and you read the answers as JSON.
Use it when you need several decisions or values at once, want the user to
compare options visually (mockups, pictures), or someone else should answer.

Here it runs in your code sandbox: a small server (`ask-form serve`) keeps
this chat's forms, and the relay at {{RELAY}} carries the user's browser to
it. The forms live as long as this sandbox does.

## 1. Start it (before every form)

```bash
command -v ask-form >/dev/null 2>&1 || npm install -g --silent ask-form@latest \
  || npm install -g --silent --prefix "$HOME/.local" ask-form@latest
ask-form start --relay {{RELAY}} --key {{KEY}}
```

If `ask-form` is still not found, call `$HOME/.local/bin/ask-form` instead.
`start` prints JSON ending with `"ready": true`. Run it again before each
`create`, `wait` or `get`: it does nothing when the server already runs, and
brings it back when the sandbox was paused or reset.

When it fails, don't work around it. Tell the user what to change:

- `npm install` refused (403, "host not allowed", ENOTFOUND): Claude's code
  sandbox can't reach registry.npmjs.org. Settings → Capabilities → Code
  execution → "Allow network egress" must allow package managers (on a Team or
  Enterprise plan, the organization owner sets it).
- `start` can't reach {{RELAY}}: add `{{HOST}}` to the allowed domains, same
  place ("package managers and specific domains").
- `start` says the key is unknown or used up: the user downloads the skill
  again at {{RELAY}}/#claude-ai (or runs `ask-form skill export --claude-ai` on a
  computer with ask-form) and uploads the new zip in place of this one.

## 2. Ask

Write the questions as a JSON array (the field types are in `reference.md`,
next to this file), then create the form:

```bash
cat > /tmp/spec.json <<'JSON'
[{"key":"stack","label":"Which stack?","type":"radio","required":true,"options":["Vite + React","Next.js"]},
 {"key":"notes","label":"Anything I should know?","type":"textarea"}]
JSON
ask-form create --title "Project setup" --description "Two choices before I start." --fields-file /tmp/spec.json
```

Put the `url` from the output in your reply as a link, then go to step 3 in
the same turn. The url holds the form's token: don't repeat it elsewhere. It
opens on any device, phone included.

Write the form in the user's language and pass it: `--lang fr` for French
questions (en and fr exist; it sets the buttons and messages around your
questions). Pictures to choose from: write them with `--asset file.png` and
`{{asset:file.png}}`; mockups are `html` options (see `reference.md`).

Not here: `--open` (no browser in the sandbox) and `secret` fields (the value
would end up in this chat: ask the user to keep secrets out of it).

## 3. Wait for the answer, then carry on

Right after the reply with the link, still in the same turn:

```bash
ask-form wait <id> --timeout 4m
```

- Exit 0: the answers are in the JSON. Carry on with the task.
- Exit 3: declined, revoked or expired. A decline may carry a `reason`: read it.
- Exit 4: not answered yet. Run the same command again, three times in all
  (about 12 minutes). If the command is cut off before it returns, use
  `--timeout 2m`.

Still no answer after that: end your turn. Say the form stays open and ask
the user to tell you once they have answered. On their next message, run
`ask-form start …` (the line from step 1), then `ask-form get <id>`. If `get`
says the form is unknown, the sandbox was reset and the form went with it:
create it again.

If the user says the page shows "offline", this sandbox was paused: run
`ask-form start …` and tell them to reload the page.

## Many people, one form

`--multi` makes one link that takes any number of answers (a team vote,
feedback): `ask-form close <id>` ends it, then `ask-form get <id>` has every
answer. Here it only takes answers while this chat's sandbox is up, so keep
such forms short-lived (`--ttl 2h`).

## Other commands

`ask-form get <id>` (status and answers), `ask-form list`, `ask-form revoke <id>`
(withdraw a form), `ask-form status`.

<!-- Exported by ask-form {{VERSION}} for claude.ai. Export again to update. -->
