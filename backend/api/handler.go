// Package api exposes the calculator over HTTP: decoding, input validation,
// mapping calc domain errors to status+code, and JSON encoding. No arithmetic
// here — all math is in the calc package.
package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/shopspring/decimal"

	"calculator/calc"
)

// DefaultMaxBytes bounds the request body (requests are tiny). Enforced via
// http.MaxBytesReader so an oversized body yields 413, never an OOM.
const DefaultMaxBytes int64 = 64 << 10

// Stable client-facing error codes — contract surface (see SPEC).
const (
	codeBadRequest      = "BAD_REQUEST"
	codeUnknownOp       = "UNKNOWN_OP"
	codeMissingOperand  = "MISSING_OPERAND"
	codeInvalidNumber   = "INVALID_NUMBER"
	codeDivideByZero    = "DIVIDE_BY_ZERO"
	codeNegativeSqrt    = "NEGATIVE_SQRT"
	codeUndefinedResult = "UNDEFINED_RESULT"
	codePayloadTooLarge = "PAYLOAD_TOO_LARGE"
	codeOutOfRange      = "OPERAND_OUT_OF_RANGE"
	codeMethodNotAllow  = "METHOD_NOT_ALLOWED"
)

// calcRequest is the POST /api/calculate body. Operands are pointers so an
// absent/null field is distinct from "" (the missing-operand check) and cross
// the wire as strings, so no precision is lost before we parse them.
type calcRequest struct {
	Operation string  `json:"operation"`
	A         *string `json:"a"`
	B         *string `json:"b"`
}

// calcResponse is the success body; result is a string (same precision reason),
// precision its significant-digit count.
type calcResponse struct {
	Result    string `json:"result"`
	Precision int    `json:"precision"`
}

type errorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

// New returns the service handler with the default body-size limit.
func New() http.Handler { return NewWithLimit(DefaultMaxBytes) }

// NewWithLimit is New with a custom body-size limit, letting tests hit the 413
// path without a 64 KiB body.
func NewWithLimit(maxBytes int64) http.Handler {
	h := &handler{maxBytes: maxBytes}
	mux := http.NewServeMux()
	// Path-only patterns are less specific than the method-specific ones, so they
	// catch other methods and return our JSON 405 instead of the mux's plain text.
	mux.HandleFunc("POST /api/calculate", h.calculate)
	mux.HandleFunc("GET /health", h.health)
	mux.HandleFunc("/api/calculate", methodNotAllowed(http.MethodPost))
	mux.HandleFunc("/health", methodNotAllowed(http.MethodGet))
	return mux
}

// methodNotAllowed returns a 405 handler that sets Allow and reuses writeError,
// so a wrong method looks like every other error response.
func methodNotAllowed(allow string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Allow", allow)
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", codeMethodNotAllow)
	}
}

type handler struct {
	maxBytes int64
}

func (h *handler) health(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (h *handler) calculate(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, h.maxBytes)

	var req calcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large", codePayloadTooLarge)
			return
		}
		// Unparseable/empty body: generic message, no decoder leak, never 500.
		writeError(w, http.StatusBadRequest, "invalid request body", codeBadRequest)
		return
	}

	op := calc.Operation(req.Operation)
	binary, err := calc.IsBinary(op)
	if err != nil {
		writeError(w, http.StatusBadRequest, "unknown operation", codeUnknownOp)
		return
	}

	if req.A == nil {
		writeError(w, http.StatusBadRequest, "operand 'a' is required", codeMissingOperand)
		return
	}
	a, err := decimal.NewFromString(*req.A)
	if err != nil {
		writeError(w, http.StatusBadRequest, "operand 'a' is not a valid decimal number", codeInvalidNumber)
		return
	}

	var b decimal.Decimal
	if binary {
		if req.B == nil {
			writeError(w, http.StatusBadRequest, "operand 'b' is required for this operation", codeMissingOperand)
			return
		}
		b, err = decimal.NewFromString(*req.B)
		if err != nil {
			writeError(w, http.StatusBadRequest, "operand 'b' is not a valid decimal number", codeInvalidNumber)
			return
		}
	}

	result, err := calc.Calculate(op, a, b)
	if err != nil {
		writeCalcError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, calcResponse{
		Result:    result.String(),
		Precision: result.NumDigits(),
	})
}

// writeCalcError maps a calc sentinel error to its status+code. calc returns
// only client-caused errors, so every branch is 4xx; the default still avoids 500.
func writeCalcError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, calc.ErrDivideByZero):
		writeError(w, http.StatusBadRequest, "division by zero", codeDivideByZero)
	case errors.Is(err, calc.ErrNegativeSqrt):
		writeError(w, http.StatusBadRequest, "square root of a negative number", codeNegativeSqrt)
	case errors.Is(err, calc.ErrUnknownOperator):
		writeError(w, http.StatusBadRequest, "unknown operation", codeUnknownOp)
	case errors.Is(err, calc.ErrMissingOperand):
		writeError(w, http.StatusBadRequest, "missing operand", codeMissingOperand)
	case errors.Is(err, calc.ErrOutOfRange):
		writeError(w, http.StatusBadRequest, "operand magnitude out of range", codeOutOfRange)
	case errors.Is(err, calc.ErrUndefinedResult):
		writeError(w, http.StatusBadRequest, "result is undefined for the given operands", codeUndefinedResult)
	default:
		writeError(w, http.StatusBadRequest, "invalid request", codeBadRequest)
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, msg, code string) {
	writeJSON(w, status, errorResponse{Error: msg, Code: code})
}
