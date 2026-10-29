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
// fits a mockup to its frame. Any <script> or on* handler the agent
// wrote is refused by that policy: html blocks and mockups are script-free.
// A widget field's frame is the one exception (widget.go).

// frameScript: dark-mode class, fit (mockups), height beacon (html blocks).
//
// Fit lays a mockup out at its own size and scales it to fill its frame,
// whole and centred — a card, the fullscreen view, or a tab of its own. Its
// own size is its width (what it draws: text, pictures and painted boxes, not
// the empty full-width blocks around them — measured at a wide probe, then
// shrunk until it stops changing) and the body's height at that width. A
// mockup that fills any width (fluid) is laid out at the frame's width
// instead, wider only when it has to shrink to fit the height. It reports its
// size to the page, which shapes a card or the fullscreen view after it
// (app.js). The beacon's height is the body's, not the document's: the
// document is never shorter than its frame, so measuring it could only ever
// grow the frame.
const frameScript = `(function(){var d=document.documentElement,fit=d.hasAttribute("data-fit"),last="",nat=-1,` +
	`LEAF=/^(SVG|IMG|CANVAS|VIDEO|IFRAME|SELECT|TEXTAREA)$/,SOLID=/^(SVG|IMG|CANVAS|VIDEO|IFRAME|SELECT|TEXTAREA|INPUT|BUTTON|HR|PROGRESS|METER)$/;` +
	`try{if(matchMedia("(prefers-color-scheme: dark)").matches)d.classList.add("dark")}catch(e){}` +
	`function send(m){var k=JSON.stringify(m);if(k!==last){last=k;m.type="ask-form:frame";parent.postMessage(m,"*")}}` +
	`function high(b){return Math.ceil(Math.max(b.scrollHeight,b.getBoundingClientRect().height))}` +
	`function painted(s){return s.backgroundImage!=="none"||s.boxShadow!=="none"||!/^(transparent|rgba\(0, 0, 0, 0\))$/.test(s.backgroundColor)||` +
	`parseFloat(s.borderLeftWidth)+parseFloat(s.borderRightWidth)+parseFloat(s.borderTopWidth)+parseFloat(s.borderBottomWidth)>0}` +
	`function ink(b){var o=b.getBoundingClientRect().left,L=1/0,R=-1/0,g=document.createRange();` +
	`function add(r,c){var l=c?Math.max(r.left,c[0]):r.left,x=c?Math.min(r.right,c[1]):r.right;if(x>l&&r.height>0){L=Math.min(L,l);R=Math.max(R,x)}}` +
	`(function walk(n,c){for(var k=n.firstChild;k;k=k.nextSibling){` +
	`if(k.nodeType===3){if(/\S/.test(k.data)){g.selectNodeContents(k);add(g.getBoundingClientRect(),c)}}` +
	`else if(k.nodeType===1){var s=getComputedStyle(k),t=k.tagName.toUpperCase(),r=k.getBoundingClientRect();if(s.display==="none")continue;` +
	`if(s.visibility!=="hidden"&&(SOLID.test(t)||painted(s)))add(r,c);` +
	`if(!LEAF.test(t))walk(k,s.overflowX==="visible"?c:[Math.max(r.left,c?c[0]:-1/0),Math.min(r.right,c?c[1]:1/0)])}}})(b,null);` +
	`return R>L?[L-o,R-o]:null}` +
	`function natural(b){var w=1600,e,f,n,i;b.style.width=w+"px";e=ink(b);if(!e)return 0;` +
	`if(e[1]>w+1)return Math.ceil(e[1]+Math.max(0,e[0]));if(e[1]-e[0]>=w-2)return 0;` +
	`for(i=0;i<6;i++){n=Math.ceil(e[1]-e[0]);if(n>=w-1)break;b.style.width=n+"px";f=ink(b);` +
	`if(!f||f[0]<-1||f[1]>n+1){w=Math.min(w,Math.ceil(e[1]+Math.max(0,e[0])));break}w=n;e=f}return w}` +
	`function place(){var b=document.body,vw=innerWidth,vh=innerHeight,W,H,h0,z,i;if(!b)return;` +
	`if(!fit){send({h:high(b)});return}` +
	`d.style.overflow="hidden";b.style.boxSizing="border-box";b.style.transform="";if(nat<0)nat=natural(b);` +
	`W=nat||vw;b.style.width=W+"px";H=h0=high(b);` +
	`if(!nat)for(i=0;i<2&&vh>0&&H>vh+1;i++){W=Math.ceil(vw*H/vh);b.style.width=W+"px";H=high(b)}` +
	`z=W>0&&H>0?Math.min(vw/W,vh>0?vh/H:1/0):1;` +
	`b.style.transformOrigin="0 0";b.style.transform="translate("+(vw-W*z)/2+"px,"+Math.max(0,(vh-H*z)/2)+"px) scale("+z+")";` +
	`send(nat?{w:nat,h:H}:{w:vw,h:h0,fluid:1})}` +
	`function again(){nat=-1;place()}` +
	`if(!fit&&window.ResizeObserver)new ResizeObserver(function(){place()}).observe(d);` +
	`addEventListener("load",again);addEventListener("resize",function(){place()});` +
	`if(fit&&document.fonts&&document.fonts.ready)document.fonts.ready.then(again);` +
	`if(!fit)setInterval(place,1500);place()})();`

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

// frameDoc builds the document for one block or mockup. fit: scale the
// content to fill the frame (mockups).
func frameDoc(st store.State, prefix, markup string, fit bool, asset func(name string) (string, bool)) string {
	return frameDocWith(st, prefix, markup, fit, "", "", asset)
}

// frameDocWith is frameDoc with extra markup (a widget's SDK) at the top of
// the head, and the machine's brand tokens (brand.Brand.FrameCSS) after the
// theme's.
func frameDocWith(st store.State, prefix, markup string, fit bool, sdk, brandCSS string, asset func(name string) (string, bool)) string {
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
		`<style>` + themeCSS + frameBaseCSS + brandCSS + `</style>` + sdk
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
