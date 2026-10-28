// The relay replaces {{ORIGIN}} and {{HOST}} in the served HTML. The
// prerender writes the placeholders themselves; the browser reads the filled-in
// value back from the <meta>, so the hydrated page renders what the server sent.
const ORIGIN_PLACEHOLDER = '{{ORIGIN}}'
const HOST_PLACEHOLDER = '{{HOST}}'

export interface Origin {
  origin: string
  host: string
}

export function readOrigin(): Origin {
  if (typeof document === 'undefined') return { origin: ORIGIN_PLACEHOLDER, host: HOST_PLACEHOLDER }
  let origin = document.querySelector<HTMLMetaElement>('meta[name="ask-form-origin"]')?.content ?? ''
  if (!origin || origin.includes('{{')) origin = location.origin // vite dev server
  return { origin, host: origin.replace(/^https?:\/\//, '') }
}
