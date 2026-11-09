# ask-form

Your coding agent asks, you answer from anywhere. `ask-form` lets Claude Code,
Claude's code sandbox (claude.ai) or any agent that runs shell commands put
structured questions in a private web form. You answer in a browser, phone
included, and the answers come back to the agent as JSON.

This package is the `ask-form` binary for npm: a small launcher plus one
package per platform (`ask-form-linux-x64`, `ask-form-linux-arm64`,
`ask-form-darwin-x64`, `ask-form-darwin-arm64`) holding the Go binary of the
[GitHub release](https://github.com/GabrielVidal1/ask-form/releases) with the
same version.

```bash
npx ask-form version
npm install -g ask-form && ask-form setup     # a computer that stays on
ask-form start --key <key>                    # a sandbox: this session only
```

On your own computer, the one-line installer does the same without Node:
`curl -fsSL https://ask-form.xyz/install.sh | sh`.

In claude.ai: download the skill at [ask-form.xyz](https://ask-form.xyz/#claude-ai)
(or `ask-form skill export --claude-ai` on a computer with ask-form), upload it
in Customize → Skills, and let Claude's code sandbox reach `ask-form.xyz`.

Docs, field types and how it keeps forms private:
[github.com/GabrielVidal1/ask-form](https://github.com/GabrielVidal1/ask-form).
MIT license.
