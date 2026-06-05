# SPEC — Decimal-Safe Calculator Service

**Status:** contract / source of truth. Code and tests derive from this doc.
Sezzle Principal take-home. Calculator is the vehicle; evaluated qualities are
architecture judgment, failure handling, trade-off reasoning.

## Stack decisions
- Backend: stdlib net/http (Go 1.22+ method routing). No chi/gin. Would adopt a router at >10 routes / middleware chains / versioned groups.
- Numbers: shopspring/decimal. The money domain (add, subtract, multiply, divide, percentage) is float64-free end to end. Sole exception: transcendental fractional powers (x^y, y non-integer) use the library's float-seeded Ln, refined back to decimal precision — see "Float-free boundary" below.
- Result encoding: JSON string ("result":"0.333..."). Arbitrary-precision decimals don't fit JSON number without reintroducing float error (Stripe-style).
- Identity: general arithmetic, fintech-documented. NOT a ledger (see Limits).

## API
POST /api/calculate
Request: { "operation":"divide", "a":"1", "b":"3" }
- operation: enum add|subtract|multiply|divide|power|sqrt|percentage
- a: decimal string
- b: decimal string | null (null/absent for sqrt)
Operands cross the API as STRINGS, not numbers (a client sending 0.1 as a JSON number already lost precision).

Success 200: { "result":"0.3333333333333333333333333333", "precision":28 }
Error 4xx:  { "error":"division by zero", "code":"DIVIDE_BY_ZERO" }

GET /health -> 200 empty.

## Operation semantics
- add/subtract/multiply: exact, full precision, no rounding.
- divide: may be non-terminating -> truncate to 28 significant digits.
- sqrt: irrational for non-squares -> 28-digit precision, half-to-even.
- power int exp: exact. power fractional exp: same policy as sqrt.
- percentage: x*p/100, exact.
Rounding mode: banker's rounding (half-to-even). Rounding ONLY at output boundaries, never intermediate.

## Decimal/money rationale
float64 can't represent base-10 fractions (0.1+0.2 != 0.3); harmless once, catastrophic accumulated. Decimal eliminates it.
Tricky part = rounding policy: half-to-even avoids directional bias over volume; round only at boundaries; scale is currency-dependent in real systems (USD 2, JPY 0, BHD 3) — this calc is currency-agnostic and returns the precision used.
divide/sqrt/fractional-power are inherently non-terminating in decimal -> explicit precision (28) is a conscious decision, not a library default. This is the single most important behavior.

## Float-free boundary (honest scope of the no-float guarantee)

The no-float64 guarantee is absolute for the money domain — the four core operations plus percentage never touch float64 at any point, operands and results included. Square root is also float-free: it is hand-rolled Newton-Raphson in pure decimal.

The one exception is genuinely transcendental fractional power (x^y with non-integer y). shopspring/decimal computes this through Ln, which seeds its iteration with a float64 (math.Log of the operand) before refining the result back to the requested decimal precision. The float64 is a starting estimate for an iterative refinement, not a value that flows into the result — the returned digits are computed in decimal. We verified sqrt(2) and power(2, 0.5) produce the identical 28-significant-digit result, confirming the fractional-power path reaches the same precision as the float-free sqrt.

Why not eliminate it: a float-free Ln/Exp would mean reimplementing transcendental functions in arbitrary precision — substantial scope for a capability (x^y for irrational y) that no money calculation requires. The conscious decision is to keep the money-relevant paths provably float-free and document this single transcendental seam rather than hide it. If a fully float-free transcendental path were required, it would be a deliberate follow-up, not a default.

## limits
Applies money-handling principles at single-operation level. NOT a banking system. A real ledger also needs double-entry, atomicity, idempotency, audit, reconciliation.

## Unhappy-path matrix (each row = a test)
| # | Case | Expected |
|---|---|---|
| 1 | unparseable JSON body | 400, generic msg, no internal leak, never 500 |
| 2 | empty body | 400 |
| 3 | unknown operation | 400 code UNKNOWN_OP |
| 4 | missing b on binary op | 400 code MISSING_OPERAND |
| 5 | a/b not valid decimal string | 400 code INVALID_NUMBER |
| 6 | divide by zero | 400 code DIVIDE_BY_ZERO, no panic/Inf |
| 7 | sqrt of negative | 400 code NEGATIVE_SQRT |
| 8a | divide non-terminating (1/3) | 200, truncated to 28 digits |
| 8b | sqrt non-perfect-square (2) | 200, 28-digit precision |
| 8c | power fractional exponent | 200, same policy |
| 9 | more places than precision | half-to-even applied |
| 10 | GET instead of POST | 405 |
| 11 | oversized payload | 413 via MaxBytesReader |
| 12 | N concurrent requests | no data race (-race clean) |
| 13 | divide exceeding max precision | defined behavior, documented, not silent garbage |

## Definition of done — Backend (gate before frontend)
- All 13 rows have passing tests.
- go test -race ./... clean. go vet ./... clean.
- Coverage >= 85% on arithmetic package.
- README documents stack decisions + money rationale + honest limits.

## Definition of done — Frontend
- Client-side validation rejects non-decimal input before request.
- Backend error codes surface as readable messages; nothing swallowed.
- Operands sent as strings (no JS-number precision loss).
- RTL tests: happy + >=3 unhappy (divide-by-zero, invalid input, server error).
- tsc strict, zero warnings.

## Whole
- GitHub Actions: backend tests (-race, coverage) + frontend build/test on push.
- README: setup, run, API examples, design decisions, limits.
- PROMPTS.md: real prompts used.
