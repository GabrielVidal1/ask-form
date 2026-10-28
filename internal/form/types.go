package form

import (
	"fmt"
	"math"
	"strings"
)

// listSpec reads a list field's own settings: its `columns` (none = a plain
// list of texts, else a table whose rows are objects) and the row bounds
// `min` / `max`.
func listSpec(f *Field, m map[string]any) error {
	if raw, has := m["columns"]; has && raw != nil {
		cols, ok := raw.([]any)
		if !ok {
			return fmt.Errorf("field %q: columns must be a list of {key,label,type}", f.Key)
		}
		if len(cols) > MaxTableCols {
			return fmt.Errorf("field %q: at most %d columns", f.Key, MaxTableCols)
		}
		seen := map[string]bool{}
		for n, rc := range cols {
			cm, ok := rc.(map[string]any)
			if !ok {
				return fmt.Errorf("field %q: column #%d must be an object", f.Key, n)
			}
			c := Column{
				Label:       clip(strings.TrimSpace(str(cm["label"])), MaxLabelChars),
				Placeholder: clip(strings.TrimSpace(str(cm["placeholder"])), MaxLabelChars),
				Type:        strings.ToLower(strings.TrimSpace(str(cm["type"]))),
			}
			c.Key = slug(str(cm["key"]))
			if c.Key == "" {
				c.Key = slug(c.Label)
			}
			if c.Key == "" {
				return fmt.Errorf("field %q: column #%d needs a key or a label", f.Key, n)
			}
			if c.Label == "" {
				c.Label = c.Key
			}
			if seen[c.Key] {
				return fmt.Errorf("field %q: column %q appears twice", f.Key, c.Key)
			}
			seen[c.Key] = true
			switch c.Type {
			case "":
				c.Type = ColText
			case ColText, ColNumber:
			case ColSelect:
				opts, _ := cm["options"].([]any)
				for _, o := range opts {
					if s := clip(strings.TrimSpace(str(o)), MaxLabelChars); s != "" && !contains(c.Options, s) {
						c.Options = append(c.Options, s)
					}
				}
				if len(c.Options) < 2 || len(c.Options) > MaxOptions {
					return fmt.Errorf("field %q: select column %q needs 2–%d options", f.Key, c.Key, MaxOptions)
				}
			default:
				return fmt.Errorf("field %q: column %q: type %q is not text, number or select", f.Key, c.Key, c.Type)
			}
			f.Cols = append(f.Cols, c)
		}
	}
	for k, dst := range map[string]**float64{"min": &f.Min, "max": &f.Max} {
		if v, ok := num(m[k]); ok {
			v = math.Trunc(v)
			*dst = &v
		}
	}
	if f.Min != nil && *f.Min < 0 {
		f.Min = ptr(0)
	}
	if f.Max == nil || *f.Max > MaxListRows {
		f.Max = ptr(MaxListRows)
	}
	if f.Min != nil && *f.Min > *f.Max {
		return fmt.Errorf("field %q: min > max", f.Key)
	}
	return nil
}

// ratingSpec reads a rating's scale: `min` (default 1) to `max` (default 5),
// at most 11 steps, drawn as stars or numbers.
func ratingSpec(f *Field, m map[string]any) error {
	lo, hi := 1.0, 5.0
	if v, ok := num(m["min"]); ok {
		lo = math.Trunc(v)
	}
	if v, ok := num(m["max"]); ok {
		hi = math.Trunc(v)
	}
	if lo < 0 || hi <= lo || hi-lo > 10 {
		return fmt.Errorf("field %q: a rating runs from min (>= 0) to max, at most 11 steps", f.Key)
	}
	f.Min, f.Max = &lo, &hi
	f.Style = strings.ToLower(strings.TrimSpace(str(m["style"])))
	switch f.Style {
	case "stars", "numbers":
	case "":
		f.Style = "numbers"
		if lo == 1 && hi <= 7 {
			f.Style = "stars"
		}
	default:
		return fmt.Errorf("field %q: style %q is not stars or numbers", f.Key, f.Style)
	}
	f.MinLabel = clip(strings.TrimSpace(str(m["minLabel"])), MaxLabelChars)
	f.MaxLabel = clip(strings.TrimSpace(str(m["maxLabel"])), MaxLabelChars)
	return nil
}
