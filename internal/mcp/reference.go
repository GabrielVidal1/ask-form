package mcp

import (
	_ "embed"
	"encoding/json"
)

// instructions go to the client in initialize: when to reach for the tools.
const instructions = `ask-form puts your questions in a private web form that the user answers in a browser, phone included, and gives you the answers as JSON. Reach for it when you need several decisions or values at once, want the user to compare options visually (mockups, pictures), or other people should answer (multi). Flow: create_form → put the link in your reply → get_answers (waits up to 90 s per call; the chat may also show a live card with the form's status). Write the form in the user's language and pass lang (en or fr). Put your recommendation in each field's "default" and say why in "help".`

// createDescription is create_form's description: the field reference, in
// short. The full one is the ReferenceURI resource (reference.md).
const createDescription = `Put questions in a private web form the user answers in any browser (their phone too); you get the answers as JSON with get_answers. Use it for several decisions or values at once, visual choices (mockups, pictures), or answers from other people (multi: true).

fields is an array, in order. A question: {"key","label","type", "required"?, "default"?, "help"?, "group"?, "showIf"?}. Types:
- text, textarea, email, number (min/max), date (YYYY-MM-DD), time (HH:MM), datetime, daterange (→ {"start","end"})
- radio, select (one; → string), multiselect (several; → array of strings), checkbox (→ bool). "options": strings or {"value","label","description"?}. Picture/mockup choices: an option with "image": "{{asset:NAME}}" (pass the file in assets) or "html": a mockup drawn at a fixed width (240–600 px) with inline styles; give each a label; "columns" 1–6, "aspect" "1/2"|"1"|"2/1".
- slider (min, max, step, minLabel, maxLabel → number), rating (min 1, max ≤ 11 steps, "style":"stars"|"numbers" → number)
- list: rows of text (→ array of strings), or a table with "columns":[{"key","label","type":"text|number|select","options"?}] (→ array of objects); min/max rows
- widget: a control you write as HTML + inline script in a locked frame without network; call askForm.setValue(v) to report the answer; "schema" string|number|boolean|object|array, "height".
Display blocks (ask nothing): {"type":"markdown","text"}, {"type":"html","html"} (no scripts), {"type":"diff","text": unified diff}, {"type":"details","label","text"}.
Not from a chat: "secret" and "file" fields.
- group: questions sharing a group are one page; past ~4 questions, use groups.
- showIf / requiredIf: {"key":"q","equals":v} (or notEquals, in, includes, gt, gte, lt, lte, answered), combined with {"all":[…]}, {"any":[…]}, {"not":{…}}; tests look at questions above.
- Every choice field gets an "Other…" answer and every form ends with an optional "anything_else" box: don't add your own. A radio/select's "Other…" text next to a picked option comes back as "<key>.other".
- html and mockups follow the page's light/dark theme when coloured with hsl(var(--foreground)), hsl(var(--muted-foreground)), hsl(var(--card)), hsl(var(--border)), hsl(var(--primary)).
- Put your recommendation in "default" and why in "help", so one tap accepts it.

Example: [{"type":"markdown","text":"Two choices before I start."},{"key":"stack","label":"Stack","type":"radio","required":true,"options":["Vite","Next.js"],"default":"Vite","help":"Static site, no server needed."},{"key":"deploy","label":"Deploy when done","type":"checkbox","default":true}]

Answers: {"key": value}; an optional question left blank is absent. Write title, questions and options in the user's language and pass lang.`

//go:embed reference.md
var referenceMarkdown string

//go:embed card.html
var cardHTML string

func resourceList() []map[string]any {
	return []map[string]any{
		{"uri": CardURI, "name": "ask-form card", "title": "ask-form card", "mimeType": appMime,
			"description": "The form's live card in the conversation: status, a button that opens it, then one that sends the answers back."},
		{"uri": ReferenceURI, "name": "field-reference", "title": "ask-form field reference", "mimeType": "text/markdown",
			"description": "Every field type, its keys and the shape of its answer."},
	}
}

func (h *handler) readResource(params json.RawMessage) (any, error) {
	var p struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "resources/read: " + err.Error()}
	}
	switch p.URI {
	case CardURI:
		return map[string]any{"contents": []map[string]any{{
			"uri": CardURI, "mimeType": appMime, "text": cardHTML,
			"_meta": map[string]any{
				"ui": map[string]any{
					"prefersBorder": false,
					// No network, no remote assets: only the host's own font
					// (Claude passes Anthropic Sans from assets.claude.ai).
					"csp": map[string]any{"connectDomains": []string{}, "resourceDomains": []string{"https://assets.claude.ai"}},
				},
				"openai/widgetPrefersBorder": false,
				"openai/widgetDescription":   "The form's status, a button that opens it, and one that sends the answers back to the chat.",
				"openai/widgetCSP":           map[string]any{"connect_domains": []string{}, "resource_domains": []string{}},
			},
		}}}, nil
	case ReferenceURI:
		return map[string]any{"contents": []map[string]any{{"uri": ReferenceURI, "mimeType": "text/markdown", "text": referenceMarkdown}}}, nil
	}
	return nil, &rpcError{Code: codeNoResource, Message: "resource not found: " + p.URI}
}
