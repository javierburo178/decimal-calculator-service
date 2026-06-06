import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// https://vite.dev/config/
// Test configuration lives in vitest.config.ts (kept separate so this build
// config stays on Vite's own types; Vitest bundles a different Vite version).
export default defineConfig({
  plugins: [react()],
})
