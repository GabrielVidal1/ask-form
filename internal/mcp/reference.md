# ask-form field reference (MCP)

`create_form` takes `title`, an optional markdown `description`, and `fields`: an
array of questions and display blocks, in order. The user answers in a
browser; `get_answers` returns `{"key": value}`.

## Questions

```json
[{"type":"markdown","text":"Context the user needs, **markdown**."},
 {"key":"name","label":"Project name","type":"text","required":true,"group":"Basics"},
 {"key":"stack","label":"Stack","type":"radio","required":true,"options":["Vite","Next",{"value":"astro","label":"Astro","description":"static first"}]},
 {"key":"features","label":"Features","type":"multiselect","options":["auth","db"]},
 {"key":"deploy","label":"Deploy after","type":"checkbox"},
 {"key":"polish","label":"Polish","type":"slider","min":1,"max":5,"default":3,"minLabel":"rough","maxLabel":"pixel-perfect"},
 {"key":"budget","label":"Budget (€)","type":"number","min":0},
 {"key":"when","label":"Deadline","type":"date"},
 {"key":"email","label":"Your email","type":"email"},
 {"key":"hero","label":"Hero image","type":"radio","options":[{"value":"a","label":"Fjord","image":"{{asset:a.jpg}}"},{"value":"b","label":"Peaks","html":"<div style=\"width:360px\">…</div>"}]},
 {"type":"html","label":"Current layout","html":"<svg …>…</svg>"}]
```

- Types: `text textarea number select multiselect radio checkbox slider date email
  time datetime daterange rating list widget`, plus the display blocks `markdown`,
  `html` (no scripts; inline styles are fine), `diff` and `details`.
- Not from a chat: `secret` (the value would land in the conversation) and `file`.
- Any field takes `required`, `default`, `help`, `placeholder` and `group`. Past ~4
  questions, give them `group`s: each group is one page.
- A slider's `minLabel` / `maxLabel` name its two ends; `min`, `max`, `step`.
- `rating`: one tap on a scale, `min` (default 1) to `max` (default 5, at most 11
  steps), `"style":"stars"` (the default up to 7) or `"numbers"` (an NPS is
  `min 0, max 10`). → a number.
- `time` (HH:MM), `datetime` (YYYY-MM-DDTHH:MM, the person's own clock) and
  `daterange` (two dates, the end not before the start).
- `list`: rows the person adds and removes. Without `columns` each row is one text
  (→ an array of strings); with `"columns":[{"key","label","type":"text|number|select","options"}]`
  it is a table (→ an array of objects). `min` / `max` bound the rows (default max 50).
- `diff` shows a unified diff (`text`). `details` is a collapsible section: `label`
  is its summary, `text` its markdown, `"open":true` starts it expanded.
- Every choice field gets an "Other…" free-text choice (on a select or radio it
  can be ticked next to the option), and every form ends with an optional
  `anything_else` textarea. Don't add your own.
- Pictures only through `assets` (`{"name":"a.jpg","data":"<base64>"}`) and
  `{{asset:<name>}}`. Remote images are blocked.
- Picture and mockup choices show one card per row on a phone, two from 560 px
  (`columns` 1–6 changes that). Give every option a `label`. `"aspect"`: `"1/2"`
  (tall), `"1"` or `"2/1"` (wide). Draw a mockup at a fixed width (240–600 px): it
  is scaled to fill its card.
- Colour an `html` block or mockup with the theme variables
  (`hsl(var(--foreground))`, `--muted-foreground`, `--card`, `--border`,
  `--primary`…) and it follows the page into dark mode.
- Put your recommendation in `default` and say why in `help`.

## Widgets

When no type fits (drag items, tap a spot on a mockup, tune a colour), write the
control as HTML plus script:

```json
{"key":"spot","label":"Where should the button go?","type":"widget","schema":"object","required":true,"height":320,
 "html":"<div id=p style=\"height:300px\"></div><script>p.onclick=e=>askForm.setValue({x:e.offsetX,y:e.offsetY})</script>"}
```

It runs in a locked frame with no network (inline everything). `askForm.setValue(v)`
reports the answer (≤ 16 KB, the last call wins), `askForm.onInit(cb)` /
`askForm.value` give back an earlier answer, `askForm.setHeight(px)`,
`askForm.theme`. `schema` (`string number boolean object array`) makes the page
refuse any other kind of value.

## Conditions

`showIf` draws a field only while its condition holds (hidden questions are not
required and are left out of the answers); `requiredIf` makes it required while
it holds. A condition looks at questions above it:

```json
{"key":"stack","type":"radio","options":["Vite","Next"]},
{"key":"ssr","type":"checkbox","label":"Server rendering","showIf":{"key":"stack","equals":"Next"}}
```

A test is `{"key":"q","<op>":value}` with `equals`, `notEquals`, `in`, `includes`,
`gt` `gte` `lt` `lte` or `answered`; combine with `{"all":[…]}`, `{"any":[…]}`,
`{"not":{…}}`.

## Answers

- text, textarea, date, select, radio → string; `<key>.other` → the "Other…" text
  added next to a picked option
- email → the address, lowercased · multiselect → array of strings · checkbox →
  bool · number, slider, rating → number
- time, datetime → a string · daterange → `{"start","end"}`
- list → an array of strings, or of objects (a table) · widget → the JSON it reported
- Unanswered optional fields are absent.
- A declined form has `reason` (their words, maybe empty).
- A multi form (`multi: true`) has `answerCount` and `responses`:
  `[{"n":1,"at":"…","answers":{…}},…]`, oldest first.
