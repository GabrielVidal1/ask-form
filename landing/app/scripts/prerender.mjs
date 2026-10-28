// Renders the page once at build time into dist/index.html, so the landing
// reads without JavaScript (and to agents that fetch it); main.tsx hydrates it.
import { readFileSync, rmSync, writeFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const root = fileURLToPath(new URL('..', import.meta.url))
const { render } = await import(root + '.ssr/entry-server.js')
const file = root + '../dist/index.html'
const html = readFileSync(file, 'utf8')
if (!html.includes('<!--app-->')) throw new Error('index.html: no <!--app--> slot')
writeFileSync(file, html.replace('<!--app-->', render()))
rmSync(root + '.ssr', { recursive: true, force: true })
console.log('prerendered', file)
