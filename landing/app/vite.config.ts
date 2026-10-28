import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// The build lands in ../dist, which the relay embeds (landing/landing.go) and
// serves at / (index.html) and /assets/*. `npm run build` also prerenders the
// page into index.html, so it reads without JavaScript.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: { outDir: '../dist', emptyOutDir: true, assetsDir: 'assets' },
  // Bundle the design system into the prerender build: Node can't import its
  // extensionless prismjs imports on its own.
  ssr: { noExternal: ['@gabvdl/ui'] },
  server: { host: true, port: 5188, allowedHosts: true },
})
