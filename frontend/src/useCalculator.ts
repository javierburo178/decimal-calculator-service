// All calculator logic lives here: input state, client-side validation,
// orchestration of the API call, loading state, and mapping backend error
// `code`s to human-readable messages. It renders nothing and is fully testable
// with renderHook (no DOM). The view (App.tsx) is presentation only.
//
// The pieces of logic are pure module-level helpers (validateField, validate,
// errorToMessage); the hook just wires them to React state, so each function
// stays small and independently testable. Operands are named `a`/`b` to match
// the backend contract.

import { useCallback, useState } from 'react'
import {
  ApiError,
  NetworkError,
  calculate,
  isUnary,
  type CalculateResult,
  type Operation,
} from './api'

// Backend error `code` -> user-facing message. The raw code is never shown and
// the error is never swallowed; an unknown code falls back to a generic message
// so a new backend code degrades gracefully instead of leaking.
const ERROR_MESSAGES: Readonly<Record<string, string>> = {
  DIVIDE_BY_ZERO: 'Cannot divide by zero.',
  NEGATIVE_SQRT: 'Cannot take the square root of a negative number.',
  INVALID_NUMBER: 'One of the values is not a valid number.',
  MISSING_OPERAND: 'A required value is missing.',
  UNKNOWN_OP: 'That operation is not supported.',
  UNDEFINED_RESULT: 'The result is undefined for those values.',
  OPERAND_OUT_OF_RANGE: 'A value is too large or too small to compute safely.',
  PAYLOAD_TOO_LARGE: 'The request is too large to process.',
  BAD_REQUEST: 'The request could not be understood.',
  METHOD_NOT_ALLOWED: 'The request could not be understood.',
}

// Client-side decimal check — a UX guard only; the backend stays the source of
// truth. Accepts integers, decimals, a leading sign, and scientific notation
// (1e10, -1.5e-3, .5, 2.).
const DECIMAL_RE = /^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$/

/** Whether `s` looks like a decimal the backend would accept. */
export function isDecimalString(s: string): boolean {
  return DECIMAL_RE.test(s.trim())
}

// Field-agnostic messages: each renders directly under its labelled input, so
// the message never needs to name the field.
const EMPTY_MESSAGE = 'Enter a value.'
const INVALID_MESSAGE = 'Enter a valid number (e.g. 0.1, -3, 1e10).'

export interface FieldErrors {
  a: string | null
  b: string | null
}

const NO_FIELD_ERRORS: FieldErrors = { a: null, b: null }

/** Validate a single operand: empty, then format. Null means valid. */
function validateField(value: string): string | null {
  if (value.trim() === '') return EMPTY_MESSAGE
  if (!isDecimalString(value)) return INVALID_MESSAGE
  return null
}

/** Validate both operands; `b` is skipped for unary operations. */
function validate(a: string, b: string, bRequired: boolean): FieldErrors {
  return {
    a: validateField(a),
    b: bRequired ? validateField(b) : null,
  }
}

function hasFieldError(errs: FieldErrors): boolean {
  return errs.a !== null || errs.b !== null
}

/** Turn any thrown value into a user-facing message; never expose a raw code. */
function errorToMessage(e: unknown): string {
  if (e instanceof ApiError) return ERROR_MESSAGES[e.code] ?? 'The calculation could not be completed.'
  if (e instanceof NetworkError) return e.message
  return 'Something went wrong. Please try again.'
}

export interface UseCalculator {
  operation: Operation
  a: string
  b: string
  /** Latest successful result, or null. Cleared on any input change. */
  result: CalculateResult | null
  /** Latest request-level error message (backend/network), or null. */
  error: string | null
  /** Per-field client-validation messages. */
  fieldErrors: FieldErrors
  loading: boolean
  /** Whether the current operation needs operand `b`. */
  bRequired: boolean
  setOperation: (op: Operation) => void
  setA: (value: string) => void
  setB: (value: string) => void
  /** Validate client-side first, then call the backend. */
  submit: () => Promise<void>
}

export function useCalculator(): UseCalculator {
  const [operation, setOperationState] = useState<Operation>('add')
  const [a, setAState] = useState('')
  const [b, setBState] = useState('')
  const [result, setResult] = useState<CalculateResult | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [fieldErrors, setFieldErrors] = useState<FieldErrors>(NO_FIELD_ERRORS)
  const [loading, setLoading] = useState(false)

  const bRequired = !isUnary(operation)

  // Any edit invalidates the previous output, so clear it to avoid showing a
  // stale result/error next to changed inputs.
  const clearOutput = useCallback(() => {
    setResult(null)
    setError(null)
  }, [])

  const setOperation = useCallback(
    (op: Operation) => {
      setOperationState(op)
      setFieldErrors(NO_FIELD_ERRORS)
      clearOutput()
    },
    [clearOutput],
  )

  const setA = useCallback(
    (value: string) => {
      setAState(value)
      setFieldErrors((fe) => ({ ...fe, a: null }))
      clearOutput()
    },
    [clearOutput],
  )

  const setB = useCallback(
    (value: string) => {
      setBState(value)
      setFieldErrors((fe) => ({ ...fe, b: null }))
      clearOutput()
    },
    [clearOutput],
  )

  const runCalculation = useCallback(async () => {
    setLoading(true)
    setError(null)
    setResult(null)
    try {
      setResult(
        await calculate({
          operation,
          a: a.trim(),
          b: bRequired ? b.trim() : null,
        }),
      )
    } catch (e) {
      setError(errorToMessage(e))
    } finally {
      setLoading(false)
    }
  }, [operation, a, b, bRequired])

  const submit = useCallback(async () => {
    const errs = validate(a, b, bRequired)
    if (hasFieldError(errs)) {
      setFieldErrors(errs)
      clearOutput()
      return
    }
    setFieldErrors(NO_FIELD_ERRORS)
    await runCalculation()
  }, [a, b, bRequired, runCalculation, clearOutput])

  return {
    operation,
    a,
    b,
    result,
    error,
    fieldErrors,
    loading,
    bRequired,
    setOperation,
    setA,
    setB,
    submit,
  }
}
