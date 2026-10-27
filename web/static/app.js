// ask-form answer page — progressive enhancement. The page works without it;
// this adds group paging, "Other…" niceties, a local draft, a confirm step, a
// fullscreen viewer for pictures and mockups, and auto-sized html blocks.
(function () {
  "use strict";
  var root = document.documentElement;
  root.classList.add("js");

  function $all(sel, el) { return Array.prototype.slice.call((el || document).querySelectorAll(sel)); }
  function make(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text != null) e.textContent = text;
    return e;
  }
  var store = {
    get: function (k) { try { return JSON.parse(localStorage.getItem(k) || "null"); } catch (e) { return null; } },
    set: function (k, v) { try { localStorage.setItem(k, JSON.stringify(v)); } catch (e) { /* private mode */ } },
    del: function (k) { try { localStorage.removeItem(k); } catch (e) { /* ignore */ } }
  };

  // A closed page: the answers went through, the draft can go.
  var closed = document.querySelector("[data-closed]");
  if (closed) store.del("ask-form:draft:" + closed.getAttribute("data-closed"));

  // html blocks report their height (frame documents post it; only the
  // ones without a fixed height follow it).
  window.addEventListener("message", function (e) {
    var d = e.data;
    if (!d || d.type !== "ask-form:frame" || typeof d.h !== "number") return;
    $all("iframe.frame.autosize").forEach(function (f) {
      if (f.contentWindow === e.source) f.style.height = Math.max(40, Math.min(4000, Math.ceil(d.h) + 2)) + "px";
    });
  });

  var form = document.getElementById("form");
  if (!form) { setupZoom(); return; }
  var id = form.getAttribute("data-form");
  var draftKey = "ask-form:draft:" + id;
  var hadErrors = form.hasAttribute("data-errors");
  form.noValidate = true; // validation happens below, page-aware

  var fields = $all(".field", form).filter(function (f) {
    var t = f.getAttribute("data-type");
    return t !== "markdown" && t !== "html";
  });
  var touched = {};

  // ── values ────────────────────────────────────────────────────────────────
  function inputs(f) { return $all("input, select, textarea", f); }
  function otherInput(f) { return f.querySelector("[data-other-for]"); }
  function otherPicked(f) {
    return inputs(f).some(function (i) {
      return (i.value === "__other__" && i.checked) || (i.tagName === "SELECT" && i.value === "__other__");
    });
  }
  function isEmpty(f) {
    var t = f.getAttribute("data-type");
    var els = inputs(f);
    if (t === "checkbox" || t === "slider") return false;
    if (t === "radio" || t === "multiselect" || (t === "select" && f.classList.contains("visual-field"))) {
      var picked = els.filter(function (i) { return (i.type === "radio" || i.type === "checkbox") && i.checked && i.value !== "__other__"; });
      if (picked.length) return false;
      var o = otherInput(f);
      return !(otherPicked(f) && o && o.value.trim());
    }
    if (t === "select") {
      var s = f.querySelector("select");
      if (!s || !s.value) return true;
      if (s.value !== "__other__") return false;
      var oi = otherInput(f);
      return !(oi && oi.value.trim());
    }
    var main = els[0];
    return !main || !main.value.trim();
  }
  function unanswered(f) {
    if (f.closest(".catchall")) return false;
    var t = f.getAttribute("data-type");
    if (t === "checkbox" || t === "slider") return !touched[f.getAttribute("data-key")];
    return isEmpty(f);
  }
  function missing(f) { return f.hasAttribute("data-required") && f.getAttribute("data-type") !== "checkbox" && isEmpty(f); }

  // ── draft (never a secret) ──────────────────────────────────────────────
  function saveDraft() {
    var v = {};
    $all("input, select, textarea", form).forEach(function (i) {
      if (!/^[fo]\./.test(i.name) || i.type === "password") return;
      if (i.type === "radio" || i.type === "checkbox") {
        if (i.checked) (v[i.name] = v[i.name] || []).push(i.value);
        else v[i.name] = v[i.name] || [];
      } else v[i.name] = i.value;
    });
    store.set(draftKey, { v: v, t: touched, seen: seen, at: Date.now() });
  }
  var seen = {};
  var draft = store.get(draftKey);
  if (draft && !hadErrors) {
    $all("input, select, textarea", form).forEach(function (i) {
      if (!(i.name in draft.v) || i.type === "password") return;
      var val = draft.v[i.name];
      if (i.type === "radio" || i.type === "checkbox") i.checked = Array.isArray(val) && val.indexOf(i.value) >= 0;
      else if (typeof val === "string") i.value = val;
    });
    touched = draft.t || {};
  }
  if (draft && draft.seen) seen = draft.seen;

  // ── per-field behaviour ─────────────────────────────────────────────────
  fields.forEach(function (f) {
    var key = f.getAttribute("data-key");
    var t = f.getAttribute("data-type");
    var o = otherInput(f);
    if (o) {
      // Typing next to "Other…" picks it.
      o.addEventListener("input", function () {
        if (!o.value.trim()) return;
        inputs(f).forEach(function (i) {
          if (i.value === "__other__" && (i.type === "radio" || i.type === "checkbox")) i.checked = true;
          if (i.tagName === "SELECT") i.value = "__other__";
        });
      });
      inputs(f).forEach(function (i) {
        if (i.value === "__other__" || i.tagName === "SELECT") {
          i.addEventListener("change", function () { if (otherPicked(f) && !o.value) o.focus(); });
        }
      });
    }
    if (t === "slider") {
      var r = f.querySelector("input[type=range]"), out = f.querySelector("output");
      var sync = function () { if (out) out.textContent = r.value; };
      r.addEventListener("input", sync);
      sync();
    }
    if (t === "secret") {
      var p = f.querySelector("input[type=password]");
      var b = make("button", "reveal", "Show");
      b.type = "button";
      b.addEventListener("click", function () {
        var show = p.type === "password";
        p.type = show ? "text" : "password";
        b.textContent = show ? "Hide" : "Show";
      });
      p.parentNode.appendChild(b);
    }
    $all("textarea", f).forEach(function (ta) {
      var grow = function () { ta.style.height = "auto"; ta.style.height = Math.min(ta.scrollHeight + 2, 600) + "px"; };
      ta.addEventListener("input", grow);
      grow();
    });
    f.addEventListener("input", function () { touched[key] = true; clearErr(f); refresh(); saveDraft(); });
    f.addEventListener("change", function () { touched[key] = true; clearErr(f); refresh(); saveDraft(); });
  });

  function clearErr(f) {
    f.classList.remove("has-error");
    $all(".err.js-err", f).forEach(function (e) { e.remove(); });
  }
  function flag(f, msg) {
    clearErr(f);
    f.classList.add("has-error");
    f.appendChild(make("p", "err js-err", msg));
  }

  // ── pages ─────────────────────────────────────────────────────────────────
  var groups = $all(".group", form);
  var current = 0, pager = null, nav = null;
  function groupOf(el) { return groups.indexOf(el.closest(".group")); }
  function groupPending(g) {
    return $all(".field", groups[g]).some(function (f) { return fields.indexOf(f) >= 0 && unanswered(f); });
  }
  function show(g, focus) {
    current = Math.max(0, Math.min(groups.length - 1, g));
    seen[current] = true;
    groups.forEach(function (el, i) { el.classList.toggle("current", i === current); });
    refresh();
    if (focus) {
      var first = $all(".field", groups[current]).filter(unanswered)[0];
      var el = first && first.querySelector("input:not([type=hidden]), select, textarea");
      window.scrollTo({ top: form.getBoundingClientRect().top + window.scrollY - 8 });
      if (el && focus === "field") el.focus({ preventScroll: true });
    }
  }
  if (groups.length > 1) {
    form.classList.add("paged");
    pager = make("div", "pager");
    var row = make("div", "pager-row");
    var count = make("span", "pager-count");
    var segs = make("div", "pager-segs");
    groups.forEach(function (el, i) {
      var s = make("button", "pager-seg");
      s.type = "button";
      s.setAttribute("aria-label", "Page " + (i + 1) + (el.getAttribute("data-title") ? ": " + el.getAttribute("data-title") : ""));
      s.addEventListener("click", function () { show(i, true); });
      segs.appendChild(s);
    });
    row.appendChild(count);
    row.appendChild(segs);
    pager.appendChild(row);
    pager.appendChild(make("div", "pager-title"));
    form.insertBefore(pager, groups[0]);

    nav = make("div", "nav");
    var back = make("button", "btn ghost", "← Back");
    var next = make("button", "btn", "Next →");
    back.type = next.type = "button";
    back.addEventListener("click", function () { show(current - 1, true); });
    next.addEventListener("click", function () { show(current + 1, "field"); });
    nav.appendChild(back);
    nav.appendChild(next);
    groups[groups.length - 1].after(nav);
    nav.back = back;
    nav.next = next;
  }
  function refresh() {
    fields.forEach(function (f) {
      var lab = f.querySelector(".label");
      var dot = lab && lab.querySelector(".pending-dot");
      var want = !!pager && unanswered(f);
      if (want && !dot && lab) lab.appendChild(make("span", "pending-dot"));
      if (!want && dot) dot.remove();
    });
    if (!pager) return;
    pager.querySelector(".pager-count").textContent = (current + 1) + "/" + groups.length;
    $all(".pager-seg", pager).forEach(function (s, i) {
      s.className = "pager-seg" + (i === current ? " current" : i < current ? " done" : "") +
        (i !== current && groupPending(i) ? " pending" : "");
    });
    pager.querySelector(".pager-title").textContent = groups[current].getAttribute("data-title") || "";
    nav.back.disabled = current === 0;
    nav.next.disabled = current === groups.length - 1;
  }
  if (pager) {
    var start = 0;
    if (hadErrors) {
      var bad = form.querySelector(".has-error");
      start = bad ? Math.max(0, groupOf(bad)) : 0;
    } else {
      for (var i = 0; i < groups.length; i++) {
        if (!seen[i] || groupPending(i)) { start = i; break; }
      }
    }
    show(start, false);
    // The error summary's links land on the field's page.
    $all(".errors a").forEach(function (a) {
      a.addEventListener("click", function (e) {
        var target = document.getElementById(a.getAttribute("href").slice(1));
        if (!target) return;
        e.preventDefault();
        var g = groupOf(target);
        if (g >= 0) show(g, false);
        target.scrollIntoView({ block: "center" });
        var el = target.querySelector("input:not([type=hidden]), select, textarea");
        if (el) el.focus({ preventScroll: true });
      });
    });
  } else refresh();

  // ── submit: required check, then a confirm step ─────────────────────────
  var confirmed = false;
  form.addEventListener("submit", function (e) {
    var by = e.submitter;
    if (by && by.value === "decline") { lock(); store.del(draftKey); return; }
    if (confirmed) { lock(); return; }
    e.preventDefault();
    var miss = fields.filter(missing);
    if (miss.length) {
      miss.forEach(function (f) { flag(f, "This question is required."); });
      var g = groupOf(miss[0]);
      if (pager && g >= 0) show(g, false);
      miss[0].scrollIntoView({ block: "center" });
      var el = miss[0].querySelector("input:not([type=hidden]), select, textarea");
      if (el) el.focus({ preventScroll: true });
      return;
    }
    openConfirm(by);
  });
  function lock() {
    setTimeout(function () { $all("button[type=submit]", form).forEach(function (b) { b.disabled = true; }); }, 0);
  }

  function labelOf(f) {
    var l = f.querySelector(".label-text") || f.querySelector(".check span");
    return l ? l.textContent.trim() : f.getAttribute("data-key");
  }
  function choiceText(i) {
    var lab = i.closest("label");
    if (!lab) return i.value;
    var c = lab.querySelector(".caption span, .choice-text span, .text-card span");
    return c ? c.textContent.trim() : i.value;
  }
  function answerOf(f) {
    var t = f.getAttribute("data-type");
    var o = otherInput(f);
    var oText = o && otherPicked(f) ? o.value.trim() : "";
    if (t === "checkbox") return f.querySelector("input").checked ? "Yes" : "No";
    if (t === "secret") {
      var p = f.querySelector("input");
      return p.value.trim() ? "🔒 " + p.value.trim().length + " characters — not shown" : "";
    }
    if (t === "select" && !f.classList.contains("visual-field")) {
      var s = f.querySelector("select");
      if (!s.value) return "";
      return s.value === "__other__" ? oText : s.options[s.selectedIndex].textContent.trim();
    }
    if (t === "radio" || t === "multiselect" || t === "select") {
      var parts = inputs(f).filter(function (i) {
        return (i.type === "radio" || i.type === "checkbox") && i.checked && i.value !== "__other__";
      }).map(choiceText);
      if (oText) parts.push(oText);
      return parts.join(", ");
    }
    var main = f.querySelector("input, textarea");
    return main ? main.value.trim() : "";
  }
  function openConfirm(by) {
    var wrap = make("div", "confirm");
    wrap.setAttribute("role", "dialog");
    wrap.setAttribute("aria-modal", "true");
    var box = make("div", "confirm-box");
    box.appendChild(make("h2", null, "Send these answers?"));
    box.appendChild(make("p", "help", "The form closes once sent — you can't change them afterwards."));
    var dl = make("dl", "recap");
    var n = 0;
    fields.forEach(function (f) {
      var a = answerOf(f);
      if (!a) return;
      dl.appendChild(make("dt", null, labelOf(f)));
      dl.appendChild(make("dd", null, a));
      n++;
    });
    if (!n) dl.appendChild(make("dd", null, "No answers — the form will be sent empty."));
    box.appendChild(dl);
    var acts = make("div", "actions");
    var back = make("button", "btn ghost", "Back");
    var send = make("button", "btn primary", "Confirm & send");
    back.type = send.type = "button";
    back.addEventListener("click", function () { wrap.remove(); if (by) by.focus(); });
    send.addEventListener("click", function () {
      confirmed = true;
      send.disabled = true;
      if (form.requestSubmit && by) form.requestSubmit(by);
      else {
        var h = make("input");
        h.type = "hidden"; h.name = "action"; h.value = "submit";
        form.appendChild(h);
        form.submit();
      }
    });
    wrap.addEventListener("keydown", function (e) { if (e.key === "Escape") back.click(); });
    acts.appendChild(back);
    acts.appendChild(send);
    box.appendChild(acts);
    wrap.appendChild(box);
    document.body.appendChild(wrap);
    send.focus();
  }

  setupZoom();

  // ── fullscreen pictures and mockups ─────────────────────────────────────
  function setupZoom() {
    $all("a.zoom, a.frame-open").forEach(function (a) {
      a.addEventListener("click", function (e) {
        e.preventDefault();
        e.stopPropagation();
        var field = a.closest(".field");
        if (a.hasAttribute("data-zoom-frame") || a.classList.contains("frame-open")) {
          openOverlay([{ frame: a.getAttribute("href") }], 0);
          return;
        }
        var imgs = $all("img[data-zoom]", field || document);
        var list = imgs.map(function (i) { return { img: i.getAttribute("data-zoom"), label: i.getAttribute("data-zoom-label") || "" }; });
        var at = list.map(function (x) { return x.img; }).indexOf(a.getAttribute("href"));
        openOverlay(list, Math.max(0, at));
      });
    });
  }
  function openOverlay(list, at) {
    var ov = make("div", "overlay");
    ov.setAttribute("role", "dialog");
    ov.setAttribute("aria-modal", "true");
    var close = make("button", "overlay-close", "✕");
    close.type = "button";
    close.setAttribute("aria-label", "Close");
    var label = make("div", "overlay-label");
    var holder = make("div");
    holder.style.display = "contents";
    function render() {
      holder.textContent = "";
      var it = list[at];
      if (it.frame) {
        var fr = make("iframe");
        fr.src = it.frame;
        fr.setAttribute("sandbox", "allow-scripts");
        fr.setAttribute("referrerpolicy", "no-referrer");
        holder.appendChild(fr);
      } else {
        var im = make("img");
        im.src = it.img;
        im.alt = it.label;
        holder.appendChild(im);
      }
      label.textContent = list.length > 1 ? (at + 1) + "/" + list.length + (it.label ? " · " + it.label : "") : (it.label || "");
    }
    function done() { ov.remove(); document.removeEventListener("keydown", key); }
    function key(e) {
      if (e.key === "Escape") done();
      if (e.key === "ArrowLeft" && list.length > 1) { at = (at - 1 + list.length) % list.length; render(); }
      if (e.key === "ArrowRight" && list.length > 1) { at = (at + 1) % list.length; render(); }
    }
    ov.appendChild(holder);
    ov.appendChild(close);
    ov.appendChild(label);
    if (list.length > 1) {
      var prev = make("button", "overlay-prev", "‹"), nxt = make("button", "overlay-next", "›");
      prev.type = nxt.type = "button";
      prev.addEventListener("click", function (e) { e.stopPropagation(); at = (at - 1 + list.length) % list.length; render(); });
      nxt.addEventListener("click", function (e) { e.stopPropagation(); at = (at + 1) % list.length; render(); });
      ov.appendChild(prev);
      ov.appendChild(nxt);
    }
    close.addEventListener("click", done);
    ov.addEventListener("click", function (e) { if (e.target === ov) done(); });
    document.addEventListener("keydown", key);
    render();
    document.body.appendChild(ov);
    close.focus();
  }
})();
