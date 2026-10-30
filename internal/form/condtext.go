package form

import (
	"strings"

	"github.com/GabrielVidal1/ask-form/internal/i18n"
)

// CondText is a condition in words, for the note a printed page puts above a
// question it shows only sometimes: `“Needs login” is Yes`. fields are the
// form's: a test names its question by label and an option by its label.
func CondText(lang string, c *Cond, fields []Field) string {
	if c == nil {
		return ""
	}
	byKey := make(map[string]Field, len(fields))
	for _, f := range fields {
		byKey[f.Key] = f
	}
	return condText(lang, c, byKey, false)
}

func condText(lang string, c *Cond, byKey map[string]Field, nested bool) string {
	t := func(s string, a ...any) string { return i18n.T(lang, s, a...) }
	join := func(subs []*Cond, sep string) string {
		parts := make([]string, len(subs))
		for i, s := range subs {
			parts[i] = condText(lang, s, byKey, true)
		}
		out := strings.Join(parts, sep)
		if nested && len(parts) > 1 {
			out = "(" + out + ")"
		}
		return out
	}
	switch {
	case len(c.All) > 0:
		return join(c.All, t(" and "))
	case len(c.Any) > 0:
		return join(c.Any, t(" or "))
	case c.Not != nil:
		if c.Not.Key != "" {
			if flipped, ok := negate(*c.Not); ok {
				return condText(lang, &flipped, byKey, nested)
			}
		}
		return t("not %s", condText(lang, c.Not, byKey, true))
	}
	f, ok := byKey[c.Key]
	label := f.Label
	if !ok || label == "" {
		label = c.Key
	}
	val := func(v any) string { return condValue(lang, f, v) }
	switch c.Op {
	case OpAnswered:
		if b, _ := c.Val.(bool); b {
			return t("“%s” is answered", label)
		}
		return t("“%s” is left blank", label)
	case OpEquals, OpNotEquals:
		if b, isBool := c.Val.(bool); isBool && f.Type == Checkbox {
			if b == (c.Op == OpEquals) {
				return t("“%s” is ticked", label)
			}
			return t("“%s” is not ticked", label)
		}
		if c.Op == OpNotEquals {
			return t("“%s” is not %s", label, val(c.Val))
		}
		return t("“%s” is %s", label, val(c.Val))
	case OpIn:
		list, _ := c.Val.([]any)
		vals := make([]string, len(list))
		for i, v := range list {
			vals[i] = val(v)
		}
		return t("“%s” is %s", label, strings.Join(vals, t(" or ")))
	case OpIncludes:
		if f.Type == Multiselect || f.Type == List {
			return t("“%s” includes %s", label, val(c.Val))
		}
		return t("“%s” contains “%s”", label, val(c.Val))
	case OpGt:
		return t("“%s” is more than %s", label, val(c.Val))
	case OpGte:
		return t("“%s” is at least %s", label, val(c.Val))
	case OpLt:
		return t("“%s” is less than %s", label, val(c.Val))
	case OpLte:
		return t("“%s” is at most %s", label, val(c.Val))
	}
	return label
}

// negate is a leaf's opposite where it has one in words.
func negate(c Cond) (Cond, bool) {
	switch c.Op {
	case OpEquals:
		c.Op = OpNotEquals
	case OpNotEquals:
		c.Op = OpEquals
	case OpAnswered:
		b, _ := c.Val.(bool)
		c.Val = !b
	default:
		return c, false
	}
	return c, true
}

// condValue is a tested value as the respondent knows it: an option's label,
// a number as written, yes / no.
func condValue(lang string, f Field, v any) string {
	switch x := v.(type) {
	case bool:
		if x {
			return i18n.T(lang, "Yes")
		}
		return i18n.T(lang, "No")
	case float64:
		return FormatNumber(x)
	case string:
		if f.Choice() {
			return f.OptionLabel(x)
		}
		return x
	}
	return ""
}
