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
	"strconv"
	"strings"
	"testing"

	"github.com/Rishang/xeol-operator/internal/scanner"
)

func trivyUI(t *testing.T) *Handler {
	t.Helper()
	h := newTrivyHandler(t)
	h.UI = true
	return h
}

func TestUITabs(t *testing.T) {
	eolOnly := newHandler(t, true)
	eolOnly.UI = true
	if body := raw(t, serve(eolOnly), "/?tab=trivy"); strings.Contains(body, `class="tabs"`) || !strings.Contains(body, "<h2>Findings</h2>") {
		t.Error("trivy off: want the EOL report without tabs")
	}

	h := trivyUI(t)
	body := raw(t, serve(h), "/?namespace=test")
	if !strings.Contains(body, "<h2>Findings</h2>") || !strings.Contains(body, `href="?namespace=test&amp;tab=trivy"`) {
		t.Error("both on: want the EOL report by default and a Trivy tab that keeps the filters")
	}
	// The fragment swaps the report and updates the header's tab and filter out of band.
	frag := raw(t, serve(h), "/ui/report?tab=trivy")
	if !strings.Contains(frag, "<h2>Vulnerabilities</h2>") ||
		!strings.Contains(frag, `hx-swap-oob="true"><input type="hidden" name="tab" value="trivy"`) || !strings.Contains(frag, `name="fixable"`) {
		t.Error("tab=trivy fragment: want the Trivy report and out-of-band header controls")
	}

	h.EOL = false
	if body := raw(t, serve(h), "/"); strings.Contains(body, `class="tabs"`) || !strings.Contains(body, "<h2>Vulnerabilities</h2>") {
		t.Error("EOL off: want the Trivy report at / without tabs")
	}
}

func TestUITrivyReport(t *testing.T) {
	h := trivyUI(t)
	rows := func(url string) int { return strings.Count(raw(t, serve(h), url), `<td class="cve">`) }

	var s Status
	get(t, serve(h), "/api/v1/status", &s)
	body := raw(t, serve(h), "/?tab=trivy")
	for _, want := range []string{
		"Workloads with CRITICAL</h2><b>" + strconv.Itoa(s.Vulnerabilities.WorkloadsCritical) + "</b>",
		"<small>HIGH</small><b>" + strconv.Itoa(s.Vulnerabilities.BySeverity["HIGH"]) + "</b>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("tiles lack %q", want)
		}
	}
	var api []VulnRow
	get(t, serve(h), "/api/v1/vulnerabilities", &api)
	if n := rows("/?tab=trivy"); n != len(api) {
		t.Errorf("table has %d rows, API %d", n, len(api))
	}
	if n := rows("/?tab=trivy&v_severity=CRITICAL"); n != 2 {
		t.Errorf("v_severity=CRITICAL: %d rows, want 2", n)
	}
	if n := rows("/?tab=trivy&pick=libc6"); n != 2 {
		t.Errorf("pick=libc6: %d rows, want 2", n)
	}

	img, _ := h.Inventory.Get("sha256:node")
	h.Inventory.SetResult("sha256:node", scanner.Result{Findings: img.Findings, Vulns: []scanner.Vuln{
		{ID: "<b>CVE-X</b>", Severity: "HIGH", Package: "p", URL: "javascript:alert(1)"}}}, "", img.Scanned, img.Next)
	if body := raw(t, serve(h), "/?tab=trivy&namespace=web"); strings.Contains(body, "javascript:") || strings.Contains(body, "<b>CVE-X</b>") {
		t.Error("hostile CVE id or URL rendered live")
	}
}
