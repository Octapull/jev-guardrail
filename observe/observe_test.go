package observe

import (
	"bytes"
	"context"
	"errors"
	"github.com/Octapull/jev-guardrail/client"
	"github.com/Octapull/jev-guardrail/guard"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type fake struct{}

func (fake) Evaluate(context.Context, any) (guard.Verdict, error) {
	return guard.Verdict{Decision: "block", LatencyMS: 25, Usage: client.Usage{InputTokens: 10}, Error: "SECRET_RAW_ERROR"}, errors.New("SECRET_RAW_ERROR")
}
func TestMetricsAndLogsDoNotStoreContent(t *testing.T) {
	var buf bytes.Buffer
	o := New(&buf)
	wrapped := o.Wrap("input", fake{})
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = wrapped.Evaluate(context.Background(), "SECRET_INPUT") }()
	}
	wg.Wait()
	if strings.Contains(buf.String(), "SECRET") || strings.Count(buf.String(), "\n") != 10 {
		t.Fatal("sensitive data or missing logs")
	}
	w := httptest.NewRecorder()
	o.ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	for _, s := range []string{`jevguard_decisions_total{decision="block"} 10`, `jevguard_errors_total 10`, `jevguard_input_tokens_total 100`} {
		if !strings.Contains(w.Body.String(), s) {
			t.Fatalf("missing metric %s", s)
		}
	}
}
