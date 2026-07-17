import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Admin back-office SPA, served under /admin (base below). Builds to the
// default dist/ (gitignored). Unlike the main web/ app — which the
// Dockerfile builds and copies into server/cmd/server/web/dist/ (the
// //go:embed target in server/cmd/server/static.go) — this admin app has
// no embed step yet: it isn't part of the Dockerfile build, and the
// backend has no //go:embed path for it. For now it's deployed/run
// standalone: `npm run dev` starts its own Vite dev server (:5174, a port
// distinct from the main web app's 5173 so both can run at once), and
// `npm run build` just produces a dist/ that has to be served separately.
export default defineConfig({
  base: '/admin/',
  plugins: [react()],
})
