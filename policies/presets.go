// Package policies embeds ready-to-edit starter policies.
package policies

import (
	"embed"
	"fmt"
	"github.com/Octapull/jev-guardrail/guard"
)

//go:embed *.yaml
var files embed.FS

var Names = []string{"chatbot", "injection", "toxicity", "output", "tool-safety", "off-topic"}

func Bytes(name string) ([]byte, error) {
	for _, n := range Names {
		if n == name {
			return files.ReadFile(name + ".yaml")
		}
	}
	return nil, fmt.Errorf("unknown preset %q; choose %v", name, Names)
}
func Load(name string) (guard.Policy, error) {
	b, err := Bytes(name)
	if err != nil {
		return guard.Policy{}, err
	}
	return guard.Parse(b)
}
