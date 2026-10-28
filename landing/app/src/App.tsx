import { useEffect, useRef } from 'react'
import { gsap } from 'gsap'
import { ScrollTrigger } from 'gsap/ScrollTrigger'
import { CopyButton } from '@gabvdl/ui'
import {
  ArrowDown,
  ChevronRight,
  Clock,
  Code,
  FileJson,
  Github,
  Image,
  KeyRound,
  ListChecks,
  MapPin,
  Rocket,
  Server,
  ShieldCheck,
  Smartphone,
  Terminal as TerminalIcon,
  Users,
} from 'lucide-react'

import { Demo } from './components/Demo'
import type { Origin } from './lib-origin'

const GITHUB = 'https://github.com/GabrielVidal1/ask-form'

const compare = [
  { icon: MapPin, q: 'Where you answer', old: 'At the keyboard. Step away and the agent waits.', now: 'Any browser, phone included. The agent keeps working meanwhile.' },
  { icon: ListChecks, q: 'How many questions', old: 'A few at a time, turn after turn.', now: 'Twenty at once, in pages, with its picks pre-selected.' },
  {
    icon: Users,
    q: 'Who answers',
    old: 'You, at that keyboard.',
    now: 'Anyone with the link: a team vote, a client sign-off, beta testers. Every answer comes back, with an email field to know who said what.',
  },
  { icon: Image, q: 'What you see', old: 'Text.', now: 'Mockups to pick from, diagrams, tables and code.' },
  { icon: FileJson, q: 'What it gets back', old: '“1b, yes, the second one”, to interpret.', now: 'JSON, keyed by question.' },
  { icon: KeyRound, q: 'Secrets', old: 'Pasted into the chat and kept in the transcript.', now: 'Written straight to .env. The model never sees them.' },
]

const facts = [
  { icon: Server, title: 'Forms stay on your machine', text: 'The relay only passes page requests through. It stores no form and no answer.' },
  {
    icon: Clock,
    title: 'One private link per form',
    text: 'A 256-bit token. Answered once then closed, or open to your whole team until you close it. Every form expires.',
  },
  { icon: ShieldCheck, title: 'Secrets go to .env', text: "A secret field's value never appears in the answers the agent reads." },
]

export default function App({ origin }: { origin: Origin }) {
  const installLine = `Fetch ${origin.origin}/agent-install and follow it to install ask-form and set it up, then: `
  const promptLine = `Fetch ${origin.origin}/agent-install and follow it to install the ask-form skill and set it up, then ask me with it: `
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
          <a className="brand" href="/">
            <span className="logo">
              <ListChecks size={17} strokeWidth={2.6} />
            </span>
            ask-form
          </a>
          <span className="nav-links">
            <a href="#examples" className="hide-sm">
              Examples
            </a>
            <a href="#install" className="hide-sm">
              Install
            </a>
            <a href={GITHUB}>
              <Github size={16} /> GitHub
            </a>
          </span>
        </nav>
      </div>

      <header className="hero wrap" ref={heroRef}>
        <span className="glow" aria-hidden />
        <h1>
          {'Your coding agent asks.'.split(' ').map((w, i) => (
            <span key={i} className="w">
              {w}&nbsp;
            </span>
          ))}
          <br />
          {'You answer from anywhere.'.split(' ').map((w, i) => (
            <span key={i} className="w">
              <em>{w}</em>
              {i < 3 && ' '}
            </span>
          ))}
        </h1>
        <p className="lede">
          When your agent needs your call (a stack, a layout, an API key), it sends you a link instead of waiting at the
          terminal. You answer from your phone, alone or with your team. It reads the answers as JSON and gets back to work.
        </p>
        <p className="cta">
          <a className="btn btn-primary" href="#install">
            <Rocket size={17} /> Try it on your setup
          </a>
          <a className="btn btn-ghost" href="#examples">
            See it in action <ArrowDown size={17} />
          </a>
        </p>
        <p className="agents">
          <TerminalIcon size={15} /> Works with any coding agent that can run a shell command.
        </p>
      </header>

      <Demo host={origin.host} installLine={installLine} />

      <section className="section wrap">
        <div className="reveal">
          <p className="eyebrow">Why not just ask in the terminal?</p>
          <h2 className="h2">Your agent can already ask. Here’s what a form changes.</h2>
          <p className="sub">The same questions, answered somewhere better than a blinking prompt.</p>
        </div>
        <div className="vs panel" role="table">
          <div className="vs-row vs-head" role="row">
            <span role="columnheader" />
            <span role="columnheader">In the terminal</span>
            <span role="columnheader">With ask-form</span>
          </div>
          {compare.map((c) => (
            <div key={c.q} className="vs-row" role="row">
              <span className="vs-q" role="rowheader">
                <c.icon size={17} />
                <span>
                  {c.q}
                </span>
              </span>
              <span className="vs-old" role="cell">
                {c.old}
              </span>
              <span className="vs-new" role="cell">
                {c.now}
              </span>
            </div>
          ))}
        </div>
      </section>

      <section className="section wrap" id="install">
        <div className="reveal">
          <p className="eyebrow">Try it on your setup</p>
          <h2 className="h2">One sentence to your agent.</h2>
          <p className="sub">
            Any coding agent running on your computer will do. It reads <a href="/agent-install">/agent-install</a>, installs
            ask-form, sets itself up and sends you a first form.
          </p>
        </div>
        <div className="install">
          <div className="panel use-now reveal">
            <h3>
              <Smartphone size={20} /> Paste this into your coding agent
            </h3>
            <p>Finish the sentence with what you want to be asked. Not a chat app such as claude.ai or ChatGPT: its sandbox can’t keep ask-form running.</p>
            <div className="copy">
              <pre>
                {promptLine}
                <span className="ph" aria-hidden>
                  what you want to be asked
                </span>
              </pre>
              <CopyButton value={promptLine} label="Copy" className="copy-btn" />
            </div>
          </div>
          <details className="panel more reveal">
            <summary>
              <ChevronRight size={16} className="chev" /> Manual install
            </summary>
            <div className="body">
              <p style={{ margin: '0 0 12px' }}>
                Linux and macOS, x86-64 or arm64. One command installs the binary in <code>~/.local/bin</code>, connects your
                machine to this relay, starts the background service and adds the Claude Code skill.
              </p>
              <div className="copy">
                <pre className="mono">curl -fsSL {origin.origin}/install.sh | sh</pre>
                <CopyButton value={`curl -fsSL ${origin.origin}/install.sh | sh`} label="Copy" className="copy-btn" />
              </div>
              <ul>
                <li>
                  Without the relay (forms answerable on this machine only): <code>curl -fsSL {origin.origin}/install.sh | sh -s -- --local</code>
                </li>
                <li>
                  With Go 1.22+: <code>go install github.com/GabrielVidal1/ask-form/cmd/ask-form@latest</code>, then <code>ask-form setup</code>
                </li>
                <li>
                  Or take a binary from <a href={GITHUB + '/releases'}>GitHub Releases</a>, then run <code>ask-form setup</code>
                </li>
              </ul>
              <p className="small">
                The service installs new releases by itself (<code>ASK_FORM_AUTO_UPGRADE=notify</code> or <code>off</code> stops
                that). <code>ask-form uninstall</code> removes the service and the skill. Other agents than Claude Code:{' '}
                <code>ask-form skill print --body &gt;&gt; AGENTS.md</code>.
              </p>
            </div>
          </details>
        </div>
      </section>

      <section className="section wrap">
        <div className="reveal">
          <p className="eyebrow">Private by design</p>
          <h2 className="h2">Your forms never leave your machine.</h2>
        </div>
        <ul className="facts">
          {facts.map((f) => (
            <li key={f.title} className="panel fact">
              <span className="fact-ic">
                <f.icon size={20} />
              </span>
              <b>{f.title}</b>
              <span>{f.text}</span>
            </li>
          ))}
        </ul>
        <p className="how reveal">
          Same 404 for an unknown form and a wrong token, no referrer, no cache, no indexing, a strict CSP, mockups in
          sandboxed script-free frames. More in <a href={GITHUB + '/blob/main/docs/how-it-works.md'}>how it works</a>.
        </p>
      </section>

      <footer className="wrap">
        <span className="by">
          Made with ❤️ by <a href="https://gabvdl.xyz">Gabriel</a>
        </span>
        <span>
          <a href={GITHUB}>
            <Code size={14} /> Source on GitHub
          </a>{' '}
          · MIT
        </span>
      </footer>
    </>
  )
}
