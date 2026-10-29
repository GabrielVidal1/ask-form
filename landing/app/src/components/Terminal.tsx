import { ProgressiveText } from '@gabvdl/ui'
import { ArrowUpRight, Check, LoaderCircle } from 'lucide-react'

import type { Example } from '../examples/data'

interface Props {
  ex: Example
  host: string
  /** What the prompt line is typing towards: ProgressiveText deletes back to
   *  the common prefix and types the rest whenever it changes. */
  prompt: string
  onTyped: () => void
}

/** The terminal side of the demo: your prompt, then the agent's reply. */
export function Terminal({ ex, host, prompt, onTyped }: Props) {
  return (
    <div className="term">
      <div className="term-bar" aria-hidden>
        <i />
        <i />
        <i />
        <span>{ex.cwd}</span>
      </div>
      <div className="term-body">
        <div className="t-you">
          <span className="t-ps" aria-hidden>
            ❯
          </span>
          <span className="t-text">
            <span className="sr-only">{ex.prompt}</span>
            {/* With scripting on, the browser keeps <noscript> as raw text, so
                the prerender's escaped ' or & never match on hydration. */}
            <noscript suppressHydrationWarning>{ex.prompt}</noscript>
            <span aria-hidden>
              <ProgressiveText text={prompt} speed={60} deleteSpeed={360} onDone={onTyped}>
                {(visible) => (
                  <>
                    {visible}
                    <span className="t-caret" />
                  </>
                )}
              </ProgressiveText>
            </span>
          </span>
        </div>
        <div className="t-agent">
          {ex.lines.map((l, i) => {
            switch (l.kind) {
              case 'tool':
                return (
                  <div key={i} className="t-line" data-line>
                    <span className="t-bullet tool">●</span>
                    <span className="t-cmd">
                      <b>ask-form create</b>
                      {l.text.replace(/^ask-form create/, '')}
                      <span className="t-out">⎿ {l.out}</span>
                    </span>
                  </div>
                )
              case 'say':
                return (
                  <div key={i} className="t-line" data-line>
                    <span className="t-bullet">●</span>
                    <span className="t-say">
                      {l.text}
                      {l.link && (
                        <>
                          <br />
                          <a className="t-link" href={ex.href}>
                            {host}/f/{ex.form.slug}
                            <ArrowUpRight size={14} />
                          </a>
                        </>
                      )}
                    </span>
                  </div>
                )
              case 'wait':
                return (
                  <div key={i} className="t-line" data-line="wait">
                    <span className="t-bullet wait">
                      <LoaderCircle size={14} className="t-spin" />
                    </span>
                    <span className="t-dim">{l.text}</span>
                  </div>
                )
              case 'work':
                return (
                  <div key={i} className="t-work" data-line>
                    <Check size={13} />
                    <span>{l.text}</span>
                  </div>
                )
              case 'done':
                return (
                  <div key={i} className="t-line" data-line>
                    <span className="t-bullet done">✓</span>
                    <span className="t-done">{l.text}</span>
                  </div>
                )
            }
          })}
        </div>
      </div>
    </div>
  )
}
