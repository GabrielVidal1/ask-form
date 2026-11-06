package server

import (
	"crypto/sha256"
	"encoding/base64"
	"regexp"
	"strings"

	"github.com/GabrielVidal1/ask-form/internal/brand"
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
// own size is the narrowest width that draws it as a wide probe (1600 px)
// does — no taller (nothing wrapped), nothing sticking out, every text and
// painted box as wide as it was (a centred caption or a toggle pushed right
// may move closer, nothing may shrink) — and the body's height at that
// width. It shrinks to the width of what it draws while that holds, which
// handles a centred card; that step forgets the padding around the ink
// (text in a padded box would wrap a word a line, then be scaled up to fill
// the card), so when it fails the padding is mirrored and a binary search,
// no lower than its widest piece, finds the narrowest width that holds. A
// mockup that draws more at a wider probe (2000 px: a paragraph that wraps,
// a box across the page), or that no narrower width holds, follows the
// width it's given: it is fluid. A
// mockup that fills any width (fluid) is laid out at the frame's width
// instead, wider only when it has to shrink to fit the height. It reports its
// size to the page, which shapes a card or the fullscreen view after it
// (app.js). The beacon's height is the body's, not the document's: the
// document is never shorter than its frame, so measuring it could only ever
// grow the frame. It also sends the height the body takes at a printed page's
// width (A4 and Letter, the larger), which the page gives the frame while it
// prints.
//
// Legibility: an agent writes a mockup for the page it pictures — most often
// a light one, color:#333 here and background:#eee there — while the page may
// be dark (the OS, or a hub's theme in a frame of its own), and a brand may
// have no dark palette at all. The page tells each frame what is behind it
// (`ask-form:backdrop`: the colour it paints there and its colour scheme,
// app.js). The frame takes the palette of that side — the theme's and the
// brand's tokens for it, data-af-scheme (frameSchemeCSS) — and keeps the
// page's colour scheme, without which Chromium paints the frame's canvas
// opaque. Then, on a dark backdrop, it checks every piece of text against
// what is drawn under it: when some is unreadable (contrast under 3:1) and
// the mockup drawn as written, on the light palette and a light sheet of its
// own (data-af-paper), leaves less of it unreadable, it is drawn that way.
// Without a word from the page (a snapshot, an older relay's app.js) the
// backdrop is taken to be the frame's own background, or the canvas for a
// frame open in a tab of its own. The first word from the page also has the
// frame report its size again: a report sent before the page's script was
// listening is lost, and the frame only reports a size that changed.
const frameScript = `(function(){var d=document.documentElement,fit=d.hasAttribute("data-fit"),last="",nat=-1,B=null,O="",S="light",` +
	`LEAF=/^(SVG|IMG|CANVAS|VIDEO|IFRAME|SELECT|TEXTAREA)$/,SOLID=/^(SVG|IMG|CANVAS|VIDEO|IFRAME|SELECT|TEXTAREA|INPUT|BUTTON|HR|PROGRESS|METER)$/;` +
	`try{if(matchMedia("(prefers-color-scheme: dark)").matches){S="dark";d.classList.add("dark")}}catch(e){}` +
	`function col(s){var m=/^rgba?\(([^)]*)\)$/.exec(s||""),v;if(!m)return null;v=m[1].split(/[\s,\/]+/).map(Number);` +
	`return v.length>2&&v.length<5&&v.every(isFinite)?[v[0],v[1],v[2],v.length>3?v[3]:1]:null}` +
	`function over(a,b){var k=a[3];return[a[0]*k+b[0]*(1-k),a[1]*k+b[1]*(1-k),a[2]*k+b[2]*(1-k),1]}` +
	`function lum(c){var s=0,i,v;for(i=0;i<3;i++){v=c[i]/255;s+=[.2126,.7152,.0722][i]*(v<=.04045?v/12.92:Math.pow((v+.055)/1.055,2.4))}return s}` +
	`function under(e,c){var L=[],s,g;for(;e&&e.nodeType===1;e=e.parentNode){s=getComputedStyle(e);` +
	`if(s.backgroundImage!=="none"||!(g=col(s.backgroundColor)))return null;if(g[3]>0){L.push(g);if(g[3]>=1)break}}` +
	`while(L.length)c=over(L.pop(),c);return c}` +
	`function unread(c){var n=0,g=document.createRange();(function walk(e){for(var k=e.firstChild,s,t,u,a,b,r;k;k=k.nextSibling){` +
	`if(k.nodeType===3){if(!/\S/.test(k.data))continue;g.selectNodeContents(k);r=g.getBoundingClientRect();s=getComputedStyle(e);` +
	`if(!r.width||!r.height||s.visibility==="hidden"||!(t=col(s.color))||!(u=under(e,c)))continue;t=over(t,u);a=lum(t);b=lum(u);` +
	`if((Math.max(a,b)+.05)/(Math.min(a,b)+.05)<3)n+=k.data.trim().length}` +
	`else if(k.nodeType===1&&!LEAF.test(k.tagName.toUpperCase())&&getComputedStyle(k).display!=="none")walk(k)}})(document.body);return n}` +
	`function sheet(on){if(on)d.setAttribute("data-af-paper","");else d.removeAttribute("data-af-paper");` +
	`d.classList.toggle("dark",!on&&S==="dark");d.style.colorScheme=on?"light":O;if(window.askForm)askForm.theme=on?"light":S}` +
	`function own(){var p,c;if(parent===window)return/dark/.test(getComputedStyle(d).colorScheme)?[18,18,18,1]:[255,255,255,1];` +
	`p=document.createElement("i");p.style.color="hsl(var(--background))";d.appendChild(p);c=col(getComputedStyle(p).color);d.removeChild(p);return c}` +
	`function legible(){var c,a;if(!document.body)return;sheet(0);c=B||own();if(!c||lum(c)>=.2||!(a=unread(c)))return;sheet(1);if(unread(c)>=a)sheet(0)}` +
	`addEventListener("message",function(e){var m=e.data,c;if(e.source!==parent||parent===window||!m||m.type!=="ask-form:backdrop"||!(c=col(m.color)))return;` +
	`if(!B)last="";B=[c[0],c[1],c[2],1];S=lum(B)<.2?"dark":"light";O=m.scheme==="dark"||m.scheme==="light"?m.scheme:"";` +
	`d.setAttribute("data-af-scheme",S);legible();place()});` +
	`function send(m){var k=JSON.stringify(m);if(k!==last){last=k;m.type="ask-form:frame";parent.postMessage(m,"*")}}` +
	`function high(b){return Math.ceil(Math.max(b.scrollHeight,b.getBoundingClientRect().height))}` +
	`function paper(b){var w=b.style.width,h=0;[700,724].forEach(function(x){b.style.width=x+"px";h=Math.max(h,high(b))});b.style.width=w;return h}` +
	`function painted(s){return s.backgroundImage!=="none"||s.boxShadow!=="none"||!/^(transparent|rgba\(0, 0, 0, 0\))$/.test(s.backgroundColor)||` +
	`parseFloat(s.borderLeftWidth)+parseFloat(s.borderRightWidth)+parseFloat(s.borderTopWidth)+parseFloat(s.borderBottomWidth)>0}` +
	`function ink(b){var o=b.getBoundingClientRect().left,L=1/0,R=-1/0,S=0,M=0,g=document.createRange();` +
	`function add(r,c){var l=c?Math.max(r.left,c[0]):r.left,x=c?Math.min(r.right,c[1]):r.right;if(x>l&&r.height>0){L=Math.min(L,l);R=Math.max(R,x);S+=x-l;M=Math.max(M,x-l)}}` +
	`(function walk(n,c){for(var k=n.firstChild;k;k=k.nextSibling){` +
	`if(k.nodeType===3){if(/\S/.test(k.data)){g.selectNodeContents(k);add(g.getBoundingClientRect(),c)}}` +
	`else if(k.nodeType===1){var s=getComputedStyle(k),t=k.tagName.toUpperCase(),r=k.getBoundingClientRect();if(s.display==="none")continue;` +
	`if(s.visibility!=="hidden"&&(SOLID.test(t)||painted(s)))add(r,c);` +
	`if(!LEAF.test(t))walk(k,s.overflowX==="visible"?c:[Math.max(r.left,c?c[0]:-1/0),Math.min(r.right,c?c[1]:1/0)])}}})(b,null);` +
	`return R>L?[L-o,R-o,S,M]:null}` +
	`function natural(b){var w=1600,h,k,e,f,lo,m,n,i;b.style.width="2000px";e=ink(b);k=e?e[2]:0;b.style.width=w+"px";e=ink(b);if(!e)return 0;` +
	`if(e[1]>w+1)return Math.ceil(e[1]+Math.max(0,e[0]));if(e[1]-e[0]>=w-2||k>e[2]+.5)return 0;h=high(b);k=e[2];lo=Math.max(0,Math.floor(e[3])-2);` +
	`function fits(x){b.style.width=x+"px";if(high(b)>h+1)return null;var g=ink(b);return g&&g[0]>=-1&&g[1]<=x+1&&g[2]>=k-.5?g:null}` +
	`for(i=0;i<4;i++){n=Math.ceil(e[1]-e[0]);if(n>=w-1)break;f=fits(n);if(f){w=n;e=f;continue}` +
	`lo=Math.max(lo,n);n=Math.ceil(e[1]+Math.max(0,e[0]));if(n<w&&fits(n))w=n;break}` +
	`while(w-lo>1){m=(lo+w)>>1;if(fits(m))w=m;else lo=m}` +
	`if(w>=1600)return 0;b.style.width=w+"px";e=ink(b)||e;return Math.max(w,Math.ceil(e[1]+Math.max(0,e[0])))}` +
	`function place(){var b=document.body,vw=innerWidth,vh=innerHeight,W,H,h0,z,i;if(!b)return;` +
	`if(!fit){send({h:high(b),p:paper(b)});return}` +
	`d.style.overflow="hidden";b.style.boxSizing="border-box";b.style.transform="";if(nat<0)nat=natural(b);` +
	`W=nat||vw;b.style.width=W+"px";H=h0=high(b);` +
	`if(!nat)for(i=0;i<2&&vh>0&&H>vh+1;i++){W=Math.ceil(vw*H/vh);b.style.width=W+"px";H=high(b)}` +
	`z=W>0&&H>0?Math.min(vw/W,vh>0?vh/H:1/0):1;` +
	`b.style.transformOrigin="0 0";b.style.transform="translate("+(vw-W*z)/2+"px,"+Math.max(0,(vh-H*z)/2)+"px) scale("+z+")";` +
	`send(nat?{w:nat,h:H}:{w:vw,h:h0,fluid:1})}` +
	`function again(){nat=-1;place()}` +
	`if(!fit&&window.ResizeObserver)new ResizeObserver(function(){place()}).observe(d);` +
	`addEventListener("load",function(){again();legible()});addEventListener("resize",function(){place()});` +
	`if(fit&&document.fonts&&document.fonts.ready)document.fonts.ready.then(again);` +
	`if(!fit)setInterval(place,1500);place();legible()})();`

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
		`}html{print-color-adjust:exact;-webkit-print-color-adjust:exact}body{display:flow-root;` +
		`font:13px/1.5 system-ui,-apple-system,"Segoe UI",Roboto,sans-serif}` +
		`img,video,svg,canvas{max-width:100%}a{color:hsl(var(--primary))}`
	headRe = regexp.MustCompile(`(?i)<head[^>]*>`)
	htmlRe = regexp.MustCompile(`(?i)<html[^>]*>`)
	bodyRe = regexp.MustCompile(`(?i)</body\s*>`)
)

// frameSchemeCSS is what the frame script switches between (see Legibility
// above): the theme's and the brand's tokens for the side of the page the
// frame sits on, whatever the OS prefers (data-af-scheme), and the light
// "paper" a mockup written for a light page is drawn on when the page is
// dark (data-af-paper). An attribute selector weighs what :root does: these
// beat the theme's and the brand's rules by coming after them, and a full
// document's own :root rules, further down, still beat these. On paper
// (print) a frame is light whatever the screen was.
func frameSchemeCSS(brandCSS string) string {
	light, dark := brand.SchemeTokens(themeCSS + brandCSS)
	return `[data-af-scheme="light"]{` + light + `}[data-af-scheme="dark"]{` + dark + `}` +
		`[data-af-paper]{` + light + `;color-scheme:light;background:hsl(var(--background))}` +
		`@media print{:root{` + light + `;color-scheme:light!important}}`
}

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
		`<style>` + themeCSS + frameBaseCSS + brandCSS + frameSchemeCSS(brandCSS) + `</style>` + sdk
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
