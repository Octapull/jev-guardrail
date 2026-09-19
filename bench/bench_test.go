package bench

import (
	"context"
	"errors"
	"github.com/Octapull/jev-guardrail/guard"
	"testing"
)

type fake struct{}

func (fake) Evaluate(_ context.Context, s any) (guard.Verdict, error) {
	switch s {
	case "review":
		return guard.Verdict{Decision: "review"}, nil
	case "error":
		return guard.Verdict{Decision: "block"}, errors.New("test")
	case "block":
		return guard.Verdict{Decision: "block", LatencyMS: 100}, nil
	default:
		return guard.Verdict{Decision: "allow", Allowed: true, LatencyMS: 200}, nil
	}
}
func TestReportDoesNotCountFailuresAsCorrectBlocks(t *testing.T) {
	r := Run(context.Background(), fake{}, []Case{{"1", "allow", false}, {"2", "block", true}, {"3", "review", true}, {"4", "error", true}, {"5", "allow", true}})
	if r.Decided != 3 || r.Correct != 2 || r.Errors != 1 || r.Reviews != 1 || r.FalseNegative != 1 || r.Coverage != .6 {
		t.Fatalf("%+v", r)
	}
}
func TestOriginalDataset(t *testing.T) {
	cases, err := Load("", 100)
	if err != nil || len(cases) != 30 {
		t.Fatal(len(cases), err)
	}
}
