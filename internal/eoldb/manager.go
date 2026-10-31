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
	"time"

	"github.com/go-logr/logr"

	"github.com/Rishang/xeol-operator/internal/dbloop"
)

// StaleAfter is when a DB counts as stale (the web page's age tone; the XeolDatabaseStale alert has its own threshold).
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

	dbloop.Loop
}

// Start runs until ctx ends. It never returns an error for a failed build: the old DB stays in use.
func (m *Manager) Start(ctx context.Context) error {
	return m.Run(ctx, dbloop.Spec{Name: "EOL database", Dir: m.Dir, Online: m.Online, Interval: m.Interval, Log: m.Log,
		Refresh: func(ctx context.Context) error {
			m.Log.Info("building EOL database from endoflife.date", "url", m.URL)
			return m.rebuild(ctx) // Build swaps the new DB in only once it is complete
		},
		Check: func(ctx context.Context) (time.Time, error) {
			if err := m.status(ctx); err != nil {
				return time.Time{}, err
			}
			built, _ := m.Built()
			return built, nil
		},
	})
}

// Ready is a healthz.Checker: ready once the DB has been valid at least once.
func (m *Manager) Ready(_ *http.Request) error {
	if !m.IsReady() {
		return errors.New("EOL database not ready")
	}
	return nil
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
