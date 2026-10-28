package form

import (
	"html"
	"html/template"
	"strings"
)

// DiffHTML draws a unified diff for a phone: one line per row, added and
// removed lines tinted, file headers and hunk markers set apart. It needs no
// script, and everything is escaped.
func DiffHTML(src string) template.HTML {
	var b strings.Builder
	lines := strings.Split(strings.ReplaceAll(strings.TrimRight(src, "\r\n"), "\r\n", "\n"), "\n")
	for _, l := range lines {
		class := "d-ctx"
		switch {
		case strings.HasPrefix(l, "+++") || strings.HasPrefix(l, "---"):
			class = "d-file"
		case strings.HasPrefix(l, "@@"):
			class = "d-hunk"
		case strings.HasPrefix(l, "diff ") || strings.HasPrefix(l, "index ") || strings.HasPrefix(l, "new file") ||
			strings.HasPrefix(l, "deleted file") || strings.HasPrefix(l, "rename ") || strings.HasPrefix(l, "similarity "):
			class = "d-meta"
		case strings.HasPrefix(l, "+"):
			class = "d-add"
		case strings.HasPrefix(l, "-"):
			class = "d-del"
		}
		b.WriteString(`<span class="` + class + `">`)
		b.WriteString(html.EscapeString(l))
		b.WriteString("</span>")
	}
	return template.HTML(b.String())
}
