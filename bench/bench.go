// Package bench measures original labeled examples without claiming production accuracy.
package bench

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Octapull/jev-guardrail/middleware"
	"math"
	"os"
	"sort"
)

//go:embed cases.jsonl
var defaults []byte

type Case struct {
	ID    string `json:"id"`
	State any    `json:"state"`
	Block bool   `json:"block"`
}
type Result struct {
	ID            string  `json:"id"`
	ExpectedBlock bool    `json:"expected_block"`
	Decision      string  `json:"decision"`
	LatencyMS     float64 `json:"latency_ms"`
	Error         bool    `json:"error"`
}
type Report struct {
	Cases         int      `json:"cases"`
	Decided       int      `json:"decided"`
	Correct       int      `json:"correct"`
	Reviews       int      `json:"reviews"`
	Errors        int      `json:"errors"`
	Interrupted   bool     `json:"interrupted"`
	TruePositive  int      `json:"true_positive"`
	FalsePositive int      `json:"false_positive"`
	TrueNegative  int      `json:"true_negative"`
	FalseNegative int      `json:"false_negative"`
	Accuracy      *float64 `json:"accuracy_on_decided"`
	Coverage      float64  `json:"decision_coverage"`
	P50MS         float64  `json:"p50_ms"`
	P95MS         float64  `json:"p95_ms"`
	InputTokens   int64    `json:"input_tokens"`
	EstimatedUSD  float64  `json:"estimated_usage_usd"`
	Results       []Result `json:"results"`
	Note          string   `json:"note"`
}

func Load(path string, limit int) ([]Case, error) {
	if limit < 1 || limit > 100 {
		return nil, errors.New("benchmark limit must be 1–100")
	}
	raw := defaults
	if path != "" {
		stat, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if stat.Size() > 1024*1024 {
			return nil, errors.New("dataset exceeds 1 MiB")
		}
		raw, err = os.ReadFile(path)
		if err != nil {
			return nil, err
		}
	}
	var out []Case
	seen := map[string]bool{}
	s := bufio.NewScanner(bytes.NewReader(raw))
	for s.Scan() {
		if len(bytes.TrimSpace(s.Bytes())) == 0 {
			continue
		}
		var row struct {
			ID    string `json:"id"`
			State any    `json:"state"`
			Block *bool  `json:"block"`
		}
		if err := json.Unmarshal(s.Bytes(), &row); err != nil {
			return nil, err
		}
		if row.ID == "" || seen[row.ID] || row.State == nil || row.Block == nil {
			return nil, fmt.Errorf("invalid or duplicate dataset case %q", row.ID)
		}
		seen[row.ID] = true
		out = append(out, Case{row.ID, row.State, *row.Block})
		if len(out) == limit {
			break
		}
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("empty dataset")
	}
	return out, nil
}
func Run(ctx context.Context, e middleware.Evaluator, cases []Case) Report {
	r := Report{Results: []Result{}, Note: "Small original fixture set, not a calibrated safety benchmark. Reviews are abstentions; errors excluded from accuracy. Usage estimate: $0.042/M input tokens, output free."}
	var times []float64
	for _, c := range cases {
		if ctx.Err() != nil {
			r.Interrupted = true
			break
		}
		v, err := e.Evaluate(ctx, c.State)
		r.Cases++
		r.InputTokens += v.Usage.InputTokens
		r.Results = append(r.Results, Result{c.ID, c.Block, v.Decision, v.LatencyMS, err != nil})
		if err != nil {
			r.Errors++
			continue
		}
		times = append(times, v.LatencyMS)
		if v.Decision == "review" {
			r.Reviews++
			continue
		}
		r.Decided++
		blocked := !v.Allowed
		if blocked == c.Block {
			r.Correct++
		}
		if blocked && c.Block {
			r.TruePositive++
		}
		if blocked && !c.Block {
			r.FalsePositive++
		}
		if !blocked && !c.Block {
			r.TrueNegative++
		}
		if !blocked && c.Block {
			r.FalseNegative++
		}
	}
	if r.Decided > 0 {
		acc := float64(r.Correct) / float64(r.Decided)
		r.Accuracy = &acc
	}
	if r.Cases > 0 {
		r.Coverage = float64(r.Decided) / float64(r.Cases)
	}
	sort.Float64s(times)
	if len(times) > 0 {
		r.P50MS = times[int(math.Ceil(float64(len(times))*0.5))-1]
		r.P95MS = times[int(math.Ceil(float64(len(times))*0.95))-1]
	}
	r.EstimatedUSD = float64(r.InputTokens) * 0.042 / 1e6
	return r
}
