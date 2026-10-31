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

// Package dbloop is the part the EOL (internal/eoldb) and vulnerability (internal/vulndb) database
// managers share: refresh on an interval, keep the old DB when a refresh fails, report readiness, and
// swap a finished download in.
package dbloop

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/go-logr/logr"
)

// RetryAfter is how soon a manager tries again while it has no usable DB.
const RetryAfter = 30 * time.Second

// Loop is embedded by a DB manager. Its zero value is ready to use.
type Loop struct {
	readyOnce sync.Once
	initOnce  sync.Once
	ready     chan struct{}
}

// Spec is one manager's side of Run.
type Spec struct {
	Name     string // for logs and the readiness error, e.g. "EOL database"
	Dir      string // logged
	Online   bool   // false: mounted, never refreshed
	Interval time.Duration
	// SkipFirst skips the first refresh, e.g. when the DB on the volume is still fresh after a restart.
	SkipFirst bool
	// Refresh builds or downloads a new DB and swaps it in only when complete (online mode).
	Refresh func(context.Context) error
	// Check returns the usable DB's time (built or published), or an error when there is none.
	Check func(context.Context) (time.Time, error)
	Log   logr.Logger
}

func (l *Loop) init() { l.initOnce.Do(func() { l.ready = make(chan struct{}) }) }

// Run refreshes and checks the DB until ctx ends. A failed refresh never returns an error: the old DB
// stays in use and the next refresh comes after Interval, or RetryAfter while there is no usable DB.
func (l *Loop) Run(ctx context.Context, s Spec) error {
	l.init()
	skip := s.SkipFirst
	for {
		next := s.Interval
		var refreshErr error
		if s.Online && !skip {
			if refreshErr = s.Refresh(ctx); refreshErr != nil {
				s.Log.Error(refreshErr, "refreshing "+s.Name+" failed")
			}
		}
		skip = false
		if at, err := s.Check(ctx); err != nil {
			s.Log.Info(s.Name+" not usable yet", "dir", s.Dir, "reason", err.Error())
			next = RetryAfter
		} else {
			msg := s.Name + " ready"
			if refreshErr != nil {
				msg = s.Name + " refresh failed; keeping the previous one"
			}
			s.Log.Info(msg, "dir", s.Dir, "online", s.Online, "dbTime", at.Format(time.RFC3339), "nextCheck", next.String())
			l.readyOnce.Do(func() { close(l.ready) })
			if !s.Online {
				next = time.Hour // mounted: only re-check readiness occasionally
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(next):
		}
	}
}

// Wait blocks until the DB has been usable at least once.
func (l *Loop) Wait(ctx context.Context) error {
	l.init()
	select {
	case <-l.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// IsReady is true once the DB has been usable at least once.
func (l *Loop) IsReady() bool {
	l.init()
	select {
	case <-l.ready:
		return true
	default:
		return false
	}
}

// NeedLeaderElection is false: every replica needs its own DB copy.
func (l *Loop) NeedLeaderElection() bool { return false }

// Swap replaces dst with src. shortcut: a scan starting in the instant between the two renames sees no
// DB and fails once; it is retried later. A scan that already opened the old DB keeps reading it.
func Swap(src, dst string) error {
	old := dst + ".old"
	_ = os.RemoveAll(old)
	if err := os.Rename(dst, old); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(src, dst); err != nil {
		_ = os.Rename(old, dst)
		return err
	}
	return os.RemoveAll(old)
}
