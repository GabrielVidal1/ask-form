package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/GabrielVidal1/ask-form/examples"
	"github.com/GabrielVidal1/ask-form/internal/brand"
	"github.com/GabrielVidal1/ask-form/internal/i18n"
	"github.com/GabrielVidal1/ask-form/internal/server"
	"github.com/GabrielVidal1/ask-form/web"
)

// ── brand ────────────────────────────────────────────────────────────────────

const brandUsage = `usage: ask-form brand set [FILE.css | -] [--name TEXT] [--logo IMAGE] [--primary COLOR] [--font FONT]
       ask-form brand show [--css]
       ask-form brand clear [css | name | logo | primary | font]…
       ask-form brand init                 a starter stylesheet with every theme token, on stdout
       ask-form brand preview [FILE.css] [the flags of set] [--lang en|fr] [--no-open]`

// brandPath is where the brand lives; serve reads <its --config>/brand.json.
func brandPath() string { return filepath.Join(configDir(), "brand.json") }

func cmdBrand(args []string) int {
	if len(args) == 0 {
		return fail(exitUsage, "%s", brandUsage)
	}
	switch args[0] {
	case "set":
		return brandSet(args[1:])
	case "show":
		return brandShow(args[1:])
	case "clear", "reset":
		return brandClear(args[1:])
	case "init":
		fmt.Print(brandStarter())
		return exitOK
	case "preview":
		return brandPreview(args[1:])
	}
	return fail(exitUsage, "%s", brandUsage)
}

// brandParts are the flags set and preview share.
type brandParts struct{ name, logo, primary, font *string }

func brandFlags(cmd string) (*flag.FlagSet, brandParts) {
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	return fs, brandParts{
		name:    fs.String("name", "", "brand name, shown above the title and in the browser tab"),
		logo:    fs.String("logo", "", "image shown above the title (png, jpg, gif, webp, avif, svg)"),
		primary: fs.String("primary", "", "accent colour, e.g. #f97316 (buttons, links, picked choices)"),
		font:    fs.String("font", "", "font file the page is set in (woff2, woff, ttf, otf)"),
	}
}

// apply puts the stylesheet at cssPath ("-" = stdin, "" = keep) and the
// parts given on the command line onto b.
func (p brandParts) apply(b *brand.Brand, cssPath string, stdin []byte) error {
	var err error
	switch cssPath {
	case "":
	case "-":
		if b.CSS, err = brand.Prepare(string(stdin), "."); err != nil {
			return err
		}
	default:
		raw, err := os.ReadFile(cssPath)
		if err != nil {
			return err
		}
		if b.CSS, err = brand.Prepare(string(raw), filepath.Dir(cssPath)); err != nil {
			return fmt.Errorf("%s: %w", cssPath, err)
		}
	}
	if s := strings.TrimSpace(*p.name); s != "" {
		b.Name = s
	}
	if *p.logo != "" {
		if b.Logo, err = brand.ImageFile(*p.logo); err != nil {
			return fmt.Errorf("--logo %w", err)
		}
	}
	if *p.primary != "" {
		if b.Primary, err = brand.ParseColor(*p.primary); err != nil {
			return fmt.Errorf("--primary %w", err)
		}
	}
	if *p.font != "" {
		if b.Font, err = brand.FontFile(*p.font); err != nil {
			return fmt.Errorf("--font %w", err)
		}
	}
	return brand.Check(*b)
}

// brandCSSArg is the one stylesheet argument set and preview take.
func brandCSSArg(pos []string) (string, []byte, error) {
	switch len(pos) {
	case 0:
		return "", nil, nil
	case 1:
	default:
		return "", nil, fmt.Errorf("one stylesheet at most (got %s)", strings.Join(pos, " "))
	}
	if pos[0] != "-" {
		return pos[0], nil, nil
	}
	b, err := io.ReadAll(io.LimitReader(os.Stdin, 4*brand.MaxSize))
	return "-", b, err
}

func brandSet(args []string) int {
	fs, parts := brandFlags("brand set")
	cssPath, stdin, err := brandCSSArg(interleaved(fs, args))
	if err != nil {
		return fail(exitUsage, "%v", err)
	}
	if fs.NFlag() == 0 && cssPath == "" {
		return fail(exitUsage, "%s", brandUsage)
	}
	b, err := brand.Load(brandPath())
	if err != nil {
		// A broken file is replaced, not merged into.
		fmt.Fprintf(os.Stderr, "ask-form: replacing the unusable brand: %v\n", err)
		b = brand.Brand{}
	}
	if err := parts.apply(&b, cssPath, stdin); err != nil {
		return fail(exitErr, "%v", err)
	}
	b.Updated = time.Now().UTC().Truncate(time.Second)
	if err := brand.Save(brandPath(), b); err != nil {
		return fail(exitErr, "%v", err)
	}
	out := brandSummary(b)
	out["applies"] = "to every page of this machine's forms from their next load (no restart); `ask-form brand preview` shows it"
	printJSON(out)
	return exitOK
}

func brandShow(args []string) int {
	fs := flag.NewFlagSet("brand show", flag.ExitOnError)
	css := fs.Bool("css", false, "print the stored stylesheet itself")
	interleaved(fs, args)
	b, err := brand.Load(brandPath())
	if err != nil {
		return fail(exitErr, "%v", err)
	}
	if *css {
		fmt.Print(b.CSS)
		return exitOK
	}
	printJSON(brandSummary(b))
	return exitOK
}

func brandClear(args []string) int {
	path := brandPath()
	if len(args) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fail(exitErr, "%v", err)
		}
		printJSON(brandSummary(brand.Brand{}))
		return exitOK
	}
	b, err := brand.Load(path)
	if err != nil {
		return fail(exitErr, "%v", err)
	}
	for _, part := range args {
		switch part {
		case "css":
			b.CSS = ""
		case "name":
			b.Name = ""
		case "logo":
			b.Logo = ""
		case "primary":
			b.Primary = ""
		case "font":
			b.Font = ""
		default:
			return fail(exitUsage, "%s", brandUsage)
		}
	}
	if b.Empty() {
		os.Remove(path)
	} else {
		b.Updated = time.Now().UTC().Truncate(time.Second)
		if err := brand.Save(path, b); err != nil {
			return fail(exitErr, "%v", err)
		}
	}
	printJSON(brandSummary(b))
	return exitOK
}

// brandSummary describes a brand without dumping its files.
func brandSummary(b brand.Brand) map[string]any {
	out := map[string]any{"file": brandPath()}
	if b.Empty() {
		out["brand"] = "none: ask-form's own look"
		return out
	}
	if b.Name != "" {
		out["name"] = b.Name
	}
	if b.Logo != "" {
		out["logo"] = dataInfo(b.Logo)
	}
	if b.Primary != "" {
		out["primary"] = b.Primary
	}
	if b.Font != "" {
		out["font"] = dataInfo(b.Font)
	}
	if b.CSS != "" {
		out["css"] = sizeText(len(b.CSS))
	}
	out["size"] = sizeText(len(b.PageCSS()) + len(b.Logo))
	out["updated"] = b.Updated
	return out
}

// dataInfo is "image/png, 12 KB" for a data: URI.
func dataInfo(uri string) string {
	ct, data, _ := strings.Cut(strings.TrimPrefix(uri, "data:"), ";base64,")
	return ct + ", " + sizeText(len(data)*3/4)
}

func sizeText(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%d bytes", n)
	}
	return fmt.Sprintf("%d KB", (n+512)>>10)
}

// brandStarter is `brand init`: every theme token at ask-form's own value,
// ready to edit, and pointers to the rest.
func brandStarter() string {
	theme, _ := web.FS.ReadFile("static/theme.css")
	body := string(theme)
	if _, rest, ok := strings.Cut(body, "*/"); ok {
		body = strings.TrimLeft(rest, "\n")
	}
	return `/* ask-form brand: the look of every form this machine serves.

   ask-form brand preview brand.css    see it on the example forms (reload after an edit)
   ask-form brand set brand.css        apply it (no restart; set it again after an edit)

   The tokens below are ask-form's own values: change the ones you want and
   delete the rest. Colours are "H S% L%" triplets, used as hsl(var(--x)); the
   second block is dark mode. html blocks and mockups in a form take these
   tokens too. Images and fonts next to this file work as url(./logo.png):
   they are inlined when you set it (nothing loads from other sites).
   --name, --logo, --primary and --font on the command line cover the usual
   cases without CSS; --primary and --font win over this file. */

` + body + `
/* Any rule of the page can be overridden, e.g.:

body { font-family: "Inter", system-ui, sans-serif; }   with an @font-face above
h1 { letter-spacing: -0.03em; }
.btn.primary { border-radius: 999px; }
.choice, .card { border-radius: 4px; }
.group-title { color: hsl(var(--foreground)); }
.brand-logo { height: 28px; }                           the --logo image
@media (prefers-color-scheme: dark) { .brand-logo { filter: invert(1); } }
*/
`
}

// brandPreview serves the example forms drawn with the brand — the stored
// one, with whatever the command line changes, read again on every page so
// an edit to the stylesheet shows on reload. Nothing is saved.
func brandPreview(args []string) int {
	fs, parts := brandFlags("brand preview")
	listen := fs.String("listen", "127.0.0.1:0", "address to serve the preview on (loopback)")
	noOpen := fs.Bool("no-open", false, "don't open a browser, only print the address")
	langFlag := fs.String("lang", os.Getenv("ASK_FORM_LANG"), "language of the example forms: "+strings.Join(i18n.Langs, ", ")+" (default: the machine's, else en)")
	cssPath, stdin, err := brandCSSArg(interleaved(fs, args))
	if err != nil {
		return fail(exitUsage, "%v", err)
	}
	if !isLoopback(*listen) {
		return fail(exitUsage, "--listen %s: a loopback address", *listen)
	}
	lang, err := i18n.Parse(*langFlag)
	if err != nil {
		return fail(exitUsage, "--lang: %v", err)
	}
	lang = i18n.Of(lang)
	stored, err := brand.Load(brandPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "ask-form: the stored brand is unusable, previewing without it: %v\n", err)
		stored = brand.Brand{}
	}
	var (
		mu       sync.Mutex
		last     brand.Brand
		lastErr  string
		build    = func() (brand.Brand, error) { b := stored; return b, parts.apply(&b, cssPath, stdin) }
		previewb = func() brand.Brand {
			mu.Lock()
			defer mu.Unlock()
			b, err := build()
			if err != nil {
				if err.Error() != lastErr {
					log.Printf("brand: %v — showing the last good version", err)
				}
				lastErr = err.Error()
				return last
			}
			last, lastErr = b, ""
			return b
		}
	)
	if last, err = build(); err != nil {
		return fail(exitErr, "%v", err)
	}
	exs, err := server.LoadExamplesIn(examples.FS, lang)
	if err != nil || len(exs) == 0 {
		return fail(exitErr, "examples: %v", err)
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return fail(exitErr, "%v", err)
	}
	index := "http://" + ln.Addr().String() + server.ExamplesPrefix(lang)
	url := index + "/" + exs[0].Name
	opened := !*noOpen && openBrowser(url)
	printJSON(map[string]any{"url": url, "examples": index, "opened": opened,
		"stop": "Ctrl-C (nothing is saved: `ask-form brand set` applies it)"})
	srv := server.HTTPServer("", server.PreviewHandler(exs, lang, previewb), 30*time.Second)
	go func() {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		<-ctx.Done()
		srv.Close()
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fail(exitErr, "%v", err)
	}
	return exitOK
}
