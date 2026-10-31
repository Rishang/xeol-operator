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

package metrics

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/Rishang/xeol-operator/internal/config"
	"github.com/Rishang/xeol-operator/internal/controller"
	"github.com/Rishang/xeol-operator/internal/scanner"
)

func setup(t *testing.T) (*controller.Inventory, *Collector) {
	t.Helper()
	inv := controller.NewInventory()
	cfg := &controller.Config{}
	cfg.Set(controller.ConfigSnapshot{WorkloadLabels: []string{"team", "app.kubernetes.io/part-of", "team", "app_kubernetes_io_part_of"}}) // repeats dropped
	built := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	return inv, &Collector{Inventory: inv, Config: cfg, DBBuilt: func() (time.Time, error) { return built, nil }}
}

func TestCollect(t *testing.T) {
	inv, c := setup(t)
	pod := types.NamespacedName{Namespace: "payments", Name: "api-7d9f-x"}
	inv.SetPod(pod, []controller.PodImage{{
		Digest: "sha256:abc", Repo: "docker.io/library/python", Ref: "python:3.8-slim",
		Workload: controller.Workload{Namespace: "payments", Kind: "Deployment", Name: "api",
			Labels: map[string]string{"team": "pay", "app.kubernetes.io/part-of": "shop"}},
	}})
	eol, _ := time.Parse(time.DateOnly, "2024-10-07")
	f := scanner.Finding{Product: "Python", Cycle: "3.8", EOL: eol, Package: "python", Version: "3.8.20", URL: "https://endoflife.date/python"}
	inv.SetResult("sha256:abc", scanner.Result{Findings: []scanner.Finding{f}}, "", time.Now(), time.Now().Add(time.Hour))

	want := `
# HELP xeol_db_built_timestamp_seconds When the EOL database in use was built (Unix seconds).
# TYPE xeol_db_built_timestamp_seconds gauge
xeol_db_built_timestamp_seconds 1.7929728e+09
# HELP xeol_eol_date_seconds End-of-life date (Unix seconds) of a product found in a running image. Compare with time() in PromQL.
# TYPE xeol_eol_date_seconds gauge
xeol_eol_date_seconds{cycle="3.8",digest="sha256:abc",image="python:3.8-slim",package="python",product="Python",url="https://endoflife.date/python",version="3.8.20"} 1.7282592e+09
# HELP xeol_image_workload A workload running an image digest; join on digest with xeol_eol_date_seconds.
# TYPE xeol_image_workload gauge
xeol_image_workload{digest="sha256:abc",k8s_label_app_kubernetes_io_part_of="shop",k8s_label_team="pay",kind="Deployment",name="api",namespace="payments"} 1
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(want)); err != nil {
		t.Fatal(err)
	}
}

func TestNoStaleSeries(t *testing.T) {
	inv, c := setup(t)
	pod := types.NamespacedName{Namespace: "a", Name: "p"}
	inv.SetPod(pod, []controller.PodImage{{Digest: "sha256:abc", Repo: "r", Workload: controller.Workload{Namespace: "a", Kind: "Pod", Name: "p"}}})
	inv.SetResult("sha256:abc", scanner.Result{Findings: []scanner.Finding{{Product: "X", Cycle: "1", EOL: time.Unix(0, 0)}}}, "", time.Now(), time.Now())
	if n := testutil.CollectAndCount(c, "xeol_eol_date_seconds"); n != 1 {
		t.Fatalf("before delete: %d series", n)
	}
	inv.DeletePod(pod)
	if n := testutil.CollectAndCount(c, "xeol_eol_date_seconds", "xeol_image_workload"); n != 0 {
		t.Fatalf("after delete: %d series, want 0", n)
	}
}

func TestNoDBNoBuiltMetric(t *testing.T) {
	inv, c := setup(t)
	_ = inv
	c.DBBuilt = func() (time.Time, error) { return time.Time{}, errors.New("missing") }
	if n := testutil.CollectAndCount(c, "xeol_db_built_timestamp_seconds"); n != 0 {
		t.Fatalf("got %d series", n)
	}
}

func TestRegister(t *testing.T) {
	_, c := setup(t)
	reg := prometheus.NewRegistry()
	if err := Register(reg, c); err != nil {
		t.Fatal(err)
	}
	Observe(3*time.Second, scanner.ReasonAuth)
	if v := testutil.ToFloat64(ScanErrors.WithLabelValues(scanner.ReasonAuth)); v != 1 {
		t.Fatalf("auth errors = %v", v)
	}
}

func TestCollectIgnores(t *testing.T) {
	inv, c := setup(t)
	snap, err := controller.SnapshotFrom(&config.Config{IgnoreProducts: []config.IgnoreRule{
		{Products: []string{"gorilla"}, Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": "grafana"}}},
		{Products: []string{"perl"}, Namespaces: []string{"test"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	c.Config.Set(snap)
	run := func(ns, name, digest string, labels map[string]string, products ...string) {
		inv.SetPod(types.NamespacedName{Namespace: ns, Name: name}, []controller.PodImage{{Digest: digest,
			Workload: controller.Workload{Namespace: ns, Kind: "Deployment", Name: name, Labels: labels}}})
		fs := make([]scanner.Finding, 0, len(products))
		for _, p := range products {
			fs = append(fs, scanner.Finding{Product: p, Cycle: "1", URL: "https://endoflife.date/" + p})
		}
		inv.SetResult(digest, scanner.Result{Findings: fs}, "", time.Now(), time.Now().Add(time.Hour))
	}
	run("monitoring", "grafana", "sha256:graf", map[string]string{"app.kubernetes.io/name": "grafana"}, "gorilla")
	run("monitoring", "prometheus", "sha256:prom", nil, "gorilla")
	run("test", "python38", "sha256:py", nil, "perl", "python")
	run("test", "python38-agent", "sha256:py", nil, "perl", "python") // same digest, also in test
	run("web", "shared", "sha256:web", nil, "perl")
	run("test", "shared-too", "sha256:web", nil, "perl") // same digest in test and web: kept

	// Left: prometheus gorilla, py python, web perl.
	if n := testutil.CollectAndCount(c, "xeol_eol_date_seconds"); n != 3 {
		t.Fatalf("series = %d, want 3", n)
	}
}

func TestCollectVulns(t *testing.T) {
	inv, c := setup(t)
	updated := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	c.VulnDBUpdated = func() (time.Time, error) { return updated, nil }
	pod := types.NamespacedName{Namespace: "a", Name: "p"}
	inv.SetPod(pod, []controller.PodImage{{Digest: "sha256:abc", Repo: "r", Workload: controller.Workload{Namespace: "a", Kind: "Pod", Name: "p"}}})
	inv.SetResult("sha256:abc", scanner.Result{Vulns: []scanner.Vuln{
		{ID: "CVE-1", Severity: "CRITICAL", FixedVersion: "1.1"},
		{ID: "CVE-2", Severity: "CRITICAL", FixedVersion: "1.2"},
		{ID: "CVE-3", Severity: "HIGH"},
	}}, "", time.Now(), time.Now().Add(time.Hour))
	want := `
# HELP xeol_vuln_db_updated_timestamp_seconds When the trivy vulnerability database in use was published (Unix seconds).
# TYPE xeol_vuln_db_updated_timestamp_seconds gauge
xeol_vuln_db_updated_timestamp_seconds 1.793232e+09
# HELP xeol_vulnerabilities Known vulnerabilities in a running image digest: unique (CVE, package) rows by severity and whether a fixed version exists. Join on digest with xeol_image_workload.
# TYPE xeol_vulnerabilities gauge
xeol_vulnerabilities{digest="sha256:abc",fixable="false",severity="HIGH"} 1
xeol_vulnerabilities{digest="sha256:abc",fixable="true",severity="CRITICAL"} 2
`
	// No MEDIUM/LOW series and no CRITICAL/fixable=false series: zero counts are omitted.
	if err := testutil.CollectAndCompare(c, strings.NewReader(want), "xeol_vulnerabilities", "xeol_vuln_db_updated_timestamp_seconds"); err != nil {
		t.Fatal(err)
	}
	inv.DeletePod(pod)
	if n := testutil.CollectAndCount(c, "xeol_vulnerabilities"); n != 0 {
		t.Fatalf("after delete: %d series, want 0", n)
	}
}
