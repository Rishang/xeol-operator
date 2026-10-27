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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	xeolv1alpha1 "github.com/Rishang/xeol-operator/api/v1alpha1"
	"github.com/Rishang/xeol-operator/internal/scanner"
)

func ignoreSnap(t *testing.T, rules ...xeolv1alpha1.IgnoreRule) ConfigSnapshot {
	t.Helper()
	snap, err := SnapshotFrom(&xeolv1alpha1.XeolScannerSpec{IgnoreProducts: rules})
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

var (
	node16   = scanner.Finding{Product: "Node.js", Cycle: "16", URL: "https://endoflife.date/nodejs"}
	python38 = scanner.Finding{Product: "Python", Cycle: "3.8", URL: "https://endoflife.date/python"}
	python39 = scanner.Finding{Product: "Python", Cycle: "3.9", URL: "https://endoflife.date/python"}
	perl     = scanner.Finding{Product: "Perl", Cycle: "5.36", URL: "https://endoflife.date/perl"}
	gorilla  = scanner.Finding{Product: "Gorilla Toolkit", Cycle: "1", URL: "https://endoflife.date/gorilla"}
	noURL    = scanner.Finding{Product: "Debian", Cycle: "12"}

	grafana    = Workload{Namespace: "monitoring", Kind: "Deployment", Name: "grafana", Labels: map[string]string{"app.kubernetes.io/name": "grafana"}}
	prometheus = Workload{Namespace: "monitoring", Kind: "StatefulSet", Name: "prometheus"}
	py38       = Workload{Namespace: "test", Kind: "Deployment", Name: "python38", Labels: map[string]string{"team": "payments"}}
	py38Agent  = Workload{Namespace: "test", Kind: "DaemonSet", Name: "python38-agent", Labels: map[string]string{"team": "platform"}}
	legacyPay  = Workload{Namespace: "pay", Kind: "Deployment", Name: "api", Labels: map[string]string{"team": "legacy"}}
)

func TestIgnoreGlobal(t *testing.T) {
	s := ignoreSnap(t,
		xeolv1alpha1.IgnoreRule{Products: []string{"NodeJS"}},                          // slug, any case
		xeolv1alpha1.IgnoreRule{Products: []string{"python"}, Cycles: []string{"3.8"}}, // one cycle only
		xeolv1alpha1.IgnoreRule{Products: []string{"debian"}},                          // no URL: lowercased name
	)
	for _, tt := range []struct {
		f    scanner.Finding
		want bool
	}{{node16, true}, {python38, true}, {python39, false}, {perl, false}, {noURL, true}} {
		if got := s.Ignored(tt.f, nil); got != tt.want {
			t.Errorf("Ignored(%s %s) = %v, want %v", tt.f.Product, tt.f.Cycle, got, tt.want)
		}
		if got := s.IgnoredFor(tt.f, py38); got != tt.want {
			t.Errorf("IgnoredFor(%s %s) = %v, want %v", tt.f.Product, tt.f.Cycle, got, tt.want)
		}
	}
	// The display name works too, spaces and all.
	s = ignoreSnap(t, xeolv1alpha1.IgnoreRule{Products: []string{"Node.js", " gorilla toolkit "}})
	if !s.Ignored(node16, nil) || !s.Ignored(gorilla, nil) || s.Ignored(perl, nil) {
		t.Error("display names Node.js / Gorilla Toolkit should match")
	}
}

func TestIgnoreScoped(t *testing.T) {
	s := ignoreSnap(t,
		xeolv1alpha1.IgnoreRule{Products: []string{"gorilla"},
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": "grafana"}}},
		xeolv1alpha1.IgnoreRule{Products: []string{"perl"}, Namespaces: []string{"test"}},
		xeolv1alpha1.IgnoreRule{Products: []string{"python"}, Namespaces: []string{"p*"},
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"team": "legacy"}}},
	)
	// Selector on the workload's own labels.
	if !s.IgnoredFor(gorilla, grafana) || s.IgnoredFor(gorilla, prometheus) {
		t.Error("gorilla: want hidden for grafana only")
	}
	if !s.Ignored(gorilla, []Workload{grafana}) || s.Ignored(gorilla, []Workload{prometheus}) {
		t.Error("gorilla: want grafana's digest dropped, prometheus's kept")
	}
	// Namespace scope covers workloads with different labels; a shared digest drops when all match.
	if !s.Ignored(perl, []Workload{py38, py38Agent}) {
		t.Error("perl: want shared test digest dropped")
	}
	if s.Ignored(perl, []Workload{py38, legacyPay}) || s.IgnoredFor(perl, legacyPay) || !s.IgnoredFor(perl, py38) {
		t.Error("perl: want kept outside test, hidden for test rows")
	}
	if s.Ignored(perl, nil) {
		t.Error("perl: scoped rule must not drop an image with no workloads")
	}
	// namespaces AND selector.
	if !s.IgnoredFor(python38, legacyPay) || s.IgnoredFor(python38, py38) {
		t.Error("python: want hidden only where namespace and selector both match")
	}
}

func TestSnapshotInvalidIgnore(t *testing.T) {
	for _, r := range []xeolv1alpha1.IgnoreRule{
		{Products: []string{"perl"}, Selector: &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "team", Operator: "Bogus"}}}},
	} {
		if _, err := SnapshotFrom(&xeolv1alpha1.XeolScannerSpec{IgnoreProducts: []xeolv1alpha1.IgnoreRule{r}}); err == nil {
			t.Errorf("SnapshotFrom(%+v): want error", r)
		}
	}
}
