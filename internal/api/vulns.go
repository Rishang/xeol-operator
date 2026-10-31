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
	"cmp"
	"slices"
	"time"

	"github.com/Rishang/xeol-operator/internal/controller"
	"github.com/Rishang/xeol-operator/internal/scanner"
)

// The severities that get their own counts and tiles.
const (
	sevCritical = "CRITICAL"
	sevHigh     = "HIGH"
)

// severityRank orders severities worst first; unknown values sort last.
func severityRank(s string) int {
	if i := slices.Index(scanner.Severities, s); i >= 0 {
		return i
	}
	return len(scanner.Severities)
}

// VulnRow is one vulnerability in one workload's image.
type VulnRow struct {
	Namespace    string `json:"namespace"`
	Kind         string `json:"kind"`
	Name         string `json:"name"`
	ID           string `json:"id"`
	Title        string `json:"title"`
	Severity     string `json:"severity"`
	Package      string `json:"package"`
	Version      string `json:"version"`
	FixedVersion string `json:"fixed_version"`
	Image        string `json:"image"`
	Type         string `json:"type"`
	URL          string `json:"url"`
	Digest       string `json:"digest"`
	ScannedAt    string `json:"scanned_at"`
}

func (h *Handler) vulnRows(q query, imgs []controller.Image) []VulnRow {
	var rows []VulnRow
	cfg := h.config()
	for _, img := range imgs {
		for _, w := range img.Workloads {
			if !q.match(w) {
				continue
			}
			for _, v := range cfg.VulnsFor(img.Vulns, w) {
				if len(q.severities) > 0 && !q.severities[v.Severity] {
					continue
				}
				rows = append(rows, VulnRow{
					Namespace: w.Namespace, Kind: w.Kind, Name: w.Name,
					ID: v.ID, Title: v.Title, Severity: v.Severity, Package: v.Package, Version: v.Version,
					FixedVersion: v.FixedVersion, Image: img.Ref, Type: v.Type, URL: v.URL, Digest: img.Digest,
					ScannedAt: img.Scanned.UTC().Format(time.RFC3339),
				})
			}
		}
	}
	slices.SortFunc(rows, func(a, b VulnRow) int {
		return cmp.Or(cmp.Compare(severityRank(a.Severity), severityRank(b.Severity)), cmp.Compare(a.ID, b.ID),
			cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name), cmp.Compare(a.Package, b.Package))
	})
	return rows
}

// vulnerabilities lists one row per (workload, CVE, package), worst severity first.
func (h *Handler) vulnerabilities(q query) any {
	return nonNil(h.vulnRows(q, h.Inventory.Images()))
}

// vulnStatus counts over the images that run in a matching workload, and over matching workloads.
func (h *Handler) vulnStatus(q query, imgs []controller.Image, now time.Time) *VulnStatus {
	vs := &VulnStatus{BySeverity: map[string]int{}}
	for _, sev := range scanner.Severities {
		vs.BySeverity[sev] = 0
	}
	crit, high := map[string]bool{}, map[string]bool{} // workloads running an image with a CRITICAL / a HIGH
	cfg := h.config()
	for _, img := range imgs {
		if !slices.ContainsFunc(img.Workloads, q.match) {
			continue
		}
		t := scanner.CountVulns(imageVulns(cfg, q, img))
		for k, n := range t {
			vs.BySeverity[k.Severity] += n
		}
		vs.FixableCriticalHigh += t.Fixable(sevCritical, sevHigh)
		if t.Severity(sevCritical)+t.Severity(sevHigh) > 0 { // "vulnerable": CRITICAL or HIGH, as in workloads_vulnerable
			vs.ImagesVulnerable++
		}
		for _, w := range img.Workloads {
			if !q.match(w) {
				continue
			}
			wt := scanner.CountVulns(cfg.VulnsFor(img.Vulns, w)) // a workload's own ignore rules
			k := workloadKey(w.Namespace, w.Kind, w.Name)
			crit[k], high[k] = crit[k] || wt.Severity(sevCritical) > 0, high[k] || wt.Severity(sevHigh) > 0
		}
	}
	for k := range crit {
		if crit[k] {
			vs.WorkloadsCritical++
		}
		if high[k] {
			vs.WorkloadsHigh++
		}
	}
	if h.VulnDBUpdated != nil {
		if updated, err := h.VulnDBUpdated(); err == nil {
			vs.DBUpdated, vs.DBAgeSeconds = updated.UTC().Format(time.RFC3339), ageSeconds(now, updated)
		}
	}
	return vs
}
