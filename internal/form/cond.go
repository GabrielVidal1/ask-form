package form

import (
	"fmt"
	"strings"
)

// Cond is a condition on earlier answers, the value of a field's `showIf`
// (the field is drawn, and counted, only while it holds) or `requiredIf` (the
// field is required while it holds). It is a leaf — one question, one test —
// or an `all` / `any` / `not` of other conditions.
//
// Spec shapes (what the agent writes):
//
//	{"key":"stack","equals":"Next"}
//	{"key":"features","includes":"auth"}
//	{"key":"budget","gt":100}
//	{"all":[{"key":"a","answered":true},{"not":{"key":"b","equals":"x"}}]}
type Cond struct {
	Key string  `json:"key,omitempty"`
	Op  string  `json:"op,omitempty"`
	Val any     `json:"value,omitempty"`
	All []*Cond `json:"all,omitempty"`
	Any []*Cond `json:"any,omitempty"`
	Not *Cond   `json:"not,omitempty"`
}

// Leaf operators. A test on a question the respondent has not answered — or
// that is itself hidden — fails, except notEquals and `answered: false`.
const (
	OpEquals    = "equals"
	OpNotEquals = "notEquals"
	OpIn        = "in"       // the answer is one of a list
	OpIncludes  = "includes" // a multi-answer holds the value; a text contains it
	OpGt        = "gt"
	OpGte       = "gte"
	OpLt        = "lt"
	OpLte       = "lte"
	OpAnswered  = "answered"
)

var condOps = []string{OpEquals, OpNotEquals, OpIn, OpIncludes, OpGt, OpGte, OpLt, OpLte, OpAnswered}

// Limits on a condition's size and depth.
const (
	maxCondDepth  = 4
	maxCondLeaves = 24
)

// parseCond reads a condition from the spec. known maps the keys of the
// fields before this one to their types: a condition looks backwards only,
// so the server can decide what is hidden in one pass.
func parseCond(v any, known map[string]string) (*Cond, error) {
	leaves := 0
	c, err := condNode(v, known, 1, &leaves)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func condNode(v any, known map[string]string, depth int, leaves *int) (*Cond, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("a condition is an object like {\"key\":\"q\",\"equals\":\"yes\"}")
	}
	if depth > maxCondDepth {
		return nil, fmt.Errorf("conditions nest at most %d deep", maxCondDepth)
	}
	c := &Cond{}
	groups := 0
	for _, name := range []string{"all", "any"} {
		raw, has := m[name]
		if !has {
			continue
		}
		groups++
		items, ok := raw.([]any)
		if !ok || len(items) == 0 {
			return nil, fmt.Errorf("%q takes a non-empty list of conditions", name)
		}
		for _, it := range items {
			sub, err := condNode(it, known, depth+1, leaves)
			if err != nil {
				return nil, err
			}
			if name == "all" {
				c.All = append(c.All, sub)
			} else {
				c.Any = append(c.Any, sub)
			}
		}
	}
	if raw, has := m["not"]; has {
		groups++
		sub, err := condNode(raw, known, depth+1, leaves)
		if err != nil {
			return nil, err
		}
		c.Not = sub
	}
	if groups > 1 {
		return nil, fmt.Errorf("use one of all / any / not per condition")
	}
	if groups == 1 {
		if m["key"] != nil {
			return nil, fmt.Errorf("a condition is either a test on a key or an all / any / not")
		}
		return c, nil
	}
	// A leaf: key + exactly one operator.
	*leaves++
	if *leaves > maxCondLeaves {
		return nil, fmt.Errorf("at most %d tests in one condition", maxCondLeaves)
	}
	key := slug(str(m["key"]))
	if key == "" {
		return nil, fmt.Errorf("a test needs the key of an earlier question")
	}
	kind, ok := known[key]
	if !ok {
		return nil, fmt.Errorf("%q is not a question above this one", key)
	}
	if kind == Secret || kind == Markdown || kind == HTML || kind == Diff || kind == Details {
		return nil, fmt.Errorf("%q (%s) can't be tested", key, kind)
	}
	var ops []string
	for _, op := range condOps {
		if _, has := m[op]; has {
			ops = append(ops, op)
		}
	}
	if len(ops) != 1 {
		return nil, fmt.Errorf("test on %q needs exactly one of %s", key, strings.Join(condOps, ", "))
	}
	op, val := ops[0], m[ops[0]]
	switch op {
	case OpIn:
		list, ok := val.([]any)
		if !ok || len(list) == 0 {
			return nil, fmt.Errorf("%q on %q takes a non-empty list", op, key)
		}
	case OpGt, OpGte, OpLt, OpLte:
		if _, ok := val.(float64); !ok {
			return nil, fmt.Errorf("%q on %q takes a number", op, key)
		}
	case OpAnswered:
		if _, ok := val.(bool); !ok {
			return nil, fmt.Errorf("%q on %q takes true or false", op, key)
		}
	default:
		switch val.(type) {
		case string, float64, bool:
		default:
			return nil, fmt.Errorf("%q on %q takes a text, number or true / false", op, key)
		}
	}
	c.Key, c.Op, c.Val = key, op, val
	return c, nil
}

// Eval reports whether the condition holds. get returns an earlier
// question's answer (nil when it is unanswered) and false when the question is
// hidden — a test on a hidden question fails, so hiding cascades to what hangs
// on it.
func (c *Cond) Eval(get func(key string) (any, bool)) bool {
	switch {
	case c == nil:
		return true
	case len(c.All) > 0:
		for _, s := range c.All {
			if !s.Eval(get) {
				return false
			}
		}
		return true
	case len(c.Any) > 0:
		for _, s := range c.Any {
			if s.Eval(get) {
				return true
			}
		}
		return false
	case c.Not != nil:
		return !c.Not.Eval(get)
	}
	v, visible := get(c.Key)
	if !visible {
		return false
	}
	switch c.Op {
	case OpAnswered:
		return answered(v) == c.Val.(bool)
	case OpNotEquals:
		return !equalValue(v, c.Val)
	case OpEquals:
		return equalValue(v, c.Val)
	case OpIn:
		for _, x := range c.Val.([]any) {
			if equalValue(v, x) {
				return true
			}
		}
		return false
	case OpIncludes:
		return includesValue(v, c.Val)
	case OpGt, OpGte, OpLt, OpLte:
		n, ok := v.(float64)
		if !ok {
			return false
		}
		t := c.Val.(float64)
		switch c.Op {
		case OpGt:
			return n > t
		case OpGte:
			return n >= t
		case OpLt:
			return n < t
		}
		return n <= t
	}
	return false
}

// answered reports whether an answer counts as given: present, and not an
// empty list or an unticked checkbox.
func answered(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case []string:
		return len(t) > 0
	case []any:
		return len(t) > 0
	case []map[string]any:
		return len(t) > 0
	}
	return true
}

func equalValue(a, b any) bool {
	if a == nil {
		return false
	}
	switch x := a.(type) {
	case string:
		y, ok := b.(string)
		return ok && x == y
	case float64:
		y, ok := b.(float64)
		return ok && x == y
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	}
	return false
}

// includesValue: a list answer holds the value, or a text answer contains it
// (case-insensitively).
func includesValue(v, want any) bool {
	w := str(want)
	switch t := v.(type) {
	case string:
		return w != "" && strings.Contains(strings.ToLower(t), strings.ToLower(w))
	case []string:
		return contains(t, w)
	case []any:
		for _, x := range t {
			if str(x) == w {
				return true
			}
		}
	}
	return false
}
