package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type allowance struct {
	calls  int
	tokens int64
	deny   bool
}

func (a *allowance) Reserve() error {
	if a.deny {
		return errors.New("budget exhausted")
	}
	a.calls++
	return nil
}
func (a *allowance) RecordUsage(n int64) error { a.tokens += n; return nil }
func request() Request {
	return Request{State: "hello", Questions: map[string]Question{"safe": {Type: "noul", Instructions: "Is safe?"}}}
}
func TestWireFormatAndRetryAccounting(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-secret" || r.Method != "POST" {
			t.Error("bad auth/method")
		}
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if req.Model != "jev-latest" || len(req.Questions) != 1 {
			t.Error("invalid wire contract")
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(429)
			return
		}
		w.Header().Set("X-Request-ID", "request-1")
		_, _ = w.Write([]byte(`{"answers":{"safe":{"type":"noul","noul":0.2}},"usage":{"input_tokens":99}}`))
	}))
	defer s.Close()
	a := &allowance{}
	c, err := New(Config{APIKey: "test-secret", Endpoint: s.URL, Allowance: a, MaxRetries: 1})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Evaluate(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	if a.calls != 2 || a.tokens != 99 || resp.RequestID != "request-1" {
		t.Fatalf("retry/usage wrong: %+v %+v", a, resp)
	}
}
func TestNoCallsWithoutBudgetOrForOversizedInput(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer s.Close()
	a := &allowance{deny: true}
	c, _ := New(Config{APIKey: "test", Endpoint: s.URL, Allowance: a})
	if _, err := c.Evaluate(context.Background(), request()); err == nil {
		t.Fatal("missing budget rejection")
	}
	a.deny = false
	r := request()
	r.State = strings.Repeat("x", MaxRequestBytes+1)
	if _, err := c.Evaluate(context.Background(), r); err == nil {
		t.Fatal("missing size rejection")
	}
	if calls.Load() != 0 || a.calls != 0 {
		t.Fatal("network or reservation should not occur")
	}
}
func TestAuthErrorsNotRetriedOrEchoed(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte("test-secret"))
	}))
	defer s.Close()
	a := &allowance{}
	c, _ := New(Config{APIKey: "test-secret", Endpoint: s.URL, Allowance: a, MaxRetries: 2})
	_, err := c.Evaluate(context.Background(), request())
	if err == nil || strings.Contains(err.Error(), "test-secret") || a.calls != 1 {
		t.Fatalf("unsafe error/retry: %v %+v", err, a)
	}
}
func TestDeadlineAndNoRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("redirect followed") }))
	defer target.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer s.Close()
	a := &allowance{}
	c, _ := New(Config{APIKey: "test", Endpoint: s.URL, Allowance: a})
	if _, err := c.Evaluate(context.Background(), request()); err == nil {
		t.Fatal("redirect accepted")
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, err := c.Evaluate(ctx, request())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if a.calls != 1 {
		t.Fatal("expired request was reserved")
	}
}
