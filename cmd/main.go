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
	"crypto/tls"
	"flag"
	"net/http"
	"os"
	"path/filepath"
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
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	xeolv1alpha1 "github.com/Rishang/xeol-operator/api/v1alpha1"
	"github.com/Rishang/xeol-operator/internal/api"
	"github.com/Rishang/xeol-operator/internal/controller"
	"github.com/Rishang/xeol-operator/internal/eoldb"
	"github.com/Rishang/xeol-operator/internal/metrics"
	"github.com/Rishang/xeol-operator/internal/results"
	"github.com/Rishang/xeol-operator/internal/scanner"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(xeolv1alpha1.AddToScheme(scheme))
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
		"EOL database source: online (build from endoflife.date daily) or mounted (read --db-dir).")
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
	if dbSource != "online" && dbSource != "mounted" {
		setupLog.Error(nil, "--db-source must be online or mounted", "value", dbSource)
		os.Exit(1)
	}

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
	apiHandler := &api.Handler{Now: time.Now, UI: serveUI, MCP: serveMCP}
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
	config := &controller.Config{Changed: make(chan event.GenericEvent, 1)}
	db := &eoldb.Manager{
		Dir: dbDir, Online: dbSource == "online", URL: eolURL, Client: &http.Client{Timeout: 2 * time.Minute},
		Interval: 24 * time.Hour, XeolBin: xeolBin, Log: ctrl.Log.WithName("eoldb"),
	}
	xeol := &scanner.Xeol{Bin: xeolBin, Syft: syftBin, DBDir: dbDir, StateDir: stateDir, Timeout: scanTimeout,
		MemoryLimit: scanMemoryLimit, MaxProcs: scanMaxProcs, OOMBackoff: 24 * time.Hour,
		Solo: scanWorkers <= 1}
	queue := &scanner.Queue{
		Scan: xeol.Scan,
		Store: &controller.ScanStore{Secrets: mgr.GetAPIReader(), Inventory: inventory, Config: config,
			Results: resultsDB, Log: ctrl.Log.WithName("scan")},
		Workers:    scanWorkers,
		RetryAfter: time.Hour,
		Wait:       db.Wait,
		Observe:    metrics.Observe,
		Log:        ctrl.Log.WithName("scan"),
	}
	if err := mgr.Add(db); err != nil {
		setupLog.Error(err, "Failed to add EOL database manager")
		os.Exit(1)
	}
	if err := mgr.Add(queue); err != nil {
		setupLog.Error(err, "Failed to add scan queue")
		os.Exit(1)
	}
	if err := (&controller.PodReconciler{
		Client: mgr.GetClient(), APIReader: mgr.GetAPIReader(), Config: config, Inventory: inventory,
		Results: resultsDB, Enqueue: queue.Add,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "pod")
		os.Exit(1)
	}
	if err := (&controller.XeolScannerReconciler{
		Client: mgr.GetClient(), Config: config, Inventory: inventory, DB: db,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "xeolscanner")
		os.Exit(1)
	}
	apiHandler.Inventory, apiHandler.Config, apiHandler.DBBuilt = inventory, config, db.Built
	apiHandler.Leader = func() bool {
		select {
		case <-mgr.Elected():
			return true
		default:
			return false
		}
	}
	if err := metrics.Register(ctrlmetrics.Registry, &metrics.Collector{
		Inventory: inventory, Config: config, DBBuilt: db.Built,
	}); err != nil {
		setupLog.Error(err, "Failed to register metrics")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("eol-database", db.Ready); err != nil {
		setupLog.Error(err, "Failed to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("Starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "Failed to run manager")
		os.Exit(1)
	}
}
