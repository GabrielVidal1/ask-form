package server

import (
	"fmt"
	"html/template"
	"net/url"
	"strings"

	"github.com/GabrielVidal1/ask-form/internal/form"
	"github.com/GabrielVidal1/ask-form/internal/store"
)

// pageField is one field as the answer page draws it.
type pageField struct {
	form.Field
	Name, OtherName string
	DOMID           string
	Value           string          // single-valued fields
	Picked          map[string]bool // radio / select / multiselect values
	OtherPicked     bool
	OtherText       string
	Checked         bool
	Error           string
	Reenter         bool     // a secret to type again after an invalid send
	FrameURL        string   // html block document
	OptionFrames    []string // per option: its mockup document, "" when none
	Num             struct{ Min, Max, Step string }
}

type pageGroup struct {
	Title  string
	ID     string // anchor the desktop section list links to
	Nav    string // its entry there: the title, else "Section N"
	Fields []pageField
}

type formPage struct {
	ID, Title   string
	Base        string // URL prefix of this form: /f/<id>, or /examples/<name> for a demo
	Description template.HTML
	Groups      []pageGroup
	CatchAll    *pageField
	CSRF        string
	Errors      []pageError
	Nonce       string
	PageCSS     template.CSS
	Expires     string
	Paged       bool
	Demo        *demoInfo // set on an example: nothing sent is stored
	Multi       bool      // open to everyone with the link
	Count       int       // a multi form's answers so far
	ShowCount   bool      // …shown to the respondent
}

// demoInfo is what an example page shows on top of the form.
type demoInfo struct{ Index, Spec string }

type pageError struct{ DOMID, Label, Msg string }

// buildPage lays a form out for the answer page: fields filled from values
// (the defaults on first load, what was sent after an invalid submit),
// grouped into pages the way the ai-agent pages them.
func buildPage(st store.State, base string, values url.Values, errs map[string]string, csrf, nonce string) formPage {
	p := formPage{
		ID: st.ID, Title: st.Title, Base: base, Description: form.MarkdownHTML(st.Description),
		CSRF: csrf, Nonce: nonce, Expires: st.ExpiresAt.UTC().Format("2006-01-02 15:04 UTC"),
		Multi: st.Multi, Count: len(st.Responses), ShowCount: st.Multi && !st.HideCount,
	}
	var css strings.Builder
	fields := make([]pageField, 0, len(st.Fields))
	for i, f := range st.Fields {
		pf := pageField{
			Field: f, Name: form.InputName(f.Key), OtherName: form.OtherName(f.Key),
			DOMID: fmt.Sprintf("q%d", i), Picked: map[string]bool{}, Error: errs[f.Key],
		}
		fill(&pf, values)
		pf.Reenter = values != nil && f.Type == form.Secret
		if f.Type == form.HTML {
			pf.FrameURL = fmt.Sprintf("%s/frame/%s", base, f.Key)
			h := f.Height
			if h <= 0 {
				h = 240
			}
			fmt.Fprintf(&css, "#%s .frame{height:%dpx}", pf.DOMID, h)
			if f.Height > 0 {
				fmt.Fprintf(&css, "#%s .frame{resize:none}", pf.DOMID)
			}
		}
		if f.Visual() {
			pf.OptionFrames = make([]string, len(f.Options))
			for n, o := range f.Options {
				if o.HTML != "" {
					pf.OptionFrames[n] = fmt.Sprintf("%s/frame/%s/%d", base, f.Key, n)
				}
			}
			if f.Columns > 0 {
				// From the width the grid gets 2 columns at (app.css); a phone keeps 1.
				fmt.Fprintf(&css, "@media (min-width:560px){#%s .cards{grid-template-columns:repeat(%d,minmax(0,1fr))}}", pf.DOMID, f.Columns)
			}
			aspect := f.Aspect
			if aspect == "" {
				aspect = "1"
				for _, o := range f.Options {
					if o.HTML != "" {
						aspect = "4/3"
					}
				}
			}
			if aspect != "auto" {
				// Validated by the spec (auto, N or W/H): safe inside CSS.
				fmt.Fprintf(&css, "#%s .visual:not(.text-card){aspect-ratio:%s}", pf.DOMID, aspect)
			} else {
				fmt.Fprintf(&css, "#%s .visual img{height:auto;object-fit:contain}#%s .visual:not(.text-card){aspect-ratio:auto}", pf.DOMID, pf.DOMID)
			}
		}
		if f.Min != nil {
			pf.Num.Min = form.FormatNumber(*f.Min)
		}
		if f.Max != nil {
			pf.Num.Max = form.FormatNumber(*f.Max)
		}
		if f.Step != nil {
			pf.Num.Step = form.FormatNumber(*f.Step)
		} else if f.Type == form.Number {
			pf.Num.Step = "any"
		}
		if pf.Error != "" {
			p.Errors = append(p.Errors, pageError{DOMID: pf.DOMID, Label: f.Label, Msg: pf.Error})
		}
		fields = append(fields, pf)
	}
	p.PageCSS = template.CSS(css.String())
	p.Groups, p.CatchAll = groupFields(fields)
	p.Paged = len(p.Groups) > 1
	for i := range p.Groups {
		g := &p.Groups[i]
		g.ID, g.Nav = fmt.Sprintf("g%d", i+1), g.Title
		if g.Nav == "" {
			g.Nav = fmt.Sprintf("Section %d", i+1)
		}
	}
	return p
}

// fill sets a field's current value: from the submitted values when there
// are any, else from the spec's default. A secret is never refilled.
func fill(pf *pageField, values url.Values) {
	f := pf.Field
	if values != nil {
		if f.Type == form.Secret {
			return
		}
		vals := values[pf.Name]
		pf.OtherText = values.Get(pf.OtherName)
		for _, v := range vals {
			pf.Picked[v] = true
		}
		pf.OtherPicked = pf.Picked[form.OtherValue]
		if len(vals) > 0 {
			pf.Value = vals[0]
		}
		pf.Checked = pf.Value != ""
		return
	}
	switch d := f.Default.(type) {
	case nil:
		if f.Type == form.Slider && f.Min != nil {
			pf.Value = form.FormatNumber(*f.Min)
		}
	case bool:
		pf.Checked = d
	case float64:
		pf.Value = form.FormatNumber(d)
	case string:
		pf.Value = d
		pf.Checked = d == "true" || d == "on"
	case []any:
		for _, x := range d {
			if s, ok := x.(string); ok {
				pf.Picked[s] = true
			}
		}
	}
	if f.Choice() && pf.Value != "" {
		if hasOption(f, pf.Value) {
			pf.Picked[pf.Value] = true
		} else {
			// A default outside the options pre-fills "Other…".
			pf.OtherPicked, pf.OtherText = true, pf.Value
			pf.Picked[form.OtherValue] = true
		}
	}
}

func hasOption(f form.Field, v string) bool {
	for _, o := range f.Options {
		if o.Value == v {
			return true
		}
	}
	return false
}

// groupFields pages the fields by their `group`: a question without one
// stays in the group of the question before it; a display block without one
// joins the question after it (its context), else the one before. Groups
// keep the order of their first appearance. The catch-all is pinned last.
func groupFields(fields []pageField) ([]pageGroup, *pageField) {
	var catchAll *pageField
	var rest []pageField
	for i := range fields {
		if fields[i].CatchAll {
			f := fields[i]
			catchAll = &f
			continue
		}
		rest = append(rest, fields[i])
	}
	eff := make([]string, len(rest))
	cur := ""
	for i, f := range rest {
		if f.Display() {
			continue
		}
		if f.Group != "" {
			cur = f.Group
		}
		eff[i] = cur
	}
	for i, f := range rest {
		if !f.Display() {
			continue
		}
		if f.Group != "" {
			eff[i] = f.Group
			continue
		}
		eff[i] = ""
		found := false
		for j := i + 1; j < len(rest); j++ {
			if !rest[j].Display() {
				eff[i], found = eff[j], true
				break
			}
		}
		if !found {
			for j := i - 1; j >= 0; j-- {
				if !rest[j].Display() {
					eff[i] = eff[j]
					break
				}
			}
		}
	}
	var groups []pageGroup
	index := map[string]int{}
	for i, f := range rest {
		g, ok := index[eff[i]]
		if !ok {
			g = len(groups)
			index[eff[i]] = g
			groups = append(groups, pageGroup{Title: eff[i]})
		}
		groups[g].Fields = append(groups[g].Fields, f)
	}
	if len(groups) == 0 {
		groups = []pageGroup{{}}
	}
	return groups, catchAll
}

// recapItem is one answered question on the closed page.
type recapItem struct{ Label, Text string }

func recap(fields []form.Field, answers map[string]any) []recapItem {
	var out []recapItem
	for _, f := range fields {
		if f.Display() {
			continue
		}
		v, ok := answers[f.Key]
		if !ok {
			continue
		}
		if t := form.AnswerText(f, v); t != "" {
			out = append(out, recapItem{Label: f.Label, Text: t})
		}
	}
	return out
}
