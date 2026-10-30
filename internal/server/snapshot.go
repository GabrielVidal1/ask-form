package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"image"
	"image/color"
	_ "image/gif" // decoders for upload thumbnails
	"image/jpeg"
	"image/png"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/GabrielVidal1/ask-form/internal/form"
	"github.com/GabrielVidal1/ask-form/internal/i18n"
	"github.com/GabrielVidal1/ask-form/internal/store"
	"github.com/GabrielVidal1/ask-form/web"
)

// A snapshot is the form as it closed, kept as one self-contained page: the
// questions drawn read-only with the answers in them (a multi form: the
// questions once, then every answer), its look, pictures and mockups inlined.
// It opens offline in any browser, years later, whatever ask-form became.
// `serve` writes one when a form stops taking answers — submitted, declined,
// revoked, expired, closed — unless the machine (`--no-snapshots`) or the
// form (`create --no-snapshot`) said no, and once at start for the forms
// that closed before (the backfill). The admin API is its only reader
// (GET /v1/forms/{id}/snapshot): the respondent's link doesn't change.
//
// Secrets stay out (a length, never the value); an uploaded picture comes as
// a thumbnail, any other file as its name and size.

// snapScript sizes the frames from their beacon (html blocks, mockup cards)
// — the part of app.js a read-only page needs.
const snapScript = `addEventListener("message",function(e){var d=e.data,i,f,l;` +
	`if(!d||d.type!=="ask-form:frame"||typeof d.h!=="number")return;l=document.querySelectorAll("iframe");` +
	`for(i=0;i<l.length;i++){f=l[i];if(f.contentWindow!==e.source)continue;` +
	`if(f.classList.contains("autosize"))f.style.height=Math.max(40,Math.min(4000,Math.ceil(d.h)+2))+"px";` +
	`if(d.w>0&&d.h>0&&f.closest(".fit-content .mock"))f.parentNode.style.aspectRatio=d.w+" / "+d.h}});`

// snapCSS is what a snapshot adds to app.css: its banner, inputs that read
// as answers rather than as greyed-out controls, the multi form's answers.
const snapCSS = `.snap-banner{background:hsl(var(--muted));border-bottom:1px solid hsl(var(--border));font-size:14.5px}` +
	`.snap-banner-in{display:flex;flex-wrap:wrap;align-items:baseline;gap:4px 24px;max-width:1440px;margin:0 auto;padding:12px 16px}` +
	`.snap-banner p{margin:0}.snap-banner .snap-id{margin-left:auto;color:hsl(var(--muted-foreground));font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;font-size:.85em}` +
	`.snap-fields{border:0;margin:0;padding:0;min-width:0}` +
	`.snap-fields :is(input:not([type=radio],[type=checkbox]),select,textarea):disabled{opacity:1;cursor:default;color:hsl(var(--foreground));-webkit-text-fill-color:hsl(var(--foreground))}` +
	// A disabled control is greyed out whatever accent-color says: the ticks
	// are drawn instead, so a picked choice reads at a glance.
	`.snap-fields ::placeholder{color:transparent;-webkit-text-fill-color:transparent}` + // a hint, not an answer
	`.snap-fields input[type=radio],.snap-fields input[type=checkbox]{-webkit-appearance:none;appearance:none;` +
	`border:1.5px solid hsl(var(--muted-foreground)/.55);background:hsl(var(--card)) center/12px no-repeat}` +
	`.snap-fields input[type=radio]{border-radius:50%}.snap-fields input[type=checkbox]{border-radius:5px}` +
	`.snap-fields input[type=radio]:checked{border-color:hsl(var(--primary));background-image:radial-gradient(hsl(var(--primary)) 0 4px,transparent 4.5px)}` +
	`.snap-fields input[type=checkbox]:checked{border-color:hsl(var(--primary));background-color:hsl(var(--primary));` +
	`background-image:url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 12 12'%3E%3Cpath d='M2.5 6.2l2.3 2.3 4.7-5' fill='none' stroke='white' stroke-width='1.8'/%3E%3C/svg%3E")}` +
	`.snap-fields .card,.snap-fields .choice,.snap-fields .rate{cursor:default}` +
	`.snap-empty{color:hsl(var(--muted-foreground));font-style:italic;margin:6px 0 0}` +
	`.snap-secret{margin:6px 0 0;padding:10px 12px;border:1px dashed hsl(var(--border));border-radius:calc(var(--radius) - 4px);color:hsl(var(--muted-foreground))}` +
	`.snap-json{margin:6px 0 0;padding:10px 12px;background:hsl(var(--muted));border-radius:calc(var(--radius) - 4px);white-space:pre-wrap;overflow-wrap:anywhere;font:13px/1.5 ui-monospace,SFMono-Regular,Menlo,Consolas,monospace}` +
	`.snap-files{list-style:none;margin:6px 0 0;padding:0;display:flex;flex-wrap:wrap;gap:12px}` +
	`.snap-files li{display:flex;flex-direction:column;gap:4px;max-width:240px;font-size:14px}` +
	`.snap-files img{display:block;max-width:240px;max-height:240px;border-radius:calc(var(--radius) - 4px);border:1px solid hsl(var(--border));background:hsl(var(--card))}` +
	`.snap-files small{color:hsl(var(--muted-foreground))}` +
	`.snap-responses{margin-top:32px}.snap-response{margin-top:16px}.snap-response h3{margin:0;font-size:16px}` +
	`.snap-response time{color:hsl(var(--muted-foreground));font-weight:400}.snap-response .recap{margin-top:8px}`

var (
	snapScriptHash = func() string {
		sum := sha256.Sum256([]byte(snapScript))
		return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	}()
	// snapBaseCSS: the answer page's own stylesheets, as they are in this
	// binary, plus the snapshot's.
	snapBaseCSS = func() string {
		var b strings.Builder
		for _, name := range []string{"static/theme.css", "static/app.css", "static/upload.css"} {
			css, _ := web.FS.ReadFile(name)
			b.Write(css)
			b.WriteByte('\n')
		}
		return b.String() + snapCSS
	}()
)

// snapshotCSP is a snapshot's policy, in its <meta> (the file travels
// without headers) and on the admin response: nothing loads, the two pinned
// scripts run — ours, and the frames' (which inherit it) — and nothing posts.
func snapshotCSP() string {
	return "default-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src data:; media-src data:; " +
		"script-src " + snapScriptHash + " " + frameScriptHash + "; frame-src 'self' data: about:; " +
		"base-uri 'none'; form-action 'none'"
}

type snapshotPage struct {
	formPage
	CSP, Banner, Version string
	Styles               template.CSS
	Script               template.JS
	Responses            []snapResponse
}

// snapResponse is one answer to a multi form.
type snapResponse struct {
	N     int
	When  string
	Recap []recapItem
}

// snapFile is one uploaded file as a snapshot shows it.
type snapFile struct {
	Name, Size string
	Thumb      template.URL // a data: URI, "" when it isn't a picture (or is gone)
}

// renderSnapshot draws a closed form's snapshot page.
func (s *Server) renderSnapshot(st store.State) ([]byte, error) {
	lang := i18n.Of(st.Lang)
	asset := func(name string) (string, bool) {
		b, a, err := s.store.Asset(st.ID, name)
		if err != nil {
			return "", false
		}
		return dataURI(a.ContentType, b), true
	}
	assets := map[string]string{} // asset URL → data: URI
	prefix := Prefix + st.ID + "/assets/"
	for _, a := range st.Assets {
		if data, ok := asset(a.Name); ok {
			assets[prefix+a.Name] = data
		}
	}
	answers := st.Answers
	if st.Multi || st.Status != store.Submitted {
		answers = nil // the questions as they were asked, every one of them
	}
	page := buildPage(st, Prefix+st.ID, answerValues(st.Fields, answers), nil, "", "")
	page.Brand = pageLook(s.look())
	brandFrame := s.look().FrameCSS()
	hidden := hiddenFields(st.Fields, answers)
	files := s.snapFiles(st, answers)
	for gi := range page.Groups {
		g := &page.Groups[gi]
		kept := g.Fields[:0]
		for _, pf := range g.Fields {
			if hidden[pf.Key] {
				continue
			}
			snapField(&pf, st, prefix, brandFrame, assets, asset, answers, files[pf.Key])
			kept = append(kept, pf)
		}
		g.Fields = kept
	}
	if _, said := answers[form.CatchAllKey]; !said {
		page.CatchAll = nil // "Anything else?" left empty, or never answered
	} else {
		snapField(page.CatchAll, st, prefix, brandFrame, assets, asset, answers, nil)
	}
	groups := page.Groups[:0]
	for _, g := range page.Groups {
		if len(g.Fields) > 0 {
			groups = append(groups, g)
		}
	}
	page.Groups, page.Paged = groups, false

	p := snapshotPage{
		formPage: page, CSP: snapshotCSP(), Version: Version,
		Styles: template.CSS(snapBaseCSS + string(page.Brand.CSS) + string(page.PageCSS)),
		Script: template.JS(snapScript),
		Banner: snapBanner(lang, st),
	}
	for _, r := range st.Responses {
		p.Responses = append(p.Responses, snapResponse{N: r.N, When: i18n.Date(lang, r.At), Recap: recap(lang, st.Fields, r.Answers)})
	}
	var buf bytes.Buffer
	if err := pageTemplates[lang].ExecuteTemplate(&buf, "snapshot", p); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// snapBanner is the line on top of a snapshot: how the form ended, and when.
func snapBanner(lang string, st store.State) string {
	when := ""
	if st.ClosedAt != nil {
		when = i18n.Date(lang, *st.ClosedAt)
	}
	if st.Multi {
		switch st.Status {
		case store.Expired:
			return i18n.T(lang, "Expired on %s", when)
		case store.Revoked:
			return i18n.T(lang, "Withdrawn on %s", when)
		}
	}
	switch st.Status {
	case store.Submitted:
		return i18n.T(lang, "Answered on %s", when)
	case store.Declined:
		return i18n.T(lang, "Declined on %s — no answers", when)
	case store.Revoked:
		return i18n.T(lang, "Withdrawn on %s — no answers", when)
	case store.Expired:
		return i18n.T(lang, "Expired on %s without an answer", when)
	case store.Closed:
		return i18n.T(lang, "Closed on %s", when)
	}
	return ""
}

// snapField turns one field of the answer page into its read-only, offline
// self: frames carry their document, pictures their bytes, links go.
func snapField(pf *pageField, st store.State, prefix, brandFrame string, assets map[string]string,
	asset func(string) (string, bool), answers map[string]any, files []snapFile) {
	pf.Snap = true
	if pf.FrameURL != "" && pf.Type == form.HTML {
		pf.FrameDoc = frameDocWith(st, prefix, pf.HTML, false, "", brandFrame, asset)
	}
	if pf.Type == form.Widget {
		if v, ok := answers[pf.Key]; ok {
			b, _ := json.MarshalIndent(v, "", "  ")
			pf.Value = string(b)
		} else {
			pf.Value = ""
		}
	}
	if pf.Type == form.Secret {
		if m, ok := answers[pf.Key].(map[string]any); ok {
			pf.SecretChars = fmt.Sprint(m["chars"])
		}
	}
	pf.Files = files
	if pf.Type == form.List {
		// The blank rows a list pads itself with are for typing in.
		rows := pf.Rows[:0]
		for _, r := range pf.Rows {
			if strings.Join(r, "") != "" {
				rows = append(rows, r)
			}
		}
		pf.Rows = rows
	}
	if pf.Visual() {
		pf.OptionDocs = make([]string, len(pf.Options))
		pf.OptionImages = make([]template.URL, len(pf.Options))
		for n, o := range pf.Options {
			if o.HTML != "" {
				pf.OptionDocs[n] = frameDocWith(st, prefix, o.HTML, true, "", brandFrame, asset)
			}
			if data, ok := assets[o.Image]; ok {
				pf.OptionImages[n] = template.URL(data)
			}
		}
	}
}

// hiddenFields are the questions a respondent never saw: their showIf failed
// on the answers given. Nothing is hidden from a form left unanswered.
func hiddenFields(fields []form.Field, answers map[string]any) map[string]bool {
	hidden := map[string]bool{}
	if answers == nil {
		return hidden
	}
	get := func(k string) (any, bool) { v := answers[k]; return v, !hidden[k] }
	for _, f := range fields {
		if f.ShowIf != nil && !f.ShowIf.Eval(get) {
			hidden[f.Key] = true
		}
	}
	return hidden
}

// answerValues turns answers back into the values the answer page fills its
// inputs from — Parse the other way round. nil answers: every input empty.
func answerValues(fields []form.Field, answers map[string]any) url.Values {
	v := url.Values{}
	for _, f := range fields {
		a, ok := answers[f.Key]
		if !ok || f.Display() {
			continue
		}
		name := form.InputName(f.Key)
		switch f.Type {
		case form.Checkbox:
			if b, _ := a.(bool); b {
				v.Set(name, "on")
			}
		case form.Select, form.Radio:
			s := fmt.Sprint(a)
			if hasOption(f, s) {
				v.Set(name, s)
			} else {
				v.Set(name, form.OtherValue)
				v.Set(form.OtherName(f.Key), s)
			}
		case form.Multiselect:
			var other []string
			for _, x := range answerStrings(a) {
				if hasOption(f, x) {
					v.Add(name, x)
				} else {
					other = append(other, x)
				}
			}
			if len(other) > 0 {
				v.Add(name, form.OtherValue)
				v.Set(form.OtherName(f.Key), strings.Join(other, ", "))
			}
		case form.Number, form.Slider, form.Rating:
			if n, ok := a.(float64); ok {
				v.Set(name, form.FormatNumber(n))
			} else {
				v.Set(name, fmt.Sprint(a))
			}
		case form.Daterange:
			if m, ok := a.(map[string]any); ok {
				v.Set(name+form.RangeStart, fmt.Sprint(m["start"]))
				v.Set(name+form.RangeEnd, fmt.Sprint(m["end"]))
			}
		case form.List:
			if len(f.Cols) == 0 {
				for _, r := range answerStrings(a) {
					v.Add(name, r)
				}
				break
			}
			var rows []map[string]any
			switch t := a.(type) {
			case []map[string]any:
				rows = t
			case []any:
				for _, r := range t {
					m, _ := r.(map[string]any)
					rows = append(rows, m)
				}
			}
			for _, m := range rows {
				for _, c := range f.Cols {
					cell := ""
					if x, has := m[c.Key]; has {
						cell = fmt.Sprint(x)
						if n, ok := x.(float64); ok {
							cell = form.FormatNumber(n)
						}
					}
					v.Add(name+"."+c.Key, cell)
				}
			}
		case form.Secret, form.File, form.Widget:
			// Drawn apart (snapField): a length, the files, the JSON.
		default:
			v.Set(name, fmt.Sprint(a))
		}
	}
	return v
}

// answerStrings reads a multiselect or list answer: []string fresh from a
// submit, []any once it went through the responses file.
func answerStrings(a any) []string {
	switch t := a.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, x := range t {
			out = append(out, fmt.Sprint(x))
		}
		return out
	}
	return nil
}

// snapFiles lists each file field's uploads, a thumbnail for the pictures
// still on disk.
func (s *Server) snapFiles(st store.State, answers map[string]any) map[string][]snapFile {
	out := map[string][]snapFile{}
	lang := i18n.Of(st.Lang)
	for _, f := range st.Fields {
		if f.Type != form.File {
			continue
		}
		raw, ok := answers[f.Key]
		if !ok {
			continue
		}
		var infos []form.FileInfo
		b, _ := json.Marshal(raw)
		if json.Unmarshal(b, &infos) != nil {
			continue
		}
		for _, fi := range infos {
			sf := snapFile{Name: fi.Name, Size: form.HumanSizeIn(lang, fi.Size)}
			if data, err := s.store.UploadedFile(st.ID, fi.Path); err == nil {
				sf.Thumb = thumbnail(fi.ContentType, data)
			}
			out[f.Key] = append(out[f.Key], sf)
		}
	}
	return out
}

// Thumbnails: JPEG, PNG and GIF are scaled down to thumbMax a side (a JPEG
// stays a JPEG, the others become PNGs, transparency kept); the formats the
// standard library can't decode come as they are when small enough.
const (
	thumbMax      = 480
	thumbRawBytes = 256 << 10
)

func thumbnail(contentType string, data []byte) template.URL {
	switch contentType {
	case "image/jpeg", "image/png", "image/gif":
	case "image/webp", "image/avif", "image/svg+xml":
		if len(data) <= thumbRawBytes {
			return template.URL(dataURI(contentType, data))
		}
		return ""
	default:
		return ""
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return ""
	}
	img = scaleDown(img, thumbMax)
	var buf bytes.Buffer
	if contentType == "image/jpeg" {
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80})
		contentType = "image/jpeg"
	} else {
		err = png.Encode(&buf, img)
		contentType = "image/png"
	}
	if err != nil {
		return ""
	}
	return template.URL(dataURI(contentType, buf.Bytes()))
}

// scaleDown shrinks img to fit max×max, averaging each box of source pixels
// (premultiplied, so transparent edges don't darken); a small one is kept.
func scaleDown(img image.Image, max int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= max && h <= max || w == 0 || h == 0 {
		return img
	}
	nw, nh := max, h*max/w
	if h > w {
		nw, nh = w*max/h, max
	}
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	out := image.NewNRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		y0, y1 := b.Min.Y+y*h/nh, b.Min.Y+(y+1)*h/nh
		if y1 == y0 {
			y1++
		}
		for x := 0; x < nw; x++ {
			x0, x1 := b.Min.X+x*w/nw, b.Min.X+(x+1)*w/nw
			if x1 == x0 {
				x1++
			}
			var r, g, bl, a, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					cr, cg, cb, ca := img.At(sx, sy).RGBA()
					r, g, bl, a, n = r+uint64(cr), g+uint64(cg), bl+uint64(cb), a+uint64(ca), n+1
				}
			}
			if a == 0 {
				continue
			}
			// Back from premultiplied 16-bit to straight 8-bit.
			out.SetNRGBA(x, y, color.NRGBA{
				R: uint8(r * 0xff / a), G: uint8(g * 0xff / a), B: uint8(bl * 0xff / a), A: uint8(a / n >> 8),
			})
		}
	}
	return out
}

// ── when they are taken ─────────────────────────────────────────────────────

// snapshot writes a form's snapshot if it's closed, has none, and neither the
// machine nor the form opted out; it returns the state with Snapshot set. A
// failure is logged: the form itself is unaffected.
func (s *Server) snapshot(st store.State) store.State {
	if s.cfg.NoSnapshots || st.NoSnapshot || st.Snapshot || st.Status == store.Pending {
		return st
	}
	page, err := s.renderSnapshot(st)
	if err == nil {
		err = s.store.PutSnapshot(st.ID, page)
	}
	if err != nil {
		log.Printf("snapshot %s: %v", st.ID, err)
		return st
	}
	st.Snapshot = true
	return st
}

// SnapshotSweep takes the snapshots no close wrote: forms that closed before
// this version or while snapshots were off (at start: the backfill), and the
// ones that expired (expiry is lazy, no request closes them). Each form is
// tried once per run of serve. Blocks until ctx ends.
func (s *Server) SnapshotSweep(ctx context.Context, every time.Duration) {
	if s.cfg.NoSnapshots {
		return
	}
	tried := map[string]bool{}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		for _, st := range s.store.List() {
			if st.Status == store.Pending || st.Snapshot || st.NoSnapshot || tried[st.ID] {
				continue
			}
			tried[st.ID] = true
			s.snapshot(st)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// adminSnapshot serves a form's snapshot page (404 when it has none: still
// open, opted out, or taken before snapshots existed and not yet swept).
func (s *Server) adminSnapshot(w http.ResponseWriter, r *http.Request) {
	st, ok := s.getOr404(w, r.PathValue("id"))
	if !ok {
		return
	}
	page, err := s.store.Snapshot(st.ID)
	if err != nil {
		apiError(w, http.StatusNotFound, "no snapshot of "+st.ID)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", snapshotCSP()+"; sandbox allow-scripts")
	h.Set("X-Content-Type-Options", "nosniff")
	w.Write(page)
}
