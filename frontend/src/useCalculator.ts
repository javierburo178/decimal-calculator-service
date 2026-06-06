// All calculator logic lives here: input state, client-side validation,
// orchestration of the API call, loading state, and mapping backend error
// `code`s to human-readable messages. It renders nothing and is fully testable
// with renderHook (no DOM). The view (App.tsx) is presentation only.
//
// The pieces of logic are pure module-level helpers (validateField, validate,
// errorToMessage); the hook just wires them to React state, so each function
// stays small and independently testable. Operands are named `a`/`b` to match
// the backend contract.

import { useCallback, useEffect, useRef, useState } from 'react'
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

  // Tracks the in-flight request so a newer action can supersede an older one.
  const abortRef = useRef<AbortController | null>(null)

  // Cancel any in-flight request (on input/operation change or unmount). The
  // abort is intentional, so the superseded request must not surface an error.
  const cancelInFlight = useCallback(() => {
    if (abortRef.current !== null) {
      abortRef.current.abort()
      abortRef.current = null
      setLoading(false)
    }
  }, [])

  // Any edit invalidates the previous output, so clear it to avoid showing a
  // stale result/error next to changed inputs.
  const clearOutput = useCallback(() => {
    setResult(null)
    setError(null)
  }, [])

  const setOperation = useCallback(
    (op: Operation) => {
      cancelInFlight()
      setOperationState(op)
      setFieldErrors(NO_FIELD_ERRORS)
      clearOutput()
    },
    [cancelInFlight, clearOutput],
  )

  const setA = useCallback(
    (value: string) => {
      cancelInFlight()
      setAState(value)
      setFieldErrors((fe) => ({ ...fe, a: null }))
      clearOutput()
    },
    [cancelInFlight, clearOutput],
  )

  const setB = useCallback(
    (value: string) => {
      cancelInFlight()
      setBState(value)
      setFieldErrors((fe) => ({ ...fe, b: null }))
      clearOutput()
    },
    [cancelInFlight, clearOutput],
  )

  const runCalculation = useCallback(async () => {
    abortRef.current?.abort() // supersede any previous in-flight request
    const controller = new AbortController()
    abortRef.current = controller

    setLoading(true)
    setError(null)
    setResult(null)
    try {
      const res = await calculate(
        { operation, a: a.trim(), b: bRequired ? b.trim() : null },
        controller.signal,
      )
      if (controller.signal.aborted) return // superseded — ignore the stale result
      setResult(res)
    } catch (e) {
      if (controller.signal.aborted) return // aborted intentionally — not an error
      setError(errorToMessage(e))
    } finally {
      // Only the live request owns the loading flag; a superseded one must not
      // flip it off under the newer request.
      if (!controller.signal.aborted) setLoading(false)
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

  // Abort an in-flight request if the component unmounts (no setState after).
  useEffect(() => () => abortRef.current?.abort(), [])

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
