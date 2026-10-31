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
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

type cdxComp struct {
	Name       string `json:"name"`
	Version    string `json:"version"`
	PURL       string `json:"purl"`
	Properties []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"properties"`
}

// srcProps returns a component's trivy source properties by short name.
func srcProps(c cdxComp) map[string]string {
	m := map[string]string{}
	for _, p := range c.Properties {
		if len(p.Name) > len(trivyProp) && p.Name[:len(trivyProp)] == trivyProp {
			m[p.Name[len(trivyProp):]] = p.Value
		}
	}
	return m
}

func fixedUp(t *testing.T, file string) (before, after map[string]cdxComp) {
	t.Helper()
	b, err := os.ReadFile(fixture(t, file))
	if err != nil {
		t.Fatal(err)
	}
	out, err := fixUpCycloneDX(b)
	if err != nil {
		t.Fatal(err)
	}
	index := func(b []byte) map[string]cdxComp {
		var doc struct{ Components []cdxComp }
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatal(err)
		}
		m := map[string]cdxComp{}
		for _, c := range doc.Components {
			m[c.Name] = c
		}
		return m
	}
	return index(b), index(out)
}

func TestFixUpCycloneDX(t *testing.T) {
	tests := []struct {
		file, pkg string
		want      map[string]string
	}{
		// apk: trivy uses the full version as SrcVersion, no release.
		{"node16.cdx.json", "libcrypto3", map[string]string{"SrcName": "openssl", "SrcVersion": "3.1.2-r0"}},
		// deb: the source version from upstream=name@version, not the +bN binary rebuild.
		{"py312.cdx.json", "bash", map[string]string{"SrcName": "bash", "SrcVersion": "5.2.37", "SrcRelease": "2"}},
		// deb with an epoch.
		{"py312.cdx.json", "libcap2", map[string]string{"SrcName": "libcap2", "SrcEpoch": "1", "SrcVersion": "2.75", "SrcRelease": "10+deb13u1"}},
	}
	for _, tt := range tests {
		t.Run(tt.pkg, func(t *testing.T) {
			_, after := fixedUp(t, tt.file)
			c, ok := after[tt.pkg]
			if !ok {
				t.Fatalf("%s not in %s", tt.pkg, tt.file)
			}
			if got := srcProps(c); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("%s (%s): got %v, want %v", tt.pkg, c.PURL, got, tt.want)
			}
		})
	}
}

func TestSourcePropsRPM(t *testing.T) {
	got := sourceProps("pkg:rpm/redhat/openssl-libs@3.0.7-27.el9?arch=x86_64&epoch=1&upstream=openssl-3.0.7-27.el9.src.rpm", "1:3.0.7-27.el9")
	want := [][2]string{{"SrcName", "openssl"}, {"SrcVersion", "3.0.7"}, {"SrcRelease", "27.el9"}, {"SrcEpoch", "1"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseTrivy(t *testing.T) {
	tests := []struct {
		file string
		n    int
		want Vuln
	}{
		{"node16.trivy.json", 38, Vuln{ID: "CVE-2023-5363", Severity: "HIGH", Package: "libcrypto3", Version: "3.1.2-r0",
			FixedVersion: "3.1.4-r0", Type: "alpine"}},
		{"py312.trivy.json", 170, Vuln{}},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			b, err := os.ReadFile(fixture(t, tt.file))
			if err != nil {
				t.Fatal(err)
			}
			got, err := parseTrivy(b)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tt.n {
				t.Errorf("got %d vulns, want %d", len(got), tt.n)
			}
			types := map[string]int{}
			for _, v := range got {
				types[v.Type]++
				if v.ID == "" || v.Package == "" || !slices.Contains(Severities, v.Severity) {
					t.Errorf("incomplete row %+v", v)
				}
				if tt.want.ID != "" && v.ID == tt.want.ID && v.Package == tt.want.Package {
					v.Title, v.URL = "", ""
					if v != tt.want {
						t.Errorf("got %+v, want %+v", v, tt.want)
					}
					tt.want.ID = "" // found
				}
			}
			if tt.want.ID != "" {
				t.Errorf("%s in %s not found", tt.want.ID, tt.want.Package)
			}
			t.Logf("by type: %v", types)
		})
	}
}

func TestParseTrivyDedupesPaths(t *testing.T) {
	// One Go module vulnerable in two binaries: trivy reports it per binary.
	b := []byte(`{"Results":[
	  {"Target":"usr/bin/a","Type":"gobinary","Vulnerabilities":[{"VulnerabilityID":"CVE-1","PkgName":"golang.org/x/net","InstalledVersion":"v0.1.0","Severity":"HIGH"}]},
	  {"Target":"usr/bin/b","Type":"gobinary","Vulnerabilities":[{"VulnerabilityID":"CVE-1","PkgName":"golang.org/x/net","InstalledVersion":"v0.1.0","Severity":"HIGH"},
	    {"VulnerabilityID":"CVE-2","PkgName":"golang.org/x/net","InstalledVersion":"v0.1.0","Severity":"weird"}]},
	  {"Target":"Python","Type":"python-pkg"}]}`)
	got, err := parseTrivy(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d vulns, want 2: %+v", len(got), got)
	}
	if got[1].Severity != "UNKNOWN" {
		t.Errorf("unknown severity mapped to %q, want UNKNOWN", got[1].Severity)
	}
}

// writeSBOMs is a fake syft body that writes each requested format: syft-json as "{}", CycloneDX
// as the node16 fixture.
func writeSBOMs(t *testing.T) string {
	cdx := fixture(t, "node16.cdx.json")
	return `for a; do case $a in syft-json=*) echo '{}' > "${a#syft-json=}";; ` +
		`cyclonedx-json=*) cp ` + cdx + ` "${a#cyclonedx-json=}";; esac; done`
}

func trivyFixture(t *testing.T) string { return "cat " + fixture(t, "node16.trivy.json") }

func newTrivyScan(t *testing.T, logs, trivyBody string) *Runner {
	x := newXeol(t, fake(t, "xeol", logs, catFixture(t)))
	x.Syft = fake(t, "syft", logs, writeSBOMs(t))
	// trivy must get the fixed-up SBOM (trivy's SrcName property), else exit 3.
	x.TrivyBin = fake(t, "trivy", logs, `grep -q aquasecurity:trivy:SrcName "$2" || exit 3; `+trivyBody)
	x.TrivyCacheDir = "/trivy/current"
	return x
}

func TestScanWithTrivy(t *testing.T) {
	logs := t.TempDir()
	x := newTrivyScan(t, logs, trivyFixture(t))
	got, err := x.Scan(context.Background(), Target{Ref: ref})
	if err != nil || got.VulnErr != nil {
		t.Fatalf("err %v, vulnErr %v", err, got.VulnErr)
	}
	if len(got.Findings) != 3 || len(got.Vulns) != 38 {
		t.Fatalf("got %d findings, %d vulns; want 3, 38", len(got.Findings), len(got.Vulns))
	}
	syftArgs := read(t, logs+"/syft-args")
	for _, want := range []string{"-o syft-json=", "-o cyclonedx-json="} {
		if !strings.Contains(syftArgs, want) {
			t.Errorf("syft args %q missing %q", syftArgs, want)
		}
	}
	args := read(t, logs+"/trivy-args")
	for _, want := range []string{"sbom " + x.WorkDir, "--scanners vuln", "-f json", "--cache-dir /trivy/current",
		"--skip-db-update", "--skip-java-db-update", "--offline-scan", "--skip-version-check", "--disable-telemetry"} {
		if !strings.Contains(args, want) {
			t.Errorf("trivy args %q missing %q", args, want)
		}
	}
}

func TestScanTrivyFailureKeepsFindings(t *testing.T) {
	x := newTrivyScan(t, t.TempDir(), `echo "db corrupt" >&2; exit 1`)
	got, err := x.Scan(context.Background(), Target{Ref: ref})
	if err != nil {
		t.Fatalf("a trivy failure must not fail the scan: %v", err)
	}
	if se, ok := errors.AsType[*ScanError](got.VulnErr); !ok || se.Reason != ReasonTrivy || !strings.Contains(se.Msg, "db corrupt") {
		t.Errorf("vulnErr = %v, want reason %q", got.VulnErr, ReasonTrivy)
	}
	if len(got.Findings) != 3 || got.Vulns != nil {
		t.Errorf("got %d findings, vulns %v; want 3 findings and nil vulns (keep previous)", len(got.Findings), got.Vulns)
	}
}

func TestScanTrivyOnly(t *testing.T) {
	logs := t.TempDir()
	x := newTrivyScan(t, logs, trivyFixture(t))
	x.EOL = false
	got, err := x.Scan(context.Background(), Target{Ref: ref})
	if err != nil || got.Findings != nil || len(got.Vulns) != 38 {
		t.Fatalf("got %d findings (nil=%v), %d vulns, err %v", len(got.Findings), got.Findings == nil, len(got.Vulns), err)
	}
	if args := read(t, logs+"/syft-args"); strings.Contains(args, "syft-json") || !strings.Contains(args, "cyclonedx-json") {
		t.Errorf("syft args %q: want CycloneDX only", args)
	}
	if read(t, logs+"/xeol-args") != "" {
		t.Error("xeol ran with EOL off")
	}
}

func TestSweepRemovesLeftovers(t *testing.T) {
	x := &Runner{WorkDir: t.TempDir()}
	stale := filepath.Join(x.WorkDir, "scan-123", "layers")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(x.WorkDir, "other")
	if err := os.WriteFile(keep, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := x.Sweep(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(x.WorkDir, "scan-123")); !os.IsNotExist(err) {
		t.Errorf("scan-123 still there: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("sweep removed a non-scan file: %v", err)
	}
}

func TestCountVulns(t *testing.T) {
	tally := CountVulns([]Vuln{
		{Severity: "CRITICAL", FixedVersion: "1.1"}, {Severity: "CRITICAL"}, {Severity: "HIGH", FixedVersion: "2"},
		{Severity: "LOW", FixedVersion: "3"}, {Severity: "LOW"},
	})
	if tally.Severity("CRITICAL") != 2 || tally.Severity("HIGH") != 1 || tally.Severity("MEDIUM") != 0 {
		t.Errorf("severity counts %v", tally)
	}
	if tally.Fixable() != 3 || tally.Fixable("CRITICAL", "HIGH") != 2 {
		t.Errorf("fixable counts %v", tally)
	}
	if len(tally) != 5 { // one entry per (severity, fixable) seen: the metric's series
		t.Errorf("buckets %v", tally)
	}
}
