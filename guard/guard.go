// Package guard batches all policy rules into a single typed evaluation.
package guard

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/Octapull/jev-guardrail/client"
)

type Evaluator interface {
	Evaluate(context.Context, client.Request) (client.Response, error)
}
type Violation struct {
	Rule       string   `json:"rule"`
	Value      any      `json:"value"`
	Confidence *float64 `json:"confidence,omitempty"`
	Action     Action   `json:"action"`
}
type Verdict struct {
	Allowed    bool         `json:"allowed"`
	Decision   string       `json:"decision"`
	Violations []Violation  `json:"violations"`
	Uncertain  []string     `json:"uncertain"`
	LatencyMS  float64      `json:"latency_ms"`
	RequestID  string       `json:"request_id,omitempty"`
	Cached     bool         `json:"cached"`
	Usage      client.Usage `json:"usage"`
	Error      string       `json:"error,omitempty"`
}
type cacheEntry struct {
	key     [32]byte
	verdict Verdict
	expires time.Time
}
type Guard struct {
	policy    Policy
	evaluator Evaluator
	gate      chan struct{}
	cache     map[[32]byte]*list.Element
	order     *list.List
	cacheSize int
}

func New(p Policy, e Evaluator, cacheSize int) (*Guard, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if e == nil {
		return nil, errors.New("evaluator is required")
	}
	// Own a deep copy: mutations by a caller must not change a running policy.
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	return &Guard{policy: p, evaluator: e, gate: make(chan struct{}, 1), cache: map[[32]byte]*list.Element{}, order: list.New(), cacheSize: cacheSize}, nil
}
func clone(v Verdict) Verdict {
	v.Violations = append([]Violation{}, v.Violations...)
	for i := range v.Violations {
		if v.Violations[i].Confidence != nil {
			c := *v.Violations[i].Confidence
			v.Violations[i].Confidence = &c
		}
	}
	v.Uncertain = append([]string{}, v.Uncertain...)
	return v
}
func (g *Guard) Evaluate(ctx context.Context, state any) (v Verdict, err error) {
	start := time.Now()
	v = Verdict{Allowed: true, Decision: "allow", Violations: []Violation{}, Uncertain: []string{}}
	defer func() { v.LatencyMS = float64(time.Since(start).Microseconds()) / 1000 }()
	d, _ := time.ParseDuration(g.policy.Budget)
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	// A cancelable gate prevents duplicate concurrent bills and serializes the LRU.
	select {
	case g.gate <- struct{}{}:
		defer func() { <-g.gate }()
	case <-ctx.Done():
		return g.failure(v, ctx.Err())
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return g.failure(v, err)
	}
	if len(raw) > client.MaxRequestBytes {
		return g.failure(v, errors.New("state exceeds local size limit"))
	}
	key := sha256.Sum256(raw)
	if entry, ok := g.cache[key]; ok {
		e := entry.Value.(cacheEntry)
		if time.Now().Before(e.expires) {
			g.order.MoveToFront(entry)
			v = clone(e.verdict)
			v.Cached = true
			v.Usage = client.Usage{}
			return v, nil
		}
		delete(g.cache, key)
		g.order.Remove(entry)
	}
	// Evaluate exactly the immutable snapshot used for the cache key.
	response, err := g.evaluator.Evaluate(ctx, client.Request{Model: g.policy.Model, State: json.RawMessage(raw), Questions: g.policy.questions()})
	if err != nil {
		return g.failure(v, err)
	}
	v.RequestID = response.RequestID
	v.Usage = response.Usage
	for _, rule := range g.policy.Rules {
		answer, ok := response.Answers[rule.Name]
		if !ok {
			return g.failure(v, fmt.Errorf("missing answer for %s", rule.Name))
		}
		if err := validateAnswer(rule, answer); err != nil {
			return g.failure(v, err)
		}
		hit, uncertain := false, false
		var value any
		switch rule.Type {
		case "noul":
			value = *answer.Noul
			hit = *answer.Noul >= rule.Threshold
			uncertain = !hit && *answer.Noul > rule.ClearBelow
		case "choice":
			value = *answer.Choice
			for _, b := range rule.BlockOn {
				if b == *answer.Choice {
					hit = true
				}
			}
			uncertain = *answer.Confidence < rule.MinConfidence
		case "score":
			value = *answer.Score
			hit = (rule.Compare == "gte" && *answer.Score >= rule.Threshold) || (rule.Compare == "lte" && *answer.Score <= rule.Threshold)
			uncertain = *answer.Confidence < rule.MinConfidence
		}
		if uncertain {
			v.Uncertain = append(v.Uncertain, rule.Name)
			apply(&v, g.policy.OnUncertain)
			continue
		}
		if hit {
			v.Violations = append(v.Violations, Violation{Rule: rule.Name, Value: value, Confidence: answer.Confidence, Action: rule.Action})
			apply(&v, rule.Action)
		}
	}
	if g.cacheSize > 0 {
		g.cache[key] = g.order.PushFront(cacheEntry{key: key, verdict: clone(v), expires: time.Now().Add(5 * time.Minute)})
		if g.order.Len() > g.cacheSize {
			last := g.order.Back()
			delete(g.cache, last.Value.(cacheEntry).key)
			g.order.Remove(last)
		}
	}
	return v, nil
}
func apply(v *Verdict, a Action) {
	switch a {
	case Block:
		v.Allowed = false
		v.Decision = "block"
	case Review:
		v.Allowed = false
		if v.Decision != "block" {
			v.Decision = "review"
		}
	case Flag:
		if v.Decision == "allow" {
			v.Decision = "flag"
		}
	}
}
func (g *Guard) failure(v Verdict, err error) (Verdict, error) {
	v.Error = err.Error()
	if g.policy.FailMode == "closed" {
		apply(&v, Block)
	} else {
		apply(&v, Flag)
	}
	return v, err
}
func validateAnswer(r Rule, a client.Answer) error {
	bad := func() error { return fmt.Errorf("invalid %s answer for %s", r.Type, r.Name) }
	if a.Type != r.Type {
		return bad()
	}
	if r.Type == "noul" {
		if a.Noul == nil || !unit(*a.Noul) {
			return bad()
		}
		return nil
	}
	if a.Confidence == nil || !unit(*a.Confidence) || len(a.Probabilities) == 0 {
		return bad()
	}
	sum := 0.0
	for _, p := range a.Probabilities {
		if !unit(p) {
			return bad()
		}
		sum += p
	}
	if math.Abs(sum-1) > 0.02 {
		return bad()
	}
	if r.Type == "choice" {
		options := r.Criteria.(map[string]any)
		if a.Choice == nil || len(options) != len(a.Probabilities) {
			return bad()
		}
		if _, ok := options[*a.Choice]; !ok {
			return bad()
		}
		for k := range options {
			if _, ok := a.Probabilities[k]; !ok {
				return bad()
			}
			if a.Probabilities[k] > a.Probabilities[*a.Choice]+0.001 {
				return bad()
			}
		}
	} else {
		levels := r.Criteria.([]any)
		if a.Score == nil || math.IsNaN(*a.Score) || *a.Score < 0 || *a.Score > float64(len(levels)-1) || len(levels) != len(a.Probabilities) {
			return bad()
		}
		expected := 0.0
		for i := range levels {
			p, ok := a.Probabilities[strconv.Itoa(i)]
			if !ok {
				return bad()
			}
			expected += float64(i) * p
		}
		if math.Abs(expected-*a.Score) > 0.05 {
			return bad()
		}
	}
	return nil
}
