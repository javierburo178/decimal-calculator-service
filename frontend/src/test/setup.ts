// Vitest setup: jest-dom matchers and DOM cleanup between tests. We use explicit
// imports (no global injection), so cleanup is wired manually here.
import { afterEach } from 'vitest'
import { cleanup } from '@testing-library/react'
import '@testing-library/jest-dom/vitest'

afterEach(() => {
  cleanup()
})
