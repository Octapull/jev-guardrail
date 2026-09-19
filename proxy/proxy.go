// Package proxy implements a buffered, text-only Chat Completions safety gateway.
package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"github.com/Octapull/jev-guardrail/client"
	"github.com/Octapull/jev-guardrail/middleware"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Config struct {
	Upstream      string
	Input, Output middleware.Evaluator
	HTTPClient    *http.Client
}
type Handler struct {
	cfg    Config
	target string
	http   *http.Client
}

func New(cfg Config) (*Handler, error) {
	if cfg.Input == nil || cfg.Output == nil {
		return nil, errors.New("input and output guards are required")
	}
	u, err := url.Parse(cfg.Upstream)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("invalid upstream URL")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")) {
		return nil, errors.New("upstream requires HTTPS, except loopback servers")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(u.Path, "/v1") {
		u.Path += "/v1"
	}
	u.Path += "/chat/completions"
	h := http.Client{Timeout: 60 * time.Second}
	if cfg.HTTPClient != nil {
		h = *cfg.HTTPClient
	}
	h.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return &Handler{cfg: cfg, target: u.String(), http: &h}, nil
}
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/chat/completions" {
		middleware.Reject(w, 404, "unsupported_endpoint", nil)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		middleware.Reject(w, 405, "method_not_allowed", nil)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, client.MaxRequestBytes+1))
	r.Body.Close()
	if err != nil {
		middleware.Reject(w, 400, "unreadable_body", nil)
		return
	}
	if len(raw) > client.MaxRequestBytes {
		middleware.Reject(w, 413, "request_too_large", nil)
		return
	}
	var body map[string]any
	if json.Unmarshal(raw, &body) != nil || body == nil {
		middleware.Reject(w, 400, "invalid_json", nil)
		return
	}
	if s, ok := body["stream"]; ok && s != false {
		middleware.Reject(w, 400, "streaming_not_supported_use_stream_false", nil)
		return
	}
	if _, ok := body["audio"]; ok {
		middleware.Reject(w, 400, "text_only", nil)
		return
	}
	if m, ok := body["modalities"]; ok {
		b, _ := json.Marshal(m)
		if string(b) != "[\"text\"]" {
			middleware.Reject(w, 400, "text_only", nil)
			return
		}
	}
	messages, ok := body["messages"].([]any)
	if !ok || len(messages) == 0 {
		middleware.Reject(w, 400, "messages_required", nil)
		return
	}
	for _, m := range messages {
		if !textMessage(m) {
			middleware.Reject(w, 400, "text_only_messages_required", nil)
			return
		}
	}
	v, err := h.cfg.Input.Evaluate(r.Context(), body)
	if !v.Allowed {
		status := 403
		if err != nil {
			status = 503
		}
		middleware.Reject(w, status, "input_rejected", &v)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, h.target, bytes.NewReader(raw))
	if err != nil {
		middleware.Reject(w, 502, "upstream_request_failed", nil)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	// Only the caller's upstream credential is forwarded; the TypeSafe key stays in client.
	if auth := r.Header.Get("Authorization"); auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := h.http.Do(req)
	if err != nil {
		middleware.Reject(w, 502, "upstream_unavailable", nil)
		return
	}
	defer resp.Body.Close()
	output, err := io.ReadAll(io.LimitReader(resp.Body, client.MaxRequestBytes+1))
	if err != nil || len(output) > client.MaxRequestBytes {
		middleware.Reject(w, 502, "upstream_response_too_large_or_unreadable", nil)
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		middleware.Reject(w, 502, "upstream_non_success", nil)
		return
	}
	var data map[string]any
	if json.Unmarshal(output, &data) != nil {
		middleware.Reject(w, 502, "invalid_upstream_json", nil)
		return
	}
	choices, ok := data["choices"].([]any)
	if !ok || len(choices) == 0 {
		middleware.Reject(w, 502, "upstream_choices_required", nil)
		return
	}
	for _, c := range choices {
		choice, ok := c.(map[string]any)
		if !ok || !textMessage(choice["message"]) {
			middleware.Reject(w, 502, "unsupported_upstream_message", nil)
			return
		}
	}
	out, err := h.cfg.Output.Evaluate(r.Context(), map[string]any{"request": body, "assistant_output": data})
	if !out.Allowed {
		status := 403
		if err != nil {
			status = 503
		}
		middleware.Reject(w, status, "output_rejected", &out)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Jevguard-Input", v.Decision)
	w.Header().Set("X-Jevguard-Output", out.Decision)
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(output)
}
func textMessage(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	role, ok := m["role"].(string)
	if !ok {
		return false
	}
	switch role {
	case "system", "developer", "user", "assistant", "tool":
	default:
		return false
	}
	if _, ok := m["audio"]; ok {
		return false
	}
	switch c := m["content"].(type) {
	case string:
		return c != "" || m["tool_calls"] != nil
	case nil:
		return m["tool_calls"] != nil && role == "assistant"
	case []any:
		if len(c) == 0 {
			return false
		}
		for _, part := range c {
			p, ok := part.(map[string]any)
			if !ok || p["type"] != "text" {
				return false
			}
			if _, ok := p["text"].(string); !ok {
				return false
			}
		}
		return true
	default:
		return false
	}
}
