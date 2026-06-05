// Package calc implements decimal-safe arithmetic for the calculator service.
//
// It contains no transport concerns. Every operation works on
// shopspring/decimal values (never float64) and returns one of the package's
// sentinel errors for domain failures, which the transport layer maps to HTTP
// status codes and stable error codes.
//
// Rounding policy (the single most important behavior of the service): results
// that are not guaranteed to terminate — divide, sqrt, fractional power and
// non-terminating negative-integer power — are rounded to Precision significant
// digits using banker's rounding (half-to-even). Rounding happens exactly once,
// at the output boundary, never on intermediate values; extra guard digits are
// carried through the computation so that single boundary round is unbiased.
// Exact operations (add, subtract, multiply, percentage, positive-integer
// power) are returned at full precision with no rounding.
package calc

import (
	"errors"
	"math/big"

	"github.com/shopspring/decimal"
)

// Precision is the number of significant digits retained for results that may
// be non-terminating. Per SPEC this is a conscious, explicit choice — not a
// library default.
const Precision int32 = 28

// guardDigits are extra digits carried through intermediate computation so the
// single banker's round at the output boundary is not biased by how an earlier
// step happened to truncate. They are discarded by the final round.
const guardDigits int32 = 16

// workPrecision is the decimal-place precision used for intermediate
// non-terminating computation, before the result is rounded to Precision
// significant digits at the output boundary.
const workPrecision = Precision + guardDigits

// MaxMagnitude bounds the order of magnitude of every operand: a value v is
// accepted only when 10^-MaxMagnitude <= |v| <= 10^MaxMagnitude (zero is always
// in range). This is an operational-safety guard, not a math policy. A decimal's
// magnitude lives in its (one-byte-cheap) exponent field, so a tiny request body
// like "1e999999999" passes MaxBytesReader yet would force Add/Sub to align
// exponents into a ~1e9-digit coefficient — unbounded memory and a hung request.
// Rejecting out-of-range operands up front guarantees nothing unbounded is ever
// materialized.
const MaxMagnitude int32 = 1000

// MaxPowerExponent bounds |exponent| for the power operation specifically. Even
// in-range operands can blow up multiplicatively: base**exp with a large integer
// exponent makes PowBigInt materialize an enormous integer (10**1e9 never
// returns). Capping the exponent keeps the result materialization bounded. A
// huge exponent's magnitude (e.g. 1e9 written as "1000000000") is NOT caught by
// MaxMagnitude — its order of magnitude is only 9 — so this separate cap is
// required.
const MaxPowerExponent int32 = 1000

// Operation enumerates the supported operations.
type Operation string

const (
	Add        Operation = "add"
	Subtract   Operation = "subtract"
	Multiply   Operation = "multiply"
	Divide     Operation = "divide"
	Power      Operation = "power"
	Sqrt       Operation = "sqrt"
	Percentage Operation = "percentage"
)

// Sentinel domain errors. The transport layer matches these with errors.Is to
// produce stable HTTP error codes. None of them represent a server fault; they
// are all caused by client input and must map to 4xx, never 500.
var (
	ErrUnknownOperator = errors.New("unknown operation")
	ErrDivideByZero    = errors.New("division by zero")
	ErrNegativeSqrt    = errors.New("square root of a negative number")
	ErrMissingOperand  = errors.New("missing operand")
	ErrUndefinedResult = errors.New("undefined result")
	ErrOutOfRange      = errors.New("operand magnitude out of range")
)

// IsBinary reports whether op requires a second operand (b). It returns
// ErrUnknownOperator for unrecognized operations, so a caller can validate the
// operation and discover its arity in a single step.
func IsBinary(op Operation) (bool, error) {
	switch op {
	case Add, Subtract, Multiply, Divide, Power, Percentage:
		return true, nil
	case Sqrt:
		return false, nil
	default:
		return false, ErrUnknownOperator
	}
}

// Calculate evaluates op on operands a and b. For unary operations (Sqrt) b is
// ignored. The returned value is normalized to its minimal canonical form
// (trailing fractional zeros removed without changing the value).
func Calculate(op Operation, a, b decimal.Decimal) (decimal.Decimal, error) {
	// Operational-safety guard: reject pathological-magnitude inputs BEFORE any
	// arithmetic, so a tiny request body can never make the library materialize
	// an unbounded coefficient (see MaxMagnitude / MaxPowerExponent).
	if err := guardMagnitude(op, a, b); err != nil {
		return decimal.Decimal{}, err
	}
	res, err := compute(op, a, b)
	if err != nil {
		return decimal.Decimal{}, err
	}
	return stripTrailingZeros(res), nil
}

// guardMagnitude rejects inputs whose magnitude would force an unbounded
// materialization. It runs before compute and returns only sentinel errors
// (ErrUnknownOperator for a bad op, ErrOutOfRange for a magnitude violation),
// so every rejection still maps to a 4xx, never a hang and never a 500.
func guardMagnitude(op Operation, a, b decimal.Decimal) error {
	binary, err := IsBinary(op)
	if err != nil {
		return err
	}
	if !withinMagnitude(a) {
		return ErrOutOfRange
	}
	if binary && !withinMagnitude(b) {
		return ErrOutOfRange
	}
	// b is the exponent for power; cap it separately — a large integer exponent
	// is itself low-magnitude but drives result size (10**1e9).
	if op == Power && b.Abs().GreaterThan(decimal.New(int64(MaxPowerExponent), 0)) {
		return ErrOutOfRange
	}
	return nil
}

// withinMagnitude reports whether d's order of magnitude is within
// ±MaxMagnitude. The order of magnitude is the power of ten of the most
// significant digit (adjusted exponent); both inputs are cheap to read and
// never materialize the coefficient. Zero has no magnitude and is always in
// range.
func withinMagnitude(d decimal.Decimal) bool {
	if d.IsZero() {
		return true
	}
	adjExp := d.Exponent() + int32(d.NumDigits()) - 1
	return adjExp <= MaxMagnitude && adjExp >= -MaxMagnitude
}

func compute(op Operation, a, b decimal.Decimal) (decimal.Decimal, error) {
	switch op {
	case Add:
		return a.Add(b), nil
	case Subtract:
		return a.Sub(b), nil
	case Multiply:
		return a.Mul(b), nil
	case Divide:
		return div(a, b)
	case Power:
		return pow(a, b)
	case Sqrt:
		return sqrt(a)
	case Percentage:
		return percentage(a, b), nil
	default:
		return decimal.Decimal{}, ErrUnknownOperator
	}
}

func div(a, b decimal.Decimal) (decimal.Decimal, error) {
	if b.IsZero() {
		return decimal.Decimal{}, ErrDivideByZero
	}
	// Carry the intermediate at workPrecision *significant* digits, not a fixed
	// number of decimal places. A small-magnitude quotient (|q| << 1) has its
	// most-significant digit far below the decimal point, so a fixed-place
	// DivRound would discard real significant digits before the boundary round
	// ever sees them — 1/1e50 would collapse to 0. We estimate the quotient's
	// adjusted exponent from the operands (exact to within 1, which the guard
	// digits absorb) and convert "workPrecision significant digits" into the
	// equivalent decimal-place count: places = workPrecision - 1 - qAdjExp.
	// Clamp so that |q| >= 1 keeps the original place count — those results were
	// already correct and must stay byte-identical. Rounding still happens
	// exactly once, at the boundary, half-to-even.
	places := workPrecision - 1 - (adjustedExponent(a) - adjustedExponent(b))
	if places < workPrecision {
		places = workPrecision
	}
	q := a.DivRound(b, places)
	return roundSignificant(q, Precision), nil
}

// percentage computes a * b / 100. Dividing by 100 only shifts the decimal
// point, so the result is exact; we multiply by 0.01 (exact) rather than
// dividing (which would be capped by the library's division precision).
func percentage(a, b decimal.Decimal) decimal.Decimal {
	return a.Mul(b).Mul(decimal.New(1, -2))
}

// sqrt computes the square root via Newton-Raphson in pure decimal arithmetic
// (no float64 computes any result digit) and rounds to Precision significant
// digits at the boundary. The iteration converges quadratically, so a handful
// of steps reach workPrecision; the loop bound is a safety net, not the
// expected path.
func sqrt(a decimal.Decimal) (decimal.Decimal, error) {
	switch a.Sign() {
	case -1:
		return decimal.Decimal{}, ErrNegativeSqrt
	case 0:
		return decimal.Zero, nil
	}

	half := decimal.New(5, -1) // 0.5
	// Initial guess ~ 10^(adjExp/2): the right order of magnitude so the
	// quadratically-convergent iteration needs only a few steps.
	adjExp := a.Exponent() + int32(a.NumDigits()) - 1
	x := decimal.New(1, adjExp/2)
	threshold := decimal.New(1, -workPrecision)

	for i := 0; i < 100; i++ {
		// x_{n+1} = (x_n + a/x_n) / 2
		next := x.Add(a.DivRound(x, workPrecision)).Mul(half)
		if next.Sub(x).Abs().LessThanOrEqual(threshold) {
			x = next
			break
		}
		x = next
	}
	return roundSignificant(x, Precision), nil
}

func pow(base, exp decimal.Decimal) (decimal.Decimal, error) {
	// x ** 0 = 1. We adopt the common convention 0 ** 0 = 1 rather than
	// erroring, to avoid surprising a caller on a case the SPEC does not list.
	if exp.IsZero() {
		return decimal.New(1, 0), nil
	}

	if exp.IsInteger() {
		if exp.Sign() > 0 {
			// Positive integer exponent: exact repeated multiplication.
			return base.PowBigInt(exp.BigInt())
		}
		// Negative integer exponent: 1 / base**|exp|. The power itself is
		// exact, but the reciprocal may be non-terminating (e.g. 3**-1), so it
		// goes through the divide policy (28 sig figs, half-to-even).
		if base.IsZero() {
			return decimal.Decimal{}, ErrDivideByZero // 1/0
		}
		denom, err := base.PowBigInt(new(big.Int).Abs(exp.BigInt()))
		if err != nil {
			return decimal.Decimal{}, ErrUndefinedResult
		}
		return div(decimal.New(1, 0), denom)
	}

	// Fractional exponent: transcendental, same precision policy as sqrt.
	if base.IsNegative() {
		return decimal.Decimal{}, ErrUndefinedResult // would be imaginary
	}
	if base.IsZero() {
		return decimal.Zero, nil // 0 ** (positive fractional) = 0
	}
	res, err := base.PowWithPrecision(exp, workPrecision)
	if err != nil {
		return decimal.Decimal{}, ErrUndefinedResult
	}
	return roundSignificant(res, Precision), nil
}

// roundSignificant rounds d to sig significant digits using banker's rounding
// (half-to-even). shopspring rounds by decimal place, so we translate "sig
// significant digits" into the equivalent place via the adjusted exponent (the
// power of ten of the most-significant digit).
func roundSignificant(d decimal.Decimal, sig int32) decimal.Decimal {
	if d.IsZero() {
		return decimal.Zero
	}
	places := sig - 1 - adjustedExponent(d)
	return d.RoundBank(places)
}

// adjustedExponent returns the power of ten of d's most-significant digit (its
// order of magnitude). It reads only the exponent and digit count, so it never
// materializes the coefficient. Behaviour for d == 0 is unspecified (callers
// guard zero separately).
func adjustedExponent(d decimal.Decimal) int32 {
	return d.Exponent() + int32(d.NumDigits()) - 1
}

// stripTrailingZeros returns d with trailing zeros in the fractional part
// removed, without changing its value (0.5000 -> 0.5, 20.00 -> 20). Zeros in
// the integer part are preserved (100 stays 100). This yields a minimal
// canonical textual result; it is normalization, not rounding.
func stripTrailingZeros(d decimal.Decimal) decimal.Decimal {
	if d.IsZero() {
		return decimal.Zero
	}
	coef := new(big.Int).Set(d.Coefficient())
	exp := d.Exponent()
	ten := big.NewInt(10)
	quo := new(big.Int)
	rem := new(big.Int)
	for exp < 0 {
		quo.QuoRem(coef, ten, rem)
		if rem.Sign() != 0 {
			break
		}
		coef.Set(quo)
		exp++
	}
	return decimal.NewFromBigInt(coef, exp)
}
