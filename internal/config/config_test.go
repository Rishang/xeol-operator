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

package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
)

func TestParse(t *testing.T) {
	c, err := Parse(nil)
	if err != nil || c.Interval.Duration != 24*time.Hour || !slices.Equal(c.WorkloadKinds, DefaultWorkloadKinds) {
		t.Fatalf("empty file: %+v, %v", c, err)
	}
	c, err = Parse([]byte("interval: 6h\nworkloadKinds: []\nignoreProducts:\n  - products: [perl]\n    namespaces: [test]\n"))
	if err != nil || c.Interval.Duration != 6*time.Hour || c.WorkloadKinds == nil || len(c.WorkloadKinds) != 0 ||
		c.IgnoreProducts[0].Namespaces[0] != "test" {
		t.Fatalf("got %+v, %v", c, err)
	}
	for in, want := range map[string]string{
		"interval: 12h\nintervall: 6h\n":            "intervall",
		"interval: soon\n":                          "\"soon\"",
		"interval: -1h\n":                           "negative",
		"ignoreProducts:\n  - namespaces: [test]\n": "products is empty",
	} {
		if _, err := Parse([]byte(in)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: error %v, want one naming %q", in, err, want)
		}
	}
}

// TestWatcher writes the file the way the kubelet updates a mounted ConfigMap: config.yaml -> ..data/config.yaml,
// with ..data a symlink swapped atomically to a new timestamped dir.
func TestWatcher(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	gen := 0
	publish := func(content string) {
		t.Helper()
		gen++
		ts := filepath.Join(dir, "..ts"+string(rune('0'+gen)))
		if err := os.Mkdir(ts, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ts, "config.yaml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		tmp := filepath.Join(dir, "..data_tmp")
		if err := os.Symlink(filepath.Base(ts), tmp); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, filepath.Join(dir, "..data")); err != nil {
			t.Fatal(err)
		}
	}
	publish("interval: 1h\n")
	if err := os.Symlink(filepath.Join("..data", "config.yaml"), path); err != nil {
		t.Fatal(err)
	}
	_, first, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var applied []time.Duration
	var lastErr error
	w := &Watcher{Path: path, Interval: 10 * time.Millisecond, Last: first, Log: logr.Discard(),
		Apply: func(c Config) error {
			if c.Interval.Duration == 13*time.Hour {
				return errors.New("rejected by Apply") // e.g. a selector that doesn't compile
			}
			mu.Lock()
			defer mu.Unlock()
			applied = append(applied, c.Interval.Duration)
			return nil
		},
		Failed: func(err error) { mu.Lock(); defer mu.Unlock(); lastErr = err },
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _ = w.Start(ctx) }()
	waitFor := func(what string, ok func() bool) {
		t.Helper()
		for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
			mu.Lock()
			done := ok()
			mu.Unlock()
			if done {
				return
			}
		}
		t.Fatalf("timed out waiting for %s (applied %v, err %v)", what, applied, lastErr)
	}

	publish("interval: 2h\n")
	waitFor("reload", func() bool { return slices.Equal(applied, []time.Duration{2 * time.Hour}) })
	publish("interval: 2h\nbogus: 1\n")
	waitFor("parse error", func() bool { return lastErr != nil })
	publish("interval: 13h\n")
	waitFor("Apply error", func() bool { return lastErr != nil && strings.Contains(lastErr.Error(), "Apply") })
	publish("interval: 3h\n")
	waitFor("recovery", func() bool { return lastErr == nil && len(applied) == 2 && applied[1] == 3*time.Hour })
	// Undoing a bad edit restores the applied bytes: the error clears with nothing to apply.
	publish("interval: 3h\nbogus: 1\n")
	waitFor("second parse error", func() bool { return lastErr != nil })
	publish("interval: 3h\n")
	waitFor("undo clears the error", func() bool { return lastErr == nil && len(applied) == 2 })
}
