package brand

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPrepareInlinesLocalFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "logo.png"), []byte("PNG"), 0o600)
	os.MkdirAll(filepath.Join(dir, "fonts"), 0o700)
	os.WriteFile(filepath.Join(dir, "fonts", "a.woff2"), []byte("WOFF"), 0o600)
	css := `/* url(ignored.png) */ .top{background:url( logo.png ) no-repeat}
@font-face{font-family:A;src:URL("fonts/a.woff2?v=2") format("woff2"), local(A)}
.x{content:"url(nope.png)";mask:url(#m);background:url('data:image/gif;base64,R0lG')}`
	out, err := Prepare(css, dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`url("data:image/png;base64,UE5H")`, `url("data:font/woff2;base64,V09GRg==")`,
		"/* url(ignored.png) */", `"url(nope.png)"`, "url(#m)", `url('data:image/gif;base64,R0lG')`, "local(A)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in\n%s", want, out)
		}
	}
}

func TestPrepareRefuses(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "x.css"), []byte("a{}"), 0o600)
	for css, want := range map[string]string{
		`@import "x.css";`:                       "@import",
		`a{background:url(https://cdn.x/a.png)}`: "other sites",
		`a{background:url("//cdn.x/a.png")}`:     "other sites",
		`a{background:url(missing.png)}`:         "missing.png",
		`a{background:url(x.css)}`:               "only images",
		`a{background:url(logo.png`:              "unterminated",
	} {
		if _, err := Prepare(css, dir); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err %v, want it to mention %q", css, err, want)
		}
	}
}

func TestCheck(t *testing.T) {
	if err := Check(Brand{CSS: `a::after{content:"</style><script>"}`}); err == nil {
		t.Error(`"</" accepted`)
	}
	if err := Check(Brand{CSS: strings.Repeat("a", MaxSize+1)}); err == nil {
		t.Error("oversized brand accepted")
	}
	if err := Check(Brand{CSS: ":root{--primary:200 80% 50%}"}); err != nil {
		t.Error(err)
	}
}

func TestFileFollowsChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "brand.json")
	f := Open(path)
	if !f.Get().Empty() {
		t.Fatal("brand without a file")
	}
	if err := Save(path, Brand{CSS: "a{color:red}"}); err != nil {
		t.Fatal(err)
	}
	if got := f.Get().CSS; got != "a{color:red}" {
		t.Fatalf("got %q", got)
	}
	Save(path, Brand{CSS: "a{color:blue}"})
	os.Chtimes(path, time.Now().Add(time.Second), time.Now().Add(time.Second))
	if got := f.Get().CSS; got != "a{color:blue}" {
		t.Fatalf("after a change: %q", got)
	}
	os.WriteFile(path, []byte(`{"css":"</style>"}`), 0o600)
	os.Chtimes(path, time.Now().Add(2*time.Second), time.Now().Add(2*time.Second))
	if !f.Get().Empty() {
		t.Fatal("an unsafe file was applied")
	}
	os.Remove(path)
	if !f.Get().Empty() {
		t.Fatal("brand after the file went")
	}
}

func TestTokens(t *testing.T) {
	css := `/* brand */
:root { --primary: 200 80% 50%; color-scheme: light; --font: "A;B" }
html, :root { --radius: 4px }
body { --not-this: 1; background: red }
.top h1 { font-family: A }
@font-face { font-family: A; src: url("data:font/woff2;base64,AA") }
@media (prefers-color-scheme: dark) { :root { --primary: 200 80% 60% } .x { color: red } }
@media print { .y { display: none } }`
	got := Tokens(css)
	want := `:root{--primary: 200 80% 50%;--font: "A;B"}:root{--radius: 4px}` +
		`@media (prefers-color-scheme: dark){:root{--primary: 200 80% 60%}}`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if Tokens("") != "" || Tokens("a{color:red") != "" {
		t.Fatal("tokens out of nothing")
	}
}

func TestParseColor(t *testing.T) {
	for in, want := range map[string]string{"#F97316": "#f97316", "f97316": "#f97316", "#fa0": "#ffaa00", " #123abc ": "#123abc"} {
		if got, err := ParseColor(in); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	for _, in := range []string{"", "#12", "#12345g", "red", "#1234567"} {
		if _, err := ParseColor(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestPageCSS(t *testing.T) {
	b := Brand{Primary: "#f97316", Font: "data:font/woff2;base64,AA==", CSS: ".top h1{letter-spacing:0}"}
	css := b.PageCSS()
	for _, want := range []string{
		":root{--primary:25 95% 53%;--ring:25 95% 53%;--primary-foreground:0 0% 100%}",
		"@media (prefers-color-scheme: dark){:root{--primary:25 95% 58%;",
		`src:url("data:font/woff2;base64,AA==") format("woff2")`,
		`body{font-family:"ask-form brand",`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("missing %s in\n%s", want, css)
		}
	}
	if !strings.HasPrefix(css, b.CSS) {
		t.Error("the settings don't come after the CSS file")
	}
	// A light colour takes dark text; a dark one is lifted in dark mode.
	if css := (Brand{Primary: "#facc15"}).PageCSS(); !strings.Contains(css, "--primary-foreground:24 10% 10%") {
		t.Errorf("yellow: %s", css)
	}
	if css := (Brand{Primary: "#1e3a8a"}).PageCSS(); !strings.Contains(css, "dark){:root{--primary:224 64% 58%") {
		t.Errorf("navy: %s", css)
	}
	if got := (Brand{Primary: "#f97316"}).FrameCSS(); !strings.Contains(got, "--primary:25 95% 53%") || strings.Contains(got, "font") {
		t.Errorf("frame css: %s", got)
	}
}

func TestCheckParts(t *testing.T) {
	for _, b := range []Brand{
		{Name: strings.Repeat("n", MaxName+1)},
		{Name: "two\nlines"},
		{Logo: "https://x/logo.png"},
		{Logo: `data:text/html;base64,PHNjcmlwdD4=`},
		{Font: "data:image/png;base64,AA=="},
		{Primary: "orange"},
	} {
		if Check(b) == nil {
			t.Errorf("accepted %+v", b)
		}
	}
	ok := Brand{Name: "Acme", Logo: "data:image/svg+xml;base64,PHN2Zz4=", Font: "data:font/ttf;base64,AA==", Primary: "#123456"}
	if err := Check(ok); err != nil {
		t.Error(err)
	}
}
