// Package askform holds the files published next to the binaries.
//
// install.sh lives at the repository root, so a raw GitHub URL works as well
// as https://ask-form.xyz/install.sh, which the relay serves from here.
package askform

import (
	"embed"
	"io/fs"
)

//go:embed install.sh
var InstallScript []byte

//go:embed all:plugins/ask-form
var pluginFS embed.FS

// Plugin is the chat-app plugin (Claude and ChatGPT read the same folder:
// .claude-plugin/plugin.json + .mcp.json for Claude, plugin.json + mcp.json
// for OpenAI, the skill in skills/). The repository's
// .claude-plugin/marketplace.json lists it for Claude's "Add marketplace";
// the relay serves it zipped (skill.PluginZip).
var Plugin, _ = fs.Sub(pluginFS, "plugins/ask-form")
