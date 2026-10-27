# ask-form

Your coding agent asks, you answer from anywhere. `ask-form` lets Claude Code
(or any CLI agent) put structured questions in a private web form. You answer
in a browser, phone included, and the answers come back to the agent as JSON.

Try the example forms at [forms.gabvdl.xyz/examples](https://forms.gabvdl.xyz/examples).

## Install

Linux or macOS:

```bash
curl -fsSL https://forms.gabvdl.xyz/install.sh | sh
```

This puts the binary in `~/.local/bin`, connects the machine to the relay at
forms.gabvdl.xyz, starts `ask-form serve` as a background service, and adds
the Claude Code skill. Or let your agent do it. Paste this:

> Fetch https://forms.gabvdl.xyz/agent-install and follow it to install ask-form and set it up for yourself, then ask me something with it to check it works.

Other ways: `go install github.com/GabrielVidal1/ask-form/cmd/ask-form@latest`
then `ask-form setup`, or a binary from
[Releases](https://github.com/GabrielVidal1/ask-form/releases).
`ask-form setup --local` skips the relay; forms are then answered on the same
machine only.

## Use

```bash
ask-form create --title "Project setup" --fields-file spec.json
# {"id":"project-setup_k3f9q2","status":"pending","url":"https://forms.gabvdl.xyz/forms/project-setup_k3f9q2?token=…",…}

ask-form wait project-setup_k3f9q2 --timeout 30m     # in the background
# {"id":"project-setup_k3f9q2","status":"submitted","answers":{"stack":"Vite","deploy":true},…}
```

The agent's reference (field types, answers, exit codes) is
[`skill/SKILL.md`](skill/SKILL.md). `ask-form skill print --body` prints it
for agents other than Claude Code.

## Upgrade

`ask-form serve` installs new releases by itself. Set
`ASK_FORM_AUTO_UPGRADE` to `minor` (no new major versions), `notify` or `off`
to change that.
`ask-form upgrade` upgrades by hand, and `ask-form uninstall` removes the
service and the skill.

## More

- [How it works and security](docs/how-it-works.md): the relay, tokens,
  headers, open registration, upgrades.
- Your own relay: `ask-form-relay serve --machine-listen "" --register open`
  behind HTTPS, then `ask-form setup --relay https://your.host` on each machine.

## License

[MIT](LICENSE)
