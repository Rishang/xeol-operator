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
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"k8s.io/apimachinery/pkg/types"

	"github.com/Rishang/xeol-operator/internal/controller"
	"github.com/Rishang/xeol-operator/internal/scanner"
)

func TestUIAndMCPOff(t *testing.T) {
	h := setup(t, true)
	for _, p := range []string{"/", "/mcp"} {
		if code := get(t, h, p, nil); code != http.StatusNotFound {
			t.Errorf("%s with flag off: code = %d, want 404", p, code)
		}
	}
}

func TestUI(t *testing.T) {
	hd := newHandler(t, true)
	hd.UI = true
	h := serve(hd)
	body := func(url string) (int, string) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
		return rec.Code, rec.Body.String()
	}
	code, b := body("/")
	if code != http.StatusOK || !strings.Contains(b, `<div class="tile bad"><h2 title="Workloads running software past end of life">Workloads past EOL</h2><b>3</b>`) ||
		!strings.Contains(b, `<small>auth</small><b>0</b>`) || !strings.Contains(b, `<small>oom</small><b>1</b>`) ||
		!strings.Contains(b, `<li>web/cache (Deployment): redis:7</li>`) || !strings.Contains(b, `No scans failed with <strong>auth</strong>`) ||
		!strings.Contains(b, `<b>30<span>days</span></b>`) || !strings.Contains(b, "python38") {
		t.Fatalf("GET / = %d:\n%s", code, b)
	}
	// The fragment honours filters and is just the report.
	if code, b := body("/ui/report?namespace=web&within_days=0"); code != http.StatusOK || strings.Contains(b, "<html") ||
		strings.Contains(b, "python38") || !strings.Contains(b, "node22") {
		t.Fatalf("GET /ui/report = %d:\n%s", code, b)
	}
	// q keeps only table rows containing it, case-insensitively.
	if _, b := body("/ui/report?q=AGENT"); !strings.Contains(b, "agent") || strings.Contains(b, "node22") {
		t.Errorf("q=AGENT should keep only the agent DaemonSet:\n%s", b)
	}
	if code, _ := body("/ui/report?within_days=-1"); code != http.StatusBadRequest {
		t.Errorf("bad within_days = %d, want 400", code)
	}
	if code, _ := body("/nope"); code != http.StatusNotFound {
		t.Errorf("GET /nope = %d, want 404", code)
	}
	hd.Leader = func() bool { return false }
	if code, b := body("/"); code != http.StatusOK || !strings.Contains(b, "on standby") || strings.Contains(b, "python38") {
		t.Errorf("standby GET / = %d, want the standby notice", code)
	}
}

func TestUIFindingsTable(t *testing.T) {
	hd := newHandler(t, true)
	hd.UI = true
	h := serve(hd)
	page := func(url string) string {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
		return rec.Body.String()
	}
	before := func(b, x, y string) bool {
		i, j := strings.Index(b, x), strings.Index(b, y)
		return i >= 0 && j >= 0 && i < j
	}
	// Rows: Gorilla and Node.js (node22), Python (python38, agent).
	if b := page("/ui/report?sort=product"); !before(b, "<td>Gorilla</td>", "<td>Node.js</td>") || !before(b, "<td>Node.js</td>", "<td>Python</td>") {
		t.Errorf("sort=product: want Gorilla, Node.js, Python")
	}
	// The clicked header (last sort) wins over the page's current one.
	if b := page("/ui/report?sort=product&sort=-product"); !before(b, "<td>Python</td>", "<td>Gorilla</td>") || !strings.Contains(b, `value="product" title="Sort by product">product<span aria-hidden="true">▼`) {
		t.Errorf("sort=-product: want Python first and a ▼ header that toggles back")
	}
	if b := page("/ui/report?f_product=NODE"); !strings.Contains(b, "<td>Node.js</td>") || strings.Contains(b, "<td>Python</td>") || strings.Contains(b, "<td>Gorilla</td>") {
		t.Errorf("f_product=NODE: want only Node.js")
	}
	// Checked values: rows equal to one of them; the funnel shows as active; clear drops the column's filter.
	if b := page("/ui/report?v_product=Python&v_product=Gorilla"); strings.Contains(b, "<td>Node.js</td>") ||
		!strings.Contains(b, "<td>Python</td>") || !strings.Contains(b, `value="Gorilla" form="filters" checked`) ||
		!strings.Contains(b, `class="funnel on"`) {
		t.Errorf("v_product: want Python and Gorilla rows only, checked, funnel on")
	}
	if b := page("/ui/report?v_product=Python&clear=product"); !strings.Contains(b, "<td>Node.js</td>") || strings.Contains(b, `class="funnel on"`) {
		t.Errorf("clear=product: want the product filter dropped")
	}
	if b := page("/?within_days=45"); !strings.Contains(b, `<option value="45" selected>45 days</option>`) ||
		!strings.Contains(b, `<option value="90" >90 days</option>`) {
		t.Errorf("window dropdown: want presets plus the URL's 45 days selected")
	}
	// A runway dot filters to its product cycle and dims the others; clicking it again clears the filter.
	if b := page("/ui/report?pick=Python%093.8"); strings.Contains(b, "<td>Node.js</td>") || !strings.Contains(b, "<td>Python</td>") ||
		!strings.Contains(b, `class="mark eol on"`) || !strings.Contains(b, " dim\"") {
		t.Errorf("pick: want only Python rows, its dot on and the rest dimmed")
	}
	if b := page("/ui/report?pick=Python%093.8&v_product=Python&v_cycle=3.8"); !strings.Contains(b, "<td>Node.js</td>") || strings.Contains(b, " dim\"") {
		t.Errorf("pick again: want the filter cleared")
	}
	if b := page("/ui/report?f_name=nothing-matches"); !strings.Contains(b, "No findings match these filters.") {
		t.Errorf("want the no-match row")
	}
}

func TestUIEscapes(t *testing.T) {
	hd := newHandler(t, true)
	hd.UI = true
	hd.Inventory.SetPod(types.NamespacedName{Namespace: "x", Name: "p"}, []controller.PodImage{{Digest: "sha256:x",
		Ref: `<script>alert(1)</script>`, Workload: controller.Workload{Namespace: "x", Kind: "Pod", Name: "p"}}})
	hd.Inventory.SetResult("sha256:x", []scanner.Finding{{Product: "P", EOL: now.Add(-time.Hour), URL: "javascript:alert(1)"}}, "", now, now)
	rec := httptest.NewRecorder()
	serve(hd).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if b := rec.Body.String(); strings.Contains(b, "<script>alert") || strings.Contains(b, `href="javascript:`) {
		t.Fatal("cluster data rendered unescaped")
	}
}

func TestMCP(t *testing.T) {
	hd := newHandler(t, true)
	hd.MCP = true
	srv := httptest.NewServer(serve(hd))
	defer srv.Close()

	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
	s, err := c.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp", DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	tools, err := s.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(tools.Tools))
	for _, tl := range tools.Tools {
		names = append(names, tl.Name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"findings", "namespaces", "status", "workloads"}) {
		t.Fatalf("tools = %v", names)
	}

	// Same JSON as the HTTP endpoint.
	res, err := s.CallTool(t.Context(), &mcp.CallToolParams{Name: "findings", Arguments: map[string]any{"within_days": 0, "namespace": "test"}})
	if err != nil || res.IsError {
		t.Fatalf("findings: %v %+v", err, res)
	}
	var got, want []FindingRow
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &got); err != nil {
		t.Fatal(err)
	}
	get(t, serve(hd), "/api/v1/findings?within_days=0&namespace=test", &want)
	if len(got) == 0 || !slices.EqualFunc(got, want, func(a, b FindingRow) bool { return a.Name == b.Name && a.Product == b.Product }) {
		t.Fatalf("mcp findings = %+v, want %+v", got, want)
	}

	res, err = s.CallTool(t.Context(), &mcp.CallToolParams{Name: "status", Arguments: map[string]any{"within_days": -1}})
	if err != nil || !res.IsError {
		t.Fatalf("negative within_days: want a tool error, got %v %+v", err, res)
	}
}
