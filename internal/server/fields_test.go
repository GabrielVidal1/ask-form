package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/GabrielVidal1/ask-form/examples"
	"github.com/GabrielVidal1/ask-form/internal/i18n"
)

// The review-change example uses every field added with conditions: its page
// carries the conditions for the script, and the server alone (no JavaScript)
// holds the same line on what is hidden and what is required.
func TestConditionalExamplePage(t *testing.T) {
	list, err := LoadExamples(examples.FS)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(ExamplesHandler(map[string][]Example{i18n.EN: list}, ""))
	defer srv.Close()
	get := func(path string) string {
		res, err := http.Get(srv.URL + path)
		if err != nil || res.StatusCode != 200 {
			t.Fatalf("GET %s: %v %v", path, err, res)
		}
		b, _ := io.ReadAll(res.Body)
		return string(b)
	}
	page := get("/examples/review-change")
	for _, want := range []string{
		`data-show-if="`,                                // a condition for the script
		`data-required-if="`,                            // a requiredIf
		`class="rating stars"`,                          // rating drawn as stars
		`type="radio" name="f.clarity"`,                 // …made of radios, so it works without a script
		`<span class="d-add">`,                          // diff lines
		`<details class="block-details">`,               // details block
		`name="f.requested.where"`,                      // a table's cells
		`name="f.freeze.start"`,                         // a date range's inputs
		`type="time" name="f.window"`,                   // time
		`class="req if-req" aria-hidden="true" hidden>`, // requiredIf marker, off until it applies
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	if strings.Contains(page, `<script>`) {
		t.Error("the answer page carries an inline script")
	}

	post := func(v url.Values) (int, string) {
		res, err := http.PostForm(srv.URL+"/examples/review-change", v)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	// Reject without a reason: the reason (requiredIf) is what's missing; the
	// required table (showIf: approve with changes) is not asked for.
	code, body := post(url.Values{"f.verdict": {"reject"}})
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, "What would make you change your mind?") || strings.Contains(body, "Requested changes</a>") {
		t.Fatalf("reject without reason: %d", code)
	}
	// Reject with a reason: the rollout answers sent anyway are dropped.
	code, body = post(url.Values{"f.verdict": {"reject"}, "f.reason": {"too risky"}, "f.window": {"23:00"}, "f.tell": {"on"}})
	if code != 200 || !strings.Contains(body, "too risky") || strings.Contains(body, "23:00") || strings.Contains(body, `&#34;tell&#34;`) {
		t.Fatalf("reject: %d %s", code, body)
	}
	// Approve with changes needs its table.
	code, _ = post(url.Values{"f.verdict": {"changes"}})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("changes without rows: %d", code)
	}
	code, body = post(url.Values{"f.verdict": {"changes"},
		"f.requested.where": {"a.go", ""}, "f.requested.what": {"rename", ""}, "f.requested.severity": {"nit", ""},
		"f.freeze.start": {"2026-12-24"}, "f.freeze.end": {"2026-12-26"}})
	if code != 200 || !strings.Contains(body, "rename") || !strings.Contains(body, "2026-12-24 → 2026-12-26") {
		t.Fatalf("changes: %d %s", code, body)
	}
}
