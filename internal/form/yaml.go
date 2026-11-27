package form

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The YAML a form file is written in: a strict subset of YAML 1.2, read into
// a tree of *Map (a mapping, its keys in the file's order), []any, string,
// bool, nil and json.Number (a number, kept as the text it was written as).
// A JSON form file is read into the same tree (decodeJSON), so the rest of
// the package reads both alike.
//
// In: block mappings and lists (a list may sit at its key's indentation),
// `|` and `>` scalars with their `-` / `+` / indentation indicators, flow
// `[a, b]` and `{k: v}` over several lines, single- and double-quoted
// scalars, plain scalars over several lines, comments, a leading `---`.
// Refused, with the line: anchors and aliases, tags, directives, `?` keys,
// several documents, tabs as indentation, a key given twice.
//
// Types are YAML 1.2's core ones, narrowed: only true / false are booleans
// (yes, no, on, off are text), null is null / ~ / nothing, and a number is a
// plain scalar written as JSON writes one (-1, 2.5, 1e3), so 0x1F, +1, 1_000
// or .5 stay text, and 22:00 is a time, not a sexagesimal number.

// maxYAMLDepth bounds nesting: a form file never needs much, and a deep one
// must not exhaust the stack.
const maxYAMLDepth = 64

// YAMLError is a parse error at a line of the file (1-based).
type YAMLError struct {
	Line int
	Msg  string
}

func (e *YAMLError) Error() string { return fmt.Sprintf("line %d: %s", e.Line, e.Msg) }

type yline struct {
	n      int    // line number
	indent int    // leading spaces
	text   string // the line after them
	raw    string // the whole line
}

type yparser struct {
	lines []yline
	i     int
	depth int
}

// ParseYAML reads a YAML document of the subset above.
func ParseYAML(src []byte) (any, error) {
	if !utf8.Valid(src) {
		return nil, &YAMLError{1, "the file is not UTF-8 text"}
	}
	s := strings.TrimPrefix(string(src), "\uFEFF")
	p := &yparser{}
	for n, raw := range strings.Split(s, "\n") {
		raw = strings.TrimSuffix(raw, "\r")
		t := strings.TrimLeft(raw, " ")
		p.lines = append(p.lines, yline{n: n + 1, indent: len(raw) - len(t), text: t, raw: raw})
	}
	p.skipBlank()
	if l, ok := p.peek(); ok && (l.raw == "---" || strings.HasPrefix(l.raw, "--- ")) {
		if rest := strings.TrimSpace(strings.TrimPrefix(l.raw, "---")); rest != "" && !strings.HasPrefix(rest, "#") {
			return nil, p.errAt(l, "put the document on the lines after ---")
		}
		p.i++
	}
	p.skipBlank()
	if _, ok := p.peek(); !ok {
		return nil, &YAMLError{1, "the file is empty"}
	}
	v, err := p.block(-1)
	if err != nil {
		return nil, err
	}
	p.skipBlank()
	if l, ok := p.peek(); ok {
		switch {
		case l.raw == "---" || strings.HasPrefix(l.raw, "--- "):
			return nil, p.errAt(l, "one document per file: remove this ---")
		case l.raw == "..." || strings.HasPrefix(l.raw, "... "):
			p.i++
			p.skipBlank()
			if l, ok := p.peek(); ok {
				return nil, p.errAt(l, "nothing may follow the end of the document (...)")
			}
		default:
			return nil, p.errAt(l, "this line is not inside the structure above it: check its indentation")
		}
	}
	return v, nil
}

func (p *yparser) errAt(l yline, format string, a ...any) error {
	return &YAMLError{l.n, fmt.Sprintf(format, a...)}
}

func (p *yparser) peek() (yline, bool) {
	if p.i >= len(p.lines) {
		return yline{}, false
	}
	return p.lines[p.i], true
}

// blank: nothing but spaces, or a comment.
// docMarker: a --- or ... line, which ends the node being read.
func docMarker(l yline) bool {
	return l.indent == 0 && (l.raw == "---" || l.raw == "..." || strings.HasPrefix(l.raw, "--- ") || strings.HasPrefix(l.raw, "... "))
}

func blank(l yline) bool {
	t := strings.TrimLeft(l.text, " \t")
	return t == "" || t[0] == '#'
}

func (p *yparser) skipBlank() {
	for p.i < len(p.lines) && blank(p.lines[p.i]) {
		p.i++
	}
}

// checkLine refuses a tab in the indentation of the line about to be read
// as structure.
func (p *yparser) checkLine() error {
	if l, ok := p.peek(); ok && strings.HasPrefix(l.text, "\t") {
		return p.errAt(l, "indent with spaces, not tabs")
	}
	return nil
}

// block reads the node on the next lines, indented more than parent; nil
// when there is none.
func (p *yparser) block(parent int) (any, error) {
	p.skipBlank()
	if err := p.checkLine(); err != nil {
		return nil, err
	}
	l, ok := p.peek()
	if !ok || l.indent <= parent || docMarker(l) {
		return nil, nil
	}
	return p.node(l.indent, parent)
}

// node reads the node whose first line, the current one, starts at col.
func (p *yparser) node(col, parent int) (any, error) {
	p.depth++
	defer func() { p.depth-- }()
	l := p.lines[p.i]
	if p.depth > maxYAMLDepth {
		return nil, p.errAt(l, "nested more than %d deep", maxYAMLDepth)
	}
	switch {
	case seqItem(l.text):
		return p.seq(col)
	case mapEntry(l.text):
		return p.mapping(col)
	}
	return p.inline(l.text, parent)
}

func seqItem(t string) bool {
	return t == "-" || strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "-\t")
}

// mapEntry reports whether a line starts with `key:` (a plain or quoted key).
func mapEntry(t string) bool {
	_, _, ok, _ := splitKey(t)
	return ok
}

// splitKey cuts `key: rest`. err is set for a key form the subset refuses.
func splitKey(t string) (key, rest string, ok bool, err string) {
	if t == "" {
		return
	}
	switch t[0] {
	case '"', '\'':
		k, n, e := quotedLine(t)
		if e != "" {
			return
		}
		after := t[n:]
		if strings.HasPrefix(after, ":") && (len(after) == 1 || after[1] == ' ' || after[1] == '\t') {
			return k, strings.TrimLeft(after[1:], " \t"), true, ""
		}
		return
	case '[', '{', '#', '|', '>', '-', '%', '@', '`':
		if t[0] != '-' || seqItem(t) {
			return
		}
	case '?':
		if len(t) == 1 || t[1] == ' ' {
			return "", "", true, "? keys are not supported: write key: value"
		}
	case '&', '*', '!':
		if i := keyColon(t); i > 0 {
			return "", "", true, "anchors (&), aliases (*) and tags (!) are not supported"
		}
		return
	}
	i := keyColon(t)
	if i <= 0 {
		return
	}
	return strings.TrimRight(t[:i], " \t"), strings.TrimLeft(t[i+1:], " \t"), true, ""
}

// keyColon finds the `:` ending a plain key: followed by a space or the end
// of the line, before any comment.
func keyColon(t string) int {
	for i := 0; i < len(t); i++ {
		switch t[i] {
		case ':':
			if i+1 == len(t) || t[i+1] == ' ' || t[i+1] == '\t' {
				return i
			}
		case '#':
			if i > 0 && (t[i-1] == ' ' || t[i-1] == '\t') {
				return -1
			}
		}
	}
	return -1
}

func (p *yparser) mapping(col int) (any, error) {
	m := newMap()
	for {
		p.skipBlank()
		if err := p.checkLine(); err != nil {
			return nil, err
		}
		l, ok := p.peek()
		if !ok || l.indent < col || docMarker(l) {
			return m, nil
		}
		if l.indent > col {
			return nil, p.errAt(l, "unexpected indentation")
		}
		if seqItem(l.text) {
			return nil, p.errAt(l, "a list item where a key was expected: indent it under its key")
		}
		key, rest, ok, bad := splitKey(l.text)
		if bad != "" {
			return nil, p.errAt(l, "%s", bad)
		}
		if !ok {
			return nil, p.errAt(l, "expected key: value")
		}
		if m.Has(key) {
			return nil, p.errAt(l, "key %q appears twice", key)
		}
		var v any
		var err error
		if rest == "" || rest[0] == '#' {
			p.i++
			p.skipBlank()
			if next, ok := p.peek(); ok && next.indent == col && seqItem(next.text) {
				v, err = p.seq(col) // a list may sit at its key's indentation
			} else {
				v, err = p.block(col)
			}
		} else {
			v, err = p.inline(rest, col)
		}
		if err != nil {
			return nil, err
		}
		m.Set(key, v)
	}
}

func (p *yparser) seq(col int) (any, error) {
	out := []any{}
	for {
		p.skipBlank()
		if err := p.checkLine(); err != nil {
			return nil, err
		}
		l, ok := p.peek()
		if !ok || l.indent < col || docMarker(l) {
			return out, nil
		}
		if l.indent > col {
			return nil, p.errAt(l, "unexpected indentation")
		}
		if !seqItem(l.text) {
			return out, nil // the key after a list at its key's indentation
		}
		rest := strings.TrimLeft(l.text[1:], " \t")
		if rest == "" || rest[0] == '#' {
			p.i++
			v, err := p.block(col)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
			continue
		}
		if strings.HasPrefix(l.text[1:], "\t") {
			return nil, p.errAt(l, "put a space, not a tab, after -")
		}
		// The item's own content: read it as if it started a line of its
		// own, at its column, so a mapping's next keys line up under it.
		inner := col + len(l.text) - len(rest)
		p.lines[p.i] = yline{n: l.n, indent: inner, text: rest, raw: l.raw}
		p.depth++
		var v any
		var err error
		if p.depth > maxYAMLDepth {
			err = p.errAt(l, "nested more than %d deep", maxYAMLDepth)
		} else if seqItem(rest) || mapEntry(rest) {
			v, err = p.node(inner, col)
		} else {
			v, err = p.inline(rest, col)
		}
		p.depth--
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
}

// inline reads the value that starts at t, inside the current line, for a
// node indented more than parent: a block scalar, a flow collection, a quoted
// or a plain scalar (they may go on over the next lines).
func (p *yparser) inline(t string, parent int) (any, error) {
	l := p.lines[p.i]
	switch t[0] {
	case '|', '>':
		return p.blockScalar(t, parent)
	case '[', '{', '"', '\'':
		sc := &yscan{p: p, s: t, start: l.n}
		v, err := sc.value()
		if err != nil {
			return nil, err
		}
		if rest := strings.TrimLeft(sc.s[sc.pos:], " \t"); rest != "" && rest[0] != '#' {
			return nil, &YAMLError{sc.line(), fmt.Sprintf("unexpected %q after the value", clip(rest, 20))}
		}
		p.i++
		return v, nil
	case '&', '*', '!':
		return nil, p.errAt(l, "anchors (&), aliases (*) and tags (!) are not supported")
	case '%':
		return nil, p.errAt(l, "directives (%%) are not supported")
	}
	if seqItem(t) {
		return nil, p.errAt(l, "a list starts on the line after its key")
	}
	if t[0] == '?' && (len(t) == 1 || t[1] == ' ') {
		return nil, p.errAt(l, "? keys are not supported: write key: value")
	}
	parts := []string{stripComment(t)}
	p.i++
	// A plain scalar goes on over the lines indented under it; blank lines
	// inside it are newlines, the ones after it are left for the next read.
	for {
		j := p.i
		for j < len(p.lines) && strings.TrimSpace(p.lines[j].raw) == "" {
			j++
		}
		if j >= len(p.lines) {
			break
		}
		n := p.lines[j]
		if n.indent <= parent || blank(n) || strings.HasPrefix(n.text, "\t") {
			break
		}
		if mapEntry(n.text) {
			return nil, p.errAt(n, "unexpected key: indent it under a key of its own, or quote the text above")
		}
		for k := p.i; k < j; k++ {
			parts = append(parts, "")
		}
		parts = append(parts, stripComment(n.text))
		p.i = j + 1
	}
	return plain(fold(parts)), nil
}

// fold joins the lines of a multi-line plain scalar: a line break is a
// space, an empty line a newline.
func fold(parts []string) string {
	var b strings.Builder
	b.WriteString(parts[0])
	empty := 0
	for _, s := range parts[1:] {
		if s = strings.TrimSpace(s); s == "" {
			empty++
			continue
		}
		if empty > 0 {
			b.WriteString(strings.Repeat("\n", empty))
		} else {
			b.WriteByte(' ')
		}
		empty = 0
		b.WriteString(s)
	}
	return b.String()
}

func stripComment(t string) string {
	for i := 0; i < len(t); i++ {
		if t[i] == '#' && i > 0 && (t[i-1] == ' ' || t[i-1] == '\t') {
			return strings.TrimRight(t[:i], " \t")
		}
	}
	return strings.TrimRight(t, " \t")
}

var jsonNumber = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][-+]?[0-9]+)?$`)

// plain resolves a plain scalar.
func plain(s string) any {
	switch s {
	case "", "~", "null", "Null", "NULL":
		return nil
	case "true", "True", "TRUE":
		return true
	case "false", "False", "FALSE":
		return false
	}
	if jsonNumber.MatchString(s) {
		return json.Number(s)
	}
	return s
}

// blockScalar reads a `|` or `>` scalar whose header is t.
func (p *yparser) blockScalar(t string, parent int) (any, error) {
	l := p.lines[p.i]
	head := stripComment(t)
	style, chomp, ind := head[0], byte(0), 0
	for _, c := range []byte(head[1:]) {
		switch {
		case (c == '-' || c == '+') && chomp == 0:
			chomp = c
		case c >= '1' && c <= '9' && ind == 0:
			ind = int(c - '0')
		default:
			return nil, p.errAt(l, "after %c: only -, + and one digit 1-9", style)
		}
	}
	p.i++
	indent := -1
	if ind > 0 {
		indent = max(parent, 0) + ind
		if parent < 0 {
			indent = ind
		}
	}
	var lines []string
	for p.i < len(p.lines) {
		n := p.lines[p.i]
		if strings.TrimSpace(n.raw) == "" {
			if indent >= 0 && len(n.raw) > indent {
				lines = append(lines, n.raw[indent:])
			} else {
				lines = append(lines, "")
			}
			p.i++
			continue
		}
		if indent < 0 {
			if n.indent <= parent {
				break
			}
			indent = n.indent
		}
		if n.indent < indent {
			break
		}
		lines = append(lines, n.raw[indent:])
		p.i++
	}
	trail := 0
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
		trail++
	}
	var text string
	if style == '|' {
		text = strings.Join(lines, "\n")
	} else {
		text = foldBlock(lines)
	}
	switch {
	case len(lines) == 0:
		text = ""
		if chomp == '+' {
			text = strings.Repeat("\n", trail)
		}
	case chomp == '-':
	case chomp == '+':
		text += strings.Repeat("\n", trail+1)
	default:
		text += "\n"
	}
	return text, nil
}

// foldBlock folds a `>` scalar: lines join with a space, an empty line is a
// newline, and more-indented lines keep their line breaks.
func foldBlock(lines []string) string {
	var b strings.Builder
	prevText := false // the line before was ordinary text
	for i, s := range lines {
		more := s != "" && (s[0] == ' ' || s[0] == '\t')
		if i > 0 {
			switch {
			case s == "":
				b.WriteByte('\n')
			case prevText && !more:
				b.WriteByte(' ')
			default:
				if lines[i-1] != "" || more {
					b.WriteByte('\n')
				}
			}
		}
		b.WriteString(s)
		prevText = s != "" && !more
	}
	return b.String()
}

// ── flow collections and quoted scalars ─────────────────────────────────────

// yscan reads from the current line on, fetching the next lines as a flow
// collection or a quoted scalar goes on over them.
type yscan struct {
	p     *yparser
	s     string
	pos   int
	start int // the line it began on, for "never closed"
}

func (sc *yscan) line() int { return sc.p.lines[sc.p.i].n }

func (sc *yscan) errf(format string, a ...any) error {
	return &YAMLError{sc.line(), fmt.Sprintf(format, a...)}
}

// nextLine moves on to the next line; false at the end of the file.
func (sc *yscan) nextLine() bool {
	if sc.p.i+1 >= len(sc.p.lines) {
		return false
	}
	sc.p.i++
	sc.s, sc.pos = sc.p.lines[sc.p.i].raw, 0
	return true
}

// ws skips spaces, comments and (in a flow collection) line breaks.
func (sc *yscan) ws() error {
	for {
		for sc.pos < len(sc.s) && (sc.s[sc.pos] == ' ' || sc.s[sc.pos] == '\t') {
			sc.pos++
		}
		if sc.pos < len(sc.s) && sc.s[sc.pos] == '#' && (sc.pos == 0 || sc.s[sc.pos-1] == ' ' || sc.s[sc.pos-1] == '\t') {
			sc.pos = len(sc.s)
		}
		if sc.pos < len(sc.s) {
			return nil
		}
		if !sc.nextLine() {
			return &YAMLError{sc.start, "a [ or { opened here is never closed"}
		}
	}
}

func (sc *yscan) value() (any, error) {
	sc.p.depth++
	defer func() { sc.p.depth-- }()
	if sc.p.depth > maxYAMLDepth {
		return nil, sc.errf("nested more than %d deep", maxYAMLDepth)
	}
	switch c := sc.s[sc.pos]; c {
	case '[':
		return sc.flowSeq()
	case '{':
		return sc.flowMap()
	case '"', '\'':
		return sc.quoted()
	case '&', '*', '!':
		return nil, sc.errf("anchors (&), aliases (*) and tags (!) are not supported")
	}
	return sc.flowPlain(), nil
}

func (sc *yscan) flowSeq() (any, error) {
	sc.pos++ // [
	out := []any{}
	for {
		if err := sc.ws(); err != nil {
			return nil, err
		}
		if sc.s[sc.pos] == ']' {
			sc.pos++
			return out, nil
		}
		if sc.s[sc.pos] == ',' {
			return nil, sc.errf("an empty item in [ ]")
		}
		v, err := sc.value()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		if err := sc.ws(); err != nil {
			return nil, err
		}
		switch sc.s[sc.pos] {
		case ',':
			sc.pos++
		case ']':
		case ':':
			return nil, sc.errf("a key: value inside [ ] needs { } around it")
		default:
			return nil, sc.errf("expected , or ] in the list")
		}
	}
}

func (sc *yscan) flowMap() (any, error) {
	sc.pos++ // {
	m := newMap()
	for {
		if err := sc.ws(); err != nil {
			return nil, err
		}
		if sc.s[sc.pos] == '}' {
			sc.pos++
			return m, nil
		}
		var key string
		quotedKey := false
		switch sc.s[sc.pos] {
		case '"', '\'':
			k, err := sc.quoted()
			if err != nil {
				return nil, err
			}
			key, quotedKey = k.(string), true
		case ',', ':':
			return nil, sc.errf("a key is missing in { }")
		case '[', '{', '?', '&', '*', '!':
			return nil, sc.errf("a key in { } is a plain or quoted text")
		default:
			k := sc.flowPlain()
			s, isStr := k.(string)
			if !isStr {
				s = fmt.Sprint(k)
				if k == nil {
					s = "null"
				}
			}
			key = s
		}
		if m.Has(key) {
			return nil, sc.errf("key %q appears twice", key)
		}
		if err := sc.ws(); err != nil {
			return nil, err
		}
		if sc.s[sc.pos] != ':' {
			return nil, sc.errf("expected : after the key %q", key)
		}
		sc.pos++
		if !quotedKey && sc.pos < len(sc.s) && !strings.ContainsRune(" \t,}]", rune(sc.s[sc.pos])) {
			return nil, sc.errf("put a space after the : of %q", key)
		}
		if err := sc.ws(); err != nil {
			return nil, err
		}
		var v any
		if c := sc.s[sc.pos]; c != ',' && c != '}' {
			var err error
			if v, err = sc.value(); err != nil {
				return nil, err
			}
			if err := sc.ws(); err != nil {
				return nil, err
			}
		}
		m.Set(key, v)
		switch sc.s[sc.pos] {
		case ',':
			sc.pos++
		case '}':
		default:
			return nil, sc.errf("expected , or } after the value of %q", key)
		}
	}
}

// flowPlain reads a plain scalar inside [ ] or { }: it ends at , [ ] { },
// at a `: ` and at the end of the line.
func (sc *yscan) flowPlain() any {
	start := sc.pos
	for sc.pos < len(sc.s) {
		c := sc.s[sc.pos]
		if strings.IndexByte(",[]{}", c) >= 0 {
			break
		}
		if c == ':' && (sc.pos+1 == len(sc.s) || strings.IndexByte(" \t,[]{}", sc.s[sc.pos+1]) >= 0) {
			break
		}
		if c == '#' && sc.pos > start && (sc.s[sc.pos-1] == ' ' || sc.s[sc.pos-1] == '\t') {
			break
		}
		sc.pos++
	}
	return plain(strings.TrimSpace(sc.s[start:sc.pos]))
}

// quoted reads a '…' or "…" scalar, over several lines if need be.
func (sc *yscan) quoted() (any, error) {
	q := sc.s[sc.pos]
	sc.pos++
	var b strings.Builder
	var parts []string // finished lines, folded at the end
	for {
		if sc.pos >= len(sc.s) {
			parts = append(parts, b.String())
			b.Reset()
			if !sc.nextLine() {
				return nil, &YAMLError{sc.start, "a quote opened here is never closed"}
			}
			sc.s = strings.TrimLeft(sc.s, " \t")
			continue
		}
		c := sc.s[sc.pos]
		switch {
		case c == q && q == '\'' && sc.pos+1 < len(sc.s) && sc.s[sc.pos+1] == '\'':
			b.WriteByte('\'')
			sc.pos += 2
		case c == q:
			sc.pos++
			parts = append(parts, b.String())
			return foldQuoted(parts), nil
		case c == '\\' && q == '"':
			if sc.pos+1 >= len(sc.s) { // an escaped line break: the text goes on
				if !sc.nextLine() {
					return nil, &YAMLError{sc.start, "a quote opened here is never closed"}
				}
				sc.s = strings.TrimLeft(sc.s, " \t") // joined with nothing
				continue
			}
			r, n, err := unescape(sc.s[sc.pos:])
			if err != "" {
				return nil, sc.errf("%s", err)
			}
			b.WriteString(r)
			sc.pos += n
		default:
			b.WriteByte(c)
			sc.pos++
		}
	}
}

// foldQuoted joins a quoted scalar's lines: a line break is a space, an
// empty line a newline (an escaped break joined its lines already).
func foldQuoted(parts []string) string {
	if len(parts) == 1 {
		return parts[0]
	}
	var b strings.Builder
	b.WriteString(strings.TrimRight(parts[0], " \t"))
	empty := 0
	for i, s := range parts[1:] {
		last := i == len(parts)-2
		if !last {
			s = strings.TrimRight(s, " \t")
		}
		if s == "" && !last {
			empty++
			continue
		}
		if empty > 0 {
			b.WriteString(strings.Repeat("\n", empty))
		} else {
			b.WriteByte(' ')
		}
		empty = 0
		b.WriteString(s)
	}
	return b.String()
}

// unescape reads one \-escape of a double-quoted scalar.
func unescape(s string) (string, int, string) {
	if len(s) < 2 {
		return "", 0, "a \\ at the end of the text"
	}
	simple := map[byte]string{'0': "\x00", 'a': "\a", 'b': "\b", 't': "\t", '\t': "\t", 'n': "\n", 'v': "\v",
		'f': "\f", 'r': "\r", 'e': "\x1b", ' ': " ", '"': "\"", '/': "/", '\\': "\\", 'N': "\u0085",
		'_': " ", 'L': " ", 'P': " "}
	if r, ok := simple[s[1]]; ok {
		return r, 2, ""
	}
	width := map[byte]int{'x': 2, 'u': 4, 'U': 8}[s[1]]
	if width == 0 {
		return "", 0, fmt.Sprintf("unknown escape \\%c", s[1])
	}
	if len(s) < 2+width {
		return "", 0, fmt.Sprintf("\\%c needs %d hex digits", s[1], width)
	}
	n, err := strconv.ParseUint(s[2:2+width], 16, 32)
	if err != nil || !utf8.ValidRune(rune(n)) {
		return "", 0, fmt.Sprintf("bad escape \\%s", s[1:2+width])
	}
	return string(rune(n)), 2 + width, ""
}

// quotedLine reads a quoted key on one line: its text and the bytes it took.
func quotedLine(t string) (string, int, string) {
	q := t[0]
	var b strings.Builder
	for i := 1; i < len(t); i++ {
		c := t[i]
		switch {
		case c == q && q == '\'' && i+1 < len(t) && t[i+1] == '\'':
			b.WriteByte('\'')
			i++
		case c == q:
			return b.String(), i + 1, ""
		case c == '\\' && q == '"':
			r, n, err := unescape(t[i:])
			if err != "" {
				return "", 0, err
			}
			b.WriteString(r)
			i += n - 1
		default:
			b.WriteByte(c)
		}
	}
	return "", 0, "unclosed quote"
}

// ── ordered mappings ────────────────────────────────────────────────────────

// Map is a mapping of a form file, its keys in the order they were written:
// the order of a section's fields and of a question's options.
type Map struct {
	Keys []string
	vals map[string]any
}

func newMap() *Map { return &Map{vals: map[string]any{}} }

// Has reports whether the mapping holds k.
func (m *Map) Has(k string) bool { _, ok := m.vals[k]; return ok }

// Get is the value under k.
func (m *Map) Get(k string) any { return m.vals[k] }

// Set puts v under k, at the end when k is new.
func (m *Map) Set(k string, v any) {
	if !m.Has(k) {
		m.Keys = append(m.Keys, k)
	}
	m.vals[k] = v
}

// Delete removes k.
func (m *Map) Delete(k string) {
	if !m.Has(k) {
		return
	}
	delete(m.vals, k)
	for i, x := range m.Keys {
		if x == k {
			m.Keys = append(m.Keys[:i:i], m.Keys[i+1:]...)
			break
		}
	}
}

// MarshalJSON writes the mapping as a JSON object, keys in order.
func (m *Map) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range m.Keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		b.Write(kb)
		b.WriteByte(':')
		vb, err := json.Marshal(m.vals[k])
		if err != nil {
			return nil, err
		}
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// decodeJSON reads a JSON document into the tree ParseYAML gives: objects
// keep their keys' order, numbers their text; a key given twice is refused.
func decodeJSON(src []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(src))
	dec.UseNumber()
	v, err := jsonValue(dec, 0)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("not valid JSON: something follows the document")
	}
	return v, nil
}

func jsonValue(dec *json.Decoder, depth int) (any, error) {
	if depth > maxYAMLDepth {
		return nil, fmt.Errorf("nested more than %d deep", maxYAMLDepth)
	}
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	d, ok := t.(json.Delim)
	if !ok {
		return t, nil // string, json.Number, bool or nil
	}
	switch d {
	case '{':
		m := newMap()
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return nil, err
			}
			k, _ := kt.(string)
			if m.Has(k) {
				return nil, fmt.Errorf("key %q appears twice", k)
			}
			v, err := jsonValue(dec, depth+1)
			if err != nil {
				return nil, err
			}
			m.Set(k, v)
		}
		_, err := dec.Token()
		return m, err
	case '[':
		list := []any{}
		for dec.More() {
			v, err := jsonValue(dec, depth+1)
			if err != nil {
				return nil, err
			}
			list = append(list, v)
		}
		_, err := dec.Token()
		return list, err
	}
	return nil, fmt.Errorf("unexpected %v", d)
}
