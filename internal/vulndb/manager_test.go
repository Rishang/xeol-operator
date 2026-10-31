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

package vulndb

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
)

// fakeTrivy writes a trivy stand-in. With ok it writes a DB updated at `updated` into --cache-dir
// and logs its args; otherwise it fails like a registry error.
func fakeTrivy(t *testing.T, ok bool, updated string) (bin, argsLog string) {
	t.Helper()
	dir := t.TempDir()
	argsLog = filepath.Join(dir, "args")
	body := `echo "registry unreachable" >&2; exit 1`
	if ok {
		body = `while [ $# -gt 0 ]; do [ "$1" = --cache-dir ] && c=$2; shift; done
mkdir -p "$c/db" && echo bolt > "$c/db/trivy.db" && echo '{"Version":2,"UpdatedAt":"` + updated + `"}' > "$c/db/metadata.json"`
	}
	bin = filepath.Join(dir, "trivy")
	script := "#!/bin/sh\necho \"$@\" >> " + argsLog + "\n" + body + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, argsLog
}

func writeDB(t *testing.T, cacheDir, updated, downloaded string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(cacheDir, "db"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "db", "trivy.db"), []byte("bolt"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta := `{"Version":2,"UpdatedAt":"` + updated + `","DownloadedAt":"` + downloaded + `"}`
	if err := os.WriteFile(filepath.Join(cacheDir, "db", "metadata.json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
}

func waitReady(t *testing.T, m *Manager) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := m.Wait(ctx); err != nil {
		t.Fatalf("never ready: %v", err)
	}
}

func TestDownload(t *testing.T) {
	bin, args := fakeTrivy(t, true, "2026-10-29T13:09:46Z")
	m := &Manager{Dir: t.TempDir(), Online: true, TrivyBin: bin, Repositories: []string{"mirror.local/trivy-db:2"},
		Interval: time.Hour, Log: logr.Discard()}
	if m.Ready(nil) == nil {
		t.Fatal("ready before any download")
	}
	go func() { _ = m.Start(t.Context()) }()
	waitReady(t, m)
	updated, err := m.Updated()
	if err != nil || !updated.Equal(time.Date(2026, 10, 29, 13, 9, 46, 0, time.UTC)) {
		t.Fatalf("updated %v, err %v", updated, err)
	}
	if m.CacheDir() != filepath.Join(m.Dir, "current") {
		t.Errorf("cache dir %q", m.CacheDir())
	}
	b, _ := os.ReadFile(args)
	for _, want := range []string{"image --download-db-only", "--db-repository mirror.local/trivy-db:2", "--skip-version-check", "--disable-telemetry"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("args %q missing %q", b, want)
		}
	}
	if strings.Contains(string(b), "java") {
		t.Errorf("must not fetch the Java DB: %q", b)
	}
	// Only current/ is left: the download dir and the swapped-out DB are gone.
	entries, _ := os.ReadDir(m.Dir)
	if len(entries) != 1 || entries[0].Name() != "current" {
		t.Errorf("dir holds %v, want only current", entries)
	}
}

func TestFailedDownloadKeepsOldDB(t *testing.T) {
	bin, _ := fakeTrivy(t, false, "")
	m := &Manager{Dir: t.TempDir(), Online: true, TrivyBin: bin, Interval: time.Hour, Log: logr.Discard()}
	writeDB(t, filepath.Join(m.Dir, "current"), "2026-10-01T00:00:00Z", "2026-10-01T01:00:00Z")
	if err := os.Mkdir(filepath.Join(m.Dir, ".download-123"), 0o755); err != nil { // cut short by a restart
		t.Fatal(err)
	}
	go func() { _ = m.Start(t.Context()) }()
	waitReady(t, m)
	if updated, err := m.Updated(); err != nil || updated.Day() != 1 {
		t.Fatalf("old DB not kept: %v, %v", updated, err)
	}
	if _, err := os.Stat(filepath.Join(m.Dir, ".download-123")); !os.IsNotExist(err) {
		t.Errorf("leftover download dir kept: %v", err)
	}
}

func TestRestartKeepsFreshDB(t *testing.T) {
	bin, args := fakeTrivy(t, true, "2026-10-29T00:00:00Z")
	m := &Manager{Dir: t.TempDir(), Online: true, TrivyBin: bin, Interval: time.Hour, Log: logr.Discard()}
	writeDB(t, filepath.Join(m.Dir, "current"), "2026-10-19T00:00:00Z", time.Now().Add(-time.Minute).Format(time.RFC3339))
	go func() { _ = m.Start(t.Context()) }()
	waitReady(t, m)
	if b, _ := os.ReadFile(args); len(b) != 0 {
		t.Errorf("downloaded again although the DB is fresh: %q", b)
	}
}

func TestMounted(t *testing.T) {
	bin, args := fakeTrivy(t, true, "2026-10-29T00:00:00Z")
	m := &Manager{Dir: t.TempDir(), TrivyBin: bin, Interval: time.Hour, Log: logr.Discard()}
	writeDB(t, m.Dir, "2026-10-19T00:00:00Z", "")
	go func() { _ = m.Start(t.Context()) }()
	waitReady(t, m)
	if m.CacheDir() != m.Dir {
		t.Errorf("mounted cache dir %q, want %q", m.CacheDir(), m.Dir)
	}
	if b, _ := os.ReadFile(args); len(b) != 0 {
		t.Errorf("mounted mode ran trivy: %q", b)
	}
}
