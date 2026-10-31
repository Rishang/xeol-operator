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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Rishang/xeol-operator/internal/config"
	"github.com/Rishang/xeol-operator/internal/scanner"
)

func ignoreSnap(t *testing.T, rules ...config.IgnoreRule) ConfigSnapshot {
	t.Helper()
	snap, err := SnapshotFrom(&config.Config{IgnoreProducts: rules})
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
		config.IgnoreRule{Products: []string{"NodeJS"}},                          // slug, any case
		config.IgnoreRule{Products: []string{"python"}, Cycles: []string{"3.8"}}, // one cycle only
		config.IgnoreRule{Products: []string{"debian"}},                          // no URL: lowercased name
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
	s = ignoreSnap(t, config.IgnoreRule{Products: []string{"Node.js", " gorilla toolkit "}})
	if !s.Ignored(node16, nil) || !s.Ignored(gorilla, nil) || s.Ignored(perl, nil) {
		t.Error("display names Node.js / Gorilla Toolkit should match")
	}
}

func TestIgnoreScoped(t *testing.T) {
	s := ignoreSnap(t,
		config.IgnoreRule{Products: []string{"gorilla"},
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": "grafana"}}},
		config.IgnoreRule{Products: []string{"perl"}, Namespaces: []string{"test"}},
		config.IgnoreRule{Products: []string{"python"}, Namespaces: []string{"p*"},
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
	for _, r := range []config.IgnoreRule{
		{Products: []string{"perl"}, Selector: &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "team", Operator: "Bogus"}}}},
	} {
		if _, err := SnapshotFrom(&config.Config{IgnoreProducts: []config.IgnoreRule{r}}); err == nil {
			t.Errorf("SnapshotFrom(%+v): want error", r)
		}
	}
}

func TestIgnoreVulnerabilities(t *testing.T) {
	snap, err := SnapshotFrom(&config.Config{IgnoreVulnerabilities: []config.VulnIgnoreRule{
		{IDs: []string{"cve-2024-0001"}},                                 // by ID, any case, everywhere
		{Packages: []string{"LIBCRYPTO3"}, Namespaces: []string{"test"}}, // by package, one namespace
		{IDs: []string{"CVE-2024-0003"}, Packages: []string{"zlib"}},     // both must match
		{Packages: []string{"perl-base"}, Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"team": "legacy"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var (
		byID      = scanner.Vuln{ID: "CVE-2024-0001", Package: "openssl"}
		crypto    = scanner.Vuln{ID: "CVE-2024-0002", Package: "libcrypto3"}
		zlibMatch = scanner.Vuln{ID: "CVE-2024-0003", Package: "zlib"}
		zlibOther = scanner.Vuln{ID: "CVE-2024-0004", Package: "zlib"}
		perlBase  = scanner.Vuln{ID: "CVE-2024-0005", Package: "perl-base"}
		all       = []scanner.Vuln{byID, crypto, zlibMatch, zlibOther, perlBase}
	)
	ids := func(vs []scanner.Vuln) (out []string) {
		for _, v := range vs {
			out = append(out, v.ID)
		}
		return out
	}
	for _, tt := range []struct {
		name string
		got  []scanner.Vuln
		want []string
	}{
		{"test workload", snap.VulnsFor(all, py38), []string{"CVE-2024-0004", "CVE-2024-0005"}},
		{"other namespace", snap.VulnsFor(all, grafana), []string{"CVE-2024-0002", "CVE-2024-0004", "CVE-2024-0005"}},
		{"selector", snap.VulnsFor(all, legacyPay), []string{"CVE-2024-0002", "CVE-2024-0004"}},
		// libcrypto3 is hidden in test only, so an image shared with monitoring keeps it in metrics.
		{"shared image", snap.ImageVulns(all, []Workload{py38, grafana}), []string{"CVE-2024-0002", "CVE-2024-0004", "CVE-2024-0005"}},
		{"image only in test", snap.ImageVulns(all, []Workload{py38, py38Agent}), []string{"CVE-2024-0004", "CVE-2024-0005"}},
	} {
		if got := ids(tt.got); !slices.Equal(got, tt.want) {
			t.Errorf("%s: %v, want %v", tt.name, got, tt.want)
		}
	}
	if none := (ConfigSnapshot{}).VulnsFor(all, py38); &none[0] != &all[0] {
		t.Error("no rules: want the input slice back, not a copy")
	}
}
