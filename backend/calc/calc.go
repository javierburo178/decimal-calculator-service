// Package calc implements decimal-safe arithmetic. It has no transport
// concerns: every operation works on shopspring/decimal values and returns a
// sentinel error for domain failures, which the transport layer maps to HTTP.
//
// Non-terminating results (divide, sqrt, fractional/negative-integer power) are
// rounded to Precision significant digits, half-to-even, once at the output
// boundary; guard digits keep that single round unbiased. Exact operations are
// returned at full precision.
package calc

import (
	"errors"
	"math/big"

	"github.com/shopspring/decimal"
)

// Precision is the significant digits retained for non-terminating results — a
// deliberate choice per SPEC, not a library default.
const Precision int32 = 28

// guardDigits are extra digits carried through intermediate steps so the single
// boundary round isn't biased by an earlier truncation.
const guardDigits int32 = 16

const workPrecision = Precision + guardDigits

// MaxMagnitude / MaxPowerExponent are operational-safety guards, not math.
// Magnitude lives in the (cheap) exponent field, so a tiny body like
// "1e999999999" passes MaxBytesReader yet would force a multi-billion-digit
// coefficient. We bound the operand magnitude, and — separately — the power
// exponent, since a large exponent is itself low-magnitude (1e9 = "1000000000",
// order 9) but blows up the result.
const (
	MaxMagnitude     int32 = 1000
	MaxPowerExponent int32 = 1000
)

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

// Sentinel domain errors — all client-caused, matched with errors.Is by the
// transport layer and mapped to 4xx (never 500).
var (
	ErrUnknownOperator = errors.New("unknown operation")
	ErrDivideByZero    = errors.New("division by zero")
	ErrNegativeSqrt    = errors.New("square root of a negative number")
	ErrMissingOperand  = errors.New("missing operand")
	ErrUndefinedResult = errors.New("undefined result")
	ErrOutOfRange      = errors.New("operand magnitude out of range")
)

// IsBinary reports whether op needs operand b, returning ErrUnknownOperator for
// an unrecognized op so callers validate and find arity in one step.
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

// Calculate evaluates op on a and b (b ignored for unary ops). The result is
// normalized (trailing fractional zeros stripped, value unchanged).
func Calculate(op Operation, a, b decimal.Decimal) (decimal.Decimal, error) {
	if err := guardMagnitude(op, a, b); err != nil {
		return decimal.Decimal{}, err
	}
	res, err := compute(op, a, b)
	if err != nil {
		return decimal.Decimal{}, err
	}
	return stripTrailingZeros(res), nil
}

// guardMagnitude rejects pathological-magnitude inputs before any arithmetic, so
// nothing unbounded is ever materialized. Returns only sentinel errors.
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
	if op == Power && b.Abs().GreaterThan(decimal.New(int64(MaxPowerExponent), 0)) {
		return ErrOutOfRange
	}
	return nil
}

// withinMagnitude reports whether |d| is within 10^±MaxMagnitude (zero always
// in range), reading only exponent and digit count — never the coefficient.
func withinMagnitude(d decimal.Decimal) bool {
	if d.IsZero() {
		return true
	}
	adjExp := adjustedExponent(d)
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
	// Carry workPrecision *significant* digits, not fixed decimal places: a
	// small quotient (|q| << 1) would otherwise lose real digits before the
	// boundary round (1/1e50 -> 0). Estimate q's exponent from the operands
	// (exact within 1, absorbed by guard digits) and convert to a place count.
	places := workPrecision - 1 - (adjustedExponent(a) - adjustedExponent(b))
	if places < workPrecision {
		places = workPrecision
	}
	q := a.DivRound(b, places)
	return roundSignificant(q, Precision), nil
}

// percentage computes a*b/100 exactly by multiplying by 0.01 (a shift), avoiding
// the library's division-precision cap.
func percentage(a, b decimal.Decimal) decimal.Decimal {
	return a.Mul(b).Mul(decimal.New(1, -2))
}

// sqrt is Newton-Raphson in pure decimal (no float64 in any result digit),
// rounded to Precision at the boundary. Quadratic convergence reaches
// workPrecision in a few steps; the loop bound is just a safety net.
func sqrt(a decimal.Decimal) (decimal.Decimal, error) {
	switch a.Sign() {
	case -1:
		return decimal.Decimal{}, ErrNegativeSqrt
	case 0:
		return decimal.Zero, nil
	}

	half := decimal.New(5, -1) // 0.5
	// Seed at the right order of magnitude (~10^(adjExp/2)) for fast convergence.
	adjExp := adjustedExponent(a)
	x := decimal.New(1, adjExp/2)
	threshold := decimal.New(1, -workPrecision)

	for i := 0; i < 100; i++ {
		next := x.Add(a.DivRound(x, workPrecision)).Mul(half) // (x + a/x) / 2
		if next.Sub(x).Abs().LessThanOrEqual(threshold) {
			x = next
			break
		}
		x = next
	}
	return roundSignificant(x, Precision), nil
}

func pow(base, exp decimal.Decimal) (decimal.Decimal, error) {
	if exp.IsZero() {
		return decimal.New(1, 0), nil // adopt 0**0 = 1
	}

	if exp.IsInteger() {
		if exp.Sign() > 0 {
			return base.PowBigInt(exp.BigInt())
		}
		// Negative integer: 1 / base**|exp|; the reciprocal may be
		// non-terminating, so route it through the divide policy.
		if base.IsZero() {
			return decimal.Decimal{}, ErrDivideByZero
		}
		denom, err := base.PowBigInt(new(big.Int).Abs(exp.BigInt()))
		if err != nil {
			return decimal.Decimal{}, ErrUndefinedResult
		}
		return div(decimal.New(1, 0), denom)
	}

	// Fractional exponent: same precision policy as sqrt.
	if base.IsNegative() {
		return decimal.Decimal{}, ErrUndefinedResult // imaginary
	}
	if base.IsZero() {
		return decimal.Zero, nil
	}
	res, err := base.PowWithPrecision(exp, workPrecision)
	if err != nil {
		return decimal.Decimal{}, ErrUndefinedResult
	}
	return roundSignificant(res, Precision), nil
}

// roundSignificant rounds d to sig significant digits, half-to-even. shopspring
// rounds by decimal place, so we convert via the adjusted exponent.
func roundSignificant(d decimal.Decimal, sig int32) decimal.Decimal {
	if d.IsZero() {
		return decimal.Zero
	}
	places := sig - 1 - adjustedExponent(d)
	return d.RoundBank(places)
}

// adjustedExponent is the power of ten of d's most-significant digit. Cheap
// (no coefficient materialization); undefined for zero (callers guard it).
func adjustedExponent(d decimal.Decimal) int32 {
	return d.Exponent() + int32(d.NumDigits()) - 1
}

// stripTrailingZeros removes trailing fractional zeros without changing the
// value (0.5000 -> 0.5, 20.00 -> 20); integer-part zeros stay (100 -> 100).
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
