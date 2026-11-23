package accounts

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"html/template"
	"net/http"
	"net/url"
	"strings"
)

// The consent pages: plain HTML forms that work with JavaScript off (the
// link page's script only shows the linked state sooner — it never submits),
// ask-form's look, a strict CSP.

type consentPage struct {
	Client       string // the client's self-given name ("" = none)
	RedirectHost string
	Req          string // the signed request
	Install      string // <origin>/install.sh
	Host         string // the relay's host, e.g. ask-form.xyz
	Error        string
	Nonce        string
	// FormAction is the redirect_uri's origin as a CSP source (cspOrigin).
	FormAction string
}

type linkPage struct {
	consentPage
	Code   string
	Linked string // the machine the code is linked to ("" = not yet)
	Online string // "online" | "offline" | "" (unknown)
}

type errorPage struct {
	Title, Text string
	Nonce       string
}

var pages = template.Must(template.New("").Parse(`
{{define "head"}}<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<title>{{.}} · ask-form</title>
{{end}}

{{define "style"}}<style nonce="{{.}}">
:root{--bg:36 33% 97%;--fg:24 10% 12%;--card:0 0% 100%;--muted:24 6% 40%;--primary:24 95% 53%;--primary-fg:0 0% 100%;--border:30 15% 86%;--soft:30 20% 93%;--danger:0 72% 45%;color-scheme:light}
@media (prefers-color-scheme:dark){:root{--bg:24 10% 7%;--fg:30 20% 92%;--card:24 9% 10%;--muted:30 8% 62%;--primary:24 95% 56%;--primary-fg:24 30% 8%;--border:24 8% 20%;--soft:24 8% 15%;--danger:0 70% 62%;color-scheme:dark}}
*,*::before,*::after{box-sizing:border-box}
body{margin:0;background:hsl(var(--bg));color:hsl(var(--fg));font:16px/1.55 system-ui,-apple-system,"Segoe UI",Roboto,"Helvetica Neue",sans-serif}
main{max-width:520px;margin:0 auto;padding:32px 16px 40px}
.brand{display:flex;align-items:center;gap:8px;font-weight:650;letter-spacing:-.01em;margin-bottom:28px}
.brand i{width:10px;height:10px;border-radius:50%;background:hsl(var(--primary));display:inline-block}
h1{margin:0 0 12px;font-size:clamp(24px,6vw,30px);line-height:1.2;letter-spacing:-.01em}
p{margin:0 0 14px}
.muted{color:hsl(var(--muted))}
.small{font-size:14px}
code,pre{font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;font-size:.92em}
code{background:hsl(var(--soft));padding:1px 6px;border-radius:6px;overflow-wrap:anywhere}
ul.can{margin:0 0 22px;padding-left:20px}
ul.can li{margin:2px 0}
fieldset{border:0;margin:0 0 22px;padding:0}
legend{font-weight:600;margin-bottom:10px;padding:0}
.choice{display:flex;gap:12px;align-items:flex-start;padding:14px 16px;margin-bottom:10px;border:1px solid hsl(var(--border));border-radius:12px;background:hsl(var(--card));cursor:pointer}
.choice:has(input:checked){border-color:hsl(var(--primary));box-shadow:0 0 0 1px hsl(var(--primary))}
.choice input{margin:4px 0 0;accent-color:hsl(var(--primary));width:18px;height:18px;flex:none}
.choice strong{display:block}
.choice span{display:block;color:hsl(var(--muted));font-size:14px}
.actions{display:flex;gap:10px;flex-wrap:wrap;margin-top:6px}
button{font:inherit;font-weight:600;border-radius:10px;padding:11px 18px;cursor:pointer;border:1px solid hsl(var(--border));background:hsl(var(--card));color:hsl(var(--fg))}
button.primary{background:hsl(var(--primary));border-color:hsl(var(--primary));color:hsl(var(--primary-fg))}
button:focus-visible,input:focus-visible{outline:2px solid hsl(var(--primary));outline-offset:2px}
.error{border:1px solid hsl(var(--danger));color:hsl(var(--danger));border-radius:10px;padding:10px 14px;margin-bottom:18px}
pre.cmd{background:hsl(var(--soft));border:1px solid hsl(var(--border));border-radius:12px;padding:14px 16px;font-size:17px;overflow-x:auto;margin:0 0 14px;user-select:all}
.status{display:flex;align-items:center;gap:8px;font-weight:600}
.status::before{content:"";width:9px;height:9px;border-radius:50%;background:hsl(var(--muted))}
.status.ok::before{background:hsl(140 55% 42%)}
form.inline{display:inline}
.or{margin-top:22px}
</style>{{end}}

{{define "consent"}}{{template "head" "Connect"}}{{template "style" .Nonce}}</head><body><main>
<div class="brand"><i></i>ask-form</div>
<h1>Connect ask-form</h1>
<p>{{if .Client}}<strong>{{.Client}}</strong> <span class="muted small">(the name it gave itself)</span>{{else}}An app{{end}} wants to ask you questions in ask-form forms. You'll go back to <code>{{.RedirectHost}}</code> afterwards.</p>
<p class="muted small">It will be able to:</p>
<ul class="can"><li>create forms for you to answer (or share)</li><li>read their answers</li><li>close them</li></ul>
{{if .Error}}<p class="error" role="alert">{{.Error}}</p>{{end}}
<form method="post" action="/oauth/authorize">
<input type="hidden" name="req" value="{{.Req}}">
<fieldset><legend>Where should your forms live?</legend>
<label class="choice"><input type="radio" name="choice" value="cloud" checked><span><strong>On {{.Host}}</strong><span>Nothing to install. Each form and its answers are deleted 24 hours after it closes.</span></span></label>
<label class="choice"><input type="radio" name="choice" value="machine"><span><strong>On my computer</strong><span>Forms and answers stay on your computer, which needs ask-form running (<code>curl -fsSL {{.Install}} | sh</code>). You'll link it with one command.</span></span></label>
</fieldset>
<div class="actions"><button class="primary" type="submit" name="action" value="allow">Allow</button><button type="submit" name="action" value="deny">Cancel</button></div>
</form>
</main></body></html>{{end}}

{{define "link"}}{{template "head" "Link your computer"}}{{template "style" .Nonce}}</head><body><main>
<div class="brand"><i></i>ask-form</div>
{{if .Linked}}
<h1>Is this your computer?</h1>
<p class="status ok" role="status">Linked to “{{.Linked}}”{{if eq .Online "offline"}} (offline right now: forms work once ask-form serve runs there){{end}}.</p>
<p>Continue only if you ran <code>ask-form link {{.Code}}</code> yourself, on “{{.Linked}}”. Your forms and their answers will go to that computer. If you don't know it, cancel.</p>
<form method="post" action="/oauth/authorize">
<input type="hidden" name="req" value="{{.Req}}">
<input type="hidden" name="choice" value="machine">
<input type="hidden" name="link" value="{{.Code}}">
<input type="hidden" name="confirm" value="{{.Linked}}">
<div class="actions"><button class="primary" type="submit" name="action" value="allow">Yes, that's my computer</button></div>
</form>
{{else}}
<h1>Link your computer</h1>
<p>On the computer that should keep your forms, run:</p>
<pre class="cmd">ask-form link {{.Code}}</pre>
<p class="muted small">It needs ask-form 0.18 or later (<code>ask-form upgrade</code>). Not installed yet: <code>curl -fsSL {{.Install}} | sh</code>, then the command above. The code works for 15 minutes.</p>
{{if .Error}}<p class="error" role="alert">{{.Error}}</p>{{end}}
<p class="status" id="status" role="status">Waiting for your computer…</p>
<p class="small" id="ask" hidden>Continue only if you ran the command yourself, on that computer. If you don't know it, cancel.</p>
<form method="post" action="/oauth/authorize">
<input type="hidden" name="req" value="{{.Req}}">
<input type="hidden" name="choice" value="machine">
<input type="hidden" name="link" value="{{.Code}}">
<input type="hidden" name="confirm" value="" id="confirm">
<div class="actions"><button class="primary" type="submit" name="action" value="allow" id="go">Continue</button></div>
</form>
{{end}}
<p class="small muted or">Or:</p>
<div class="actions">
<form class="inline" method="post" action="/oauth/authorize"><input type="hidden" name="req" value="{{.Req}}"><input type="hidden" name="choice" value="cloud"><button type="submit" name="action" value="allow">Keep them on {{.Host}} instead</button></form>
<form class="inline" method="post" action="/oauth/authorize"><input type="hidden" name="req" value="{{.Req}}"><button type="submit" name="action" value="deny">Cancel</button></form>
</div>
{{if not .Linked}}<script nonce="{{.Nonce}}">
(function(){
  // Shows the linked state as soon as the computer links. It never submits:
  // the user says it is their computer.
  var code={{.Code}}, st=document.getElementById("status"), ask=document.getElementById("ask"),
      confirm=document.getElementById("confirm"), go=document.getElementById("go"), until=Date.now()+15*60*1000;
  // Disable Continue while waiting so a mid-click race cannot confirm a machine before the user sees its name.
  go.disabled=true;
  function poll(){
    if(Date.now()>until) return;
    fetch("/oauth/link",{method:"POST",headers:{"Content-Type":"application/x-www-form-urlencoded"},body:"code="+encodeURIComponent(code),cache:"no-store",credentials:"same-origin"}).then(function(r){return r.json()}).then(function(d){
      if(d.linked){
        st.className="status ok";
        st.textContent="Linked to “"+d.machine+"”"+(d.online===false?" (offline right now: forms work once ask-form serve runs there)":"")+". Is that your computer?";
        confirm.value=d.machine;
        go.textContent="Yes, that's my computer";
        ask.hidden=false;
        setTimeout(function(){go.disabled=false;},400);
        return;
      }
      setTimeout(poll,2000);
    }).catch(function(){setTimeout(poll,4000)});
  }
  setTimeout(poll,1500);
})();
</script>{{end}}
</main></body></html>{{end}}

{{define "error"}}{{template "head" .Title}}{{template "style" .Nonce}}</head><body><main>
<div class="brand"><i></i>ask-form</div>
<h1>{{.Title}}</h1>
<p>{{.Text}}</p>
</main></body></html>{{end}}
`))

// render writes one of the pages with its own nonce and headers.
func (s *Service) render(w http.ResponseWriter, status int, page any) {
	nb := make([]byte, 16)
	rand.Read(nb)
	nonce := base64.RawStdEncoding.EncodeToString(nb)
	host := s.cfg.PublicURL
	if u, err := url.Parse(s.cfg.PublicURL); err == nil && u.Host != "" {
		host = u.Host
	}
	var name string
	var data any
	formAction := "'none'"
	switch p := page.(type) {
	case consentPage:
		p.Nonce, p.Host = nonce, host
		name, data = "consent", p
		formAction = "'self'"
		if p.FormAction != "" {
			formAction += " " + p.FormAction
		}
	case linkPage:
		p.Nonce, p.Host = nonce, host
		name, data = "link", p
		formAction = "'self'"
		if p.FormAction != "" {
			formAction += " " + p.FormAction
		}
	case errorPage:
		p.Nonce = nonce
		name, data = "error", p
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := pages.ExecuteTemplate(&buf, name, data); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'nonce-"+nonce+"'; script-src 'nonce-"+nonce+"'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; frame-ancestors 'none'; form-action "+formAction)
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Robots-Tag", "noindex, nofollow")
	if strings.HasPrefix(s.cfg.PublicURL, "https://") {
		h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
	}
	w.WriteHeader(status)
	w.Write(buf.Bytes())
}
