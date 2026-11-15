---
name: ask-form
description: Ask the user structured questions in a private web form they answer in the chat's card or from any browser, phone included — text, choices, picture or mockup choices, sliders, ratings, dates and times, lists and tables, follow-up questions that appear only when they apply — and read the answers back as JSON with the ask-form connector's tools. Use when you need several decisions or values from the user at once, want them to compare options visually, or several people should answer the same form (a team vote, feedback).
---

# ask-form

The ask-form connector puts your questions in a private web form. You call
`create_form`, the user answers (in the card the chat shows, or at the link on
any device), and `get_answers` hands you their answers as JSON.

Reach for it instead of a long list of questions in your reply when you need
**several decisions or values at once**, want the user to **compare options
visually** (mockups, pictures), or **someone else** should answer. One quick
yes/no stays a plain question in the chat.

## Ask

1. Call `create_form` with a `title`, an optional `description` (markdown: the
   context the user needs) and the `fields` (below). Write the form in the
   user's language and pass `lang` (`en` or `fr`; it sets the buttons and
   messages around your questions).
2. Put the `url` it returns in your reply as a link. It holds the form's
   token: don't repeat it anywhere else. Where the chat shows the ask-form
   card, the user can open the form from there too.
3. Call `get_answers` with the form's `id`. It waits up to `wait_seconds`
   (max 90) for the answers. Still pending: call it again, or end your turn
   saying you'll continue once they've answered — the card's *Continue with
   my answers* button, or the user's next message, brings you back; then call
   `get_answers` once more.

A form the user declines comes back `declined`, sometimes with a `reason`:
read it before deciding what to do next. `close_form` withdraws a form you no
longer need; `list_forms` shows this connection's recent forms.

## Fields

```json
[{"type":"markdown","text":"Context the user needs, **markdown**."},
 {"key":"name","label":"Project name","type":"text","required":true,"group":"Basics"},
 {"key":"stack","label":"Stack","type":"radio","required":true,"default":"vite","help":"Fastest to ship.","options":[{"value":"vite","label":"Vite"},{"value":"next","label":"Next.js","description":"if you need SSR"}]},
 {"key":"features","label":"Features","type":"multiselect","options":["auth","db"]},
 {"key":"deploy","label":"Deploy after","type":"checkbox"},
 {"key":"polish","label":"Polish","type":"slider","min":1,"max":5,"default":3,"minLabel":"rough","maxLabel":"pixel-perfect"},
 {"key":"when","label":"Deadline","type":"date"},
 {"key":"layout","label":"Layout","type":"radio","options":[{"value":"a","label":"Sidebar","html":"<div style=\"width:360px\">…</div>"},{"value":"b","label":"Top bar","html":"<div style=\"width:360px\">…</div>"}]}]
```

- Types: `text textarea number select multiselect radio checkbox slider date email
  time datetime daterange rating list widget`, plus the display blocks `markdown`,
  `html` (no scripts; inline styles are fine), `diff` and `details`. No `secret` or
  `file` fields from a chat: a secret would land in the conversation.
- Any field takes `required`, `default`, `help` and `group`. Past ~4 questions,
  give them `group`s: each group is one page.
- **Mockups**: give a choice's options an `html` (a design drawn at a fixed
  240–600 px width) and each becomes a card the user picks from. Colour it with the
  theme variables (`hsl(var(--foreground))`, `--muted-foreground`, `--card`,
  `--border`, `--primary`) so it follows dark mode. Pictures go in `assets`
  (`[{"name":"a.png","data":"<base64>"}]`) and are referenced as
  `"image":"{{asset:a.png}}"`; remote images are blocked.
- `rating` (stars or numbers, `min`/`max`), `list` (rows; with `columns` a table),
  `daterange` (`{"start","end"}`), `time`, `datetime`.
- Every choice field gets an "Other…" free-text choice and every form ends with an
  optional "Anything else?" box: don't add your own.
- Ask only what applies: `"showIf":{"key":"stack","equals":"next"}` (ops `equals
  notEquals in includes gt gte lt lte answered`; combine with `all`, `any`, `not`).
  A condition looks at questions above it.
- Put your recommendation in `default` and say why in `help`, so the user can
  accept it in one tap.

## Many people, one form

`multi: true` makes one link that takes any number of answers (a team vote,
feedback) until it expires (`ttl`, e.g. `"72h"`) or you `close_form` it;
`get_answers` then returns `responses`, one per person.

## Answers

`{"id","status","answers":{key: value}}`: text, date, select, radio → string
(an "Other…" answer is the user's own text; `<key>.other` is a detail added next to
an option) · multiselect → array · checkbox → bool · number, slider, rating →
number · daterange → `{"start","end"}` · list → array of strings or objects ·
widget → the JSON it reported. Unanswered optional fields are absent.

## Where the forms live

The user chose when they connected: on ask-form.xyz, where a form and its
answers are deleted 24 hours after it closes, or on their own computer (it
must be running `ask-form serve`; a tool error says when it's offline).
