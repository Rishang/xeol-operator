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

package eoldb

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
)

// StaleAfter is when a DB counts as stale for the DBStale condition and alert.
const StaleAfter = 30 * 24 * time.Hour

// Manager keeps xeol's DB available. Online mode rebuilds it from endoflife.date on an interval;
// mounted mode only reads Dir. It implements manager.Runnable and a healthz checker.
type Manager struct {
	Dir      string
	Online   bool
	URL      string
	Client   *http.Client
	Interval time.Duration
	XeolBin  string
	Log      logr.Logger

	readyOnce sync.Once
	ready     chan struct{}
	initOnce  sync.Once
}

func (m *Manager) init() { m.initOnce.Do(func() { m.ready = make(chan struct{}) }) }

// Start runs until ctx ends. It never returns an error for a failed build: the old DB stays in use.
func (m *Manager) Start(ctx context.Context) error {
	m.init()
	retry := 30 * time.Second
	for {
		next := m.Interval
		var rebuildErr error
		if m.Online {
			m.Log.Info("building EOL database from endoflife.date", "url", m.URL)
			// Build swaps the new DB in only once it is complete, so a failure leaves the old one as is.
			if rebuildErr = m.rebuild(ctx); rebuildErr != nil {
				m.Log.Error(rebuildErr, "building EOL database from endoflife.date failed")
			}
		}
		if err := m.status(ctx); err != nil {
			m.Log.Info("EOL database not usable yet", "dir", m.Dir, "reason", err.Error())
			next = retry
		} else {
			// With a usable DB, a failed rebuild waits for the next interval (daily), not the retry.
			built, _ := m.Built()
			msg := "EOL database ready"
			if rebuildErr != nil {
				msg = "EOL database rebuild failed; keeping the previous one"
			}
			m.Log.Info(msg, "dir", m.Dir, "online", m.Online, "built", built.Format(time.RFC3339),
				"nextCheck", next.String())
			m.readyOnce.Do(func() { close(m.ready) })
			if !m.Online {
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

// NeedLeaderElection is false: every replica needs its own DB copy.
func (m *Manager) NeedLeaderElection() bool { return false }

// Wait blocks until the DB has been valid at least once.
func (m *Manager) Wait(ctx context.Context) error {
	m.init()
	select {
	case <-m.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Ready is a healthz.Checker: ready once the DB has been valid at least once.
func (m *Manager) Ready(_ *http.Request) error {
	m.init()
	select {
	case <-m.ready:
		return nil
	default:
		return errors.New("EOL database not ready")
	}
}

// Built is the current DB's build time.
func (m *Manager) Built() (time.Time, error) { return Built(m.Dir) }

func (m *Manager) rebuild(ctx context.Context) error {
	data, err := Fetch(ctx, m.Client, m.URL)
	if err != nil {
		return err
	}
	return Build(data, m.Dir, time.Now())
}

var validStatus = regexp.MustCompile(`(?m)^Status:\s+valid\s*$`)

// status runs `xeol db status`, the same check xeol does before a scan.
func (m *Manager) status(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, m.XeolBin, "db", "status")
	// xeol panics at startup without HOME.
	cmd.Env = []string{"HOME=" + os.TempDir(), "XEOL_DB_CACHE_DIR=" + m.Dir, "XEOL_DB_VALIDATE_AGE=false", "XEOL_CHECK_FOR_APP_UPDATE=false"}
	out, err := cmd.CombinedOutput()
	if err != nil || !validStatus.Match(out) {
		return fmt.Errorf("xeol db status: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
