// The page comes in two languages: English at /, French at /fr. Every word a
// visitor reads lives in en.tsx and fr.tsx (the demo's in examples/data*.tsx);
// components take theirs from useCopy() (context.ts).
import { en, type Copy } from './en'
import { fr } from './fr'

export type Lang = 'en' | 'fr'
export type { Copy }

export const copies: Record<Lang, Copy> = { en, fr }

/** The page's language: what the prerender wrote in <html lang>, or the path on the dev server. */
export function readLang(): Lang {
  if (typeof document === 'undefined') return 'en'
  if (document.documentElement.lang === 'fr' || /^\/fr(\/|$)/.test(location.pathname)) return 'fr'
  return 'en'
}
