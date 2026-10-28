package server

import "github.com/GabrielVidal1/ask-form/internal/form"

// A widget field's frame is the one document that runs the agent's own
// script. It is still the same sandbox as an html block's — opaque origin
// (`sandbox allow-scripts` and nothing else: no same-origin, forms, popups or
// top navigation), `default-src 'none'` so no fetch, XHR, socket, worker or
// remote image, and the answer page's `frame-src 'self'` keeps the frame from
// navigating anywhere else. What the script can do is draw, listen to pointer
// events and call askForm.setValue: the page (widget.js) takes values only
// from the frame it put there.

// widgetCSP is a widget document's policy: frameCSP with inline script
// allowed. (A hash in script-src would switch 'unsafe-inline' off, so none.)
func widgetCSP() string {
	return "default-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src data:; media-src data:; " +
		"script-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'; " +
		"sandbox allow-scripts"
}

// widgetSDK is what the agent's script finds as window.askForm, in the head
// before its own markup:
//
//	askForm.setValue(v)  hand the page the answer (any JSON value; null clears it)
//	askForm.value        the answer already given (draft, or after a failed send); null if none
//	askForm.onInit(cb)   cb(value) once that is known (at once if it already is)
//	askForm.setHeight(h) ask for the frame to be at least h px tall
//	askForm.theme        "light" or "dark"
//
// The page answers the frame's `ask-form:ready` with `ask-form:init`.
const widgetSDK = `<script>(function(){var cbs=[],ready=false,a=window.askForm={value:null,theme:"light",` +
	`setValue:function(v){var j=null;try{j=v===undefined?null:JSON.parse(JSON.stringify(v))}catch(e){}` +
	`parent.postMessage({type:"ask-form:value",value:j},"*")},` +
	`setHeight:function(h){h=Math.max(0,Math.min(4000,+h||0));(document.body||document.documentElement).style.minHeight=h+"px"},` +
	`onInit:function(cb){if(typeof cb!=="function")return;if(ready)cb(a.value);else cbs.push(cb)}};` +
	`try{if(matchMedia("(prefers-color-scheme: dark)").matches)a.theme="dark"}catch(e){}` +
	`addEventListener("message",function(e){var m=e.data;if(e.source!==parent||!m||m.type!=="ask-form:init")return;` +
	`a.value=m.value===undefined?null:m.value;if(ready)return;ready=true;var l=cbs;cbs=[];` +
	`l.forEach(function(cb){try{cb(a.value)}catch(x){}})});` +
	`function hi(){parent.postMessage({type:"ask-form:ready"},"*")}hi();addEventListener("load",hi)})();</script>`

// isWidgetFrame reports whether /frame/<key> (no option index) is a widget's.
func isWidgetFrame(fields []form.Field, key, n string) bool {
	if n != "" {
		return false
	}
	for _, f := range fields {
		if f.Key == key && f.Type == form.Widget {
			return true
		}
	}
	return false
}

// frameFor picks the SDK to inject and the CSP to send with the frame
// document of key / option n: an html block or a mockup stays script-free.
func frameFor(fields []form.Field, key, n string) (sdk, csp string) {
	if isWidgetFrame(fields, key, n) {
		return widgetSDK, widgetCSP()
	}
	return "", frameCSP()
}
