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
	"time"

	"github.com/go-logr/logr"
	"k8s.io/client-go/util/workqueue"
)

// Store is where the queue reads targets and writes results.
type Store interface {
	// Target resolves a digest to something scannable; false if it stopped running.
	Target(ctx context.Context, digest string) (Target, bool, error)
	// SetResult stores a scan; nil Findings or Vulns keep the previous ones (see Result).
	SetResult(digest string, r Result, errReason string, at, next time.Time)
	// Due lists digests whose next scan time has passed.
	Due(now time.Time) []string
	// Interval is the rescan interval after a successful scan.
	Interval() time.Duration
}

// Queue scans digests with a fixed number of workers. The workqueue dedupes: a digest that is
// already queued is not added twice, and one digest is never scanned by two workers at once.
type Queue struct {
	Scan    func(ctx context.Context, t Target) (Result, error)
	Store   Store
	Workers int
	// RetryAfter is how soon a failed scan is retried.
	RetryAfter time.Duration
	// Wait blocks until scans can run (the EOL database is ready).
	Wait func(ctx context.Context) error
	// Observe records each scan's duration and error reason ("" on success).
	Observe func(d time.Duration, errReason string)
	Log     logr.Logger

	once sync.Once
	q    workqueue.TypedInterface[string]
	// scanning holds digests being scanned: the ticker must not re-add them, or the workqueue
	// scans them again as soon as the current scan finishes.
	scanning sync.Map
}

func (q *Queue) queue() workqueue.TypedInterface[string] {
	q.once.Do(func() { q.q = workqueue.NewTyped[string]() })
	return q.q
}

// Add queues a digest for scanning.
func (q *Queue) Add(digest string) { q.queue().Add(digest) }

// Start runs workers and a ticker that queues due digests until ctx ends.
func (q *Queue) Start(ctx context.Context) error {
	wq := q.queue()
	defer wq.ShutDown()
	q.Log.Info("waiting for the EOL database before scanning")
	if err := q.Wait(ctx); err != nil {
		return nil // shutting down before the DB was ready
	}
	q.Log.Info("scanning started", "workers", max(q.Workers, 1), "queued", wq.Len())
	var wg sync.WaitGroup
	for range max(q.Workers, 1) {
		wg.Go(func() {
			for q.next(ctx) {
			}
		})
	}
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		if due := q.Store.Due(time.Now()); len(due) > 0 {
			before := wq.Len()
			for _, d := range due {
				if _, busy := q.scanning.Load(d); !busy {
					wq.Add(d)
				}
			}
			if added := wq.Len() - before; added > 0 {
				q.Log.Info("rescans due", "added", added, "queued", wq.Len())
			}
		}
		select {
		case <-ctx.Done():
			wq.ShutDown()
			wg.Wait()
			return nil
		case <-tick.C:
		}
	}
}

func (q *Queue) next(ctx context.Context) bool {
	digest, shutdown := q.q.Get()
	if shutdown {
		return false
	}
	defer q.q.Done(digest)
	q.scanning.Store(digest, struct{}{})
	defer q.scanning.Delete(digest)

	t, ok, err := q.Store.Target(ctx, digest)
	if err != nil {
		q.Log.Error(err, "resolving scan target", "digest", digest)
		now := time.Now()
		q.Store.SetResult(digest, Result{}, ReasonOther, now, now.Add(q.RetryAfter))
		if q.Observe != nil {
			q.Observe(0, ReasonOther) // counted as an error like any failed scan
		}
		return true
	}
	if !ok {
		return true
	}

	q.Log.Info("scanning", "image", t.Ref)
	start := time.Now()
	res, err := q.Scan(ctx, t)
	end := time.Now()
	reason := ""
	next := end.Add(q.Store.Interval())
	if err == nil && res.VulnErr != nil {
		err = res.VulnErr // EOL findings are kept; the image is retried for its vulnerabilities
	}
	if err != nil {
		if ctx.Err() != nil {
			return false // shutting down: don't record a spurious failure
		}
		reason = ReasonOther
		if se, ok := errors.AsType[*ScanError](err); ok {
			reason = se.Reason
		}
		next = end.Add(q.RetryAfter)
		q.Log.Info("scan failed", "image", t.Ref, "reason", reason, "error", err.Error())
	} else {
		q.Log.Info("scanned", "image", t.Ref, "duration", end.Sub(start).Round(time.Millisecond).String(),
			"findings", len(res.Findings), "vulnerabilities", len(res.Vulns), "queued", q.q.Len())
	}
	q.Store.SetResult(digest, res, reason, end, next)
	if q.Observe != nil {
		q.Observe(end.Sub(start), reason)
	}
	return true
}

// NeedLeaderElection is true: only the leader scans.
func (q *Queue) NeedLeaderElection() bool { return true }
