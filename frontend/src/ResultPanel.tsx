// Shows the calculation outcome: an error banner, or the result. The result is
// rendered exactly as the backend string — never Number(...) — to preserve the
// decimal precision the whole service exists to guarantee.

import type { CalculateResult } from './api'

interface ResultPanelProps {
  result: CalculateResult | null
  error: string | null
}

export function ResultPanel({ result, error }: ResultPanelProps) {
  if (error !== null) {
    return (
      <p className="banner banner--error" role="alert">
        {error}
      </p>
    )
  }

  if (result !== null) {
    const figures = result.precision === 1 ? 'figure' : 'figures'
    return (
      <div className="banner banner--result" aria-live="polite">
        <output className="result__value">{result.result}</output>
        <span className="result__meta">
          {result.precision} significant {figures}
        </span>
      </div>
    )
  }

  return null
}
