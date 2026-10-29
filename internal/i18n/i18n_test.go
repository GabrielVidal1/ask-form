package i18n

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	for in, want := range map[string]string{"": "", "en": EN, "EN": EN, "english": EN, "fr": FR, "fr-FR": FR, "fr_CA": FR, "Français": FR, " french ": FR} {
		if got, err := Parse(in); err != nil || got != want {
			t.Errorf("Parse(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := Parse("de"); err == nil || !strings.Contains(err.Error(), "en, fr") {
		t.Errorf("Parse(de): %v", err)
	}
	if Of("de") != EN || Of("") != EN || Of("fr") != FR {
		t.Error("Of falls back to English")
	}
}

func TestPluralAndDate(t *testing.T) {
	for _, c := range []struct {
		lang string
		n    int
		want string
	}{{EN, 0, "0 answers"}, {EN, 1, "1 answer"}, {EN, 2, "2 answers"}, {FR, 0, " : il a reçu 0 réponse"}, {FR, 1, " : il a reçu 1 réponse"}, {FR, 2, " : il a reçu 2 réponses"}} {
		got := N(c.lang, c.n, ": it took %d answer", ": it took %d answers")
		if c.lang == EN {
			got = strings.TrimPrefix(got, ": it took ")
		}
		if got != frSpacing.Replace(c.want) {
			t.Errorf("N(%s, %d) = %q, want %q", c.lang, c.n, got, c.want)
		}
	}
	at := time.Date(2026, 3, 1, 9, 5, 0, 0, time.FixedZone("CET", 3600))
	if got := Date(EN, at); got != "1 Mar 2026, 08:05 UTC" {
		t.Errorf("Date(en) = %q", got)
	}
	if got := Date(FR, at); got != "1er mars 2026 à 08:05 UTC" {
		t.Errorf("Date(fr) = %q", got)
	}
}

// French typography: no plain space before ? ! ; : in the catalogue.
func TestFrenchSpacing(t *testing.T) {
	if got := T(FR, "Send these answers?"); got != "Envoyer ces réponses ?" {
		t.Errorf("%q", got)
	}
	for k, v := range fr {
		if regexp.MustCompile(` [?!;:]`).MatchString(v) {
			t.Errorf("%q: a plain space before punctuation in %q", k, v)
		}
	}
}

// Every translation keeps its key's fmt verbs and {placeholders}, in order.
func TestPlaceholders(t *testing.T) {
	verbs := regexp.MustCompile(`%[dsv]|\{[a-z]+\}`)
	for code, l := range languages {
		for k, v := range l.strings {
			a, b := verbs.FindAllString(k, -1), verbs.FindAllString(v, -1)
			if strings.Join(a, ",") != strings.Join(b, ",") {
				t.Errorf("%s %q → %q: placeholders %v vs %v", code, k, v, a, b)
			}
		}
	}
}

// Every string the code, the templates and the page scripts look up has a
// translation in every language: a missing one would show in English.
func TestCatalogueComplete(t *testing.T) {
	root := filepath.Join("..", "..")
	str := `"((?:[^"\\]|\\.)*)"`
	patterns := map[string][]*regexp.Regexp{
		".go": {
			regexp.MustCompile(`i18n\.T\([^,()]+, ` + str),
			regexp.MustCompile(`i18n\.N\([^,]+, [^,]+, ` + str + `, ` + str),
			regexp.MustCompile(`\bt\(` + str),                                       // internal/form's local t
			regexp.MustCompile(`message\(w, [^,]+, http\.\w+, ` + str + `, ` + str), // (*Server).message, renderMessage
			regexp.MustCompile(`badUpload\(\w+, ` + str),
		},
		".html": {
			regexp.MustCompile(`[{(]t ` + str),
			regexp.MustCompile(`\btn \S+ ` + str + ` ` + str),
		},
		".js": {regexp.MustCompile(`\btr\(` + str)},
	}
	keys := map[string]string{} // key → where
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && p != root && (d.Name() == "node_modules" || d.Name() == "landing" || strings.HasPrefix(d.Name(), ".")) {
			return filepath.SkipDir
		}
		res := patterns[filepath.Ext(p)]
		if d.IsDir() || res == nil || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, re := range res {
			for _, m := range re.FindAllStringSubmatch(string(b), -1) {
				for _, s := range m[1:] {
					k, err := strconv.Unquote(`"` + s + `"`)
					if err != nil {
						k = s
					}
					keys[k] = p
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) < 150 {
		t.Fatalf("only %d keys found: the patterns are off", len(keys))
	}
	for _, k := range jsKeys {
		keys[k] = "jsKeys"
	}
	var missing []string
	for code, l := range languages {
		for k, where := range keys {
			if _, ok := l.strings[k]; !ok {
				missing = append(missing, code+": "+strconv.Quote(k)+" ("+where+")")
			}
		}
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Error("no translation: " + m)
	}
}

func TestJS(t *testing.T) {
	if JS(EN) != "" {
		t.Error("English needs no catalogue on the page")
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(JS(FR)), &m); err != nil || len(m) != len(jsKeys) {
		t.Fatalf("JS(fr): %d of %d keys, %v", len(m), len(jsKeys), err)
	}
	if m["Confirm & send"] != "Confirmer et envoyer" {
		t.Errorf("JS(fr): %v", m["Confirm & send"])
	}
}
