// The little UI mockups the example forms offer as choices — what an agent
// would draw as `html` options so you pick a layout by looking at it.
import { Check, Flame, Sparkles } from 'lucide-react'

const habits = ['Read', 'Run', 'Water', 'Sleep']

export function HabitToday() {
  return (
    <div className="mk">
      <div className="mk-h">Today</div>
      {habits.map((h, i) => (
        <div key={h} className="mk-row">
          <span className={'mk-dot' + (i < 2 ? ' on' : '')}>{i < 2 && <Check size={9} strokeWidth={4} />}</span>
          <span className="mk-t">{h}</span>
        </div>
      ))}
    </div>
  )
}

export function HabitWeek() {
  const days = ['M', 'T', 'W', 'T', 'F', 'S', 'S']
  const done = [
    [1, 1, 0, 1, 1, 0, 0],
    [1, 0, 1, 1, 0, 1, 0],
    [1, 1, 1, 1, 1, 1, 0],
    [0, 1, 0, 1, 1, 0, 0],
  ]
  return (
    <div className="mk">
      <div className="mk-h">This week</div>
      <div className="mk-week">
        <span />
        {days.map((d, i) => (
          <span key={i} className="mk-day">{d}</span>
        ))}
        {done.map((row, r) => [
          <span key={'l' + r} className="mk-t sm">{habits[r]}</span>,
          ...row.map((v, c) => <span key={r + '-' + c} className={'mk-cell' + (v ? ' on' : '')} />),
        ])}
      </div>
    </div>
  )
}

export function HabitStreaks() {
  return (
    <div className="mk">
      <div className="mk-h">Streaks</div>
      <div className="mk-grid2">
        {[12, 5, 21, 3].map((n, i) => (
          <div key={i} className="mk-card">
            <Flame size={11} className="mk-acc" />
            <b>{n}</b>
            <span className="mk-t sm">{habits[i]}</span>
          </div>
        ))}
      </div>
    </div>
  )
}

export function InvoiceTable() {
  return (
    <div className="mk">
      <div className="mk-h">Invoices</div>
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
      <div className="mk-scroll">← scrolls →</div>
    </div>
  )
}

export function InvoiceCards() {
  return (
    <div className="mk">
      <div className="mk-h">Invoices</div>
      {['#1042', '#1041', '#1040'].map((n, i) => (
        <div key={n} className="mk-inv">
          <div className="mk-inv-top">
            <b>{n}</b>
            <span className={'mk-pill' + (i === 0 ? ' due' : '')}>{i === 0 ? 'due' : 'paid'}</span>
          </div>
          <span className="mk-bar w70" />
        </div>
      ))}
    </div>
  )
}

export function PricingOne() {
  return (
    <div className="mk mk-center">
      <div className="mk-h">Pro</div>
      <div className="mk-price">
        €9<small>/mo</small>
      </div>
      <span className="mk-bar w70" />
      <span className="mk-bar w60" />
      <span className="mk-bar w50" />
      <span className="mk-btn">Subscribe</span>
    </div>
  )
}

export function PricingToggle() {
  return (
    <div className="mk mk-center">
      <div className="mk-toggle">
        <span>Monthly</span>
        <span className="on">Yearly</span>
      </div>
      <div className="mk-price">
        €90<small>/yr</small>
      </div>
      <span className="mk-save">2 months free</span>
      <span className="mk-bar w60" />
      <span className="mk-bar w50" />
      <span className="mk-btn">Subscribe</span>
    </div>
  )
}

export function OnboardChecklist() {
  return (
    <div className="mk">
      <div className="mk-h">Get started</div>
      <div className="mk-progress">
        <span />
      </div>
      {['Create a board', 'Invite your team', 'Add a first task'].map((t, i) => (
        <div key={t} className="mk-row">
          <span className={'mk-dot sq' + (i === 0 ? ' on' : '')}>{i === 0 && <Check size={9} strokeWidth={4} />}</span>
          <span className="mk-t">{t}</span>
        </div>
      ))}
    </div>
  )
}

export function OnboardTour() {
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
        <span>Tap + to add your first task</span>
        <span className="mk-tip-foot">1 of 4 · Next</span>
      </div>
    </div>
  )
}
