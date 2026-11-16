// The English page (/): every word a visitor reads, plus the demo's examples.
// fr.tsx has the same shape (the Copy type below).
import type { ReactNode } from 'react'

import { examples } from '../examples/data'

/** One step of a chat app's tab in the "Add it to your chat" panel; code is a copyable block under it. */
export interface ChatStep {
  text: ReactNode
  code?: string
}
/** A tab of that panel: one chat app (or a family of them). */
export interface ChatApp {
  id: string
  name: string
  steps: ChatStep[]
}

/** The connector's address on this relay. */
export const connector = (origin: string) => `${origin}/mcp`
/** The plugin zip this relay serves (Claude's "Upload plugin", ChatGPT's plugin upload). */
export const pluginZip = (origin: string) => `${origin}/plugin/ask-form.zip`
/** The repository Claude adds as a plugin marketplace. */
export const MARKETPLACE = 'GabrielVidal1/ask-form'

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
    chat: 'Add to your chat',
    see: 'See it in action',
    agents: 'Works with any coding agent that can run a shell command, and in chat apps through a connector.',
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
    pasteNote: 'Finish the sentence with what you want to be asked. In a chat app (claude.ai, ChatGPT…), add the connector below instead.',
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
  /** The "Add it to your chat" panel of the install section (#chat; #claude-ai still lands there). */
  chat: {
    title: 'Add it to your chat',
    intro:
      'Claude, ChatGPT and other chat apps reach ask-form through a connector: one URL, a one-click sign-in, nothing to install. Then ask for something that needs your input, and the form shows up as a card in the chat.',
    url: 'Connector URL',
    apps: 'Chat apps',
    signin: 'The sign-in page needs no account: it only asks where your forms live (see below).',
    tabs: (origin: string): ChatApp[] => [
      {
        id: 'claude',
        name: 'Claude',
        steps: [
          {
            text: (
              <>
                In <b>Customize → Connectors</b>, choose <b>Add custom connector</b> and paste the URL. On a Team or
                Enterprise plan, an Owner adds it once in <b>Organization settings → Connectors</b>, then everyone
                connects.
              </>
            ),
          },
          { text: <>Click <b>Connect</b> and sign in. It works on the web, in the desktop app and on your phone.</> },
          {
            text: (
              <>
                Or take the plugin, the connector plus a skill that teaches Claude to write good forms:{' '}
                <b>Customize → Plugins → Add marketplace</b> with <code>{MARKETPLACE}</code>, or <b>Upload plugin</b>{' '}
                with <a href={pluginZip(origin)}>the plugin zip</a>.
              </>
            ),
          },
        ],
      },
      {
        id: 'chatgpt',
        name: 'ChatGPT',
        steps: [
          {
            text: (
              <>
                In <b>Plugins</b>, press <b>+</b>, then <b>Add custom MCP server</b>, and paste the URL. On paid plans;
                on Business and Enterprise, a workspace admin adds it.
              </>
            ),
          },
          { text: <>Sign in, then type <b>@</b> in a chat and pick ask-form.</> },
          {
            text: (
              <>
                Or upload <a href={pluginZip(origin)}>the plugin zip</a>: the same connector, with a skill for writing good
                forms.
              </>
            ),
          },
        ],
      },
      {
        id: 'claude-code',
        name: 'Claude Code',
        steps: [
          { text: 'Add the connector from a terminal:', code: `claude mcp add --transport http ask-form ${connector(origin)}` },
          { text: <>In Claude Code, run <code>/mcp</code> and sign in to ask-form.</> },
          {
            text: (
              <>
                Or the plugin: <code>/plugin marketplace add {MARKETPLACE}</code>, then{' '}
                <code>/plugin install ask-form@ask-form</code>. On your own computer, the install above works too, with no
                sign-in.
              </>
            ),
          },
        ],
      },
      {
        id: 'editors',
        name: 'Cursor / VS Code',
        steps: [
          {
            text: (
              <>
                Cursor: <b>Settings → MCP</b>, or <code>~/.cursor/mcp.json</code>:
              </>
            ),
            code: `{\n  "mcpServers": {\n    "ask-form": { "url": "${connector(origin)}" }\n  }\n}`,
          },
          {
            text: (
              <>
                VS Code: <b>MCP: Add Server → HTTP</b> and paste the URL, or <code>.vscode/mcp.json</code>:
              </>
            ),
            code: `{\n  "servers": {\n    "ask-form": { "type": "http", "url": "${connector(origin)}" }\n  }\n}`,
          },
          { text: 'Sign in when the editor asks.' },
        ],
      },
      {
        id: 'others',
        name: 'Le Chat & others',
        steps: [
          {
            text: (
              <>
                Le Chat: <b>Connectors</b>, then add a <b>custom MCP connector</b> with the URL.
              </>
            ),
          },
          {
            text: 'Any MCP client that takes a remote server with OAuth sign-in (Goose, Windsurf, Zed, Gemini CLI…): add an HTTP server with the URL.',
          },
        ],
      },
    ],
  },
  /** Under the chat panel: the two places a chat's forms can live. */
  where: {
    title: 'Where your forms live',
    sub: 'You choose on the sign-in page. To change, connect again.',
    cloud: {
      title: (host: string) => `On ${host}`,
      points: (host: string): ReactNode[] => [
        'An anonymous account: no sign-up, no email.',
        `A form and its answers are deleted from ${host} 24 hours after it closes.`,
        'Only the holder of its private link opens it.',
      ],
    },
    computer: {
      title: 'On your computer',
      points: (host: string): ReactNode[] => [
        <>
          Run <code>ask-form link CODE</code> on a machine where ask-form runs (the install above).
        </>,
        `Forms and answers stay on it, never stored on ${host}.`,
        'The computer must be on when the chat asks and reads.',
      ],
    },
  },
  /** The second demo: the same idea in a chat, with the connector's card. */
  chatDemo: {
    title: 'In a chat, the form shows up as a card.',
    sub: 'Answer in the chat or on your phone, then hand the answers back in one tap.',
    window: 'A chat using the ask-form connector',
    bar: 'New chat',
    user: 'Plan a weekend in Lisbon for the two of us, mid-March.',
    intro: 'Happy to. A few choices first, all in one form:',
    tool: 'ask-form · create_form',
    card: {
      title: 'Lisbon weekend',
      meta: '4 questions · open until Sunday 18:00',
      waiting: 'Waiting for your answers',
      answered: 'Answered',
      open: 'Open form',
      cont: 'Continue with my answers',
      hint: 'The link works on any device, your phone included.',
    },
    sent: 'I answered the form “Lisbon weekend”.',
    reply:
      'Thanks! Two nights in Alfama, the 28 tram on Saturday morning, and a table for fado that evening. Here is the plan, day by day…',
    composer: 'Reply…',
  },
  privacy: {
    eyebrow: 'Private by design',
    title: 'Your forms stay where you choose.',
    /** App puts an icon on each, in this order. */
    facts: [
      {
        title: 'Forms stay on your machine',
        text: 'The relay only passes page requests through. It stores no form and no answer, except a chat’s forms you chose to keep on it, deleted 24 hours after they close.',
      },
      {
        title: 'One private link per form',
        text: 'A 256-bit token. Answered once then closed, or open to your whole team until you close it. Every form expires.',
      },
      { title: 'Secrets go to .env', text: "A secret field's value never appears in the answers the agent reads." },
    ],
    how: 'Same 404 for an unknown form and a wrong token, no referrer, no cache, no indexing, a strict CSP, mockups in sandboxed script-free frames. The relay counts usage (sign-ins, forms answered, downloads), never what a form says, its answers or who you are.',
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
