import { useEffect, useRef } from 'react'
import { gsap } from 'gsap'
import { ScrollTrigger } from 'gsap/ScrollTrigger'
import {
  ArrowDown,
  ChevronRight,
  Clock,
  Code,
  Github,
  Image,
  ListChecks,
  MapPin,
  Rocket,
  Server,
  ShieldCheck,
  Smartphone,
  Terminal as TerminalIcon,
  Users,
} from 'lucide-react'

import { copies, type Lang } from './copy'
import { CopyProvider } from './copy/context'
import { CopyButton } from './components/CopyButton'
import { Demo } from './components/Demo'
import type { Origin } from './lib-origin'

const GITHUB = 'https://github.com/GabrielVidal1/ask-form'
// The repo links stay off the page for now: flip this to bring them back.
const SHOW_GITHUB = false

// The icons of the copy's compare rows and privacy facts, in their order.
const compareIcons = [MapPin, Users, Image]
const factIcons = [Server, Clock, ShieldCheck]

// The language switch: plain links, English stays the default for everyone.
const langs: { lang: Lang; href: string; short: string; name: string }[] = [
  { lang: 'en', href: '/', short: 'EN', name: 'English' },
  { lang: 'fr', href: '/fr', short: 'FR', name: 'Français' },
]

export default function App({ origin, lang }: { origin: Origin; lang: Lang }) {
  return (
    <CopyProvider value={copies[lang]}>
      <Page origin={origin} lang={lang} />
    </CopyProvider>
  )
}

function Page({ origin, lang }: { origin: Origin; lang: Lang }) {
  const t = copies[lang]
  const installLine = t.demo.installLine(origin.origin)
  const promptLine = t.install.prompt(origin.origin)
  const heroRef = useRef<HTMLElement>(null)

  useEffect(() => {
    if (!document.documentElement.classList.contains('live')) return
    gsap.registerPlugin(ScrollTrigger)
    const ctx = gsap.context(() => {
      // Hero: the headline word by word, then the rest.
      const hero = gsap.timeline({ defaults: { ease: 'power3.out' } })
      hero
        .from('.hero .w', { yPercent: 60, autoAlpha: 0, duration: 0.7, stagger: 0.06 })
        .from('.hero .lede, .hero .cta, .hero .agents', { y: 16, autoAlpha: 0, duration: 0.6, stagger: 0.1 }, '-=0.35')
        .from('.glow', { scale: 0.6, autoAlpha: 0, duration: 1.4, ease: 'power2.out' }, 0)
      gsap.to('.glow', { xPercent: 6, yPercent: 8, duration: 7, ease: 'sine.inOut', yoyo: true, repeat: -1 })
      gsap.set('.reveal', { autoAlpha: 1 })
      // Sections as they scroll in.
      ScrollTrigger.batch('.reveal', {
        start: 'top 88%',
        once: true,
        onEnter: (els) => gsap.from(els, { y: 28, autoAlpha: 0, duration: 0.7, ease: 'power3.out', stagger: 0.1 }),
      })
      ScrollTrigger.batch('.vs-row, .fact', {
        start: 'top 92%',
        once: true,
        onEnter: (els) => gsap.from(els, { x: -18, autoAlpha: 0, duration: 0.5, ease: 'power2.out', stagger: 0.07 }),
      })
    })
    return () => ctx.revert()
  }, [])

  return (
    <>
      <div className="wrap">
        <nav className="nav">
          <a className="brand" href={t.home}>
            <span className="logo">
              <ListChecks size={17} strokeWidth={2.6} />
            </span>
            ask-form
          </a>
          <span className="nav-links">
            <a href="#examples" className="hide-sm">
              {t.nav.examples}
            </a>
            <a href="#install" className="hide-sm">
              {t.nav.install}
            </a>
            {SHOW_GITHUB && (
              <a href={GITHUB}>
                <Github size={16} /> GitHub
              </a>
            )}
            <span className="langs" role="group" aria-label={t.nav.language}>
              {langs.map((l, i) => (
                <span key={l.lang}>
                  {i > 0 && <span aria-hidden> · </span>}
                  <a
                    href={l.href}
                    hrefLang={l.lang}
                    lang={l.lang}
                    title={l.name}
                    aria-label={l.name}
                    aria-current={l.lang === lang ? 'page' : undefined}
                  >
                    {l.short}
                  </a>
                </span>
              ))}
            </span>
          </span>
        </nav>
      </div>

      <header className="hero wrap" ref={heroRef}>
        <span className="glow" aria-hidden />
        <h1>
          {t.hero.line1.split(' ').map((w, i) => (
            <span key={i} className="w">
              {w}&nbsp;
            </span>
          ))}
          <br />
          {t.hero.line2.split(' ').map((w, i, all) => (
            <span key={i} className="w">
              <em>{w}</em>
              {i < all.length - 1 && ' '}
            </span>
          ))}
        </h1>
        <p className="lede">{t.hero.lede}</p>
        <p className="cta">
          <a className="btn btn-primary" href="#install">
            <Rocket size={17} /> {t.hero.tryNow}
          </a>
          <a className="btn btn-ghost" href="#examples">
            {t.hero.see} <ArrowDown size={17} />
          </a>
        </p>
        <p className="agents">
          <TerminalIcon size={15} /> {t.hero.agents}
        </p>
      </header>

      <Demo host={origin.host} installLine={installLine} />

      <section className="section wrap">
        <div className="reveal">
          <h2 className="h2">{t.compare.title}</h2>
          <p className="sub">{t.compare.sub}</p>
        </div>
        <div className="vs panel" role="table">
          <div className="vs-row vs-head" role="row">
            <span role="columnheader" />
            <span role="columnheader">{t.compare.old}</span>
            <span role="columnheader">{t.compare.now}</span>
          </div>
          {t.compare.rows.map((c, i) => {
            const Icon = compareIcons[i]
            return (
              <div key={c.q} className="vs-row" role="row">
                <span className="vs-q" role="rowheader">
                  <Icon size={17} />
                  <span>{c.q}</span>
                </span>
                <span className="vs-old" role="cell">
                  {c.old}
                </span>
                <span className="vs-new" role="cell">
                  {c.now}
                </span>
              </div>
            )
          })}
        </div>
      </section>

      <section className="section wrap" id="install">
        <div className="reveal">
          <h2 className="h2">{t.install.title}</h2>
          <p className="sub">{t.install.sub}</p>
        </div>
        <div className="install">
          <div className="panel use-now reveal">
            <h3>
              <Smartphone size={20} /> {t.install.paste}
            </h3>
            <p>{t.install.pasteNote}</p>
            <div className="copy">
              <pre>
                {promptLine}
                <span className="ph" aria-hidden>
                  {t.install.placeholder}
                </span>
              </pre>
              <CopyButton value={promptLine} label={t.copy.copy} className="copy-btn" />
            </div>
          </div>
          <details className="panel more reveal">
            <summary>
              <ChevronRight size={16} className="chev" /> {t.install.manual}
            </summary>
            <div className="body">
              <p style={{ margin: '0 0 12px' }}>{t.install.manualIntro}</p>
              <div className="copy">
                <pre className="mono">curl -fsSL {origin.origin}/install.sh | sh</pre>
                <CopyButton value={`curl -fsSL ${origin.origin}/install.sh | sh`} label={t.copy.copy} className="copy-btn" />
              </div>
              <ul>
                <li>
                  {t.install.local} <code>curl -fsSL {origin.origin}/install.sh | sh -s -- --local</code>
                </li>
                {SHOW_GITHUB && (
                  <>
                    <li>{t.install.goInstall}</li>
                    <li>{t.install.releases(GITHUB)}</li>
                  </>
                )}
              </ul>
              <p className="small">{t.install.small}</p>
            </div>
          </details>
        </div>
      </section>

      <section className="section wrap">
        <div className="reveal">
          <p className="eyebrow">{t.privacy.eyebrow}</p>
          <h2 className="h2">{t.privacy.title}</h2>
        </div>
        <ul className="facts">
          {t.privacy.facts.map((f, i) => {
            const Icon = factIcons[i]
            return (
              <li key={f.title} className="panel fact">
                <span className="fact-ic">
                  <Icon size={20} />
                </span>
                <b>{f.title}</b>
                <span>{f.text}</span>
              </li>
            )
          })}
        </ul>
        <p className="how reveal">
          {t.privacy.how}
          {SHOW_GITHUB && <> {t.privacy.more(GITHUB)}</>} {t.privacy.selfHost(GITHUB)}
        </p>
      </section>

      <footer className="wrap">
        <span className="by">
          {t.footer.by} <a href="https://gabvdl.xyz">Gabriel</a>
        </span>
        {SHOW_GITHUB && (
          <>
            <span aria-hidden>·</span>
            <a href={GITHUB}>
              <Code size={14} /> {t.footer.source}
            </a>
          </>
        )}
        <span aria-hidden>·</span>
        <span>{t.footer.license}</span>
      </footer>
    </>
  )
}
