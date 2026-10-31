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

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/Rishang/xeol-operator/internal/config"
	"github.com/Rishang/xeol-operator/internal/controller"
	"github.com/Rishang/xeol-operator/internal/scanner"
)

var now = time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)

func setup(t *testing.T, leader bool, ignores ...config.IgnoreRule) http.Handler {
	t.Helper()
	return serve(newHandler(t, leader, ignores...))
}

func serve(h *Handler) http.Handler {
	mux := http.NewServeMux()
	for p, hh := range h.Routes() {
		mux.Handle(p, hh)
	}
	return mux
}

func newHandler(t *testing.T, leader bool, ignores ...config.IgnoreRule) *Handler {
	t.Helper()
	inv := controller.NewInventory()
	wl := func(ns, kind, name string) controller.Workload {
		return controller.Workload{Namespace: ns, Kind: kind, Name: name}
	}
	// python:3.8 runs in a Deployment and a DaemonSet; node:22 in another Deployment; one image unscanned.
	inv.SetPod(types.NamespacedName{Namespace: "test", Name: "a"}, []controller.PodImage{{Digest: "sha256:py", Ref: "python:3.8-slim", Workload: wl("test", "Deployment", "python38")}})
	inv.SetPod(types.NamespacedName{Namespace: "test", Name: "b"}, []controller.PodImage{{Digest: "sha256:py", Ref: "python:3.8-slim", Workload: controller.Workload{Namespace: "test", Kind: "DaemonSet", Name: "agent", Labels: map[string]string{"team": "platform"}}}})
	inv.SetPod(types.NamespacedName{Namespace: "web", Name: "c"}, []controller.PodImage{{Digest: "sha256:node", Ref: "node:22-alpine", Workload: wl("web", "Deployment", "node22")}})
	inv.SetPod(types.NamespacedName{Namespace: "web", Name: "d"}, []controller.PodImage{{Digest: "sha256:new", Ref: "redis:7", Workload: wl("web", "Deployment", "cache")}})
	day := 24 * time.Hour
	inv.SetResult("sha256:py", scanner.Result{Findings: []scanner.Finding{
		{Product: "Python", Cycle: "3.8", EOL: now.Add(-750 * day), Package: "python", Version: "3.8.20", URL: "https://endoflife.date/python"},
		{Product: "Debian", Cycle: "12", EOL: now.Add(600 * day)},
	}}, "", now, now.Add(day))
	inv.SetResult("sha256:node", scanner.Result{Findings: []scanner.Finding{{Product: "Node.js", Cycle: "22", EOL: now.Add(30 * day)},
		{Product: "Gorilla", Cycle: "1", EOL: time.Unix(0, 0)}}}, "", now, now.Add(day)) // EOL without a date
	inv.SetResult("sha256:new", scanner.Result{}, scanner.ReasonOOM, now, now.Add(day))
	cfg := &controller.Config{}
	snap, err := controller.SnapshotFrom(&config.Config{IgnoreProducts: ignores})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Set(snap)
	h := &Handler{EOL: true, Inventory: inv, Config: cfg, Now: func() time.Time { return now }, Leader: func() bool { return leader },
		DBBuilt: func() (time.Time, error) { return now.Add(-2 * time.Hour), nil }}
	return h
}

func get(t *testing.T, h http.Handler, url string, out any) int {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code == http.StatusOK && out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("%s: %v: %s", url, err, rec.Body.String())
		}
	}
	return rec.Code
}

func TestFindings(t *testing.T) {
	h := setup(t, true)
	var rows []FindingRow
	get(t, h, "/api/v1/findings", &rows)
	// Python in 2 workloads + Node 22 (30 days, inside the default 180); Debian (600 days) is outside.
	if len(rows) != 4 || rows[0].Product != "Gorilla" || rows[0].EOL != "unknown" || rows[0].DaysLeft != nil ||
		rows[1].Product != "Python" || *rows[1].DaysLeft != -750 || rows[3].Product != "Node.js" || *rows[3].DaysLeft != 30 {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[1].URL != "https://endoflife.date/python" || rows[1].EOL == "" {
		t.Fatalf("missing url/eol: %+v", rows[1])
	}
	get(t, h, "/api/v1/findings?within_days=1000", &rows)
	if len(rows) != 6 {
		t.Fatalf("within_days=1000: %d rows", len(rows))
	}
	get(t, h, "/api/v1/findings?namespace=web&kind=Deployment", &rows)
	if len(rows) != 2 || rows[1].Name != "node22" {
		t.Fatalf("filtered rows = %+v", rows)
	}
	get(t, h, "/api/v1/findings?namespace=$__all", &rows)
	if len(rows) != 4 {
		t.Fatalf("Grafana All should not filter: %d rows", len(rows))
	}
	get(t, h, "/api/v1/findings?namespace=nope", &rows)
	if rows == nil || len(rows) != 0 {
		t.Fatalf("empty result must be [] not null: %+v", rows)
	}
}

func TestIgnoreProducts(t *testing.T) {
	var rows []FindingRow
	// Global rule, matched by the lowercased name when there is no URL ("Gorilla").
	get(t, setup(t, true, config.IgnoreRule{Products: []string{"GORILLA"}}), "/api/v1/findings", &rows)
	if len(rows) != 3 || rows[0].Product != "Python" {
		t.Fatalf("global: rows = %+v", rows)
	}
	// Shared python digest: hidden for the DaemonSet (team=platform) only.
	h := setup(t, true, config.IgnoreRule{Products: []string{"python"},
		Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"team": "platform"}}})
	get(t, h, "/api/v1/findings", &rows)
	if len(rows) != 3 || rows[1].Product != "Python" || rows[1].Name != "python38" {
		t.Fatalf("selector: rows = %+v", rows)
	}
	var wls []WorkloadRow
	get(t, h, "/api/v1/workloads", &wls)
	for _, w := range wls {
		if w.Name == "agent" && w.EOLNow != 0 {
			t.Fatalf("agent still counts ignored python: %+v", w)
		}
	}
	// Namespace scope: both python workloads are in test.
	get(t, setup(t, true, config.IgnoreRule{Products: []string{"python"}, Namespaces: []string{"te*"}}), "/api/v1/findings", &rows)
	if len(rows) != 2 || rows[0].Product != "Gorilla" || rows[1].Product != "Node.js" {
		t.Fatalf("namespaces: rows = %+v", rows)
	}
}

func TestWorkloadsAndNamespaces(t *testing.T) {
	h := setup(t, true)
	var wls []WorkloadRow
	get(t, h, "/api/v1/workloads", &wls)
	if len(wls) != 4 || wls[0].Name != "node22" || wls[0].EOLNow != 1 || wls[0].EOLSoon != 1 {
		t.Fatalf("workloads = %+v", wls)
	}
	var nss []NamespaceRow
	get(t, h, "/api/v1/namespaces", &nss)
	if len(nss) != 2 || nss[0].Namespace != "test" || nss[0].EOLNow != 2 || nss[0].WorkloadsEOL != 2 || nss[1].EOLSoon != 1 || nss[1].Workloads != 2 || nss[1].EOLNow != 1 {
		t.Fatalf("namespaces = %+v", nss)
	}
}

func TestStatus(t *testing.T) {
	hd := newHandler(t, true)
	h := serve(hd)
	var s Status
	get(t, h, "/api/v1/status", &s)
	if s.ImagesTracked != 3 || s.ImagesScanned != 3 || s.Workloads != 4 || s.WorkloadsEOL != 3 || s.WorkloadsEOLSoon != 0 ||
		s.NextEOLDays == nil || *s.NextEOLDays != 30 || s.DBAgeSeconds == nil || *s.DBAgeSeconds != 7200 ||
		s.FailedScans != 1 || s.FailedScansByCause["oom"] != 1 || s.LastSweep != now.UTC().Format(time.RFC3339) || s.ConfigError != "" {
		t.Fatalf("status = %+v", s)
	}
	hd.Config.SetError(errors.New("parsing config: unknown field \"intervall\""))
	get(t, h, "/api/v1/status", &s)
	if !strings.Contains(s.ConfigError, "intervall") {
		t.Fatalf("config_error = %q", s.ConfigError)
	}
}

func TestNotLeaderAndBadInput(t *testing.T) {
	if code := get(t, setup(t, false), "/api/v1/status", nil); code != http.StatusServiceUnavailable {
		t.Fatalf("non-leader code = %d", code)
	}
	if code := get(t, setup(t, true), "/api/v1/findings?within_days=x", nil); code != http.StatusBadRequest {
		t.Fatalf("bad within_days code = %d", code)
	}
}
