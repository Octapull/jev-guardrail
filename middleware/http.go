// Package middleware adapts guards to net/http and compatible routers.
package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"github.com/Octapull/jev-guardrail/client"
	"github.com/Octapull/jev-guardrail/guard"
	"net/http"
)

type Evaluator interface {
	Evaluate(context.Context, any) (guard.Verdict, error)
}
type verdictKey struct{}

func Verdict(ctx context.Context) (guard.Verdict, bool) {
	v, ok := ctx.Value(verdictKey{}).(guard.Verdict)
	return v, ok
}
func Reject(w http.ResponseWriter, status int, kind string, v *guard.Verdict) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error   string         `json:"error"`
		Verdict *guard.Verdict `json:"verdict,omitempty"`
	}{kind, v})
}

// HTTP evaluates a JSON request body, restores its bytes, and attaches the verdict.
func HTTP(g Evaluator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(io.LimitReader(r.Body, client.MaxRequestBytes+1))
			r.Body.Close()
			if err != nil {
				Reject(w, 400, "unreadable_body", nil)
				return
			}
			if len(body) > client.MaxRequestBytes {
				Reject(w, 413, "body_too_large", nil)
				return
			}
			var state any
			if json.Unmarshal(body, &state) != nil || state == nil {
				Reject(w, 400, "expected_json_state", nil)
				return
			}
			v, err := g.Evaluate(r.Context(), state)
			if !v.Allowed {
				status := 403
				if err != nil {
					status = 503
				}
				Reject(w, status, "guard_rejected", &v)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			w.Header().Set("X-Jevguard-Decision", v.Decision)
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), verdictKey{}, v)))
		})
	}
}
