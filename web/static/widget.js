// ask-form widget fields. A widget is an iframe running the agent's own HTML
// and script in a sandbox (opaque origin, no network). It talks to this page
// with postMessage; this file is the page's end of that:
//   ask-form:value  the widget's answer → the field's hidden input, so the
//                   draft, the confirm step and the POST work as for any field
//   ask-form:ready  the widget is up → we send back what is already saved
//                   (a draft, or the answer of a send that failed)
// Only the frame this page put in a field is listened to.
(function () {
  "use strict";
  var page = window.askFormPage; // app.js, when there is a form to answer
  if (!page) return;
  var MAX = 16 * 1024; // form.MaxWidgetValue

  var entries = [].slice.call(document.querySelectorAll('.field[data-type="widget"]')).map(function (el) {
    var input = el.querySelector("input[data-widget]");
    input.disabled = false; // off in the markup: without JS the <noscript> textarea answers
    return { el: el, frame: el.querySelector("iframe"), input: input, schema: input.getAttribute("data-schema") || "", next: null, queued: false };
  });
  if (!entries.length) return;

  function kind(v) { return Array.isArray(v) ? "array" : typeof v; }

  function init(e) {
    var w = e.frame.contentWindow;
    if (!w) return;
    var v = null;
    try { if (e.input.value) v = JSON.parse(e.input.value); } catch (x) { v = null; }
    w.postMessage({ type: "ask-form:init", value: v }, "*");
  }

  // A widget may report on every pointer move: one write per frame is enough.
  function apply(e) {
    e.queued = false;
    var v = e.next, s = "";
    if (v !== undefined && v !== null) {
      try { s = JSON.stringify(v) || ""; } catch (x) { s = ""; }
      if (s === "null") s = "";
    }
    if (s && new TextEncoder().encode(s).length > MAX) {
      page.flag(e.el, page.tr("The widget's answer is too large to send."));
      return;
    }
    if (s && e.schema && kind(v) !== e.schema) {
      page.flag(e.el, page.tr("The widget gave a {kind}; this question wants a {schema}.", { kind: kind(v), schema: e.schema }));
      return;
    }
    e.input.value = s;
    page.touch(e.el);
  }

  window.addEventListener("message", function (ev) {
    var d = ev.data;
    if (!d || typeof d.type !== "string") return;
    var e = entries.filter(function (x) { return x.frame.contentWindow === ev.source; })[0];
    if (!e) return;
    if (d.type === "ask-form:ready") { init(e); return; }
    if (d.type !== "ask-form:value") return;
    e.next = d.value;
    if (!e.queued) {
      e.queued = true;
      window.requestAnimationFrame(function () { apply(e); });
    }
  });

  // The frame may have come up before this script ran, or its "ready" got
  // lost: tell it again once it has loaded.
  entries.forEach(function (e) {
    e.frame.addEventListener("load", function () { init(e); });
    init(e);
  });
})();
