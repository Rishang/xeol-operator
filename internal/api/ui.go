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
	"embed"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/Rishang/xeol-operator/internal/controller"
	"github.com/Rishang/xeol-operator/internal/eoldb"
	"github.com/Rishang/xeol-operator/internal/scanner"
)

// The page is rendered on the server; htmx (the browser loads it from a CDN, pinned with SRI) only swaps in
// /ui/report on filter changes and Refresh. Without htmx the filters form falls back to a full reload.
//
//go:embed ui/page.html
var uiFiles embed.FS

var page = template.Must(template.New("page.html").Funcs(template.FuncMap{
	"runway": runway, "tone": tone, "plural": plural, "half": func(n int) int { return n / 2 },
	"days": func(d *float64) string {
		if d == nil {
			return "no date"
		}
		return strconv.Itoa(int(*d))
	},
	"hours":     func(s *float64) int { return int(*s / 3600) },
	"windows":   windows,
	"humanDays": func(d *float64) amount { return humanDays(*d) },
	"humanAge":  func(s *float64) amount { return humanAge(*s) },
	"nextTone":  nextTone,
	"ageTone":   ageTone,
	"join":      strings.Join,
}).ParseFS(uiFiles, "ui/page.html"))

// csp allows only this page, inline styles, and the pinned htmx script.
const csp = "default-src 'none'; script-src https://cdn.jsdelivr.net; connect-src 'self'; style-src 'unsafe-inline'; " +
	"form-action 'self'; base-uri 'none'; frame-ancestors 'none'"

// report is what the page and the /ui/report fragment render.
type report struct {
	Leader      bool
	Updated     string // render time, shown so a refresh visibly lands
	Filter      string // free text: keeps table rows containing it
	Namespace   string
	Kind        string
	WithinDays  int
	Namespaces  []string // filter options, from every workload
	Kinds       []string
	Status      Status
	Sort        string // findings sort: a column key, "-" prefixed for descending; "" is soonest EOL first
	Cols        []colView
	ColFiltered bool
	Findings    []FindingRow
	Marks       []mark // the runway's dots
	Workloads   []WorkloadRow
	ByNS        []NamespaceRow
	Failed      []reasonCell
}

// findingCols are the findings table's columns, in display order, with the value each sorts and filters on.
var findingCols = []struct {
	key, title string
	num        bool
	get        func(FindingRow) string
}{
	{"namespace", "namespace", false, func(r FindingRow) string { return r.Namespace }},
	{"kind", "kind", false, func(r FindingRow) string { return r.Kind }},
	{"name", "name", false, func(r FindingRow) string { return r.Name }},
	{"product", "product", false, func(r FindingRow) string { return r.Product }},
	{"cycle", "cycle", false, func(r FindingRow) string { return r.Cycle }},
	{"eol", "EOL date", false, func(r FindingRow) string { return r.EOL }},
	{"days_left", "days left", true, func(r FindingRow) string {
		if r.DaysLeft == nil {
			return "no date"
		}
		return strconv.Itoa(int(*r.DaysLeft))
	}},
	{"image", "image", false, func(r FindingRow) string { return r.Image }},
	{"package", "package", false, func(r FindingRow) string { return r.Package }},
	{"version", "version", false, func(r FindingRow) string { return r.Version }},
	{"url", "EOL info", false, func(r FindingRow) string { return r.URL }},
	{"scanned_at", "scanned at", false, func(r FindingRow) string { return r.ScannedAt }},
}

// colView is one findings column header: its filter state and the sort a click on it applies.
type colView struct {
	Key, Title, Filter, Next, Arrow string
	Num, Active                     bool
	Values                          []colValue // distinct values in the rows, for the filter's checkboxes
}

type colValue struct {
	Value   string
	Checked bool
}

// mark is one runway dot: a product cycle, the workloads running it, and whether the table's filters keep it.
type mark struct {
	Product, Cycle string
	DaysLeft       *float64
	Workloads      []string
	On, Dim        bool
}

// runwayMarks puts every finding on the runway, one dot per product cycle (they share an EOL date). With a
// column filter active, dots with rows left in the table are on and the rest dimmed, so all stay clickable.
func runwayMarks(all, shown []FindingRow, filtered bool) []mark {
	var ms []mark
	idx := map[[2]string]int{}
	for _, r := range all {
		k := [2]string{r.Product, r.Cycle}
		i, ok := idx[k]
		if !ok {
			i, idx[k] = len(ms), len(ms)
			ms = append(ms, mark{Product: r.Product, Cycle: r.Cycle, DaysLeft: r.DaysLeft, Dim: filtered})
		}
		if w := r.Namespace + "/" + r.Name; !slices.Contains(ms[i].Workloads, w) {
			ms[i].Workloads = append(ms[i].Workloads, w)
		}
	}
	if filtered {
		for _, r := range shown {
			i := idx[[2]string{r.Product, r.Cycle}]
			ms[i].On, ms[i].Dim = true, false
		}
	}
	return ms
}

// tableFindings applies the column filters and the sort. Per column key: f_<key> keeps rows containing the
// text (any case), v_<key> (repeatable) keeps rows equal to one of the checked values, clear=<key> drops
// both. sort=key or sort=-key; the last value wins, so a clicked header overrides the page's current sort.
// pick=<product>\t<cycle> (a runway dot) filters to that product cycle, or drops that filter if it is the
// current one.
func tableFindings(rep *report, v url.Values, rows []FindingRow) []FindingRow {
	if p, c, ok := strings.Cut(v.Get("pick"), "\t"); ok {
		if slices.Equal(v["v_product"], []string{p}) && slices.Equal(v["v_cycle"], []string{c}) {
			delete(v, "v_product")
			delete(v, "v_cycle")
		} else {
			v["v_product"], v["v_cycle"] = []string{p}, []string{c}
		}
	}
	if s := v["sort"]; len(s) > 0 {
		rep.Sort = s[len(s)-1]
	}
	key, desc := strings.TrimPrefix(rep.Sort, "-"), strings.HasPrefix(rep.Sort, "-")
	all := slices.Clone(rows) // value lists come from the unfiltered rows, so a checked value never vanishes
	for _, c := range findingCols {
		cv := colView{Key: c.key, Title: c.title, Num: c.num, Next: c.key}
		var checked []string
		if !slices.Contains(v["clear"], c.key) {
			cv.Filter, checked = strings.TrimSpace(v.Get("f_"+c.key)), v["v_"+c.key]
		}
		cv.Active = cv.Filter != "" || len(checked) > 0
		if cv.Active {
			rep.ColFiltered = true
		}
		vals := make([]string, 0, len(all))
		for _, r := range all {
			vals = append(vals, c.get(r))
		}
		slices.Sort(vals)
		for _, x := range slices.Compact(vals) {
			cv.Values = append(cv.Values, colValue{x, slices.Contains(checked, x)})
		}
		if f := strings.ToLower(cv.Filter); f != "" || len(checked) > 0 {
			rows = slices.DeleteFunc(rows, func(r FindingRow) bool {
				x := c.get(r)
				return !strings.Contains(strings.ToLower(x), f) || len(checked) > 0 && !slices.Contains(checked, x)
			})
		}
		if c.key == key {
			cv.Arrow, cv.Next = "▲", "-"+c.key
			if desc {
				cv.Arrow, cv.Next = "▼", c.key
			}
			cmpRows := func(a, b FindingRow) int { return cmp.Compare(strings.ToLower(c.get(a)), strings.ToLower(c.get(b))) }
			if c.num {
				cmpRows = func(a, b FindingRow) int { return cmp.Compare(daysLeft(a), daysLeft(b)) }
			}
			slices.SortStableFunc(rows, func(a, b FindingRow) int {
				if desc {
					return cmpRows(b, a)
				}
				return cmpRows(a, b)
			})
		}
		rep.Cols = append(rep.Cols, cv)
	}
	return rows
}

// uiRoutes serves the page at / and the htmx fragment at /ui/report. A standby replica renders a
// notice (200, so htmx swaps it in) instead of the API's 503.
func (h *Handler) uiRoutes() map[string]http.Handler {
	render := func(tmpl string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			q, err := parseQuery(r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			rep := report{Leader: h.Leader == nil || h.Leader(), Filter: strings.TrimSpace(r.URL.Query().Get("q")),
				Namespace: r.URL.Query().Get("namespace"),
				Kind:      r.URL.Query().Get("kind"), WithinDays: q.withinDays,
				Updated: h.Now().Format("15:04:05")}
			if rep.Leader {
				all, _ := newQuery("", "", q.withinDays)
				for _, w := range h.workloads(all).([]WorkloadRow) {
					rep.Namespaces, rep.Kinds = append(rep.Namespaces, w.Namespace), append(rep.Kinds, w.Kind)
				}
				slices.Sort(rep.Namespaces)
				slices.Sort(rep.Kinds)
				rep.Namespaces, rep.Kinds = slices.Compact(rep.Namespaces), slices.Compact(rep.Kinds)
				rep.Status = h.status(q).(Status)
				rep.Findings = h.findings(q).([]FindingRow)
				rep.Workloads = h.workloads(q).([]WorkloadRow)
				rep.ByNS = h.namespaces(q).([]NamespaceRow)
				rep.Failed = reasonCells(q, h.Inventory.Images())
				if f := strings.ToLower(rep.Filter); f != "" {
					miss := func(fields ...string) bool {
						return !strings.Contains(strings.ToLower(strings.Join(fields, "\x00")), f)
					}
					rep.Findings = slices.DeleteFunc(rep.Findings, func(r FindingRow) bool {
						return miss(r.Namespace, r.Kind, r.Name, r.Product, r.Cycle, r.Package, r.Version, r.Image)
					})
					rep.Workloads = slices.DeleteFunc(rep.Workloads, func(r WorkloadRow) bool { return miss(r.Namespace, r.Kind, r.Name) })
					rep.ByNS = slices.DeleteFunc(rep.ByNS, func(r NamespaceRow) bool { return miss(r.Namespace) })
				}
				unfiltered := slices.Clone(rep.Findings) // tableFindings filters in place
				rep.Findings = tableFindings(&rep, r.URL.Query(), rep.Findings)
				rep.Marks = runwayMarks(unfiltered, rep.Findings, rep.ColFiltered)
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Content-Security-Policy", csp)
			if err := page.ExecuteTemplate(w, tmpl, rep); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
		})
	}
	return map[string]http.Handler{"/{$}": render("page.html"), "/ui/report": render("report")}
}

// runway places a finding on the expiry strip: already EOL on the left 30% (log-scaled age, up to 10
// years), upcoming on the right 70% (linear up to the window). Unknown dates sit at the far left.
func runway(d *float64, within int) string {
	const today = 30.0
	var p float64
	switch {
	case d == nil:
		p = 1 // EOL, no date: at the far left, but not clipped by the edge
	case *d < 0:
		p = today * (1 - math.Min(1, math.Log1p(-*d)/math.Log1p(3650)))
	case within == 0:
		p = today
	default:
		p = today + (100-today)*math.Min(1, *d/float64(within))
	}
	return fmt.Sprintf("%.2f%%", p)
}

// amount is a stat box value: a number and its unit, e.g. 2 and "years".
type amount struct {
	N    int
	Unit string
}

func unit(n int, one, many string) amount { return amount{n, plural(n, one, many)} }

// humanDays rounds a day count the way a stat panel shows it: days, then months, then years.
func humanDays(d float64) amount {
	switch {
	case d >= 365:
		return unit(int(math.Round(d/365)), "year", "years")
	case d >= 60:
		return unit(int(math.Round(d/30)), "month", "months")
	default:
		return unit(int(d), "day", "days")
	}
}

func humanAge(s float64) amount {
	switch {
	case s < 3600:
		return unit(int(s/60), "minute", "minutes")
	case s < 48*3600:
		return unit(int(s/3600), "hour", "hours")
	default:
		return unit(int(s/86400), "day", "days")
	}
}

// nextTone: amber when the next EOL falls inside the window, green otherwise.
func nextTone(d *float64, within int) string {
	if d != nil && *d < float64(within) {
		return "warn"
	}
	return "good"
}

// ageTone: green up to 2 days (one missed daily rebuild), amber up to the 30-day stale mark, red beyond.
func ageTone(s *float64) string {
	switch {
	case s == nil || *s > eoldb.StaleAfter.Seconds():
		return "bad"
	case *s > 48*3600:
		return "warn"
	default:
		return "good"
	}
}

type reasonCell struct {
	Reason string
	N      int // failed images
	Where  []string
}

// reasonCells lists every scan failure reason, zeros included, as the dashboard's panel does, with the
// workloads (and image) each failed image runs in, for the box's info tooltip. Counts match the status.
func reasonCells(q query, imgs []controller.Image) []reasonCell {
	cells := make([]reasonCell, 0, 5)
	for _, r := range []string{scanner.ReasonAuth, scanner.ReasonNotFound, scanner.ReasonOOM, scanner.ReasonOther, scanner.ReasonTimeout} {
		c := reasonCell{Reason: r}
		for _, img := range imgs {
			if img.ErrReason != r || !slices.ContainsFunc(img.Workloads, q.match) {
				continue
			}
			c.N++
			for _, w := range img.Workloads {
				if q.match(w) {
					c.Where = append(c.Where, w.Namespace+"/"+w.Name+" ("+w.Kind+"): "+img.Ref)
				}
			}
		}
		slices.Sort(c.Where)
		cells = append(cells, c)
	}
	return cells
}

type window struct {
	Days  int
	Label string
}

// windows are the "EOL soon" choices; a within_days from the URL that isn't one is added as its own option.
func windows(cur int) []window {
	w := []window{{0, "Already EOL only"}, {30, "30 days"}, {60, "60 days"}, {90, "90 days"}, {180, "6 months"},
		{365, "1 year"}, {730, "2 years"}}
	if !slices.ContainsFunc(w, func(x window) bool { return x.Days == cur }) {
		w = append(w, window{cur, strconv.Itoa(cur) + " days"})
		slices.SortFunc(w, func(a, b window) int { return cmp.Compare(a.Days, b.Days) })
	}
	return w
}

// tone colours days left: past EOL (or no date) red, within dueSoon days yellow, later plain.
func tone(d *float64) string {
	switch {
	case d == nil || *d < 0:
		return "eol"
	case *d < dueSoon:
		return "soon"
	default:
		return "later"
	}
}

// dueSoon matches the Grafana dashboard's yellow threshold for days left.
const dueSoon = 180

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
