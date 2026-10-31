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

	"k8s.io/apimachinery/pkg/types"
)

func TestRescanMatch(t *testing.T) {
	inv := NewInventory()
	w := Workload{Namespace: "test", Kind: "Deployment", Name: "x"}
	inv.SetPod(types.NamespacedName{Namespace: "test", Name: "a"}, []PodImage{
		{Digest: "sha256:py", Repo: "docker.io/library/python", Ref: "python:3.8-slim", Workload: w},
		{Digest: "sha256:node", Repo: "docker.io/library/node", Ref: "node:22-alpine", Workload: w},
		{Digest: "sha256:int", Repo: "registry.internal/team/app", Ref: "registry.internal/team/app:v1", Workload: w},
	})
	for _, tt := range []struct {
		globs []string
		want  []string
	}{
		{nil, []string{"registry.internal/team/app:v1", "node:22-alpine", "python:3.8-slim"}}, // all, by digest order
		{[]string{"python:3.8*"}, []string{"python:3.8-slim"}},                                // the ref
		{[]string{"docker.io/library/node"}, []string{"node:22-alpine"}},                      // the repository
		{[]string{"registry.internal/*", "python*"}, []string{"registry.internal/team/app:v1", "python:3.8-slim"}},
		{[]string{"redis*"}, []string{}},
	} {
		var queued []string
		got := inv.Rescan(tt.globs, func(d string) { queued = append(queued, d) })
		if !slices.Equal(got, tt.want) || len(queued) != len(tt.want) {
			t.Errorf("Rescan(%q) = %q, queued %q; want %q", tt.globs, got, queued, tt.want)
		}
	}
}
