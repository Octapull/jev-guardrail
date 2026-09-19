package middleware

import (
	"context"
	"errors"
	"io"
	"github.com/Octapull/jev-guardrail/guard"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type stub struct {
	allow bool
	err   error
	calls int
}

func (s *stub) Evaluate(context.Context, any) (guard.Verdict, error) {
	s.calls++
	return guard.Verdict{Allowed: s.allow, Decision: "allow"}, s.err
}
func TestHTTPRestoresBodyAndContext(t *testing.T) {
	e := &stub{allow: true}
	body := `{"message":"hello"}`
	h := HTTP(e)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if string(b) != body {
			t.Error("body lost")
		}
		if _, ok := Verdict(r.Context()); !ok {
			t.Error("verdict missing")
		}
		w.WriteHeader(204)
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/", strings.NewReader(body)))
	if w.Code != 204 || e.calls != 1 {
		t.Fatal(w.Code, e.calls)
	}
}
func TestHTTPRejectsBeforeNext(t *testing.T) {
	for _, tc := range []struct {
		body    string
		evalErr error
		status  int
	}{{`{}`, nil, 403}, {`{}`, errors.New("offline"), 503}, {`broken`, nil, 400}, {strings.Repeat("x", 17000), nil, 413}} {
		e := &stub{err: tc.evalErr}
		h := HTTP(e)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("next called") }))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/", strings.NewReader(tc.body)))
		if w.Code != tc.status {
			t.Fatal(w.Code, tc.status)
		}
	}
}
