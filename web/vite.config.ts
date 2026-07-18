import type { IncomingMessage } from 'node:http'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Vite's proxy matches by path *prefix*, which collides with our own
// client-side routes that happen to share a segment with an API path —
// e.g. the frontend route /containers/:id/logs vs. the backend's
// /containers prefix (the backend itself has no route at exactly that
// path; only /containers/:id/logs/stream). A real browser navigation or
// refresh to /containers/abc/logs would otherwise get proxied straight
// to the Go server and 404, instead of Vite serving the SPA shell for
// React Router to handle client-side.
//
// This doesn't affect production: there, container-api's own router
// does exact pattern matching (not prefix matching), so an unmatched
// path like /containers/abc/logs already falls through cleanly to a
// static-file/SPA-fallback handler with no special-casing needed. This
// bypass exists only to make Vite's dev proxy behave the same way.
//
// The distinguishing signal: a real page navigation sends
// `Accept: text/html`; our own fetch() calls always ask for JSON
// explicitly (see api/client.ts) and never match this.
function bypassHtmlNavigations(req: IncomingMessage) {
  if (req.headers.accept?.includes('text/html')) {
    return '/index.html'
  }
}

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/containers': { target: 'http://localhost:8080', bypass: bypassHtmlNavigations },
      '/jobs': { target: 'http://localhost:8080', bypass: bypassHtmlNavigations },
      '/uploads': { target: 'http://localhost:8080', bypass: bypassHtmlNavigations },
      '/logs': { target: 'http://localhost:8080', bypass: bypassHtmlNavigations },
      '/healthz': 'http://localhost:8080',
      '/ws': { target: 'ws://localhost:8080', ws: true },
    },
  },
})
