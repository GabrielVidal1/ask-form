# ask-form

Your coding agent asks, you answer from anywhere. `ask-form` lets Claude Code
(or any CLI agent) put structured questions in a private web form. You answer
in a browser, phone included, and the answers come back to the agent as JSON.

Try the example forms at [ask-form.xyz/examples](https://ask-form.xyz/examples).

## Install

Linux or macOS:

```bash
curl -fsSL https://ask-form.xyz/install.sh | sh
```

This puts the binary in `~/.local/bin`, connects the machine to the relay at
ask-form.xyz, starts `ask-form serve` as a background service, and adds
the Claude Code skill. Or let your agent do it: paste this into Claude Code, or
any coding agent on your computer. A chat app's sandbox (the Claude app,
ChatGPT) can't keep ask-form running.

> Fetch https://ask-form.xyz/agent-install and follow it to install the ask-form skill and set it up, then ask me with it: *what you want to be asked*

Other ways: `go install github.com/GabrielVidal1/ask-form/cmd/ask-form@latest`
then `ask-form setup`, or a binary from
[Releases](https://github.com/GabrielVidal1/ask-form/releases).
`ask-form setup --local` skips the relay; forms are then answered on the same
machine only.

## Use

```bash
ask-form create --title "Project setup" --fields-file spec.json
# {"id":"project-setup_k3f9q2","status":"pending","url":"https://ask-form.xyz/f/project-setup_k3f9q2?token=…",…}

ask-form wait project-setup_k3f9q2 --timeout 30m     # in the background
# {"id":"project-setup_k3f9q2","status":"submitted","answers":{"stack":"Vite","deploy":true},…}
```

The `url` opens on the machine itself; `publicUrl` is the same form from any
device, your phone included. For a team vote or anyone else's feedback,
`--multi` makes one public link that takes any number of answers until it
expires or `ask-form close <id>`; `wait` then returns them all.

Forms speak English or French. `create --lang fr` sets the language of the page
around the questions (buttons, errors, the thanks page), and
`ask-form setup --lang fr` makes French the machine's default. The agent writes
the questions themselves in the same language.

The agent's reference (field types, answers, exit codes) is
[`skill/SKILL.md`](skill/SKILL.md). `ask-form skill print --body` prints it
for agents other than Claude Code.

## Upgrade

`ask-form serve` installs new releases by itself. Set
`ASK_FORM_AUTO_UPGRADE` to `minor` (no new major versions), `notify` or `off`
to change that.
`ask-form upgrade` upgrades by hand, and `ask-form uninstall` removes the
service and the skill.

## Branding

Your forms can wear your look instead of ask-form's. Set it once per machine;
every form that machine serves picks it up on its next page load, no restart:

```bash
ask-form brand set --name "Acme" --logo logo.svg --primary "#2563eb"   # the usual
ask-form brand init > brand.css      # or a starter stylesheet with every theme token
ask-form brand preview brand.css     # the example forms with it (reload after an edit)
ask-form brand set brand.css         # apply it
ask-form brand show | clear [logo…]
```

`--font FILE` sets the page in your font. In the stylesheet, overriding the
theme tokens (`--primary`, `--background`…) recolours everything, and any
other rule restyles what it selects; html blocks and mockups take the tokens
too. Images and fonts next to the file work as `url(./logo.png)`: they are
inlined when you set it, since the page loads nothing from other sites.
`--primary` and `--font` win over the same thing in the file.

## Snapshots

When a form closes (answered, declined, withdrawn, expired), `serve` keeps a
snapshot of it: one self-contained HTML page of the form as it was asked, read-only,
with the answers in it, its look, pictures and mockups inlined. It opens
offline in any browser, years later, whatever ask-form has become since.
Secrets stay out (only their length), uploaded pictures come as thumbnails.
They live in `~/.local/share/ask-form/snapshots/` and are served on the
loopback admin API (`GET /v1/forms/<id>/snapshot`), never on the form's link.

`ask-form setup --no-snapshots` (or `ASK_FORM_SNAPSHOTS=off`) turns them off
for the machine, and `create --no-snapshot` for one form.

## Events

`ask-form setup --events-url https://…` makes `serve` POST every form event
(created with its link, submitted, declined, revoked, expired…) to that URL —
how another program follows the forms without polling. The
[ai-agent](https://git.gabvdl.xyz/gabrielvidal/ai-agent) hub receives them on
the lab. Details in [how it works](docs/how-it-works.md#events).

## Your own relay

ask-form.xyz is one relay; `ask-form-relay` is in this repo, so you can run
your own on your domain, for a team or to keep any third party out. It keeps
no forms or answers, only a hash of each machine's token. Every release ships
its binaries, and the Dockerfile builds its image. In short, behind an HTTPS
proxy:

```bash
ask-form-relay serve --machine-listen= --public-url https://forms.example.com
ask-form-relay machine add laptop                                              # a token for one machine
curl -fsSL https://forms.example.com/install.sh | sh -s -- --token '<token>'   # on that machine
```

[Running your own relay](docs/self-hosting.md) covers Docker Compose and
systemd, Caddy, nginx and Traefik configs, closed or open registration,
moving machines over, Cloudflare and upgrades.

## More

- [How it works and security](docs/how-it-works.md): the relay, tokens,
  headers, open registration, upgrades.
- [Running your own relay](docs/self-hosting.md).

## License

[MIT](LICENSE)
