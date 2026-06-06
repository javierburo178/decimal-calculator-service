// Pure transport layer for the calculator backend. Knows nothing about React.
//
// The decimal contract is the whole point of this service, so it is enforced
// here at the boundary: operands are sent as STRINGS (a JS number would corrupt
// 0.1 before the request ever leaves the browser), and the result is read back
// as a STRING and never coerced to Number (which would re-introduce float
// error). See SPEC.md "Decimal/money rationale".

/** The operations the backend supports, in display order. */
export const OPERATIONS = [
  'add',
  'subtract',
  'multiply',
  'divide',
  'power',
  'sqrt',
  'percentage',
] as const

export type Operation = (typeof OPERATIONS)[number]

/** Operations that take a single operand (no `b`). */
const UNARY_OPERATIONS: ReadonlySet<Operation> = new Set<Operation>(['sqrt'])

/** Whether `op` is unary (only `a`); the inverse means `b` is required. */
export function isUnary(op: Operation): boolean {
  return UNARY_OPERATIONS.has(op)
}

export interface CalculateRequest {
  operation: Operation
  /** Operand `a` as a decimal string — never a JS number. */
  a: string
  /** Operand `b` as a decimal string for binary ops; null for unary (sqrt). */
  b: string | null
}

export interface CalculateResult {
  /** The result as a decimal string — display as-is, never Number(...). */
  result: string
  /** Significant figures the result carries. */
  precision: number
}

/** The backend's documented error body: { error, code }. */
interface ErrorBody {
  error: string
  code: string
}

/**
 * A domain error returned by the backend (a 4xx with a stable `code`).
 * The `code` is the contract surface the UI maps to a human message; the
 * `message` is the backend's human-readable text (a sensible fallback).
 */
export class ApiError extends Error {
  readonly code: string
  readonly status: number

  constructor(code: string, message: string, status: number) {
    super(message)
    this.name = 'ApiError'
    this.code = code
    this.status = status
  }
}

/** A failure to reach or understand the backend (offline, 5xx, malformed body). */
export class NetworkError extends Error {
  constructor(message: string) {
    super(message)
    this.name = 'NetworkError'
  }
}

const API_URL = (import.meta.env.VITE_API_URL ?? 'http://localhost:8080').replace(
  /\/+$/,
  '',
)

function isCalculateResult(v: unknown): v is CalculateResult {
  return (
    typeof v === 'object' &&
    v !== null &&
    typeof (v as Record<string, unknown>).result === 'string' &&
    typeof (v as Record<string, unknown>).precision === 'number'
  )
}

function isErrorBody(v: unknown): v is ErrorBody {
  return (
    typeof v === 'object' &&
    v !== null &&
    typeof (v as Record<string, unknown>).error === 'string' &&
    typeof (v as Record<string, unknown>).code === 'string'
  )
}

/**
 * POST a calculation to the backend.
 *
 * Resolves with the typed result on 200. Throws `ApiError` for a backend domain
 * error (4xx with {error,code}) or `NetworkError` for anything else (unreachable
 * server, 5xx, unparseable body). Never returns a partial/ambiguous value.
 */
export async function calculate(
  req: CalculateRequest,
  signal?: AbortSignal,
): Promise<CalculateResult> {
  let res: Response
  try {
    res = await fetch(`${API_URL}/api/calculate`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      // Operands are already strings on `req`; JSON.stringify keeps them strings
      // (no number coercion) — the whole point of the decimal contract.
      body: JSON.stringify(req),
      signal,
    })
  } catch (e) {
    // An abort is intentional (a newer request superseded this one); let the
    // caller recognize it instead of reporting a fake failure.
    if (e instanceof DOMException && e.name === 'AbortError') throw e
    throw new NetworkError('Could not reach the calculator service. Is it running?')
  }

  let body: unknown
  try {
    body = await res.json()
  } catch (e) {
    if (e instanceof DOMException && e.name === 'AbortError') throw e
    throw new NetworkError('The server returned a response that could not be read.')
  }

  if (res.ok) {
    if (isCalculateResult(body)) return body
    throw new NetworkError('The server returned an unexpected response shape.')
  }

  if (isErrorBody(body)) {
    throw new ApiError(body.code, body.error, res.status)
  }
  throw new NetworkError(`The server returned an error (HTTP ${res.status}).`)
}
