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

// Package vulndb keeps trivy's vulnerability DB available to scans, the way internal/eoldb keeps
// xeol's: download, swap in only when complete, keep the old one on failure.
package vulndb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"github.com/Rishang/xeol-operator/internal/dbloop"
)

// StaleAfter is when the web page shows the DB age as stale (trivy publishes it every few hours).
// The XeolVulnDatabaseStale alert has its own threshold, the chart's trivy.dbStaleDays.
const StaleAfter = 7 * 24 * time.Hour

// Manager keeps trivy's DB available. Online mode downloads it into Dir/current on an interval;
// mounted mode only reads Dir. It implements manager.Runnable and a healthz checker.
type Manager struct {
	Dir      string
	Online   bool
	TrivyBin string
	// Repositories overrides trivy's --db-repository list (e.g. a local mirror); empty keeps trivy's.
	Repositories []string
	Interval     time.Duration
	Log          logr.Logger

	dbloop.Loop
}

// CacheDir is the trivy --cache-dir to scan with: it holds db/trivy.db.
func (m *Manager) CacheDir() string {
	if m.Online {
		return filepath.Join(m.Dir, "current")
	}
	return m.Dir
}

// Start runs until ctx ends. It never returns an error for a failed download: the old DB stays in use.
func (m *Manager) Start(ctx context.Context) error {
	return m.Run(ctx, dbloop.Spec{Name: "vulnerability database", Dir: m.CacheDir(), Online: m.Online, Interval: m.Interval,
		Log: m.Log,
		// After a container restart the volume still holds the DB: don't download it again before it is due.
		SkipFirst: m.Online && m.downloadedWithin(m.Interval),
		Refresh: func(ctx context.Context) error {
			m.Log.Info("downloading vulnerability database", "repositories", m.Repositories)
			return m.download(ctx)
		},
		Check: func(context.Context) (time.Time, error) { return m.Updated() },
	})
}

// downloadedWithin is true when the DB in use was downloaded less than d ago (metadata.json DownloadedAt).
func (m *Manager) downloadedWithin(d time.Duration) bool {
	b, err := os.ReadFile(filepath.Join(m.CacheDir(), "db", "metadata.json"))
	var meta struct{ DownloadedAt time.Time }
	return err == nil && json.Unmarshal(b, &meta) == nil && time.Since(meta.DownloadedAt) < d
}

// Ready is a healthz.Checker: ready once the DB has been usable at least once.
func (m *Manager) Ready(_ *http.Request) error {
	if !m.IsReady() {
		return errors.New("vulnerability database not ready")
	}
	return nil
}

// Updated is when trivy last published the DB in use (metadata.json UpdatedAt). An error means
// there is no usable DB.
func (m *Manager) Updated() (time.Time, error) { return Updated(m.CacheDir()) }

// Updated reads a trivy cache dir's DB update time and checks the DB file is there.
func Updated(cacheDir string) (time.Time, error) {
	db := filepath.Join(cacheDir, "db")
	if st, err := os.Stat(filepath.Join(db, "trivy.db")); err != nil {
		return time.Time{}, err
	} else if st.Size() == 0 {
		return time.Time{}, errors.New("trivy.db is empty")
	}
	b, err := os.ReadFile(filepath.Join(db, "metadata.json"))
	if err != nil {
		return time.Time{}, err
	}
	var meta struct {
		Version   int
		UpdatedAt time.Time
	}
	if err := json.Unmarshal(b, &meta); err != nil {
		return time.Time{}, fmt.Errorf("parsing trivy DB metadata: %w", err)
	}
	if meta.UpdatedAt.IsZero() {
		return time.Time{}, errors.New("trivy DB metadata has no UpdatedAt")
	}
	return meta.UpdatedAt, nil
}

// download fetches the main DB (not the Java DB: SBOM scans don't need it) into a temp dir next to
// Dir/current and swaps it in only once trivy succeeded and the result is readable.
func (m *Manager) download(ctx context.Context) error {
	if err := os.MkdirAll(m.Dir, 0o755); err != nil {
		return err
	}
	// A download cut short by a container restart leaves its dir behind; with the current DB and a new
	// download next to it, it would push the volume over its size limit.
	left, _ := filepath.Glob(filepath.Join(m.Dir, ".download-*"))
	for _, d := range left {
		_ = os.RemoveAll(d)
	}
	tmp, err := os.MkdirTemp(m.Dir, ".download-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	args := []string{"image", "--download-db-only", "--cache-dir", tmp, "--no-progress", "--quiet",
		"--skip-version-check", "--disable-telemetry"}
	if len(m.Repositories) > 0 {
		args = append(args, "--db-repository", strings.Join(m.Repositories, ","))
	}
	cmd := exec.CommandContext(ctx, m.TrivyBin, args...)
	cmd.WaitDelay = time.Second
	// trivy writes nothing else, but keep its config and temp files inside the download dir.
	cmd.Env = []string{"HOME=" + tmp, "TMPDIR=" + tmp, "XDG_CONFIG_HOME=" + tmp, "XDG_CACHE_HOME=" + tmp}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("trivy --download-db-only: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	if _, err := Updated(tmp); err != nil {
		return fmt.Errorf("downloaded DB not usable: %w", err)
	}
	return dbloop.Swap(tmp, filepath.Join(m.Dir, "current"))
}
