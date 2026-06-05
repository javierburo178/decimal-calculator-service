package calc

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"
)

// dec parses a decimal string for tests, failing loudly on a bad literal.
func dec(t *testing.T, s string) decimal.Decimal {
	t.Helper()
	v, err := decimal.NewFromString(s)
	if err != nil {
		t.Fatalf("invalid test decimal %q: %v", s, err)
	}
	return v
}

// TestCalculate exercises every operation and every success path with exact
// expected strings. Non-terminating results (divide 1/3, sqrt 2, fractional
// power) are verified against their full 28-significant-digit form.
func TestCalculate(t *testing.T) {
	const sqrt2 = "1.414213562373095048801688724" // 28 significant digits
	const oneThird = "0.3333333333333333333333333333"

	tests := []struct {
		name string
		op   Operation
		a, b string
		want string
	}{
		// add / subtract / multiply: exact, full precision, no rounding.
		{"add base-10 fraction (the 0.1+0.2 case)", Add, "0.1", "0.2", "0.3"},
		{"add negatives", Add, "-2.5", "-2.5", "-5"},
		{"subtract", Subtract, "5", "3", "2"},
		{"subtract to negative", Subtract, "1", "3", "-2"},
		{"multiply exact", Multiply, "1.5", "1.5", "2.25"},
		{"multiply tiny", Multiply, "0.1", "0.1", "0.01"},

		// divide: terminating -> exact; non-terminating -> 28 sig figs.
		{"divide terminating", Divide, "6", "3", "2"},
		{"divide half", Divide, "1", "2", "0.5"},
		{"divide non-terminating 1/3", Divide, "1", "3", oneThird},
		{"divide non-terminating 2/3 (rounds up at boundary)", Divide, "2", "3", "0.6666666666666666666666666667"},

		// sqrt: perfect squares exact; irrationals 28 sig figs.
		{"sqrt perfect square", Sqrt, "4", "", "2"},
		{"sqrt perfect square 9", Sqrt, "9", "", "3"},
		{"sqrt zero", Sqrt, "0", "", "0"},
		{"sqrt of small", Sqrt, "0.0001", "", "0.01"},
		{"sqrt irrational 2", Sqrt, "2", "", sqrt2},

		// power: positive int exact; zero; negative int; fractional.
		{"power positive int", Power, "2", "10", "1024"},
		{"power to zero", Power, "5", "0", "1"},
		{"power 0**0 convention", Power, "0", "0", "1"},
		{"power negative int terminating", Power, "2", "-1", "0.5"},
		{"power negative int non-terminating", Power, "3", "-1", oneThird},
		{"power fractional == sqrt", Power, "2", "0.5", sqrt2},
		{"power fractional perfect", Power, "9", "0.5", "3"},

		// percentage: x*p/100, exact.
		{"percentage", Percentage, "200", "10", "20"},
		{"percentage fractional exact", Percentage, "12.5", "8", "1"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Calculate(tc.op, dec(t, tc.a), dec(t, ifEmptyZero(tc.b)))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.String() != tc.want {
				t.Fatalf("Calculate(%s, %s, %s) = %q, want %q", tc.op, tc.a, tc.b, got.String(), tc.want)
			}
		})
	}
}

func ifEmptyZero(s string) string {
	if s == "" {
		return "0"
	}
	return s
}

// TestCalculateErrors covers every domain sentinel error.
func TestCalculateErrors(t *testing.T) {
	tests := []struct {
		name    string
		op      Operation
		a, b    string
		wantErr error
	}{
		{"divide by zero", Divide, "1", "0", ErrDivideByZero},
		{"divide zero by zero", Divide, "0", "0", ErrDivideByZero},
		{"sqrt of negative", Sqrt, "-4", "0", ErrNegativeSqrt},
		{"unknown operation", Operation("modulo"), "1", "2", ErrUnknownOperator},
		{"negative base fractional power (imaginary)", Power, "-4", "0.5", ErrUndefinedResult},
		{"zero to negative power (infinity)", Power, "0", "-2", ErrDivideByZero},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Calculate(tc.op, dec(t, tc.a), dec(t, tc.b))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Calculate(%s) error = %v, want %v", tc.op, err, tc.wantErr)
			}
		})
	}
}

// TestRoundSignificantHalfToEven proves banker's rounding at the boundary:
// halves round to the nearest even digit, in both directions.
func TestRoundSignificantHalfToEven(t *testing.T) {
	tests := []struct {
		name string
		in   string
		sig  int32
		want string
	}{
		{"2.5 -> 2 (down to even)", "2.5", 1, "2"},
		{"3.5 -> 4 (up to even)", "3.5", 1, "4"},
		{"4.5 -> 4 (down to even)", "4.5", 1, "4"},
		{"5.5 -> 6 (up to even)", "5.5", 1, "6"},
		{"0.125 -> 0.12 (down to even)", "0.125", 2, "0.12"},
		{"0.135 -> 0.14 (up to even)", "0.135", 2, "0.14"},
		{"non-half rounds normally", "0.6666", 2, "0.67"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := stripTrailingZeros(roundSignificant(dec(t, tc.in), tc.sig))
			if got.String() != tc.want {
				t.Fatalf("roundSignificant(%s, %d) = %q, want %q", tc.in, tc.sig, got.String(), tc.want)
			}
		})
	}
}

// TestNonTerminatingCappedAtPrecision is the SPEC's most important behavior and
// matrix row 13: a non-terminating quotient is deterministically capped at
// Precision significant digits — defined behavior, not silent garbage.
func TestNonTerminatingCappedAtPrecision(t *testing.T) {
	for _, op := range []struct {
		name string
		a, b string
	}{
		{"divide 1/3", "1", "3"},
		{"divide 2/7", "2", "7"},
		{"divide 10/3", "10", "3"},
	} {
		t.Run(op.name, func(t *testing.T) {
			got, err := Calculate(Divide, dec(t, op.a), dec(t, op.b))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if int32(got.NumDigits()) > Precision {
				t.Fatalf("result %q has %d significant digits, exceeds cap %d", got.String(), got.NumDigits(), Precision)
			}
		})
	}
}

// TestSqrtRoundTrip independently checks irrational sqrt accuracy: squaring the
// result must be within one unit at the last retained place of the input.
func TestSqrtRoundTrip(t *testing.T) {
	for _, in := range []string{"2", "3", "5", "10", "0.5"} {
		root, err := Calculate(Sqrt, dec(t, in), decimal.Zero)
		if err != nil {
			t.Fatalf("sqrt(%s): %v", in, err)
		}
		diff := root.Mul(root).Sub(dec(t, in)).Abs()
		if diff.GreaterThan(decimal.New(1, -25)) {
			t.Fatalf("sqrt(%s)=%s squares to off-by %s (too large)", in, root, diff)
		}
	}
}

// TestMagnitudeGuard covers the operational-safety guard (matrix row 14):
// out-of-range operands and oversized power exponents are rejected with
// ErrOutOfRange before any computation, while values at the limit still
// compute. This is what prevents the unbounded-materialization DoS.
func TestMagnitudeGuard(t *testing.T) {
	tests := []struct {
		name    string
		op      Operation
		a, b    string
		wantErr error // nil => must succeed
	}{
		{"operand a above max magnitude", Add, "1e1001", "2", ErrOutOfRange},
		{"operand b above max magnitude", Add, "2", "1e1001", ErrOutOfRange},
		{"operand below min magnitude", Add, "1e-1001", "2", ErrOutOfRange},
		{"power exponent above cap", Power, "10", "1001", ErrOutOfRange},
		{"power exponent below -cap", Power, "10", "-1001", ErrOutOfRange},
		{"power huge low-magnitude exponent (1e9)", Power, "10", "1000000000", ErrOutOfRange},

		// Boundary: exactly at the limit is accepted.
		{"operand a at max magnitude", Add, "1e1000", "0", nil},
		{"operand at min magnitude", Add, "1e-1000", "0", nil},
		{"power exponent at cap", Power, "1", "1000", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Calculate(tc.op, dec(t, tc.a), dec(t, tc.b))
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("Calculate(%s,%s,%s) = unexpected error %v", tc.op, tc.a, tc.b, err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Calculate(%s,%s,%s) error = %v, want %v", tc.op, tc.a, tc.b, err, tc.wantErr)
			}
		})
	}
}

// TestIsBinary documents the arity contract used by the transport layer.
func TestIsBinary(t *testing.T) {
	tests := []struct {
		op      Operation
		want    bool
		wantErr error
	}{
		{Add, true, nil},
		{Subtract, true, nil},
		{Multiply, true, nil},
		{Divide, true, nil},
		{Power, true, nil},
		{Percentage, true, nil},
		{Sqrt, false, nil},
		{Operation("nope"), false, ErrUnknownOperator},
	}
	for _, tc := range tests {
		got, err := IsBinary(tc.op)
		if got != tc.want || !errors.Is(err, tc.wantErr) {
			t.Fatalf("IsBinary(%s) = (%v, %v), want (%v, %v)", tc.op, got, err, tc.want, tc.wantErr)
		}
	}
}
