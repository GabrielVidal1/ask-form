package form

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMarkdownDeepBlockquotesAndURLs(t *testing.T) {
	// Deeply nested blockquotes must not stack-overflow (H-8).
	deep := strings.Repeat("> ", 2000) + "hello"
	out := string(MarkdownHTML(deep))
	if !strings.Contains(out, "hello") {
		t.Fatalf("expected content preserved, got %q", out)
	}
	if strings.Count(out, "<blockquote>") > maxQuoteDepth+1 {
		t.Fatalf("blockquote depth not capped: %d", strings.Count(out, "<blockquote>"))
	}

	// Underscores inside a URL must not be mangled into <em> tags (L-6).
	link := string(MarkdownHTML("[doc](https://example.com/foo_bar_baz)"))
	if !strings.Contains(link, `href="https://example.com/foo_bar_baz"`) {
		t.Fatalf("URL underscores mangled: %s", link)
	}
}

func TestAcceptMatchRejectsHTMLForGenericText(t *testing.T) {
	for _, tc := range []struct {
		accept, name, sniff string
		want                bool
	}{
		{"text/*", "evil.html", "text/html", false},
		{"text/*", "evil.html", "text/plain", false},
		{"text/*", "evil.svg", "text/plain", false},
		{"text/*", "notes.txt", "text/plain", true},
		{".json", "data.json", "text/html", false},
		{".json", "data.json", "text/plain", true},
		{"image/*", "pic.png", "image/png", true},
	} {
		if got := AcceptMatch(tc.accept, tc.name, tc.sniff); got != tc.want {
			t.Errorf("AcceptMatch(%q, %q, %q) = %v, want %v", tc.accept, tc.name, tc.sniff, got, tc.want)
		}
	}
}

func TestSafeFileNameBidiAndReserved(t *testing.T) {
	bidi := SafeFileName("report\u202egnp.exe")
	if strings.ContainsRune(bidi, '\u202e') {
		t.Fatalf("bidi override leaked: %q", bidi)
	}
	for _, name := range []string{"CON", "con.txt", "NUL.pdf", "COM1.log"} {
		got := SafeFileName(name)
		base := strings.ToUpper(strings.SplitN(got, ".", 2)[0])
		if base == "CON" || base == "NUL" || base == "COM1" {
			t.Errorf("reserved Windows name not neutralized: %q -> %q", name, got)
		}
	}
}

func TestSpecBoundsDefaultsAndConditions(t *testing.T) {
	bigList := make([]any, MaxListRows+5)
	for i := range bigList {
		bigList[i] = "x"
	}
	raw1, _ := json.Marshal([]map[string]any{{"key": "items", "label": "Items", "type": "list", "default": bigList}})
	if _, err := Normalize(raw1); err == nil {
		t.Fatal("expected oversized list default to be rejected")
	}

	bigIn := make([]any, MaxOptions+5)
	for i := range bigIn {
		bigIn[i] = "a"
	}
	raw2, _ := json.Marshal([]map[string]any{
		{"key": "a", "label": "A", "type": "text"},
		{"key": "b", "label": "B", "type": "text", "showIf": map[string]any{"key": "a", "in": bigIn}},
	})
	if _, err := Normalize(raw2); err == nil {
		t.Fatal("expected oversized condition 'in' list to be rejected")
	}
}
