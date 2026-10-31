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
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"

	"github.com/Rishang/xeol-operator/internal/scanner"
)

func TestParseImageID(t *testing.T) {
	tests := []struct {
		in, repo, digest string
		ok               bool
	}{
		{"docker.io/library/python@sha256:1d52", "docker.io/library/python", "sha256:1d52", true},
		{"docker-pullable://python@sha256:1d52", "python", "sha256:1d52", true},
		{"sha256:1d52", "", "", false}, // local-only image, nothing to pull
		{"", "", "", false},
		{"ghcr.io/x/y@md5:zz", "", "", false},
	}
	for _, tt := range tests {
		repo, digest, ok := parseImageID(tt.in)
		if repo != tt.repo || digest != tt.digest || ok != tt.ok {
			t.Errorf("parseImageID(%q) = %q, %q, %v", tt.in, repo, digest, ok)
		}
	}
}

func TestExcluded(t *testing.T) {
	if !excluded([]string{"kube-*"}, "kube-system") || excluded([]string{"kube-*"}, "payments") {
		t.Error("namespace glob")
	}
	if !excluded([]string{"registry.internal/*"}, "", "registry.internal/app") ||
		!excluded([]string{"registry.internal/*"}, "registry.internal/team/app:1.2") ||
		!excluded([]string{"*/team/*"}, "ghcr.io/team/app") || excluded([]string{"registry.internal/*"}, "docker.io/app") {
		t.Error("image glob: * must cross /")
	}
	if !excluded([]string{"python:3.?"}, "python:3.8") || excluded([]string{"a.b"}, "axb") {
		t.Error("? is one character, . is literal")
	}
	if excluded([]string{"*"}, "") {
		t.Error("empty value must not match")
	}
}

func TestInventoryDue(t *testing.T) {
	inv := NewInventory()
	now := time.Now()
	p := types.NamespacedName{Namespace: "a", Name: "p"}
	if added, _ := inv.SetPod(p, []PodImage{{Digest: "sha256:a"}, {Digest: "sha256:b"}}); len(added) != 2 {
		t.Fatalf("added = %v", added)
	}
	if added, _ := inv.SetPod(p, []PodImage{{Digest: "sha256:a"}, {Digest: "sha256:b"}}); len(added) != 0 {
		t.Fatalf("re-adding the same pod added %v", added)
	}
	inv.SetResult("sha256:a", scanner.Result{}, "", now, now.Add(time.Hour))
	if due := inv.Due(now); len(due) != 1 || due[0] != "sha256:b" {
		t.Fatalf("due = %v, want only b", due)
	}
	inv.DeletePod(p)
	if len(inv.Images()) != 0 {
		t.Fatal("images left after last pod deleted")
	}
}

func TestInventoryKeepsResultsOnPodUpdate(t *testing.T) {
	inv := NewInventory()
	p := types.NamespacedName{Namespace: "a", Name: "p"}
	inv.SetPod(p, []PodImage{{Digest: "sha256:a"}, {Digest: "sha256:b"}})
	inv.SetResult("sha256:a", scanner.Result{}, "", time.Now(), time.Now().Add(time.Hour))
	if _, removed := inv.SetPod(p, []PodImage{{Digest: "sha256:a"}}); len(removed) != 1 || removed[0] != "sha256:b" { // b removed, a unchanged
		t.Fatalf("removed = %v, want [sha256:b]", removed)
	}
	img, ok := inv.Get("sha256:a")
	if !ok || img.Scanned.IsZero() {
		t.Fatal("unchanged digest lost its result")
	}
	if _, ok := inv.Get("sha256:b"); ok {
		t.Fatal("digest no longer run by any pod kept")
	}
}

func TestInventoryRepoFollowsRunningPods(t *testing.T) {
	inv := NewInventory()
	a, b := types.NamespacedName{Namespace: "x", Name: "a"}, types.NamespacedName{Namespace: "x", Name: "b"}
	inv.SetPod(a, []PodImage{{Digest: "sha256:d", Repo: "mirror.example/app"}})
	inv.SetPod(b, []PodImage{{Digest: "sha256:d", Repo: "docker.io/app"}})
	inv.DeletePod(a)
	if img, _ := inv.Get("sha256:d"); img.Repo != "docker.io/app" {
		t.Fatalf("repo = %q, want the remaining Pod's", img.Repo)
	}
}

func TestInventoryRestoreKeepsFindingsOfFailedScan(t *testing.T) {
	inv := NewInventory()
	inv.SetPod(types.NamespacedName{Namespace: "x", Name: "a"}, []PodImage{{Digest: "sha256:d"}})
	now := time.Now()
	f := []scanner.Finding{{Product: "Python"}}
	inv.SetResult("sha256:d", scanner.Result{Findings: f}, "auth", now, now) // restored: last scan failed, earlier findings saved
	inv.SetResult("sha256:d", scanner.Result{}, "auth", now, now)            // another failed scan keeps them
	if img, _ := inv.Get("sha256:d"); len(img.Findings) != 1 {
		t.Fatalf("findings = %v", img.Findings)
	}
}

func TestInventoryTrivyFailureKeepsVulns(t *testing.T) {
	inv := NewInventory()
	inv.SetPod(types.NamespacedName{Namespace: "x", Name: "a"}, []PodImage{{Digest: "sha256:d"}})
	now := time.Now()
	old := []scanner.Vuln{{ID: "CVE-1", Package: "libcrypto3"}}
	inv.SetResult("sha256:d", scanner.Result{Findings: []scanner.Finding{{Product: "Python", Cycle: "3.8"}}, Vulns: old}, "", now, now)
	// Next scan: xeol succeeds with new findings, trivy fails.
	inv.SetResult("sha256:d", scanner.Result{Findings: []scanner.Finding{{Product: "Python", Cycle: "3.9"}}}, scanner.ReasonTrivy, now, now)
	img, _ := inv.Get("sha256:d")
	if len(img.Findings) != 1 || img.Findings[0].Cycle != "3.9" {
		t.Errorf("findings = %v, want the new scan's", img.Findings)
	}
	if len(img.Vulns) != 1 || img.Vulns[0].ID != "CVE-1" || img.ErrReason != scanner.ReasonTrivy {
		t.Errorf("vulns = %v, reason %q; want the previous vulns kept and reason trivy", img.Vulns, img.ErrReason)
	}
	// A later successful trivy run with nothing found replaces them.
	inv.SetResult("sha256:d", scanner.Result{Findings: img.Findings, Vulns: []scanner.Vuln{}}, "", now, now)
	if img, _ := inv.Get("sha256:d"); img.Vulns == nil || len(img.Vulns) != 0 {
		t.Errorf("vulns = %#v, want empty", img.Vulns)
	}
}
