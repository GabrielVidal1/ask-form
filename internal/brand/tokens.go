package brand

import "strings"

// Tokens is the part of a brand that html blocks and mockups take: the
// custom properties (--primary: …) its :root / html rules set, under the
// same @media / @supports conditions. The rest of the CSS styles the answer
// page and would only get in the way inside an agent's drawing.
func Tokens(css string) string { return tokensIn(stripComments(css)) }

func tokensIn(css string) string {
	var out strings.Builder
	for i := 0; i < len(css); {
		open, term := scanTo(css, i, "{;")
		prelude := strings.TrimSpace(css[i:open])
		if term != '{' {
			i = open + 1 // a statement (@charset …;) or a stray ';'
			continue
		}
		end, _ := scanTo(css, open+1, "}")
		body := css[open+1 : min(end, len(css))]
		i = end + 1
		switch {
		case foldPrefix(prelude, "@media"), foldPrefix(prelude, "@supports"):
			if inner := tokensIn(body); inner != "" {
				out.WriteString(prelude + "{" + inner + "}")
			}
		case rootSelector(prelude):
			var decls []string
			for _, d := range splitTop(body, ';') {
				if d = strings.TrimSpace(d); strings.HasPrefix(d, "--") {
					decls = append(decls, d)
				}
			}
			if len(decls) > 0 {
				out.WriteString(":root{" + strings.Join(decls, ";") + "}")
			}
		}
	}
	return out.String()
}

// rootSelector: every selector of the list is :root or html.
func rootSelector(prelude string) bool {
	for _, s := range strings.Split(prelude, ",") {
		if s = strings.ToLower(strings.TrimSpace(s)); s != ":root" && s != "html" {
			return false
		}
	}
	return prelude != ""
}

// scanTo finds the first of stops at nesting depth 0 from i, skipping
// strings and nested (), [] and {} — the index and the byte found (0 at the
// end of the text). A '}' stop ends the block i is inside.
func scanTo(css string, i int, stops string) (int, byte) {
	depth := 0
	for i < len(css) {
		c := css[i]
		switch {
		case c == '"' || c == '\'':
			i = stringEnd(css, i)
			continue
		case depth == 0 && strings.IndexByte(stops, c) >= 0:
			return i, c
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
		}
		i++
	}
	return len(css), 0
}

// splitTop splits s at every sep outside strings and brackets.
func splitTop(s string, sep byte) []string {
	var parts []string
	for len(s) > 0 {
		j, _ := scanTo(s, 0, string(sep))
		parts = append(parts, s[:j])
		if j >= len(s) {
			break
		}
		s = s[j+1:]
	}
	return parts
}

func stripComments(css string) string {
	var out strings.Builder
	for i := 0; i < len(css); {
		switch c := css[i]; {
		case c == '"' || c == '\'':
			j := stringEnd(css, i)
			out.WriteString(css[i:j])
			i = j
		case strings.HasPrefix(css[i:], "/*"):
			end := strings.Index(css[i+2:], "*/")
			if end < 0 {
				return out.String()
			}
			out.WriteByte(' ')
			i += end + 4
		default:
			out.WriteByte(c)
			i++
		}
	}
	return out.String()
}
