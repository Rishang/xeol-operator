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
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// Vuln is one known vulnerability in a package of an image.
type Vuln struct {
	ID           string
	Title        string
	Severity     string // CRITICAL, HIGH, MEDIUM, LOW or UNKNOWN
	Package      string
	Version      string
	FixedVersion string // empty when no fix is released
	Type         string // package type, e.g. alpine, debian, gobinary
	// URL is the primary reference, e.g. https://avd.aquasec.com/nvd/cve-2024-1234.
	URL string
}

// VulnKey is a bucket of vulnerabilities: one severity, with or without a released fix. It is also
// the label set of the xeol_vulnerabilities metric.
type VulnKey struct {
	Severity string
	Fixable  bool
}

// VulnTally counts an image's vulnerabilities per VulnKey. Every count of vulnerabilities (metrics,
// API rows, status) reads one, so "critical", "high" and "fixable" mean the same everywhere.
type VulnTally map[VulnKey]int

// CountVulns tallies vulns.
func CountVulns(vulns []Vuln) VulnTally {
	t := VulnTally{}
	for _, v := range vulns {
		t[VulnKey{v.Severity, v.FixedVersion != ""}]++
	}
	return t
}

// Severity counts the vulnerabilities of one severity, fixed or not.
func (t VulnTally) Severity(sev string) int { return t[VulnKey{sev, false}] + t[VulnKey{sev, true}] }

// Fixable counts the vulnerabilities with a released fix, of the given severities (all when none given).
func (t VulnTally) Fixable(sevs ...string) int {
	n := 0
	for k, c := range t {
		if k.Fixable && (len(sevs) == 0 || slices.Contains(sevs, k.Severity)) {
			n += c
		}
	}
	return n
}

// trivyProp prefixes the CycloneDX component properties trivy reads back from an SBOM
// (trivy pkg/sbom/core: SrcName, SrcVersion, SrcRelease, SrcEpoch). They are trivy's own
// convention, not a public API: re-check them on a trivy bump.
const trivyProp = "aquasecurity:trivy:"

// trivy source-package property names, after trivyProp.
const (
	srcName    = "SrcName"
	srcVersion = "SrcVersion"
	srcRelease = "SrcRelease"
)

// fixUpCycloneDX adds trivy's source-package properties to the OS packages of a syft CycloneDX
// SBOM. trivy matches OS advisories by source package, and syft only records the source in the
// purl's "upstream" qualifier, which trivy ignores: without this, subpackages such as libcrypto3
// (source openssl) get no findings. Everything else in the document passes through unchanged.
func fixUpCycloneDX(b []byte) ([]byte, error) {
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("parsing CycloneDX SBOM: %w", err)
	}
	comps, _ := doc["components"].([]any)
	for _, c := range comps {
		comp, ok := c.(map[string]any)
		if !ok {
			continue
		}
		purl, _ := comp["purl"].(string)
		version, _ := comp["version"].(string)
		props := sourceProps(purl, version)
		if props == nil {
			continue
		}
		existing, _ := comp["properties"].([]any)
		for _, p := range existing {
			if m, ok := p.(map[string]any); ok && m["name"] == trivyProp+srcName {
				props = nil // already set, e.g. by a newer syft: leave it
				break
			}
		}
		for _, kv := range props {
			if kv[1] == "" {
				continue // e.g. a native Debian package has no release
			}
			existing = append(existing, map[string]any{"name": trivyProp + kv[0], "value": kv[1]})
		}
		comp["properties"] = existing
	}
	return json.Marshal(doc)
}

// sourceProps returns trivy's source-package properties for an OS package purl with an
// "upstream" qualifier, following what trivy itself writes for the same package type:
//   - apk: SrcName, SrcVersion = the full package version (e.g. 3.5.4-r0)
//   - deb: SrcName, SrcEpoch, SrcVersion, SrcRelease from the source version (syft writes
//     upstream=name@version when it differs from the binary package's, e.g. +bN rebuilds)
//   - rpm: SrcName, SrcEpoch, SrcVersion, SrcRelease from the source RPM file name
//
// It returns nil for any other package.
func sourceProps(purl, version string) [][2]string {
	typ, rest, ok := strings.Cut(strings.TrimPrefix(purl, "pkg:"), "/")
	if !ok || (typ != "apk" && typ != "deb" && typ != "rpm") {
		return nil
	}
	_, query, _ := strings.Cut(rest, "?")
	query, _, _ = strings.Cut(query, "#")
	q, err := url.ParseQuery(query)
	if err != nil || q.Get("upstream") == "" {
		return nil
	}
	upstream := q.Get("upstream")
	switch typ {
	case "apk":
		return [][2]string{{srcName, upstream}, {srcVersion, version}}
	case "deb":
		name, v, found := strings.Cut(upstream, "@")
		if !found {
			v = version
		}
		epoch, ver, rel := splitEVR(v)
		return withEpoch(epoch, [][2]string{{srcName, name}, {srcVersion, ver}, {srcRelease, rel}})
	default: // rpm: upstream is the source RPM, e.g. openssl-3.0.7-27.el9.src.rpm
		srpm := strings.TrimSuffix(upstream, ".src.rpm")
		i := strings.LastIndex(srpm, "-")
		j := strings.LastIndex(srpm[:max(i, 0)], "-")
		if i < 0 || j < 0 {
			return nil
		}
		return withEpoch(q.Get("epoch"), [][2]string{{srcName, srpm[:j]}, {srcVersion, srpm[j+1 : i]}, {srcRelease, srpm[i+1:]}})
	}
}

// splitEVR splits a Debian version "[epoch:]version[-release]".
func splitEVR(v string) (epoch, version, release string) {
	if e, rest, ok := strings.Cut(v, ":"); ok {
		epoch, v = e, rest
	}
	if i := strings.LastIndex(v, "-"); i >= 0 {
		return epoch, v[:i], v[i+1:]
	}
	return epoch, v, ""
}

func withEpoch(epoch string, props [][2]string) [][2]string {
	if epoch != "" && epoch != "0" {
		props = append(props, [2]string{"SrcEpoch", epoch})
	}
	return props
}

type trivyOutput struct {
	Results []struct {
		Type            string
		Vulnerabilities []struct {
			VulnerabilityID  string
			PkgName          string
			InstalledVersion string
			FixedVersion     string
			Severity         string
			Title            string
			PrimaryURL       string
		}
	}
}

// Severities are trivy's levels, worst first; anything else is reported as UNKNOWN.
var Severities = []string{"CRITICAL", "HIGH", "MEDIUM", "LOW", "UNKNOWN"}

// parseTrivy flattens `trivy sbom -f json` output into one Vuln per (ID, package, version):
// the same package found at several paths (e.g. one Go module in two binaries) is reported once.
func parseTrivy(b []byte) ([]Vuln, error) {
	var out trivyOutput
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("parsing trivy output: %w", err)
	}
	var vulns []Vuln
	seen := map[[3]string]bool{}
	for _, r := range out.Results {
		for _, v := range r.Vulnerabilities {
			k := [3]string{v.VulnerabilityID, v.PkgName, v.InstalledVersion}
			if seen[k] {
				continue
			}
			seen[k] = true
			sev := strings.ToUpper(v.Severity)
			if !slices.Contains(Severities, sev) {
				sev = "UNKNOWN"
			}
			vulns = append(vulns, Vuln{
				ID:           v.VulnerabilityID,
				Title:        v.Title,
				Severity:     sev,
				Package:      v.PkgName,
				Version:      v.InstalledVersion,
				FixedVersion: v.FixedVersion,
				Type:         r.Type,
				URL:          v.PrimaryURL,
			})
		}
	}
	return vulns, nil
}
