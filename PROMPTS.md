# Prompts & Process

The take-home permits AI tooling and asks that prompts be shared. This project was
built with a deliberate **generator–evaluator** process rather than one-shot
generation: one agent implements against a written spec, a separate agent audits
the result adversarially in a clean context. Every design decision was made and
verified by me; the agents executed against decisions captured in `SPEC.md`.

## Process

1. **Spec first.** `SPEC.md` was written as the source of truth — the API
   contract, operation semantics, the money/precision policy, and an
   unhappy-path matrix — before any implementation.
2. **Generator.** An implementation pass built the backend strictly against the
   spec (logic/transport split, decimal arithmetic, the full test matrix).
3. **Evaluator.** A separate, hostile audit pass in a clean context tried to
   break the service against the spec. It found defects the implementation pass
   missed (below).
4. **Targeted fixes.** Each finding was fixed in isolation, then re-audited.

## What the adversarial audit caught

The evaluator pass found, despite 94.6% statement coverage on the arithmetic
package:
- **A denial-of-service vector** — operands/exponents whose magnitude is driven
  by the decimal exponent (cheap in bytes) could materialize multi-billion-digit
  numbers and hang the server (observed ~857 MB RSS from two requests). Fixed
  with a magnitude guard rejecting such inputs with a 400 before compute.
- **A small-magnitude precision bug** — results below ~1e-16 shed significant
  figures, and below ~1e-44 collapsed to a wrong `0` (`1/1e50` returned `0`).
  Fixed by making intermediate precision exponent-relative.
- **A float64 over-claim in the spec** — the no-float64 guarantee was worded as
  an absolute the underlying library can't honor (it uses float64 for exact
  exponent-scale alignment and digit counting). The spec was corrected to claim
  only what's true: no float64 in result-digit computation.
- **An error-shape inconsistency** — the 405 returned plain text instead of the
  documented `{error, code}` JSON. Fixed.

This is the core argument for the process: an independent skeptical pass surfaces
cases that line coverage does not, because coverage measures lines executed, not
cases considered.

## Representative prompts

### Spec-to-implementation (generator)
> Implement the complete backend strictly against SPEC.md. Separate pure
> arithmetic (`calc`, no HTTP, sentinel errors) from transport (`httpapi`:
> decode, validate, map domain errors to status+code). stdlib net/http only;
> shopspring/decimal, never float64 for result digits; operands and results as
> JSON strings; domain errors as 4xx with stable codes, never 500. Table-driven
> tests for calc and httptest tests for every unhappy-path matrix row. Write a
> HANDOFF documenting every decision the spec left open; do not self-approve.

### Adversarial audit (evaluator, run in a clean session)
> You are a hostile QA engineer auditing a backend you did not write. SPEC.md is
> the contract. For every unhappy-path row, EXECUTE the case (curl/test), don't
> just read. Verify tests assert the right thing (status AND code). Probe the
> decimal behavior: significant-digits vs decimal-places, half-to-even vs
> half-up, any float64 on a money path, numeric extremes (overflow/hang/OOM).
> Output a criterion / PASS-FAIL / evidence table and one verdict. If anything
> fails, the answer is NO.

### Targeted fix (one finding at a time)
> A hostile audit found [finding]. Fix ONLY this — touch nothing else. [precise
> description + observed-vs-expected]. Add tests proving the fix. Existing tests
> and `go vet` must stay clean. Update HANDOFF; do not self-approve.

## Tooling
- Claude (planning, spec design, review) and Claude Code (implementation,
  auditing, fixes), Go 1.26, shopspring/decimal.
