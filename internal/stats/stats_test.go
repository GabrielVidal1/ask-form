package stats

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func clock(t0 time.Time) (func() time.Time, func(time.Duration)) {
	now := t0
	return func() time.Time { return now }, func(d time.Duration) { now = now.Add(d) }
}

func TestKey(t *testing.T) {
	if got := Key("tool_calls", "tool", "create_form", "result", "ok"); got != `tool_calls{result="ok",tool="create_form"}` {
		t.Fatalf("labels not sorted: %s", got)
	}
	if got := Key("x", "v", `Evil"}\n{`); got != `x{v="eviln"}` { // no quote, brace or backslash gets in
		t.Fatalf("unsafe label: %s", got)
	}
	if got := Key("x", "v", strings.Repeat("a", 100)); len(got) > len(`x{v=""}`)+40 {
		t.Fatalf("label not cut: %s", got)
	}
	name, l := parseKey(Key("forms_created", "where", "cloud", "kind", "multi"))
	if name != "forms_created" || l["where"] != "cloud" || l["kind"] != "multi" {
		t.Fatalf("parseKey: %s %v", name, l)
	}
}

func TestApp(t *testing.T) {
	for name, want := range map[string]string{
		"Claude": "claude", "claude-ai": "claude", "ChatGPT": "chatgpt", "OpenAI MCP": "chatgpt",
		"Le Chat": "lechat", "Cursor": "cursor", "MCP Inspector": "inspector", "": "unnamed",
		"Totally Custom <script>": "other",
	} {
		if got := App(name); got != want {
			t.Errorf("App(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestLifecycle(t *testing.T) {
	now, advance := clock(time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC))
	r := newRecorder(filepath.Join(t.TempDir(), "stats.json"), now)
	r.SignedIn("acct_a", "Claude", Cloud)
	r.SignedIn("acct_b", "ChatGPT", Machine)
	r.Tool("acct_a", Cloud, "create_form", true)
	exp := now().Add(7 * 24 * time.Hour)
	r.FormCreated("acct_a", Cloud, "f1", Shape{Types: []string{"radio", "weird"}, Questions: 1}, exp)
	r.FormCreated("acct_a", Cloud, "f2", Shape{Questions: 5, Multi: true, Lang: "fr"}, exp)
	r.FormCreated("acct_b", Machine, "f3", Shape{Questions: 2}, exp)
	advance(4 * time.Minute)
	r.FormClosed("f1", "submitted", now())
	r.FormClosed("f1", "submitted", now()) // once
	r.FormClosed("unknown-form", "declined", now())
	r.FormClosed("f2", "closed", time.Time{})

	s := r.Summarize(1)
	if s.SignIns["claude"] != 1 || s.SignIns["chatgpt"] != 1 || s.SignInsWhere["machine"] != 1 {
		t.Fatalf("sign-ins: %v %v", s.SignIns, s.SignInsWhere)
	}
	if s.Outcomes["answered"] != 1 || s.Outcomes["closed"] != 1 || s.Outcomes["declined"] != 0 {
		t.Fatalf("outcomes: %v", s.Outcomes)
	}
	if s.AnswerTime["5m"] != 1 || s.MedianAnswer != "5m" {
		t.Fatalf("answer time: %v %q", s.AnswerTime, s.MedianAnswer)
	}
	if s.FieldTypes["radio"] != 1 || s.FieldTypes["other"] != 1 || s.FormsSize["4-6"] != 1 || s.FormsKind["multi"] != 1 || s.FormsLang["fr"] != 1 {
		t.Fatalf("shape: %v %v %v %v", s.FieldTypes, s.FormsSize, s.FormsKind, s.FormsLang)
	}
	if s.Active != 1 {
		t.Fatalf("active: %d", s.Active)
	}

	// f3 (on a computer) never seen closed: a day after it expired, "unknown".
	advance(8 * 24 * time.Hour)
	r.Prune()
	if s := r.Summarize(30); s.Outcomes["unknown"] != 1 || len(r.db.Pending) != 0 {
		t.Fatalf("after prune: %v, %d pending", s.Outcomes, len(r.db.Pending))
	}

	// acct_a comes back 8 days after signing in: kept; acct_b never did.
	r.Tool("acct_a", Cloud, "list_forms", true)
	if q := r.retention(now()); q.Of != 2 || q.Kept != 1 {
		t.Fatalf("retention: %+v", q)
	}
	if s := r.Summarize(1); s.Active != 1 || s.Returning != 1 {
		t.Fatalf("returning: %+v", s)
	}

	var b bytes.Buffer
	r.WriteMetrics(&b)
	for _, line := range []string{
		`askform_answer_seconds_bucket{le="60"} 0`,
		`askform_answer_seconds_bucket{le="300"} 1`,
		`askform_answer_seconds_bucket{le="+Inf"} 1`,
		`askform_answer_seconds_sum 240`,
		`askform_forms_closed_total{outcome="unknown",where="machine"} 1`,
		`askform_retention_7d_ratio 0.5`,
		`askform_tool_calls_total{result="ok",tool="create_form"} 1`,
	} {
		if !strings.Contains(b.String(), line+"\n") {
			t.Errorf("metrics lack %q:\n%s", line, b.String())
		}
	}
	if strings.Contains(b.String(), "acct_") || strings.Contains(r.Summarize(30).Text(), "acct_") {
		t.Error("an account id leaked")
	}
}

func TestFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "stats.json")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	r.Count("downloads", "what", "install_sh")
	r.SignedIn("acct_x", "Claude", Cloud)
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r.Close() // twice is fine
	back, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s := back.Summarize(1)
	if s.Downloads["install_sh"] != 1 || s.Accounts != 1 || s.Since == "" {
		t.Fatalf("after reload: %+v", s)
	}
	var nilRec *Recorder // a relay without counts
	nilRec.Count("x")
	nilRec.Tool("a", Cloud, "t", true)
	nilRec.FormClosed("f", "submitted", time.Now())
	if err := nilRec.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestKnownAndOldDays(t *testing.T) {
	now, advance := clock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	r := newRecorder(filepath.Join(t.TempDir(), "s.json"), now)
	r.Known("acct_old", "Claude", Cloud, now().AddDate(0, 0, -3))
	r.Known("acct_old", "ChatGPT", Machine, now()) // already known: unchanged
	r.Tool("acct_old", Cloud, "create_form", true)
	if s := r.Summarize(1); s.SignIns["claude"] != 0 || s.Active != 1 || s.Returning != 1 {
		t.Fatalf("known account: %+v", s)
	}
	a := r.db.Accounts[hash("acct_old")]
	if a.App != "claude" || a.Where != Cloud {
		t.Fatalf("summary changed: %+v", a)
	}
	advance(keepDays * 24 * time.Hour)
	r.Tool("acct_new", Cloud, "create_form", true) // never seen sign in
	advance(2 * 24 * time.Hour)
	r.Prune()
	if r.db.Accounts[hash("acct_old")] != nil {
		t.Fatal("an account unseen for 400 days stayed")
	}
	if a := r.db.Accounts[hash("acct_new")]; a == nil || a.App != "unknown" {
		t.Fatalf("a new account went: %+v", a)
	}
}
