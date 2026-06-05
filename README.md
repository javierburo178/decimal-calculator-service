# Decimal-Safe Calculator Service

A full-stack calculator with a Go backend microservice and a React + TypeScript frontend. Arithmetic runs server-side on arbitrary-precision decimals (`shopspring/decimal`), not float64 — so `0.1 + 0.2` returns exactly `0.3`, and results carry 28 significant figures. Built as a take-home; the calculator is the vehicle, the focus is architecture, failure handling, and trade-off reasoning.

## Architecture

Monorepo with two independent deployables:

```
.
├── backend/          Go microservice — autonomous (own module, Dockerfile, lifecycle)
│   ├── calc/         pure decimal arithmetic; no HTTP; sentinel errors only
│   ├── httpapi/      transport: decode, validate, map domain errors → status+code
│   └── main.go       server wiring + graceful shutdown
└── frontend/         React + TS app (consumes the API)
```

The backend separates **logic from transport**: `calc` knows nothing about HTTP and is unit-tested in isolation; `httpapi` owns decoding, validation, and the error→(status, code) mapping. The split is what makes the arithmetic provable without spinning up a server.

Monorepo is a deliberate choice for evaluation convenience (clone once, run with one compose file). The backend is a true microservice — its own module, Dockerfile, and lifecycle — and could be extracted to its own repo if the system grew to multiple services.

## Setup & run

### Prerequisites
- Go 1.22+ (developed on 1.26)
- Node 18+ and npm (for the frontend)
- Optional: Docker + Docker Compose

### Backend
```bash
cd backend
go run .                 # serves on :8080
```
Configurable via `PORT` (e.g. `PORT=9090 go run .`).

### Frontend
[Frontend: pendiente Chunk 2 — cómo levantar el dev server y la env var del API]

### Both, via Docker
[Docker: pendiente — docker compose up levanta backend + frontend]

## API

### `POST /api/calculate`

Request body:
```json
{ "operation": "divide", "a": "1", "b": "3" }
```

| Field | Type | Notes |
|---|---|---|
| `operation` | string | `add`, `subtract`, `multiply`, `divide`, `power`, `sqrt`, `percentage` |
| `a` | string | decimal as a **string** (see Design decisions) |
| `b` | string \| null | required for binary ops; omit for `sqrt` |

Success — `200`:
```json
{ "result": "0.3333333333333333333333333333", "precision": 28 }
```

Error — `4xx`:
```json
{ "error": "division by zero", "code": "DIVIDE_BY_ZERO" }
```

### Examples
```bash
# Exact decimal — no float drift
curl -sX POST localhost:8080/api/calculate \
  -d '{"operation":"add","a":"0.1","b":"0.2"}'
# → {"result":"0.3","precision":1}

# Non-terminating, capped at 28 significant figures
curl -sX POST localhost:8080/api/calculate \
  -d '{"operation":"divide","a":"1","b":"3"}'
# → {"result":"0.3333333333333333333333333333","precision":28}

# Domain error → 400 with a stable code
curl -sX POST localhost:8080/api/calculate \
  -d '{"operation":"divide","a":"1","b":"0"}'
# → {"error":"division by zero","code":"DIVIDE_BY_ZERO"}

# Wrong method → 405, same JSON error shape
curl -sX GET localhost:8080/api/calculate
# → {"error":"method not allowed","code":"METHOD_NOT_ALLOWED"}
```

### `GET /health` → `200`, empty body.

## Design decisions

**Decimal, not float64.** `float64` can't represent base-10 fractions exactly: `0.1 + 0.2` yields `0.30000000000000004`. Harmless once, but accumulated across many operations it drifts — unacceptable for anything money-adjacent. All arithmetic uses `shopspring/decimal` (base-10), so results are exact. This is the single most consequential decision; everything below follows from it.

**Operands and results cross the API as strings, not JSON numbers.** A client that sends `0.1` as a JSON number has *already* lost precision before the request leaves the browser — JSON numbers are IEEE-754 floats. Sending `"0.1"` as a string keeps the value exact end to end. Same on the way back: the result is a string (Stripe's API does this for the same reason). A `precision` field reports the significant figures carried.

**stdlib `net/http`, no web framework.** Go 1.22's `ServeMux` does method-based routing (`POST /api/calculate`), which covers everything three routes need. A router like chi/gin would be dependency that doesn't earn its place here. I'd reach for one at >10 routes, middleware chains, or versioned groups — not before.

**One endpoint, not one-per-operation.** `POST /api/calculate` with an `operation` field is easier to extend and test than six near-identical routes.

**Errors are a typed contract.** Every error returns `{error, code}` with a stable, documented `code` (`DIVIDE_BY_ZERO`, `INVALID_NUMBER`, …) and the right 4xx status. The `code` is for machines (the frontend switches on it); the `error` is for humans. No known-bad input ever returns 500.

## Money & precision

The decimal choice is the foundation; these are the policies built on it.

**Rounding: banker's rounding (half-to-even).** When a result must be rounded, ties go to the nearest even digit (`2.5 → 2`, `3.5 → 4`). Naive half-up rounding biases results upward, and over a high volume of operations that bias accumulates into real money. Half-to-even is the financial standard precisely because it doesn't drift in one direction.

**Round only at boundaries.** Intermediate computation carries extra guard digits; rounding to the final 28 significant figures happens once, at the output boundary — never in intermediate steps. Rounding mid-computation is exactly how fractions of a cent leak.

**Precision is part of the contract.** Non-terminating results (`1/3`, `√2`, fractional powers) are capped at 28 significant figures — a conscious, documented choice, not a library default. Small-magnitude results keep their full 28 figures down to the operand range (`1/1e50` returns `1e-50`, not `0`). The response reports the `precision` actually carried.

**The float-free boundary, stated honestly.** No `float64` is used to compute any result digit — the four core operations, percentage, and `sqrt` (hand-rolled Newton-Raphson) are pure decimal. The one exception is transcendental fractional power (`x^y`, non-integer `y`), where the library seeds an iteration with a `float64` before refining back to decimal; the seed never flows into the result digits. I document this single seam rather than claim an absolute ("no float64 anywhere") that no decimal library on real hardware can honestly guarantee.

## Scope boundaries (conscious, not accidental)

The hard part of scoping isn't what to build — it's what to leave out and say so. These are deliberate omissions for a single-purpose service, each with the condition that would change the call.

**Result-magnitude hardening.** Operand magnitude and power exponent are each bounded (rejecting unbounded-memory inputs with a 400). Their *product* is still reachable — `1e1000 ^ 1000` computes a ~1M-digit result: bounded and fast (~60ms), not the unbounded hang the guard removed. A defense-in-depth follow-up would bound the *result* magnitude directly. Left as a documented, lower-severity item.

**Configurable precision.** Precision is a single documented constant (28). A real multi-currency system would need per-currency scale (USD 2, JPY 0, BHD 3) and likely a precision *context* object. I did not build that abstraction — for one general-purpose calculator it's premature, and a configurable knob is surface to maintain and test. The constant is named and isolated; extracting a context is a small, deliberate change *if* the requirement arrives.

**Observability.** Errors are typed, but there's no structured logging, metrics, or tracing. For production I'd add structured logs on the non-convergence and out-of-range paths and latency metrics per operation. Out of scope for a take-home; noted as the first thing I'd add for a real deployment.

**Float-free transcendentals.** Eliminating the `float64` seed in fractional power means reimplementing `Ln`/`Exp` in arbitrary precision — substantial work for a capability no money calculation needs. Deliberately deferred (see Money & precision).

## Testing

```bash
cd backend
go test ./...            # all tests
go test -race ./...      # with the race detector
go test -cover ./calc/   # coverage (currently 94.6% on the arithmetic package)
go vet ./...             # static analysis
```

Tests are table-driven in `calc` (every operation, every sentinel error, half-to-even boundary proofs, sqrt round-trip) and `httptest`-based in `httpapi` (the full unhappy-path matrix by row, plus concurrency under `-race`).

**Process note.** The backend was built with a generator–evaluator loop: one pass implements against the spec, a second pass audits it adversarially in a clean context. The audit pass found a denial-of-service vector and a small-magnitude precision bug that 94% statement coverage did not — a reminder that coverage measures lines executed, not cases considered.

## Honest limits

This applies money-handling *principles* at the level of a single operation — exact decimals, explicit rounding, precision as a contract. It is **not** a banking or ledger system. A production financial system additionally needs double-entry accounting, transactional atomicity, idempotency keys, an audit trail, and reconciliation. Those are out of scope here by design; the point of this service is correct, well-reasoned arithmetic at the unit level, not a system of record.
