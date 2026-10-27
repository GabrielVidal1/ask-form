// Package skill holds the agent instructions for ask-form: SKILL.md is the
// Claude Code skill `ask-form setup` installs, and its body is what other
// agents get (`ask-form skill print --body`).
package skill

import (
	_ "embed"
	"strings"
)

// Markdown is SKILL.md, frontmatter included.
//
//go:embed SKILL.md
var Markdown string

// Marker identifies a SKILL.md that ask-form wrote (and may rewrite).
const Marker = "<!-- Installed by `ask-form setup`"

// Body is SKILL.md without its frontmatter and install marker: the part to
// paste into another agent's instructions (AGENTS.md, .cursorrules…).
func Body() string {
	s := Markdown
	if strings.HasPrefix(s, "---\n") {
		if i := strings.Index(s[4:], "\n---\n"); i >= 0 {
			s = s[4+i+5:]
		}
	}
	var keep []string
	for _, l := range strings.Split(s, "\n") {
		if !strings.HasPrefix(l, Marker) {
			keep = append(keep, l)
		}
	}
	return strings.TrimLeft(strings.Join(keep, "\n"), "\n")
}
