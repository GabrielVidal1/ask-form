package relay

import (
	"net/http/httptest"
	"testing"
)

func TestCountedPaths(t *testing.T) {
	for p, want := range map[string]bool{
		"/examples": true, "/examples/": true, "/fr/examples": true, "/examples/review-change": true,
		"/examples/review-change/": true, "/examples/review-change/hero.jpg": false, "/examplesx": false,
		"/examples/a/b": false,
	} {
		if got := isExamplePage(p); got != want {
			t.Errorf("isExamplePage(%q) = %v", p, got)
		}
	}
	for p, want := range map[string]string{
		"/f/pick-a-colour_ab12cd": "pick-a-colour_ab12cd", "/forms/x_1/": "x_1",
		"/f/_static/app.css": "", "/f/x/answers": "", "/f/": "", "/fx/y": "",
	} {
		got, ok := formPage(p)
		if got != want || ok != (want != "") {
			t.Errorf("formPage(%q) = %q %v", p, got, ok)
		}
	}
	for ua, want := range map[string]string{
		"": "bot", "curl/8.5": "bot", "Mozilla/5.0 (compatible; Googlebot/2.1)": "bot",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) Safari/604.1": "person",
		"Mozilla/5.0 (Macintosh) HeadlessChrome/140.0":                        "bot",
	} {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("User-Agent", ua)
		if got := who(req); got != want {
			t.Errorf("who(%q) = %s", ua, got)
		}
	}
}
