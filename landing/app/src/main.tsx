import { StrictMode } from 'react'
import { createRoot, hydrateRoot } from 'react-dom/client'
import './index.css'
import App from './App.tsx'
import { readOrigin } from './lib-origin.ts'

const root = document.getElementById('root')!
const app = (
  <StrictMode>
    <App origin={readOrigin()} />
  </StrictMode>
)
// Built pages arrive prerendered (scripts/prerender.mjs); the dev server's don't.
if (root.firstElementChild) hydrateRoot(root, app)
else createRoot(root).render(app)
