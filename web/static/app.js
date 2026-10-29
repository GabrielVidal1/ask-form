// ask-form answer page — progressive enhancement. The page works without it;
// this adds group paging (on a phone; a desktop shows every group and tracks
// them in the section list), "Other…" niceties, a local draft, the server's
// checks then a confirm step, a fullscreen viewer for pictures and mockups,
// and auto-sized html blocks.
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

  // Frame documents report their size. An html block without a fixed height
  // follows its height; a mockup reports the size it is drawn at, which a
  // card that fits its content takes as its ratio and the fullscreen view
  // as its shape (the frame then scales the mockup to fill either).
  window.addEventListener("message", function (e) {
    var d = e.data;
    if (!d || d.type !== "ask-form:frame" || typeof d.h !== "number") return;
    $all("iframe.frame.autosize").forEach(function (f) {
      if (f.contentWindow === e.source) f.style.height = Math.max(40, Math.min(4000, Math.ceil(d.h) + 2)) + "px";
    });
    if (!(d.w > 0 && d.h > 0)) return;
    $all(".fit-content .mock iframe, .overlay iframe").forEach(function (f) {
      if (f.contentWindow !== e.source) return;
      f.setAttribute("data-w", d.w);
      f.setAttribute("data-h", d.h);
      if (d.fluid) f.setAttribute("data-fluid", "");
      if (f.closest(".overlay")) fitOverlay(f);
      else f.parentNode.style.aspectRatio = d.w + " / " + d.h;
    });
  });

  var form = document.getElementById("form");
  if (!form) { setupZoom(); return; }
  var id = form.getAttribute("data-form");
  var draftKey = "ask-form:draft:" + id;
  var hadErrors = form.hasAttribute("data-errors");
  form.noValidate = true; // validation happens below, page-aware

  var fields = $all(".field", form).filter(function (f) {
    return !f.classList.contains("is-display");
  });
  // Every block and question in the spec's order (the page groups them, which
  // can reorder them): conditions look back along this order.
  var allFields = $all(".field", form).sort(function (a, b) { return Number(a.id.slice(1)) - Number(b.id.slice(1)); });
  var byKey = {};
  allFields.forEach(function (f) { byKey[f.getAttribute("data-key")] = f; });
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
    if (t === "list" || t === "daterange") return !els.some(function (i) { return i.value.trim() !== ""; });
    if (t === "radio" || t === "rating" || t === "multiselect" || (t === "select" && f.classList.contains("visual-field"))) {
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
    if (f.closest(".catchall") || f.hidden) return false;
    var t = f.getAttribute("data-type");
    if (t === "checkbox" || t === "slider") return !touched[f.getAttribute("data-key")];
    return isEmpty(f);
  }
  // What the server would turn down (form.Parse, same messages), so a send
  // never opens the confirm step only to come back with errors: "" = fine.
  function problem(f) {
    var t = f.getAttribute("data-type");
    if (f.hidden) return "";
    if (t === "file" && f.hasAttribute("data-uploading")) return "Wait for the upload to finish.";
    if (t === "checkbox" || t === "slider") return "";
    var main = f.querySelector("input:not([type=hidden]):not([data-other-for]), textarea");
    // A half-typed number or date reads as "": the browser knows better.
    if (main && main.validity && main.validity.badInput) return t === "date" ? "Enter a date (YYYY-MM-DD)." : "Enter a number.";
    if (isEmpty(f)) {
      if (!f.hasAttribute("data-required")) return "";
      if (otherPicked(f)) return "Type your answer next to “Other…”.";
      if (t === "widget") return "Interact with this first.";
      return t === "multiselect" ? "Pick at least one." : "This question is required.";
    }
    if (t === "daterange") {
      var dr = inputs(f).map(function (i) { return i.value; });
      if (!dr[0] || !dr[1]) return "Pick both dates.";
      return dr[1] < dr[0] ? "The end is before the start." : "";
    }
    if (t === "list") {
      var need = Number(f.querySelector("[data-list]").getAttribute("data-min") || 0);
      return need && filledRows(f) < need ? "Add at least " + need + "." : "";
    }
    var v = main ? main.value.trim() : "";
    if (t === "number") {
      var n = Number(v.replace(",", "."));
      if (!isFinite(n)) return "Enter a number.";
      var min = main.getAttribute("min"), max = main.getAttribute("max");
      if (min && n < Number(min)) return "At least " + min + ".";
      if (max && n > Number(max)) return "At most " + max + ".";
    }
    if (t === "date" && !/^\d{4}-\d{2}-\d{2}$/.test(v)) return "Enter a date (YYYY-MM-DD).";
    if (t === "email" && !validEmail(v.toLowerCase())) return "Enter an email address, like name@example.com.";
    if (t === "secret" && v.length > 8000) return "That value is too long.";
    return "";
  }
  // form.ValidEmail: one @, something before it, a dotted domain of letters,
  // digits and dashes after it, no spaces.
  function validEmail(s) {
    if (s.length > 254 || /[\s<>(),;:"\[\]\\]/.test(s)) return false;
    var at = s.indexOf("@");
    var local = s.slice(0, at), domain = s.slice(at + 1);
    if (at < 1 || local.length > 64 || domain.indexOf("@") >= 0 || domain.indexOf(".") < 0) return false;
    return domain.split(".").every(function (l) {
      return l.length > 0 && l.length <= 63 && l[0] !== "-" && l[l.length - 1] !== "-" && /^(?:[a-z0-9-]|[^\x00-\x7f])+$/.test(l);
    });
  }

  // ── lists: rows of inputs, a row more or less ─────────────────────────────
  function listBlank(row) { return $all("input, select", row).every(function (i) { return !i.value.trim(); }); }
  function filledRows(f) { return $all(".list-row", f).filter(function (r) { return !listBlank(r); }).length; }
  function clearRow(row) { $all("input, select", row).forEach(function (i) { i.value = ""; }); }
  function syncAdd(list) {
    var add = list.parentNode.querySelector(".list-add");
    if (add) add.disabled = $all(".list-row", list).length >= Number(list.getAttribute("data-max") || 50);
  }
  function bindRow(list, row) {
    $all(".row-remove", row).forEach(function (b) { b.remove(); });
    var rm = make("button", "row-remove", "✕");
    rm.type = "button";
    rm.setAttribute("aria-label", "Remove this row");
    rm.addEventListener("click", function () {
      if ($all(".list-row", list).length > 1) row.remove(); else clearRow(row);
      syncAdd(list);
      list.closest(".field").dispatchEvent(new Event("input", { bubbles: true }));
    });
    row.appendChild(rm);
  }
  function addRow(list) {
    var rows = $all(".list-row", list);
    if (rows.length >= Number(list.getAttribute("data-max") || 50)) return null;
    var row = rows[rows.length - 1].cloneNode(true);
    clearRow(row);
    list.appendChild(row);
    bindRow(list, row);
    syncAdd(list);
    return row;
  }
  function setupList(list) {
    var rows = $all(".list-row", list);
    // Without a script the page offers a few blank rows; with one, a row more
    // is a tap away, so the blank ones beyond what is needed go.
    var keep = Math.max(1, Number(list.getAttribute("data-min") || 0));
    while (rows.length > keep && listBlank(rows[rows.length - 1])) rows.pop().remove();
    $all(".list-row", list).forEach(function (r) { bindRow(list, r); });
    var add = make("button", "list-add", "＋ Add");
    add.type = "button";
    add.addEventListener("click", function () {
      var row = addRow(list);
      var first = row && row.querySelector("input, select");
      if (first) first.focus();
      list.closest(".field").dispatchEvent(new Event("input", { bubbles: true }));
    });
    list.after(add);
    syncAdd(list);
    // Enter in a cell goes to the next row (adding one) instead of sending.
    list.addEventListener("keydown", function (e) {
      if (e.key !== "Enter" || !e.target.matches("input")) return;
      e.preventDefault();
      var all = $all(".list-row", list), at = all.indexOf(e.target.closest(".list-row"));
      var next = all[at + 1] || (listBlank(all[at]) ? null : addRow(list));
      var el = next && next.querySelector("input, select");
      if (el) el.focus();
    });
  }
  $all("[data-list]", form).forEach(setupList);

  // ── draft (never a secret) ──────────────────────────────────────────────
  function saveDraft() {
    var v = {};
    $all("input, select, textarea", form).forEach(function (i) {
      if (!/^[fo]\./.test(i.name) || i.type === "password") return;
      if (i.type === "radio" || i.type === "checkbox") {
        if (i.checked) (v[i.name] = v[i.name] || []).push(i.value);
        else v[i.name] = v[i.name] || [];
      } else if (i.closest("[data-list]")) (v[i.name] = v[i.name] || []).push(i.value);
      else v[i.name] = i.value;
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
    // A list comes back with as many rows as it had.
    $all("[data-list]", form).forEach(function (list) {
      var names = $all("input, select", $all(".list-row", list)[0]).map(function (i) { return i.name; });
      var arrs = names.map(function (n) { return draft.v[n]; }).filter(Array.isArray);
      if (!arrs.length) return;
      var n = Math.max(1, Math.max.apply(null, arrs.map(function (a) { return a.length; })));
      var rows = $all(".list-row", list);
      while (rows.length < n && addRow(list)) rows = $all(".list-row", list);
      while (rows.length > n) rows.pop().remove();
      rows.forEach(function (row, ri) {
        $all("input, select", row).forEach(function (i) {
          var a = draft.v[i.name];
          i.value = Array.isArray(a) && a[ri] != null ? a[ri] : "";
        });
      });
      syncAdd(list);
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
    if (t === "daterange") {
      var dr = inputs(f);
      var span = function () { dr[1].min = dr[0].value || ""; };
      dr[0].addEventListener("change", span);
      span();
    }
    f.addEventListener("input", function () { touched[key] = true; clearErr(f); applyConditions(); refresh(); saveDraft(); });
    f.addEventListener("change", function () { touched[key] = true; clearErr(f); applyConditions(); refresh(); saveDraft(); });
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

  // ── conditions: showIf hides a field (and what tests it), requiredIf asks ──
  // The page mirrors form.Cond: a test on a hidden question fails.
  var hiddenKey = {}, applying = false;
  function rawValue(f) {
    var t = f.getAttribute("data-type"), els = inputs(f), key = f.getAttribute("data-key");
    if (t === "checkbox") return !!(els[0] && els[0].checked);
    if (t === "rating") {
      var c = els.filter(function (i) { return i.checked; })[0];
      return c ? Number(c.value) : null;
    }
    if (t === "radio" || t === "select" || t === "multiselect") {
      var o = otherInput(f), other = o && otherPicked(f) ? o.value.trim() : "";
      if (t === "select" && !f.classList.contains("visual-field")) {
        var s = f.querySelector("select");
        return s.value === "__other__" ? (other || null) : (s.value || null);
      }
      var picked = els.filter(function (i) {
        return (i.type === "radio" || i.type === "checkbox") && i.checked && i.value !== "__other__";
      }).map(function (i) { return i.value; });
      if (other) picked.push(other);
      return t === "multiselect" ? picked : (picked.length ? picked[0] : null);
    }
    if (t === "number" || t === "slider") {
      var nv = els[0].value.trim().replace(",", ".");
      return nv !== "" && isFinite(Number(nv)) ? Number(nv) : null;
    }
    if (t === "daterange") {
      var d = els.map(function (i) { return i.value; });
      return d[0] && d[1] ? { start: d[0], end: d[1] } : null;
    }
    if (t === "widget" || t === "file") {
      try {
        var j = els[0] && els[0].value ? JSON.parse(els[0].value) : null;
        return Array.isArray(j) && !j.length ? null : j;
      } catch (e) { return null; }
    }
    if (t === "list") {
      var prefix = "f." + key + ".";
      var rows = $all(".list-row", f).map(function (row) {
        var cells = $all("input, select", row), obj = {}, texts = [];
        cells.forEach(function (i) {
          var v = i.value.trim();
          if (!v) return;
          texts.push(v);
          obj[i.name.indexOf(prefix) === 0 ? i.name.slice(prefix.length) : ""] = i.type === "number" ? Number(v.replace(",", ".")) : v;
        });
        return { texts: texts, obj: obj };
      }).filter(function (r) { return r.texts.length; });
      var table = $all("input, select", f).some(function (i) { return i.name.indexOf(prefix) === 0; });
      return rows.map(function (r) { return table ? r.obj : r.texts[0]; });
    }
    var v = els[0] ? els[0].value.trim() : "";
    if (v !== "" && t === "email") v = v.toLowerCase();
    return v === "" ? null : v;
  }
  function isAnswered(v) {
    if (v === null || v === undefined) return false;
    if (typeof v === "boolean") return v;
    if (typeof v === "string") return v !== "";
    if (Array.isArray(v)) return v.length > 0;
    return true;
  }
  function sameValue(a, b) { return a !== null && a !== undefined && typeof a === typeof b && a === b; }
  function evalCond(c) {
    if (c.all) return c.all.every(evalCond);
    if (c.any) return c.any.some(evalCond);
    if (c.not) return !evalCond(c.not);
    if (!byKey[c.key] || hiddenKey[c.key]) return false;
    var v = rawValue(byKey[c.key]);
    switch (c.op) {
      case "answered": return isAnswered(v) === c.value;
      case "equals": return sameValue(v, c.value);
      case "notEquals": return !sameValue(v, c.value);
      case "in": return c.value.some(function (x) { return sameValue(v, x); });
      case "includes":
        if (typeof v === "string") return c.value !== "" && v.toLowerCase().indexOf(String(c.value).toLowerCase()) >= 0;
        return Array.isArray(v) && v.some(function (x) { return String(x) === String(c.value); });
      case "gt": return typeof v === "number" && v > c.value;
      case "gte": return typeof v === "number" && v >= c.value;
      case "lt": return typeof v === "number" && v < c.value;
      case "lte": return typeof v === "number" && v <= c.value;
    }
    return false;
  }
  function condOf(f, attr) {
    var cache = "_" + attr;
    if (!(cache in f)) {
      try { f[cache] = f.hasAttribute(attr) ? JSON.parse(f.getAttribute(attr)) : null; } catch (e) { f[cache] = null; }
    }
    return f[cache];
  }
  function vis(g) { return !groups[g].classList.contains("group-empty"); }
  function applyConditions() {
    if (applying) return;
    applying = true;
    allFields.forEach(function (f) {
      var key = f.getAttribute("data-key"), show = condOf(f, "data-show-if");
      var hide = !!show && !evalCond(show);
      hiddenKey[key] = hide;
      if (f.hidden !== hide) {
        f.hidden = hide;
        inputs(f).forEach(function (i) { i.disabled = hide; });
        if (hide) clearErr(f);
      }
      var need = condOf(f, "data-required-if");
      if (need) {
        var on = !hide && evalCond(need);
        if (on) f.setAttribute("data-required", ""); else f.removeAttribute("data-required");
        $all(".if-req", f).forEach(function (m) { m.hidden = !on; });
      }
    });
    // A page whose questions are all hidden goes too.
    groups.forEach(function (g) {
      var qs = $all(".field", g).filter(function (f) { return !f.classList.contains("is-display"); });
      var empty = qs.length > 0 && qs.every(function (f) { return f.hidden; });
      g.classList.toggle("group-empty", empty);
    });
    applying = false;
    if (pager && !vis(current)) show(stepGroup(current, 1) !== current ? stepGroup(current, 1) : stepGroup(current, -1), false);
  }
  function stepGroup(from, dir) {
    for (var i = from + dir; i >= 0 && i < groups.length; i += dir) if (vis(i)) return i;
    return from;
  }
  // widget.js (widget fields) drives the page through these.
  window.askFormPage = {
    touch: function (f) { touched[f.getAttribute("data-key")] = true; clearErr(f); applyConditions(); refresh(); saveDraft(); },
    flag: flag
  };

  // ── pages ─────────────────────────────────────────────────────────────────
  // A phone pages through the groups; a desktop shows them all, with the
  // section list (.toc) beside them. CSS picks which: the list is displayed
  // exactly when the page is wide enough.
  var groups = $all(".group", form);
  var current = 0, pager = null, nav = null;
  var toc = document.querySelector(".toc"), tocBar = null, tocProgress = null;
  applyConditions();
  function wide() { return !!toc && getComputedStyle(toc).display !== "none"; }
  function groupOf(el) { return groups.indexOf(el.closest(".group")); }
  function groupPending(g) {
    return $all(".field", groups[g]).some(function (f) { return fields.indexOf(f) >= 0 && unanswered(f); });
  }
  function show(g, focus) {
    current = Math.max(0, Math.min(groups.length - 1, g));
    if (!vis(current)) current = stepGroup(current, 1) !== current ? stepGroup(current, 1) : stepGroup(current, -1);
    seen[current] = true;
    groups.forEach(function (el, i) { el.classList.toggle("current", i === current); });
    refresh();
    if (focus) {
      var first = $all(".field", groups[current]).filter(unanswered)[0];
      var el = first && first.querySelector("input:not([type=hidden]), select, textarea");
      if (wide()) groups[current].scrollIntoView({ block: "start" });
      else window.scrollTo({ top: form.getBoundingClientRect().top + window.scrollY - 8 });
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
    back.addEventListener("click", function () { show(stepGroup(current, -1), true); });
    next.addEventListener("click", function () { show(stepGroup(current, 1), "field"); });
    nav.appendChild(back);
    nav.appendChild(next);
    groups[groups.length - 1].after(nav);
    nav.back = back;
    nav.next = next;
  }
  if (toc) {
    tocProgress = make("p", "toc-progress");
    var bar = make("div", "toc-bar");
    tocBar = make("i");
    bar.appendChild(tocBar);
    toc.appendChild(tocProgress);
    toc.appendChild(bar);
    $all("a[data-toc]", toc).forEach(function (a, i) {
      a.addEventListener("click", function (e) {
        if (!wide()) return;
        e.preventDefault();
        var still = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
        groups[i].scrollIntoView({ block: "start", behavior: still ? "auto" : "smooth" });
      });
    });
  }
  // On a desktop the current group is the one scrolled to (the last whose top
  // is above a third of the window, the last one at the bottom of the page).
  function spy() {
    if (!wide()) return;
    var line = window.innerHeight / 3, at = 0;
    groups.forEach(function (el, i) { if (vis(i) && el.getBoundingClientRect().top <= line) at = i; });
    while (at > 0 && !vis(at)) at--;
    if (window.scrollY > 0 && window.innerHeight + window.scrollY >= document.documentElement.scrollHeight - 2) at = stepGroup(groups.length, -1);
    if (at === current) return;
    current = at;
    seen[at] = true;
    groups.forEach(function (el, i) { el.classList.toggle("current", i === current); });
    refresh();
  }
  function refresh() {
    if (toc) {
      $all("a[data-toc]", toc).forEach(function (a, i) {
        var qs = $all(".field", groups[i]).filter(function (f) { return fields.indexOf(f) >= 0 && !f.hidden; });
        a.parentNode.hidden = !vis(i);
        a.className = (i === current ? "current" : "") + (qs.length ? (qs.some(unanswered) ? " pending" : " done") : "");
        if (i === current) a.setAttribute("aria-current", "location");
        else a.removeAttribute("aria-current");
      });
      var asked = fields.filter(function (f) { return !f.closest(".catchall") && !f.hidden; });
      var n = asked.filter(function (f) { return !unanswered(f); }).length;
      tocProgress.textContent = n + " of " + asked.length + " answered";
      tocBar.style.width = (asked.length ? Math.round(100 * n / asked.length) : 0) + "%";
    }
    fields.forEach(function (f) {
      var lab = f.querySelector(".label");
      var dot = lab && lab.querySelector(".pending-dot");
      var want = !!pager && unanswered(f);
      if (want && !dot && lab) lab.appendChild(make("span", "pending-dot"));
      if (!want && dot) dot.remove();
    });
    if (!pager) return;
    var shown = groups.map(function (el, i) { return i; }).filter(vis);
    pager.querySelector(".pager-count").textContent = (shown.indexOf(current) + 1) + "/" + shown.length;
    $all(".pager-seg", pager).forEach(function (s, i) {
      s.hidden = !vis(i);
      s.className = "pager-seg" + (i === current ? " current" : i < current ? " done" : "") +
        (i !== current && groupPending(i) ? " pending" : "");
    });
    pager.querySelector(".pager-title").textContent = groups[current].getAttribute("data-title") || "";
    nav.back.disabled = stepGroup(current, -1) === current;
    nav.next.disabled = stepGroup(current, 1) === current;
  }
  if (pager) {
    var start = 0;
    if (hadErrors) {
      var bad = form.querySelector(".has-error");
      start = bad ? Math.max(0, groupOf(bad)) : 0;
    } else {
      for (var i = 0; i < groups.length; i++) {
        if (vis(i) && (!seen[i] || groupPending(i))) { start = i; break; }
      }
    }
    show(start, false);
    spy();
    var ticking = false, wasWide = wide();
    window.addEventListener("scroll", function () {
      if (ticking) return;
      ticking = true;
      window.requestAnimationFrame(function () { ticking = false; spy(); });
    }, { passive: true });
    window.addEventListener("resize", function () {
      var w = wide();
      if (w === wasWide) return;
      wasWide = w;
      if (w) spy(); else show(current, false);
    });
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

  // ── submit: the whole form checked, then a confirm step ─────────────────
  var confirmed = false;
  form.addEventListener("submit", function (e) {
    var by = e.submitter;
    if (by && by.value === "decline") { lock(); store.del(draftKey); return; }
    if (confirmed) { lock(); return; }
    e.preventDefault();
    var bad = fields.filter(function (f) {
      var msg = problem(f);
      if (msg) flag(f, msg);
      return !!msg;
    });
    if (bad.length) {
      var g = groupOf(bad[0]);
      if (pager && g >= 0) show(g, false);
      bad[0].scrollIntoView({ block: "center" });
      var el = bad[0].querySelector("input:not([type=hidden]), select, textarea");
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
    var c = lab.querySelector(".bar-text span, .choice-text span");
    return c ? c.textContent.trim() : i.value;
  }
  function answerOf(f) {
    var t = f.getAttribute("data-type");
    if (t === "file") return $all(".file-item.done .file-name", f).map(function (n) { return n.textContent; }).join(", ");
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
    if (t === "radio" || t === "rating" || t === "multiselect" || t === "select") {
      var parts = inputs(f).filter(function (i) {
        return (i.type === "radio" || i.type === "checkbox") && i.checked && i.value !== "__other__";
      }).map(choiceText);
      if (oText) parts.push(oText);
      return parts.join(", ");
    }
    if (t === "list") {
      return $all(".list-row", f).map(function (row) {
        return $all("input, select", row).map(function (i) { return i.value.trim(); }).filter(Boolean).join(" · ");
      }).filter(Boolean).join("; ");
    }
    if (t === "daterange") {
      var d = inputs(f).map(function (i) { return i.value; });
      return d[0] || d[1] ? d[0] + " → " + d[1] : "";
    }
    var main = f.querySelector("input, textarea");
    if (t === "widget" && main) {
      var wv = main.value.trim();
      try { var j = JSON.parse(wv); wv = typeof j === "string" ? j : JSON.stringify(j); } catch (e) { /* shown as typed */ }
      return wv.length > 200 ? wv.slice(0, 200) + "…" : wv;
    }
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
      if (f.hidden) return;
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
          openOverlay([{ frame: a.getAttribute("href"), label: a.getAttribute("data-zoom-label") || "" }], 0);
          return;
        }
        var imgs = $all("img[data-zoom]", field || document);
        var list = imgs.map(function (i) { return { img: i.getAttribute("data-zoom"), label: i.getAttribute("data-zoom-label") || "" }; });
        var at = list.map(function (x) { return x.img; }).indexOf(a.getAttribute("href"));
        openOverlay(list, Math.max(0, at));
      });
    });
  }
  // The fullscreen view of a mockup: its own shape, as large as the screen
  // allows below the close button and above the label; a fluid one as wide
  // as the view lets it read, as tall as it is.
  function fitOverlay(f) {
    var w = Number(f.getAttribute("data-w")), h = Number(f.getAttribute("data-h"));
    var aw = window.innerWidth - 32, ah = window.innerHeight - 128;
    if (!(w > 0 && h > 0 && aw > 0 && ah > 0)) return;
    if (f.hasAttribute("data-fluid")) {
      f.style.width = Math.min(aw, 1280) + "px";
      f.style.height = Math.min(ah, h) + "px";
      return;
    }
    var z = Math.min(aw / w, ah / h);
    f.style.width = Math.floor(w * z) + "px";
    f.style.height = Math.floor(h * z) + "px";
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
    function done() { ov.remove(); document.removeEventListener("keydown", key); window.removeEventListener("resize", refit); }
    function refit() { var f = holder.querySelector("iframe"); if (f) fitOverlay(f); }
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
    window.addEventListener("resize", refit);
    render();
    document.body.appendChild(ov);
    close.focus();
  }
})();
