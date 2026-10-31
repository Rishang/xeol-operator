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

package main

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Rishang/xeol-operator/internal/api"
)

func TestRescanCmd(t *testing.T) {
	h := &api.Handler{Rescan: func(globs []string) []string {
		if len(globs) == 1 && globs[0] == "redis*" {
			return nil
		}
		return []string{"python:3.8-slim", "node:22-alpine"}
	}}
	srv := httptest.NewServer(h.Admin())
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	run := func(args ...string) (int, string, string) {
		var out, errOut bytes.Buffer
		code := rescanCmd(append([]string{"--addr", addr}, args...), &out, &errOut)
		return code, out.String(), errOut.String()
	}
	if code, out, _ := run("python*"); code != 0 || !strings.Contains(out, "queued 2 image(s)") ||
		!strings.Contains(out, "  node:22-alpine") {
		t.Errorf("rescan python* = %d %q", code, out)
	}
	if code, out, _ := run("redis*"); code != 0 || !strings.Contains(out, "no running image matches") {
		t.Errorf("rescan redis* = %d %q", code, out)
	}
	if code, _, errOut := run(strings.Repeat("x", 300)); code != 1 || !strings.Contains(errOut, "400") {
		t.Errorf("too long glob = %d %q", code, errOut)
	}
	var out, errOut bytes.Buffer
	code := rescanCmd([]string{"--addr", "127.0.0.1:1"}, &out, &errOut)
	if code != 1 || !strings.Contains(errOut.String(), "is the operator running") {
		t.Errorf("no operator = %d %q", code, errOut.String())
	}
}

func TestLoopbackOnly(t *testing.T) {
	for addr, ok := range map[string]bool{"127.0.0.1:8082": true, "[::1]:8082": true, "localhost:8082": true,
		":8082": false, "0.0.0.0:8082": false, "10.0.0.1:8082": false, "8082": false} {
		if err := loopbackOnly(addr); (err == nil) != ok {
			t.Errorf("loopbackOnly(%q) = %v, want ok=%v", addr, err, ok)
		}
	}
}
