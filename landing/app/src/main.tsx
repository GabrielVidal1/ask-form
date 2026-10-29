import { StrictMode } from 'react'
import { createRoot, hydrateRoot } from 'react-dom/client'
import './index.css'
import App from './App.tsx'
import { readLang } from './copy'
import { readOrigin } from './lib-origin.ts'

const root = document.getElementById('root')!
// The prerender set <html lang>; the dev server serves one index.html for /fr too.
const lang = readLang()
document.documentElement.lang = lang
const app = (
  <StrictMode>
    <App origin={readOrigin()} lang={lang} />
  </StrictMode>
)
// Built pages arrive prerendered (scripts/prerender.mjs); the dev server's don't.
if (root.firstElementChild) hydrateRoot(root, app)
else createRoot(root).render(app)
