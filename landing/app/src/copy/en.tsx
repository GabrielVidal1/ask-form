// The English page (/): every word a visitor reads, plus the demo's examples.
// fr.tsx has the same shape (the Copy type below).
import type { ReactNode } from 'react'

import { examples } from '../examples/data'

export const en = {
  lang: 'en',
  /** This page's path, and where its demo forms live. */
  home: '/',
  examplesPath: '/examples',
  /** The prerender writes these into <head>. */
  head: {
    title: 'ask-form: your coding agent asks, you answer from anywhere',
    description:
      'ask-form: when your coding agent needs a decision, it sends you a form. You answer from any browser, phone included, alone or with your whole team, and it reads the answers as JSON.',
  },
  nav: { examples: 'Examples', install: 'Install', language: 'Language' },
  hero: {
    /** Split into words for the entrance; the second line is the coloured one. */
    line1: 'Your coding agent asks.',
    line2: 'You answer from anywhere.',
    lede: 'Your agent asks detailed questions in rich forms. Anyone answers, from anywhere.',
    tryNow: 'Try now',
    see: 'See it in action',
    agents: 'Works with any coding agent that can run a shell command.',
  },
  copy: { copy: 'Copy', copied: 'Copied' },
  demo: {
    title: 'You ask once. It sends everything it needs to know.',
    sub: 'Your agent puts every question in one form. You answer where you are, it carries on.',
    tabs: 'Examples',
    /** What the demo's copy button puts in front of an example's prompt. */
    installLine: (origin: string) => `Fetch ${origin}/agent-install and follow it to install ask-form and set it up, then: `,
    copyPrompt: 'Copy this prompt, with the install line',
    answer: 'Answer this form yourself',
    all: 'All examples',
  },
  phone: {
    title: 'An ask-form form on a phone',
    date: 'Tuesday 28 October',
    now: 'now',
    notif: 'Your agent has questions for you',
    send: 'Send answers',
    sent: 'Sent',
    secret: 'Kept out of the answers your agent reads.',
  },
  /** The words inside the examples' mockups (examples/mockups.tsx). */
  mock: {
    today: 'Today',
    habits: ['Read', 'Run', 'Water', 'Sleep'],
    week: 'This week',
    days: ['M', 'T', 'W', 'T', 'F', 'S', 'S'],
    streaks: 'Streaks',
    invoices: 'Invoices',
    scrolls: '← scrolls →',
    due: 'due',
    paid: 'paid',
    plan: 'Pro',
    month: { price: '€9', per: '/mo' },
    year: { price: '€90', per: '/yr' },
    monthly: 'Monthly',
    yearly: 'Yearly',
    free: '2 months free',
    subscribe: 'Subscribe',
    start: 'Get started',
    steps: ['Create a board', 'Invite your team', 'Add a first task'],
    tip: 'Tap + to add your first task',
    tipFoot: '1 of 4 · Next',
  },
  compare: {
    title: 'Why not just ask in the terminal?',
    sub: 'The same questions, answered somewhere better than a blinking prompt.',
    old: 'In the terminal',
    now: 'With ask-form',
    /** Where, who, what: App puts an icon on each, in this order. */
    rows: [
      { q: 'Where you answer', old: 'At the keyboard. Step away and the agent waits.', now: 'Any browser, phone included. The agent keeps working meanwhile.' },
      {
        q: 'Who answers',
        old: 'You, at that keyboard.',
        now: 'Anyone with the link: a team vote, a client sign-off, beta testers. Every answer comes back, with an email field to know who said what.',
      },
      { q: 'What you see', old: 'Text.', now: 'Mockups to pick from, diagrams, tables and code.' },
    ],
  },
  install: {
    title: 'Try now',
    sub: (
      <>
        Any coding agent running on your computer will do. It reads <a href="/agent-install">/agent-install</a>, installs
        ask-form, sets itself up and sends you a first form.
      </>
    ) as ReactNode,
    paste: 'Paste this into your coding agent',
    pasteNote:
      'Finish the sentence with what you want to be asked. Not a chat app such as claude.ai or ChatGPT: its sandbox can’t keep ask-form running.',
    /** The prompt the visitor copies and finishes. */
    prompt: (origin: string) =>
      `Fetch ${origin}/agent-install and follow it to install the ask-form skill and set it up, then ask me with it: `,
    placeholder: 'what you want to be asked',
    manual: 'Manual install',
    manualIntro: (
      <>
        Linux and macOS, x86-64 or arm64. One command installs the binary in <code>~/.local/bin</code>, connects your
        machine to this relay, starts the background service and adds the Claude Code skill.
      </>
    ) as ReactNode,
    local: 'Without the relay (forms answerable on this machine only):',
    goInstall: (
      <>
        With Go 1.22+: <code>go install github.com/GabrielVidal1/ask-form/cmd/ask-form@latest</code>, then{' '}
        <code>ask-form setup</code>
      </>
    ) as ReactNode,
    releases: (github: string): ReactNode => (
      <>
        Or take a binary from <a href={github + '/releases'}>GitHub Releases</a>, then run <code>ask-form setup</code>
      </>
    ),
    small: (
      <>
        The service installs new releases by itself (<code>ASK_FORM_AUTO_UPGRADE=notify</code> or <code>off</code> stops
        that). <code>ask-form uninstall</code> removes the service and the skill. Other agents than Claude Code:{' '}
        <code>ask-form skill print --body &gt;&gt; AGENTS.md</code>.
      </>
    ) as ReactNode,
  },
  privacy: {
    eyebrow: 'Private by design',
    title: 'Your forms never leave your machine.',
    /** App puts an icon on each, in this order. */
    facts: [
      { title: 'Forms stay on your machine', text: 'The relay only passes page requests through. It stores no form and no answer.' },
      {
        title: 'One private link per form',
        text: 'A 256-bit token. Answered once then closed, or open to your whole team until you close it. Every form expires.',
      },
      { title: 'Secrets go to .env', text: "A secret field's value never appears in the answers the agent reads." },
    ],
    how: 'Same 404 for an unknown form and a wrong token, no referrer, no cache, no indexing, a strict CSP, mockups in sandboxed script-free frames.',
    more: (github: string): ReactNode => (
      <>
        More in <a href={github + '/blob/main/docs/how-it-works.md'}>how it works</a>.
      </>
    ),
    selfHost: (github: string): ReactNode => (
      <>
        The relay is open source too: <a href={github + '/blob/main/docs/self-hosting.md'}>run your own</a> on your
        domain.
      </>
    ),
  },
  footer: { by: 'Made with ❤️ by', source: 'Source on GitHub', license: 'MIT license' },
  examples,
}

export type Copy = typeof en
