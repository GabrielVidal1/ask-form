import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { gsap } from 'gsap'
import { ArrowRight, BookOpen } from 'lucide-react'
import { CopyButton } from '@gabvdl/ui'

import { examples, type Example } from '../examples/data'
import { DEVICE_H, Phone, type PhoneState } from './Phone'
import { Terminal } from './Terminal'

// useLayoutEffect warns during the build-time render; it only matters in the browser.
const useIsoLayoutEffect = typeof window === 'undefined' ? useEffect : useLayoutEffect

const VIEW_TOP = 84 // status bar + address bar, in device px
const PHONE_W = 290 // the phone's screen width; CSS scales it down on phones
const HOLD = 3.2 // seconds the finished example stays up before the next one

/** Every field filled in: what the page shows without animation. */
function filled(ex: Example): PhoneState {
  const typed: Record<number, number> = {}
  ex.form.blocks.forEach((b, i) => {
    if (b.kind === 'secret') typed[i] = b.chars
    if (b.kind === 'text') typed[i] = b.value.length
  })
  return { picked: {}, typed }
}

interface Props {
  host: string
  /** The prompt's copy button puts the install line in front of it. */
  installLine: string
}

/**
 * "One prompt, one form": the terminal on the left types your prompt and the
 * agent's reply, the phone on the right gets the link and answers it. Each
 * example runs as one GSAP timeline, started once its prompt has finished
 * typing; then the next example's prompt replaces it in place.
 */
export function Demo({ host, installLine }: Props) {
  const [active, setActive] = useState(0)
  const [prompt, setPrompt] = useState('')
  const [phone, setPhone] = useState<PhoneState>(() => filled(examples[0]))
  const [live, setLive] = useState(false)
  const [run, setRun] = useState(0) // bumps to restart the current example

  const rootRef = useRef<HTMLDivElement>(null)
  const stageRef = useRef<HTMLDivElement>(null)
  const tlRef = useRef<gsap.core.Timeline | null>(null)
  const visibleRef = useRef(false)
  const startedRef = useRef(false)
  const activeRef = useRef(0)
  activeRef.current = active

  const ex = examples[active]

  useEffect(() => {
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
      setPrompt(examples[0].prompt) // ProgressiveText shows it at once
      return
    }
    document.documentElement.classList.add('live')
    setLive(true)
    // Play only while the demo is on screen and the tab is visible.
    const io = new IntersectionObserver(
      ([e]) => {
        visibleRef.current = e.isIntersecting
        if (e.isIntersecting && !startedRef.current) {
          startedRef.current = true
          setRun((r) => r + 1)
        }
        if (e.isIntersecting && !document.hidden) tlRef.current?.play()
        else tlRef.current?.pause()
      },
      { threshold: 0.35 },
    )
    io.observe(rootRef.current!)
    const vis = () => (document.hidden || !visibleRef.current ? tlRef.current?.pause() : tlRef.current?.play())
    document.addEventListener('visibilitychange', vis)
    return () => {
      io.disconnect()
      document.removeEventListener('visibilitychange', vis)
      tlRef.current?.kill()
    }
  }, [])

  // A new example (or a restart): put both sides back to their start, then
  // let the prompt type. The rest waits for onTyped.
  useIsoLayoutEffect(() => {
    if (!live || run === 0) return
    tlRef.current?.kill()
    const stage = stageRef.current!
    const root = rootRef.current!
    const q = (s: string) => stage.querySelector<HTMLElement>(`[data-ph="${s}"]`)!
    gsap.set(root.querySelectorAll('[data-line]'), { autoAlpha: 0, y: 8 })
    gsap.set(root.querySelectorAll('.tab-bar'), { scaleX: 0 })
    gsap.set(q('notif'), { autoAlpha: 0, yPercent: -160 })
    gsap.set(q('app'), { autoAlpha: 1, yPercent: 100 })
    gsap.set(q('sent'), { autoAlpha: 0 })
    gsap.set(q('scroll'), { y: 0 })
    gsap.set(q('tap'), { autoAlpha: 0 })
    setPhone({ picked: {}, typed: {} })
    setPrompt(ex.prompt)
  }, [live, run, active])

  const next = useCallback(() => {
    setActive((a) => (a + 1) % examples.length)
    setRun((r) => r + 1)
  }, [])

  const onTyped = useCallback(() => {
    if (!live) return
    tlRef.current?.kill()
    const tl = buildTimeline(examples[activeRef.current], stageRef.current!, rootRef.current!, {
      pick: (b, o) => setPhone((p) => ({ ...p, picked: { ...p.picked, [b]: o } })),
      type: (b, n) => setPhone((p) => ({ ...p, typed: { ...p.typed, [b]: n } })),
    })
    tl.eventCallback('onComplete', next)
    tlRef.current = tl
    if (!visibleRef.current || document.hidden) tl.pause()
  }, [live, next])

  const pickTab = (i: number) => {
    if (i === active && live) return
    setActive(i)
    if (live) setRun((r) => r + 1)
    else {
      setPhone(filled(examples[i]))
      setPrompt(examples[i].prompt)
    }
  }

  return (
    <section className="demo wrap" id="examples" ref={rootRef}>
      <div className="demo-head reveal">
        <h2 className="h2">You ask once. It sends everything it needs to know.</h2>
        <p className="sub">Your agent puts every question in one form. You answer where you are, it carries on.</p>
      </div>
      <div className="tabs reveal" role="tablist" aria-label="Examples">
        {examples.map((e, i) => (
          <button
            key={e.id}
            type="button"
            role="tab"
            className="tab"
            aria-selected={i === active}
            data-tab={i}
            onClick={() => pickTab(i)}
          >
            <e.icon size={15} />
            {e.tab}
            <span className="tab-bar" />
          </button>
        ))}
      </div>
      <div className="stage reveal">
        <Terminal ex={ex} host={host} prompt={prompt} onTyped={onTyped} />
        <div className="phone-col">
          <span className="phone-shadow" />
          <Phone ref={stageRef} ex={ex} host={host} state={phone} width={PHONE_W} />
        </div>
      </div>
      <div className="stage-foot reveal">
        <CopyButton value={() => installLine + ex.prompt} label="Copy this prompt, with the install line" className="btn btn-ghost btn-sm" />
        <a href={ex.href}>
          Answer this form yourself <ArrowRight size={15} />
        </a>
        <a href="/examples">
          <BookOpen size={15} /> All examples
        </a>
      </div>
    </section>
  )
}

interface Hooks {
  pick: (block: number, option: number) => void
  type: (block: number, chars: number) => void
}

/** The agent's reply line by line, and the phone receiving and answering the form. */
function buildTimeline(ex: Example, stage: HTMLElement, root: HTMLElement, h: Hooks) {
  const q = (s: string) => stage.querySelector<HTMLElement>(`[data-ph="${s}"]`)!
  // On screen px per device px: the phone's own scale times any CSS one.
  const scale = stage.getBoundingClientRect().width / 390
  const scroller = q('scroll')
  const tap = q('tap')
  const lines = [...root.querySelectorAll<HTMLElement>('[data-line]')]
  const sRect = scroller.getBoundingClientRect()
  const viewH = DEVICE_H - VIEW_TOP
  const maxY = Math.max(0, scroller.offsetHeight - viewH)
  // Where an element sits in the (unscrolled) form, in device px.
  const box = (el: HTMLElement) => {
    const r = el.getBoundingClientRect()
    return { x: (r.left - sRect.left) / scale, y: (r.top - sRect.top) / scale, w: r.width / scale, h: r.height / scale }
  }
  let scrollY = 0

  const tl = gsap.timeline({ paused: false })
  let li = 0
  const show = (at?: gsap.Position) => {
    const el = lines[li++]
    if (el) tl.to(el, { autoAlpha: 1, y: 0, duration: 0.35, ease: 'power2.out' }, at)
  }
  const scrollTo = (el: HTMLElement) => {
    const b = box(el)
    const y = Math.round(Math.min(maxY, Math.max(0, b.y + b.h / 2 - viewH * 0.55)))
    if (Math.abs(y - scrollY) > 4) tl.to(scroller, { y: -y, duration: 0.7, ease: 'power2.inOut' })
    scrollY = y
  }
  const tapAt = (x: number, y: number, then?: () => void) => {
    tl.set(tap, { x, y, scale: 0.5, autoAlpha: 0 }, '+=0.25')
    tl.to(tap, { autoAlpha: 1, scale: 1, duration: 0.16, ease: 'power1.out' })
    if (then) tl.call(then)
    tl.to(tap, { autoAlpha: 0, scale: 1.6, duration: 0.32, ease: 'power1.in' }, '+=0.08')
  }
  const tapEl = (el: HTMLElement, then?: () => void) => {
    scrollTo(el)
    const b = box(el)
    tapAt(b.x + b.w / 2, VIEW_TOP + b.y + b.h / 2 - scrollY, then)
  }

  // ❯ prompt sent → the agent creates the form and sends the link.
  ex.lines.forEach((l) => {
    if (l.kind === 'tool') show('+=0.35')
    if (l.kind === 'say') show('+=0.55')
  })

  // The phone: the notification, the form, the taps.
  tl.addLabel('phone', '+=0.2')
  tl.to(q('notif'), { autoAlpha: 1, yPercent: 0, duration: 0.55, ease: 'back.out(1.5)' }, 'phone')
  show('phone+=0.45') // waiting / collecting
  tapAt(195, 100)
  tl.to(q('notif'), { yPercent: -160, duration: 0.35, ease: 'power2.in' })
  tl.set(q('notif'), { autoAlpha: 0 }) // or its edge and shadow peek in at the top
  tl.to(q('app'), { yPercent: 0, duration: 0.6, ease: 'power3.out' }, '<0.05')
  tl.to({}, { duration: 0.5 })

  // While you answer, the agent works on (its lines land during the taps).
  const work = ex.lines.filter((l) => l.kind === 'work').length
  for (let k = 0; k < work; k++) show(`phone+=${2.6 + k * 2.4}`)

  ex.form.blocks.forEach((b, i) => {
    const field = scroller.querySelector<HTMLElement>(`[data-block="${i}"]`)
    if ((b.kind === 'secret' || b.kind === 'text') && field) {
      const input = field.querySelector<HTMLElement>('.pf-input')!
      const n = b.kind === 'secret' ? b.chars : b.value.length
      const p = { n: 0 }
      tapEl(input)
      tl.to(p, { n, duration: n * 0.055, ease: 'none', onUpdate: () => h.type(i, Math.round(p.n)) })
    }
    if (b.kind === 'choice' && b.tap !== undefined) {
      const opt = scroller.querySelector<HTMLElement>(`[data-tap="${i}-${b.tap}"]`)
      if (opt) tapEl(opt, () => h.pick(i, b.tap!))
      tl.to({}, { duration: 0.35 })
    }
  })
  tapEl(scroller.querySelector<HTMLElement>('[data-tap="send"]')!)
  tl.to(q('sent'), { autoAlpha: 1, duration: 0.25 })
  tl.fromTo(q('sent').querySelector('.ph-sent-ic'), { scale: 0.4 }, { scale: 1, duration: 0.5, ease: 'back.out(2)' }, '<')

  // Back in the terminal: the answers arrive.
  show('+=0.5')
  tl.to({}, { duration: HOLD })

  const bar = root.querySelector(`[data-tab="${examples.indexOf(ex)}"] .tab-bar`)
  if (bar) tl.fromTo(bar, { scaleX: 0 }, { scaleX: 1, ease: 'none', duration: tl.duration() }, 0)
  return tl
}
