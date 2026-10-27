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
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-logr/logr"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/products-full.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// xeolBin returns the real xeol binary, or skips: these checks prove xeol accepts our DB.
func xeolBin(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("xeol")
	if err != nil {
		t.Skip("xeol not on PATH")
	}
	return p
}

func TestBuild(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	if err := Build(fixture(t), dir, now); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "1", "xeol.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	count := func(q string, args ...any) int {
		var n int
		if err := db.QueryRow(q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(`SELECT count(*) FROM products`); n != 6 {
		t.Errorf("products = %d, want 6", n)
	}
	if n := count(`SELECT count(*) FROM purls p JOIN products x ON x.id = p.product_id WHERE x.name = 'Python' AND p.purl = 'pkg:generic/python'`); n != 1 {
		t.Errorf("python generic purl rows = %d, want 1", n)
	}
	if n := count(`SELECT count(*) FROM cpes p JOIN products x ON x.id = p.product_id WHERE x.name = 'Alpine Linux'`); n == 0 {
		t.Error("Alpine Linux has no CPEs; distro matching needs them")
	}
	var eol string
	if err := db.QueryRow(`SELECT CAST(c.eol AS TEXT) FROM cycles c JOIN products x ON x.id = c.product_id WHERE x.name = 'Python' AND c.release_cycle = '3.8'`).Scan(&eol); err != nil {
		t.Fatal(err)
	}
	if eol != "2024-10-07" {
		t.Errorf("Python 3.8 eol = %q", eol)
	}
	built, err := Built(dir)
	if err != nil || !built.Equal(now) {
		t.Errorf("Built = %v, %v; want %v", built, err, now)
	}
}

func TestBuildAcceptedByXeol(t *testing.T) {
	bin := xeolBin(t)
	dir := t.TempDir()
	if err := Build(fixture(t), dir, time.Now()); err != nil {
		t.Fatal(err)
	}
	m := &Manager{Dir: dir, XeolBin: bin}
	if err := m.status(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestBuildKeepsOldDBOnError(t *testing.T) {
	dir := t.TempDir()
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := Build(fixture(t), dir, old); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`not json`, `{"schema_version":"2.0.0","result":[{}]}`, `{"schema_version":"1.2.1","result":[]}`} {
		if err := Build([]byte(bad), dir, time.Now()); err == nil {
			t.Errorf("Build(%q) succeeded", bad)
		}
	}
	if built, _ := Built(dir); !built.Equal(old) {
		t.Errorf("old DB replaced: built %v", built)
	}
	left, _ := filepath.Glob(filepath.Join(dir, ".build-*"))
	if len(left) != 0 {
		t.Errorf("temp dirs left behind: %v", left)
	}
}

func TestManagerOnline(t *testing.T) {
	bin := xeolBin(t)
	data := fixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(data) }))
	defer srv.Close()

	m := &Manager{Dir: t.TempDir(), Online: true, URL: srv.URL, Client: srv.Client(), Interval: time.Hour,
		XeolBin: bin, Log: logr.Discard()}
	if m.Ready(nil) == nil {
		t.Fatal("ready before first build")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	go func() { _ = m.Start(ctx) }()
	if err := m.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Ready(nil); err != nil {
		t.Fatal(err)
	}
}

func TestManagerMountedReadOnly(t *testing.T) {
	bin := xeolBin(t)
	dir := t.TempDir()
	if err := Build(fixture(t), dir, time.Now()); err != nil {
		t.Fatal(err)
	}
	// Read-only, like an air-gapped volume mount.
	for _, p := range []string{filepath.Join(dir, "1"), dir} {
		if err := os.Chmod(p, 0o555); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755); _ = os.Chmod(filepath.Join(dir, "1"), 0o755) })

	m := &Manager{Dir: dir, XeolBin: bin, Interval: time.Hour, Log: logr.Discard()}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	go func() { _ = m.Start(ctx) }()
	if err := m.Wait(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestManagerMountedMissing(t *testing.T) {
	bin := xeolBin(t)
	m := &Manager{Dir: t.TempDir(), XeolBin: bin, Interval: time.Hour, Log: logr.Discard()}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() { _ = m.Start(ctx) }()
	if err := m.Wait(ctx); err == nil {
		t.Fatal("ready with no DB")
	}
}
