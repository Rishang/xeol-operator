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

package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/Rishang/xeol-operator/internal/api"
	"github.com/Rishang/xeol-operator/internal/config"
	"github.com/Rishang/xeol-operator/internal/controller"
	"github.com/Rishang/xeol-operator/internal/eoldb"
	"github.com/Rishang/xeol-operator/internal/metrics"
	"github.com/Rishang/xeol-operator/internal/results"
	"github.com/Rishang/xeol-operator/internal/scanner"
	"github.com/Rishang/xeol-operator/internal/vulndb"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	// +kubebuilder:scaffold:scheme
}

// nolint:gocyclo
func main() {
	if len(os.Args) > 1 && os.Args[1] == "build-db" {
		os.Exit(buildDB(os.Args[2:]))
	}

	var metricsAddr string
	var metricsCertPath, metricsCertName, metricsCertKey string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var tlsOpts []func(*tls.Config)
	var scanWorkers int
	var scanTimeout time.Duration
	var serveUI, serveMCP bool
	var dbSource, dbDir, xeolBin, syftBin, stateDir, eolURL, scanMemoryLimit string
	var scanMaxProcs int
	var configPath string
	flag.StringVar(&configPath, "config", config.DefaultPath,
		"Scan config file (YAML: namespaceSelector, excludeNamespaces, ...); reloaded when it changes. Missing = defaults.")
	var eolScan, trivyScan bool
	var trivyBin, trivyDBDir, trivyDBRepo string
	flag.BoolVar(&eolScan, "eol", true, "Scan for end-of-life software with xeol.")
	flag.BoolVar(&trivyScan, "trivy", false, "Scan for vulnerabilities with trivy, on the same syft SBOM.")
	flag.StringVar(&trivyBin, "trivy-bin", "trivy", "Path to the trivy binary.")
	flag.StringVar(&trivyDBDir, "trivy-db-dir", "/var/cache/trivy",
		"trivy database directory: online mode keeps it in current/; mounted mode reads db/trivy.db from it.")
	flag.StringVar(&trivyDBRepo, "trivy-db-repository", "",
		"Comma-separated OCI repositories for the trivy DB (online mode); empty uses trivy's defaults.")
	flag.IntVar(&scanWorkers, "scan-workers", 1, "Concurrent xeol scans; memory use depends on the image.")
	flag.StringVar(&stateDir, "state-dir", "/var/lib/xeol-operator",
		"In-flight scan markers; keep it on a volume that survives container restarts to detect OOM kills.")
	flag.StringVar(&scanMemoryLimit, "scan-memory-limit", "",
		"GOMEMLIMIT for syft and xeol, e.g. 800MiB; empty leaves it unset.")
	flag.IntVar(&scanMaxProcs, "scan-max-procs", 2, "GOMAXPROCS for syft and xeol; 0 leaves it unset.")
	flag.DurationVar(&scanTimeout, "scan-timeout", 5*time.Minute, "Timeout for one image scan.")
	flag.BoolVar(&serveUI, "ui", false, "Serve the HTML UI at / on the metrics port.")
	flag.BoolVar(&serveMCP, "mcp", false, "Serve the findings as MCP tools (Streamable HTTP) at /mcp on the metrics port.")
	flag.StringVar(&dbSource, "db-source", "online",
		"Database source: online (build the EOL DB from endoflife.date and download the trivy DB daily) "+
			"or mounted (read --db-dir and --trivy-db-dir).")
	flag.StringVar(&dbDir, "db-dir", "/var/cache/xeol", "xeol database directory; holds 1/xeol.db.")
	flag.StringVar(&xeolBin, "xeol-bin", "xeol", "Path to the xeol binary.")
	flag.StringVar(&syftBin, "syft-bin", "syft", "Path to the syft binary that catalogs images for xeol.")
	flag.StringVar(&eolURL, "eol-url", eoldb.DefaultURL, "endoflife.date products/full URL (online mode).")
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.StringVar(&metricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	flag.StringVar(&metricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	flag.StringVar(&metricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics server")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	if !eolScan && !trivyScan {
		setupLog.Error(nil, "at least one of --eol or --trivy must be on")
		os.Exit(1)
	}
	if dbSource != "online" && dbSource != "mounted" {
		setupLog.Error(nil, "--db-source must be online or mounted", "value", dbSource)
		os.Exit(1)
	}
	online := dbSource == "online"

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("Disabling HTTP/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.25.0/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	// The JSON API (for Grafana's Infinity datasource), the UI and MCP share the metrics port; the
	// handler's data fields are filled in once the manager and inventory exist.
	// The routes depend on the scan types, so they are set before Routes() below.
	apiHandler := &api.Handler{Now: time.Now, UI: serveUI, MCP: serveMCP, Trivy: trivyScan, EOL: eolScan}
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
		ExtraHandlers: apiHandler.Routes(),
	}

	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.25.0/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	// If the certificate is not specified, controller-runtime will automatically
	// generate self-signed certificates for the metrics server. While convenient for development and testing,
	// this setup is not recommended for production.
	//
	// TODO(user): If you enable certManager, uncomment the following lines:
	// - [METRICS-WITH-CERTS] at config/default/kustomization.yaml to generate and use certificates
	// managed by cert-manager for the metrics server.
	// - [PROMETHEUS-WITH-CERTS] at config/prometheus/kustomization.yaml for TLS certification.
	if len(metricsCertPath) > 0 {
		setupLog.Info("Initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", metricsCertPath, "metrics-cert-name", metricsCertName, "metrics-cert-key", metricsCertKey)

		metricsServerOptions.CertDir = metricsCertPath
		metricsServerOptions.CertName = metricsCertName
		metricsServerOptions.KeyName = metricsCertKey
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "01f6af80.io",
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "Failed to start manager")
		os.Exit(1)
	}

	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		setupLog.Error(err, "Failed to create state dir", "dir", stateDir)
		os.Exit(1)
	}
	resultsDB, err := results.Open(filepath.Join(stateDir, "results.db"))
	if err != nil {
		setupLog.Error(err, "Failed to open results store")
		os.Exit(1)
	}
	defer func() { _ = resultsDB.Close() }()
	inventory := controller.NewInventory()
	scanCfg := &controller.Config{Changed: make(chan event.GenericEvent, 1)}
	// The config file is loaded before anything starts: a bad file stops the operator instead of scanning
	// with a guess. Later edits are picked up by the watcher; a bad edit keeps the current config.
	cfgFile, cfgBytes, err := config.Load(configPath)
	if err == nil {
		err = scanCfg.Apply(cfgFile)
	}
	if err != nil {
		setupLog.Error(err, "Invalid config file", "path", configPath)
		os.Exit(1)
	}
	setupLog.Info("config file loaded", "path", configPath)
	db := &eoldb.Manager{
		Dir: dbDir, Online: online, URL: eolURL, Client: &http.Client{Timeout: 2 * time.Minute},
		Interval: 24 * time.Hour, XeolBin: xeolBin, Log: ctrl.Log.WithName("eoldb"),
	}
	dbs := scanDBs{}
	if eolScan {
		dbs.eol, dbs.all = db, append(dbs.all, db)
	}
	xeol := &scanner.Runner{XeolBin: xeolBin, Syft: syftBin, DBDir: dbDir, StateDir: stateDir, Timeout: scanTimeout,
		MemoryLimit: scanMemoryLimit, MaxProcs: scanMaxProcs, OOMBackoff: 24 * time.Hour,
		Solo: scanWorkers <= 1, EOL: eolScan}
	if trivyScan {
		dbs.vuln = &vulndb.Manager{Dir: trivyDBDir, Online: online, TrivyBin: trivyBin,
			Interval: 24 * time.Hour, Log: ctrl.Log.WithName("vulndb")}
		if trivyDBRepo != "" {
			dbs.vuln.Repositories = strings.Split(trivyDBRepo, ",")
		}
		xeol.TrivyBin, xeol.TrivyCacheDir = trivyBin, dbs.vuln.CacheDir()
		dbs.all = append(dbs.all, dbs.vuln)
	}
	// Scans interrupted by a container restart (e.g. an OOM kill) leave their layers and SBOMs behind.
	if err := xeol.Sweep(); err != nil {
		setupLog.Error(err, "Failed to remove leftover scan dirs")
	}
	queue := &scanner.Queue{
		Scan: xeol.Scan,
		Store: &controller.ScanStore{Secrets: mgr.GetAPIReader(), Inventory: inventory, Config: scanCfg,
			Results: resultsDB, Log: ctrl.Log.WithName("scan")},
		Workers:    scanWorkers,
		RetryAfter: time.Hour,
		Wait:       dbs.Wait,
		Observe:    metrics.Observe,
		Log:        ctrl.Log.WithName("scan"),
	}
	for _, r := range dbs.all {
		if err := mgr.Add(r); err != nil {
			setupLog.Error(err, "Failed to add database manager")
			os.Exit(1)
		}
	}
	if err := mgr.Add(queue); err != nil {
		setupLog.Error(err, "Failed to add scan queue")
		os.Exit(1)
	}
	if err := (&controller.PodReconciler{
		Client: mgr.GetClient(), APIReader: mgr.GetAPIReader(), Config: scanCfg, Inventory: inventory,
		Results: resultsDB, Enqueue: queue.Add,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "pod")
		os.Exit(1)
	}
	if err := mgr.Add(&config.Watcher{Path: configPath, Interval: 5 * time.Second, Last: cfgBytes,
		Apply: scanCfg.Apply, Failed: scanCfg.SetError, Log: ctrl.Log.WithName("config")}); err != nil {
		setupLog.Error(err, "Failed to add config watcher")
		os.Exit(1)
	}
	apiHandler.Inventory, apiHandler.Config = inventory, scanCfg
	apiHandler.DBBuilt, apiHandler.VulnDBUpdated = dbs.Built, dbs.Updated
	apiHandler.Leader = func() bool {
		select {
		case <-mgr.Elected():
			return true
		default:
			return false
		}
	}
	collector := &metrics.Collector{Inventory: inventory, Config: scanCfg, DBBuilt: dbs.Built, VulnDBUpdated: dbs.Updated}
	if err := metrics.Register(ctrlmetrics.Registry, collector); err != nil {
		setupLog.Error(err, "Failed to register metrics")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("databases", dbs.Ready); err != nil {
		setupLog.Error(err, "Failed to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("Starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "Failed to run manager")
		os.Exit(1)
	}
}

// dbManager is what the EOL and vulnerability database managers have in common (internal/dbloop).
type dbManager interface {
	manager.Runnable
	Wait(context.Context) error
	Ready(*http.Request) error
}

// scanDBs is the databases of the enabled scan types; a nil field means that scan type is off.
type scanDBs struct {
	eol  *eoldb.Manager
	vuln *vulndb.Manager
	all  []dbManager // the enabled ones
}

// Wait blocks until every enabled database has been usable once: scans need all of them.
func (d scanDBs) Wait(ctx context.Context) error {
	for _, m := range d.all {
		if err := m.Wait(ctx); err != nil {
			return err
		}
	}
	return nil
}

// Ready reports every enabled database that is not ready yet.
func (d scanDBs) Ready(r *http.Request) error {
	errs := make([]error, 0, len(d.all))
	for _, m := range d.all {
		errs = append(errs, m.Ready(r))
	}
	return errors.Join(errs...)
}

// Updated is the vulnerability database's publish time; an error with vulnerability scanning off.
func (d scanDBs) Updated() (time.Time, error) {
	if d.vuln == nil {
		return time.Time{}, errors.New("vulnerability scanning is off")
	}
	return d.vuln.Updated()
}

// Built is the EOL database's build time; an error with EOL scanning off.
func (d scanDBs) Built() (time.Time, error) {
	if d.eol == nil {
		return time.Time{}, errors.New("EOL scanning is off")
	}
	return d.eol.Built()
}
