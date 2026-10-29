import { forwardRef } from 'react'
import { IOSStatusBar, PhonePreview } from '@gabvdl/ui'
import { Check, CircleCheck, Lock, MessageSquareText } from 'lucide-react'

import { useCopy } from '../copy/context'
import type { Block, Example } from '../examples/data'

/** The logical size the screen is laid out at, then scaled into the frame. */
export const DEVICE_W = 390
export const DEVICE_H = 844

export interface PhoneState {
  /** block index → picked option */
  picked: Record<number, number>
  /** block index → characters typed so far into a secret / text field */
  typed: Record<number, number>
}

interface Props {
  ex: Example
  host: string
  state: PhoneState
  width: number
}

/**
 * The phone side of the demo: a lock screen, the notification the agent's link
 * arrives as, the answer page (laid out at a real phone's width and scaled
 * into the design system's PhonePreview) and the "sent" screen. Demo.tsx moves
 * between them with GSAP; the refs it needs are `data-ph` attributes.
 */
export const Phone = forwardRef<HTMLDivElement, Props>(function Phone({ ex, host, state, width }, ref) {
  const t = useCopy().phone
  const scale = width / DEVICE_W
  const f = ex.form
  return (
    <PhonePreview screenWidth={width} deviceWidth={DEVICE_W} deviceHeight={DEVICE_H} frameColor="#1d1916" title={t.title}>
      <div className="ph-stage" ref={ref} style={{ transform: `scale(${scale})` }} aria-hidden>
        <div className="ph-lock" data-ph="lock">
          <div className="ph-status on-dark">
            <IOSStatusBar />
          </div>
          <b>9:41</b>
          <span>{t.date}</span>
        </div>
        <div className="ph-notif" data-ph="notif" data-tap="notif">
          <span className="ph-notif-icon">
            <MessageSquareText size={20} />
          </span>
          <span className="ph-notif-txt">
            <small>
              <span>ask-form</span>
              <span>{t.now}</span>
            </small>
            <b>{f.title}</b>
            <span>{t.notif}</span>
          </span>
        </div>
        <div className="ph-app" data-ph="app">
          <div className="ph-status">
            <IOSStatusBar />
          </div>
          <div className="ph-url">
            <span>
              <Lock size={12} />
              {host}/f/{f.slug}
            </span>
          </div>
          <div className="ph-view">
            <div className="ph-scroll pf" data-ph="scroll">
              <h3>{f.title}</h3>
              <p className="pf-desc">{f.desc}</p>
              <div className="pf-pager">
                <small>1/{f.pages}</small>
                <span className="pf-segs">
                  {Array.from({ length: f.pages }, (_, i) => (
                    <i key={i} className={i === 0 ? 'on' : ''} />
                  ))}
                </span>
              </div>
              <p className="pf-ptitle">{f.page}</p>
              <div className="pf-group">
                {f.blocks.map((b, i) => (
                  <FormBlock key={i} b={b} i={i} state={state} />
                ))}
              </div>
              <div className="pf-actions">
                <span className="pf-send" data-tap="send">
                  {t.send}
                </span>
              </div>
            </div>
          </div>
        </div>
        <div className="ph-sent" data-ph="sent">
          <span className="ph-sent-ic">
            <CircleCheck size={40} />
          </span>
          <h4>{t.sent}</h4>
          <p>{f.sent}</p>
        </div>
        <span className="ph-tap" data-ph="tap" />
      </div>
    </PhonePreview>
  )
})

function FormBlock({ b, i, state }: { b: Block; i: number; state: PhoneState }) {
  const t = useCopy().phone
  switch (b.kind) {
    case 'finding':
      return (
        <div className="pf-finding">
          <b>{b.title}</b>
          <code>{b.where}</code> {b.text}
          <pre>{b.code}</pre>
        </div>
      )
    case 'secret':
      return (
        <div className="pf-field" data-block={i}>
          <span className="pf-label">
            {b.label}
            <i>*</i>
          </span>
          <span className="pf-input mono">{'•'.repeat(Math.min(b.chars, state.typed[i] ?? 0))}</span>
          <p className="pf-note">
            <Lock size={12} /> {t.secret}
          </p>
        </div>
      )
    case 'text':
      return (
        <div className="pf-field" data-block={i}>
          <span className="pf-label">
            {b.label}
            {!b.optional && <i>*</i>}
          </span>
          <span className="pf-input">{b.value.slice(0, state.typed[i] ?? 0)}</span>
          {b.help && <p className="pf-note">{b.help}</p>}
        </div>
      )
    case 'choice': {
      const picked = state.picked[i] ?? b.selected
      if (b.cards)
        return (
          <div className="pf-field" data-block={i}>
            <span className="pf-label">
              {b.label}
              <i>*</i>
            </span>
            <div className={'pf-cards n' + b.options.length}>
              {b.options.map((o, j) => (
                <div key={j} className={'pf-card' + (picked === j ? ' on' : '')} data-tap={`${i}-${j}`}>
                  <div className="pf-visual">{o.mock && <o.mock />}</div>
                  <span className="pf-cap">
                    <span className="pf-tick">{picked === j && <Check size={11} strokeWidth={3.5} />}</span>
                    {o.label}
                  </span>
                </div>
              ))}
            </div>
          </div>
        )
      return (
        <div className="pf-field" data-block={i}>
          <span className="pf-label">
            {b.label}
            <i>*</i>
          </span>
          <div className="pf-choices">
            {b.options.map((o, j) => (
              <div key={j} className={'pf-choice' + (picked === j ? ' on' : '')} data-tap={`${i}-${j}`}>
                <span className="pf-radio" />
                <span>
                  {o.label}
                  {o.desc && <small>{o.desc}</small>}
                </span>
              </div>
            ))}
          </div>
        </div>
      )
    }
  }
}
