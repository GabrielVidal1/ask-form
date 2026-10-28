import { renderToString } from 'react-dom/server'
import App from './App.tsx'
import { readOrigin } from './lib-origin.ts'

export function render(): string {
  return renderToString(<App origin={readOrigin()} />)
}
