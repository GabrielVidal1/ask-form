package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestFromThisPage(t *testing.T) {
	cases := []struct {
		name, site, origin string
		want               bool
	}{
		{"no headers (a script, an old browser)", "", "", true},
		{"same-origin, no Origin", "same-origin", "", true},
		{"same-origin, this host", "same-origin", "https://forms.example", true},
		// Every browser's plain form submit from our no-referrer pages.
		{"same-origin, Origin null", "same-origin", "null", true},
		{"Origin null without Fetch Metadata", "", "null", false},
		{"cross-site, Origin null (a sandboxed frame)", "cross-site", "null", false},
		{"same-site, Origin null", "same-site", "null", false},
		{"cross-site, this host", "cross-site", "https://forms.example", false},
		{"another host", "", "https://evil.example", false},
		{"another host claiming same-origin", "same-origin", "https://evil.example", false},
		{"this host over http", "same-origin", "http://forms.example", false},
		{"localhost over http", "same-origin", "http://localhost", false}, // host differs
	}
	for _, tc := range cases {
		r := httptest.NewRequest("POST", "https://forms.example/f/x", nil)
		if tc.site != "" {
			r.Header.Set("Sec-Fetch-Site", tc.site)
		}
		if tc.origin != "" {
			r.Header.Set("Origin", tc.origin)
		}
		if got := fromThisPage(r); got != tc.want {
			t.Errorf("%s: fromThisPage = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A browser submitting a form sends `Origin: null` (our pages are
// no-referrer) with `Sec-Fetch-Site: same-origin`: that send goes through.
// v0.20.0 answered it with a 404 ("form not found", and "refused to connect"
// in a hub's frame, the 404 being frame-ancestors 'none').
func TestBrowserSubmitWithNullOrigin(t *testing.T) {
	create, pub := langServer(t, "")
	open := func() (*respondent, string) {
		code, out := create(map[string]any{"title": "Origin", "fields": json.RawMessage(`[{"key":"x","label":"X"}]`)})
		if code != http.StatusCreated {
			t.Fatalf("create: %d %v", code, out)
		}
		r := openForm(t, pub, out["url"].(string))
		return r, between(r.get(), `name="csrf" value="`, `"`)
	}
	send := func(r *respondent, csrf string, hdr map[string]string) int {
		vals := url.Values{"csrf": {csrf}, "action": {"submit"}, "f.x": {"hello"}}
		req, _ := http.NewRequest("POST", r.base, strings.NewReader(vals.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		res, err := r.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}

	// What Chrome, Firefox and Safari send on the Send button.
	r, csrf := open()
	browser := map[string]string{"Origin": "null", "Sec-Fetch-Site": "same-origin", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"}
	if code := send(r, csrf, browser); code != http.StatusSeeOther {
		t.Fatalf("browser submit (Origin: null, same-origin): %d, want 303", code)
	}
	if page := r.get(); !strings.Contains(page, "data-closed") {
		t.Errorf("after the send the page is the closed one: %.300s", page)
	}

	// The same in a hub's frame: Sec-Fetch-Dest iframe.
	r, csrf = open()
	browser["Sec-Fetch-Dest"] = "iframe"
	if code := send(r, csrf, browser); code != http.StatusSeeOther {
		t.Fatalf("framed submit (Origin: null, same-origin): %d, want 303", code)
	}

	// An opaque origin elsewhere, or null with nothing vouching for it: refused.
	r, csrf = open()
	for _, hdr := range []map[string]string{
		{"Origin": "null", "Sec-Fetch-Site": "cross-site"},
		{"Origin": "null"},
		{"Origin": "https://evil.example", "Sec-Fetch-Site": "same-origin"},
	} {
		if code := send(r, csrf, hdr); code != http.StatusNotFound {
			t.Errorf("%v: %d, want 404", hdr, code)
		}
	}
}
