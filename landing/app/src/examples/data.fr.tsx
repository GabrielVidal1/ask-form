// The French page's examples: data.tsx's list, same order and shape, in
// French. Each one mirrors examples/<name>/spec.fr.json, the form
// /fr/examples/<name> serves. Commands, flags, paths and slugs stay as they are.
import { Code, KeyRound, Sparkles, Users } from 'lucide-react'

import type { Example } from './data'
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

export const examples: Example[] = [
  {
    id: 'new-project',
    tab: 'Nouveau projet',
    icon: Sparkles,
    cwd: '~/habit-tracker',
    prompt:
      "Je veux une appli de suivi d'habitudes pour moi et quelques amis. Avant de générer quoi que ce soit, posez-moi toutes vos questions avec ask-form, vos choix présélectionnés.",
    lines: [
      { kind: 'tool', text: `ask-form create --title "Suivi d'habitudes : avant de générer le projet"`, out: '11 questions · 3 pages · 3 maquettes' },
      { kind: 'say', text: "Tout ce qu'il me faut tient dans un seul formulaire, mes choix déjà cochés :", link: true },
      { kind: 'wait', text: "J'attends vos réponses en arrière-plan…" },
      { kind: 'work', text: "README.md écrit : ce que fait l'appli, pour qui" },
      { kind: 'work', text: 'Modèle de données esquissé : habitudes, validations, amis' },
      { kind: 'done', text: 'Réponses reçues : Vite + React, SQLite, liens magiques, la grille de la semaine. Je génère le projet.' },
    ],
    form: {
      slug: 'habit-tracker_k3f9q2',
      title: "Suivi d'habitudes : avant de générer le projet",
      desc: "Le dépôt est vide, donc quelques choix d'abord. J'ai présélectionné ce qui correspond à ce que vous m'avez dit.",
      page: 'Interface et stack',
      pages: 3,
      blocks: [
        {
          kind: 'choice',
          label: "Écran d'accueil",
          cards: true,
          selected: 0,
          tap: 1,
          options: [
            { label: 'Liste du jour', mock: HabitToday },
            { label: 'Grille de la semaine', mock: HabitWeek },
            { label: 'Cartes de séries', mock: HabitStreaks },
          ],
        },
        {
          kind: 'choice',
          label: 'Front-end',
          selected: 0,
          options: [
            { label: 'Vite + React', desc: 'Une SPA statique : la moins chère à héberger' },
            { label: 'Next.js', desc: "Rendu serveur et routes d'API" },
            { label: 'SvelteKit' },
          ],
        },
      ],
      sent: 'Votre agent a vos réponses.',
    },
    href: '/fr/examples/new-project',
  },
  {
    id: 'review-findings',
    tab: 'Revue de code',
    icon: Code,
    cwd: '~/billing',
    prompt:
      "Faites la revue de cette branche par rapport à main. Envoyez-moi les constats avec ask-form, les plus graves d'abord, et corrigez ceux que je choisis.",
    lines: [
      { kind: 'tool', text: 'ask-form create --title "Revue de feat/invoices : 6 constats"', out: "6 constats, les plus graves d'abord · code et maquettes" },
      { kind: 'say', text: "Six constats, les plus graves d'abord, mon avis sur chacun présélectionné :", link: true },
      { kind: 'wait', text: "J'attends vos choix…" },
      { kind: 'work', text: 'Test du constat 1 écrit : il échoue bien' },
      { kind: 'work', text: "Les 31 autres endpoints vérifiés : aucun n'a la même faille" },
      { kind: 'done', text: '5 corrections, 1 issue pour plus tard, les factures en cartes sur téléphone. Je corrige, puis je pousse.' },
    ],
    form: {
      slug: 'review-feat-invoices_p81xq0',
      title: 'Revue de feat/invoices',
      desc: "14 fichiers modifiés (+612 −88). Six constats, les plus graves d'abord.",
      page: 'À corriger absolument',
      pages: 3,
      blocks: [
        {
          kind: 'finding',
          title: "1 · N'importe qui peut lire n'importe quelle facture",
          where: 'api/invoices/[id].ts:18',
          text: "ne vérifie jamais que la facture appartient à l'appelant.",
          code: 'const invoice = await db.invoice\n  .findUnique({ where: { id } })',
        },
        {
          kind: 'choice',
          label: 'Que dois-je faire ?',
          selected: 0,
          options: [
            { label: 'Le corriger', desc: "Limiter la requête au compte de l'appelant" },
            { label: 'Ouvrir une issue pour plus tard' },
          ],
        },
        {
          kind: 'choice',
          label: '5 · Le tableau des factures sur téléphone',
          cards: true,
          selected: 0,
          tap: 1,
          options: [
            { label: 'Défile sur le côté (actuel)', mock: InvoiceTable },
            { label: 'Cartes empilées', mock: InvoiceCards },
          ],
        },
      ],
      sent: 'Votre agent a vos choix.',
    },
    href: '/fr/examples/review-findings',
  },
  {
    id: 'api-keys',
    tab: "Clés d'API",
    icon: KeyRound,
    cwd: '~/shop',
    prompt: 'Ajoutez le paiement Stripe et Sentry. Demandez-moi les clés avec ask-form, jamais dans le chat.',
    lines: [
      { kind: 'tool', text: 'ask-form create --title "Stripe + Sentry : clés et paramètres"', out: '10 questions · 2 champs secrets' },
      { kind: 'say', text: 'Le code de facturation est prêt. Vos clés vont directement dans .env.local :', link: true },
      { kind: 'wait', text: "J'attends les clés…" },
      { kind: 'work', text: 'Gestionnaire de webhook ajouté, avec ses tests' },
      { kind: 'done', text: 'Clés écrites dans .env.local, sans passer sous mes yeux. Bascule annuelle, 9 €/mois. Je branche le paiement.' },
    ],
    form: {
      slug: 'stripe-sentry_7cw2ma',
      title: 'Stripe + Sentry : clés',
      desc: 'Les champs secrets ne me parviennent jamais : ils vont directement dans .env.local.',
      page: 'Stripe',
      pages: 2,
      blocks: [
        { kind: 'secret', label: 'Clé secrète Stripe', chars: 22 },
        {
          kind: 'choice',
          label: 'Page de tarifs',
          cards: true,
          selected: 0,
          tap: 1,
          options: [
            { label: 'Une seule offre', mock: PricingOne },
            { label: 'Mensuel / annuel', mock: PricingToggle },
          ],
        },
      ],
      sent: 'Vos clés sont dans .env.local.',
    },
    href: '/fr/examples/api-keys',
  },
  {
    id: 'team-vote',
    tab: "Vote d'équipe",
    icon: Users,
    cwd: '~/tasks-app',
    prompt:
      "Envoyez les deux maquettes d'onboarding à l'équipe avec ask-form. Recueillez les votes de chacun jusqu'à vendredi, puis construisez la gagnante.",
    lines: [
      { kind: 'tool', text: 'ask-form create --title "Onboarding : lequel ?" --multi --ttl 72h', out: 'un lien pour tout le monde · fermeture ven. 18 h' },
      { kind: 'say', text: "Un seul lien pour toute l'équipe, à poster dans #product :", link: true },
      { kind: 'wait', text: "J'attends la clôture du vote vendredi…" },
      { kind: 'work', text: 'En attendant : ce que les deux versions ont en commun' },
      { kind: 'done', text: 'Vote clos, 5 voix : 4 × Checklist, 1 × Visite guidée. Je construis la checklist.' },
    ],
    form: {
      slug: 'onboarding-vote_x2m7rd',
      title: 'Onboarding : lequel ?',
      desc: 'Deux approches pour le premier écran que voit un nouvel utilisateur. Choisissez celle que vous livreriez.',
      page: 'Votre vote',
      pages: 1,
      blocks: [
        { kind: 'text', label: 'Votre nom', value: 'Léa' },
        { kind: 'text', label: 'E-mail', value: 'lea@acme.dev', optional: true, help: 'Facultatif : pour que votre agent sache qui a voté.' },
        {
          kind: 'choice',
          label: 'Quel onboarding ?',
          cards: true,
          selected: -1,
          tap: 0,
          options: [
            { label: 'Checklist', mock: OnboardChecklist },
            { label: 'Visite guidée', mock: OnboardTour },
          ],
        },
      ],
      sent: "Merci, Léa. 4 personnes ont répondu pour l'instant.",
    },
    href: '/fr/examples/team-vote',
  },
]
