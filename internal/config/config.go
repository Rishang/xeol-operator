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

// Package config is the scan configuration file (--config): which running images are scanned and how
// results are labelled. The Helm chart renders it from scanner.* values into a mounted ConfigMap, and
// Watcher reloads it when the kubelet updates the file. Pod-level settings (workers, memory, DB source,
// scan types) are operator flags, not file fields.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

// DefaultPath is where the chart mounts the file.
const DefaultPath = "/etc/xeol-operator/config.yaml"

// DefaultInterval is how often each image digest is rescanned when interval is absent.
const DefaultInterval = 24 * time.Hour

// DefaultWorkloadKinds are scanned when workloadKinds is absent; an explicit empty list scans every kind.
var DefaultWorkloadKinds = []string{"Deployment", "StatefulSet", "DaemonSet", "Job", "CronJob"}

// Config is the file's content: the schema. Unknown keys are rejected (Parse).
type Config struct {
	// NamespaceSelector limits scanning to namespaces with matching labels. Empty selects all.
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty"`

	// ExcludeNamespaces are namespace name globs to skip, e.g. "kube-*". "*" matches any characters, "?" one.
	ExcludeNamespaces []string `json:"excludeNamespaces,omitempty"`

	// ExcludeImages are globs on the image as written in the Pod spec or its repository, e.g.
	// "registry.internal/*" ("*" matches any characters, "/" included; "?" one character).
	ExcludeImages []string `json:"excludeImages,omitempty"`

	// Interval is how often each image digest is rescanned (default 24h).
	Interval metav1.Duration `json:"interval,omitzero"`

	// WorkloadKinds are the top-level workload kinds to scan (a Pod's owner chain, e.g. Pod → ReplicaSet →
	// Deployment, Job → CronJob), matched exactly. Absent means DefaultWorkloadKinds; [] means every kind.
	WorkloadKinds []string `json:"workloadKinds"`

	// WorkloadLabels are workload label keys copied onto metrics as k8s_label_<key>, e.g. "team".
	WorkloadLabels []string `json:"workloadLabels,omitempty"`

	// IgnoreProducts hides accepted findings from metrics and the JSON API. Images are still scanned.
	IgnoreProducts []IgnoreRule `json:"ignoreProducts,omitempty"`

	// IgnoreVulnerabilities hides accepted vulnerabilities (trivy) the same way.
	IgnoreVulnerabilities []VulnIgnoreRule `json:"ignoreVulnerabilities,omitempty"`
}

// VulnIgnoreRule hides vulnerabilities by ID and/or package, optionally only in some namespaces or
// workloads (Namespaces and Selector work as in IgnoreRule). With both IDs and Packages, both must match.
type VulnIgnoreRule struct {
	// IDs are vulnerability IDs as trivy reports them, e.g. "CVE-2024-1234", "GHSA-...". Case-insensitive.
	IDs []string `json:"ids,omitempty"`

	// Packages are package names as trivy reports them, e.g. "libcrypto3". Case-insensitive.
	Packages []string `json:"packages,omitempty"`

	// Namespaces limits the rule to namespace name globs. Empty means every namespace.
	Namespaces []string `json:"namespaces,omitempty"`

	// Selector limits the rule to workloads whose own labels match.
	Selector *metav1.LabelSelector `json:"selector,omitempty"`
}

// IgnoreRule hides findings of some products, optionally only some cycles, namespaces or workloads.
// A rule applies to a workload only when both namespaces and selector match; with neither it applies everywhere.
type IgnoreRule struct {
	// Products are endoflife.date product slugs (https://endoflife.date/<slug>, e.g. "nodejs") or display names
	// (e.g. "Node.js", "Gorilla Toolkit"). Case-insensitive.
	Products []string `json:"products"`

	// Cycles limits the rule to these release cycles, e.g. "3.8". Empty means every cycle.
	Cycles []string `json:"cycles,omitempty"`

	// Namespaces limits the rule to namespace name globs, e.g. "test". Empty means every namespace.
	Namespaces []string `json:"namespaces,omitempty"`

	// Selector limits the rule to workloads (Deployment, DaemonSet, ...; a bare Pod) whose own labels match.
	Selector *metav1.LabelSelector `json:"selector,omitempty"`
}

// Parse reads the file's YAML strictly (an unknown field is an error) and fills in the defaults.
// Empty input is a valid, all-defaults config.
func Parse(b []byte) (Config, error) {
	var c Config
	if err := yaml.UnmarshalStrict(b, &c); err != nil {
		return Config{}, fmt.Errorf("parsing config: %w", err)
	}
	if c.Interval.Duration < 0 {
		return Config{}, fmt.Errorf("parsing config: interval %s is negative", c.Interval.Duration)
	}
	for i, r := range c.IgnoreProducts {
		if len(r.Products) == 0 {
			return Config{}, fmt.Errorf("parsing config: ignoreProducts[%d]: products is empty", i)
		}
	}
	for i, r := range c.IgnoreVulnerabilities {
		if len(r.IDs) == 0 && len(r.Packages) == 0 {
			return Config{}, fmt.Errorf("parsing config: ignoreVulnerabilities[%d]: needs ids or packages", i)
		}
	}
	if c.Interval.Duration == 0 {
		c.Interval.Duration = DefaultInterval
	}
	if c.WorkloadKinds == nil {
		c.WorkloadKinds = DefaultWorkloadKinds
	}
	return c, nil
}

// Load reads and parses the file. A missing file is an empty one: built-in defaults.
func Load(path string) (Config, []byte, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		b, err = nil, nil
	}
	if err != nil {
		return Config{}, nil, err
	}
	c, err := Parse(b)
	return c, b, err
}
