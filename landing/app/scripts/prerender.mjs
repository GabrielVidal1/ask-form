// Renders the page once per language at build time, so the landing reads
// without JavaScript (and to agents that fetch it); main.tsx hydrates it.
// dist/index.html is English (served at /), dist/fr/index.html French (/fr);
// both load the same hashed /assets/.
import { mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = fileURLToPath(new URL('..', import.meta.url))
const { render, pages } = await import(root + '.ssr/entry-server.js')
const dist = root + '../dist/'
const template = readFileSync(dist + 'index.html', 'utf8')

const attr = (s) => s.replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
// Each piece must be found exactly once: a template change shouldn't go unnoticed.
function swap(html, find, to, what) {
  const n = typeof find === 'string' ? html.split(find).length - 1 : (html.match(new RegExp(find, 'g')) ?? []).length
  if (n !== 1) throw new Error(`index.html: ${what} found ${n} times`)
  return html.replace(find, () => to)
}

for (const p of pages) {
  let html = template
  html = swap(html, '<html lang="en">', `<html lang="${p.lang}">`, '<html lang="en">')
  html = swap(html, /<title>[^<]*<\/title>/, `<title>${attr(p.title)}</title>`, '<title>')
  html = swap(html, /<meta name="description" content="[^"]*" \/>/, `<meta name="description" content="${attr(p.description)}" />`, 'the description')
  html = swap(html, '<!--app-->', render(p.lang), 'the <!--app--> slot')
  const file = dist + p.file
  mkdirSync(dirname(file), { recursive: true })
  writeFileSync(file, html)
  console.log('prerendered', p.lang, file)
}
rmSync(root + '.ssr', { recursive: true, force: true })
