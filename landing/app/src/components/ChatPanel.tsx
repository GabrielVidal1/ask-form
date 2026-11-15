import { useRef, useState, type KeyboardEvent } from 'react'
import { Cloud, Laptop, MessageSquare } from 'lucide-react'

import { useCopy } from '../copy/context'
import { connector } from '../copy/en'
import type { Origin } from '../lib-origin'
import { CopyButton } from './CopyButton'

/**
 * "Add it to your chat": the connector URL, then one tab per chat app with
 * its steps. Every panel is in the page; without JavaScript the tab bar
 * hides and they all show, one under the other, each under its own name.
 */
export function ChatPanel({ origin }: { origin: Origin }) {
  const t = useCopy()
  const apps = t.chat.tabs(origin.origin)
  const url = connector(origin.origin)
  const [active, setActive] = useState(0)
  const tabs = useRef<(HTMLButtonElement | null)[]>([])

  // Arrow keys move along the tab bar, like any tablist.
  const onKey = (e: KeyboardEvent) => {
    const n = apps.length
    const keys: Record<string, number> = { ArrowRight: (active + 1) % n, ArrowLeft: (active - 1 + n) % n, Home: 0, End: n - 1 }
    const to = keys[e.key]
    if (to === undefined) return
    e.preventDefault()
    setActive(to)
    tabs.current[to]?.focus()
  }

  return (
    <div className="panel any-chat reveal" id="chat">
      {/* Links to the claude.ai panel this one replaced land here. */}
      <span id="claude-ai" className="anchor" aria-hidden />
      <h3>
        <MessageSquare size={20} /> {t.chat.title}
      </h3>
      <p>{t.chat.intro}</p>
      <p className="url-label">{t.chat.url}</p>
      <div className="copy">
        <pre className="mono">{url}</pre>
        <CopyButton value={() => connector(origin.origin)} label={t.copy.copy} className="copy-btn" />
      </div>
      <div className="tabs chat-tabs" role="tablist" aria-label={t.chat.apps} onKeyDown={onKey}>
        {apps.map((a, i) => (
          <button
            key={a.id}
            ref={(el) => {
              tabs.current[i] = el
            }}
            type="button"
            role="tab"
            id={`chat-tab-${a.id}`}
            aria-controls={`chat-panel-${a.id}`}
            aria-selected={i === active}
            tabIndex={i === active ? 0 : -1}
            className="tab"
            onClick={() => setActive(i)}
          >
            {a.name}
          </button>
        ))}
      </div>
      {apps.map((a, i) => (
        <div
          key={a.id}
          role="tabpanel"
          id={`chat-panel-${a.id}`}
          aria-labelledby={`chat-tab-${a.id}`}
          hidden={i !== active}
          className="chat-app"
        >
          <h4 className="tab-h">{a.name}</h4>
          <ol className="steps">
            {a.steps.map((s, j) => (
              <li key={j}>
                <span>{s.text}</span>
                {s.code && (
                  <div className="copy">
                    <pre className="mono">{s.code}</pre>
                    <CopyButton value={s.code} label={t.copy.copy} className="copy-btn" />
                  </div>
                )}
              </li>
            ))}
          </ol>
        </div>
      ))}
      <p className="small">{t.chat.signin}</p>
    </div>
  )
}

/** Under the panel: where a chat's forms live, the two choices side by side. */
export function WhereForms({ origin }: { origin: Origin }) {
  const t = useCopy().where
  const cols = [
    { icon: Cloud, title: t.cloud.title(origin.host), points: t.cloud.points(origin.host) },
    { icon: Laptop, title: t.computer.title, points: t.computer.points(origin.host) },
  ]
  return (
    <div className="where reveal">
      <h3>{t.title}</h3>
      <p className="small">{t.sub}</p>
      <div className="where-cols">
        {cols.map((c) => (
          <div key={c.title} className="panel where-col">
            <span className="fact-ic">
              <c.icon size={20} />
            </span>
            <b>{c.title}</b>
            <ul>
              {c.points.map((p, i) => (
                <li key={i}>{p}</li>
              ))}
            </ul>
          </div>
        ))}
      </div>
    </div>
  )
}
