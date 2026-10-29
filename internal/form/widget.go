package form

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

// A widget is a question answered by the agent's own control: HTML and
// script the agent wrote, run in a sandboxed frame (no network, opaque
// origin), that hands the page one JSON value (`askForm.setValue(v)`). The
// value travels as a JSON string in the form post and comes back to the agent
// decoded.

// MaxWidgetValue caps a widget's answer, as the JSON it is sent in.
const MaxWidgetValue = 16 << 10

// widgetSchemas are the JSON kinds a widget's `schema` may demand.
var widgetSchemas = []string{"string", "number", "boolean", "object", "array"}

// normalizeWidget reads what is specific to a widget field: its markup, the
// kind of value it returns, its starting height and its default.
func normalizeWidget(f *Field, m map[string]any) error {
	src := strings.TrimSpace(str(m["html"]))
	if src == "" {
		return errors.New(`widget needs a non-empty "html"`)
	}
	if len(src) > MaxBlockChars {
		return fmt.Errorf("widget html exceeds %d KB", MaxBlockChars/1024)
	}
	f.HTML = src
	if h, ok := num(m["height"]); ok && h > 0 {
		f.Height = int(math.Min(h, 4000))
	}
	if s := strings.ToLower(strings.TrimSpace(str(m["schema"]))); s != "" {
		if !contains(widgetSchemas, s) {
			return fmt.Errorf("schema %q is not one of %s", s, strings.Join(widgetSchemas, ", "))
		}
		f.Schema = s
	}
	if d, ok := m["default"]; ok && d != nil {
		b, err := json.Marshal(d)
		if err != nil || len(b) > MaxWidgetValue {
			return fmt.Errorf("default is over %d KB", MaxWidgetValue>>10)
		}
		if err := checkSchema(f.Schema, d); err != nil {
			return fmt.Errorf("default: %w", err)
		}
	}
	return nil
}

// checkSchema reports whether a decoded JSON value is of the kind a widget's
// schema asks for ("" = any).
func checkSchema(schema string, v any) error {
	ok := true
	switch schema {
	case "string":
		_, ok = v.(string)
	case "number":
		_, ok = v.(float64)
	case "boolean":
		_, ok = v.(bool)
	case "object":
		_, ok = v.(map[string]any)
	case "array":
		_, ok = v.([]any)
	}
	if !ok {
		return fmt.Errorf("must be a JSON %s", schema)
	}
	return nil
}

// DefaultJSON is a widget's default as the JSON the page starts with ("" when
// it has none).
func DefaultJSON(f Field) string {
	if f.Default == nil {
		return ""
	}
	b, err := json.Marshal(f.Default)
	if err != nil {
		return ""
	}
	return string(b)
}

// parseWidget reads a widget's posted value: nil with no message = not
// answered. A string widget also takes plain text that isn't JSON, which is
// what someone typing into the no-JS textarea gives.
func parseWidget(f Field, raw string, t func(string, ...any) string) (value any, msg string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ""
	}
	if len(raw) > MaxWidgetValue {
		return nil, t("That value is over %d KB.", MaxWidgetValue>>10)
	}
	var v any
	err := json.Unmarshal([]byte(raw), &v)
	if f.Schema == "string" {
		if s, ok := v.(string); err != nil || !ok {
			v = raw
		} else {
			v = s
		}
	} else if err != nil {
		return nil, t("That value isn't valid JSON.")
	}
	if v == nil {
		return nil, ""
	}
	if err := checkSchema(f.Schema, v); err != nil {
		return nil, t("The value %s.", err.Error())
	}
	return v, ""
}

// widgetText is a widget's answer as one line for a recap.
func widgetText(v any) string {
	s, ok := v.(string)
	if !ok {
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		s = string(b)
	}
	if utf8.RuneCountInString(s) > 200 {
		return string([]rune(s)[:200]) + "…"
	}
	return s
}
