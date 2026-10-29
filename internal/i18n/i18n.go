// Package i18n is the language of what a respondent reads: the answer page,
// its errors, the thanks and closed pages, and the parts a form gets without
// the agent writing them ("Other…", the "Anything else?" box). English is the
// default and its strings are the keys; every other language is one file
// here that registers its catalogue (fr.go). What the agent wrote (title,
// labels, options) is never translated.
package i18n

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	EN = "en"
	// Default is the language of a form nobody set one for.
	Default = EN
)

// language is one language besides English.
type language struct {
	name    string            // its own name for itself: "Français"
	names   []string          // what Parse accepts besides the code ("french", "français")
	strings map[string]string // English key → text
	one     func(n int) bool  // n takes the singular
	date    func(t time.Time) string
	// typography, when set, rewrites each catalogue string (French spaces).
	typography func(string) string
	// jsKeys' translations, as the page scripts read them.
	js string
}

var languages = map[string]*language{}

// Langs are the languages a form can be in, English first.
var Langs = []string{EN}

// register adds a language (from its file's init).
func register(code string, l *language) {
	if l.typography != nil {
		for k, v := range l.strings {
			l.strings[k] = l.typography(v)
		}
	}
	m := make(map[string]string, len(jsKeys))
	for _, k := range jsKeys {
		if t, ok := l.strings[k]; ok {
			m[k] = t
		}
	}
	b, _ := json.Marshal(m)
	l.js = string(b)
	languages[code] = l
	Langs = append(Langs, code)
	sort.Strings(Langs[1:])
}

// Parse reads a --lang value ("fr", "fr-FR", "French", "français"…).
// "" stays "": not set.
func Parse(s string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(s))
	if v == "" {
		return "", nil
	}
	if i := strings.IndexAny(v, "-_"); i > 0 {
		v = v[:i]
	}
	if v == EN || v == "english" {
		return EN, nil
	}
	for code, l := range languages {
		if v == code {
			return code, nil
		}
		for _, n := range l.names {
			if v == n {
				return code, nil
			}
		}
	}
	return "", fmt.Errorf("unknown language %q (want one of: %s)", s, strings.Join(Langs, ", "))
}

// Of is a stored or configured language as a page uses it: Default unless
// it is one of Langs.
func Of(lang string) string {
	if l, err := Parse(lang); err == nil && l != "" {
		return l
	}
	return Default
}

// Name is lang's name in lang: "English", "Français".
func Name(lang string) string {
	if l := languages[lang]; l != nil {
		return l.name
	}
	return "English"
}

// T is s in lang, formatted with args like fmt.Sprintf. A string with no
// translation stays English.
func T(lang, s string, args ...any) string {
	if l := languages[lang]; l != nil {
		if t, ok := l.strings[s]; ok {
			s = t
		}
	}
	if len(args) > 0 {
		return fmt.Sprintf(s, args...)
	}
	return s
}

// N is T of one or other, whichever n takes in lang (French keeps 0 and 1
// singular), formatted with n.
func N(lang string, n int, one, other string) string {
	single := n == 1
	if l := languages[lang]; l != nil {
		single = l.one(n)
	}
	if single {
		return T(lang, one, n)
	}
	return T(lang, other, n)
}

// Date is a moment the way lang writes it, in UTC: "2 Jan 2006, 15:04 UTC",
// "2 janvier 2006 à 15:04 UTC".
func Date(lang string, t time.Time) string {
	if l := languages[lang]; l != nil {
		return l.date(t.UTC())
	}
	return t.UTC().Format("2 Jan 2006, 15:04 UTC")
}

// JS is what the page scripts need of lang's catalogue, as the JSON they
// read from <html data-l10n>: English key → text. "" for English, which
// the scripts carry themselves. The machine sends it with each page: the
// relay serving the scripts needs no translations of its own.
func JS(lang string) string {
	if l := languages[lang]; l != nil {
		return l.js
	}
	return ""
}

// jsKeys are the strings app.js, upload.js and widget.js look up ({n}-style
// placeholders, filled in by the scripts).
var jsKeys = []string{
	"Wait for the upload to finish.", "Enter a date (YYYY-MM-DD).", "Enter a number.",
	"Type your answer next to “Other…”.", "Interact with this first.", "Pick at least one.",
	"This question is required.", "Pick both dates.", "The end is before the start.",
	"Add at least {n}.", "At least {n}.", "At most {n}.", "Enter an email address, like name@example.com.",
	"That value is too long.", "Remove this row", "Show", "Hide", "Page {n}", "Page {n}: {title}",
	"← Back", "Next →", "{n} of {m} answered", "Yes", "No", "🔒 {n} characters — not shown",
	"Send these answers?", "The form closes once sent — you can't change them afterwards.",
	"No answers — the form will be sent empty.", "Back", "Confirm & send", "Close",
	"Uploaded file", "Remove {name}", "Uploading {name}", "Upload failed.", "Try again",
	"That file was refused.", "The connection dropped.", "The upload didn't finish — try again.",
	"At most {n} file.", "At most {n} files.", "That file is empty.", "Over the {size} limit.",
	"That kind of file isn't accepted here.", "B", "KB", "MB",
	"The widget's answer is too large to send.", "The widget gave a {kind}; this question wants a {schema}.",
}
