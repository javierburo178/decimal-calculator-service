import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

// Vitest config, separate from vite.config.ts. Vitest bundles its own Vite
// version, so combining its `test` types with the project's Vite 8 plugin types
// in one file conflicts under tsc; keeping it here (outside the build tsconfig)
// avoids that while Vitest still loads this file at runtime.
export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    css: false,
  },
})
