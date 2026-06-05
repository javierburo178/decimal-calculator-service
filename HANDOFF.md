# HANDOFF — Chunk 1 (Backend)

Generator → evaluator handoff. This documents what was built, every decision the
SPEC left open, and the rows/areas where I want hostile scrutiny. **I am not
claiming the Definition of Done is met — that is the evaluator's call.** Below is
what I did and the raw results.

## What was built

```
backend/
  go.mod            module "calculator", go 1.26, requires shopspring/decimal v1.4.0 (only dep)
  main.go           server wiring + graceful shutdown on SIGTERM/SIGINT
  calc/             pure decimal arithmetic — NO http. Sentinel errors. All math lives here.
    calc.go
    calc_test.go    table-driven: every op + every sentinel + half-to-even proofs + round-trip
  httpapi/          transport only: decode, validate, map domain errors -> status+code, encode
    http.go
    http_test.go    httptest, all 13 matrix rows by number + happy path + health
```

Architecture follows the requested split: `calc` is transport-agnostic and only
ever returns sentinel errors; `httpapi` owns all HTTP concerns and the
error→(status, code) mapping; `main.go` is wiring only.

### Results (raw)

- `go test -race ./...` → **clean** (`calc` and `httpapi` both ok).
- `go vet ./...` → **clean**.
- `go test -cover ./calc/` → **94.6%** of statements (target ≥ 85%).
- End-to-end smoke (live server on :8099): `divide 1/3` →
  `{"result":"0.3333333333333333333333333333","precision":28}` (matches the SPEC
  example exactly); divide-by-zero → 400 `DIVIDE_BY_ZERO`; `GET /api/calculate`
  → 405; `GET /health` → 200 empty; SIGTERM → graceful drain.

### Matrix coverage map (where each row is tested)

| Row | Test |
|----|----|
| 14 operational safety (magnitude guard) | `TestMatrix_Row14_MagnitudeGuard_DoS` + `TestMatrix_Row14_MagnitudeBoundary` (http) + `TestMagnitudeGuard` (calc) |
| 1 unparseable JSON | `TestMatrix_ErrorRows/row1` + `TestMatrix_Row1_NoInternalLeak` (asserts no decoder leak) |
| 2 empty body | `TestMatrix_ErrorRows/row2` |
| 3 unknown op | `TestMatrix_ErrorRows/row3` (`UNKNOWN_OP`) |
| 4 missing b | `TestMatrix_ErrorRows/row4` (`MISSING_OPERAND`) |
| 5 invalid number | `row5a` (bad a) + `row5b` (bad b) (`INVALID_NUMBER`) |
| 6 divide by zero | `TestMatrix_ErrorRows/row6` (`DIVIDE_BY_ZERO`) |
| 7 sqrt negative | `TestMatrix_ErrorRows/row7` (`NEGATIVE_SQRT`) |
| 8a/8b/8c | `TestMatrix_Rows8_NonTerminatingSuccess` (1/3, √2, 2^0.5) |
| 9 more places than precision | `TestMatrix_Row9_RoundingApplied` (http) + `TestRoundSignificantHalfToEven` (calc, the exact even/odd proof) |
| 10 GET → 405 | `TestMatrix_Row10_MethodNotAllowed` (GET/PUT/DELETE) |
| 11 oversized | `TestMatrix_Row11_OversizedPayload` (413, MaxBytesReader) |
| 12 concurrent | `TestMatrix_Row12_Concurrent` (64 goroutines, validated under `-race`) |
| 13 exceeds precision | `TestMatrix_Row13_ExceedsPrecisionIsDefined` (http) + `TestNonTerminatingCappedAtPrecision` (calc) |

---

## Post-audit fix — Finding C (magnitude-driven DoS)

A hostile audit found that operand/result magnitude is driven by the decimal
**exponent**, which costs almost no bytes, so `MaxBytesReader` does **not** bound
it. Two trivially-reachable requests hung the server and grew memory without
bound (observed ~857 MB RSS from two requests, connections held open):

- `{"operation":"power","a":"10","b":"1000000000"}` — `PowBigInt` tries to
  materialize a ~1e9-digit integer.
- `{"operation":"add","a":"1e999999999","b":"2"}` — `Add` aligns exponents,
  materializing a ~1e9-digit coefficient.

**Fix (surgical, calc-layer guard before any computation; nothing else touched).**
`Calculate` now calls `guardMagnitude` before `compute`, returning a sentinel
that maps to **400 `OPERAND_OUT_OF_RANGE`** in the `{error,code}` shape — never a
hang, never a 500. The check reads only the exponent and digit count, so it never
materializes the coefficient it is protecting against.

Limits chosen (both in `calc/calc.go`, documented as constants):

- **`MaxMagnitude = 1000`** — every operand must satisfy
  `1e-1000 <= |v| <= 1e1000` (zero always allowed). The bound is on the *order of
  magnitude* (adjusted exponent = `exponent + numDigits - 1`), so worst-case
  exponent alignment in `Add`/`Sub` materializes at most ~2000-digit coefficients.
  1000 is far beyond any legitimate calculator input while keeping work bounded.
- **`MaxPowerExponent = 1000`** — for `power`, `|exponent|` must be `<= 1000`.
  This is a *separate* cap because a huge exponent like `1e9` (written
  `"1000000000"`) is itself only order-of-magnitude 9, so `MaxMagnitude` does not
  catch it; yet it is exactly what makes `PowBigInt` blow up. With base also
  bounded by `MaxMagnitude`, the largest materializable power result is bounded.

New error code: **`OPERAND_OUT_OF_RANGE`** (status 400). This is a **new
operational-safety matrix row — row 14** (not in the original SPEC 13; the SPEC
matrix had no DoS/operational-safety row). Recommend the evaluator add it to
SPEC §"Unhappy-path matrix" as: *"operand/exponent magnitude beyond safe bound →
400 `OPERAND_OUT_OF_RANGE`, fast, never hang/OOM."*

Verification (live + tests): all four attack vectors now return `400
OPERAND_OUT_OF_RANGE` in <1 ms; boundary `1e1000` succeeds, `1e1001` rejected;
`go test -race ./...` and `go vet ./...` clean; calc coverage 94.4% (≥85%);
normal ops (`1/3`, `√2`, `2^10`, `0.1+0.2`) unchanged; server RSS stayed ~12 MB
under the attacks. **Scope:** precision logic, the float64/`power` question, and
everything else were deliberately left untouched.

---

## Post-audit fix — Finding B (small-magnitude precision loss)

The audit found that `divide` (and the negative-integer-power path that routes
through it) lost significant digits for results with `|value| < ~1e-16`, and
returned a wrong `0` below ~1e-44:

- `1/3e30` → 14 sig figs (want 28); `2^-120` → 8 sig figs (want 28)
- `1/1e50` → `"0"` (wrong; true value 1e-50); `1/1e1000` → `"0"` (wrong)
- `|value| >= 1` was already correct and had to stay byte-identical.

**Root cause.** `div` carried the intermediate at a *fixed* `workPrecision`
**decimal-place** count (`DivRound(b, 44)`). Significant-digit precision must
scale with where the result's most-significant digit falls; a fixed place count
discards real significant digits *before* `roundSignificant` runs, and the
boundary round cannot recover what was already gone.

**Fix (surgical, `calc/calc.go`, `div` only).** Intermediate precision is now
**significant-digit-relative** to the quotient's exponent. We estimate the
quotient's adjusted exponent from the operands —
`qAdjExp ≈ adjustedExponent(a) - adjustedExponent(b)`, exact to within 1, which
the 16 guard digits absorb — and convert "workPrecision significant digits" into
the equivalent place count:

```
places = workPrecision - 1 - qAdjExp     // sig-fig-relative
if places < workPrecision { places = workPrecision }   // clamp: |q| >= 1 unchanged
q := a.DivRound(b, places)
```

The clamp guarantees results of magnitude `>= ~1` use the original place count,
so every previously-correct case is **byte-identical**. For small magnitudes,
`places` grows with the exponent, so the intermediate always carries ≥ 28 real
sig figs for the single boundary round (still rounding **only at the boundary**,
**half-to-even**). A factored-out `adjustedExponent` helper (also now used by
`roundSignificant`) reads only the exponent + digit count, so it never
materializes a coefficient — the Finding-C DoS guard is untouched and still
bounds `places` (operands are capped at 1e±1000, so `places` is bounded too).
The **float64/fractional-power** question, the **405 body**, and the **DoS
guard** were left exactly as they were.

**Confirmed (live + tests):**

| Case | Before | After |
|---|---|---|
| `1/3e30` | 14 sig figs | **28 sig figs** |
| `2^-120` | 8 sig figs | **28 sig figs** (matches true value, 29th digit 2 → rounds down) |
| `1/1e50` | `"0"` | **`1e-50`** (exact, 1 sig fig — see note) |
| `1/1e1000` | `"0"` | **`1e-1000`** (exact, 1 sig fig) |
| `1/3`, `2/3`, `100/3`, `√2`, `3^-1`, `2^-1` | — | **byte-identical** |

Property check: `(1/x)*x` reconstructs 1 within 1e-26 across magnitudes
(`TestDivideRoundTrip`).

**Note / flag for the evaluator (faithful to the math, not the ticket wording).**
`1/1e50` and `1/1e1000` are *exact* powers of ten — `1e-50` and `1e-1000` — with
**one** significant figure, not 28. The bug was that they returned `0`; the fix
returns the correct nonzero value. Tests assert exactly-28 figs only for the
genuinely non-terminating cases (`1/3e30`, `2^-120`) and the *correct exact
value* for the powers of ten; asserting 28 figs on an exact 1-fig value would be
mathematically wrong (CLAUDE.md: never silently deviate). The `precision`
response field reports the true sig-fig count, so it reads `1` for these.

Tests added: `TestSmallMagnitudePrecision`, `TestDivideRoundTrip` (calc),
`TestSmallMagnitudePrecision_HTTP` (httpapi). `go test -race ./...` and
`go vet ./...` clean; calc coverage **94.6%**.

---

## Post-audit fix — Finding D (405 error-shape inconsistency)

The audit found that every error response used the `{error,code}` JSON shape
**except** the 405: Go 1.22 method routing auto-emitted a plain-text
`Method Not Allowed` body for a wrong method on `/api/calculate` (and `/health`),
inconsistent with the documented contract. (This is the inconsistency previously
flagged in §8 above.)

**Fix (surgical, `httpapi/http.go` only).** Added a path-only catch-all per route
that is strictly less specific than the method-specific pattern, so the valid
verb still routes to its handler while every *other* method falls through to a
small `methodNotAllowed` handler:

```go
mux.HandleFunc("POST /api/calculate", h.calculate)
mux.HandleFunc("GET /health", h.health)
mux.HandleFunc("/api/calculate", methodNotAllowed(http.MethodPost))
mux.HandleFunc("/health", methodNotAllowed(http.MethodGet))
```

`methodNotAllowed` sets the `Allow` header and **reuses the existing `writeError`
helper** (no duplicated encoding) to emit a 405 in the standard shape. New stable
code: **`METHOD_NOT_ALLOWED`**. No router restructure, no framework.

**Confirmed (live + tests):** `GET`/`PUT`/`DELETE` on `/api/calculate` →
`405`, `Allow: POST`, body `{"error":"method not allowed","code":"METHOD_NOT_ALLOWED"}`;
`POST /health` → `405`, `Allow: GET`, same shape. Regression: `POST /api/calculate`
and `GET /health` still work. `TestMatrix_Row10_MethodNotAllowed` now asserts the
JSON shape, the `code`, and the `Allow` header (not just status). `go test -race
./...` and `go vet ./...` clean. **Scope:** only the 405 body; nothing else
touched.

---

## Decisions the SPEC left open (please scrutinize)

### 1. "28 significant digits" vs "28 decimal places" — I chose **significant digits**
The SPEC says "truncate to **28 significant digits**" (3×) and the response field
is `precision`, which in decimal/IEEE/Python terminology means significant
digits. shopspring rounds by **decimal place**, not significant digits, so I
translate: round to `places = sig - 1 - adjustedExponent`, where the adjusted
exponent is the power-of-ten of the most-significant digit.

The only concrete example in the SPEC (`1/3` → 28 threes) is consistent with
**both** interpretations, so it does not disambiguate. The two diverge for
results with an integer part ≥ 1 digit: e.g. `100/3` →
`33.33333333333333333333333333` (26 decimals = **28 sig figs**), not 28
decimals. **If the evaluator intended 28 decimal places, this is the one place to
change** (`roundSignificant` in `calc/calc.go`) and a few expected strings in
tests. I believe sig-figs is the faithful reading.

### 2. `precision` in the success response = significant digits actually carried
For `1/3` this is 28 (matches the SPEC example). For terminating results it is
the real count, e.g. `1/2` → `{"result":"0.5","precision":1}`,
`0.1+0.2` → `{"result":"0.3","precision":1}`. Rationale: the SPEC says the
service "returns the precision used"; reporting the true significant-digit count
is the honest reading. Alternative considered: always return 28. I rejected it as
misleading for exact ops. Easy to flip if the evaluator wants the constant 28.

### 3. Results are normalized (trailing fractional zeros stripped)
`stripTrailingZeros` turns `2.000…0` → `2`, `20.00` → `20`, `0.5000…` → `0.5`.
This is normalization, **not** rounding — the value is unchanged — so it does not
violate "exact, full precision". Without it, `sqrt(4)` would serialize as
`2.000000000000000000000000000`, which reads like a bug. Integer-part zeros are
preserved (`100` stays `100`). Non-terminating results are unaffected (`1/3`
has no trailing zeros).

### 4. Row 13 ("divide exceeding max precision") — interpretation
I read this as: when the true quotient needs more than 28 sig figs (any
non-terminating division), the result is deterministically **capped at exactly 28
sig figs via banker's rounding** — defined and documented, never silent garbage.
Tested two ways: the result carries exactly 28 sig figs, and repeated calls are
byte-identical (deterministic). If row 13 instead meant "operands with
pathologically large precision," note that operand precision is bounded upstream
by `MaxBytesReader` (64 KiB body), and arithmetic on large-but-finite decimals is
still exact/handled.

### 5. Guard digits + "round only at boundaries"
Intermediate non-terminating computation (divide, Newton sqrt) is carried at
`Precision + 16` = 44 decimal places, then rounded **once** to 28 sig figs at the
output boundary. The 16 guard digits keep that single boundary round unbiased.
Caveat I want flagged: a rational quotient could in theory have a run of ≥16
nines/zeros straddling the cut, in which case the intermediate `DivRound` (which
rounds half-up at 44 places) could in principle perturb the 28th digit. This is
the standard guard-digit trade-off; for true correctness one would detect exact
termination. I judged 16 guard digits more than sufficient for this scope and
documented it rather than hiding it.

### 6. sqrt is hand-rolled Newton-Raphson (pure decimal); fractional power uses the library
The non-negotiable is "NEVER float64 for operands/results." shopspring v1.4.0's
`Pow`/`PowWithPrecision` route fractional exponents through `Ln`, which **seeds**
its iteration with `math.Log(InexactFloat64())` before refining in decimal. The
seed does not limit result precision (it is refined to the requested digits), but
to keep the most common irrational case (square roots) provably float-free, I
implemented `sqrt` myself with Newton-Raphson in pure decimal. **Genuinely
transcendental fractional powers** (`x^y`, y non-integer) still go through
`PowWithPrecision` — reimplementing a float-free `Ln` was out of scope. I verified
the two methods agree: `sqrt(2)` and `power(2, 0.5)` produce the **identical**
28-digit result, so "fractional power: same policy as sqrt" holds in practice.
**Flag for the evaluator:** if the float *seed* inside the library's `Ln` is
considered a violation of the non-negotiable, fractional `power` is the only path
that touches it, and it would need a float-free `Ln`/`Exp`.

### 7. Operation arity lives in `calc.IsBinary`
Whether `b` is required is domain knowledge, so it lives in `calc`, not
`httpapi`. `IsBinary` doubles as the unknown-operation check (returns
`ErrUnknownOperator`), letting the handler validate the operation and its arity
in one call before parsing operands.

### 8. Codes/behaviors not enumerated in the SPEC matrix
- **Rows 1 & 2 code**: SPEC specifies status (400) but no code. I return
  `BAD_REQUEST` with a fixed generic message (no decoder detail leaked, never
  500).
- **Row 11 code**: `PAYLOAD_TOO_LARGE` (SPEC specifies only status 413).
- **Missing `a`**: not in the matrix (matrix only lists missing `b`). `a` is
  required for every op, so absent `a` → 400 `MISSING_OPERAND` (same treatment as
  missing `b`).
- **`0 ** 0`**: adopted the common convention `= 1` rather than erroring, to
  avoid surprising on an unlisted case.
- **Negative base, fractional exponent** (imaginary), and **`0 ** negative`**
  (infinity): not in the matrix. Mapped to 400 — `UNDEFINED_RESULT` and
  `DIVIDE_BY_ZERO` respectively — so they never become 500.
- **405 body**: **RESOLVED (Finding D closed)** — see "Post-audit fix — Finding
  D" below. The 405 now returns the documented `{error,code}` JSON shape with
  code `METHOD_NOT_ALLOWED`, `Allow` header preserved. *(Originally this returned
  the mux's plain-text body; the audit flagged the inconsistency.)*

### 9. `MaxBytesReader` limit = 64 KiB (`DefaultMaxBytes`)
Requests are tiny (operation + two decimal strings); 64 KiB leaves generous room
for very long operands while bounding abuse. `NewWithLimit` lets the oversized
test use a tiny limit so it doesn't build a 64 KiB body. The number is a
judgment call, not derived from the SPEC.

### 10. Concurrency / no global mutation
I deliberately avoided shopspring's global `decimal.DivisionPrecision` /
`PowPrecisionNegativeExponent`; every rounding uses an explicit per-call
precision (`DivRound(_, n)`, `PowWithPrecision(_, n)`). The handler holds no
mutable shared state, so the service is race-free by construction (confirmed
under `-race` with 64 concurrent requests).

---

## Things I'm least sure about (rank-ordered for the evaluator)
1. **Sig-figs vs decimal-places** (decision #1) — the single highest-impact
   interpretation. If wrong, it's a localized change.
2. **Float seed inside library `Ln` for fractional power** (decision #6) — only
   path that touches float at all; may or may not satisfy the non-negotiable as
   written.
3. **`precision` field semantics** (decision #2) — constant 28 vs actual count.
4. ~~**405 returns plain text, not `{error,code}`** (decision #8).~~ **CLOSED
   (Finding D)** — 405 now uses the `{error,code}` shape; see "Post-audit fix —
   Finding D".
5. **Guard-digit edge case** (decision #5) — theoretical, documented.

## Not done (out of scope for Chunk 1 backend code)
- README (stack decisions + money rationale + honest limits) — SPEC lists it
  under the backend Definition of Done; not written yet.
- Dockerfile (CLAUDE.md mentions backend has its own Dockerfile) — not created.
- GitHub Actions / PROMPTS.md — SPEC "Whole" section, not part of backend code.

These are flagged so the evaluator can decide whether they gate "done."
