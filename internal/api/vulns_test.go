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
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Rishang/xeol-operator/internal/scanner"
)

// newTrivyHandler adds vulnerabilities to newHandler's images: python:3.8 (2 workloads in test)
// has a CRITICAL and a HIGH, node:22 (web/node22) a HIGH and a LOW.
func newTrivyHandler(t *testing.T) *Handler {
	t.Helper()
	h := newHandler(t, true)
	h.Trivy = true
	h.VulnDBUpdated = func() (time.Time, error) { return now.Add(-3 * time.Hour), nil }
	add := func(digest string, vulns ...scanner.Vuln) {
		img, _ := h.Inventory.Get(digest)
		h.Inventory.SetResult(digest, scanner.Result{Findings: img.Findings, Vulns: vulns}, "", img.Scanned, img.Next)
	}
	add("sha256:py",
		scanner.Vuln{ID: "CVE-2", Severity: "HIGH", Package: "libssl3", Version: "3.0.1", Type: "debian"},
		scanner.Vuln{ID: "CVE-1", Severity: "CRITICAL", Package: "libc6", Version: "2.36", FixedVersion: "2.36-9", Type: "debian",
			Title: "overflow", URL: "https://avd.aquasec.com/nvd/cve-1"})
	add("sha256:node",
		scanner.Vuln{ID: "CVE-3", Severity: "HIGH", Package: "libcrypto3", Version: "3.1.2-r0", FixedVersion: "3.1.4-r0", Type: "alpine"},
		scanner.Vuln{ID: "CVE-4", Severity: "LOW", Package: "busybox", Version: "1.36.1-r2", Type: "alpine"})
	return h
}

func TestVulnerabilities(t *testing.T) {
	h := serve(newTrivyHandler(t))
	var rows []VulnRow
	get(t, h, "/api/v1/vulnerabilities", &rows)
	// One row per workload, worst first: python's 2 vulns × 2 workloads + node's 2.
	if len(rows) != 6 || rows[0].Severity != "CRITICAL" || rows[5].Severity != "LOW" {
		t.Fatalf("rows = %+v", rows)
	}
	if r := rows[0]; r.ID != "CVE-1" || r.Title != "overflow" || r.FixedVersion != "2.36-9" || r.Image != "python:3.8-slim" {
		t.Errorf("row %+v", r)
	}
	get(t, h, "/api/v1/vulnerabilities?severity=critical&namespace=test", &rows)
	if len(rows) != 2 {
		t.Errorf("critical in test: %+v", rows)
	}
	get(t, h, "/api/v1/vulnerabilities?severity=All&namespace=web", &rows)
	if len(rows) != 2 {
		t.Errorf("All must not filter: %+v", rows)
	}
}

func TestVulnSummaries(t *testing.T) {
	h := serve(newTrivyHandler(t))
	var s Status
	get(t, h, "/api/v1/status", &s)
	v := s.Vulnerabilities
	// Per image digest: python's two workloads don't double its counts. Workloads with HIGH include
	// python's two (they also have a CRITICAL) and node22.
	want := map[string]int{"CRITICAL": 1, "HIGH": 2, "MEDIUM": 0, "LOW": 1, "UNKNOWN": 0}
	if v == nil || !maps.Equal(v.BySeverity, want) || v.WorkloadsCritical != 2 || v.WorkloadsHigh != 3 ||
		v.ImagesVulnerable != 2 || v.FixableCriticalHigh != 2 || *v.DBAgeSeconds != 3*3600 {
		t.Fatalf("vulnerabilities %+v", v)
	}
	var wls []WorkloadRow
	get(t, h, "/api/v1/workloads", &wls)
	for _, w := range wls {
		if w.VulnCounts == nil || // present, even when all zero
			w.Name == "agent" && (w.VulnCritical != 1 || w.VulnHigh != 1 || w.VulnFixable != 1) ||
			w.Name == "cache" && w.VulnCritical != 0 {
			t.Errorf("workload %+v", w)
		}
	}
	var nss []NamespaceRow
	get(t, h, "/api/v1/namespaces", &nss)
	for _, n := range nss {
		if n.NamespaceVulns == nil || n.Namespace == "test" && (n.WorkloadsVulnerable != 2 || n.VulnCritical != 2) {
			t.Errorf("namespace %+v", n)
		}
	}
}

func TestTrivyOff(t *testing.T) {
	h := setup(t, true)
	if code := get(t, h, "/api/v1/vulnerabilities", nil); code != http.StatusNotFound {
		t.Errorf("vulnerabilities with trivy off: %d, want 404", code)
	}
	for _, path := range []string{"/api/v1/status", "/api/v1/workloads", "/api/v1/namespaces"} {
		if body := raw(t, h, path); strings.Contains(body, `"vuln`) || strings.Contains(body, `"workloads_vulnerable"`) {
			t.Errorf("%s has vulnerability fields with trivy off: %s", path, body)
		}
	}
}

func TestEOLOff(t *testing.T) {
	hd := newTrivyHandler(t)
	hd.EOL = false
	h := serve(hd)
	if code := get(t, h, "/api/v1/findings", nil); code != http.StatusNotFound {
		t.Errorf("findings with EOL off: %d, want 404", code)
	}
	var s Status
	get(t, h, "/api/v1/status", &s)
	if s.EOLEnabled || !s.TrivyEnabled || s.Vulnerabilities == nil {
		t.Errorf("status %+v", s)
	}
}

func raw(t *testing.T, h http.Handler, url string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	return rec.Body.String()
}
