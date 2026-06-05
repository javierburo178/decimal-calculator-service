// Package httpapi exposes the calculator over HTTP. It owns request decoding,
// input validation, mapping of calc domain errors to stable HTTP status codes
// and error codes, and JSON encoding. No arithmetic lives here — all math is in
// the calc package.
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/shopspring/decimal"

	"calculator/calc"
)

// DefaultMaxBytes bounds the request body size. A calculator request is tiny
// (an operation plus two decimal strings); 64 KiB leaves generous room for very
// long operands while preventing unbounded-body abuse. Enforced with
// http.MaxBytesReader so an oversized body yields 413, never an OOM.
const DefaultMaxBytes int64 = 64 << 10

// Stable client-facing error codes. These are contract surface (see SPEC).
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

// calcRequest is the POST /api/calculate body. Operands are pointers to
// distinguish an absent/null field from an empty string, which matters for the
// missing-operand check. They cross the wire as STRINGS, not JSON numbers, so a
// client cannot lose precision before the value ever reaches us.
type calcRequest struct {
	Operation string  `json:"operation"`
	A         *string `json:"a"`
	B         *string `json:"b"`
}

// calcResponse is the success body. result is a STRING for the same
// precision-preservation reason; precision is the number of significant digits
// the result carries.
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

// NewWithLimit returns the service handler with a custom maximum request body
// size. Exposed mainly so tests can exercise the oversized-payload path without
// constructing a 64 KiB body.
func NewWithLimit(maxBytes int64) http.Handler {
	h := &handler{maxBytes: maxBytes}
	mux := http.NewServeMux()
	// Go 1.22+ method-aware routing. The method-specific patterns serve the
	// valid verb; the path-only patterns are strictly less specific, so they
	// catch every *other* method and emit our {error,code} JSON 405 instead of
	// the mux's plain-text default — keeping the error shape consistent.
	mux.HandleFunc("POST /api/calculate", h.calculate)
	mux.HandleFunc("GET /health", h.health)
	mux.HandleFunc("/api/calculate", methodNotAllowed(http.MethodPost))
	mux.HandleFunc("/health", methodNotAllowed(http.MethodGet))
	return mux
}

// methodNotAllowed returns a handler for a disallowed method on a route whose
// only valid verb is allow. It preserves the Allow header and reuses the shared
// JSON error helper so a 405 looks like every other error response.
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
		// Unparseable or empty body. Deliberately generic: we never echo the
		// decoder error (no internal leak) and never return 500 for bad input.
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

// writeCalcError maps a calc sentinel error to its stable HTTP status + code.
// calc only ever returns sentinel (client-caused) errors, so every branch is a
// 4xx; the default arm still avoids 500 rather than leaking an unexpected fault.
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
