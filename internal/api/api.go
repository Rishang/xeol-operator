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

// Package api serves the operator's current EOL findings as JSON, for Grafana (Infinity datasource)
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
)

// DefaultWithinDays is the "EOL soon" window when within_days isn't given.
const DefaultWithinDays = 180

// Handler serves /api/v1/{status,namespaces,workloads,findings}.
type Handler struct {
	Inventory *controller.Inventory
	// Config supplies spec.ignoreProducts; nil ignores nothing.
	Config  *controller.Config
	DBBuilt func() (time.Time, error)
	// Leader reports whether this replica holds the inventory; others answer 503 so a client behind
	// the Service never gets an empty answer from a standby.
	Leader func() bool
	Now    func() time.Time
	// UI serves the HTML page at /; MCP serves the same data as MCP tools at /mcp.
	UI, MCP bool
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
	return map[string]func(query) any{"status": h.status, "namespaces": h.namespaces,
		"workloads": h.workloads, "findings": h.findings}
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
	return newQuery(v.Get("namespace"), v.Get("kind"), days)
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

func (h *Handler) rows(q query, imgs []controller.Image) []FindingRow {
	now := h.Now()
	var cfg controller.ConfigSnapshot
	if h.Config != nil {
		cfg, _ = h.Config.Get()
	}
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
}

// workloadRows takes the snapshot the rows came from, so every row's workload is in it.
func (h *Handler) workloadRows(q query, imgs []controller.Image, rows []FindingRow) []WorkloadRow {
	byKey := map[string]*WorkloadRow{}
	for _, img := range imgs {
		for _, w := range img.Workloads {
			if !q.match(w) {
				continue
			}
			k := w.Namespace + "/" + w.Kind + "/" + w.Name
			if byKey[k] == nil {
				byKey[k] = &WorkloadRow{Namespace: w.Namespace, Kind: w.Kind, Name: w.Name}
			}
		}
	}
	for _, r := range rows {
		wr := byKey[r.Namespace+"/"+r.Kind+"/"+r.Name]
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
}

func (h *Handler) namespaces(q query) any {
	byNS := map[string]*NamespaceRow{}
	imgs := h.Inventory.Images()
	for _, w := range h.workloadRows(q, imgs, h.rows(q, imgs)) {
		n := byNS[w.Namespace]
		if n == nil {
			n = &NamespaceRow{Namespace: w.Namespace}
			byNS[w.Namespace] = n
		}
		n.Workloads++
		n.EOLNow += w.EOLNow
		n.EOLSoon += w.EOLSoon
		if w.EOLNow > 0 {
			n.WorkloadsEOL++
		}
	}
	out := make([]NamespaceRow, 0, len(byNS))
	for _, n := range byNS {
		out = append(out, *n)
	}
	slices.SortFunc(out, func(a, b NamespaceRow) int {
		return cmp.Or(cmp.Compare(b.EOLNow, a.EOLNow), cmp.Compare(a.Namespace, b.Namespace))
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
	WithinDays         int            `json:"within_days"`
}

func (h *Handler) status(q query) any {
	now := h.Now()
	s := Status{FailedScansByCause: map[string]int{}, WithinDays: q.withinDays}
	imgs := h.Inventory.Images()
	for _, img := range imgs {
		if !slices.ContainsFunc(img.Workloads, q.match) {
			continue
		}
		s.ImagesTracked++
		if !img.Scanned.IsZero() {
			s.ImagesScanned++
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
		s.DBBuilt = built.UTC().Format(time.RFC3339)
		age := math.Round(now.Sub(built).Seconds())
		s.DBAgeSeconds = &age
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
