package stats

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// parseKey splits a counter key back into its name and labels.
func parseKey(key string) (string, map[string]string) {
	i := strings.IndexByte(key, '{')
	if i < 0 || !strings.HasSuffix(key, "}") {
		return key, nil
	}
	labels := map[string]string{}
	for _, kv := range strings.Split(key[i+1:len(key)-1], ",") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if u, err := strconv.Unquote(v); err == nil {
			v = u
		}
		labels[k] = v
	}
	return key[:i], labels
}

// window sums every counter over the days from..to (inclusive); the caller
// holds r.mu.
func (r *Recorder) window(from, to string) map[string]int64 {
	out := map[string]int64{}
	for day, m := range r.db.Days {
		if day < from || day > to {
			continue
		}
		for k, n := range m {
			out[k] += n
		}
	}
	return out
}

// Ratio is kept out of of: people who came back.
type Ratio struct {
	Kept int `json:"kept"`
	Of   int `json:"of"`
}

// Value is the ratio, NaN for an empty cohort.
func (q Ratio) Value() float64 {
	if q.Of == 0 {
		return math.NaN()
	}
	return float64(q.Kept) / float64(q.Of)
}

// Summary is a window of days, grouped for reading. Maps are by label value.
type Summary struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Days  int    `json:"days"`
	Since string `json:"since"` // the first day counted at all

	SignIns      map[string]int64 `json:"signIns"`      // by app
	SignInsWhere map[string]int64 `json:"signInsWhere"` // cloud, machine
	Links        int64            `json:"links"`        // computers linked (`ask-form link`)
	// Active: accounts that called a tool in the window; Returning: those of
	// them that signed in before it.
	Active    int `json:"activeAccounts"`
	Returning int `json:"returningAccounts"`
	// Accounts: every account summary kept (all time).
	Accounts int `json:"accounts"`
	// Retention7d: of the accounts that signed in 8 to 90 days ago, those
	// that called a tool again a week or more after.
	Retention7d Ratio `json:"retention7d"`

	ToolCalls  map[string]int64 `json:"toolCalls"`
	ToolErrors map[string]int64 `json:"toolErrors"`
	Limited    int64            `json:"limited"`

	FormsCreated int64            `json:"formsCreated"`
	FormsWhere   map[string]int64 `json:"formsWhere"`
	FormsKind    map[string]int64 `json:"formsKind"`
	FormsLang    map[string]int64 `json:"formsLang"`
	FormsSize    map[string]int64 `json:"formsSize"` // by question count
	FormsWith    map[string]int64 `json:"formsWith"` // pictures, mockups, conditions
	FieldTypes   map[string]int64 `json:"fieldTypes"`
	Outcomes     map[string]int64 `json:"outcomes"`     // answered, declined, expired, revoked, closed, unknown
	AnswerTime   map[string]int64 `json:"answerTime"`   // by bucket: "1m", "5m", "15m", "1h", "6h", "24h", "more"
	MedianAnswer string           `json:"medianAnswer"` // the bucket the median falls in, "" with no answer
	MultiAnswers int64            `json:"multiAnswers"` // answers to cloud multi forms

	PageViews  map[string]int64 `json:"pageViews"` // by page, people only
	BotViews   int64            `json:"botViews"`
	Downloads  map[string]int64 `json:"downloads"`
	Registered map[string]int64 `json:"machinesRegistered"` // self, key
	FormViews  map[string]int64 `json:"formViews"`          // form pages opened through the relay: cloud, machine
	FormPosts  map[string]int64 `json:"formPosts"`          // answers posted through it

	Now map[string]float64 `json:"now"` // the gauges, as name{labels} → value
}

var bucketNames = map[string]string{
	"60": "1m", "300": "5m", "900": "15m", "3600": "1h", "21600": "6h", "86400": "24h", "inf": "more",
}

// BucketOrder lists the answer-time buckets, fastest first.
var BucketOrder = []string{"1m", "5m", "15m", "1h", "6h", "24h", "more"}

// Summarize sums the last days days (today included).
func (r *Recorder) Summarize(days int) Summary {
	if days < 1 {
		days = 1
	}
	var gauges []Gauge
	if r.Gauges != nil {
		gauges = r.Gauges()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now().UTC()
	to := now.Format(dayLayout)
	from := now.AddDate(0, 0, -(days - 1)).Format(dayLayout)
	s := Summary{
		From: from, To: to, Days: days, Since: r.db.Since.UTC().Format(dayLayout),
		SignIns: map[string]int64{}, SignInsWhere: map[string]int64{}, ToolCalls: map[string]int64{},
		ToolErrors: map[string]int64{}, FormsWhere: map[string]int64{}, FormsKind: map[string]int64{},
		FormsLang: map[string]int64{}, FormsSize: map[string]int64{}, FormsWith: map[string]int64{},
		FieldTypes: map[string]int64{}, Outcomes: map[string]int64{}, AnswerTime: map[string]int64{},
		PageViews: map[string]int64{}, Downloads: map[string]int64{}, Registered: map[string]int64{},
		FormViews: map[string]int64{}, FormPosts: map[string]int64{}, Now: map[string]float64{},
	}
	for k, n := range r.window(from, to) {
		name, l := parseKey(k)
		switch name {
		case "signins":
			s.SignIns[l["app"]] += n
			s.SignInsWhere[l["where"]] += n
		case "links":
			s.Links += n
		case "tool_calls":
			s.ToolCalls[l["tool"]] += n
			if l["result"] == "error" {
				s.ToolErrors[l["tool"]] += n
			}
		case "limited":
			s.Limited += n
		case "forms_created":
			s.FormsCreated += n
			s.FormsWhere[l["where"]] += n
			s.FormsKind[l["kind"]] += n
			s.FormsLang[l["lang"]] += n
		case "forms_by_size":
			s.FormsSize[l["questions"]] += n
		case "forms_with":
			s.FormsWith[l["feature"]] += n
		case "form_fields":
			s.FieldTypes[l["type"]] += n
		case "forms_closed":
			s.Outcomes[l["outcome"]] += n
		case "answer_time":
			s.AnswerTime[bucketNames[l["le"]]] += n
		case "multi_answers":
			s.MultiAnswers += n
		case "page_views":
			if l["who"] == "bot" {
				s.BotViews += n
			} else {
				s.PageViews[l["page"]] += n
			}
		case "downloads":
			s.Downloads[l["what"]] += n
		case "machines_registered":
			s.Registered[l["how"]] += n
		case "form_views":
			s.FormViews[l["via"]] += n
		case "form_posts":
			s.FormPosts[l["via"]] += n
		}
	}
	var answered int64
	for _, n := range s.AnswerTime {
		answered += n
	}
	if answered > 0 {
		var seen int64
		for _, b := range BucketOrder {
			seen += s.AnswerTime[b]
			if seen*2 >= answered {
				s.MedianAnswer = b
				break
			}
		}
	}
	s.Accounts = len(r.db.Accounts)
	for _, a := range r.db.Accounts {
		if n := len(a.Days); n > 0 && a.Days[n-1] >= from {
			s.Active++
			if a.Created < from {
				s.Returning++
			}
		}
	}
	s.Retention7d = r.retention(now)
	for _, g := range gauges {
		s.Now[Key(g.Name, g.Labels...)] = g.Value
	}
	return s
}

// retention: of the accounts that signed in 8 to 90 days ago, those that
// called a tool a week or more after; the caller holds r.mu.
func (r *Recorder) retention(now time.Time) Ratio {
	oldest := now.AddDate(0, 0, -90).Format(dayLayout)
	newest := now.AddDate(0, 0, -8).Format(dayLayout)
	var q Ratio
	for _, a := range r.db.Accounts {
		if a.Created < oldest || a.Created > newest {
			continue
		}
		q.Of++
		created, err := time.Parse(dayLayout, a.Created)
		if err != nil {
			continue
		}
		week := created.AddDate(0, 0, 7).Format(dayLayout)
		if n := len(a.Days); n > 0 && a.Days[n-1] >= week {
			q.Kept++
		}
	}
	return q
}

// active counts the accounts that called a tool in the last days days;
// the caller holds r.mu.
func (r *Recorder) active(now time.Time, days int) int {
	from := now.AddDate(0, 0, -(days - 1)).Format(dayLayout)
	n := 0
	for _, a := range r.db.Accounts {
		if k := len(a.Days); k > 0 && a.Days[k-1] >= from {
			n++
		}
	}
	return n
}

// ── Prometheus ───────────────────────────────────────────────────────────────

var help = map[string]string{
	"signins":             "Accounts that signed in to the chat-app connector, by app and where their forms live.",
	"links":               "Computers linked to a connector account (ask-form link).",
	"tool_calls":          "The connector's tool calls, by tool and result.",
	"limited":             "Connector requests turned away by its rate limits.",
	"forms_created":       "Forms made through the connector.",
	"forms_by_size":       "Forms made through the connector, by number of questions.",
	"forms_with":          "Forms made through the connector with pictures, mockups or conditions.",
	"form_fields":         "Fields of the forms made through the connector, by type.",
	"forms_closed":        "Forms made through the connector, by how they ended.",
	"multi_answers":       "Answers to the connector's cloud forms that take many answers.",
	"page_views":          "Landing and example page views, people and bots apart.",
	"downloads":           "install.sh, the agent's install page, the plugin zip and the claude.ai skill, fetched.",
	"machines_registered": "Machines that registered with the relay, on their own or with a key.",
	"form_views":          "Form pages opened through the relay, cloud forms or machines'.",
	"form_posts":          "Answers posted through the relay, cloud forms or machines'.",
}

// WriteMetrics writes every counter's total since the counts began, the
// time-to-answer histogram and the gauges, in Prometheus' text format.
func (r *Recorder) WriteMetrics(w io.Writer) {
	var gauges []Gauge
	if r.Gauges != nil {
		gauges = r.Gauges()
	}
	r.mu.Lock()
	now := r.now().UTC()
	totals := r.window("", "9999")
	since := r.db.Since
	active := map[string]int{"1d": r.active(now, 1), "7d": r.active(now, 7), "30d": r.active(now, 30)}
	ret := r.retention(now)
	kept := len(r.db.Accounts)
	r.mu.Unlock()

	byName := map[string][]string{}
	for k := range totals {
		name, _ := parseKey(k)
		byName[name] = append(byName[name], k)
	}
	names := make([]string, 0, len(byName))
	for n := range byName {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		if name == "answer_time" || name == "answer_seconds_sum" {
			continue
		}
		keys := byName[name]
		sort.Strings(keys)
		m := "askform_" + name + "_total"
		if h := help[name]; h != "" {
			fmt.Fprintf(w, "# HELP %s %s\n", m, h)
		}
		fmt.Fprintf(w, "# TYPE %s counter\n", m)
		for _, k := range keys {
			fmt.Fprintf(w, "askform_%s_total%s %d\n", name, k[len(name):], totals[k])
		}
	}

	fmt.Fprintln(w, "# HELP askform_answer_seconds Time from a connector form's creation to its answer.")
	fmt.Fprintln(w, "# TYPE askform_answer_seconds histogram")
	var cum int64
	for _, b := range answerBuckets {
		cum += totals[Key("answer_time", "le", fmt.Sprint(b))]
		fmt.Fprintf(w, "askform_answer_seconds_bucket{le=\"%d\"} %d\n", b, cum)
	}
	cum += totals[Key("answer_time", "le", "inf")]
	fmt.Fprintf(w, "askform_answer_seconds_bucket{le=\"+Inf\"} %d\n", cum)
	fmt.Fprintf(w, "askform_answer_seconds_sum %d\n", totals["answer_seconds_sum"])
	fmt.Fprintf(w, "askform_answer_seconds_count %d\n", cum)

	fmt.Fprintln(w, "# HELP askform_active_accounts Connector accounts that called a tool in the last day, 7 days, 30 days.")
	fmt.Fprintln(w, "# TYPE askform_active_accounts gauge")
	for _, win := range []string{"1d", "7d", "30d"} {
		fmt.Fprintf(w, "askform_active_accounts{window=%q} %d\n", win, active[win])
	}
	fmt.Fprintln(w, "# HELP askform_account_summaries Connector accounts the counts keep a summary of.")
	fmt.Fprintln(w, "# TYPE askform_account_summaries gauge")
	fmt.Fprintf(w, "askform_account_summaries %d\n", kept)
	if ret.Of > 0 {
		fmt.Fprintln(w, "# HELP askform_retention_7d_ratio Of the accounts that signed in 8 to 90 days ago, the share that called a tool a week or more after.")
		fmt.Fprintln(w, "# TYPE askform_retention_7d_ratio gauge")
		fmt.Fprintf(w, "askform_retention_7d_ratio %g\n", ret.Value())
		fmt.Fprintln(w, "# HELP askform_retention_7d_cohort The accounts that signed in 8 to 90 days ago.")
		fmt.Fprintln(w, "# TYPE askform_retention_7d_cohort gauge")
		fmt.Fprintf(w, "askform_retention_7d_cohort %d\n", ret.Of)
	}
	fmt.Fprintln(w, "# HELP askform_stats_since_seconds When the counts began (Unix time).")
	fmt.Fprintln(w, "# TYPE askform_stats_since_seconds gauge")
	fmt.Fprintf(w, "askform_stats_since_seconds %d\n", since.Unix())

	seen := map[string]bool{}
	for _, g := range gauges {
		m := "askform_" + g.Name
		if !seen[m] {
			seen[m] = true
			if g.Help != "" {
				fmt.Fprintf(w, "# HELP %s %s\n", m, g.Help)
			}
			fmt.Fprintf(w, "# TYPE %s gauge\n", m)
		}
		fmt.Fprintf(w, "%s %g\n", Key(m, g.Labels...), g.Value)
	}
}

// ── text ─────────────────────────────────────────────────────────────────────

// Text is the summary for a terminal.
func (s Summary) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "ask-form usage, %s → %s (%d days; counted since %s)\n\n", s.From, s.To, s.Days, s.Since)
	line := func(label string, v any) { fmt.Fprintf(&b, "  %-22s %v\n", label, v) }
	b.WriteString("Chat-app connector\n")
	line("sign-ins", fmt.Sprintf("%d  %s", sum(s.SignIns), pairs(s.SignIns)))
	line("  where", pairs(s.SignInsWhere))
	line("computers linked", s.Links)
	line("active accounts", fmt.Sprintf("%d (%d returning)", s.Active, s.Returning))
	if s.Retention7d.Of > 0 {
		line("back after a week", fmt.Sprintf("%d of %d (%.0f %%)", s.Retention7d.Kept, s.Retention7d.Of, 100*s.Retention7d.Value()))
	}
	line("tool calls", fmt.Sprintf("%d  %s", sum(s.ToolCalls), pairs(s.ToolCalls)))
	if n := sum(s.ToolErrors); n > 0 {
		line("  errors", fmt.Sprintf("%d  %s", n, pairs(s.ToolErrors)))
	}
	if s.Limited > 0 {
		line("  rate-limited", s.Limited)
	}
	b.WriteString("\nForms\n")
	line("created", fmt.Sprintf("%d  %s", s.FormsCreated, pairs(s.FormsWhere)))
	line("  kind", pairs(s.FormsKind))
	line("  language", pairs(s.FormsLang))
	line("  questions", ordered(s.FormsSize, []string{"1", "2-3", "4-6", "7-10", "11+"}))
	line("  with", pairs(s.FormsWith))
	line("field types", pairs(s.FieldTypes))
	line("outcomes", pairs(s.Outcomes))
	if s.MedianAnswer != "" {
		line("time to answer", fmt.Sprintf("median ≤ %s  (%s)", s.MedianAnswer, ordered(s.AnswerTime, BucketOrder)))
	}
	if s.MultiAnswers > 0 {
		line("multi answers", s.MultiAnswers)
	}
	b.WriteString("\nRelay\n")
	line("page views", fmt.Sprintf("%d  %s  (+%d bots)", sum(s.PageViews), pairs(s.PageViews), s.BotViews))
	line("downloads", pairs(s.Downloads))
	line("machines registered", pairs(s.Registered))
	line("form pages opened", pairs(s.FormViews))
	line("answers posted", pairs(s.FormPosts))
	if len(s.Now) > 0 {
		b.WriteString("\nNow\n")
		keys := make([]string, 0, len(s.Now))
		for k := range s.Now {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			line(k, s.Now[k])
		}
	}
	return b.String()
}

func sum(m map[string]int64) int64 {
	var n int64
	for _, v := range m {
		n += v
	}
	return n
}

// pairs is "a 3 · b 1", biggest first; "–" when empty.
func pairs(m map[string]int64) string {
	keys := make([]string, 0, len(m))
	for k, v := range m {
		if v != 0 {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return "–"
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s %d", k, m[k])
	}
	return strings.Join(parts, " · ")
}

// ordered is pairs in a fixed order, zeros left out.
func ordered(m map[string]int64, order []string) string {
	var parts []string
	for _, k := range order {
		if m[k] != 0 {
			parts = append(parts, fmt.Sprintf("%s %d", k, m[k]))
		}
	}
	if len(parts) == 0 {
		return "–"
	}
	return strings.Join(parts, " · ")
}
