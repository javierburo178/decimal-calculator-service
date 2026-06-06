// Presentation only: consumes the useCalculator hook and renders the form and
// results. No business logic, no validation, no fetch — all of that lives in
// useCalculator (logic) and api.ts (transport). Operand fields are labelled
// `a` / `b` to match the backend contract.

import type { FormEvent } from 'react'
import { OPERATIONS, type Operation } from './api'
import { OPERATION_LABELS } from './operations'
import { useCalculator } from './useCalculator'
import { OperandField } from './OperandField'
import { ResultPanel } from './ResultPanel'
import './App.css'

function App() {
  const calc = useCalculator()

  const onSubmit = (e: FormEvent) => {
    e.preventDefault()
    void calc.submit()
  }

  return (
    <main className="app">
      <header className="app__header">
        <h1 className="app__title">Decimal-Safe Calculator</h1>
        <p className="app__subtitle">
          Exact decimal arithmetic, computed on the server — no float rounding.
        </p>
      </header>

      <form className="card" onSubmit={onSubmit} noValidate>
        <div className="field">
          <label className="field__label" htmlFor="operation">
            Operation
          </label>
          <select
            id="operation"
            className="control"
            value={calc.operation}
            onChange={(e) => calc.setOperation(e.target.value as Operation)}
          >
            {OPERATIONS.map((op) => (
              <option key={op} value={op}>
                {OPERATION_LABELS[op]}
              </option>
            ))}
          </select>
        </div>

        <OperandField
          id="operand-a"
          label="a"
          value={calc.a}
          error={calc.fieldErrors.a}
          onChange={calc.setA}
        />

        {calc.bRequired && (
          <OperandField
            id="operand-b"
            label="b"
            value={calc.b}
            error={calc.fieldErrors.b}
            onChange={calc.setB}
          />
        )}

        <button className="submit" type="submit" disabled={calc.loading}>
          {calc.loading ? 'Calculating…' : 'Calculate'}
        </button>
      </form>

      <ResultPanel result={calc.result} error={calc.error} />
    </main>
  )
}

export default App
