// Package form holds the field spec of a form — the JSON an agent writes —
// its normalisation, and the validation of the answers a respondent sends.
//
// The spec is the one the ai-agent's forms use (backend/forms/store.py), so a
// spec written for one works with the other: text, textarea, number, select,
// multiselect, radio, checkbox, slider, date, email and secret questions, plus
// the display-only markdown and html blocks, and `file` (chunked uploads, see
// file.go).
package form

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Field types a form may hold.
const (
	Text        = "text"
	Textarea    = "textarea"
	Number      = "number"
	Select      = "select"
	Multiselect = "multiselect"
	Radio       = "radio"
	Checkbox    = "checkbox"
	Slider      = "slider"
	Date        = "date"
	Email       = "email"
	Secret      = "secret"
	File        = "file"
	Markdown    = "markdown"
	HTML        = "html"
)

// OtherValue is the form value of the "Other…" choice; the free text typed
// next to it is what comes back as the answer.
const OtherValue = "__other__"

// CatchAllKey is the key of the "Anything else?" textarea every form ends with.
const CatchAllKey = "anything_else"

// Limits that keep one spec from bloating the store or the page.
const (
	MaxFields      = 100
	MaxOptions     = 60
	MaxLabelChars  = 500
	MaxHelpChars   = 4000
	MaxBlockChars  = 512 * 1024
	MaxGroupChars  = 80
	MaxColumns     = 6
	MaxTextChars   = 2000
	MaxLongChars   = 20000
	MaxSecretChars = 8000
	MaxEmailChars  = 254
)

var fieldTypes = []string{Text, Textarea, Number, Select, Multiselect, Radio,
	Checkbox, Slider, Date, Email, Secret, File, Markdown, HTML}

// Option is one choice of a select / radio / multiselect field. A visual
// option carries an image (a URL or `{{asset:<file>}}`) and/or an html mockup;
// a field holding one renders as a grid of choice cards.
type Option struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Image       string `json:"image,omitempty"`
	HTML        string `json:"html,omitempty"`
	// Bare: no caption under the card (a visual option given without a label).
	Bare bool `json:"bare,omitempty"`
}

// Visual reports whether the option is drawn as a picture or mockup card.
func (o Option) Visual() bool { return o.Image != "" || o.HTML != "" }

// Field is one normalised entry of a form's spec.
type Field struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Type        string   `json:"type"`
	Required    bool     `json:"required"`
	Group       string   `json:"group,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Help        string   `json:"help,omitempty"`
	Text        string   `json:"text,omitempty"` // markdown block source
	HTML        string   `json:"html,omitempty"` // html block markup
	Height      int      `json:"height,omitempty"`
	Options     []Option `json:"options,omitempty"`
	Other       bool     `json:"other,omitempty"`
	Columns     int      `json:"columns,omitempty"`
	Aspect      string   `json:"aspect,omitempty"`
	Min         *float64 `json:"min,omitempty"`
	Max         *float64 `json:"max,omitempty"`
	Step        *float64 `json:"step,omitempty"`
	MinLabel    string   `json:"minLabel,omitempty"` // slider: words under its min end
	MaxLabel    string   `json:"maxLabel,omitempty"` // slider: words under its max end
	Default     any      `json:"default,omitempty"`
	Accept      string   `json:"accept,omitempty"`   // file: <input accept> list
	MaxMB       float64  `json:"maxMB,omitempty"`    // file: largest file
	Multiple    bool     `json:"multiple,omitempty"` // file: more than one
	MaxFiles    int      `json:"maxFiles,omitempty"` // file: how many
	AutoSecret  bool     `json:"autoSecret,omitempty"`
	CatchAll    bool     `json:"catchAll,omitempty"`
}

// Display reports whether the field is a display-only block.
func (f Field) Display() bool { return f.Type == Markdown || f.Type == HTML }

// Choice reports whether the field picks among options.
func (f Field) Choice() bool {
	return f.Type == Select || f.Type == Radio || f.Type == Multiselect
}

// Visual reports whether the field renders as a grid of choice cards.
func (f Field) Visual() bool {
	for _, o := range f.Options {
		if o.Visual() {
			return true
		}
	}
	return false
}

// OptionLabel is the label of the option with that value, or the value
// itself (an "Other…" answer).
func (f Field) OptionLabel(v string) string {
	for _, o := range f.Options {
		if o.Value == v {
			return o.Label
		}
	}
	return v
}

var (
	slugRe       = regexp.MustCompile(`[^a-z0-9_-]+`)
	aspectRe     = regexp.MustCompile(`^(auto|\d+(\.\d+)?(/\d+(\.\d+)?)?)$`)
	catchAllRe   = regexp.MustCompile(`(?i)\banything\s+else\b`)
	credentialRe = regexp.MustCompile(`(?i)\b(token|password|passwd|passphrase|pass phrase|api key|apikey|secret|` +
		`client secret|private key|access key|credentials?|mot de passe|mdp|pat|otp|2fa code|totp)\b`)
	// AssetRe matches a `{{asset:<file>}}` placeholder.
	AssetRe = regexp.MustCompile(`\{\{\s*asset:\s*([^}\s]+)\s*\}\}`)
)

func slug(s string) string {
	return strings.Trim(slugRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-"), "-")
}

// LooksLikeCredential reports whether a text field's key or label names a
// credential — such a field is upgraded to a masked secret.
func LooksLikeCredential(key, label string) bool {
	t := strings.NewReplacer("_", " ", "-", " ").Replace(key + " " + label)
	return credentialRe.MatchString(t)
}

// Normalize validates a raw spec (the agent's JSON array) and returns the
// fields the form will hold, ending with the "Anything else?" catch-all.
func Normalize(raw json.RawMessage) ([]Field, error) {
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, errors.New("fields must be a JSON array of objects")
	}
	if len(items) == 0 {
		return nil, errors.New("fields must be a non-empty list")
	}
	if len(items) > MaxFields {
		return nil, fmt.Errorf("at most %d fields", MaxFields)
	}
	out := make([]Field, 0, len(items)+1)
	seen := map[string]bool{}
	for i, m := range items {
		if m == nil {
			return nil, fmt.Errorf("field #%d: must be an object", i)
		}
		ftype := strings.ToLower(strings.TrimSpace(str(m["type"])))
		if ftype == "" {
			ftype = Text
		}
		if !contains(fieldTypes, ftype) {
			return nil, fmt.Errorf("field #%d: unknown type %q (one of %s)", i, ftype, strings.Join(fieldTypes, ", "))
		}
		label := clip(strings.TrimSpace(str(m["label"])), MaxLabelChars)
		key := slug(str(m["key"]))
		// A display block never names itself after its heading: a markdown
		// block labelled "Name" must not take the `name` key and push the
		// real question to `name-2`.
		if key == "" && ftype != Markdown && ftype != HTML {
			key = slug(label)
		}
		if len(key) > 64 {
			key = strings.Trim(key[:64], "-")
		}
		autoSecret := ftype == Text && LooksLikeCredential(key, label)
		if autoSecret {
			ftype = Secret
		}
		var block string
		if ftype == Markdown || ftype == HTML {
			src := "text"
			if ftype == HTML {
				src = "html"
			}
			block = strings.TrimSpace(str(m[src]))
			if block == "" {
				block = strings.TrimSpace(str(m["text"]))
			}
			if block == "" {
				return nil, fmt.Errorf("field #%d: %s needs a non-empty %q", i, ftype, src)
			}
			if len(block) > MaxBlockChars {
				return nil, fmt.Errorf("field #%d: %s block exceeds %d KB", i, ftype, MaxBlockChars/1024)
			}
			if key == "" {
				key = fmt.Sprintf("%s-%d", ftype, i+1)
			}
		}
		if key == "" {
			return nil, fmt.Errorf("field #%d: needs a key or a label", i)
		}
		base, n := key, 2
		for seen[key] {
			key = fmt.Sprintf("%s-%d", base, n)
			n++
		}
		seen[key] = true
		f := Field{Key: key, Label: label, Type: ftype, Required: truthy(m["required"])}
		if f.Label == "" {
			f.Label = key
		}
		if g := clip(strings.TrimSpace(str(m["group"])), MaxGroupChars); g != "" {
			f.Group = g
		}
		if f.Display() {
			f.Label = label // a block's heading is optional
			f.Required = false
			if ftype == HTML {
				f.HTML = block
				if h, ok := num(m["height"]); ok && h > 0 {
					f.Height = int(math.Min(h, 4000))
				}
			} else {
				f.Text = block
			}
			out = append(out, f)
			continue
		}
		f.Placeholder = clip(strings.TrimSpace(str(m["placeholder"])), MaxLabelChars)
		f.Help = clip(strings.TrimSpace(str(m["help"])), MaxHelpChars)
		if f.Choice() {
			opts, _ := m["options"].([]any)
			if len(opts) > MaxOptions {
				return nil, fmt.Errorf("field %q: at most %d options", key, MaxOptions)
			}
			for n, ro := range opts {
				o, err := option(ro, key, n)
				if err != nil {
					return nil, err
				}
				if o == nil || hasValue(f.Options, o.Value) {
					continue // a repeated value would make two choices share one tick
				}
				f.Options = append(f.Options, *o)
			}
			if len(f.Options) < 2 {
				return nil, fmt.Errorf("field %q: %s needs >= 2 options", key, ftype)
			}
			f.Other = true // "Other…" is always on: a question must stay answerable
			if f.Visual() {
				if c, ok := num(m["columns"]); ok {
					f.Columns = int(math.Max(1, math.Min(MaxColumns, c)))
				}
				aspect := strings.ToLower(strings.Join(strings.Fields(str(m["aspect"])), ""))
				if aspect != "" {
					if !aspectRe.MatchString(aspect) || strings.HasPrefix(aspect, "0") {
						return nil, fmt.Errorf("field %q: aspect %q is not auto, N or W/H (e.g. 16/9)", key, aspect)
					}
					f.Aspect = aspect
				}
			}
		}
		if ftype == Number || ftype == Slider {
			for k, dst := range map[string]**float64{"min": &f.Min, "max": &f.Max, "step": &f.Step} {
				if v, ok := num(m[k]); ok {
					v := v
					*dst = &v
				}
			}
			if ftype == Slider {
				if f.Min == nil {
					f.Min = ptr(0)
				}
				if f.Max == nil {
					f.Max = ptr(100)
				}
				if f.Step == nil {
					f.Step = ptr(1)
				}
				f.MinLabel = clip(strings.TrimSpace(str(m["minLabel"])), MaxLabelChars)
				f.MaxLabel = clip(strings.TrimSpace(str(m["maxLabel"])), MaxLabelChars)
			}
			if f.Min != nil && f.Max != nil && *f.Min > *f.Max {
				return nil, fmt.Errorf("field %q: min > max", key)
			}
			if f.Step != nil && *f.Step <= 0 {
				f.Step = nil
			}
		}
		if ftype == File {
			if err := fileSpec(&f, m); err != nil {
				return nil, fmt.Errorf("field %q: %w", key, err)
			}
		}
		f.AutoSecret = autoSecret
		// A secret's default would put a value in the spec, which everyone sees;
		// a file has none.
		if d, ok := m["default"]; ok && d != nil && ftype != Secret && ftype != File {
			f.Default = d
		}
		out = append(out, f)
	}
	return withCatchAll(out), nil
}

func option(o any, key string, n int) (*Option, error) {
	m, isMap := o.(map[string]any)
	if !isMap {
		v := clip(strings.TrimSpace(str(o)), MaxLabelChars)
		if v == "" {
			return nil, nil
		}
		if v == OtherValue {
			return nil, fmt.Errorf("field %q: %q is a reserved option value", key, OtherValue)
		}
		return &Option{Value: v, Label: v}, nil
	}
	image := strings.TrimSpace(str(m["image"]))
	html := strings.TrimSpace(str(m["html"]))
	lab := clip(strings.TrimSpace(str(m["label"])), MaxLabelChars)
	val := lab
	if m["value"] != nil {
		val = clip(strings.TrimSpace(str(m["value"])), MaxLabelChars)
	}
	if val == "" && (image != "" || html != "") {
		val = fmt.Sprintf("option-%d", n+1)
	}
	if val == "" {
		return nil, nil
	}
	if val == OtherValue {
		return nil, fmt.Errorf("field %q: %q is a reserved option value", key, OtherValue)
	}
	opt := &Option{Value: val, Label: lab, Image: image}
	if opt.Label == "" {
		opt.Label = val
	}
	opt.Description = clip(strings.TrimSpace(str(m["description"])), MaxHelpChars)
	if len(html) > MaxBlockChars {
		return nil, fmt.Errorf("field %q: option %q html exceeds %d KB", key, val, MaxBlockChars/1024)
	}
	opt.HTML = html
	if lab == "" && (image != "" || html != "") {
		opt.Bare = true
	}
	return opt, nil
}

// withCatchAll ends the spec with the optional "Anything else?" textarea — or
// marks the agent's own trailing one as the catch-all instead of doubling it.
func withCatchAll(fields []Field) []Field {
	last := &fields[len(fields)-1]
	if last.Type == Textarea && !last.Required && catchAllRe.MatchString(last.Label) {
		last.CatchAll = true
		return fields
	}
	key, n := CatchAllKey, 2
	for hasKey(fields, key) {
		key = fmt.Sprintf("%s-%d", CatchAllKey, n)
		n++
	}
	return append(fields, Field{
		Key: key, Label: "Anything else?", Type: Textarea, CatchAll: true,
		Placeholder: "Optional — anything the questions above missed",
	})
}

// Assets lists the `{{asset:<file>}}` names a spec (or any text) references.
func Assets(texts ...string) []string {
	var out []string
	for _, t := range texts {
		for _, m := range AssetRe.FindAllStringSubmatch(t, -1) {
			if !contains(out, m[1]) {
				out = append(out, m[1])
			}
		}
	}
	return out
}

// ── small helpers ────────────────────────────────────────────────────────────

func str(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

func num(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, !math.IsNaN(t) && !math.IsInf(t, 0)
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return f, err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
	}
	return 0, false
}

func truthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t == "true" || t == "1" || t == "yes"
	case float64:
		return t != 0
	}
	return false
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// Cut on a rune boundary.
	for n > 0 && n < len(s) && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}

func ptr(f float64) *float64 { return &f }

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func hasValue(opts []Option, v string) bool {
	for _, o := range opts {
		if o.Value == v {
			return true
		}
	}
	return false
}

func hasKey(fields []Field, k string) bool {
	for _, f := range fields {
		if f.Key == k {
			return true
		}
	}
	return false
}
