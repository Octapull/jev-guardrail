// Package client implements the TypeSafe System One HTTP API.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const Endpoint = "https://api.typesafe.ai/v1/systemone"
const MaxRequestBytes = 16 * 1024

type Question struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}
type Request struct {
	Model     string              `json:"model"`
	State     any                 `json:"state"`
	Questions map[string]Question `json:"questions"`
}
type Answer struct {
	Type          string             `json:"type"`
	Noul          *float64           `json:"noul,omitempty"`
	Choice        *string            `json:"choice,omitempty"`
	Score         *float64           `json:"score,omitempty"`
	Confidence    *float64           `json:"confidence,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
}
type Usage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}
type Response struct {
	Model     string            `json:"model"`
	Answers   map[string]Answer `json:"answers"`
	Usage     Usage             `json:"usage"`
	RequestID string            `json:"request_id,omitempty"`
}
type Allowance interface {
	Reserve() error
	RecordUsage(int64) error
}
type Config struct {
	APIKey     string
	Endpoint   string
	HTTPClient *http.Client
	Allowance  Allowance
	MaxRetries int
}
type Client struct {
	cfg  Config
	http *http.Client
}
type APIError struct{ Status int }

func (e *APIError) Error() string { return fmt.Sprintf("TypeSafe API returned HTTP %d", e.Status) }

func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("TYPESAFE_API_KEY is missing; set it in .env or the environment")
	}
	if cfg.Allowance == nil {
		return nil, errors.New("budget allowance is required")
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = Endpoint
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1"))) {
		return nil, errors.New("endpoint must be HTTPS or a loopback test server")
	}
	if cfg.MaxRetries < 0 || cfg.MaxRetries > 2 {
		return nil, errors.New("retries must be between 0 and 2")
	}
	h := http.Client{Timeout: 10 * time.Second}
	if cfg.HTTPClient != nil {
		h = *cfg.HTTPClient
	}
	h.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{cfg: cfg, http: &h}, nil
}
func (c *Client) Evaluate(ctx context.Context, r Request) (Response, error) {
	var out Response
	if r.Model == "" {
		r.Model = "jev-latest"
	}
	if len(r.Questions) < 1 || len(r.Questions) > 16 {
		return out, errors.New("request needs 1–16 questions")
	}
	body, err := json.Marshal(r)
	if err != nil {
		return out, err
	}
	if len(body) > MaxRequestBytes {
		return out, fmt.Errorf("request exceeds %d bytes; shorten the state explicitly", MaxRequestBytes)
	}
	for attempt := 0; attempt <= c.cfg.MaxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.Endpoint, bytes.NewReader(body))
		if err != nil {
			return out, err
		}
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
		req.Header.Set("Content-Type", "application/json")
		if err = c.cfg.Allowance.Reserve(); err != nil {
			return out, err
		}
		resp, err := c.http.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			return out, errors.New("TypeSafe network request failed")
		}
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 1024*1024+1))
		resp.Body.Close()
		if readErr != nil || len(raw) > 1024*1024 {
			return out, errors.New("invalid or oversized TypeSafe response")
		}
		if resp.StatusCode == http.StatusOK {
			if err := json.Unmarshal(raw, &out); err != nil {
				return Response{}, errors.New("invalid TypeSafe JSON response")
			}
			if out.Answers == nil {
				return Response{}, errors.New("TypeSafe response is missing answers")
			}
			out.RequestID = resp.Header.Get("X-Request-ID")
			if err := c.cfg.Allowance.RecordUsage(out.Usage.InputTokens); err != nil {
				return Response{}, fmt.Errorf("record usage: %w", err)
			}
			return out, nil
		}
		retry := resp.StatusCode == 408 || resp.StatusCode == 429 || resp.StatusCode >= 500
		if !retry || attempt == c.cfg.MaxRetries {
			return out, &APIError{Status: resp.StatusCode}
		}
		delay := time.Duration(100*(1<<attempt)+rand.IntN(100)) * time.Millisecond
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return out, ctx.Err()
		case <-timer.C:
		}
	}
	return out, errors.New("request failed")
}
