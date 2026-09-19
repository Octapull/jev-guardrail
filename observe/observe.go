// Package observe provides content-free decision logs and Prometheus text metrics.
package observe

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"github.com/Octapull/jev-guardrail/guard"
	"github.com/Octapull/jev-guardrail/middleware"
	"net/http"
	"sync"
)

type Observer struct {
	mu                           sync.Mutex
	writer                       io.Writer
	counts                       map[string]int64
	errors, cacheHits, logErrors int64
	latency                      float64
	tokens                       int64
}

func New(w io.Writer) *Observer { return &Observer{writer: w, counts: map[string]int64{}} }

type wrapper struct {
	o     *Observer
	e     middleware.Evaluator
	stage string
}

func (o *Observer) Wrap(stage string, e middleware.Evaluator) middleware.Evaluator {
	return &wrapper{o: o, e: e, stage: stage}
}
func (w *wrapper) Evaluate(ctx context.Context, state any) (guard.Verdict, error) {
	v, err := w.e.Evaluate(ctx, state)
	o := w.o
	o.mu.Lock()
	defer o.mu.Unlock()
	o.counts[v.Decision]++
	o.latency += v.LatencyMS / 1000
	o.tokens += v.Usage.InputTokens
	if err != nil {
		o.errors++
	}
	if v.Cached {
		o.cacheHits++
	}
	if o.writer != nil {
		// Only decisions, never prompts, output, credentials, or raw API errors.
		entry := struct {
			Stage       string  `json:"stage"`
			Decision    string  `json:"decision"`
			Cached      bool    `json:"cached"`
			Latency     float64 `json:"latency_ms"`
			InputTokens int64   `json:"input_tokens"`
			Failed      bool    `json:"failed"`
		}{w.stage, v.Decision, v.Cached, v.LatencyMS, v.Usage.InputTokens, err != nil}
		if json.NewEncoder(o.writer).Encode(entry) != nil {
			o.logErrors++
		}
	}
	return v, err
}
func (o *Observer) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	o.mu.Lock()
	defer o.mu.Unlock()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintln(w, "# TYPE jevguard_decisions_total counter")
	var total int64
	for _, d := range []string{"allow", "flag", "review", "block"} {
		n := o.counts[d]
		total += n
		fmt.Fprintf(w, "jevguard_decisions_total{decision=%q} %d\n", d, n)
	}
	fmt.Fprintf(w, "jevguard_errors_total %d\njevguard_cache_hits_total %d\njevguard_log_errors_total %d\njevguard_input_tokens_total %d\njevguard_evaluation_seconds_sum %g\njevguard_evaluation_seconds_count %d\n", o.errors, o.cacheHits, o.logErrors, o.tokens, o.latency, total)
}
