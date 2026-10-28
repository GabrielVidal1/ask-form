package server

import (
	"crypto/sha256"
	"encoding/base64"
	"regexp"
	"strings"

	"github.com/GabrielVidal1/ask-form/internal/store"
	"github.com/GabrielVidal1/ask-form/web"
)

// An html block (or an html mockup option) is served as a document of its
// own at /f/<id>/frame/<key>[/<n>], framed with sandbox="allow-scripts"
// and sent with a CSP `sandbox` of its own, so it has an opaque origin: no
// access to the page, its cookie or the form. Its policy allows inline styles
// and data: images (the form's assets are inlined), and exactly one script —
// ours, pinned by hash — which reports the document's height to the page and
// fits a fixed-width mockup to its card. Any <script> or on* handler the agent
// wrote is refused by that policy: html blocks and mockups are script-free.
// A widget field's frame is the one exception (widget.go).

// frameScript: dark-mode class, fit-to-width (mockups), height beacon. Fit
// measures the mockup's own box (its top-level content, not what spills out
// of it; a fluid one that fills the frame: its overflow). Wider than the
// frame, it is scaled down to it; narrower, it is scaled up (at most 3×) only
// inside its card (src ending in #card), so the full-size view keeps it at
// its own size. The height is the body's, not the document's: the document
// is never shorter than its frame, so measuring it could only ever grow the
// frame.
const frameScript = `(function(){var d=document.documentElement;` +
	`try{if(matchMedia("(prefers-color-scheme: dark)").matches)d.classList.add("dark")}catch(e){}` +
	`var fit=d.hasAttribute("data-fit"),up=fit&&location.hash==="#card",last=0;` +
	`function size(){var b=document.body;if(!b)return;` +
	`if(fit){b.style.zoom="";var v=d.clientWidth,r=document.createRange();r.selectNodeContents(b);var w=r.getBoundingClientRect().right;` +
	`if(Math.abs(w-v)<=1)w=Math.max(d.scrollWidth,b.scrollWidth);` +
	`if(w>v+1&&v>0)b.style.zoom=String(v/w);else if(up&&w>0&&w<v-1)b.style.zoom=String(Math.min(3,v/w))}` +
	`var h=Math.ceil(Math.max(b.scrollHeight,b.getBoundingClientRect().height));` +
	`if(h!==last){last=h;parent.postMessage({type:"ask-form:frame",h:h},"*")}}` +
	`if(window.ResizeObserver){new ResizeObserver(size).observe(d)}` +
	`addEventListener("load",size);addEventListener("resize",size);setInterval(size,1500);size()})();`

var (
	frameScriptHash = func() string {
		sum := sha256.Sum256([]byte(frameScript))
		return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	}()
	themeCSS = func() string {
		b, _ := web.FS.ReadFile("static/theme.css")
		return string(b)
	}()
	frameBaseCSS = `html,body{margin:0;padding:0;background:transparent;color:hsl(var(--foreground));` +
		`}body{display:flow-root;` +
		`font:13px/1.5 system-ui,-apple-system,"Segoe UI",Roboto,sans-serif}` +
		`img,video,svg,canvas{max-width:100%}a{color:hsl(var(--primary))}`
	headRe = regexp.MustCompile(`(?i)<head[^>]*>`)
	htmlRe = regexp.MustCompile(`(?i)<html[^>]*>`)
	bodyRe = regexp.MustCompile(`(?i)</body\s*>`)
)

// frameCSP is a frame document's policy.
func frameCSP() string {
	return "default-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src data:; media-src data:; " +
		"script-src " + frameScriptHash + "; base-uri 'none'; form-action 'none'; frame-ancestors 'self'; " +
		"sandbox allow-scripts"
}

// frameDoc builds the document for one block or mockup. fit: scale content
// wider than the frame down to it (mockup cards).
func frameDoc(st store.State, prefix, markup string, fit bool, asset func(name string) (string, bool)) string {
	return frameDocWith(st, prefix, markup, fit, "", asset)
}

// frameDocWith is frameDoc with extra markup (a widget's SDK) at the top of
// the head.
func frameDocWith(st store.State, prefix, markup string, fit bool, sdk string, asset func(name string) (string, bool)) string {
	// The form's assets, referenced by URL (prefix + name) in the spec, are
	// inlined: an opaque origin can't send the cookie that guards them.
	for _, a := range st.Assets {
		url := prefix + a.Name
		if strings.Contains(markup, url) {
			if data, ok := asset(a.Name); ok {
				markup = strings.ReplaceAll(markup, url, data)
			}
		}
	}
	head := `<meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">` +
		`<style>` + themeCSS + frameBaseCSS + `</style>` + sdk
	script := `<script>` + frameScript + `</script>`
	attr := ""
	if fit {
		attr = ` data-fit`
	}
	if htmlRe.MatchString(markup) {
		// A full document: our head goes first so its own styles win.
		if loc := headRe.FindStringIndex(markup); loc != nil {
			markup = markup[:loc[1]] + head + markup[loc[1]:]
		} else {
			loc := htmlRe.FindStringIndex(markup)
			markup = markup[:loc[1]] + "<head>" + head + "</head>" + markup[loc[1]:]
		}
		if fit {
			loc := htmlRe.FindStringIndex(markup)
			markup = markup[:loc[1]-1] + attr + markup[loc[1]-1:]
		}
		if loc := bodyRe.FindStringIndex(markup); loc != nil {
			return markup[:loc[0]] + script + markup[loc[0]:]
		}
		return markup + script
	}
	return "<!doctype html><html" + attr + "><head>" + head + "</head><body>" + markup + script + "</body></html>"
}

func dataURI(contentType string, b []byte) string {
	return "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(b)
}
