package guard_test

import (
	"context"
	"errors"
	"github.com/Octapull/jev-guardrail/client"
	"github.com/Octapull/jev-guardrail/guard"
	"github.com/Octapull/jev-guardrail/policies"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type evaluatorFunc func(context.Context, client.Request) (client.Response, error)

func (f evaluatorFunc) Evaluate(ctx context.Context, r client.Request) (client.Response, error) {
	return f(ctx, r)
}
func ptr[T any](v T) *T { return &v }
func TestNoulDecisionBands(t *testing.T) {
	for _, tc := range []struct {
		p        float64
		decision string
		allowed  bool
	}{{0, "allow", true}, {0.3, "allow", true}, {0.31, "review", false}, {0.7, "block", false}, {1, "block", false}} {
		p, _ := policies.Load("injection")
		e := evaluatorFunc(func(_ context.Context, r client.Request) (client.Response, error) {
			return client.Response{Answers: map[string]client.Answer{"prompt_injection": {Type: "noul", Noul: ptr(tc.p)}}}, nil
		})
		g, err := guard.New(p, e, 0)
		if err != nil {
			t.Fatal(err)
		}
		v, err := g.Evaluate(context.Background(), "x")
		if err != nil || v.Decision != tc.decision || v.Allowed != tc.allowed {
			t.Fatalf("p=%v: %+v %v", tc.p, v, err)
		}
	}
}
func TestMalformedResponsesAndFailModes(t *testing.T) {
	for _, mode := range []string{"open", "closed"} {
		for _, answer := range []client.Answer{{}, {Type: "noul"}, {Type: "noul", Noul: ptr(1.1)}} {
			p, _ := policies.Load("injection")
			p.FailMode = mode
			e := evaluatorFunc(func(_ context.Context, _ client.Request) (client.Response, error) {
				return client.Response{Answers: map[string]client.Answer{"prompt_injection": answer}}, nil
			})
			g, _ := guard.New(p, e, 0)
			v, err := g.Evaluate(context.Background(), "x")
			if err == nil || v.Allowed != (mode == "open") {
				t.Fatalf("mode %s: %+v %v", mode, v, err)
			}
		}
	}
}
func TestOneBatchAndConcurrentCache(t *testing.T) {
	p, _ := policies.Load("chatbot")
	var calls atomic.Int32
	e := evaluatorFunc(func(_ context.Context, r client.Request) (client.Response, error) {
		calls.Add(1)
		if len(r.Questions) != 3 {
			t.Error("not batched")
		}
		answers := map[string]client.Answer{}
		for name := range r.Questions {
			answers[name] = client.Answer{Type: "noul", Noul: ptr(0.0)}
		}
		return client.Response{Answers: answers, Usage: client.Usage{InputTokens: 100}}, nil
	})
	g, _ := guard.New(p, e, 2)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := g.Evaluate(context.Background(), map[string]any{"message": "same"})
			if err != nil || !v.Allowed {
				t.Error(err)
			}
			if v.Cached && v.Usage.InputTokens != 0 {
				t.Error("cache charged usage")
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("expected one provider call, got %d", calls.Load())
	}
	for _, s := range []string{"other", "third", "same"} {
		_, _ = g.Evaluate(context.Background(), map[string]any{"message": s})
	}
	if calls.Load() != 4 {
		t.Fatal("LRU eviction failed")
	}
}
func TestScoreAndChoice(t *testing.T) {
	for _, tc := range []struct {
		preset, rule string
		answer       client.Answer
		decision     string
	}{
		{"toxicity", "toxicity", client.Answer{Type: "score", Score: ptr(1.8), Confidence: ptr(.8), Probabilities: map[string]float64{"0": 0, "1": .2, "2": .8}}, "block"},
		{"toxicity", "toxicity", client.Answer{Type: "score", Score: ptr(1.0), Confidence: ptr(.2), Probabilities: map[string]float64{"0": .5, "1": 0, "2": .5}}, "review"},
		{"off-topic", "topic", client.Answer{Type: "choice", Choice: ptr("in_scope"), Confidence: ptr(1.0), Probabilities: map[string]float64{"in_scope": 1, "borderline": 0, "out_of_scope": 0}}, "allow"},
		{"off-topic", "topic", client.Answer{Type: "choice", Choice: ptr("out_of_scope"), Confidence: ptr(1.0), Probabilities: map[string]float64{"in_scope": 0, "borderline": 0, "out_of_scope": 1}}, "review"},
	} {
		p, _ := policies.Load(tc.preset)
		g, err := guard.New(p, evaluatorFunc(func(_ context.Context, _ client.Request) (client.Response, error) {
			return client.Response{Answers: map[string]client.Answer{tc.rule: tc.answer}}, nil
		}), 0)
		if err != nil {
			t.Fatal(err)
		}
		v, err := g.Evaluate(context.Background(), "x")
		if err != nil || v.Decision != tc.decision {
			t.Fatalf("%s: %+v %v", tc.preset, v, err)
		}
	}
}
func TestDeadlineAndErrorsNeverCached(t *testing.T) {
	p, _ := policies.Load("injection")
	p.Budget = "5ms"
	calls := 0
	e := evaluatorFunc(func(ctx context.Context, _ client.Request) (client.Response, error) {
		calls++
		<-ctx.Done()
		return client.Response{}, ctx.Err()
	})
	g, _ := guard.New(p, e, 10)
	for range 2 {
		v, err := g.Evaluate(context.Background(), "same")
		if !errors.Is(err, context.DeadlineExceeded) || v.Allowed {
			t.Fatal(v, err)
		}
	}
	if calls != 2 {
		t.Fatal("errors cached")
	}
}
func TestPresetAndPolicyValidation(t *testing.T) {
	for _, n := range policies.Names {
		if _, err := policies.Load(n); err != nil {
			t.Fatalf("%s: %v", n, err)
		}
	}
	b, _ := policies.Bytes("injection")
	for _, raw := range []string{string(b) + "unknown: true\n", string(b) + "---\nfoo: bar\n", strings.Replace(string(b), "type: noul", "type: typo", 1), strings.Replace(string(b), "threshold: 0.7", "threshold: .nan", 1), strings.Replace(string(b), "threshold: 0.7", "min_confidence: 0.5\n    threshold: 0.7", 1)} {
		if _, err := guard.Parse([]byte(raw)); err == nil {
			t.Fatal("invalid policy accepted")
		}
	}
}
func TestWaitingForGateHonorsDeadline(t *testing.T) {
	p, _ := policies.Load("injection")
	entered := make(chan struct{})
	release := make(chan struct{})
	e := evaluatorFunc(func(_ context.Context, _ client.Request) (client.Response, error) {
		close(entered)
		<-release
		return client.Response{}, errors.New("test")
	})
	g, _ := guard.New(p, e, 0)
	done := make(chan struct{})
	go func() { defer close(done); _, _ = g.Evaluate(context.Background(), "first") }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	_, err := g.Evaluate(ctx, "second")
	close(release)
	<-done
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
