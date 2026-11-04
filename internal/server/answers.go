package server

import (
	"html/template"
	"net/http"
	"net/url"

	"github.com/GabrielVidal1/ask-form/internal/i18n"
	"github.com/GabrielVidal1/ask-form/internal/store"
)

// A look back at the whole form once it takes no more answers from this
// visitor: the questions read-only, with what they sent in them — the
// mockups and pictures they chose from, not just the recap's lines. GET
// /f/{id}/answers, the "View the full form" button at the bottom of every
// closed page. Answered: their answers (a multi form: the response this
// browser sent). Declined, withdrawn, expired, a multi form closed before
// this browser answered: the questions as they were asked, empty.
//
// It is the snapshot's page drawn live: frames and pictures load from the
// form's links as on the answer page (so it weighs what that page did,
// whatever a relay lets through) and app.js sizes the frames and opens a
// mockup full size. The page CSS travels in the nonce'd <style>, so an
// older relay's app.css draws it right.

// answersPage is that page.
type answersPage struct {
	formPage
	Back, Label, Banner string
	Note                *snapNote // a declined form's note, when one was left
}

// answersCSS is what the page adds to app.css: the snapshot's look, the
// banner's back link, its buttons as the closed page has them.
const answersCSS = snapCSS +
	`.answers-banner a{font-weight:600;text-decoration:none}.answers-banner a:hover{text-decoration:underline}` +
	`.answers .actions{justify-content:flex-start;margin-top:24px}` +
	`.answers .actions .btn{text-decoration:none;display:inline-flex;align-items:center}`

// formLink is the form's own page as this visitor reaches it: a multi form
// keeps its shareable ?token= address.
func formLink(st store.State, token string) string {
	if st.Multi {
		return Prefix + st.ID + "?token=" + url.QueryEscape(token)
	}
	return Prefix + st.ID
}

// subPage serves the pages under a form's link: only /answers so far.
func (s *Server) subPage(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("page") != "answers" {
		notFound(w)
		return
	}
	s.answersView(w, r)
}

func (s *Server) answersView(w http.ResponseWriter, r *http.Request) {
	st, token, ok := s.authorized(w, r)
	if !ok {
		return
	}
	lang := i18n.Of(st.Lang)
	p := answersPage{Back: formLink(st, token)}
	var answers map[string]any
	mine := s.answered(r, st)
	switch {
	case mine != nil:
		answers = mine.Answers
		p.Label, p.Banner = i18n.T(lang, "Your answers"), i18n.T(lang, "Sent on %s", i18n.Date(lang, mine.At))
	case st.Status == store.Pending:
		// Still taking answers from this browser: the view is the form itself.
		baseHeaders(w.Header())
		w.Header().Set("Location", p.Back)
		w.WriteHeader(http.StatusFound)
		return
	case st.Status == store.Submitted && !st.Multi:
		answers = st.Answers
		when := ""
		if st.ClosedAt != nil {
			when = i18n.Date(lang, *st.ClosedAt)
		}
		p.Label, p.Banner = i18n.T(lang, "Your answers"), i18n.T(lang, "Sent on %s", when)
	default:
		p.Label, p.Banner = i18n.T(lang, "The questions"), snapBanner(lang, st)
		if st.Status == store.Declined && (st.Reason != "" || len(st.ReasonFiles) > 0) {
			p.Note = &snapNote{Text: st.Reason, Files: s.snapFileList(st, st.ReasonFiles)}
		}
	}
	p.formPage = s.readOnlyPage(st, answers, false)
	p.Nonce = newNonce()
	p.PageCSS = template.CSS(answersCSS) + p.PageCSS
	renderPage(w, lang, "answers", p, pageCSP(p.Nonce), http.StatusOK)
}
