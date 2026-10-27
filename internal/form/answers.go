package form

import (
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Input names on the answer page: `f.<key>` carries a field's value and
// `o.<key>` the free text typed next to its "Other…" choice. Keys are
// [a-z0-9_-], so the names need no escaping.
func InputName(key string) string { return "f." + key }

// OtherName is the input name of a field's "Other…" free text.
func OtherName(key string) string { return "o." + key }

// SecretPlaceholder is what the answers keep in place of a secret's value.
func SecretPlaceholder(value string) map[string]any {
	return map[string]any{"secret": true, "chars": utf8.RuneCountInString(value)}
}

// Result is a parsed submission: the answers keyed by field key (secrets as
// placeholders), the secret values apart, and one message per invalid field.
type Result struct {
	Answers map[string]any
	Secrets map[string]string
	Errors  map[string]string
}

// OK reports whether the submission is valid.
func (r Result) OK() bool { return len(r.Errors) == 0 }

// Parse reads a submitted HTML form against the spec — strict: required
// fields, option membership (or the field's "Other…" text), numbers within
// min/max, ISO dates, length caps, one value per single-valued field.
//
// Shapes: multiselect → []string, checkbox → bool, number/slider → float64
// (an integer when it is one), secret → {"secret":true,"chars":N}, the rest a
// string. An unanswered optional field is left out (a checkbox is false).
func Parse(fields []Field, v url.Values) Result {
	r := Result{Answers: map[string]any{}, Secrets: map[string]string{}, Errors: map[string]string{}}
	for _, f := range fields {
		if f.Display() {
			continue
		}
		vals := v[InputName(f.Key)]
		if f.Type != Multiselect && len(vals) > 1 {
			r.Errors[f.Key] = "Give one value only."
			continue
		}
		first := ""
		if len(vals) == 1 {
			first = vals[0]
		}
		other := strings.TrimSpace(v.Get(OtherName(f.Key)))
		switch f.Type {
		case Checkbox:
			if first != "" && first != "on" && first != "true" {
				r.Errors[f.Key] = "Unexpected value."
				continue
			}
			r.Answers[f.Key] = first != ""
		case Select, Radio:
			val := strings.TrimSpace(first)
			if val == OtherValue {
				if other == "" {
					if f.Required {
						r.Errors[f.Key] = "Type your answer next to “Other…”."
					}
					continue
				}
				if utf8.RuneCountInString(other) > MaxTextChars {
					r.Errors[f.Key] = fmt.Sprintf("Keep it under %d characters.", MaxTextChars)
					continue
				}
				r.Answers[f.Key] = other
				continue
			}
			if val == "" {
				if f.Required {
					r.Errors[f.Key] = "This question is required."
				}
				continue
			}
			if !hasValue(f.Options, val) {
				r.Errors[f.Key] = "Pick one of the choices."
				continue
			}
			r.Answers[f.Key] = val
		case Multiselect:
			var picked []string
			bad := false
			for _, x := range vals {
				x = strings.TrimSpace(x)
				switch {
				case x == "" || contains(picked, x):
				case x == OtherValue:
					if other != "" && !contains(picked, other) {
						if utf8.RuneCountInString(other) > MaxTextChars {
							bad = true
							break
						}
						picked = append(picked, other)
					}
				case hasValue(f.Options, x):
					picked = append(picked, x)
				default:
					bad = true
				}
			}
			if bad {
				r.Errors[f.Key] = "Pick among the choices."
				continue
			}
			if len(picked) == 0 {
				if f.Required {
					r.Errors[f.Key] = "Pick at least one."
				}
				continue
			}
			r.Answers[f.Key] = picked
		case Number, Slider:
			s := strings.TrimSpace(first)
			if s == "" {
				if f.Required {
					r.Errors[f.Key] = "This question is required."
				}
				continue
			}
			n, err := strconv.ParseFloat(strings.Replace(s, ",", ".", 1), 64)
			if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
				r.Errors[f.Key] = "Enter a number."
				continue
			}
			if f.Min != nil && n < *f.Min {
				r.Errors[f.Key] = "At least " + FormatNumber(*f.Min) + "."
				continue
			}
			if f.Max != nil && n > *f.Max {
				r.Errors[f.Key] = "At most " + FormatNumber(*f.Max) + "."
				continue
			}
			r.Answers[f.Key] = n
		case Date:
			s := strings.TrimSpace(first)
			if s == "" {
				if f.Required {
					r.Errors[f.Key] = "This question is required."
				}
				continue
			}
			if _, err := time.Parse("2006-01-02", s); err != nil {
				r.Errors[f.Key] = "Enter a date (YYYY-MM-DD)."
				continue
			}
			r.Answers[f.Key] = s
		case Secret:
			// A pasted token often drags a newline or a space along.
			s := strings.TrimSpace(first)
			if s == "" {
				if f.Required {
					r.Errors[f.Key] = "This question is required."
				}
				continue
			}
			if utf8.RuneCountInString(s) > MaxSecretChars {
				r.Errors[f.Key] = "That value is too long."
				continue
			}
			r.Secrets[f.Key] = s
			r.Answers[f.Key] = SecretPlaceholder(s)
		default: // text, textarea
			s := first
			if f.Type == Text {
				s = strings.TrimSpace(s)
			} else {
				s = strings.TrimRight(strings.ReplaceAll(s, "\r\n", "\n"), " \n\t")
			}
			if strings.TrimSpace(s) == "" {
				if f.Required {
					r.Errors[f.Key] = "This question is required."
				}
				continue
			}
			limit := MaxTextChars
			if f.Type == Textarea {
				limit = MaxLongChars
			}
			if utf8.RuneCountInString(s) > limit {
				r.Errors[f.Key] = fmt.Sprintf("Keep it under %d characters.", limit)
				continue
			}
			r.Answers[f.Key] = s
		}
	}
	return r
}

// FormatNumber prints a number without a trailing ".0".
func FormatNumber(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// AnswerText renders one answer as plain text for a recap: option labels for
// option values, yes/no for a checkbox, a lock line for a secret. Empty when
// the field was left unanswered.
func AnswerText(f Field, v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case bool:
		if t {
			return "Yes"
		}
		return "No"
	case float64:
		return FormatNumber(t)
	case string:
		return f.OptionLabel(t)
	case []string:
		parts := make([]string, len(t))
		for i, x := range t {
			parts[i] = f.OptionLabel(x)
		}
		return strings.Join(parts, ", ")
	case []any:
		parts := make([]string, 0, len(t))
		for _, x := range t {
			parts = append(parts, f.OptionLabel(str(x)))
		}
		return strings.Join(parts, ", ")
	case map[string]any:
		if t["secret"] == true {
			return fmt.Sprintf("🔒 secret given (%v chars) — not shown", t["chars"])
		}
	}
	return str(v)
}
