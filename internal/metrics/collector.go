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

// Package metrics exposes scan results as Prometheus metrics, rendered from the inventory on each
// scrape so images that stop running disappear without explicit series deletion.
package metrics

import (
	"regexp"
	"slices"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Rishang/xeol-operator/internal/controller"
)

var (
	// ScanDuration observes each xeol scan.
	ScanDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "xeol_scan_duration_seconds",
		Help:    "Duration of xeol image scans.",
		Buckets: []float64{1, 2, 5, 10, 20, 30, 60, 120, 300, 600},
	})
	// ScanErrors counts failed scans by reason.
	ScanErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "xeol_scan_errors_total",
		Help: "Failed scans by reason (auth, not_found, timeout, oom, other).",
	}, []string{"reason"})

	eolDesc = prometheus.NewDesc("xeol_eol_date_seconds",
		"End-of-life date (Unix seconds) of a product found in a running image. Compare with time() in PromQL.",
		[]string{"image", "digest", "product", "cycle", "package", "version", "url"}, nil)
	dbBuiltDesc = prometheus.NewDesc("xeol_db_built_timestamp_seconds",
		"When the EOL database in use was built (Unix seconds).", nil, nil)
)

// Observe records one scan; errReason is "" on success. A zero d is a scan that never started
// (e.g. its pull secret couldn't be read): counted as an error, kept out of the duration histogram.
func Observe(d time.Duration, errReason string) {
	if d > 0 {
		ScanDuration.Observe(d.Seconds())
	}
	if errReason != "" {
		ScanErrors.WithLabelValues(errReason).Inc()
	}
}

// Collector renders findings and workload mappings from the inventory on each scrape.
type Collector struct {
	Inventory *controller.Inventory
	Config    *controller.Config
	DBBuilt   func() (time.Time, error)
}

// Describe sends nothing: the workload metric's labels follow spec.workloadLabels, so this is an unchecked collector.
func (c *Collector) Describe(chan<- *prometheus.Desc) {}

// Collect emits one series per (digest, product, cycle, package, version) and per (digest, workload).
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	if built, err := c.DBBuilt(); err == nil {
		ch <- prometheus.MustNewConstMetric(dbBuiltDesc, prometheus.GaugeValue, float64(built.Unix()))
	}

	var keys []string
	wlLabels := []string{"digest", "namespace", "kind", "name"}
	cfg, ok := c.Config.Get()
	if ok {
		for _, k := range cfg.WorkloadLabels {
			// Keys that sanitize to the same name ("app.team", "app_team") or repeat: the first wins.
			// A duplicate label name would make every scrape fail.
			if l := "k8s_label_" + sanitize(k); !slices.Contains(wlLabels, l) {
				keys, wlLabels = append(keys, k), append(wlLabels, l)
			}
		}
	}
	wlDesc := prometheus.NewDesc("xeol_image_workload",
		"A workload running an image digest; join on digest with xeol_eol_date_seconds.", wlLabels, nil)

	for _, img := range c.Inventory.Images() {
		for _, f := range img.Findings {
			if cfg.Ignored(f, img.Workloads) {
				continue
			}
			ch <- prometheus.MustNewConstMetric(eolDesc, prometheus.GaugeValue, float64(f.EOL.Unix()),
				img.Ref, img.Digest, f.Product, f.Cycle, f.Package, f.Version, f.URL)
		}
		for _, w := range img.Workloads {
			vals := make([]string, 0, 4+len(keys))
			vals = append(vals, img.Digest, w.Namespace, w.Kind, w.Name)
			for _, k := range keys {
				vals = append(vals, w.Labels[k])
			}
			ch <- prometheus.MustNewConstMetric(wlDesc, prometheus.GaugeValue, 1, vals...)
		}
	}
}

var invalidLabelChars = regexp.MustCompile(`[^a-zA-Z0-9_]`)

// sanitize turns a Kubernetes label key like "app.kubernetes.io/team" into a Prometheus label name.
func sanitize(k string) string { return invalidLabelChars.ReplaceAllString(k, "_") }

// Register adds the collector and scan metrics to a registry, e.g. controller-runtime's metrics.Registry.
func Register(reg prometheus.Registerer, c *Collector) error {
	for _, col := range []prometheus.Collector{c, ScanDuration, ScanErrors} {
		if err := reg.Register(col); err != nil {
			return err
		}
	}
	return nil
}
