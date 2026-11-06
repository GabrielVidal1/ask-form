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

// SchemeTokens splits the custom properties that css's :root / html rules
// set into the two palettes a page is drawn in, each a list of declarations
// ("--x: v;--y: w"): light takes the rules no colour-scheme query guards and
// those under (prefers-color-scheme: light), dark the unguarded ones and
// those under (prefers-color-scheme: dark), in source order, so a later
// declaration wins as it does in the cascade. A frame told which side of
// the page it sits on applies one of them whatever the OS prefers. Rules
// under any other condition (print, a width) are left out. A property set
// twice keeps its last value, in the place of its first.
func SchemeTokens(css string) (light, dark string) {
	var l, d []string
	schemeTokensIn(stripComments(css), true, true, &l, &d)
	return lastWins(l), lastWins(d)
}

func lastWins(decls []string) string {
	at := map[string]int{}
	var out []string
	for _, decl := range decls {
		name, _, _ := strings.Cut(decl, ":")
		name = strings.TrimSpace(name)
		if i, ok := at[name]; ok {
			out[i] = decl
			continue
		}
		at[name] = len(out)
		out = append(out, decl)
	}
	return strings.Join(out, ";")
}

func schemeTokensIn(css string, inLight, inDark bool, l, d *[]string) {
	for i := 0; i < len(css); {
		open, term := scanTo(css, i, "{;")
		prelude := strings.TrimSpace(css[i:open])
		if term != '{' {
			i = open + 1
			continue
		}
		end, _ := scanTo(css, open+1, "}")
		body := css[open+1 : min(end, len(css))]
		i = end + 1
		switch {
		case foldPrefix(prelude, "@media"):
			switch strings.TrimPrefix(strings.Join(strings.Fields(strings.ToLower(prelude[6:])), ""), "screenand") {
			case "(prefers-color-scheme:dark)":
				schemeTokensIn(body, false, inDark, l, d)
			case "(prefers-color-scheme:light)":
				schemeTokensIn(body, inLight, false, l, d)
			}
		case rootSelector(prelude):
			for _, decl := range splitTop(body, ';') {
				if decl = strings.TrimSpace(decl); strings.HasPrefix(decl, "--") {
					if inLight {
						*l = append(*l, decl)
					}
					if inDark {
						*d = append(*d, decl)
					}
				}
			}
		}
	}
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
