# FRONTEND_HANDOFF — Chunk 2 (Frontend)

Generator → evaluator handoff. Documents what was built, the decisions the prompt
left open, and where I want hostile scrutiny. **I am not claiming "done" — that
is the evaluator's call.** Raw results below.

## What was built

```
frontend/src/
  api.ts             Pure transport. fetch + (de)serialization + typed errors. No React.
  useCalculator.ts   All logic: state, client validation, orchestration, error mapping. Renders nothing.
  operations.ts      Presentation metadata: operation-aware operand labels.
  App.tsx            Presentation only: consumes the hook, renders form + result.
  OperandField.tsx   Small presentational component: labelled input + inline error.
  ResultPanel.tsx    Small presentational component: result string or error banner.
  vite-env.d.ts      Types VITE_API_URL on import.meta.env.
  test/setup.ts      Vitest setup (jest-dom + cleanup).
  api.test.ts        Transport tests (5).
  useCalculator.test.ts  Hook tests via renderHook (5).
  App.test.tsx       Component tests via RTL (6).
frontend/
  vite.config.ts     Build config (pure Vite).
  vitest.config.ts   Test config (separate — see decision #6).
  .env.example       Documents VITE_API_URL.
```

The three-layer split mirrors the backend's logic/transport separation: `api.ts`
is transport-only and React-free; `useCalculator` holds 100% of the logic and is
testable with `renderHook` (no DOM); `App` + the two small components are
presentation only (no fetch, no validation, no business rules).

### Results (raw)

- `npm run test` (vitest) → **16 passed** (api 5, hook 5, component 6).
- `npm run build` (`tsc -b` **strict** + `vite build`) → **success**, 0 type errors.
- `npm run lint` (eslint) → **clean**, 0 warnings.
- Boot smoke: backend on :8080 (`0.1 + 0.2` → `{"result":"0.3","precision":1}`),
  Vite dev server serves the app (`<title>Decimal-Safe Calculator</title>`) and
  transforms `main.tsx` (200).

### How the non-negotiables are met

- **Operands sent as strings.** `api.ts` serializes `firstValue`/`secondValue`
  with `JSON.stringify` directly from string state — never `Number(...)`.
  `api.test.ts` asserts the request body fields are `typeof === 'string'`.
- **Result displayed as-is.** `ResultPanel` renders `result.result` (a string)
  verbatim in an `<output>`; it is never parsed to a number. `api.test.ts`
  asserts a 28-digit result round-trips as a string unchanged.
- **Error codes → human messages.** `useCalculator` maps backend `code`s to
  copy; the raw code is never rendered and the error is never swallowed. Tests
  assert the message shows and the code does not.
- **Client validation is defense-in-depth.** Empty/non-decimal input is rejected
  before any request (`isDecimalString`), but backend errors are still handled
  (the client guard is UX, not the source of truth).

## Decisions the prompt left open (please scrutinize)

1. **Operand labels — operation-aware (user-confirmed).** The user explicitly
   rejected showing `a`/`b`. Labels now adapt to the operation: power → Base /
   Exponent; percentage → Value / Percent (%); sqrt → Number; divide → Dividend
   / Divisor; add/subtract/multiply → First number / Second number. `a`/`b`
   appear **only** in one place — the JSON body in `api.ts` — because they are
   the backend's wire field names; everywhere else the operands are
   `firstValue` / `secondValue`.
2. **UI language — English (user-confirmed).** Consistent with SPEC/README.
3. **Validation messages are field-agnostic.** "Enter a value." / "Enter a valid
   number (e.g. 0.1, -3, 1e10)." render directly under the labelled field, so the
   message never needs to name the field (and never says "a"/"b").
4. **Decimal regex** `^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$` — accepts
   integers, decimals, leading sign, and scientific notation. Intentionally
   permissive: the backend (`shopspring/decimal`) is the source of truth, so the
   client only rejects the obviously-bad to save a round trip.
5. **Stale output is cleared on any input/operation change.** Prevents a result
   sitting next to inputs that no longer produced it. Trade-off: a result
   disappears as soon as you edit — I judged that less confusing than a stale
   answer.
6. **Two config files (`vite.config.ts` + `vitest.config.ts`).** The skeleton
   uses Vite 8 (rolldown); Vitest 3.2.6 bundles a different Vite, so combining
   the `test` block with the Vite-8 `react()` plugin types in one file fails
   `tsc`. Splitting keeps the build config on Vite's own types (the only file
   `tsconfig.node.json` checks) while Vitest loads `vitest.config.ts` at runtime.
7. **`precision` shown as "N significant figures".** Surfaces the contract's
   precision field in plain language rather than hiding it.
8. **Error-code map includes codes beyond the prompt's list** (UNDEFINED_RESULT,
   PAYLOAD_TOO_LARGE, BAD_REQUEST, METHOD_NOT_ALLOWED) plus a generic fallback,
   so an unknown/new backend code degrades to a sensible message instead of
   leaking.
9. **`NetworkError` vs `ApiError`.** Transport distinguishes a reachable backend
   returning a domain 4xx (`ApiError`, mapped by code) from an unreachable/5xx/
   malformed response (`NetworkError`, message shown directly). Covered by tests.
10. **Config via `VITE_API_URL`**, default `http://localhost:8080`; trailing
    slashes trimmed. Documented in `.env.example`.

## Things I'm least sure about (rank-ordered for the evaluator)

1. **`vitest.config.ts` is outside the build tsconfig**, so it is not
   type-checked by `tsc -b`. This is the cleanest workaround for the Vite-8/
   Vitest version mismatch, but it means a type error in that one file wouldn't
   fail the build. Flag if you'd prefer the test config merged + cast instead.
2. **Decimal regex strictness.** It accepts forms like `2.` and `.5` (which the
   backend also accepts). If you want the client stricter or looser, this is the
   one line to change (`DECIMAL_RE` in `useCalculator.ts`).
3. **Clearing the result on edit** (decision #5) — a UX judgment call; easy to
   reverse if you'd rather keep the last result visible until the next submit.
4. **No request cancellation / debounce.** Submit is button-driven (not on every
   keystroke), and the backend is fast, so I did not add AbortController. If
   rapid double-submits matter, that's the addition.

## Not done (flagged, possibly out of scope for Chunk 2)

- README's `[Frontend: ...]` and `[Docker: ...]` placeholders — the prompt said
  these are filled "in a later step"; left as-is.
- No frontend Dockerfile / compose wiring yet (the `[Docker: ...]` step).
- No GitHub Actions for the frontend (SPEC "Whole" section, not Chunk 2 code).
- E2E in a real browser — covered instead by RTL (jsdom) + a boot smoke test;
  no Playwright/Cypress.
