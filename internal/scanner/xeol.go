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

// Package scanner runs syft and xeol against a registry image and returns flat findings.
// Nothing xeol-specific is exported, so the EOL source can be swapped.
package scanner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Finding is one end-of-life product found in an image.
type Finding struct {
	Product string
	Cycle   string
	// EOL is the end-of-life date. Products marked EOL without a date get the Unix epoch,
	// so they always count as already EOL.
	EOL     time.Time
	Package string
	Version string
	Type    string
	PURL    string
	Distro  string
	// URL is the product's EOL page, e.g. https://endoflife.date/python.
	URL string
}

// Target is one image to scan.
type Target struct {
	// Ref is "<repository>@sha256:<digest>".
	Ref string
	// Platform is "<os>/<arch>" of a node running the image; empty lets syft pick.
	Platform string
	// DockerConfigJSON is a merged ~/.docker/config.json with registry credentials; may be nil.
	DockerConfigJSON []byte
}

// Error reasons, used as the xeol_scan_errors_total label.
const (
	ReasonAuth     = "auth"
	ReasonNotFound = "not_found"
	ReasonTimeout  = "timeout"
	// ReasonOOM: scanning this image killed the pod last time; it is backed off instead of crash-looping.
	ReasonOOM   = "oom"
	ReasonOther = "other"
)

// ScanError is a failed scan with a classified reason.
type ScanError struct {
	Reason string
	Msg    string
}

func (e *ScanError) Error() string { return fmt.Sprintf("scan failed (%s): %s", e.Reason, e.Msg) }

// Xeol scans an image in two steps: syft catalogs it into an SBOM, then xeol matches the SBOM
// against the EOL DB. Current syft needs well under half the memory of the syft built into
// xeol v0.10.8 (grafana: 0.7 vs 1.6 GB, same findings), and the xeol step is ~70 MB.
type Xeol struct {
	// Bin is the xeol binary path.
	Bin string
	// Syft is the syft binary path.
	Syft string
	// DBDir is the xeol DB cache dir, holding 1/xeol.db.
	DBDir string
	// StateDir holds in-flight markers. It must survive container restarts (an emptyDir), so a scan
	// that OOM-killed the pod is detected and backed off instead of crash-looping.
	StateDir string
	// WorkDir is the parent for per-scan temp dirs (pulled layers, credentials); empty means os.TempDir().
	WorkDir string
	// Timeout bounds one scan.
	Timeout time.Duration
	// MemoryLimit and MaxProcs set GOMEMLIMIT and GOMAXPROCS for syft and xeol, so they stay inside
	// the pod's limits; "" / 0 leaves them unset.
	MemoryLimit string
	MaxProcs    int
	// OOMBackoff is how long an image whose scan killed the pod is skipped.
	OOMBackoff time.Duration
	// Solo is true when scans never overlap (one worker), so a leftover marker always names the culprit.
	Solo bool

	// excl serializes the rescan of an image whose marker was left by a crash during concurrent
	// scans: alone, a second crash proves it was the one.
	excl sync.RWMutex
}

// soloMark is the marker content of a scan that ran with nothing else in flight.
const soloMark = "solo"

// lookahead is wide on purpose: EOL thresholds are decided in PromQL, not here.
const lookahead = "10y"

// catalogers is the selection xeol v0.10.8 applies to its built-in syft (getProviderConfig in
// cmd/xeol/cli/commands/root.go), so the SBOM covers what `xeol registry:` would. Re-check on a bump.
const catalogers = "-dotnet-portable-executable-cataloger,+alpm-db-cataloger,+apk-db-cataloger," +
	"+cargo-auditable-binary-cataloger,+cocoapods-cataloger,+conan-cataloger,+dart-pubspec-lock-cataloger," +
	"+dotnet-deps-cataloger,+dpkg-db-cataloger,+javascript-package-cataloger,+javascript-lock-cataloger," +
	"+elixir-mix-lock-cataloger,+erlang-rebar-lock-cataloger,+go-module-file-cataloger," +
	"+go-module-binary-cataloger,+graalvm-native-image-cataloger,+haskell-cataloger,+java-archive-cataloger," +
	"+java-gradle-lockfile-cataloger,+java-pom-cataloger,+linux-kernel-cataloger,+nix-store-cataloger," +
	"+php-composer-installed-cataloger,+php-composer-lock-cataloger,+portage-cataloger," +
	"+python-package-cataloger,+python-installed-package-cataloger,+rpm-db-cataloger,+rpm-archive-cataloger," +
	"+ruby-gemfile-cataloger,+ruby-installed-gemspec-cataloger,+rust-cargo-lock-cataloger,+sbom-cataloger"

// Scan scans one image. Credentials, pulled layers and xeol's config search path all live in a
// per-scan temp dir that is removed before Scan returns.
func (x *Xeol) Scan(ctx context.Context, t Target) ([]Finding, error) {
	_, digest, ok := strings.Cut(t.Ref, "@")
	if !ok {
		return nil, fmt.Errorf("image ref %q has no digest", t.Ref)
	}
	// A marker that outlives the process means the pod died (OOM kill) while this image was being
	// scanned. If nothing else was scanning, it was the culprit: back off. Otherwise rescan it alone.
	marker := filepath.Join(x.StateDir, "inflight", strings.ReplaceAll(digest, ":", "_"))
	mark, err := os.ReadFile(marker)
	suspect := err == nil
	if st, err := os.Stat(marker); suspect && string(mark) == soloMark && err == nil && time.Since(st.ModTime()) < x.OOMBackoff {
		return nil, &ScanError{Reason: ReasonOOM,
			Msg: "scanning this image ran out of memory; retrying after " + st.ModTime().Add(x.OOMBackoff).Format(time.RFC3339)}
	}
	if suspect && !x.Solo {
		x.excl.Lock()
		defer x.excl.Unlock()
	} else {
		x.excl.RLock()
		defer x.excl.RUnlock()
	}
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		return nil, err
	}
	content := ""
	if x.Solo || suspect {
		content = soloMark
	}
	if err := os.WriteFile(marker, []byte(content), 0o644); err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(marker) }() // only a crash leaves it behind

	work, err := os.MkdirTemp(x.WorkDir, "scan-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(work) }()
	if t.DockerConfigJSON != nil {
		if err := os.WriteFile(filepath.Join(work, "config.json"), t.DockerConfigJSON, 0o600); err != nil {
			return nil, err
		}
	}

	ctx, cancel := context.WithTimeout(ctx, x.Timeout)
	defer cancel()

	sbom := filepath.Join(work, "sbom.json")
	args := []string{"scan", "registry:" + t.Ref, "-q", "--select-catalogers", catalogers, "-o", "syft-json=" + sbom}
	if t.Platform != "" {
		args = append(args, "--platform", t.Platform)
	}
	if err := x.run(ctx, io.Discard, work, x.Syft, args, "SYFT_CHECK_FOR_APP_UPDATE=false"); err != nil {
		return nil, err
	}

	// Never pass --api-key: it uploads results to xeol.io.
	var out bytes.Buffer
	err = x.run(ctx, &out, work, x.Bin, []string{"sbom:" + sbom, "-o", "json", "-q", "-l", lookahead},
		"XEOL_DB_CACHE_DIR="+x.DBDir,
		"XEOL_DB_AUTO_UPDATE=false",
		"XEOL_DB_VALIDATE_AGE=false",
		"XEOL_CHECK_FOR_APP_UPDATE=false")
	if err != nil {
		return nil, err
	}
	return parse(out.Bytes())
}

// run executes one step of a scan with its stdout going to stdout.
func (x *Xeol) run(ctx context.Context, stdout io.Writer, work, bin string, args []string, env ...string) error {
	cmd := exec.CommandContext(ctx, bin, args...)
	// Don't hang on pipes held open by child processes after the context kills the step.
	cmd.WaitDelay = time.Second
	// syft and xeol read config from cwd and HOME, and stereoscope unpacks layers into TMPDIR:
	// point all of them at the scan's own dir.
	cmd.Dir = work
	cmd.Env = append([]string{
		"HOME=" + work,
		"TMPDIR=" + work,
		"XDG_CONFIG_HOME=" + work,
		"XDG_CACHE_HOME=" + work,
		"DOCKER_CONFIG=" + work,
	}, env...)
	if x.MemoryLimit != "" {
		cmd.Env = append(cmd.Env, "GOMEMLIMIT="+x.MemoryLimit)
	}
	if x.MaxProcs > 0 {
		cmd.Env = append(cmd.Env, "GOMAXPROCS="+strconv.Itoa(x.MaxProcs))
	}
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = stdout, &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return &ScanError{Reason: ReasonTimeout, Msg: "exceeded " + x.Timeout.String()}
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return &ScanError{Reason: classify(msg), Msg: filepath.Base(bin) + ": " + msg}
	}
	return nil
}

func classify(stderr string) string {
	s := strings.ToLower(stderr)
	switch {
	case strings.Contains(s, "unauthorized"), strings.Contains(s, "authentication required"),
		strings.Contains(s, "denied"), strings.Contains(s, "forbidden"):
		return ReasonAuth
	case strings.Contains(s, "manifest_unknown"), strings.Contains(s, "name_unknown"), strings.Contains(s, "not found"):
		return ReasonNotFound
	default:
		return ReasonOther
	}
}

type xeolOutput struct {
	Matches []struct {
		Cycle struct {
			ProductName      string
			ProductPermalink string
			ReleaseCycle     string
			Eol              string
		}
		// The top-level "Package" block is always empty in xeol v0.10.8; artifact has the data.
		Artifact struct {
			Name    string `json:"name"`
			Version string `json:"version"`
			Type    string `json:"type"`
			PURL    string `json:"purl"`
		} `json:"artifact"`
	} `json:"matches"`
	Distro struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"distro"`
}

func parse(b []byte) ([]Finding, error) {
	var out xeolOutput
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("parsing xeol output: %w", err)
	}
	distro := strings.TrimSpace(out.Distro.Name + " " + out.Distro.Version)
	findings := make([]Finding, 0, len(out.Matches))
	seen := map[[4]string]bool{}
	for _, m := range out.Matches {
		k := [4]string{m.Cycle.ProductName, m.Cycle.ReleaseCycle, m.Artifact.Name, m.Artifact.Version}
		if seen[k] {
			continue // the same package found at several paths
		}
		seen[k] = true
		var eol time.Time
		switch m.Cycle.Eol {
		case "false", "":
			// No date and not EOL (e.g. Yarn 1): nothing to graph or alert on.
			continue
		case "true":
			eol = time.Unix(0, 0).UTC()
		default:
			d, err := time.Parse(time.DateOnly, m.Cycle.Eol)
			if err != nil {
				return nil, fmt.Errorf("parsing EOL %q for %s: %w", m.Cycle.Eol, m.Cycle.ProductName, err)
			}
			eol = d
		}
		findings = append(findings, Finding{
			Product: m.Cycle.ProductName,
			Cycle:   m.Cycle.ReleaseCycle,
			EOL:     eol,
			Package: m.Artifact.Name,
			Version: m.Artifact.Version,
			Type:    m.Artifact.Type,
			PURL:    m.Artifact.PURL,
			Distro:  distro,
			URL:     m.Cycle.ProductPermalink,
		})
	}
	return findings, nil
}
