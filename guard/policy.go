package guard

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"time"

	"gopkg.in/yaml.v3"
	"github.com/Octapull/jev-guardrail/client"
)

type Action string

const (
	Block  Action = "block"
	Flag   Action = "flag"
	Review Action = "review"
)

type Rule struct {
	Name          string   `yaml:"name" json:"name"`
	Type          string   `yaml:"type" json:"type"`
	Instructions  string   `yaml:"instructions" json:"instructions"`
	Criteria      any      `yaml:"criteria,omitempty" json:"criteria,omitempty"`
	Threshold     float64  `yaml:"threshold" json:"threshold"`
	ClearBelow    float64  `yaml:"clear_below" json:"clear_below"`
	MinConfidence float64  `yaml:"min_confidence" json:"min_confidence"`
	BlockOn       []string `yaml:"block_on,omitempty" json:"block_on,omitempty"`
	Compare       string   `yaml:"compare,omitempty" json:"compare,omitempty"`
	Action        Action   `yaml:"action" json:"action"`
}
type Policy struct {
	Name        string `yaml:"name" json:"name"`
	Model       string `yaml:"model" json:"model"`
	Budget      string `yaml:"budget" json:"budget"`
	FailMode    string `yaml:"fail_mode" json:"fail_mode"`
	OnUncertain Action `yaml:"on_uncertain" json:"on_uncertain"`
	Rules       []Rule `yaml:"rules" json:"rules"`
}

func validAction(a Action) bool { return a == Block || a == Flag || a == Review }
func unit(f float64) bool       { return !math.IsNaN(f) && !math.IsInf(f, 0) && f >= 0 && f <= 1 }
func (p Policy) Validate() error {
	if p.Model != "jev-latest" && p.Model != "jev-1.13.0" {
		return errors.New("supported budget-priced models: jev-latest, jev-1.13.0")
	}
	d, err := time.ParseDuration(p.Budget)
	if err != nil || d <= 0 || d > 30*time.Second {
		return errors.New("budget must be a duration in (0, 30s]")
	}
	if p.FailMode != "closed" && p.FailMode != "open" {
		return errors.New("fail_mode must be closed or open")
	}
	if !validAction(p.OnUncertain) {
		return errors.New("on_uncertain must be block, review or flag")
	}
	if len(p.Rules) == 0 || len(p.Rules) > 16 {
		return errors.New("policy must contain 1–16 rules")
	}
	seen := map[string]bool{}
	for _, r := range p.Rules {
		bad := func(reason string) error { return fmt.Errorf("rule %q: %s", r.Name, reason) }
		if r.Name == "" || seen[r.Name] {
			return bad("name must be unique and nonempty")
		}
		seen[r.Name] = true
		if r.Instructions == "" || !validAction(r.Action) || !unit(r.MinConfidence) {
			return bad("instructions, action or min_confidence invalid")
		}
		if math.IsNaN(r.Threshold) || math.IsInf(r.Threshold, 0) {
			return bad("invalid threshold")
		}
		switch r.Type {
		case "noul":
			if !unit(r.Threshold) || !unit(r.ClearBelow) || r.ClearBelow >= r.Threshold {
				return bad("requires 0 <= clear_below < threshold <= 1")
			}
			if r.MinConfidence != 0 {
				return bad("noul has no confidence; use clear_below and threshold")
			}
		case "choice":
			choices, ok := r.Criteria.(map[string]any)
			if !ok || len(choices) < 1 || len(choices) > 255 {
				return bad("choice criteria must have 1–255 options")
			}
			if len(r.BlockOn) == 0 {
				return bad("choice requires block_on")
			}
			for _, key := range r.BlockOn {
				if _, ok := choices[key]; !ok {
					return bad("block_on references unknown option")
				}
			}
		case "score":
			levels, ok := r.Criteria.([]any)
			if !ok || len(levels) < 2 || len(levels) > 10 {
				return bad("score criteria must have 2–10 ordered levels")
			}
			if r.Threshold < 0 || r.Threshold > float64(len(levels)-1) {
				return bad("score threshold outside rubric")
			}
			if r.Compare != "gte" && r.Compare != "lte" {
				return bad("score compare must be gte or lte")
			}
		default:
			return bad("type must be noul, choice or score")
		}
	}
	return nil
}
func Parse(data []byte) (Policy, error) {
	p := Policy{Model: "jev-latest", Budget: "3s", FailMode: "closed", OnUncertain: Review}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		return p, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return p, errors.New("expected one YAML policy document")
	}
	// Normalize YAML criteria into the JSON types accepted by the API.
	for i := range p.Rules {
		b, err := json.Marshal(p.Rules[i].Criteria)
		if err != nil {
			return p, err
		}
		if err = json.Unmarshal(b, &p.Rules[i].Criteria); err != nil {
			return p, err
		}
	}
	return p, p.Validate()
}
func Load(path string) (Policy, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Policy{}, err
	}
	return Parse(b)
}
func (p Policy) questions() map[string]client.Question {
	q := make(map[string]client.Question, len(p.Rules))
	for _, r := range p.Rules {
		q[r.Name] = client.Question{Type: r.Type, Instructions: r.Instructions, Criteria: r.Criteria}
	}
	return q
}
