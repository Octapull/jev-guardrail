package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFreeCommandsNeedNoKey(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "")
	for _, args := range [][]string{{"demo"}, {"bench", "--preset", "injection"}, {"budget", "--ledger", filepath.Join(t.TempDir(), "budget.jsonl")}, {"help"}} {
		var out, errout bytes.Buffer
		if code := run(args, strings.NewReader(""), &out, &errout); code != 0 {
			t.Fatalf("%v => %d: %s", args, code, errout.String())
		}
		if out.Len() == 0 {
			t.Fatal("missing output")
		}
	}
}
func TestInitRefusesOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.yaml")
	var out, errout bytes.Buffer
	args := []string{"init", "--preset", "tool-safety", "--out", path}
	if run(args, nil, &out, &errout) != 0 {
		t.Fatal(errout.String())
	}
	if run(args, nil, &out, &errout) != 1 {
		t.Fatal("overwrote existing file")
	}
}
func TestEnvLoadsLiterally(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "")
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("TYPESAFE_API_KEY='literal-$(must-not-run)'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := loadEnv(path); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("TYPESAFE_API_KEY") != "literal-$(must-not-run)" {
		t.Fatal("env altered")
	}
}
