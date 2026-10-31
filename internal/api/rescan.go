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
	"fmt"
	"io"
	"net/http"
)

// RescanRequest is the body of the admin listener's POST /rescan and the MCP rescan tool's input.
type RescanRequest struct {
	Images []string `json:"images,omitempty" jsonschema:"image globs, as in excludeImages: matched against the image as written in the Pod spec (python:3.8-slim) or its repository (docker.io/library/python); * matches anything, / included. Empty means every running image."`
}

// RescanResult lists the images queued for a rescan.
type RescanResult struct {
	Images []string `json:"images"`
}

// Request caps: the globs are compiled per request, so keep one request small.
const (
	maxRescanGlobs  = 100
	maxRescanGlobSz = 256
)

func (h *Handler) rescan(req RescanRequest) (RescanResult, error) {
	if h.Rescan == nil {
		return RescanResult{}, errors.New("rescan is not available")
	}
	if len(req.Images) > maxRescanGlobs {
		return RescanResult{}, fmt.Errorf("at most %d image globs", maxRescanGlobs)
	}
	for _, g := range req.Images {
		if len(g) > maxRescanGlobSz {
			return RescanResult{}, fmt.Errorf("image glob longer than %d characters", maxRescanGlobSz)
		}
	}
	return RescanResult{Images: nonNil(h.Rescan(req.Images))}, nil
}

// Admin serves POST /rescan for the `manager rescan` CLI. Mount it only on a loopback listener: it
// has no auth, and who may reach it is decided by who may exec into the pod.
func (h *Handler) Admin() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("POST /rescan", h.leaderOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req RescanRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			http.Error(w, "bad request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		res, err := h.rescan(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	})))
	return mux
}
