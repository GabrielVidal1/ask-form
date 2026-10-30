package form

import (
	"html"
	"html/template"
	"regexp"
	"strings"
)

// A small, safe markdown subset rendered on the server: everything is escaped
// first, so raw HTML in the source shows as text. Blocks: paragraphs, #–###
// headings, - / * / 1. lists, > quotes, ``` fences, GFM tables. Inline:
// `code`, **bold**, *italic* / _italic_, [links](https://…) — http(s) and
// mailto only, opened in a new tab without a referrer. No images: a remote
// image would leak the respondent's address (and the CSP blocks it anyway).

var (
	headingRe = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	ulRe      = regexp.MustCompile(`^\s*[-*+]\s+(.*)$`)
	olRe      = regexp.MustCompile(`^\s*\d+[.)]\s+(.*)$`)
	tableSep  = regexp.MustCompile(`^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?\s*$`)
	codeRe    = regexp.MustCompile("`([^`]+)`")
	boldRe    = regexp.MustCompile(`\*\*([^*]+)\*\*|__([^_]+)__`)
	italicRe  = regexp.MustCompile(`(^|[^\w*])\*([^*\s][^*]*)\*|(^|[^\w])_([^_\s][^_]*)_`)
	linkRe    = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)
)

// MarkdownHTML renders block markdown.
func MarkdownHTML(src string) template.HTML {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	var b strings.Builder
	var para []string
	flush := func() {
		if len(para) > 0 {
			b.WriteString("<p>" + inline(strings.Join(para, "\n")) + "</p>")
			para = nil
		}
	}
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trim := strings.TrimSpace(line)
		switch {
		case trim == "":
			flush()
		case strings.HasPrefix(trim, "```"):
			flush()
			var code []string
			for i++; i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "```"); i++ {
				code = append(code, lines[i])
			}
			b.WriteString("<pre><code>" + html.EscapeString(strings.Join(code, "\n")) + "</code></pre>")
		case headingRe.MatchString(trim):
			flush()
			m := headingRe.FindStringSubmatch(trim)
			lvl := len(m[1]) + 2 // the page's own h1/h2 sit above
			if lvl > 6 {
				lvl = 6
			}
			tag := "h" + string(rune('0'+lvl))
			b.WriteString("<" + tag + ">" + inline(m[2]) + "</" + tag + ">")
		case strings.HasPrefix(trim, ">"):
			flush()
			var q []string
			for ; i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), ">"); i++ {
				q = append(q, strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(lines[i]), ">"), " "))
			}
			i--
			b.WriteString("<blockquote>" + string(MarkdownHTML(strings.Join(q, "\n"))) + "</blockquote>")
		case ulRe.MatchString(line) || olRe.MatchString(line):
			flush()
			re, tag := ulRe, "ul"
			if !ulRe.MatchString(line) {
				re, tag = olRe, "ol"
			}
			b.WriteString("<" + tag + ">")
			for ; i < len(lines); i++ {
				if m := re.FindStringSubmatch(lines[i]); m != nil {
					b.WriteString("<li>" + inline(m[1]) + "</li>")
					continue
				}
				// A non-blank, indented line continues the previous item.
				if t := strings.TrimSpace(lines[i]); t != "" && strings.HasPrefix(lines[i], "  ") {
					b.WriteString("<br>" + inline(t))
					continue
				}
				break
			}
			i--
			b.WriteString("</" + tag + ">")
		case strings.Contains(trim, "|") && i+1 < len(lines) && tableSep.MatchString(lines[i+1]):
			// Each cell's content sits in a .cell box: app.css caps a column's
			// width there (a td ignores max-width), so a wide table scrolls
			// sideways instead of squeezing every column.
			flush()
			b.WriteString(`<div class="table"><table><thead><tr>`)
			for _, c := range cells(trim) {
				b.WriteString(`<th><div class="cell">` + inline(c) + "</div></th>")
			}
			b.WriteString("</tr></thead><tbody>")
			for i += 2; i < len(lines) && strings.Contains(lines[i], "|") && strings.TrimSpace(lines[i]) != ""; i++ {
				b.WriteString("<tr>")
				for _, c := range cells(strings.TrimSpace(lines[i])) {
					b.WriteString(`<td><div class="cell">` + inline(c) + "</div></td>")
				}
				b.WriteString("</tr>")
			}
			i--
			b.WriteString("</tbody></table></div>")
		default:
			para = append(para, trim)
		}
	}
	flush()
	return template.HTML(b.String())
}

// InlineHTML renders one line of inline markdown (a label, an option).
func InlineHTML(src string) template.HTML { return template.HTML(inline(src)) }

func cells(row string) []string {
	row = strings.TrimSuffix(strings.TrimPrefix(row, "|"), "|")
	parts := strings.Split(row, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// inline escapes the text, then applies code spans (whose content is kept
// verbatim), links, bold and italic; newlines become <br>.
func inline(src string) string {
	var codes []string
	s := codeRe.ReplaceAllStringFunc(src, func(m string) string {
		codes = append(codes, "<code>"+html.EscapeString(m[1:len(m)-1])+"</code>")
		return "\x00" + string(rune('A'+len(codes)-1)) + "\x00"
	})
	s = html.EscapeString(s)
	s = linkRe.ReplaceAllStringFunc(s, func(m string) string {
		p := linkRe.FindStringSubmatch(m)
		href := html.UnescapeString(p[2])
		low := strings.ToLower(href)
		if !strings.HasPrefix(low, "https://") && !strings.HasPrefix(low, "http://") && !strings.HasPrefix(low, "mailto:") {
			return m
		}
		return `<a href="` + html.EscapeString(href) + `" target="_blank" rel="noopener noreferrer nofollow">` + p[1] + `</a>`
	})
	s = boldRe.ReplaceAllString(s, "<strong>$1$2</strong>")
	s = italicRe.ReplaceAllString(s, "$1$3<em>$2$4</em>")
	s = strings.ReplaceAll(s, "\n", "<br>")
	for i, c := range codes {
		s = strings.Replace(s, "\x00"+string(rune('A'+i))+"\x00", c, 1)
	}
	return s
}
