package form

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const reviewDoc = `title: Review a change
description: |
  Read the diff, give a verdict.
lang: fr
sections:
  The change:
    fields:
      why:
        type: details
        label: Why
        text: Because.
  Verdict:
    intro: Only what the verdict needs.
    fields:
      verdict:
        type: radio
        label: Verdict
        required: true
        default: approve
        options:
          reject: {label: Reject, description: Don't merge}
          approve: Approve
          "3.10":
      requested:
        type: list
        label: Requested changes
        showIf: {key: verdict, equals: approve}
        columns:
          where: {label: Where, placeholder: file}
          what: What to change
          severity: {type: select, options: [nit, should, must]}
  Rollout:
    showIf: {key: verdict, in: [approve, 3.10]}
    fields:
      window: {type: time, label: Starts at, default: 22:00}
      tell: {type: checkbox, label: Tell the team, showIf: {key: verdict, notEquals: reject}}
`

func TestParseDocSections(t *testing.T) {
	d, err := ParseDoc([]byte(reviewDoc))
	if err != nil {
		t.Fatal(err)
	}
	if d.Title != "Review a change" || d.Description != "Read the diff, give a verdict." || d.Lang != "fr" {
		t.Fatalf("head = %q %q %q", d.Title, d.Description, d.Lang)
	}
	fields, err := NormalizeIn(d.Fields, "en")
	if err != nil {
		t.Fatalf("NormalizeIn: %v\n%s", err, d.Fields)
	}
	var keys, groups []string
	for _, f := range fields {
		keys = append(keys, f.Key)
		groups = append(groups, f.Group)
	}
	if got := strings.Join(keys, " "); got != "why markdown-2 verdict requested window tell anything_else" {
		t.Errorf("keys in order = %s", got)
	}
	if got := strings.Join(groups, "|"); got != "The change|Verdict|Verdict|Verdict|Rollout|Rollout|" {
		t.Errorf("groups = %s", got)
	}
	if f := fields[1]; f.Type != Markdown || f.Text != "Only what the verdict needs." {
		t.Errorf("intro = %+v", f)
	}
	v := fields[2]
	var opts []string
	for _, o := range v.Options {
		opts = append(opts, o.Value+"="+o.Label)
	}
	if got := strings.Join(opts, ","); got != "reject=Reject,approve=Approve,3.10=3.10" {
		t.Errorf("options in order = %s", got)
	}
	if v.Options[0].Description != "Don't merge" || v.Default != "approve" {
		t.Errorf("verdict = %+v", v)
	}
	var cols []string
	for _, c := range fields[3].Cols {
		cols = append(cols, c.Key+"="+c.Label+"/"+c.Type)
	}
	if got := strings.Join(cols, ","); got != "where=Where/text,what=What to change/text,severity=severity/select" {
		t.Errorf("columns = %s", got)
	}
	if w := fields[4]; w.Default != "22:00" || w.ShowIf == nil || w.ShowIf.Op != OpIn {
		t.Errorf("window = %+v (showIf %+v)", w, w.ShowIf)
	} else if vals, _ := w.ShowIf.Val.([]any); len(vals) != 2 || vals[1] != "3.10" {
		t.Errorf("a test on a radio compares texts: %#v", w.ShowIf.Val)
	}
	if c := fields[5].ShowIf; c == nil || len(c.All) != 2 || c.All[0].Op != OpIn || c.All[1].Op != OpNotEquals {
		t.Errorf("the page's showIf is AND-ed with the field's own: %+v", c)
	}
}

func TestParseDocFlatJSONUnchanged(t *testing.T) {
	src := `[{"key":"stack","type":"radio","options":["Vite","Next"],"group":"A"}]`
	d, err := ParseDoc([]byte("\n" + src + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if string(d.Fields) != src || d.Title != "" {
		t.Errorf("fields = %s", d.Fields)
	}
}

func TestParseDocJSONObjectKeepsOrder(t *testing.T) {
	d, err := ParseDoc([]byte(`{"title":"T","fields":{"z":{"type":"radio","options":{"b":"B","a":"A"}},"a":{"label":"Name"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	fields, err := Normalize(d.Fields)
	if err != nil {
		t.Fatal(err)
	}
	if fields[0].Key != "z" || fields[1].Key != "a" || fields[0].Options[0].Value != "b" {
		t.Errorf("order lost: %s", d.Fields)
	}
}

func TestParseDocLists(t *testing.T) {
	d, err := ParseDoc([]byte("fields:\n  - key: stack\n    type: radio\n    options: [Vite, Next]\n  - {key: name}\n"))
	if err != nil {
		t.Fatal(err)
	}
	fields, err := Normalize(d.Fields)
	if err != nil {
		t.Fatal(err)
	}
	if fields[0].Key != "stack" || len(fields[0].Options) != 2 || fields[1].Key != "name" {
		t.Errorf("fields = %s", d.Fields)
	}
}

func TestParseDocRefuses(t *testing.T) {
	for _, c := range []struct{ src, msg string }{
		{"", "empty"},
		{"[1, 2", "line 1: not valid JSON"},
		{"title: T\n", "needs sections"},
		{"title: T\nfields: {a: {label: A}}\nsections: {}\n", "not both"},
		{"sections:\n  - title: A\n    fields: {a: {label: A}}\n", "keyed by each page's title"},
		{"sections:\n  \"\": {fields: {a: {label: A}}}\n", "title is empty"},
		{"sections:\n  A: {fields: {a: {label: A}}}\n  \"A \": {fields: {b: {label: B}}}\n", "two sections are titled"},
		{"sections:\n  A:\n    description: x\n    fields: {a: {label: A}}\n", "did you mean intro"},
		{"sections:\n  A:\n    title: x\n    fields: {a: {label: A}}\n", "did you mean the section's key"},
		{"sections:\n  A: {fields: {}}\n", "fields is empty"},
		{"sections:\n  A: {fields: {a: {label: A, group: B}}}\n", "remove group"},
		{"sections:\n  A:\n    fields: [{\"key\": \"a\", \"group\": \"B\"}]\n", "is not its section's title"},
		{"sections:\n  A: x\n", "a section is a mapping"},
		{"fields:\n  a:\n    key: b\n", "its key is the name above it"},
		{"fields:\n  a:\n", "nothing under it"},
		{"fields:\n  a: Name\n", "give a mapping"},
		{"fields:\n  a:\n    type: radio\n    options:\n      x: {value: y}\n", "its value is the name above it"},
		{"fields:\n  a: {type: radio, options: {x: [1]}}\n", "give a mapping"},
		{"- a\n- b\n", "field #1 is a mapping"},
		{"just text\n", "a form file is a mapping"},
	} {
		_, err := ParseDoc([]byte(c.src))
		if err == nil || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("%q: err = %v, want %q", c.src, err, c.msg)
		}
	}
}

// A page's condition joins a field's own one level deeper; the limit is still
// Normalize's.
func TestParseDocShowIfDepth(t *testing.T) {
	d, err := ParseDoc([]byte("sections:\n  A:\n    fields: {a: {type: checkbox}}\n" +
		"  B:\n    showIf: {key: a, equals: true}\n    fields:\n      b: {showIf: {not: {key: a, equals: false}}}\n"))
	if err != nil {
		t.Fatal(err)
	}
	var raw []map[string]any
	json.Unmarshal(d.Fields, &raw)
	if _, ok := raw[1]["showIf"].(map[string]any)["all"]; !ok {
		t.Errorf("showIf = %v", raw[1]["showIf"])
	}
	if _, err := Normalize(d.Fields); err != nil {
		t.Error(err)
	}
}

// The YAML in the skill is what agents copy: every block reads, and the whole
// forms in it (the Ask one, the form file one) are forms.
func TestSkillYAML(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "skill", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	blocks := strings.Split(string(b), "```yaml\n")[1:]
	if len(blocks) < 4 {
		t.Fatalf("%d yaml blocks in SKILL.md", len(blocks))
	}
	forms := 0
	for _, blk := range blocks {
		src := blk[:strings.Index(blk, "```")]
		if _, err := ParseYAML([]byte(src)); err != nil {
			t.Errorf("SKILL.md block %.40q: %v", src, err)
			continue
		}
		if !strings.HasPrefix(src, "title:") {
			continue // a fragment: one field, a condition
		}
		forms++
		d, err := ParseDoc([]byte(src))
		if err != nil {
			t.Errorf("SKILL.md form %.40q: %v", src, err)
			continue
		}
		if _, err := Normalize(d.Fields); err != nil {
			t.Errorf("SKILL.md form %.40q: %v", src, err)
		}
	}
	if forms < 1 {
		t.Errorf("no whole form in SKILL.md's yaml blocks")
	}
}

// A ready-made JSON block (What's new) goes in verbatim as the section named
// after its group: JSON is YAML, over several lines at any indentation.
func TestParseDocPastedJSONBlock(t *testing.T) {
	src := "title: T\nsections:\n  Mine:\n    fields:\n      a: {type: checkbox}\n  What's new:\n    fields: [\n" +
		`{"type": "markdown", "group": "What's new", "text": "Keep it?"},` + "\n" +
		`{"key": "add", "type": "checkbox", "group": "What's new", "default": true, "showIf": {"key": "a", "equals": true}}` + "\n]\n"
	d, err := ParseDoc([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	fields, err := Normalize(d.Fields)
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 4 || fields[2].Key != "add" || fields[2].Group != "What's new" || fields[2].ShowIf == nil {
		t.Errorf("fields = %s", d.Fields)
	}
}
