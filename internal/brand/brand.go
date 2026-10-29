// Package brand is a machine's own look for the pages `ask-form serve` draws:
// a stylesheet set once with `ask-form brand set`, kept in the config
// directory and inlined into every page this machine renders (the form, the
// thanks page, its messages). The shared stylesheets under /f/_static/ stay
// the same for every machine on a relay, so a brand only ever reaches the
// forms of the machine that set it.
//
// The page's policy loads nothing from anywhere else, so a brand carries its
// files with it: Prepare turns every url() naming a local file into a data:
// URI, and refuses @import and remote urls up front instead of letting the
// browser block them silently.
package brand

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// MaxSize caps a brand once its files are inlined: it travels inside every
// page this machine serves.
const MaxSize = 512 << 10

// MaxName caps Name, in characters.
const MaxName = 80

// Brand is what `ask-form brand set` stores (<config>/brand.json).
type Brand struct {
	// Name is shown above the form title, next to the logo, and in the
	// browser tab ("Project setup · Acme").
	Name string `json:"name,omitempty"`
	// Logo is an image, as a data: URI, shown above the form title.
	Logo string `json:"logo,omitempty"`
	// Primary is the accent colour ("#rrggbb") — buttons, links, the focus
	// ring, picked choices — with a shade for dark mode derived from it.
	// It wins over a --primary the CSS sets (`brand init`'s starter has one).
	Primary string `json:"primary,omitempty"`
	// Font is a font file, as a data: URI, the whole page is set in. Like
	// Primary, it wins over the CSS.
	Font string `json:"font,omitempty"`
	// CSS goes after ask-form's own stylesheets: overriding the theme tokens
	// (--primary, --background…) recolours everything, any other rule
	// restyles what it selects.
	CSS     string    `json:"css,omitempty"`
	Updated time.Time `json:"updated"`

	tokens string // Tokens(PageCSS()), worked out once by Load
}

// Empty reports whether b changes nothing.
func (b Brand) Empty() bool {
	return b.Name == "" && b.Logo == "" && b.Primary == "" && b.Font == "" && b.CSS == ""
}

// headerCSS lays out the logo and name above the title. It travels with the
// brand rather than in app.css: the relay serves app.css, and a machine may
// run a newer version than its relay.
const headerCSS = `.brand{display:flex;align-items:center;gap:10px;margin:0 0 16px;font-weight:600;color:hsl(var(--muted-foreground))}` +
	`.brand-logo{display:block;height:36px;width:auto;max-width:min(220px,60vw);object-fit:contain}`

// PageCSS is the stylesheet a page gets: the logo and name's layout, the
// CSS (which can restyle them), then the rules Primary and Font make — a
// setting given on its own is meant, so it wins over the file.
func (b Brand) PageCSS() string {
	var css strings.Builder
	if b.Logo != "" || b.Name != "" {
		css.WriteString(headerCSS)
	}
	css.WriteString(b.CSS)
	if b.Primary != "" {
		h, s, l := hexHSL(b.Primary)
		for i, light := range []float64{l, max(l, darkMin)} {
			tok := fmt.Sprintf("--primary:%s;--ring:%s;--primary-foreground:%s",
				triplet(h, s, light), triplet(h, s, light), onColor(h, s, light))
			if i == 0 {
				css.WriteString(":root{" + tok + "}")
			} else {
				css.WriteString("@media (prefers-color-scheme: dark){:root{" + tok + "}}")
			}
		}
	}
	if b.Font != "" {
		css.WriteString(`@font-face{font-family:"ask-form brand";src:url("` + b.Font + `") format("` + fontFormat(b.Font) + `");font-display:swap}`)
		css.WriteString(`body{font-family:"ask-form brand",system-ui,-apple-system,"Segoe UI",Roboto,"Helvetica Neue",sans-serif}`)
	}
	return css.String()
}

// FrameCSS is what html blocks and mockups take from b: its theme tokens.
func (b Brand) FrameCSS() string {
	if b.tokens == "" && (b.CSS != "" || b.Primary != "") {
		return Tokens(b.PageCSS())
	}
	return b.tokens
}

var (
	colorRe = regexp.MustCompile(`^#[0-9a-f]{6}$`)
	logoRe  = regexp.MustCompile(`^data:image/(png|jpeg|gif|webp|avif|svg\+xml|x-icon);base64,[A-Za-z0-9+/]+=*$`)
	fontRe  = regexp.MustCompile(`^data:font/(woff2|woff|ttf|otf);base64,[A-Za-z0-9+/]+=*$`)
)

// Check is what a stored brand must pass before it reaches a page.
func Check(b Brand) error {
	if n := len(b.Name) + len(b.Logo) + len(b.Font) + len(b.CSS); n > MaxSize {
		return fmt.Errorf("brand is %d KB with its files inlined: at most %d KB", n>>10, MaxSize>>10)
	}
	if utf8.RuneCountInString(b.Name) > MaxName || strings.IndexFunc(b.Name, unicode.IsControl) >= 0 {
		return fmt.Errorf("name: one line, at most %d characters", MaxName)
	}
	if b.Logo != "" && !logoRe.MatchString(b.Logo) {
		return errors.New("logo: not an inlined image")
	}
	if b.Font != "" && !fontRe.MatchString(b.Font) {
		return errors.New("font: not an inlined font file")
	}
	if b.Primary != "" && !colorRe.MatchString(b.Primary) {
		return fmt.Errorf("primary %q: a #rrggbb colour", b.Primary)
	}
	// The CSS goes inside a <style> element: "</" could close it.
	if strings.Contains(b.CSS, "</") {
		return errors.New(`the CSS contains "</": write it as "\3c /" inside a string`)
	}
	return nil
}

// Load reads a brand file; a missing one is the zero Brand.
func Load(path string) (Brand, error) {
	var b Brand
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return b, nil
	}
	if err != nil {
		return b, err
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		return Brand{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := Check(b); err != nil {
		return Brand{}, err
	}
	b.tokens = Tokens(b.PageCSS())
	return b, nil
}

// Save writes b to path (0600, atomically: serve may read it at any time).
func Save(path string, b Brand) error {
	if err := Check(b); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".brand-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// File is the brand serve applies, read again whenever the file changes:
// `ask-form brand set` shows on the next page load, no restart.
type File struct {
	path string
	mu   sync.Mutex
	mod  time.Time
	size int64
	b    Brand
}

// Open follows the brand file at path (which need not exist yet).
func Open(path string) *File { return &File{path: path, size: -1} }

// Get is the current brand; the zero Brand when there is none, or when the
// file can't be used (logged once per change).
func (f *File) Get() Brand {
	f.mu.Lock()
	defer f.mu.Unlock()
	fi, err := os.Stat(f.path)
	if err != nil {
		f.mod, f.size, f.b = time.Time{}, -1, Brand{}
		return f.b
	}
	if fi.ModTime().Equal(f.mod) && fi.Size() == f.size {
		return f.b
	}
	f.mod, f.size = fi.ModTime(), fi.Size()
	b, err := Load(f.path)
	if err != nil {
		log.Printf("brand: %v — pages keep ask-form's own look", err)
		b = Brand{}
	}
	f.b = b
	return b
}

// ── url() inlining ───────────────────────────────────────────────────────────

// fileTypes are the files a brand can carry: images and fonts.
var fileTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif",
	".webp": "image/webp", ".avif": "image/avif", ".svg": "image/svg+xml", ".ico": "image/x-icon",
	".woff": "font/woff", ".woff2": "font/woff2", ".ttf": "font/ttf", ".otf": "font/otf",
}

var schemeRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)

// Prepare readies a stylesheet for Save: every url() naming a local file
// (relative to dir, the stylesheet's own directory) becomes a data: URI;
// @import and remote urls are refused, as the page would block them.
func Prepare(css, dir string) (string, error) {
	var out strings.Builder
	for i := 0; i < len(css); {
		c := css[i]
		switch {
		case strings.HasPrefix(css[i:], "/*"):
			end := strings.Index(css[i+2:], "*/")
			if end < 0 {
				out.WriteString(css[i:])
				i = len(css)
				break
			}
			out.WriteString(css[i : i+end+4])
			i += end + 4
		case c == '"' || c == '\'':
			j := stringEnd(css, i)
			out.WriteString(css[i:j])
			i = j
		case c == '@' && foldPrefix(css[i:], "@import"):
			return "", errors.New("@import: the page loads no other stylesheet — paste its rules into this file")
		case (c == 'u' || c == 'U') && foldPrefix(css[i:], "url(") && (i == 0 || !identChar(css[i-1])):
			ref, j, ok := urlArg(css, i+4)
			if !ok {
				return "", fmt.Errorf("unterminated url( at byte %d", i)
			}
			uri, err := resolve(ref, dir)
			if err != nil {
				return "", err
			}
			if uri == "" {
				out.WriteString(css[i:j]) // kept as written
			} else {
				out.WriteString(`url("` + uri + `")`)
			}
			i = j
		default:
			out.WriteByte(c)
			i++
		}
	}
	return out.String(), nil
}

// resolve turns a url() reference into a data: URI ("" = keep it as is).
func resolve(ref, dir string) (string, error) {
	switch {
	case ref == "", strings.HasPrefix(ref, "#"), foldPrefix(ref, "data:"):
		return "", nil
	case strings.HasPrefix(ref, "//"), schemeRe.MatchString(ref):
		return "", fmt.Errorf("url(%s): the page loads nothing from other sites — save the file next to the CSS and write url(./%s)",
			ref, filepath.Base(strings.SplitN(ref, "?", 2)[0]))
	}
	p := ref
	if k := strings.IndexAny(p, "?#"); k >= 0 {
		p = p[:k]
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	ct, ok := fileTypes[strings.ToLower(filepath.Ext(p))]
	if !ok {
		return "", fmt.Errorf("url(%s): only images (png, jpg, gif, webp, avif, svg, ico) and fonts (woff2, woff, ttf, otf)", ref)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("url(%s): %w", ref, err)
	}
	if len(b) > MaxSize {
		return "", fmt.Errorf("url(%s): %d KB, the whole brand can be at most %d KB", ref, len(b)>>10, MaxSize>>10)
	}
	return DataURI(ct, b), nil
}

// DataURI embeds b, of the given content type, as a data: URI.
func DataURI(contentType string, b []byte) string {
	return "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(b)
}

// urlArg reads the argument of a url( starting at i: the reference and the
// index just past the closing parenthesis.
func urlArg(css string, i int) (string, int, bool) {
	for i < len(css) && isSpace(css[i]) {
		i++
	}
	var ref string
	if i < len(css) && (css[i] == '"' || css[i] == '\'') {
		j := stringEnd(css, i)
		if j-i < 2 || css[j-1] != css[i] {
			return "", 0, false
		}
		ref, i = css[i+1:j-1], j
		for i < len(css) && isSpace(css[i]) {
			i++
		}
		if i >= len(css) || css[i] != ')' {
			return "", 0, false
		}
	} else {
		j := strings.IndexByte(css[i:], ')')
		if j < 0 {
			return "", 0, false
		}
		ref, i = strings.TrimSpace(css[i:i+j]), i+j
	}
	return ref, i + 1, true
}

// stringEnd is the index just past the CSS string starting at i (its closing
// quote; a newline or the end of the text ends a broken one).
func stringEnd(css string, i int) int {
	q := css[i]
	for j := i + 1; j < len(css); j++ {
		switch css[j] {
		case '\\':
			j++
		case q:
			return j + 1
		case '\n':
			return j
		}
	}
	return len(css)
}

func foldPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

func identChar(c byte) bool {
	return c == '-' || c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c >= 0x80
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' }
