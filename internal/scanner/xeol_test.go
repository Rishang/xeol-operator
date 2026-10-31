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
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	date := func(s string) time.Time { d, _ := time.Parse(time.DateOnly, s); return d }
	tests := []struct {
		file string
		want []Finding
	}{
		{"py38.json", []Finding{
			{Product: "Debian", Cycle: "12", Package: "Debian GNU/Linux", Type: "os"},
			{Product: "Perl", Cycle: "5.36", Package: "perl-base", Type: "deb"},
			{Product: "Python", Cycle: "3.8", EOL: date("2024-10-07"), Package: "python", Version: "3.8.20", Type: "binary"},
		}},
		{"node16.json", []Finding{
			{Product: "Alpine Linux", Cycle: "3.18", EOL: date("2025-05-09"), Package: "Alpine Linux", Version: "3.18.3", Type: "os"},
			{Product: "Node.js", Cycle: "16", EOL: date("2023-09-11"), Package: "node", Version: "16.20.2", Type: "binary"},
			// Yarn 1 has Eol "false" and no date: dropped.
		}},
		{"py312.json", []Finding{
			{Product: "Debian", Cycle: "13"},
			{Product: "Perl", Cycle: "5.40"},
			{Product: "Python", Cycle: "3.12", EOL: date("2028-10-31"), Package: "python", Type: "binary"},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			b, err := os.ReadFile(fixture(t, tt.file))
			if err != nil {
				t.Fatal(err)
			}
			got, err := parse(b)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d findings, want %d: %+v", len(got), len(tt.want), got)
			}
			for i, w := range tt.want {
				g := got[i]
				// Only compare the fields the case sets.
				if g.Product != w.Product || g.Cycle != w.Cycle ||
					(!w.EOL.IsZero() && !g.EOL.Equal(w.EOL)) ||
					(w.Package != "" && g.Package != w.Package) ||
					(w.Version != "" && g.Version != w.Version) ||
					(w.Type != "" && g.Type != w.Type) {
					t.Errorf("finding %d = %+v, want %+v", i, g, w)
				}
				if g.EOL.IsZero() {
					t.Errorf("finding %d has no EOL", i)
				}
				if g.Distro == "" {
					t.Errorf("finding %d has no distro", i)
				}
				if !strings.HasPrefix(g.URL, "https://endoflife.date/") {
					t.Errorf("finding %d URL = %q", i, g.URL)
				}
			}
		})
	}
}

func TestParseEOLTrue(t *testing.T) {
	got, err := parse([]byte(`{"matches":[{"Cycle":{"ProductName":"X","ReleaseCycle":"1","Eol":"true"},"artifact":{"name":"x"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].EOL.Unix() != 0 {
		t.Fatalf("want one finding at epoch, got %+v", got)
	}
}

func TestParseDedupesPaths(t *testing.T) {
	m := `{"Cycle":{"ProductName":"Python","ReleaseCycle":"3.8","Eol":"2024-10-07"},"artifact":{"name":"python","version":"3.8.20"}}`
	got, err := parse([]byte(`{"matches":[` + m + `,` + m + `]}`))
	if err != nil || len(got) != 1 {
		t.Fatalf("want one finding for the same package at two paths, got %+v, %v", got, err)
	}
}

// fake writes a stand-in for the named binary that logs its args to dir/<name>-args and its env to
// dir/<name>-env, then runs body.
func fake(t *testing.T, name, dir, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	script := "#!/bin/sh\necho \"$@\" >> " + dir + "/" + name + "-args\nenv >> " + dir + "/" + name + "-env\n" + body + "\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// writeSBOM is a fake syft body that writes content to the -o syft-json=<file> path.
func writeSBOM(content string) string {
	return `for a; do case $a in syft-json=*) out=${a#syft-json=};; esac; done; echo '` + content + `' > "$out"`
}

func newXeol(t *testing.T, bin string) *Runner {
	return &Runner{XeolBin: bin, Syft: fake(t, "syft", t.TempDir(), writeSBOM("{}")), DBDir: "/db", StateDir: t.TempDir(), WorkDir: t.TempDir(),
		Timeout: time.Minute, MemoryLimit: "1GiB", MaxProcs: 2, OOMBackoff: 24 * time.Hour, EOL: true}
}

// read returns the file's content, or "" if the fake binary never wrote it.
func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}

const ref = "docker.io/library/python@sha256:abc"

// fixture returns the absolute path of testdata/<name>, skipping the test when it is missing:
// fixtures are real syft, xeol and trivy output kept locally only (testdata/ is gitignored).
func fixture(t *testing.T, name string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("testdata", name))
	if err == nil {
		_, err = os.Stat(p)
	}
	if err != nil {
		t.Skipf("fixture testdata/%s not available: %v", name, err)
	}
	return p
}

func catFixture(t *testing.T) string { return "cat " + fixture(t, "py38.json") }

func TestScanArgsAndEnv(t *testing.T) {
	logs := t.TempDir()
	// xeol only gets the SBOM that syft wrote.
	x := newXeol(t, fake(t, "xeol", logs, `grep -q sbom-from-syft "${1#sbom:}" || exit 3; `+catFixture(t)))
	x.Syft = fake(t, "syft", logs, writeSBOM("sbom-from-syft"))
	got, err := x.Scan(context.Background(), Target{Ref: ref, Platform: "linux/arm64"})
	if err != nil || len(got.Findings) != 3 || got.Vulns != nil {
		t.Fatalf("got %+v, err %v", got, err)
	}
	syftArgs := read(t, logs+"/syft-args")
	for _, want := range []string{"scan registry:" + ref, "-o syft-json=", "--select-catalogers -dotnet-portable-executable-cataloger,",
		"--platform linux/arm64"} {
		if !strings.Contains(syftArgs, want) {
			t.Errorf("syft args %q missing %q", syftArgs, want)
		}
	}
	args := read(t, logs+"/xeol-args")
	for _, want := range []string{"sbom:" + x.WorkDir, "-o json", "-q", "-l 10y"} {
		if !strings.Contains(args, want) {
			t.Errorf("args %q missing %q", args, want)
		}
	}
	if strings.Contains(args, "api-key") {
		t.Errorf("must never pass --api-key: %q", args)
	}
	env := read(t, logs+"/xeol-env")
	for _, want := range []string{"HOME=" + x.WorkDir, "XEOL_DB_CACHE_DIR=/db", "XEOL_DB_AUTO_UPDATE=false",
		"XEOL_DB_VALIDATE_AGE=false", "XEOL_CHECK_FOR_APP_UPDATE=false", "GOMEMLIMIT=1GiB", "GOMAXPROCS=2"} {
		if !strings.Contains(env, want) {
			t.Errorf("env missing %q", want)
		}
	}
}

func TestScanErrors(t *testing.T) {
	tests := []struct {
		name, body, reason string
		timeout            time.Duration
	}{
		{"auth", `echo "GET https://r/v2/x: UNAUTHORIZED: authentication required" >&2; exit 1`, ReasonAuth, time.Minute},
		{"not found", `echo "MANIFEST_UNKNOWN: manifest unknown" >&2; exit 1`, ReasonNotFound, time.Minute},
		{"other", `echo "boom" >&2; exit 1`, ReasonOther, time.Minute},
		{"timeout", `sleep 5`, ReasonTimeout, 100 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			x := newXeol(t, fake(t, "xeol", t.TempDir(), tt.body))
			x.Timeout = tt.timeout
			_, err := x.Scan(context.Background(), Target{Ref: ref})
			var se *ScanError
			if !errors.As(err, &se) || se.Reason != tt.reason {
				t.Fatalf("err = %v, want reason %q", err, tt.reason)
			}
		})
	}
	// A failed pull in syft stops the scan before xeol runs.
	logs := t.TempDir()
	x := newXeol(t, fake(t, "xeol", logs, catFixture(t)))
	x.Syft = fake(t, "syft", logs, `echo "GET https://r/v2/x: UNAUTHORIZED" >&2; exit 1`)
	_, err := x.Scan(context.Background(), Target{Ref: ref})
	var se *ScanError
	if !errors.As(err, &se) || se.Reason != ReasonAuth || !strings.HasPrefix(se.Msg, "syft: ") {
		t.Fatalf("err = %v, want syft auth error", err)
	}
	if read(t, logs+"/xeol-args") != "" {
		t.Fatal("xeol ran after syft failed")
	}
}

func TestScanOOMBackoff(t *testing.T) {
	logs := t.TempDir()
	x := newXeol(t, fake(t, "xeol", logs, catFixture(t)))
	marker := filepath.Join(x.StateDir, "inflight", "sha256_abc")
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	// A marker left by a process killed while this scan ran alone: skip the image.
	if err := os.WriteFile(marker, []byte(soloMark), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := x.Scan(context.Background(), Target{Ref: ref})
	var se *ScanError
	if !errors.As(err, &se) || se.Reason != ReasonOOM {
		t.Fatalf("err = %v, want oom", err)
	}
	if read(t, logs+"/xeol-args") != "" {
		t.Fatal("scanned an image inside its OOM backoff")
	}
	// After the backoff, try again; a finished scan clears the marker.
	old := time.Now().Add(-25 * time.Hour)
	_ = os.Chtimes(marker, old, old)
	if _, err := x.Scan(context.Background(), Target{Ref: ref}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("marker left after a finished scan")
	}
}

func TestScanOOMSuspectRescannedAlone(t *testing.T) {
	logs := t.TempDir()
	x := newXeol(t, "")
	marker := filepath.Join(x.StateDir, "inflight", "sha256_abc")
	// The rescan must run marked solo, so a second crash identifies this image.
	x.XeolBin = fake(t, "xeol", logs, `grep -q `+soloMark+` `+marker+` || exit 4; `+catFixture(t))
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	// Left by a crash while other images were scanning too: not proof, so scan it again, alone.
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := x.Scan(context.Background(), Target{Ref: ref}); err != nil {
		t.Fatalf("suspect not rescanned: %v", err)
	}
}

func TestScanCredsCleanup(t *testing.T) {
	for _, body := range []string{
		`test -f "$DOCKER_CONFIG/config.json" || exit 2; echo '{"matches":[]}'`,
		`test -f "$DOCKER_CONFIG/config.json" || exit 2; echo unauthorized >&2; exit 1`,
	} {
		x := newXeol(t, fake(t, "xeol", t.TempDir(), body))
		_, err := x.Scan(context.Background(), Target{Ref: ref, DockerConfigJSON: []byte(`{"auths":{}}`)})
		var se *ScanError
		if err != nil && (!errors.As(err, &se) || se.Reason != ReasonAuth) {
			t.Fatalf("config.json missing during scan: %v", err)
		}
		if left, _ := os.ReadDir(x.WorkDir); len(left) != 0 {
			t.Fatalf("scan dir not cleaned up: %v", left)
		}
	}
}
