package proxy

import (
	"context"
	"encoding/json"
	"github.com/Octapull/jev-guardrail/guard"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type stubGuard struct {
	deny  bool
	calls atomic.Int32
	state any
}

func (g *stubGuard) Evaluate(_ context.Context, state any) (guard.Verdict, error) {
	g.calls.Add(1)
	g.state = state
	d := "allow"
	if g.deny {
		d = "block"
	}
	return guard.Verdict{Allowed: !g.deny, Decision: d}, nil
}

const input = `{"model":"local","messages":[{"role":"user","content":"hello"}],"stream":false}`
const output = `{"id":"chat-1","choices":[{"message":{"role":"assistant","content":"hello back"},"finish_reason":"stop"}]}`

func TestBothGuardsAndPreservedResponse(t *testing.T) {
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer upstream-only" || r.Header.Get("Cookie") != "" {
			t.Error("upstream path/auth/header mismatch")
		}
		_, _ = w.Write([]byte(output))
	}))
	defer up.Close()
	ig, og := &stubGuard{}, &stubGuard{}
	h, err := New(Config{Upstream: up.URL + "/v1", Input: ig, Output: og})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(input))
	req.Header.Set("Authorization", "Bearer upstream-only")
	req.Header.Set("Cookie", "secret-cookie")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 || w.Body.String() != output || ig.calls.Load() != 1 || og.calls.Load() != 1 || calls.Load() != 1 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	state := og.state.(map[string]any)
	if state["request"] == nil || state["assistant_output"] == nil {
		t.Fatal("output guard missing context")
	}
}
func TestBlockedInputDoesNotReachUpstream(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("blocked input forwarded") }))
	defer up.Close()
	h, _ := New(Config{Upstream: up.URL, Input: &stubGuard{deny: true}, Output: &stubGuard{}})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(input)))
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}
func TestBlockedOutputNeverLeaks(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Replace(output, "hello back", "SECRET_UNSAFE_RESPONSE", 1)))
	}))
	defer up.Close()
	h, _ := New(Config{Upstream: up.URL, Input: &stubGuard{}, Output: &stubGuard{deny: true}})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(input)))
	if w.Code != 403 || strings.Contains(w.Body.String(), "SECRET_UNSAFE_RESPONSE") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}
func TestUnsupportedInputsMakeNoCalls(t *testing.T) {
	ig, og := &stubGuard{}, &stubGuard{}
	h, _ := New(Config{Upstream: "http://127.0.0.1:1", Input: ig, Output: og})
	for _, body := range []string{
		strings.Replace(input, "false", "true", 1),
		`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.com/image"}}]}]}`,
		`{"messages":[]}`, `{broken`, strings.Repeat("x", 17000),
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body)))
		if w.Code != 400 && w.Code != 413 {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
	}
	if ig.calls.Load() != 0 || og.calls.Load() != 0 {
		t.Fatal("unsupported request billed")
	}
}
func TestMalformedUpstreamRejected(t *testing.T) {
	for _, body := range []string{`{}`, `{"choices":[]}`, `{"choices":[{"message":{"role":"assistant","content":[{"type":"image_url"}]}}]}`} {
		up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		ig, og := &stubGuard{}, &stubGuard{}
		h, _ := New(Config{Upstream: up.URL, Input: ig, Output: og})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(input)))
		up.Close()
		if w.Code != 502 || og.calls.Load() != 0 {
			t.Fatalf("%d", w.Code)
		}
	}
}
func TestTextToolCallsAccepted(t *testing.T) {
	var v any
	_ = json.Unmarshal([]byte(`{"role":"assistant","content":null,"tool_calls":[{"type":"function","function":{"name":"search","arguments":"{}"}}]}`), &v)
	if !textMessage(v) {
		t.Fatal("textual tool call rejected")
	}
}
