// Package budget enforces a conservative, persistent allowance before network I/O.
// Ledger files must be retained and shared by all processes using the same key.
package budget

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const MaxCents = 450
const CentsPerAttempt = 1

var ErrExhausted = errors.New("local gift-credit allowance exhausted; no API request sent")

type Ledger struct{ Path string }
type event struct {
	Kind        string    `json:"kind"`
	At          time.Time `json:"at"`
	Cents       int       `json:"cents,omitempty"`
	InputTokens int64     `json:"input_tokens,omitempty"`
}
type Status struct {
	LimitUSD          float64 `json:"limit_usd"`
	ReservedUSD       float64 `json:"reserved_usd"`
	RemainingUSD      float64 `json:"remaining_usd"`
	Attempts          int     `json:"attempts"`
	InputTokens       int64   `json:"reported_input_tokens"`
	EstimatedUSD      float64 `json:"estimated_usage_usd"`
	RemainingAttempts int     `json:"remaining_attempts"`
}

// transaction locks a separate stable inode, so concurrent CLI processes cannot overspend.
func (l *Ledger) transaction(fn func(*os.File, *Status) error) (Status, error) {
	s := Status{LimitUSD: 4.5, RemainingUSD: 4.5, RemainingAttempts: 450}
	if l == nil || l.Path == "" {
		return s, errors.New("a persistent budget ledger is required")
	}
	if err := os.MkdirAll(filepath.Dir(l.Path), 0700); err != nil {
		return s, err
	}
	lock, err := os.OpenFile(l.Path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return s, err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return s, err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	f, err := os.OpenFile(l.Path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0600)
	if err != nil {
		return s, err
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	cents := 0
	for scan.Scan() {
		var e event
		if err := json.Unmarshal(scan.Bytes(), &e); err != nil {
			return s, errors.New("budget ledger is corrupt; refusing API access")
		}
		switch e.Kind {
		case "reserve":
			if e.Cents != CentsPerAttempt {
				return s, errors.New("invalid budget reservation")
			}
			cents += e.Cents
			s.Attempts++
		case "usage":
			if e.InputTokens < 0 || e.InputTokens > 64000 {
				return s, errors.New("invalid recorded usage")
			}
			s.InputTokens += e.InputTokens
		default:
			return s, errors.New("unknown budget ledger event")
		}
	}
	if err := scan.Err(); err != nil {
		return s, err
	}
	s.ReservedUSD = float64(cents) / 100
	s.RemainingUSD = float64(max(0, MaxCents-cents)) / 100
	s.RemainingAttempts = max(0, MaxCents-cents)
	s.EstimatedUSD = float64(s.InputTokens) * 0.042 / 1e6
	if fn != nil {
		err = fn(f, &s)
	}
	return s, err
}
func appendEvent(f *os.File, e event) error {
	e.At = time.Now().UTC()
	if err := json.NewEncoder(f).Encode(e); err != nil {
		return err
	}
	return f.Sync()
}

// Reserve never refunds, even if a request times out or fails: billing may have occurred.
func (l *Ledger) Reserve() error {
	_, err := l.transaction(func(f *os.File, s *Status) error {
		if s.RemainingAttempts < 1 {
			return ErrExhausted
		}
		return appendEvent(f, event{Kind: "reserve", Cents: CentsPerAttempt})
	})
	return err
}
func (l *Ledger) RecordUsage(tokens int64) error {
	if tokens < 0 || tokens > 64000 {
		return fmt.Errorf("provider usage outside supported range: %d", tokens)
	}
	_, err := l.transaction(func(f *os.File, _ *Status) error { return appendEvent(f, event{Kind: "usage", InputTokens: tokens}) })
	return err
}
func (l *Ledger) Status() (Status, error) { return l.transaction(nil) }
