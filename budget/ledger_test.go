package budget

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestPersistentConcurrentLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 25 {
				err := (&Ledger{Path: path}).Reserve()
				if err != nil && !errors.Is(err, ErrExhausted) {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	l := &Ledger{Path: path}
	s, err := l.Status()
	if err != nil {
		t.Fatal(err)
	}
	if s.Attempts != 450 || s.RemainingAttempts != 0 || s.ReservedUSD != 4.5 {
		t.Fatalf("wrong limit: %+v", s)
	}
	if !errors.Is(l.Reserve(), ErrExhausted) {
		t.Fatal("must reject attempt 451")
	}
	if err = l.RecordUsage(1000); err != nil {
		t.Fatal(err)
	}
	s, err = l.Status()
	if err != nil {
		t.Fatal(err)
	}
	if s.InputTokens != 1000 || s.ReservedUSD != 4.5 {
		t.Fatalf("usage must not refund allowance: %+v", s)
	}
}
func TestCorruptLedgerFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	if err := os.WriteFile(path, []byte("{incomplete\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := (&Ledger{Path: path}).Reserve(); err == nil {
		t.Fatal("corrupt ledger accepted")
	}
}
