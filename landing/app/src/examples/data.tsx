// The examples the landing cycles through: what you type, what the agent says
// back, and the form it sends. Each one mirrors a real demo form under
// /examples/<name> (examples/<name>/spec.json), so "Answer it yourself" opens
// the same questions.
import type { ReactNode } from 'react'
import { Code, KeyRound, Sparkles, Users, type LucideIcon } from 'lucide-react'

import {
  HabitStreaks,
  HabitToday,
  HabitWeek,
  InvoiceCards,
  InvoiceTable,
  OnboardChecklist,
  OnboardTour,
  PricingOne,
  PricingToggle,
} from './mockups'

/** One line of the agent's reply in the terminal. `{link}` is the form's URL. */
export type Line =
  | { kind: 'tool'; text: string; out: string }
  | { kind: 'say'; text: string; link?: boolean }
  | { kind: 'wait'; text: string }
  /** Something the agent got on with while you answer. */
  | { kind: 'work'; text: string }
  | { kind: 'done'; text: string }

export interface Choice {
  label: string
  desc?: string
  mock?: () => ReactNode
}

/** What the phone shows, top to bottom. `tap` is the option the demo picks. */
export type Block =
  | { kind: 'choice'; label: string; options: Choice[]; selected: number; tap?: number; cards?: boolean }
  | { kind: 'finding'; title: string; where: string; code: string }
  | { kind: 'secret'; label: string; chars: number }
  /** A text field the demo types into; `optional` drops the red star. */
  | { kind: 'text'; label: string; value: string; optional?: boolean; help?: string }

export interface Example {
  id: string
  tab: string
  icon: LucideIcon
  cwd: string
  prompt: string
  lines: Line[]
  form: {
    slug: string
    eyebrow: string
    title: string
    desc: string
    page: string
    pages: number
    blocks: Block[]
    sent: string
  }
  /** The demo form at /examples/<name>. */
  href: string
}

export const examples: Example[] = [
  {
    id: 'new-project',
    tab: 'New project',
    icon: Sparkles,
    cwd: '~/habit-tracker',
    prompt:
      'I want a habit tracker for me and a few friends. Before you scaffold anything, ask me everything you need with ask-form, your picks pre-selected.',
    lines: [
      { kind: 'tool', text: 'ask-form create --title "Habit tracker: before I scaffold"', out: '11 questions · 3 pages · 3 mockups' },
      { kind: 'say', text: 'Everything I need is in one form, my picks already selected:', link: true },
      { kind: 'wait', text: 'Waiting for your answers in the background…' },
      { kind: 'work', text: 'Wrote README.md: what it does, who it is for' },
      { kind: 'work', text: 'Drafted the data model: habits, check-ins, friends' },
      { kind: 'done', text: 'Answers in: Vite + React, SQLite, magic links, the week grid. Scaffolding now.' },
    ],
    form: {
      slug: 'habit-tracker_k3f9q2',
      eyebrow: 'A few questions · open for 24 h',
      title: 'Habit tracker: before I scaffold',
      desc: 'The repo is empty, so a few choices first. I pre-selected what fits what you told me.',
      page: 'Look & stack',
      pages: 3,
      blocks: [
        {
          kind: 'choice',
          label: 'Home screen',
          cards: true,
          selected: 0,
          tap: 1,
          options: [
            { label: 'Today list', mock: HabitToday },
            { label: 'Week grid', mock: HabitWeek },
            { label: 'Streak cards', mock: HabitStreaks },
          ],
        },
        {
          kind: 'choice',
          label: 'Front end',
          selected: 0,
          options: [
            { label: 'Vite + React', desc: 'A static SPA: cheapest to host' },
            { label: 'Next.js', desc: 'Server rendering and API routes' },
            { label: 'SvelteKit' },
          ],
        },
      ],
      sent: 'Your agent has your answers.',
    },
    href: '/examples/new-project',
  },
  {
    id: 'review-findings',
    tab: 'Code review',
    icon: Code,
    cwd: '~/billing',
    prompt: 'Review this branch against main. Send me the findings with ask-form, worst first, and fix the ones I pick.',
    lines: [
      { kind: 'tool', text: 'ask-form create --title "Review of feat/invoices: 6 findings"', out: '6 findings, worst first · code and mockups' },
      { kind: 'say', text: 'Six findings, worst first, my call on each pre-selected:', link: true },
      { kind: 'wait', text: 'Waiting for your picks…' },
      { kind: 'work', text: 'Wrote a failing test for finding 1' },
      { kind: 'work', text: 'Checked the other 31 endpoints: none has the same hole' },
      { kind: 'done', text: '5 fixes, 1 issue for later, invoices as cards on phones. Fixing, then pushing.' },
    ],
    form: {
      slug: 'review-feat-invoices_p81xq0',
      eyebrow: 'A few questions · open for 24 h',
      title: 'Review of feat/invoices',
      desc: '14 files changed (+612 −88). Six findings, worst first.',
      page: 'Must fix',
      pages: 3,
      blocks: [
        {
          kind: 'finding',
          title: '1 · Anyone can read any invoice',
          where: 'api/invoices/[id].ts:18',
          code: 'const invoice = await db.invoice\n  .findUnique({ where: { id } })',
        },
        {
          kind: 'choice',
          label: 'What should I do?',
          selected: 0,
          options: [
            { label: 'Fix it', desc: "Scope the query to the caller's account" },
            { label: 'Open an issue for later' },
          ],
        },
        {
          kind: 'choice',
          label: '5 · The invoice table on a phone',
          cards: true,
          selected: 0,
          tap: 1,
          options: [
            { label: 'Scrolls sideways (now)', mock: InvoiceTable },
            { label: 'Stacked cards', mock: InvoiceCards },
          ],
        },
      ],
      sent: 'Your agent has your picks.',
    },
    href: '/examples/review-findings',
  },
  {
    id: 'api-keys',
    tab: 'API keys',
    icon: KeyRound,
    cwd: '~/shop',
    prompt: 'Add Stripe checkout and Sentry. Ask me for the keys with ask-form, never in the chat.',
    lines: [
      { kind: 'tool', text: 'ask-form create --title "Stripe + Sentry: keys and settings"', out: '10 questions · 2 secret fields' },
      { kind: 'say', text: 'The billing code is ready. Your keys go straight into .env.local:', link: true },
      { kind: 'wait', text: 'Waiting for the keys…' },
      { kind: 'work', text: 'Added the webhook handler and its tests' },
      { kind: 'done', text: 'Keys written to .env.local, never shown to me. Yearly toggle, €9/mo. Wiring checkout.' },
    ],
    form: {
      slug: 'stripe-sentry_7cw2ma',
      eyebrow: 'A few questions · open for 24 h',
      title: 'Stripe + Sentry: keys',
      desc: 'Secret fields never reach me: they go straight into .env.local.',
      page: 'Stripe',
      pages: 2,
      blocks: [
        { kind: 'secret', label: 'Stripe secret key', chars: 22 },
        {
          kind: 'choice',
          label: 'Pricing page',
          cards: true,
          selected: 0,
          tap: 1,
          options: [
            { label: 'One plan', mock: PricingOne },
            { label: 'Monthly / yearly', mock: PricingToggle },
          ],
        },
      ],
      sent: 'Your keys went to .env.local.',
    },
    href: '/examples/api-keys',
  },
  {
    id: 'team-vote',
    tab: 'Team vote',
    icon: Users,
    cwd: '~/tasks-app',
    prompt:
      "Send both onboarding mockups to the team with ask-form. Collect everyone's vote until Friday, then build the winner.",
    lines: [
      { kind: 'tool', text: 'ask-form create --title "Onboarding: which one?" --multi --ttl 72h', out: 'one link for everyone · closes Fri 18:00' },
      { kind: 'say', text: 'One link for the whole team, post it in #product:', link: true },
      { kind: 'wait', text: 'Waiting for the vote to close on Friday…' },
      { kind: 'work', text: 'Meanwhile: the parts both versions share' },
      { kind: 'done', text: 'Closed with 5 votes: 4 × Checklist, 1 × Tour. Building the checklist.' },
    ],
    form: {
      slug: 'onboarding-vote_x2m7rd',
      eyebrow: 'Team vote · open until Friday',
      title: 'Onboarding: which one?',
      desc: 'Two takes on the first screen new users see. Pick the one you would ship.',
      page: 'Your vote',
      pages: 1,
      blocks: [
        { kind: 'text', label: 'Your name', value: 'Léa' },
        { kind: 'text', label: 'Email', value: 'lea@acme.dev', optional: true, help: 'Optional: so your agent knows who voted.' },
        {
          kind: 'choice',
          label: 'Which onboarding?',
          cards: true,
          selected: -1,
          tap: 0,
          options: [
            { label: 'Checklist', mock: OnboardChecklist },
            { label: 'Guided tour', mock: OnboardTour },
          ],
        },
      ],
      sent: 'Thanks, Léa. 4 people have answered so far.',
    },
    href: '/examples/team-vote',
  },
]
