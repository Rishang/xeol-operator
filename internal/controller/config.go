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

package controller

import (
	"slices"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/Rishang/xeol-operator/internal/config"
	"github.com/Rishang/xeol-operator/internal/scanner"
)

// ConfigSnapshot is the parsed config file.
type ConfigSnapshot struct {
	NamespaceSelector labels.Selector
	ExcludeNamespaces []string
	ExcludeImages     []string
	Interval          time.Duration
	WorkloadKinds     []string // empty = every kind
	WorkloadLabels    []string
	// IgnoreProducts is the raw config, kept to detect changes; ignores holds it compiled.
	IgnoreProducts []config.IgnoreRule
	ignores        []rule[scanner.Finding]
	// IgnoreVulnerabilities is the raw config, kept to detect changes; vulnIgnores holds it compiled.
	IgnoreVulnerabilities []config.VulnIgnoreRule
	vulnIgnores           []rule[scanner.Vuln]
}

// Config holds the live scan config, shared by the controllers, scan queue and metrics.
type Config struct {
	// Changed gets an event after a changed config is applied, so the Pod controller re-evaluates every
	// Pod against it. Buffered (1); nil disables it.
	Changed chan event.GenericEvent

	mu   sync.RWMutex
	snap ConfigSnapshot
	ok   bool
	err  error // the last rejected config file; nil once a good one is applied
}

// Get returns the current config, and false before the first one is applied.
func (c *Config) Get() (ConfigSnapshot, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snap, c.ok
}

// Set replaces the config.
func (c *Config) Set(s ConfigSnapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snap, c.ok = s, true
}

// SetError records why the config file was last rejected (nil: the current file is applied).
func (c *Config) SetError(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.err = err
}

// Err is why the config file was last rejected, while the previous config stays in use.
func (c *Config) Err() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.err
}

// Apply compiles a config file and makes it current. If it differs from the current one, every Pod is
// re-evaluated. A config that doesn't compile (e.g. a bad label selector) is returned as an error and
// the current one stays.
func (c *Config) Apply(cfg config.Config) error {
	snap, err := SnapshotFrom(&cfg)
	if err != nil {
		return err
	}
	prev, ok := c.Get()
	changed := !ok || prev.Interval != snap.Interval || !slices.Equal(prev.ExcludeNamespaces, snap.ExcludeNamespaces) ||
		!slices.Equal(prev.ExcludeImages, snap.ExcludeImages) || !slices.Equal(prev.WorkloadLabels, snap.WorkloadLabels) ||
		!slices.Equal(prev.WorkloadKinds, snap.WorkloadKinds) ||
		prev.NamespaceSelector.String() != snap.NamespaceSelector.String() ||
		!equality.Semantic.DeepEqual(prev.IgnoreProducts, snap.IgnoreProducts) ||
		!equality.Semantic.DeepEqual(prev.IgnoreVulnerabilities, snap.IgnoreVulnerabilities)
	c.Set(snap)
	if changed {
		// After Set: Pods reconciled before this point saw no (or the old) config.
		// The event only wakes the Pod controller, which maps it to every Pod (allPods); GenericEvent needs
		// some object, and nothing reads this one.
		select {
		case c.Changed <- event.GenericEvent{Object: &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "config"}}}:
		default: // nil channel, or a re-evaluation is already pending; it will read the new config
		}
	}
	return nil
}

// SnapshotFrom compiles a config file's selectors and ignore rules.
func SnapshotFrom(cfg *config.Config) (ConfigSnapshot, error) {
	sel := labels.Everything()
	if cfg.NamespaceSelector != nil {
		var err error
		if sel, err = metav1.LabelSelectorAsSelector(cfg.NamespaceSelector); err != nil {
			return ConfigSnapshot{}, err
		}
	}
	ignores, err := compileIgnores(cfg.IgnoreProducts)
	if err != nil {
		return ConfigSnapshot{}, err
	}
	vulnIgnores, err := compileVulnIgnores(cfg.IgnoreVulnerabilities)
	if err != nil {
		return ConfigSnapshot{}, err
	}
	interval := cfg.Interval.Duration
	if interval <= 0 { // a Config built in code rather than by config.Parse
		interval = config.DefaultInterval
	}
	return ConfigSnapshot{NamespaceSelector: sel, ExcludeNamespaces: cfg.ExcludeNamespaces,
		ExcludeImages: cfg.ExcludeImages, Interval: interval, WorkloadKinds: cfg.WorkloadKinds, WorkloadLabels: cfg.WorkloadLabels,
		IgnoreProducts: cfg.IgnoreProducts, ignores: ignores,
		IgnoreVulnerabilities: cfg.IgnoreVulnerabilities, vulnIgnores: vulnIgnores}, nil
}
