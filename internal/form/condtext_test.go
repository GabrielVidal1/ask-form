package form

import (
	"strings"
	"testing"

	"github.com/GabrielVidal1/ask-form/internal/i18n"
)

// A printed page notes when a question applies, in words the respondent
// knows: labels, not keys; option labels, not values.
func TestCondText(t *testing.T) {
	cases := []struct{ cond, en, fr string }{
		{`{"key":"stack","in":["svelte","vue"]}`, `“Stack” is Svelte or Vue`, `« Stack » vaut Svelte ou Vue`},
		{`{"key":"tos","equals":true}`, `“I accept” is ticked`, `« I accept » est coché`},
		{`{"not":{"key":"tos","equals":true}}`, `“I accept” is not ticked`, `« I accept » n'est pas coché`},
		{`{"key":"tags","includes":"a"}`, `“Tags” includes a`, `« Tags » comprend a`},
		{`{"key":"note","includes":"urgent"}`, `“Note” contains “urgent”`, `« Note » contient « urgent »`},
		{`{"all":[{"key":"n","gte":100},{"not":{"key":"note","answered":true}}]}`, `“Budget” is at least 100 and “Note” is left blank`, `« Budget » vaut au moins 100 et « Note » est sans réponse`},
		{`{"any":[{"key":"stack","equals":"vue"},{"all":[{"key":"n","lt":5},{"key":"n","gt":1}]}]}`, `“Stack” is Vue or (“Budget” is less than 5 and “Budget” is more than 1)`, ``},
		{`{"not":{"any":[{"key":"stack","equals":"vue"},{"key":"tos","equals":false}]}}`, `not (“Stack” is Vue or “I accept” is not ticked)`, ``},
	}
	for _, c := range cases {
		spec := `[{"key":"stack","label":"Stack","type":"select","options":[{"value":"svelte","label":"Svelte"},{"value":"vue","label":"Vue"}]},
			{"key":"tos","label":"I accept","type":"checkbox"},
			{"key":"tags","label":"Tags","type":"multiselect","options":["a","b"]},
			{"key":"n","label":"Budget","type":"number"},
			{"key":"note","label":"Note","type":"text"},
			{"key":"q","label":"Q","type":"text","showIf":` + c.cond + `}]`
		fs, err := Normalize([]byte(spec))
		if err != nil {
			t.Fatalf("%s: %v", c.cond, err)
		}
		q := fs[len(fs)-2] // the catch-all comes last
		if got := CondText(i18n.EN, q.ShowIf, fs); got != c.en {
			t.Errorf("%s\n got %q\nwant %q", c.cond, got, c.en)
		}
		if c.fr == "" {
			continue
		}
		// French typography puts no-break spaces inside « ».
		got := strings.NewReplacer(" ", " ", " ", " ").Replace(CondText(i18n.FR, q.ShowIf, fs))
		if got != c.fr {
			t.Errorf("%s (fr)\n got %q\nwant %q", c.cond, got, c.fr)
		}
	}
	if CondText(i18n.EN, nil, nil) != "" {
		t.Error("no condition: no words")
	}
}
