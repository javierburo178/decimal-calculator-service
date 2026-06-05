package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"calculator/httpapi"
)

// do issues a request against the handler and returns the recorder.
func do(t *testing.T, h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

type successBody struct {
	Result    string `json:"result"`
	Precision int    `json:"precision"`
}

type errorBody struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) errorBody {
	t.Helper()
	var e errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("response body is not the {error,code} shape: %q (%v)", rec.Body.String(), err)
	}
	return e
}

func decodeSuccess(t *testing.T, rec *httptest.ResponseRecorder) successBody {
	t.Helper()
	var s successBody
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatalf("response body is not the {result,precision} shape: %q (%v)", rec.Body.String(), err)
	}
	return s
}

// --- Unhappy-path matrix (SPEC). Each subtest is one numbered row. ---

func TestMatrix_ErrorRows(t *testing.T) {
	h := httpapi.New()

	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantCode   string // "" => only status is asserted
	}{
		{"row1: unparseable JSON body", `{"operation":"add", "a":`, http.StatusBadRequest, "BAD_REQUEST"},
		{"row2: empty body", ``, http.StatusBadRequest, "BAD_REQUEST"},
		{"row3: unknown operation", `{"operation":"modulo","a":"1","b":"2"}`, http.StatusBadRequest, "UNKNOWN_OP"},
		{"row4: missing b on binary op", `{"operation":"add","a":"1"}`, http.StatusBadRequest, "MISSING_OPERAND"},
		{"row5a: a not a valid decimal", `{"operation":"add","a":"abc","b":"2"}`, http.StatusBadRequest, "INVALID_NUMBER"},
		{"row5b: b not a valid decimal", `{"operation":"add","a":"1","b":"xyz"}`, http.StatusBadRequest, "INVALID_NUMBER"},
		{"row6: divide by zero", `{"operation":"divide","a":"1","b":"0"}`, http.StatusBadRequest, "DIVIDE_BY_ZERO"},
		{"row7: sqrt of negative", `{"operation":"sqrt","a":"-4"}`, http.StatusBadRequest, "NEGATIVE_SQRT"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, h, http.MethodPost, "/api/calculate", tc.body)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if rec.Code == http.StatusInternalServerError {
				t.Fatalf("known-bad input produced a 500: %s", rec.Body.String())
			}
			if tc.wantCode != "" {
				if got := decodeError(t, rec).Code; got != tc.wantCode {
					t.Fatalf("code = %q, want %q", got, tc.wantCode)
				}
			}
		})
	}
}

// Row 1 detail: a malformed body must NOT leak internal/decoder detail.
func TestMatrix_Row1_NoInternalLeak(t *testing.T) {
	rec := do(t, httpapi.New(), http.MethodPost, "/api/calculate", `{"operation":}`)
	e := decodeError(t, rec)
	if strings.Contains(strings.ToLower(e.Error), "json") ||
		strings.Contains(e.Error, "character") ||
		strings.Contains(e.Error, "offset") {
		t.Fatalf("error message leaks decoder internals: %q", e.Error)
	}
}

// Rows 8a/8b/8c: non-terminating results succeed and are reported with their
// 28-significant-digit value and precision.
func TestMatrix_Rows8_NonTerminatingSuccess(t *testing.T) {
	h := httpapi.New()
	const sqrt2 = "1.414213562373095048801688724"

	tests := []struct {
		name          string
		body          string
		wantResult    string
		wantPrecision int
	}{
		{"row8a: divide 1/3", `{"operation":"divide","a":"1","b":"3"}`, "0.3333333333333333333333333333", 28},
		{"row8b: sqrt 2", `{"operation":"sqrt","a":"2"}`, sqrt2, 28},
		{"row8c: power fractional 2^0.5", `{"operation":"power","a":"2","b":"0.5"}`, sqrt2, 28},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, h, http.MethodPost, "/api/calculate", tc.body)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
			}
			s := decodeSuccess(t, rec)
			if s.Result != tc.wantResult {
				t.Fatalf("result = %q, want %q", s.Result, tc.wantResult)
			}
			if s.Precision != tc.wantPrecision {
				t.Fatalf("precision = %d, want %d", s.Precision, tc.wantPrecision)
			}
		})
	}
}

// Row 9: a result with more places than the precision is rounded at the
// boundary (half-to-even). The exact even/odd proofs live in the calc package
// unit tests; here we confirm the policy is applied end-to-end (2/3 rounds the
// final digit up to 7).
func TestMatrix_Row9_RoundingApplied(t *testing.T) {
	rec := do(t, httpapi.New(), http.MethodPost, "/api/calculate", `{"operation":"divide","a":"2","b":"3"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	s := decodeSuccess(t, rec)
	const want = "0.6666666666666666666666666667"
	if s.Result != want {
		t.Fatalf("result = %q, want %q", s.Result, want)
	}
}

// Row 10: a non-POST method on /api/calculate yields 405 (method routing).
func TestMatrix_Row10_MethodNotAllowed(t *testing.T) {
	for _, m := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		rec := do(t, httpapi.New(), m, "/api/calculate", "")
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s /api/calculate status = %d, want 405", m, rec.Code)
		}
	}
}

// Row 11: an oversized body is rejected with 413 via MaxBytesReader, not OOM or
// 500. We use a tiny limit so the test body stays small.
func TestMatrix_Row11_OversizedPayload(t *testing.T) {
	h := httpapi.NewWithLimit(16)
	big := `{"operation":"add","a":"1","b":"2","pad":"` + strings.Repeat("x", 1000) + `"}`
	rec := do(t, h, http.MethodPost, "/api/calculate", big)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (body: %s)", rec.Code, rec.Body.String())
	}
	if got := decodeError(t, rec).Code; got != "PAYLOAD_TOO_LARGE" {
		t.Fatalf("code = %q, want PAYLOAD_TOO_LARGE", got)
	}
}

// Row 12: many concurrent requests share no mutable state. Run with -race to
// detect data races; here we also assert every response is correct.
func TestMatrix_Row12_Concurrent(t *testing.T) {
	h := httpapi.New()
	const n = 64
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			rec := do(t, h, http.MethodPost, "/api/calculate", `{"operation":"divide","a":"1","b":"3"}`)
			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want 200", rec.Code)
				return
			}
			if got := decodeSuccess(t, rec).Result; got != "0.3333333333333333333333333333" {
				t.Errorf("result = %q", got)
			}
		}()
	}
	wg.Wait()
}

// Row 13: a divide whose true value exceeds the precision cap returns a
// defined, deterministic value — exactly Precision significant digits — not
// silent garbage. Repeated calls are identical.
func TestMatrix_Row13_ExceedsPrecisionIsDefined(t *testing.T) {
	h := httpapi.New()
	const body = `{"operation":"divide","a":"1","b":"3"}`
	first := decodeSuccess(t, do(t, h, http.MethodPost, "/api/calculate", body))
	if first.Precision != 28 {
		t.Fatalf("precision = %d, want 28 (capped)", first.Precision)
	}
	if len(strings.TrimPrefix(first.Result, "0.")) != 28 {
		t.Fatalf("result %q does not carry exactly 28 significant digits", first.Result)
	}
	second := decodeSuccess(t, do(t, h, http.MethodPost, "/api/calculate", body))
	if first.Result != second.Result {
		t.Fatalf("non-deterministic result: %q vs %q", first.Result, second.Result)
	}
}

// Row 14 (operational safety — new row): magnitude-driven DoS vectors are
// rejected fast with 400 OPERAND_OUT_OF_RANGE. The magnitude of a decimal lives
// in its exponent, which is nearly free in bytes, so MaxBytesReader does not
// bound it; without the guard these requests hang the server while it
// materializes a multi-billion-digit coefficient. Each request is run with a
// hard deadline so a regression to a hang FAILS instead of stalling the suite.
func TestMatrix_Row14_MagnitudeGuard_DoS(t *testing.T) {
	h := httpapi.New()
	tests := []struct {
		name string
		body string
	}{
		{"power 10^1e9 (PowBigInt bomb)", `{"operation":"power","a":"10","b":"1000000000"}`},
		{"add 1e999999999 (exponent-align bomb)", `{"operation":"add","a":"1e999999999","b":"2"}`},
		{"add giant negative exponent operand", `{"operation":"add","a":"1e-999999999","b":"2"}`},
		{"power giant negative exponent", `{"operation":"power","a":"10","b":"-1000000000"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			type result struct {
				code int
				body string
			}
			done := make(chan result, 1)
			go func() {
				rec := do(t, h, http.MethodPost, "/api/calculate", tc.body)
				done <- result{rec.Code, rec.Body.String()}
			}()
			select {
			case r := <-done:
				if r.code != http.StatusBadRequest {
					t.Fatalf("status = %d, want 400 (body: %s)", r.code, r.body)
				}
				var e errorBody
				if err := json.Unmarshal([]byte(r.body), &e); err != nil {
					t.Fatalf("not the {error,code} shape: %q", r.body)
				}
				if e.Code != "OPERAND_OUT_OF_RANGE" {
					t.Fatalf("code = %q, want OPERAND_OUT_OF_RANGE", e.Code)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("request hung (>5s) — magnitude guard did not reject %q", tc.body)
			}
		})
	}
}

// Row 14 boundary: a value exactly at the magnitude limit succeeds; one decade
// past it is rejected. Mirrors the calc-level boundary test end-to-end.
func TestMatrix_Row14_MagnitudeBoundary(t *testing.T) {
	h := httpapi.New()

	in := do(t, h, http.MethodPost, "/api/calculate", `{"operation":"multiply","a":"1e1000","b":"1"}`)
	if in.Code != http.StatusOK {
		t.Fatalf("1e1000 is at the limit and must be accepted: status %d (%s)", in.Code, in.Body.String())
	}

	out := do(t, h, http.MethodPost, "/api/calculate", `{"operation":"multiply","a":"1e1001","b":"1"}`)
	if out.Code != http.StatusBadRequest {
		t.Fatalf("1e1001 is past the limit and must be rejected: status %d", out.Code)
	}
	if got := decodeError(t, out).Code; got != "OPERAND_OUT_OF_RANGE" {
		t.Fatalf("code = %q, want OPERAND_OUT_OF_RANGE", got)
	}
}

// --- Happy path + health, for completeness. ---

func TestHappyPath(t *testing.T) {
	rec := do(t, httpapi.New(), http.MethodPost, "/api/calculate", `{"operation":"add","a":"0.1","b":"0.2"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	s := decodeSuccess(t, rec)
	if s.Result != "0.3" {
		t.Fatalf("0.1 + 0.2 = %q, want \"0.3\" (decimal-safe)", s.Result)
	}
}

func TestSqrtIgnoresB(t *testing.T) {
	// b present but irrelevant for a unary op: must still succeed.
	rec := do(t, httpapi.New(), http.MethodPost, "/api/calculate", `{"operation":"sqrt","a":"9","b":null}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if got := decodeSuccess(t, rec).Result; got != "3" {
		t.Fatalf("sqrt(9) = %q, want \"3\"", got)
	}
}

func TestHealth(t *testing.T) {
	rec := do(t, httpapi.New(), http.MethodGet, "/health", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("health body = %q, want empty", rec.Body.String())
	}
}
