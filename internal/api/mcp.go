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
	"context"
	"encoding/json"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// toolInput is every tool's arguments: the JSON API's query string as fields.
type toolInput struct {
	Namespace  string `json:"namespace,omitempty" jsonschema:"comma-separated namespaces; empty means all"`
	Kind       string `json:"kind,omitempty" jsonschema:"comma-separated workload kinds, e.g. Deployment,DaemonSet; empty means all"`
	WithinDays *int   `json:"within_days,omitempty" jsonschema:"EOL soon window in days (default 180)"`
}

var toolDescriptions = map[string]string{
	"status":     "Headline numbers: images and workloads tracked, workloads already EOL or EOL soon, days to the next EOL, EOL database age, failed scans.",
	"namespaces": "Per namespace: workloads, workloads with EOL software, findings already EOL and EOL soon.",
	"workloads":  "Every scanned workload (namespace, kind, name) with its counts of findings already EOL and EOL soon, worst first.",
	"findings":   "End-of-life products in running workloads that are already EOL or reach EOL within within_days, soonest first: product, cycle, EOL date, days left, image, package, endoflife.date URL.",
}

// mcp serves the JSON API's four endpoints as read-only MCP tools over Streamable HTTP, stateless.
func (h *Handler) mcp() http.Handler {
	s := mcp.NewServer(&mcp.Implementation{Name: "xeol-operator", Version: "v1"}, nil)
	for name, fn := range h.endpoints() {
		if name == "vulnerabilities" {
			continue // a follow-up: it needs a description and a severity input, and returns a row per CVE
		}
		mcp.AddTool(s, &mcp.Tool{Name: name, Description: toolDescriptions[name],
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
			func(_ context.Context, _ *mcp.CallToolRequest, in toolInput) (*mcp.CallToolResult, any, error) {
				days := DefaultWithinDays
				if in.WithinDays != nil {
					days = *in.WithinDays
				}
				q, err := newQuery(in.Namespace, in.Kind, days)
				if err != nil {
					return nil, nil, err
				}
				// Text, not structured content: findings and workloads are arrays, which older clients reject there.
				b, err := json.Marshal(fn(q))
				if err != nil {
					return nil, nil, err
				}
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil, nil
			})
	}
	hh := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	// No auth on this port: refuse cross-origin browser requests so a web page can't drive it.
	return http.NewCrossOriginProtection().Handler(hh)
}
