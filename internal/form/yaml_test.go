package form

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func yamlJSON(t *testing.T, src string) string {
	t.Helper()
	v, err := ParseYAML([]byte(src))
	if err != nil {
		t.Fatalf("ParseYAML(%q): %v", src, err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestYAMLReads(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{"mapping keeps order", "b: 1\na: 2\nc: 3\n", `{"b":1,"a":2,"c":3}`},
		{"nested", "a:\n  b:\n    c: x\n  d: y\n", `{"a":{"b":{"c":"x"},"d":"y"}}`},
		{"list", "- a\n- b\n", `["a","b"]`},
		{"list at its key's indentation", "a:\n- x\n- y\nb: 1\n", `{"a":["x","y"],"b":1}`},
		{"list of mappings", "- key: a\n  type: radio\n- key: b\n", `[{"key":"a","type":"radio"},{"key":"b"}]`},
		{"list in a list", "- - a\n  - b\n- c\n", `[["a","b"],"c"]`},
		{"empty value", "a:\nb: 1\n", `{"a":null,"b":1}`},
		{"literal", "t: |\n  line 1\n    more\n\n  line 3\nu: 2\n", `{"t":"line 1\n  more\n\nline 3\n","u":2}`},
		{"literal strip", "t: |-\n  a\n  b\n\n\nn: 1\n", `{"t":"a\nb","n":1}`},
		{"literal keep", "t: |+\n  a\n\n\nn: 1\n", `{"t":"a\n\n\n","n":1}`},
		{"literal with an indentation indicator", "t: |2\n   leading space\n  x\n", `{"t":" leading space\nx\n"}`},
		{"literal keeps # and tabs", "d: |\n  # kept\n  -\told\n  +\tnew\n", `{"d":"# kept\n-\told\n+\tnew\n"}`},
		{"folded", "t: >\n  a\n  b\n\n  c\n    d\n  e\n", `{"t":"a b\nc\n  d\ne\n"}`},
		{"folded strip", "t: >-\n  one\n  two\n", `{"t":"one two"}`},
		{"plain over lines", "help: This is\n  continued here\n\n  new para\nk: v\n", `{"help":"This is continued here\nnew para","k":"v"}`},
		{"double quotes", `a: "x \"q\" \u00e9 \n end"` + "\n", `{"a":"x \"q\" é \n end"}`},
		{"single quotes", "b: 'it''s'\n", `{"b":"it's"}`},
		{"quoted over lines", "a: \"one\n  two\n\n  three\"\nb: 1\n", `{"a":"one two\nthree","b":1}`},
		{"escaped line break", "a: \"one\\\n  two\"\n", `{"a":"onetwo"}`},
		{"flow over lines", "o: [a, b,\n  c]\nm: {k: v,\n  n: [1, 2]}\n", `{"o":["a","b","c"],"m":{"k":"v","n":[1,2]}}`},
		{"flow trailing comma", "a: [x, y, ]\n", `{"a":["x","y"]}`},
		{"JSON is YAML", `{"a": [1, 2.5, -3e2], "b": {"c": null, "d": true}}`, `{"a":[1,2.5,-3e2],"b":{"c":null,"d":true}}`},
		{"types", "a: yes\nb: no\nc: 22:00\nd: 3.10\ne: 0x1F\nf: ~\nh: true\ni: .5\nj: +1\nk: 007\n",
			`{"a":"yes","b":"no","c":"22:00","d":3.10,"e":"0x1F","f":null,"h":true,"i":".5","j":"+1","k":"007"}`},
		{"comments", "# top\na: 1 # c\nb: \"x # not\" # c\nc: C#\n", `{"a":1,"b":"x # not","c":"C#"}`},
		{"a second : in a value", "help: Note: something here\n", `{"help":"Note: something here"}`},
		{"url", "u: https://x.y/z?a=b\n", `{"u":"https://x.y/z?a=b"}`},
		{"quoted keys", "\"a b\": 1\n'c': 2\n", `{"a b":1,"c":2}`},
		{"keys are text", "yes: 1\n1: a\n3.10: b\n", `{"yes":1,"1":"a","3.10":"b"}`},
		{"document start", "---\na: 1\n", `{"a":1}`},
		{"CRLF", "a: 1\r\nb: two\r\n", `{"a":1,"b":"two"}`},
		{"block then comment", "a: |\n  x\n# comment\nb: 2\n", `{"a":"x\n","b":2}`},
		{"mapping in a list item", "- title: T\n  fields:\n    k: {type: radio, options: {a: A, b: B}}\n",
			`[{"title":"T","fields":{"k":{"type":"radio","options":{"a":"A","b":"B"}}}}]`},
	} {
		if got := yamlJSON(t, c.src); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
}

func TestYAMLRefuses(t *testing.T) {
	for _, c := range []struct {
		src  string
		line int
		msg  string
	}{
		{"", 1, "empty"},
		{"a: 1\na: 2\n", 2, `key "a" appears twice`},
		{"a: &x 1\n", 1, "anchors"},
		{"a: *x\n", 1, "anchors"},
		{"a: !!str 1\n", 1, "tags"},
		{"? a\n: b\n", 1, "? keys"},
		{"a: 1\n---\nb: 2\n", 2, "one document"},
		{"a:\n\tb: 1\n", 2, "tabs"},
		{"a: 1\n  b: 2\n", 2, "unexpected"},
		{"a: [1, 2\n", 1, "never closed"},
		{"a: \"open\n", 1, "quote opened here is never closed"},
		{"a: {b c}\n", 1, "expected :"},
		{"a: [b: c]\n", 1, "needs { }"},
		{"a: b\n- c\n", 2, "list item where a key"},
		{"a: - b\n", 1, "list starts on the line after"},
		{"a: |x\n  b\n", 1, "only -, +"},
		{"a: \"\\q\"\n", 1, "unknown escape"},
		{"a: b\nc\n", 2, "expected key: value"},
		{"x: " + strings.Repeat("[", 70) + strings.Repeat("]", 70) + "\n", 1, "nested more than"},
	} {
		_, err := ParseYAML([]byte(c.src))
		var ye *YAMLError
		if !errors.As(err, &ye) {
			t.Errorf("%q: err = %v, want a YAMLError", c.src, err)
			continue
		}
		if ye.Line != c.line || !strings.Contains(ye.Msg, c.msg) {
			t.Errorf("%q: got line %d %q, want line %d with %q", c.src, ye.Line, ye.Msg, c.line, c.msg)
		}
	}
}
