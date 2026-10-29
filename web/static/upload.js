// ask-form answer page — file fields. A file is sent in 1 MB chunks to the
// form's own upload endpoint the moment it is picked; the form post then only
// carries the refs of the finished uploads (a JSON array in the field's hidden
// input), so the draft, the confirm step and the server's checks keep working
// as for any other field. Loaded next to app.js; without JavaScript the field
// says so and nothing else changes.
(function () {
  "use strict";
  var CHUNK = 1 << 20;
  var RETRIES = 3;
  var form = document.getElementById("form");
  if (!form) return;
  var csrfInput = form.querySelector("input[name=csrf]");
  var csrf = csrfInput ? csrfInput.value : "";
  var formId = form.getAttribute("data-form") || "";

  function $all(sel, el) { return Array.prototype.slice.call((el || document).querySelectorAll(sel)); }
  function make(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text != null) e.textContent = text;
    return e;
  }
  // The page's language, as app.js reads it (<html data-l10n>).
  var l10n = {};
  try { l10n = JSON.parse(document.documentElement.getAttribute("data-l10n") || "{}"); } catch (e) { /* English */ }
  function tr(s, vars) {
    s = l10n[s] || s;
    for (var k in vars || {}) s = s.split("{" + k + "}").join(vars[k]);
    return s;
  }
  // French keeps 0 singular ("0 fichier"), English doesn't.
  function single(n) { return n === 1 || (n === 0 && /^fr\b/.test(document.documentElement.lang)); }
  function human(n) {
    if (n < 1000) return n + " " + tr("B");
    if (n < 1e6) return Math.round(n / 1000) + " " + tr("KB");
    var mb = (n / 1e6).toFixed(1);
    return (/^fr\b/.test(document.documentElement.lang) ? mb.replace(".", ",") : mb) + " " + tr("MB");
  }
  function rand() {
    var a = new Uint8Array(12);
    (window.crypto || window.msCrypto).getRandomValues(a);
    return Array.prototype.map.call(a, function (b) { return (b % 36).toString(36); }).join("");
  }
  function sleep(ms) { return new Promise(function (r) { setTimeout(r, ms); }); }
  var store = {
    get: function (k) { try { return JSON.parse(localStorage.getItem(k) || "null"); } catch (e) { return null; } },
    set: function (k, v) { try { localStorage.setItem(k, JSON.stringify(v)); } catch (e) { /* private mode */ } }
  };

  // What the server turns down for a name and a type, checked here first so a
  // wrong pick fails before the bytes travel (the server decides for real).
  function acceptable(file, accept) {
    if (!accept) return true;
    var name = file.name.toLowerCase(), type = (file.type || "").toLowerCase();
    return accept.split(",").some(function (t) {
      if (t.charAt(0) === ".") return name.slice(-t.length) === t;
      if (t.slice(-2) === "/*") return !type || type.indexOf(t.slice(0, -1)) === 0;
      return !type || type === t;
    });
  }

  var busy = 0;
  function setBusy(delta) {
    busy += delta;
    $all("button[type=submit]", form).forEach(function (b) {
      if (b.getAttribute("name") === "action" && b.value === "decline") return;
      b.disabled = busy > 0;
    });
  }

  $all(".field.t-file").forEach(setup);

  function setup(field) {
    var up = field.querySelector(".uploader");
    if (!up) return;
    var hidden = field.querySelector("input[data-refs]");
    var input = up.querySelector(".file-input");
    var list = up.querySelector(".files");
    var drop = up.querySelector(".drop");
    var key = field.getAttribute("data-key");
    var url = up.getAttribute("data-url");
    var maxBytes = Number(up.getAttribute("data-max-bytes")) || 0;
    var maxFiles = Number(up.getAttribute("data-max-files")) || 1;
    var multiple = up.hasAttribute("data-multiple");
    var accept = up.getAttribute("data-accept") || "";
    var metaKey = "ask-form:files:" + formId + ":" + key;
    var items = [];

    // Refs already in the field (a draft, a page re-shown with errors): the
    // names come from what this browser remembered about them.
    var known = store.get(metaKey) || {};
    var refs = [];
    try { refs = JSON.parse(hidden.value || "[]") || []; } catch (e) { refs = []; }
    refs.forEach(function (ref) {
      var m = known[ref] || {};
      items.push({ ref: ref, name: m.name || tr("Uploaded file"), size: m.size || 0, state: "done" });
    });
    render();

    function sync() {
      var done = items.filter(function (i) { return i.state === "done"; }).map(function (i) { return i.ref; });
      hidden.value = done.length ? JSON.stringify(done) : "";
      var meta = {};
      items.forEach(function (i) { if (i.ref) meta[i.ref] = { name: i.name, size: i.size }; });
      store.set(metaKey, meta);
      hidden.dispatchEvent(new Event("change", { bubbles: true }));
    }

    function render() {
      list.textContent = "";
      items.forEach(function (it) {
        var li = make("li", "file-item " + it.state);
        var top = make("div", "file-row");
        top.appendChild(make("span", "file-name", it.name));
        if (it.size) top.appendChild(make("span", "file-size", human(it.size)));
        var rm = make("button", "file-rm", "✕");
        rm.type = "button";
        rm.setAttribute("aria-label", tr("Remove {name}", { name: it.name }));
        rm.addEventListener("click", function () { remove(it); });
        top.appendChild(rm);
        li.appendChild(top);
        if (it.state === "uploading") {
          var p = make("progress");
          p.max = 100;
          p.value = it.pct || 0;
          p.setAttribute("aria-label", tr("Uploading {name}", { name: it.name }));
          li.appendChild(p);
        }
        if (it.state === "error") {
          var line = make("div", "file-err");
          line.appendChild(make("span", null, it.error || tr("Upload failed.")));
          if (it.file) {
            var retry = make("button", "file-retry", tr("Try again"));
            retry.type = "button";
            retry.addEventListener("click", function () { start(it); });
            line.appendChild(retry);
          }
          li.appendChild(line);
        }
        list.appendChild(li);
      });
      up.classList.toggle("full", !multiple && items.length > 0);
      var uploading = items.some(function (i) { return i.state === "uploading"; });
      if (uploading) field.setAttribute("data-uploading", ""); else field.removeAttribute("data-uploading");
    }

    function request(query, body) {
      var qs = "?c=" + encodeURIComponent(csrf) + query;
      return fetch(url + qs, {
        method: "POST", body: body, cache: "no-store", credentials: "same-origin",
        headers: { "Content-Type": "application/octet-stream" }
      });
    }

    function remove(it) {
      var i = items.indexOf(it);
      if (i >= 0) items.splice(i, 1);
      it.aborted = true;
      if (it.ref) request("&rm=" + encodeURIComponent(it.ref), "").catch(function () { /* the server purges it anyway */ });
      render();
      sync();
    }

    async function sendChunk(it, i, total, blob) {
      var q = "&u=" + it.session + "&i=" + i + "&n=" + total + "&name=" + encodeURIComponent(it.name);
      var last;
      for (var attempt = 0; attempt <= RETRIES; attempt++) {
        if (it.aborted) throw new Error("aborted");
        try {
          var res = await request(q, blob);
          var data = null;
          try { data = await res.json(); } catch (e) { data = null; }
          if (res.ok && data) return data;
          // A refusal that retrying can't change.
          if (res.status >= 400 && res.status < 500 && res.status !== 429 && res.status !== 408) {
            var e = new Error((data && data.error) || tr("That file was refused."));
            e.final = true;
            throw e;
          }
          last = new Error((data && data.error) || tr("The connection dropped."));
        } catch (e) {
          if (e.final || it.aborted) throw e;
          last = e;
        }
        await sleep(500 * Math.pow(3, attempt));
      }
      throw last || new Error(tr("Upload failed."));
    }

    async function start(it) {
      var file = it.file;
      it.state = "uploading";
      it.pct = 0;
      it.error = "";
      it.session = it.session || rand();
      it.aborted = false;
      render();
      setBusy(1);
      try {
        var total = Math.max(1, Math.ceil(file.size / CHUNK));
        var data = null;
        for (var i = 0; i < total; i++) {
          data = await sendChunk(it, i, total, file.slice(i * CHUNK, Math.min(file.size, (i + 1) * CHUNK)));
          it.pct = Math.round(100 * (i + 1) / total);
          render();
        }
        if (!data || !data.done) throw new Error(tr("The upload didn't finish — try again."));
        it.ref = data.ref;
        it.name = data.name || it.name;
        it.size = data.size || it.size;
        it.state = "done";
        it.file = null;
        render();
        sync();
      } catch (e) {
        if (!it.aborted) {
          it.state = "error";
          it.error = e.message || tr("Upload failed.");
          if (e.final) it.file = null; // refused for good: another try would get the same answer
          render();
        }
      } finally {
        setBusy(-1);
      }
    }

    function add(files) {
      files = Array.prototype.slice.call(files);
      if (!files.length) return;
      if (!multiple) {
        if (items.length) return; // one file: take it out first (the drop area is hidden meanwhile)
        files = files.slice(0, 1);
      }
      files.forEach(function (file) {
        var it = { name: file.name, size: file.size, file: file, state: "error" };
        if (items.length >= maxFiles) {
          it.error = tr(single(maxFiles) ? "At most {n} file." : "At most {n} files.", { n: maxFiles });
        } else if (!file.size) {
          it.error = tr("That file is empty.");
        } else if (maxBytes && file.size > maxBytes) {
          it.error = tr("Over the {size} limit.", { size: human(maxBytes) });
        } else if (!acceptable(file, accept)) {
          it.error = tr("That kind of file isn't accepted here.");
        } else {
          items.push(it);
          start(it);
          return;
        }
        it.file = null; // nothing to retry
        items.push(it);
      });
      render();
    }

    input.addEventListener("change", function () {
      add(input.files);
      input.value = "";
    });
    ["dragenter", "dragover"].forEach(function (ev) {
      drop.addEventListener(ev, function (e) { e.preventDefault(); up.classList.add("over"); });
    });
    ["dragleave", "drop"].forEach(function (ev) {
      drop.addEventListener(ev, function (e) { e.preventDefault(); up.classList.remove("over"); });
    });
    drop.addEventListener("drop", function (e) {
      if (e.dataTransfer && e.dataTransfer.files) add(e.dataTransfer.files);
    });
  }
})();
