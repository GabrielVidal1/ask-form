import { renderToString } from 'react-dom/server'
import App from './App.tsx'
import { copies, type Lang } from './copy'
import { readOrigin } from './lib-origin.ts'

/** The pages the prerender writes: dist/<file>, from index.html. */
export const pages: { lang: Lang; file: string; title: string; description: string }[] = (['en', 'fr'] as const).map(
  (lang) => ({ lang, file: lang === 'en' ? 'index.html' : lang + '/index.html', ...copies[lang].head }),
)

export function render(lang: Lang): string {
  return renderToString(<App origin={readOrigin()} lang={lang} />)
}
