package form

import (
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/GabrielVidal1/ask-form/internal/i18n"
)

var timeRe = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d(:[0-5]\d)?$`)

// RangeStart and RangeEnd are the input names' suffixes of a daterange.
const (
	RangeStart = ".start"
	RangeEnd   = ".end"
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
// (an integer when it is one), secret → {"secret":true,"chars":N}, email → the
// address trimmed and lowercased, widget → its decoded JSON value, the rest a string. An unanswered optional field is left out (a checkbox is false).
func Parse(fields []Field, v url.Values) Result { return ParseWith(fields, v, Hooks{}) }

// ParseWith is Parse with the hooks that reach beyond the spec (uploads).
func ParseWith(fields []Field, v url.Values, h Hooks) Result {
	r := Result{Answers: map[string]any{}, Secrets: map[string]string{}, Errors: map[string]string{}}
	t := func(s string, a ...any) string { return i18n.T(h.Lang, s, a...) }
	// A hidden field (its showIf fails) is skipped whole — not required, not
	// answered, whatever the browser sent — and a test on it fails in turn.
	hidden := map[string]bool{}
	get := func(k string) (any, bool) { return r.Answers[k], !hidden[k] }
	for _, f := range fields {
		if f.ShowIf != nil && !f.ShowIf.Eval(get) {
			hidden[f.Key] = true
			continue
		}
		if f.Display() {
			continue
		}
		if f.RequiredIf != nil && f.RequiredIf.Eval(get) {
			f.Required = true
		}
		vals := v[InputName(f.Key)]
		if f.Type != Multiselect && f.Type != List && len(vals) > 1 {
			r.Errors[f.Key] = t("Give one value only.")
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
				r.Errors[f.Key] = t("Unexpected value.")
				continue
			}
			r.Answers[f.Key] = first != ""
		case Select, Radio:
			val := strings.TrimSpace(first)
			if val == OtherValue {
				if other == "" {
					if f.Required {
						r.Errors[f.Key] = t("Type your answer next to “Other…”.")
					}
					continue
				}
				if utf8.RuneCountInString(other) > MaxTextChars {
					r.Errors[f.Key] = t("Keep it under %d characters.", MaxTextChars)
					continue
				}
				r.Answers[f.Key] = other
				continue
			}
			if val == "" {
				if f.Required {
					r.Errors[f.Key] = t("This question is required.")
				}
				continue
			}
			if !hasValue(f.Options, val) {
				r.Errors[f.Key] = t("Pick one of the choices.")
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
				r.Errors[f.Key] = t("Pick among the choices.")
				continue
			}
			if len(picked) == 0 {
				if f.Required {
					r.Errors[f.Key] = t("Pick at least one.")
				}
				continue
			}
			r.Answers[f.Key] = picked
		case Number, Slider:
			s := strings.TrimSpace(first)
			if s == "" {
				if f.Required {
					r.Errors[f.Key] = t("This question is required.")
				}
				continue
			}
			n, err := strconv.ParseFloat(strings.Replace(s, ",", ".", 1), 64)
			if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
				r.Errors[f.Key] = t("Enter a number.")
				continue
			}
			if f.Min != nil && n < *f.Min {
				r.Errors[f.Key] = t("At least %s.", FormatNumber(*f.Min))
				continue
			}
			if f.Max != nil && n > *f.Max {
				r.Errors[f.Key] = t("At most %s.", FormatNumber(*f.Max))
				continue
			}
			r.Answers[f.Key] = n
		case Date:
			s := strings.TrimSpace(first)
			if s == "" {
				if f.Required {
					r.Errors[f.Key] = t("This question is required.")
				}
				continue
			}
			if _, err := time.Parse("2006-01-02", s); err != nil {
				r.Errors[f.Key] = t("Enter a date (YYYY-MM-DD).")
				continue
			}
			r.Answers[f.Key] = s
		case Email:
			s := strings.ToLower(strings.TrimSpace(first))
			if s == "" {
				if f.Required {
					r.Errors[f.Key] = t("This question is required.")
				}
				continue
			}
			if !ValidEmail(s) {
				r.Errors[f.Key] = t("Enter an email address, like name@example.com.")
				continue
			}
			r.Answers[f.Key] = s
		case Time, Datetime:
			s := strings.TrimSpace(first)
			if s == "" {
				if f.Required {
					r.Errors[f.Key] = t("This question is required.")
				}
				continue
			}
			if f.Type == Time && !timeRe.MatchString(s) {
				r.Errors[f.Key] = t("Enter a time (HH:MM).")
				continue
			}
			if f.Type == Datetime {
				if _, err := time.Parse("2006-01-02T15:04", s); err != nil {
					if _, err := time.Parse("2006-01-02T15:04:05", s); err != nil {
						r.Errors[f.Key] = t("Enter a date and a time.")
						continue
					}
				}
			}
			r.Answers[f.Key] = s
		case Daterange:
			start := strings.TrimSpace(v.Get(InputName(f.Key) + RangeStart))
			end := strings.TrimSpace(v.Get(InputName(f.Key) + RangeEnd))
			if start == "" && end == "" {
				if f.Required {
					r.Errors[f.Key] = t("This question is required.")
				}
				continue
			}
			if start == "" || end == "" {
				r.Errors[f.Key] = t("Pick both dates.")
				continue
			}
			ts, err1 := time.Parse("2006-01-02", start)
			te, err2 := time.Parse("2006-01-02", end)
			if err1 != nil || err2 != nil {
				r.Errors[f.Key] = t("Enter dates (YYYY-MM-DD).")
				continue
			}
			if te.Before(ts) {
				r.Errors[f.Key] = t("The end is before the start.")
				continue
			}
			r.Answers[f.Key] = map[string]any{"start": start, "end": end}
		case Rating:
			s := strings.TrimSpace(first)
			if s == "" {
				if f.Required {
					r.Errors[f.Key] = t("This question is required.")
				}
				continue
			}
			n, err := strconv.ParseFloat(s, 64)
			if err != nil || n != math.Trunc(n) || n < *f.Min || n > *f.Max {
				r.Errors[f.Key] = t("Pick a rating.")
				continue
			}
			r.Answers[f.Key] = n
		case List:
			rows, n, msg := listAnswer(f, v, t)
			if msg != "" {
				r.Errors[f.Key] = msg
				continue
			}
			if n == 0 {
				if f.Required {
					r.Errors[f.Key] = t("Add at least one.")
				}
				continue
			}
			if f.Min != nil && float64(n) < *f.Min {
				r.Errors[f.Key] = t("Add at least %s.", FormatNumber(*f.Min))
				continue
			}
			if float64(n) > *f.Max {
				r.Errors[f.Key] = t("At most %s.", FormatNumber(*f.Max))
				continue
			}
			r.Answers[f.Key] = rows
		case Widget:
			val, msg := parseWidget(f, first, t)
			switch {
			case msg != "":
				r.Errors[f.Key] = msg
			case val != nil:
				r.Answers[f.Key] = val
			case f.Required:
				r.Errors[f.Key] = t("Interact with this first.")
			}
		case File:
			refs, msg := fileRefs(first, t)
			if msg != "" {
				r.Errors[f.Key] = msg
				continue
			}
			if len(refs) == 0 {
				if f.Required {
					r.Errors[f.Key] = t("Add a file.")
				}
				continue
			}
			if len(refs) > f.FileLimit() {
				r.Errors[f.Key] = i18n.N(h.Lang, f.FileLimit(), "At most %d file.", "At most %d files.")
				continue
			}
			if h.File == nil {
				r.Errors[f.Key] = t("Uploads aren't available here.")
				continue
			}
			var infos []FileInfo
			for _, ref := range refs {
				info, ok := h.File(f.Key, ref)
				if !ok {
					infos = nil
					r.Errors[f.Key] = t("A file is gone — add it again.")
					break
				}
				infos = append(infos, info)
			}
			if infos != nil {
				r.Answers[f.Key] = infos
			}
		case Secret:
			// A pasted token often drags a newline or a space along.
			s := strings.TrimSpace(first)
			if s == "" {
				if f.Required {
					r.Errors[f.Key] = t("This question is required.")
				}
				continue
			}
			if utf8.RuneCountInString(s) > MaxSecretChars {
				r.Errors[f.Key] = t("That value is too long.")
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
					r.Errors[f.Key] = t("This question is required.")
				}
				continue
			}
			limit := MaxTextChars
			if f.Type == Textarea {
				limit = MaxLongChars
			}
			if utf8.RuneCountInString(s) > limit {
				r.Errors[f.Key] = t("Keep it under %d characters.", limit)
				continue
			}
			r.Answers[f.Key] = s
		}
	}
	return r
}

// listAnswer reads a list field's rows: texts (no columns) or, for a table, one
// object per row built from the `<name>.<column>` inputs, which line up by
// position. Blank cells and blank rows are left out. It returns a message when
// the rows are malformed.
func listAnswer(f Field, v url.Values, t func(string, ...any) string) (rows any, n int, msg string) {
	if len(f.Cols) == 0 {
		out := []string{}
		for _, x := range v[InputName(f.Key)] {
			x = strings.TrimSpace(x)
			if x == "" {
				continue
			}
			if utf8.RuneCountInString(x) > MaxTextChars {
				return nil, 0, t("Keep each entry under %d characters.", MaxTextChars)
			}
			out = append(out, x)
		}
		return out, len(out), ""
	}
	n = -1
	for _, c := range f.Cols {
		l := len(v[InputName(f.Key)+"."+c.Key])
		if n >= 0 && l != n {
			return nil, 0, t("The rows are malformed — reload and try again.")
		}
		n = l
	}
	if n > 4*MaxListRows {
		return nil, 0, t("Too many rows.")
	}
	out := []map[string]any{}
	for i := 0; i < n; i++ {
		row := map[string]any{}
		for _, c := range f.Cols {
			x := strings.TrimSpace(v[InputName(f.Key)+"."+c.Key][i])
			if x == "" {
				continue
			}
			switch c.Type {
			case ColNumber:
				num, err := strconv.ParseFloat(strings.Replace(x, ",", ".", 1), 64)
				if err != nil || math.IsNaN(num) || math.IsInf(num, 0) {
					return nil, 0, t("Row %d: “%s” must be a number.", i+1, c.Label)
				}
				row[c.Key] = num
			case ColSelect:
				if !contains(c.Options, x) {
					return nil, 0, t("Row %d: pick one of the choices for “%s”.", i+1, c.Label)
				}
				row[c.Key] = x
			default:
				if utf8.RuneCountInString(x) > MaxTextChars {
					return nil, 0, t("Keep each entry under %d characters.", MaxTextChars)
				}
				row[c.Key] = x
			}
		}
		if len(row) > 0 {
			out = append(out, row)
		}
	}
	return out, len(out), ""
}

// ValidEmail is a deliberately plain check of an address: one @, something
// before it, a dotted domain of letters, digits and dashes after it, no
// spaces, at most MaxEmailChars. Whether it receives mail is not its job.
func ValidEmail(s string) bool {
	if len(s) > MaxEmailChars || strings.ContainsAny(s, " \t\r\n<>(),;:\"[]\\") {
		return false
	}
	local, domain, ok := strings.Cut(s, "@")
	if !ok || local == "" || len(local) > 64 || strings.Contains(domain, "@") || !strings.Contains(domain, ".") {
		return false
	}
	for _, label := range strings.Split(domain, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c > 127) {
				return false
			}
		}
	}
	return true
}

// FormatNumber prints a number without a trailing ".0".
func FormatNumber(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// AnswerText renders one answer as plain text for a recap: option labels for
// option values, yes/no for a checkbox, a lock line for a secret. Empty when
// the field was left unanswered.
func AnswerText(f Field, v any) string { return AnswerTextIn(i18n.EN, f, v) }

// AnswerTextIn is AnswerText in lang (internal/i18n).
func AnswerTextIn(lang string, f Field, v any) string {
	if f.Type == Widget {
		return widgetText(v)
	}
	if f.Type == File {
		return FileText(v)
	}
	switch t := v.(type) {
	case nil:
		return ""
	case bool:
		if t {
			return i18n.T(lang, "Yes")
		}
		return i18n.T(lang, "No")
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
			if m, ok := x.(map[string]any); ok {
				parts = append(parts, rowText(f, m))
				continue
			}
			parts = append(parts, f.OptionLabel(str(x)))
		}
		if f.Type == List {
			return strings.Join(parts, "; ")
		}
		return strings.Join(parts, ", ")
	case []map[string]any:
		parts := make([]string, 0, len(t))
		for _, m := range t {
			parts = append(parts, rowText(f, m))
		}
		return strings.Join(parts, "; ")
	case map[string]any:
		if t["secret"] == true {
			return i18n.T(lang, "🔒 secret given (%v chars) — not shown", t["chars"])
		}
		if f.Type == Daterange {
			return str(t["start"]) + " → " + str(t["end"])
		}
	}
	return str(v)
}

// rowText is one table row as a line: its cells in column order.
func rowText(f Field, m map[string]any) string {
	var parts []string
	for _, c := range f.Cols {
		if x, ok := m[c.Key]; ok {
			parts = append(parts, str(x))
		}
	}
	return strings.Join(parts, " · ")
}
