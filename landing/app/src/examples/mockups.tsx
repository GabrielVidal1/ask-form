// The little UI mockups the example forms offer as choices — what an agent
// would draw as `html` options so you pick a layout by looking at it. Their
// words come from the page's copy (`mock` in copy/en.tsx, copy/fr.tsx).
import { Check, Flame, Sparkles } from 'lucide-react'

import { useCopy } from '../copy/context'

export function HabitToday() {
  const t = useCopy().mock
  return (
    <div className="mk">
      <div className="mk-h">{t.today}</div>
      {t.habits.map((h, i) => (
        <div key={h} className="mk-row">
          <span className={'mk-dot' + (i < 2 ? ' on' : '')}>{i < 2 && <Check size={9} strokeWidth={4} />}</span>
          <span className="mk-t">{h}</span>
        </div>
      ))}
    </div>
  )
}

export function HabitWeek() {
  const t = useCopy().mock
  const done = [
    [1, 1, 0, 1, 1, 0, 0],
    [1, 0, 1, 1, 0, 1, 0],
    [1, 1, 1, 1, 1, 1, 0],
    [0, 1, 0, 1, 1, 0, 0],
  ]
  return (
    <div className="mk">
      <div className="mk-h">{t.week}</div>
      <div className="mk-week">
        <span />
        {t.days.map((d, i) => (
          <span key={i} className="mk-day">{d}</span>
        ))}
        {done.map((row, r) => [
          <span key={'l' + r} className="mk-t sm">{t.habits[r]}</span>,
          ...row.map((v, c) => <span key={r + '-' + c} className={'mk-cell' + (v ? ' on' : '')} />),
        ])}
      </div>
    </div>
  )
}

export function HabitStreaks() {
  const t = useCopy().mock
  return (
    <div className="mk">
      <div className="mk-h">{t.streaks}</div>
      <div className="mk-grid2">
        {[12, 5, 21, 3].map((n, i) => (
          <div key={i} className="mk-card">
            <Flame size={11} className="mk-acc" />
            <b>{n}</b>
            <span className="mk-t sm">{t.habits[i]}</span>
          </div>
        ))}
      </div>
    </div>
  )
}

export function InvoiceTable() {
  const t = useCopy().mock
  return (
    <div className="mk">
      <div className="mk-h">{t.invoices}</div>
      <div className="mk-table">
        {['#1042', '#1041', '#1040', '#1039'].map((n) => (
          <div key={n} className="mk-tr">
            <span>{n}</span>
            <span className="mk-bar w60" />
            <span className="mk-bar w40" />
            <span className="mk-bar w50 cut" />
          </div>
        ))}
      </div>
      <div className="mk-scroll">{t.scrolls}</div>
    </div>
  )
}

export function InvoiceCards() {
  const t = useCopy().mock
  return (
    <div className="mk">
      <div className="mk-h">{t.invoices}</div>
      {['#1042', '#1041', '#1040'].map((n, i) => (
        <div key={n} className="mk-inv">
          <div className="mk-inv-top">
            <b>{n}</b>
            <span className={'mk-pill' + (i === 0 ? ' due' : '')}>{i === 0 ? t.due : t.paid}</span>
          </div>
          <span className="mk-bar w70" />
        </div>
      ))}
    </div>
  )
}

export function PricingOne() {
  const t = useCopy().mock
  return (
    <div className="mk mk-center">
      <div className="mk-h">{t.plan}</div>
      <div className="mk-price">
        {t.month.price}
        <small>{t.month.per}</small>
      </div>
      <span className="mk-bar w70" />
      <span className="mk-bar w60" />
      <span className="mk-bar w50" />
      <span className="mk-btn">{t.subscribe}</span>
    </div>
  )
}

export function PricingToggle() {
  const t = useCopy().mock
  return (
    <div className="mk mk-center">
      <div className="mk-toggle">
        <span>{t.monthly}</span>
        <span className="on">{t.yearly}</span>
      </div>
      <div className="mk-price">
        {t.year.price}
        <small>{t.year.per}</small>
      </div>
      <span className="mk-save">{t.free}</span>
      <span className="mk-bar w60" />
      <span className="mk-bar w50" />
      <span className="mk-btn">{t.subscribe}</span>
    </div>
  )
}

export function OnboardChecklist() {
  const t = useCopy().mock
  return (
    <div className="mk">
      <div className="mk-h">{t.start}</div>
      <div className="mk-progress">
        <span />
      </div>
      {t.steps.map((s, i) => (
        <div key={s} className="mk-row">
          <span className={'mk-dot sq' + (i === 0 ? ' on' : '')}>{i === 0 && <Check size={9} strokeWidth={4} />}</span>
          <span className="mk-t">{s}</span>
        </div>
      ))}
    </div>
  )
}

export function OnboardTour() {
  const t = useCopy().mock
  return (
    <div className="mk mk-tour">
      <div className="mk-nav">
        <span className="mk-bar w30" />
        <span className="mk-plus">+</span>
      </div>
      <span className="mk-bar w70" />
      <span className="mk-bar w50" />
      <div className="mk-tip">
        <Sparkles size={10} className="mk-acc" />
        <span>{t.tip}</span>
        <span className="mk-tip-foot">{t.tipFoot}</span>
      </div>
    </div>
  )
}
