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

// Package api serves the operator's current EOL findings and vulnerabilities as JSON, for Grafana (Infinity datasource)
// or any HTTP client. Everything is computed from the in-memory inventory on each request: it is
// always the current state, with no history.
package api

import (
	"cmp"
	"encoding/json"
	"errors"
	"maps"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Rishang/xeol-operator/internal/controller"
	"github.com/Rishang/xeol-operator/internal/scanner"
)

// DefaultWithinDays is the "EOL soon" window when within_days isn't given.
const DefaultWithinDays = 180

// Handler serves /api/v1/{status,namespaces,workloads,findings,vulnerabilities}.
type Handler struct {
	Inventory *controller.Inventory
	// Config supplies the config file's ignoreProducts; nil ignores nothing.
	Config  *controller.Config
	DBBuilt func() (time.Time, error)
	// EOL is --eol: /findings and the EOL fields are served.
	EOL bool
	// Trivy is --trivy: /vulnerabilities and the vulnerability fields are served.
	Trivy         bool
	VulnDBUpdated func() (time.Time, error)
	// Leader reports whether this replica holds the inventory; others answer 503 so a client behind
	// the Service never gets an empty answer from a standby.
	Leader func() bool
	Now    func() time.Time
	// UI serves the HTML page at /; MCP serves the same data as MCP tools at /mcp.
	UI, MCP bool
	// Rescan queues the running images matching globs (all with none) and returns their refs. It backs
	// the admin listener's POST /rescan and, with MCPRescan (--mcp-allow-rescan), the MCP rescan tool.
	Rescan    func(globs []string) []string
	MCPRescan bool
}

// Routes returns the handlers to mount, keyed by path.
func (h *Handler) Routes() map[string]http.Handler {
	r := map[string]http.Handler{}
	for name, fn := range h.endpoints() {
		r["/api/v1/"+name] = h.wrap(fn)
	}
	if h.UI {
		maps.Copy(r, h.uiRoutes())
	}
	if h.MCP {
		r["/mcp"] = h.leaderOnly(h.mcp())
	}
	return r
}

func (h *Handler) endpoints() map[string]func(query) any {
	e := map[string]func(query) any{"status": h.status, "namespaces": h.namespaces, "workloads": h.workloads}
	if h.EOL {
		e["findings"] = h.findings
	}
	if h.Trivy {
		e["vulnerabilities"] = h.vulnerabilities
	}
	return e
}

// leaderOnly answers 503 on a standby: only the leader holds the inventory.
func (h *Handler) leaderOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.Leader != nil && !h.Leader() {
			http.Error(w, "not the leader replica; retry", http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) wrap(fn func(q query) any) http.Handler {
	return h.leaderOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		q, err := parseQuery(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fn(q))
	}))
}

type query struct {
	namespaces, kinds map[string]bool // empty = all
	withinDays        int
	severities        map[string]bool // vulnerabilities only; empty = all
}

var errWithinDays = errors.New("within_days must be a non-negative integer")

// newQuery builds a filter from comma-separated namespaces and kinds. Grafana's "All" and empty
// values mean no filter.
func newQuery(namespace, kind string, withinDays int) (query, error) {
	if withinDays < 0 {
		return query{}, errWithinDays
	}
	return query{namespaces: set(namespace), kinds: set(kind), withinDays: withinDays}, nil
}

// parseQuery reads ?namespace=a,b&kind=Deployment,DaemonSet&within_days=180.
func parseQuery(r *http.Request) (query, error) {
	v, days := r.URL.Query(), DefaultWithinDays
	if s := v.Get("within_days"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			return query{}, errWithinDays
		}
		days = n
	}
	q, err := newQuery(v.Get("namespace"), v.Get("kind"), days)
	q.severities = map[string]bool{}
	for sev := range set(v.Get("severity")) { // after set(): "All" means no filter
		q.severities[strings.ToUpper(sev)] = true
	}
	return q, err
}

func set(csv string) map[string]bool {
	m := map[string]bool{}
	for v := range strings.SplitSeq(csv, ",") {
		if v = strings.TrimSpace(v); v != "" && v != "All" && v != "$__all" && v != ".*" {
			m[v] = true
		}
	}
	return m
}

func (q query) match(w controller.Workload) bool {
	return (len(q.namespaces) == 0 || q.namespaces[w.Namespace]) && (len(q.kinds) == 0 || q.kinds[w.Kind])
}

// FindingRow is one EOL product in one workload's image.
type FindingRow struct {
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Product   string `json:"product"`
	Cycle     string `json:"cycle"`
	// EOL is the date, or "unknown" when the product is marked EOL without one (DaysLeft is then null).
	EOL       string   `json:"eol"`
	DaysLeft  *float64 `json:"days_left"`
	Image     string   `json:"image"`
	Package   string   `json:"package"`
	Version   string   `json:"version"`
	URL       string   `json:"url"`
	Digest    string   `json:"digest"`
	ScannedAt string   `json:"scanned_at"`
}

// config is the current config snapshot: ignore rules. Without one nothing is ignored.
func (h *Handler) config() controller.ConfigSnapshot {
	var cfg controller.ConfigSnapshot
	if h.Config != nil {
		cfg, _ = h.Config.Get()
	}
	return cfg
}

// imageVulns is img's vulnerabilities for image-level counts over the workloads q matches: without
// those that ignore rules hide for all of them.
func imageVulns(cfg controller.ConfigSnapshot, q query, img controller.Image) []scanner.Vuln {
	return cfg.ImageVulns(img.Vulns, slices.DeleteFunc(slices.Clone(img.Workloads), func(w controller.Workload) bool {
		return !q.match(w)
	}))
}

func (h *Handler) rows(q query, imgs []controller.Image) []FindingRow {
	now := h.Now()
	cfg := h.config()
	var rows []FindingRow
	for _, img := range imgs {
		for _, w := range img.Workloads {
			if !q.match(w) {
				continue
			}
			for _, f := range img.Findings {
				if cfg.IgnoredFor(f, w) {
					continue
				}
				eol, days := "unknown", (*float64)(nil)
				if f.EOL.Unix() != 0 { // the scanner uses the epoch for "EOL, no date"
					d := math.Floor(f.EOL.Sub(now).Hours() / 24)
					eol, days = f.EOL.Format(time.DateOnly), &d
				}
				rows = append(rows, FindingRow{
					Namespace: w.Namespace, Kind: w.Kind, Name: w.Name,
					Product: f.Product, Cycle: f.Cycle, EOL: eol, DaysLeft: days,
					Image: img.Ref, Package: f.Package, Version: f.Version, URL: f.URL, Digest: img.Digest,
					ScannedAt: img.Scanned.UTC().Format(time.RFC3339),
				})
			}
		}
	}
	return rows
}

// findings lists products reaching EOL within the window (or already EOL), soonest first.
func (h *Handler) findings(q query) any {
	rows := slices.DeleteFunc(h.rows(q, h.Inventory.Images()), func(r FindingRow) bool { return daysLeft(r) >= float64(q.withinDays) })
	slices.SortFunc(rows, func(a, b FindingRow) int {
		return cmp.Or(cmp.Compare(daysLeft(a), daysLeft(b)), cmp.Compare(a.Namespace, b.Namespace),
			cmp.Compare(a.Name, b.Name), cmp.Compare(a.Product, b.Product))
	})
	return nonNil(rows)
}

// WorkloadRow summarises one workload.
type WorkloadRow struct {
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	EOLNow    int    `json:"eol_now"`
	EOLSoon   int    `json:"eol_soon"`
	// Vulnerability counts over the workload's images; omitted with vulnerability scanning off.
	*VulnCounts
}

// VulnCounts are a workload's vulnerability counts over its images. Rows hold them as an embedded
// pointer that is nil with vulnerability scanning off, so the JSON fields are left out then.
type VulnCounts struct {
	VulnCritical int `json:"vuln_critical"`
	VulnHigh     int `json:"vuln_high"`
	VulnFixable  int `json:"vuln_fixable"` // any severity with a fixed version
}

// newVulnCounts is the one place the rows depend on the scan types: nil without trivy.
func (h *Handler) newVulnCounts() *VulnCounts {
	if !h.Trivy {
		return nil
	}
	return &VulnCounts{}
}

// add counts one image's vulnerabilities; a no-op on nil.
func (c *VulnCounts) add(t scanner.VulnTally) {
	if c != nil {
		c.VulnCritical += t.Severity(sevCritical)
		c.VulnHigh += t.Severity(sevHigh)
		c.VulnFixable += t.Fixable()
	}
}

// critical and high read nil as zero, for sorting.
func (c *VulnCounts) critical() int {
	if c == nil {
		return 0
	}
	return c.VulnCritical
}

func (c *VulnCounts) high() int {
	if c == nil {
		return 0
	}
	return c.VulnHigh
}

// worstFirst orders by CRITICAL count, then HIGH count, highest first.
func (c *VulnCounts) worstFirst(o *VulnCounts) int {
	return cmp.Or(cmp.Compare(o.critical(), c.critical()), cmp.Compare(o.high(), c.high()))
}

// NamespaceVulns are a namespace's vulnerability counts; nil without trivy, like VulnCounts.
type NamespaceVulns struct {
	// WorkloadsVulnerable counts workloads with a CRITICAL or HIGH vulnerability.
	WorkloadsVulnerable int `json:"workloads_vulnerable"`
	VulnCounts
}

// vulnCounts is nil without trivy.
func (r NamespaceRow) vulnCounts() *VulnCounts {
	if r.NamespaceVulns == nil {
		return nil
	}
	return &r.VulnCounts
}

// add counts one workload; a no-op on nil.
func (n *NamespaceVulns) add(w *VulnCounts) {
	if n == nil || w == nil {
		return
	}
	n.VulnCritical += w.VulnCritical
	n.VulnHigh += w.VulnHigh
	n.VulnFixable += w.VulnFixable
	if w.VulnCritical+w.VulnHigh > 0 {
		n.WorkloadsVulnerable++
	}
}

// ageSeconds is a database's age in whole seconds.
func ageSeconds(now, t time.Time) *float64 {
	age := math.Round(now.Sub(t).Seconds())
	return &age
}

// workloadKey identifies a workload across images.
func workloadKey(namespace, kind, name string) string { return namespace + "/" + kind + "/" + name }

// workloadRows takes the snapshot the rows came from, so every row's workload is in it.
func (h *Handler) workloadRows(q query, imgs []controller.Image, rows []FindingRow) []WorkloadRow {
	byKey := map[string]*WorkloadRow{}
	cfg := h.config()
	for _, img := range imgs {
		for _, w := range img.Workloads {
			if !q.match(w) {
				continue
			}
			k := workloadKey(w.Namespace, w.Kind, w.Name)
			wr := byKey[k]
			if wr == nil {
				wr = &WorkloadRow{Namespace: w.Namespace, Kind: w.Kind, Name: w.Name, VulnCounts: h.newVulnCounts()}
				byKey[k] = wr
			}
			wr.add(scanner.CountVulns(cfg.VulnsFor(img.Vulns, w))) // one workload can run several images
		}
	}
	for _, r := range rows {
		wr := byKey[workloadKey(r.Namespace, r.Kind, r.Name)]
		switch d := daysLeft(r); {
		case d < 0:
			wr.EOLNow++
		case d < float64(q.withinDays):
			wr.EOLSoon++
		}
	}
	out := make([]WorkloadRow, 0, len(byKey))
	for _, r := range byKey {
		out = append(out, *r)
	}
	slices.SortFunc(out, func(a, b WorkloadRow) int {
		return cmp.Or(cmp.Compare(b.EOLNow, a.EOLNow), cmp.Compare(b.EOLSoon, a.EOLSoon),
			a.worstFirst(b.VulnCounts),
			cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name))
	})
	return out
}

// workloads lists every scanned workload with its EOL counts, worst first.
func (h *Handler) workloads(q query) any {
	imgs := h.Inventory.Images()
	return h.workloadRows(q, imgs, h.rows(q, imgs))
}

// NamespaceRow summarises one namespace.
type NamespaceRow struct {
	Namespace    string `json:"namespace"`
	Workloads    int    `json:"workloads"`
	WorkloadsEOL int    `json:"workloads_eol"`
	EOLNow       int    `json:"eol_now"`
	EOLSoon      int    `json:"eol_soon"`
	// Omitted with vulnerability scanning off.
	*NamespaceVulns
}

func (h *Handler) namespaces(q query) any {
	byNS := map[string]*NamespaceRow{}
	imgs := h.Inventory.Images()
	for _, w := range h.workloadRows(q, imgs, h.rows(q, imgs)) {
		n := byNS[w.Namespace]
		if n == nil {
			n = &NamespaceRow{Namespace: w.Namespace}
			if w.VulnCounts != nil { // vulnerability scanning on
				n.NamespaceVulns = &NamespaceVulns{}
			}
			byNS[w.Namespace] = n
		}
		n.Workloads++
		n.EOLNow += w.EOLNow
		n.EOLSoon += w.EOLSoon
		if w.EOLNow > 0 {
			n.WorkloadsEOL++
		}
		n.add(w.VulnCounts)
	}
	out := make([]NamespaceRow, 0, len(byNS))
	for _, n := range byNS {
		out = append(out, *n)
	}
	slices.SortFunc(out, func(a, b NamespaceRow) int {
		return cmp.Or(cmp.Compare(b.EOLNow, a.EOLNow), cmp.Compare(b.vulnCounts().critical(), a.vulnCounts().critical()),
			cmp.Compare(a.Namespace, b.Namespace))
	})
	return out
}

// Status is the dashboard's headline numbers.
type Status struct {
	ImagesTracked      int            `json:"images_tracked"`
	ImagesScanned      int            `json:"images_scanned"`
	Workloads          int            `json:"workloads"`
	WorkloadsEOL       int            `json:"workloads_eol"`
	WorkloadsEOLSoon   int            `json:"workloads_eol_soon"`
	NextEOLDays        *float64       `json:"next_eol_days"`
	DBBuilt            string         `json:"db_built"`
	DBAgeSeconds       *float64       `json:"db_age_seconds"`
	FailedScans        int            `json:"failed_scans"`
	FailedScansByCause map[string]int `json:"failed_scans_by_reason"`
	// LastSweep is the oldest scan once every tracked image has one (RFC 3339): every running image was
	// scanned at least since then. Empty while some image is still waiting for its first scan.
	LastSweep string `json:"last_sweep"`
	// ConfigError is why the config file was last rejected; the previous config is in use until it is fixed.
	ConfigError  string `json:"config_error"`
	WithinDays   int    `json:"within_days"`
	EOLEnabled   bool   `json:"eol_enabled"`
	TrivyEnabled bool   `json:"trivy_enabled"`
	// Vulnerabilities is omitted with vulnerability scanning off.
	Vulnerabilities *VulnStatus `json:"vulnerabilities,omitempty"`
}

// VulnStatus is the vulnerability headline numbers. Counts are unique (CVE, package, version) rows per image
// digest, as in the xeol_vulnerabilities metric, so replicas don't multiply them.
type VulnStatus struct {
	BySeverity          map[string]int `json:"by_severity"`
	WorkloadsCritical   int            `json:"workloads_critical"`
	WorkloadsHigh       int            `json:"workloads_high"`    // with a HIGH, whether or not they also have a CRITICAL
	ImagesVulnerable    int            `json:"images_vulnerable"` // with a CRITICAL or HIGH, like workloads_vulnerable
	FixableCriticalHigh int            `json:"fixable_critical_high"`
	DBUpdated           string         `json:"db_updated"`
	DBAgeSeconds        *float64       `json:"db_age_seconds"`
}

func (h *Handler) status(q query) any {
	now := h.Now()
	s := Status{FailedScansByCause: map[string]int{}, WithinDays: q.withinDays, EOLEnabled: h.EOL, TrivyEnabled: h.Trivy}
	imgs := h.Inventory.Images()
	if h.Trivy {
		s.Vulnerabilities = h.vulnStatus(q, imgs, now)
	}
	var oldest time.Time
	for _, img := range imgs {
		if !slices.ContainsFunc(img.Workloads, q.match) {
			continue
		}
		s.ImagesTracked++
		if !img.Scanned.IsZero() {
			s.ImagesScanned++
			if oldest.IsZero() || img.Scanned.Before(oldest) {
				oldest = img.Scanned
			}
		}
		if img.ErrReason != "" {
			s.FailedScans++
			s.FailedScansByCause[img.ErrReason]++
		}
	}
	rows := h.rows(q, imgs)
	for _, w := range h.workloadRows(q, imgs, rows) {
		s.Workloads++
		if w.EOLNow > 0 {
			s.WorkloadsEOL++
		} else if w.EOLSoon > 0 {
			s.WorkloadsEOLSoon++
		}
	}
	for _, r := range rows {
		if d := daysLeft(r); d >= 0 && (s.NextEOLDays == nil || d < *s.NextEOLDays) {
			s.NextEOLDays = &d
		}
	}
	if built, err := h.DBBuilt(); err == nil {
		s.DBBuilt, s.DBAgeSeconds = built.UTC().Format(time.RFC3339), ageSeconds(now, built)
	}
	if s.ImagesScanned > 0 && s.ImagesScanned == s.ImagesTracked {
		s.LastSweep = oldest.UTC().Format(time.RFC3339)
	}
	if h.Config != nil {
		if err := h.Config.Err(); err != nil {
			s.ConfigError = err.Error()
		}
	}
	return s
}

// daysLeft treats an unknown EOL date as already EOL, sorted first.
func daysLeft(r FindingRow) float64 {
	if r.DaysLeft == nil {
		return math.Inf(-1)
	}
	return *r.DaysLeft
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
