package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/GabrielVidal1/ask-form/internal/stats"
)

// form is the Account API's FormJSON (internal/server), the parts used here.
type form struct {
	ID          string           `json:"id"`
	Title       string           `json:"title"`
	Description string           `json:"description,omitempty"`
	Status      string           `json:"status"`
	URL         string           `json:"url,omitempty"`
	PublicURL   string           `json:"publicUrl,omitempty"`
	CreatedAt   time.Time        `json:"createdAt"`
	ExpiresAt   time.Time        `json:"expiresAt"`
	ClosedAt    *time.Time       `json:"closedAt,omitempty"`
	Answers     map[string]any   `json:"answers,omitempty"`
	Reason      string           `json:"reason,omitempty"`
	ReasonFiles []map[string]any `json:"reasonFiles,omitempty"`
	Multi       bool             `json:"multi,omitempty"`
	MaxAnswers  int              `json:"maxAnswers,omitempty"`
	AnswerCount *int             `json:"answerCount,omitempty"`
	Responses   []map[string]any `json:"responses,omitempty"`
	Lang        string           `json:"lang,omitempty"`
	Warning     string           `json:"warning,omitempty"`
}

// The statuses of a form (internal/store).
const (
	statusPending = "pending"
)

// Waiting limits for get_answers: one tool call holds at most maxWait
// (Cloudflare in front of ask-form.xyz cuts a request at 100 s, Claude a tool
// call at 240 s), as slices of at most backendWait (the tunnel to a computer
// gives up after 30 s).
const (
	defaultWait = 60 * time.Second
	maxWait     = 90 * time.Second
	backendWait = 20 * time.Second
)

// machineAssets caps the base64 pictures of a form kept on the user's
// computer: the create call crosses the relay's tunnel, which carries 2 MB.
const machineAssets = 1_400_000

// CardURI is the card's ui:// resource. The version is in the name: hosts
// cache a template by its URI.
const CardURI = "ui://ask-form/card-v1.html"

// ReferenceURI is the full field reference, as a markdown resource.
const ReferenceURI = "ask-form://reference"

const uiExtension = "io.modelcontextprotocol/ui"

const appMime = "text/html;profile=mcp-app"

// Display blocks ask nothing; fieldCount leaves them out.
var displayTypes = map[string]bool{"markdown": true, "html": true, "diff": true, "details": true}

// idRe mirrors store.ValidID (internal/store), loosely: it only keeps a bad
// id from becoming a path.
var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

func toolList() []map[string]any {
	return []map[string]any{
		{
			"name":        "create_form",
			"title":       "Create a form",
			"description": createDescription,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title":       map[string]any{"type": "string", "description": "The form's title, at most 200 characters."},
					"description": map[string]any{"type": "string", "description": "Markdown shown under the title: the context the person needs."},
					"fields": map[string]any{
						"type":        "array",
						"description": "The questions and blocks, in order. Each is an object; see the tool description for the types and keys.",
						"items":       map[string]any{"type": "object"},
					},
					"lang": map[string]any{"type": "string", "enum": []string{"en", "fr"},
						"description": "Language of the page around your questions (buttons, messages). Write the questions in the same language."},
					"ttl":         map[string]any{"type": "string", "description": "How long the form stays open: 30m, 24h, 72h… Default 24h, at most 7 days (168h)."},
					"multi":       map[string]any{"type": "boolean", "description": "One link that any number of people answer (a team vote, feedback), until it expires, reaches max_answers or close_form."},
					"max_answers": map[string]any{"type": "integer", "minimum": 1, "description": "A multi form closes after this many answers."},
					"hide_count":  map[string]any{"type": "boolean", "description": "A multi form doesn't show respondents how many answered."},
					"assets": map[string]any{
						"type":        "array",
						"description": "Pictures the form shows, as base64 (an option's \"image\": \"{{asset:NAME}}\", or <img src=\"{{asset:NAME}}\"> in html). Remote images are blocked.",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"name": map[string]any{"type": "string", "description": "File name, e.g. hero-a.png"},
								"data": map[string]any{"type": "string", "description": "The file's bytes, base64"},
							},
							"required": []string{"name", "data"},
						},
					},
				},
				"required": []string{"title", "fields"},
			},
			"annotations": map[string]any{"title": "Create a form", "readOnlyHint": false, "destructiveHint": false,
				"idempotentHint": false, "openWorldHint": false},
			"_meta": map[string]any{
				"ui":                             map[string]any{"resourceUri": CardURI},
				"ui/resourceUri":                 CardURI, // the flat key, for hosts from before it moved under ui
				"openai/outputTemplate":          CardURI,
				"openai/widgetAccessible":        true,
				"openai/toolInvocation/invoking": "Creating the form…",
				"openai/toolInvocation/invoked":  "Form ready",
			},
		},
		{
			"name":  "get_answers",
			"title": "Get a form's answers",
			"description": "Read a form's status and answers. While the form waits for its answer, this waits up to wait_seconds " +
				"(default 60, at most 90) and returns as soon as it is answered, declined or closed. Still waiting after that: " +
				"call it again, or end your turn and ask the user to tell you when they've answered. A multi form returns the " +
				"answers so far at once.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":           map[string]any{"type": "string", "description": "The form id create_form returned."},
					"wait_seconds": map[string]any{"type": "integer", "minimum": 0, "maximum": int(maxWait / time.Second), "description": "How long to wait for an answer (0 = just look)."},
				},
				"required": []string{"id"},
			},
			"annotations": map[string]any{"title": "Get a form's answers", "readOnlyHint": true, "idempotentHint": true, "openWorldHint": false},
			"_meta":       map[string]any{"ui": map[string]any{"visibility": []string{"model", "app"}}},
		},
		{
			"name":  "close_form",
			"title": "Close a form",
			"description": "Close a form: one waiting for its answer is withdrawn (its link says so), a multi form stops taking " +
				"answers and keeps the ones it has. Final.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"id": map[string]any{"type": "string", "description": "The form id."}},
				"required":   []string{"id"},
			},
			"annotations": map[string]any{"title": "Close a form", "readOnlyHint": false, "destructiveHint": true, "idempotentHint": true, "openWorldHint": false},
		},
		{
			"name":        "list_forms",
			"title":       "List forms",
			"description": "This account's recent forms, newest first: id, title, status, when they were made.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
			"annotations": map[string]any{"title": "List forms", "readOnlyHint": true, "idempotentHint": true, "openWorldHint": false},
		},
	}
}

// toolResult is a CallToolResult.
type toolResult struct {
	Content           []map[string]any `json:"content"`
	StructuredContent any              `json:"structuredContent,omitempty"`
	IsError           bool             `json:"isError,omitempty"`
}

func textResult(text string, structured any) toolResult {
	return toolResult{Content: []map[string]any{{"type": "text", "text": text}}, StructuredContent: structured}
}

func toolError(format string, a ...any) toolResult {
	r := textResult(fmt.Sprintf(format, a...), nil)
	r.IsError = true
	return r
}

func (h *handler) callTool(ctx context.Context, acct Account, params json.RawMessage) (any, error) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "tools/call: " + err.Error()}
	}
	args := p.Arguments
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage("{}")
	}
	var res toolResult
	switch p.Name {
	case "create_form":
		res = h.createForm(ctx, acct, args)
	case "get_answers":
		res = h.getAnswers(ctx, acct, args)
	case "close_form":
		res = h.closeForm(ctx, acct, args)
	case "list_forms":
		res = h.listForms(ctx, acct)
	default:
		return nil, &rpcError{Code: codeInvalidParams, Message: "unknown tool: " + p.Name}
	}
	h.cfg.Stats.Tool(acct.ID, h.where(acct), p.Name, !res.IsError)
	return res, nil
}

// seen tells the stats a form's status, as a tool's result showed it: a
// closed form the connector made is counted once.
func (h *handler) seen(f form) {
	if f.ID == "" || f.Status == "" || f.Status == statusPending {
		return
	}
	var closed time.Time
	if f.ClosedAt != nil {
		closed = *f.ClosedAt
	}
	h.cfg.Stats.FormClosed(f.ID, f.Status, closed)
}

// shape is what the stats keep of a form: its fields' types, how many
// questions, and whether it has pictures, mockups or conditions.
func shape(fields []json.RawMessage, a createArgs, questions int) stats.Shape {
	s := stats.Shape{Questions: questions, Multi: a.Multi, Lang: a.Lang, Pictures: len(a.Assets) > 0}
	for _, raw := range fields {
		var f struct {
			Type       string            `json:"type"`
			ShowIf     json.RawMessage   `json:"showIf"`
			RequiredIf json.RawMessage   `json:"requiredIf"`
			Options    []json.RawMessage `json:"options"`
		}
		json.Unmarshal(raw, &f)
		t := f.Type
		if t == "" {
			t = "text"
		}
		s.Types = append(s.Types, t)
		if t == "html" {
			s.Mockups = true
		}
		if len(f.ShowIf) > 0 || len(f.RequiredIf) > 0 {
			s.Conditions = true
		}
		for _, o := range f.Options {
			var opt struct {
				HTML  string `json:"html"`
				Image string `json:"image"`
			}
			if json.Unmarshal(o, &opt) == nil {
				if opt.HTML != "" {
					s.Mockups = true
				}
				if opt.Image != "" {
					s.Pictures = true
				}
			}
		}
	}
	return s
}

// do calls the Account API. A non-nil toolResult is the error to hand the model.
func (h *handler) do(ctx context.Context, acct Account, method, path string, body []byte) (int, []byte, *toolResult) {
	if h.cfg.Backend == nil {
		r := toolError("ask-form isn't set up to keep forms here.")
		return 0, nil, &r
	}
	status, resp, err := h.cfg.Backend.Do(ctx, acct, method, path, body)
	if err != nil {
		r := toolError("%s", strings.TrimSpace(err.Error()))
		return 0, nil, &r
	}
	return status, resp, nil
}

// apiError is the Account API's error sentence, or a stand-in.
func apiError(status int, body []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error != "" {
		return e.Error
	}
	return fmt.Sprintf("ask-form answered %d %s", status, http.StatusText(status))
}

func (h *handler) where(acct Account) string {
	if acct.Machine != "" {
		return "machine"
	}
	return "cloud"
}

type createArgs struct {
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Fields      json.RawMessage `json:"fields"`
	Lang        string          `json:"lang"`
	TTL         string          `json:"ttl"`
	Multi       bool            `json:"multi"`
	MaxAnswers  int             `json:"max_answers"`
	HideCount   bool            `json:"hide_count"`
	Assets      []struct {
		Name string `json:"name"`
		Data string `json:"data"`
	} `json:"assets"`
}

func (h *handler) createForm(ctx context.Context, acct Account, raw json.RawMessage) toolResult {
	var a createArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return toolError("The arguments aren't valid: %v", err)
	}
	if strings.TrimSpace(a.Title) == "" {
		return toolError("A form needs a title.")
	}
	fields, err := decodeFields(a.Fields)
	if err != nil {
		return toolError("%v", err)
	}
	if len(fields) == 0 {
		return toolError("A form needs at least one field: pass \"fields\" as an array of objects (see the tool description).")
	}
	count := 0
	for _, f := range fields {
		var head struct {
			Type string `json:"type"`
		}
		json.Unmarshal(f, &head)
		if !displayTypes[head.Type] {
			count++
		}
	}
	type asset struct {
		Name string `json:"name"`
		Data string `json:"data"`
	}
	body := struct {
		Title       string            `json:"title"`
		Description string            `json:"description,omitempty"`
		Fields      []json.RawMessage `json:"fields"`
		Lang        string            `json:"lang,omitempty"`
		TTL         string            `json:"ttl,omitempty"`
		Multi       bool              `json:"multi,omitempty"`
		MaxAnswers  int               `json:"maxAnswers,omitempty"`
		HideCount   bool              `json:"hideCount,omitempty"`
		Assets      []asset           `json:"assets,omitempty"`
	}{Title: a.Title, Description: a.Description, Fields: fields, Lang: a.Lang, TTL: a.TTL,
		Multi: a.Multi, MaxAnswers: a.MaxAnswers, HideCount: a.HideCount}
	assetBytes := 0
	for _, x := range a.Assets {
		body.Assets = append(body.Assets, asset{Name: x.Name, Data: x.Data})
		assetBytes += len(x.Data)
	}
	if acct.Machine != "" && assetBytes > machineAssets {
		return toolError("The pictures are too big for a form kept on the user's computer: it travels through the relay's "+
			"tunnel, which carries at most 2 MB, so the assets may hold %.1f MB of base64 in all (these hold %.1f MB). "+
			"Send fewer or smaller pictures (a 1200 px JPEG is plenty).", float64(machineAssets)/1e6, float64(assetBytes)/1e6)
	}
	b, _ := json.Marshal(body)
	status, resp, terr := h.do(ctx, acct, http.MethodPost, "/_mcp/v1/forms", b)
	if terr != nil {
		return *terr
	}
	if status/100 != 2 {
		return toolError("The form wasn't created: %s", apiError(status, resp))
	}
	var f form
	if err := json.Unmarshal(resp, &f); err != nil || f.ID == "" {
		return toolError("ask-form gave an answer this server can't read.")
	}
	link := f.URL
	if f.PublicURL != "" {
		link = f.PublicURL // the one that opens on any device
	}
	lang := f.Lang
	if lang == "" {
		lang = a.Lang
	}
	sh := shape(fields, a, count)
	sh.Lang = lang
	h.cfg.Stats.FormCreated(acct.ID, h.where(acct), f.ID, sh, f.ExpiresAt)
	var sb strings.Builder
	fmt.Fprintf(&sb, "Form created: “%s” (id %s, %s, open until %s).\n", f.Title, f.ID, plural(count, "question", "questions"),
		f.ExpiresAt.UTC().Format("2006-01-02 15:04 UTC"))
	if acct.Machine != "" {
		fmt.Fprintf(&sb, "It lives on the user's computer (%q), not on ask-form.xyz.\n", acct.Machine)
	}
	if f.Multi {
		fmt.Fprintf(&sb, "Share this link with the people who should answer; it takes any number of answers:\n%s\n", link)
		fmt.Fprintf(&sb, "Call get_answers with id %q for the answers so far, and close_form when it should stop taking answers.\n", f.ID)
	} else {
		fmt.Fprintf(&sb, "Give the user this link (put it in your reply as a link; it opens on any device, phone included):\n%s\n", link)
		fmt.Fprintf(&sb, "Then call get_answers with id %q to wait for the answers (each call waits up to %d s). "+
			"Still unanswered after a couple of calls: end your turn and ask the user to tell you when they've answered.\n",
			f.ID, int(maxWait/time.Second))
	}
	sb.WriteString("The link holds the form's token: don't repeat it anywhere else.")
	if f.Warning != "" {
		fmt.Fprintf(&sb, "\nNote: %s", f.Warning)
	}
	structured := map[string]any{
		"id": f.ID, "title": f.Title, "url": link, "status": f.Status, "expiresAt": f.ExpiresAt,
		"fieldCount": count, "multi": f.Multi, "lang": lang, "where": h.where(acct),
	}
	if f.Description != "" {
		structured["description"] = f.Description
	}
	if acct.Machine != "" {
		structured["machine"] = acct.Machine
	}
	return textResult(sb.String(), structured)
}

// decodeFields takes the fields as an array, or as a string holding one (some
// clients send nested JSON as text).
func decodeFields(raw json.RawMessage) ([]json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		raw = json.RawMessage(s)
	}
	var fields []json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("\"fields\" must be an array of objects: %v", err)
	}
	for i, f := range fields {
		var obj map[string]json.RawMessage
		if json.Unmarshal(f, &obj) != nil {
			return nil, fmt.Errorf("field %d is not an object", i+1)
		}
	}
	return fields, nil
}

func formPath(id string) string { return "/_mcp/v1/forms/" + url.PathEscape(id) }

func readID(raw json.RawMessage) (string, *toolResult) {
	var a struct {
		ID string `json:"id"`
	}
	json.Unmarshal(raw, &a)
	a.ID = strings.TrimSpace(a.ID)
	if a.ID == "" {
		r := toolError("Pass the form's id (create_form returned it; list_forms shows them).")
		return "", &r
	}
	if !idRe.MatchString(a.ID) {
		r := toolError("%q is not a form id.", a.ID)
		return "", &r
	}
	return a.ID, nil
}

func (h *handler) getAnswers(ctx context.Context, acct Account, raw json.RawMessage) toolResult {
	id, terr := readID(raw)
	if terr != nil {
		return *terr
	}
	var a struct {
		Wait *float64 `json:"wait_seconds"`
	}
	json.Unmarshal(raw, &a)
	wait := defaultWait
	if a.Wait != nil {
		wait = time.Duration(*a.Wait * float64(time.Second))
	}
	if wait < 0 {
		wait = 0
	}
	if wait > maxWait {
		wait = maxWait
	}
	deadline := h.now().Add(wait)
	for {
		slice := deadline.Sub(h.now()).Round(time.Second)
		if slice > backendWait {
			slice = backendWait
		}
		path := formPath(id)
		if slice >= time.Second {
			path += "?wait=" + slice.String()
		}
		status, resp, terr := h.do(ctx, acct, http.MethodGet, path, nil)
		if terr != nil {
			return *terr
		}
		if status == http.StatusNotFound {
			return toolError("There is no form %q on this account (list_forms shows them).", id)
		}
		if status/100 != 2 {
			return toolError("Couldn't read the form: %s", apiError(status, resp))
		}
		var f form
		if err := json.Unmarshal(resp, &f); err != nil {
			return toolError("ask-form gave an answer this server can't read.")
		}
		h.seen(f)
		if f.Status != statusPending || f.Multi || !h.now().Before(deadline) || ctx.Err() != nil {
			return answersResult(f)
		}
		// A slice that came back early with the form still pending (a short
		// hold upstream): don't spin.
		select {
		case <-ctx.Done():
			return answersResult(f)
		case <-time.After(h.pause(deadline)):
		}
	}
}

// pause between two looks when the backend answered before its wait was up.
func (h *handler) pause(deadline time.Time) time.Duration {
	d := deadline.Sub(h.now())
	if d > time.Second {
		d = time.Second
	}
	if d < 0 {
		d = 0
	}
	return d
}

func answersResult(f form) toolResult {
	var sb strings.Builder
	structured := map[string]any{"id": f.ID, "title": f.Title, "status": f.Status, "expiresAt": f.ExpiresAt, "multi": f.Multi}
	if f.ClosedAt != nil {
		structured["closedAt"] = f.ClosedAt
	}
	switch {
	case f.Multi:
		n := len(f.Responses)
		if f.AnswerCount != nil {
			n = *f.AnswerCount
		}
		structured["answerCount"] = n
		structured["responses"] = nonNil(f.Responses)
		state := "still open, until " + f.ExpiresAt.UTC().Format("2006-01-02 15:04 UTC")
		if f.Status != statusPending {
			state = f.Status
		}
		fmt.Fprintf(&sb, "“%s” has %s (%s).", f.Title, plural(n, "answer", "answers"), state)
		if n > 0 {
			sb.WriteString("\nResponses, oldest first:\n")
			sb.WriteString(indentJSON(f.Responses))
		}
	case f.Status == statusPending:
		fmt.Fprintf(&sb, "Still waiting for the answers to “%s” (open until %s). Call get_answers again to keep waiting, "+
			"or end your turn and ask the user to tell you when they've answered.", f.Title, f.ExpiresAt.UTC().Format("2006-01-02 15:04 UTC"))
	case f.Status == "submitted":
		structured["answers"] = nonNilMap(f.Answers)
		fmt.Fprintf(&sb, "“%s” was answered. Answers (an optional question left blank is absent):\n%s", f.Title, indentJSON(nonNilMap(f.Answers)))
	case f.Status == "declined":
		fmt.Fprintf(&sb, "The user declined “%s”.", f.Title)
		if f.Reason != "" {
			structured["reason"] = f.Reason
			fmt.Fprintf(&sb, " Their reason: %s", f.Reason)
		} else {
			sb.WriteString(" They gave no reason.")
		}
		if len(f.ReasonFiles) > 0 {
			structured["reasonFiles"] = f.ReasonFiles
			var names []string
			for _, rf := range f.ReasonFiles {
				if n, ok := rf["name"].(string); ok {
					names = append(names, n)
				}
			}
			fmt.Fprintf(&sb, " They attached: %s.", strings.Join(names, ", "))
		}
	default: // revoked, expired, closed
		fmt.Fprintf(&sb, "“%s” is %s, with no answers.", f.Title, f.Status)
	}
	return textResult(sb.String(), structured)
}

func (h *handler) closeForm(ctx context.Context, acct Account, raw json.RawMessage) toolResult {
	id, terr := readID(raw)
	if terr != nil {
		return *terr
	}
	status, resp, terr := h.do(ctx, acct, http.MethodPost, formPath(id)+"/close", nil)
	if terr != nil {
		return *terr
	}
	var f form
	json.Unmarshal(resp, &f)
	if status/100 == 2 || status == http.StatusConflict {
		h.seen(f)
	}
	switch {
	case status == http.StatusNotFound:
		return toolError("There is no form %q on this account.", id)
	case status == http.StatusConflict:
		return textResult(fmt.Sprintf("“%s” was already %s.", f.Title, f.Status), map[string]any{"id": id, "status": f.Status})
	case status/100 != 2:
		return toolError("Couldn't close the form: %s", apiError(status, resp))
	}
	what := "withdrawn: its link now says so"
	if f.Multi {
		what = "closed: it takes no more answers, and get_answers still returns the ones it has"
	}
	return textResult(fmt.Sprintf("“%s” is %s.", f.Title, what), map[string]any{"id": f.ID, "status": f.Status})
}

func (h *handler) listForms(ctx context.Context, acct Account) toolResult {
	status, resp, terr := h.do(ctx, acct, http.MethodGet, "/_mcp/v1/forms", nil)
	if terr != nil {
		return *terr
	}
	if status/100 != 2 {
		return toolError("Couldn't list the forms: %s", apiError(status, resp))
	}
	var l struct {
		Forms []form `json:"forms"`
	}
	if err := json.Unmarshal(resp, &l); err != nil {
		return toolError("ask-form gave an answer this server can't read.")
	}
	sort.SliceStable(l.Forms, func(i, j int) bool { return l.Forms[i].CreatedAt.After(l.Forms[j].CreatedAt) })
	items := make([]map[string]any, 0, len(l.Forms))
	var sb strings.Builder
	if len(l.Forms) == 0 {
		sb.WriteString("No forms on this account yet.")
	} else {
		fmt.Fprintf(&sb, "%s, newest first:", plural(len(l.Forms), "form", "forms"))
	}
	for _, f := range l.Forms {
		h.seen(f)
		item := map[string]any{"id": f.ID, "title": f.Title, "status": f.Status, "createdAt": f.CreatedAt, "expiresAt": f.ExpiresAt}
		extra := ""
		if f.Multi {
			item["multi"] = true
			if f.AnswerCount != nil {
				item["answerCount"] = *f.AnswerCount
				extra = ", " + plural(*f.AnswerCount, "answer", "answers")
			}
		}
		items = append(items, item)
		fmt.Fprintf(&sb, "\n- %s · %s%s · “%s” · made %s", f.ID, f.Status, extra, f.Title, f.CreatedAt.UTC().Format("2006-01-02 15:04 UTC"))
	}
	return textResult(sb.String(), map[string]any{"forms": items})
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func indentJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(b)
}

func nonNil(v []map[string]any) []map[string]any {
	if v == nil {
		return []map[string]any{}
	}
	return v
}

func nonNilMap(v map[string]any) map[string]any {
	if v == nil {
		return map[string]any{}
	}
	return v
}
