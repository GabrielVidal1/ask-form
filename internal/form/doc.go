package form

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Doc is a form file once read: the form's own text, and its fields flat,
// the way the server takes them (a page is a field's `group`).
//
// A form file is YAML (or JSON of the same shape) holding the whole form:
//
//	title: Habit tracker
//	description: A few choices before I scaffold.
//	lang: en
//	sections:                       # keyed by each page's title, in order
//	  Look & stack:
//	    intro: Markdown under the page's title.
//	    showIf: {key: x, equals: y}   # the whole page, only while it holds
//	    fields:                       # keyed by the field's key, in order
//	      framework:
//	        type: radio
//	        label: Front end
//	        options:                  # keyed by the option's value, in order
//	          vite: Vite + React
//	          next: {label: Next.js, description: Server rendering}
//
// or `fields:` in place of `sections:` for a form of one page. The JSON array
// of fields of the spec before v0.21 is a form file too.
type Doc struct {
	Title       string
	Description string
	Lang        string
	Fields      json.RawMessage
	// Extra holds the other top-level keys: an example's summary, order and
	// multi. `ask-form create` refuses any.
	Extra map[string]any
}

// DocKeys are the top-level keys a form file takes.
var DocKeys = []string{"title", "description", "lang", "sections", "fields"}

var sectionKeys = []string{"intro", "showIf", "fields"}

// What an agent may write for a section key, and the key it meant.
var sectionHints = map[string]string{"description": "intro", "text": "intro", "help": "intro",
	"group": "the section's key", "name": "the section's key", "label": "the section's key", "title": "the section's key",
	"questions": "fields", "if": "showIf"}

// ParseDoc reads a form file: YAML, a JSON object of the same shape, or the
// JSON array of fields.
func ParseDoc(src []byte) (*Doc, error) {
	trim := bytes.TrimSpace(bytes.TrimPrefix(src, []byte("\uFEFF")))
	if len(trim) == 0 {
		return nil, errors.New("the form file is empty")
	}
	var tree any
	var err error
	if trim[0] == '[' || trim[0] == '{' {
		if !json.Valid(trim) {
			return nil, jsonError(trim)
		}
		if trim[0] == '[' {
			return &Doc{Fields: json.RawMessage(trim)}, nil // as before v0.21, byte for byte
		}
		tree, err = decodeJSON(trim)
	} else {
		tree, err = ParseYAML(trim)
	}
	if err != nil {
		return nil, err
	}
	return docOf(tree)
}

// jsonError says where a JSON file stops being JSON.
func jsonError(b []byte) error {
	var v any
	err := json.Unmarshal(b, &v)
	var se *json.SyntaxError
	if errors.As(err, &se) {
		line := 1 + bytes.Count(b[:se.Offset], []byte("\n"))
		return fmt.Errorf("line %d: not valid JSON: %v", line, se)
	}
	return fmt.Errorf("not valid JSON: %v", err)
}

func docOf(tree any) (*Doc, error) {
	kinds := map[string]string{} // key → type of the questions so far
	if list, ok := tree.([]any); ok {
		fields, err := compileFields(list, "", nil, kinds)
		if err != nil {
			return nil, err
		}
		return marshalDoc(&Doc{}, fields)
	}
	m, ok := tree.(*Map)
	if !ok {
		return nil, errors.New("a form file is a mapping (title, description, sections…) or a list of fields")
	}
	d := &Doc{Extra: map[string]any{}}
	for _, k := range []struct {
		name string
		dst  *string
	}{{"title", &d.Title}, {"description", &d.Description}, {"lang", &d.Lang}} {
		v := m.Get(k.name)
		if v == nil {
			continue
		}
		if !scalar(v) {
			return nil, fmt.Errorf("%s is a text", k.name)
		}
		*k.dst = strings.TrimSpace(str(text(v)))
	}
	for _, k := range m.Keys {
		if !contains(DocKeys, k) {
			d.Extra[k] = plainTree(m.Get(k))
		}
	}
	switch {
	case m.Has("sections") && m.Has("fields"):
		return nil, errors.New("give sections or fields, not both (a section holds its fields)")
	case !m.Has("sections") && !m.Has("fields"):
		return nil, errors.New("the form needs sections (or fields, for a form of one page)")
	case m.Has("fields"):
		fields, err := compileFields(m.Get("fields"), "", nil, kinds)
		if err != nil {
			return nil, err
		}
		return marshalDoc(d, fields)
	}
	secs, ok := m.Get("sections").(*Map)
	if !ok || len(secs.Keys) == 0 {
		return nil, errors.New("sections is a mapping keyed by each page's title:  Verdict: {intro: …, fields: …}")
	}
	var fields []any
	titles := map[string]bool{}
	for _, rawTitle := range secs.Keys {
		title := strings.TrimSpace(rawTitle)
		where := fmt.Sprintf("section %q", title)
		s, ok := secs.Get(rawTitle).(*Map)
		if !ok {
			return nil, fmt.Errorf("%s: a section is a mapping with fields (and intro, showIf)", where)
		}
		for _, k := range s.Keys {
			if contains(sectionKeys, k) {
				continue
			}
			if hint, ok := sectionHints[k]; ok {
				return nil, fmt.Errorf("%s: no key %q in a section, did you mean %s?", where, k, hint)
			}
			return nil, fmt.Errorf("%s: no key %q in a section (%s)", where, k, strings.Join(sectionKeys, ", "))
		}
		switch {
		case title == "":
			return nil, errors.New("a section's title is empty: each section is a page, keyed by its title")
		case len(title) > MaxGroupChars:
			return nil, fmt.Errorf("%s: a title of at most %d characters", where, MaxGroupChars)
		case titles[title]:
			return nil, fmt.Errorf("two sections are titled %q: give each page its own title", title)
		}
		titles[title] = true
		var intro any
		if v := s.Get("intro"); v != nil {
			if !scalar(v) {
				return nil, fmt.Errorf("%s: intro is a markdown text", where)
			}
			intro = str(text(v))
		}
		cond := s.Get("showIf")
		if cond != nil {
			cond = textCond(plainTree(cond), kinds)
		}
		out, err := compileFields(s.Get("fields"), title, cond, kinds)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", where, err)
		}
		if intro != nil {
			block := map[string]any{"type": Markdown, "text": intro}
			if title != "" {
				block["group"] = title
			}
			if cond != nil {
				block["showIf"] = cond
			}
			out = append([]any{block}, out...)
		}
		fields = append(fields, out...)
	}
	return marshalDoc(d, fields)
}

func marshalDoc(d *Doc, fields []any) (*Doc, error) {
	b, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	d.Fields = b
	return d, nil
}

// entries lists a section's fields, a question's options or a table's
// columns: a mapping keyed by their name (the way to write them), or a list
// (the JSON spec's way). name is "key" or "value", what the mapping's keys
// are; a mapping's entry may be a text (its label) or nothing.
func entries(v any, name, what string) ([]any, error) {
	switch t := v.(type) {
	case []any:
		return t, nil
	case *Map:
		out := make([]any, 0, len(t.Keys))
		for _, k := range t.Keys {
			switch e := t.Get(k).(type) {
			case *Map:
				if e.Has(name) {
					return nil, fmt.Errorf("%s %q: its %s is the name above it; remove %s", what, k, name, name)
				}
				m := map[string]any{name: k}
				for _, ek := range e.Keys {
					m[ek] = e.Get(ek) // still ordered: a field's options are read next
				}
				out = append(out, m)
			case nil:
				if what == "field" {
					return nil, fmt.Errorf("field %q has nothing under it: give it a type and a label", k)
				}
				out = append(out, map[string]any{name: k})
			default:
				if !scalar(e) || what == "field" {
					return nil, fmt.Errorf("%s %q: give a mapping under it", what, k)
				}
				out = append(out, map[string]any{name: k, "label": text(e)})
			}
		}
		return out, nil
	case nil:
		return nil, nil
	}
	return nil, fmt.Errorf("%ss are a mapping keyed by %s", what, name)
}

// compileFields readies one section's fields: the page's title as their
// group, its condition AND-ed into theirs, options and columns as lists, and
// numbers back to the text they were written as wherever the spec takes one.
func compileFields(v any, group string, cond any, kinds map[string]string) ([]any, error) {
	list, err := entries(v, "key", "field")
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, errors.New("fields is empty")
	}
	out := make([]any, 0, len(list))
	for i, rf := range list {
		f, ok := rf.(map[string]any)
		if m, isMap := rf.(*Map); isMap { // an item of a list of fields
			f, ok = map[string]any{}, true
			for _, k := range m.Keys {
				f[k] = m.Get(k)
			}
		}
		if !ok {
			return nil, fmt.Errorf("field #%d is a mapping (type, label…)", i+1)
		}
		if f, err = textField(f, kinds); err != nil {
			return nil, err
		}
		if group != "" {
			// A ready-made block of JSON fields pasted into the section
			// named after it keeps its group; any other group is a mistake.
			if g, has := f["group"]; has && g != nil && strings.TrimSpace(str(text(g))) != group {
				return nil, fmt.Errorf("field %s: group %q is not its section's title; remove group (the section sets the page)", fieldName(f, i), str(text(g)))
			}
			f["group"] = group
		}
		if cond != nil {
			if own, has := f["showIf"]; has && own != nil {
				f["showIf"] = map[string]any{"all": []any{cond, own}}
			} else {
				f["showIf"] = cond
			}
		}
		out = append(out, f)
	}
	return out, nil
}

// plainTree turns the ordered mappings into plain ones where order no longer
// matters (a field's own keys, a condition).
func plainTree(v any) any {
	switch t := v.(type) {
	case *Map:
		m := make(map[string]any, len(t.Keys))
		for _, k := range t.Keys {
			m[k] = plainTree(t.Get(k))
		}
		return m
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = plainTree(x)
		}
		return out
	}
	return v
}

func fieldName(f map[string]any, i int) string {
	if k := str(f["key"]); k != "" {
		return fmt.Sprintf("%q", k)
	}
	if l := str(f["label"]); l != "" {
		return fmt.Sprintf("%q", l)
	}
	return fmt.Sprintf("#%d", i+1)
}

// The keys whose value is a text, whatever it looks like.
var (
	textKeys   = []string{"key", "label", "type", "group", "placeholder", "help", "text", "html", "schema", "aspect", "accept", "style", "minLabel", "maxLabel"}
	optionKeys = []string{"value", "label", "description", "image", "html"}
	columnKeys = []string{"key", "label", "type", "placeholder"}
	// A default that is a text, for these types.
	textDefault = []string{Text, Textarea, Select, Radio, Email, Date, Time, Datetime}
	numberTypes = []string{Number, Slider, Rating}
)

func scalar(v any) bool {
	switch v.(type) {
	case nil, string, bool, json.Number:
		return true
	}
	return false
}

// text turns a number back into the text it was written as: a YAML `3.10`
// or `1` in a label stays "3.10", "1".
func text(v any) any {
	if n, ok := v.(json.Number); ok {
		return string(n)
	}
	return v
}

func textList(v any) any {
	list, ok := v.([]any)
	if !ok {
		return text(v)
	}
	out := make([]any, len(list))
	for i, x := range list {
		out[i] = text(x)
	}
	return out
}

// textEntries is entries with the texts of each one's text keys.
func textEntries(v any, name, what string, keys []string) (any, error) {
	if v == nil {
		return nil, nil
	}
	list, err := entries(v, name, what)
	if err != nil {
		return nil, err
	}
	out := make([]any, len(list))
	for i, e := range list {
		m, ok := plainTree(e).(map[string]any)
		if !ok {
			out[i] = text(e)
			continue
		}
		for _, k := range keys {
			if x, has := m[k]; has {
				m[k] = text(x)
			}
		}
		if o, has := m["options"]; has {
			m["options"] = textList(o)
		}
		out[i] = m
	}
	return out, nil
}

func textField(f map[string]any, kinds map[string]string) (map[string]any, error) {
	for k, v := range f {
		if k != "options" && k != "columns" {
			f[k] = plainTree(v)
		}
	}
	for _, k := range textKeys {
		if v, has := f[k]; has {
			f[k] = text(v)
		}
	}
	ftype := strings.ToLower(strings.TrimSpace(str(f["type"])))
	if ftype == "" {
		ftype = Text
	}
	name := fieldName(f, 0)
	var err error
	if v, has := f["options"]; has {
		if f["options"], err = textEntries(v, "value", "option", optionKeys); err != nil {
			return nil, fmt.Errorf("field %s: %w", name, err)
		}
	}
	if v, has := f["columns"]; has {
		if ftype != List {
			f["columns"] = plainTree(v) // a picture grid's column count
		} else if f["columns"], err = textEntries(v, "key", "column", columnKeys); err != nil {
			return nil, fmt.Errorf("field %s: %w", name, err)
		}
	}
	if d, has := f["default"]; has {
		switch {
		case contains(textDefault, ftype):
			f["default"] = text(d)
		case ftype == Multiselect || ftype == List:
			f["default"] = textList(d)
		}
	}
	for _, k := range []string{"showIf", "requiredIf"} {
		if c, has := f[k]; has && c != nil {
			f[k] = textCond(c, kinds)
		}
	}
	if key := str(f["key"]); key != "" {
		kinds[slug(key)] = ftype
	} else if l := str(f["label"]); l != "" {
		kinds[slug(l)] = ftype
	}
	return f, nil
}

// textCond gives a test's value the type its question answers in: `equals:
// 2` on a radio whose option is "2" compares texts.
func textCond(c any, kinds map[string]string) any {
	m, ok := c.(map[string]any)
	if !ok {
		return c
	}
	for _, k := range []string{"all", "any"} {
		if list, ok := m[k].([]any); ok {
			for i, x := range list {
				list[i] = textCond(x, kinds)
			}
		}
	}
	if not, ok := m["not"]; ok {
		m["not"] = textCond(not, kinds)
	}
	key := str(text(m["key"]))
	if key == "" || contains(numberTypes, kinds[slug(key)]) {
		return m
	}
	for _, op := range []string{OpEquals, OpNotEquals, OpIncludes} {
		if v, has := m[op]; has {
			m[op] = text(v)
		}
	}
	if v, has := m[OpIn]; has {
		m[OpIn] = textList(v)
	}
	return m
}
