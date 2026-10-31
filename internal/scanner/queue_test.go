/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package scanner

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"
)

type fakeStore struct {
	due     []string
	mu      sync.Mutex
	results map[string]string // digest -> err reason
	next    map[string]time.Time
	last    Result
}

func (s *fakeStore) Target(_ context.Context, d string) (Target, bool, error) {
	if d == "noauth" {
		return Target{}, false, errors.New("secrets is forbidden")
	}
	return Target{Ref: "r@" + d}, d != "gone", nil
}

func (s *fakeStore) SetResult(d string, r Result, reason string, _, next time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.results[d], s.next[d], s.last = reason, next, r
}

func (s *fakeStore) Due(time.Time) []string  { s.mu.Lock(); defer s.mu.Unlock(); return s.due }
func (s *fakeStore) Interval() time.Duration { return 24 * time.Hour }
func (s *fakeStore) done(n int) bool         { s.mu.Lock(); defer s.mu.Unlock(); return len(s.results) >= n }
func (s *fakeStore) get(d string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.results[d]
	return r, ok
}

func TestQueueWorkerCapAndResults(t *testing.T) {
	store := &fakeStore{results: map[string]string{}, next: map[string]time.Time{}}
	var running, peak, calls atomic.Int32
	release := make(chan struct{})
	q := &Queue{
		Store: store, Workers: 2, RetryAfter: time.Hour, Log: logr.Discard(),
		Wait: func(context.Context) error { return nil },
		Scan: func(_ context.Context, tg Target) (Result, error) {
			calls.Add(1)
			n := running.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			<-release
			running.Add(-1)
			if tg.Ref == "r@bad" {
				return Result{}, &ScanError{Reason: ReasonAuth}
			}
			return Result{Findings: []Finding{{Product: "Python"}}}, nil
		},
	}
	digests := []string{"a", "b", "c", "d", "bad", "a", "b"} // duplicates must be deduped
	for _, d := range digests {
		q.Add(d)
	}
	go func() { _ = q.Start(t.Context()) }()

	time.Sleep(200 * time.Millisecond)
	if p := peak.Load(); p != 2 {
		t.Fatalf("peak concurrent scans = %d, want 2", p)
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for !store.done(5) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if c := calls.Load(); c != 5 {
		t.Errorf("scans = %d, want 5 (duplicates deduped)", c)
	}
	if r, _ := store.get("bad"); r != ReasonAuth {
		t.Errorf("bad reason = %q, want auth", r)
	}
	if r, ok := store.get("a"); !ok || r != "" {
		t.Errorf("a = %q, %v; want success", r, ok)
	}
	store.mu.Lock()
	if until := time.Until(store.next["bad"]); until > 2*time.Hour {
		t.Errorf("failed scan retried in %v, want ~RetryAfter", until)
	}
	if until := time.Until(store.next["a"]); until < 23*time.Hour {
		t.Errorf("successful scan next in %v, want ~interval", until)
	}
	store.mu.Unlock()
}

func TestQueueWaitsForDB(t *testing.T) {
	store := &fakeStore{results: map[string]string{}, next: map[string]time.Time{}}
	ready := make(chan struct{})
	var scanned atomic.Bool
	q := &Queue{
		Store: store, Workers: 1, Log: logr.Discard(),
		Wait: func(ctx context.Context) error { <-ready; return nil },
		Scan: func(context.Context, Target) (Result, error) { scanned.Store(true); return Result{}, nil },
	}
	q.Add("a")
	go func() { _ = q.Start(t.Context()) }()
	time.Sleep(100 * time.Millisecond)
	if scanned.Load() {
		t.Fatal("scanned before DB ready")
	}
	close(ready)
	deadline := time.Now().Add(2 * time.Second)
	for !scanned.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !scanned.Load() {
		t.Fatal("never scanned after DB ready")
	}
}

func TestQueueDoesNotRescanInFlight(t *testing.T) {
	store := &fakeStore{results: map[string]string{}, next: map[string]time.Time{}, due: []string{"a"}}
	var calls atomic.Int32
	release := make(chan struct{})
	q := &Queue{
		Store: store, Workers: 1, Log: logr.Discard(),
		Wait: func(context.Context) error { return nil },
		Scan: func(context.Context, Target) (Result, error) { calls.Add(1); <-release; return Result{}, nil },
	}
	go func() { _ = q.Start(t.Context()) }() // the first tick adds "a" from Due
	time.Sleep(100 * time.Millisecond)
	// "a" is mid-scan and still due: a ticker pass must not queue it again.
	for _, d := range store.Due(time.Now()) {
		if _, busy := q.scanning.Load(d); !busy {
			q.Add(d)
		}
	}
	store.mu.Lock()
	store.due = nil
	store.mu.Unlock()
	close(release)
	time.Sleep(200 * time.Millisecond)
	if c := calls.Load(); c != 1 {
		t.Fatalf("scans = %d, want 1", c)
	}
}

func TestQueueObservesTargetErrors(t *testing.T) {
	store := &fakeStore{results: map[string]string{}, next: map[string]time.Time{}}
	observed := make(chan string, 1)
	q := &Queue{
		Store: store, Workers: 1, RetryAfter: time.Hour, Log: logr.Discard(),
		Wait: func(context.Context) error { return nil },
		Scan: func(context.Context, Target) (Result, error) {
			t.Error("scanned without a target")
			return Result{}, nil
		},
		Observe: func(_ time.Duration, reason string) { observed <- reason },
	}
	q.Add("noauth")
	go func() { _ = q.Start(t.Context()) }()
	select {
	case r := <-observed:
		if r != ReasonOther {
			t.Fatalf("observed %q, want other", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("target error not observed")
	}
}

func TestQueueTrivyFailure(t *testing.T) {
	store := &fakeStore{results: map[string]string{}, next: map[string]time.Time{}}
	q := &Queue{
		Store: store, Workers: 1, RetryAfter: time.Hour, Log: logr.Discard(),
		Wait: func(context.Context) error { return nil },
		Scan: func(context.Context, Target) (Result, error) {
			return Result{Findings: []Finding{{Product: "Python"}}, VulnErr: &ScanError{Reason: ReasonTrivy}}, nil
		},
	}
	q.Add("a")
	go func() { _ = q.Start(t.Context()) }()
	deadline := time.Now().Add(2 * time.Second)
	for !store.done(1) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.results["a"] != ReasonTrivy {
		t.Errorf("reason = %q, want %q", store.results["a"], ReasonTrivy)
	}
	if len(store.last.Findings) != 1 || store.last.Vulns != nil {
		t.Errorf("stored %+v; want the EOL findings and nil vulns", store.last)
	}
	if d := time.Until(store.next["a"]); d > time.Hour+time.Minute {
		t.Errorf("next scan in %v, want the 1h retry, not the 24h interval", d)
	}
}
