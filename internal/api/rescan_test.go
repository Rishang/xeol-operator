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

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// withRescan records the globs of each rescan and queues one fake image per call.
func withRescan(hd *Handler) *[][]string {
	var calls [][]string
	hd.Rescan = func(globs []string) []string { calls = append(calls, globs); return []string{"python:3.8-slim"} }
	return &calls
}

func TestRescanAdmin(t *testing.T) {
	hd := newHandler(t, true)
	calls := withRescan(hd)
	admin := hd.Admin()
	post := func(body string) (int, string) {
		rec := httptest.NewRecorder()
		admin.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/rescan", strings.NewReader(body)))
		return rec.Code, rec.Body.String()
	}
	code, b := post(`{"images":["python:3.8*"]}`)
	var res RescanResult
	if code != http.StatusOK || json.Unmarshal([]byte(b), &res) != nil || !slices.Equal(res.Images, []string{"python:3.8-slim"}) {
		t.Fatalf("POST /rescan = %d %s", code, b)
	}
	if code, _ := post(""); code != http.StatusOK || len(*calls) != 2 || (*calls)[1] != nil {
		t.Errorf("empty body = %d, calls %q: want every image (no globs)", code, *calls)
	}
	if code, _ := post(`{"images":[` + strings.Repeat(`"x",`, maxRescanGlobs) + `"x"]}`); code != http.StatusBadRequest {
		t.Errorf("%d globs = %d, want 400", maxRescanGlobs+1, code)
	}
	if code, _ := post(`{"images":`); code != http.StatusBadRequest {
		t.Errorf("bad JSON = %d, want 400", code)
	}
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/rescan", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /rescan = %d, want 405", rec.Code)
	}
	hd.Leader = func() bool { return false }
	if code, _ := post(""); code != http.StatusServiceUnavailable {
		t.Errorf("standby = %d, want 503", code)
	}
}

func TestMCPRescan(t *testing.T) {
	for _, allow := range []bool{false, true} {
		hd := newHandler(t, true)
		hd.MCP, hd.MCPRescan = true, allow
		srv := httptest.NewServer(serve(hd))
		calls := withRescan(hd) // after the routes are built, as in cmd/main.go
		c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
		s, err := c.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp", DisableStandaloneSSE: true}, nil)
		if err != nil {
			t.Fatal(err)
		}
		tools, err := s.ListTools(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		has := slices.ContainsFunc(tools.Tools, func(tl *mcp.Tool) bool { return tl.Name == "rescan" })
		if has != allow {
			t.Errorf("allow=%v: rescan tool listed = %v", allow, has)
		}
		if allow {
			res, err := s.CallTool(t.Context(), &mcp.CallToolParams{Name: "rescan", Arguments: map[string]any{"images": []string{"python*"}}})
			if err != nil || res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "python:3.8-slim") ||
				len(*calls) != 1 || !slices.Equal((*calls)[0], []string{"python*"}) {
				t.Errorf("rescan tool: %v %+v, calls %q", err, res, *calls)
			}
		}
		_ = s.Close()
		srv.Close()
	}
}
