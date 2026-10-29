// The French page (/fr): en.tsx's words in French. French spacing is typed in:
// a narrow no-break space (U+202F) before ? ! ; and a no-break space (U+00A0)
// before : and inside « ». No agent is named here: "votre agent de code".
import type { ReactNode } from 'react'

import { examples } from '../examples/data.fr'
import type { Copy } from './en'

export const fr: Copy = {
  lang: 'fr',
  home: '/fr',
  examplesPath: '/fr/examples',
  head: {
    title: "ask-form : votre agent de code demande, vous répondez d'où vous voulez",
    description:
      "ask-form : quand votre agent de code a besoin d'une décision, il vous envoie un formulaire. Vous répondez depuis n'importe quel navigateur, téléphone compris, en solo ou avec toute votre équipe, et il lit les réponses en JSON.",
  },
  nav: { examples: 'Exemples', install: 'Installer', language: 'Langue' },
  hero: {
    line1: 'Votre agent de code demande.',
    line2: "Vous répondez d'où vous voulez.",
    lede: "Votre agent pose ses questions dans de vrais formulaires, maquettes et code compris. N'importe qui y répond, de n'importe où.",
    tryNow: 'Essayer maintenant',
    see: 'Voir la démo',
    agents: "Fonctionne avec n'importe quel agent de code capable de lancer une commande shell.",
  },
  copy: { copy: 'Copier', copied: 'Copié' },
  demo: {
    title: "Une seule demande. Toutes ses questions d'un coup.",
    sub: 'Votre agent réunit tout dans un seul formulaire. Vous répondez où que vous soyez, il continue sur sa lancée.',
    tabs: 'Exemples',
    installLine: (origin: string) =>
      `Lisez ${origin}/agent-install et suivez ses instructions pour installer et configurer ask-form, puis : `,
    copyPrompt: "Copier ce prompt, avec la ligne d'installation",
    answer: 'Répondez vous-même à ce formulaire',
    all: 'Tous les exemples',
  },
  phone: {
    title: 'Un formulaire ask-form sur un téléphone',
    date: 'mardi 28 octobre',
    now: 'maintenant',
    notif: 'Votre agent a des questions pour vous',
    send: 'Envoyer les réponses',
    sent: 'Envoyé',
    secret: "Tenu à l'écart des réponses que lit votre agent.",
  },
  mock: {
    today: "Aujourd'hui",
    habits: ['Lire', 'Courir', 'Eau', 'Sommeil'],
    week: 'Cette semaine',
    days: ['L', 'M', 'M', 'J', 'V', 'S', 'D'],
    streaks: 'Séries',
    invoices: 'Factures',
    scrolls: '← défile →',
    due: 'à payer',
    paid: 'payée',
    plan: 'Pro',
    month: { price: '9 €', per: '/mois' },
    year: { price: '90 €', per: '/an' },
    monthly: 'Mensuel',
    yearly: 'Annuel',
    free: '2 mois offerts',
    subscribe: "S'abonner",
    start: 'Pour commencer',
    steps: ['Créer un tableau', 'Inviter votre équipe', 'Ajouter une première tâche'],
    tip: 'Appuyez sur + pour ajouter votre première tâche',
    tipFoot: '1 sur 4 · Suivant',
  },
  compare: {
    title: 'Pourquoi pas dans le terminal, tout simplement ?',
    sub: "Les mêmes questions, avec mieux qu'un curseur qui clignote pour y répondre.",
    old: 'Dans le terminal',
    now: 'Avec ask-form',
    rows: [
      {
        q: 'Où vous répondez',
        old: "Devant le clavier. Vous vous éloignez, l'agent attend.",
        now: "Dans n'importe quel navigateur, téléphone compris. L'agent continue de travailler pendant ce temps.",
      },
      {
        q: 'Qui répond',
        old: 'Vous, devant ce clavier.',
        now: "Toute personne qui a le lien : un vote d'équipe, une validation client, des bêta-testeurs. Toutes les réponses reviennent, avec un champ e-mail pour savoir qui a dit quoi.",
      },
      { q: 'Ce que vous voyez', old: 'Du texte.', now: 'Des maquettes au choix, des schémas, des tableaux et du code.' },
    ],
  },
  install: {
    title: 'Essayer maintenant',
    sub: (
      <>
        N'importe quel agent de code qui tourne sur votre ordinateur fait l'affaire. Il lit{' '}
        <a href="/agent-install">/agent-install</a>, installe ask-form, se configure et vous envoie un premier formulaire.
      </>
    ),
    paste: 'Collez ceci dans votre agent de code',
    pasteNote:
      "Complétez la phrase avec ce que vous voulez qu'il vous demande. Pas dans une app de chat : son bac à sable ne peut pas faire tourner ask-form en continu.",
    prompt: (origin: string) =>
      `Lisez ${origin}/agent-install et suivez ses instructions pour installer le skill ask-form et le configurer, puis servez-vous-en pour me demander : `,
    placeholder: "ce que vous voulez qu'il vous demande",
    manual: 'Installation manuelle',
    manualIntro: (
      <>
        Linux et macOS, x86-64 ou arm64. Une seule commande installe le binaire dans <code>~/.local/bin</code>, relie votre
        machine à ce relais, lance le service en arrière-plan et installe le skill.
      </>
    ),
    local: 'Sans le relais (formulaires accessibles depuis cette machine uniquement) :',
    goInstall: (
      <>
        Avec Go 1.22+ : <code>go install github.com/GabrielVidal1/ask-form/cmd/ask-form@latest</code>, puis{' '}
        <code>ask-form setup</code>
      </>
    ),
    releases: (github: string): ReactNode => (
      <>
        Ou prenez un binaire dans les <a href={github + '/releases'}>GitHub Releases</a>, puis lancez <code>ask-form setup</code>
      </>
    ),
    small: (
      <>
        Le service installe tout seul les nouvelles versions (<code>ASK_FORM_AUTO_UPGRADE=notify</code> ou <code>off</code>{' '}
        pour l'en empêcher). <code>ask-form uninstall</code> retire le service et le skill. Pour un agent qui lit
        AGENTS.md : <code>ask-form skill print --body &gt;&gt; AGENTS.md</code>.
      </>
    ),
  },
  privacy: {
    eyebrow: 'Privé dès la conception',
    title: 'Vos formulaires ne quittent jamais votre machine.',
    facts: [
      { title: 'Les formulaires restent chez vous', text: 'Le relais ne fait que transmettre les requêtes des pages. Il ne garde ni formulaire ni réponse.' },
      {
        title: 'Un lien privé par formulaire',
        text: "Un jeton de 256 bits. Une réponse et c'est fermé, ou ouvert à toute votre équipe jusqu'à ce que vous le fermiez. Chaque formulaire expire.",
      },
      { title: 'Les secrets vont dans .env', text: "La valeur d'un champ secret n'apparaît jamais dans les réponses que lit l'agent." },
    ],
    how: "Même 404 pour un formulaire inconnu et pour un mauvais jeton, pas de referrer, pas de cache, pas d'indexation, une CSP stricte, des maquettes dans des cadres isolés et sans script.",
    more: (github: string): ReactNode => (
      <>
        Plus de détails dans <a href={github + '/blob/main/docs/how-it-works.md'}>comment ça marche</a>.
      </>
    ),
    selfHost: (github: string): ReactNode => (
      <>
        Le relais est open source lui aussi : <a href={github + '/blob/main/docs/self-hosting.md'}>faites tourner le vôtre</a>{' '}
        sur votre domaine.
      </>
    ),
  },
  footer: { by: 'Fait avec ❤️ par', source: 'Code source sur GitHub', license: 'Licence MIT' },
  examples,
}
