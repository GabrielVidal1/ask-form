// Package landing embeds what the relay serves besides the forms: the page
// at /, and /agent-install (plain-text setup steps for a coding agent).
package landing

import _ "embed"

//go:embed index.html
var Page []byte

//go:embed agent-install.txt
var AgentInstall []byte
