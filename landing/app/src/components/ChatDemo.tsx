import { useEffect, useRef, useState } from 'react'
import { ArrowUp, ArrowUpRight, Plug, Sparkles } from 'lucide-react'

import { useCopy } from '../copy/context'

// The steps of the chat, in order; the prerender (and a reduced-motion
// visitor) gets the last one: the whole conversation, the form answered.
const USER = 0 // your message
const INTRO = 1 // the assistant's reply and its tool call
const CARD = 2 // the card, waiting
const ANSWERED = 3 // answered (on the phone, say): Continue shows
const SENT = 4 // Continue tapped: your message in the chat
const REPLY = 5 // the assistant carries on
const LAST = REPLY

// How long each step stays before the next, in ms (the last one: before it loops).
const HOLD: Record<number, number> = { [USER]: 900, [INTRO]: 900, [CARD]: 3000, [ANSWERED]: 1900, [SENT]: 1100, [REPLY]: 4200 }

/**
 * The second demo: the connector in a chat. A neutral chat window (no app's
 * branding) where the assistant creates a form, the ask-form card shows up
 * (internal/mcp/card.html's look), the form gets answered, and "Continue
 * with my answers" hands them back. It plays while on screen, then loops.
 */
export function ChatDemo() {
  const t = useCopy().chatDemo
  const c = t.card
  const [step, setStep] = useState(LAST)
  const [live, setLive] = useState(false)
  const rootRef = useRef<HTMLElement>(null)
  const visible = useRef(false)
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined)
  const stepRef = useRef(LAST)
  stepRef.current = step

  useEffect(() => {
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) return
    setLive(true)
    let started = false
    const tick = () => {
      clearTimeout(timer.current)
      if (!visible.current || document.hidden) return
      timer.current = setTimeout(() => {
        const next = stepRef.current >= LAST ? USER : stepRef.current + 1
        stepRef.current = next
        setStep(next)
        tick()
      }, HOLD[stepRef.current])
    }
    const io = new IntersectionObserver(
      ([e]) => {
        visible.current = e.isIntersecting
        if (e.isIntersecting && !started) {
          started = true
          stepRef.current = USER
          setStep(USER)
        }
        tick()
      },
      { threshold: 0.4 },
    )
    io.observe(rootRef.current!)
    const vis = () => tick()
    document.addEventListener('visibilitychange', vis)
    return () => {
      io.disconnect()
      document.removeEventListener('visibilitychange', vis)
      clearTimeout(timer.current)
    }
  }, [])

  const shown = (s: number) => (step >= s ? 'on' : 'off')
  const answered = step >= ANSWERED

  return (
    <section className="demo wrap chat-demo" ref={rootRef}>
      <div className="demo-head reveal">
        <h2 className="h2">{t.title}</h2>
        <p className="sub">{t.sub}</p>
      </div>
      <div className={`cw panel reveal${live ? ' live' : ''}`} role="figure" aria-label={t.window}>
        <div className="cw-bar" aria-hidden>
          <i />
          <span>{t.bar}</span>
        </div>
        <div className="cw-body">
          <p className="cw-you" data-s={shown(USER)}>
            {t.user}
          </p>
          <div className="cw-ai" data-s={shown(INTRO)}>
            <span className="cw-av" aria-hidden>
              <Sparkles size={14} />
            </span>
            <div className="cw-ai-body">
              <p>{t.intro}</p>
              <p className="cw-tool">
                <Plug size={13} /> {t.tool}
              </p>
              <div className="cc" data-s={shown(CARD)}>
                <p className="cc-brand">
                  <svg width="14" height="14" viewBox="0 0 16 16" aria-hidden>
                    <rect x="1.5" y="1.5" width="13" height="13" rx="3.5" fill="none" stroke="currentColor" strokeWidth="1.4" />
                    <path d="M4.8 8.2l2.1 2.1 4.3-4.6" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
                  </svg>
                  ask-form
                </p>
                <p className="cc-title">{c.title}</p>
                <p className="cc-meta">{c.meta}</p>
                <p className={`cc-status ${answered ? 'ok' : 'wait'}`}>
                  <span className="cc-dot" /> {answered ? c.answered : c.waiting}
                </p>
                <div className="cc-actions">
                  <span className="cc-btn">
                    {c.open} <ArrowUpRight size={14} />
                  </span>
                  <span className={`cc-btn primary${answered ? '' : ' gone'}`}>
                    {c.cont}
                    {live && step === ANSWERED && <span className="cc-tap" aria-hidden />}
                  </span>
                </div>
                <p className="cc-hint">{c.hint}</p>
              </div>
            </div>
          </div>
          <p className="cw-you" data-s={shown(SENT)}>
            {t.sent}
          </p>
          <div className="cw-ai" data-s={shown(REPLY)}>
            <span className="cw-av" aria-hidden>
              <Sparkles size={14} />
            </span>
            <div className="cw-ai-body">
              <p>{t.reply}</p>
            </div>
          </div>
        </div>
        <div className="cw-composer" aria-hidden>
          <span>{t.composer}</span>
          <i>
            <ArrowUp size={15} />
          </i>
        </div>
      </div>
    </section>
  )
}
