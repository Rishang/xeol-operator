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
	"bytes"
	"context"
	"time"

	"github.com/go-logr/logr"
)

// Watcher re-reads the file and applies it when its content changes. The kubelet updates a mounted
// ConfigMap by swapping a "..data" symlink; reading the path each time follows it, so polling needs no
// special handling, and the kubelet's own sync delay (up to about a minute) dominates Interval.
type Watcher struct {
	Path     string
	Interval time.Duration
	// Last is the content applied at startup; only a different content is applied again.
	Last []byte
	// Apply installs a parsed config. An error (e.g. a selector that doesn't compile) keeps the previous
	// config, like a parse error.
	Apply func(Config) error
	// Failed reports a bad file (nil when a good one is applied again).
	Failed func(error)
	Log    logr.Logger
}

// Start polls until ctx ends.
func (w *Watcher) Start(ctx context.Context) error {
	t := time.NewTicker(w.Interval)
	defer t.Stop()
	var bad string // the last error logged, so a bad file is logged once, not every tick
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		c, b, err := Load(w.Path)
		if err == nil && bytes.Equal(b, w.Last) {
			if bad != "" { // a bad edit was undone: the applied config is current again
				bad = ""
				w.Failed(nil)
			}
			continue
		}
		if err == nil {
			err = w.Apply(c)
		}
		if err != nil {
			if err.Error() != bad {
				w.Log.Error(err, "config file rejected; keeping the previous config", "path", w.Path)
				bad = err.Error()
			}
			w.Failed(err)
			continue
		}
		w.Last, bad = b, ""
		w.Failed(nil)
		w.Log.Info("config file reloaded", "path", w.Path)
	}
}

// NeedLeaderElection is false: every replica serves the config.
func (w *Watcher) NeedLeaderElection() bool { return false }
