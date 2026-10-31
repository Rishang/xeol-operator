//go:build trivy

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

package scanner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestRealTrivyMatchesSubpackages runs the trivy on PATH against the fixed-up Alpine fixture, so a
// trivy bump that stops reading the source-package properties fails here. It needs a trivy DB:
//
//	task test-trivy TRIVY_DB_DIR=<dir holding db/trivy.db>
func TestRealTrivyMatchesSubpackages(t *testing.T) {
	dbDir := os.Getenv("TRIVY_DB_DIR")
	if dbDir == "" {
		t.Skip("TRIVY_DB_DIR not set")
	}
	bin, err := exec.LookPath("trivy")
	if err != nil {
		t.Skip("trivy not on PATH")
	}
	cdx, err := os.ReadFile(fixture(t, "node16.cdx.json"))
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	path := filepath.Join(work, "sbom.cdx.json")
	if err := os.WriteFile(path, cdx, 0o600); err != nil {
		t.Fatal(err)
	}
	x := &Runner{TrivyBin: bin, TrivyCacheDir: dbDir, Timeout: time.Minute, MaxProcs: 2}
	vulns, err := x.trivy(context.Background(), work, path)
	if err != nil {
		t.Fatal(err)
	}
	byPkg := map[string]int{}
	for _, v := range vulns {
		byPkg[v.Package]++
	}
	// openssl advisories reach its subpackages only through SrcName=openssl.
	for _, pkg := range []string{"libcrypto3", "libssl3"} {
		if byPkg[pkg] == 0 {
			t.Errorf("no vulnerabilities for %s (source openssl): %v", pkg, byPkg)
		}
	}
	t.Logf("%d vulnerabilities: %v", len(vulns), byPkg)
}
