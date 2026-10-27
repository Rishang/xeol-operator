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

package controller

import (
	"context"
	"net/http"
	"slices"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	xeolv1alpha1 "github.com/Rishang/xeol-operator/api/v1alpha1"
	"github.com/Rishang/xeol-operator/internal/eoldb"
)

// statusRefresh is how often XeolScanner/default's status is recomputed from the inventory.
const statusRefresh = time.Minute

// DB is the EOL database, as the status needs it.
type DB interface {
	Built() (time.Time, error)
	Ready(req *http.Request) error
}

// ConfigSnapshot is the parsed spec of XeolScanner/default.
type ConfigSnapshot struct {
	NamespaceSelector labels.Selector
	ExcludeNamespaces []string
	ExcludeImages     []string
	Interval          time.Duration
	WorkloadKinds     []string // empty = every kind
	WorkloadLabels    []string
	// IgnoreProducts is the raw spec, kept to detect changes; ignores holds it compiled.
	IgnoreProducts []xeolv1alpha1.IgnoreRule
	ignores        []ignoreRule
}

// Config holds the live XeolScanner/default spec, shared by the controllers, scan queue and metrics.
type Config struct {
	// Changed gets an event after a changed spec is applied, so the Pod controller re-evaluates every
	// Pod against it. Buffered (1); nil disables it.
	Changed chan event.GenericEvent

	mu   sync.RWMutex
	snap ConfigSnapshot
	ok   bool
}

// Get returns the current config, and false when XeolScanner/default doesn't exist.
func (c *Config) Get() (ConfigSnapshot, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snap, c.ok
}

// Set replaces the config.
func (c *Config) Set(s ConfigSnapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snap, c.ok = s, true
}

// Clear marks XeolScanner/default as absent.
func (c *Config) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snap, c.ok = ConfigSnapshot{}, false
}

// XeolScannerReconciler applies XeolScanner/default and reports scan status on it.
type XeolScannerReconciler struct {
	client.Client
	Config    *Config
	Inventory *Inventory
	DB        DB
	// Now is overridable in tests.
	Now func() time.Time
}

// +kubebuilder:rbac:groups=xeol.io,resources=xeolscanners,verbs=get;list;watch
// +kubebuilder:rbac:groups=xeol.io,resources=xeolscanners/status,verbs=get;update;patch

// Reconcile loads the config from XeolScanner/default and writes its status. Other names are rejected.
func (r *XeolScannerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	var xs xeolv1alpha1.XeolScanner
	if err := r.Get(ctx, req.NamespacedName, &xs); err != nil {
		if apierrors.IsNotFound(err) && req.Name == xeolv1alpha1.DefaultName {
			log.Info("XeolScanner/default deleted; scanning stopped until it is recreated")
			r.Config.Clear()
			r.Inventory.Clear()
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	status := xs.Status.DeepCopy()
	if xs.Name != xeolv1alpha1.DefaultName {
		meta.SetStatusCondition(&status.Conditions, metav1.Condition{Type: xeolv1alpha1.ConditionReady,
			Status: metav1.ConditionFalse, Reason: "NotSingleton",
			Message: "only XeolScanner \"default\" is used", ObservedGeneration: xs.Generation})
		return ctrl.Result{}, r.writeStatus(ctx, &xs, status)
	}

	snap, err := SnapshotFrom(&xs.Spec)
	if err != nil {
		meta.SetStatusCondition(&status.Conditions, metav1.Condition{Type: xeolv1alpha1.ConditionReady,
			Status: metav1.ConditionFalse, Reason: "InvalidSpec", Message: err.Error(), ObservedGeneration: xs.Generation})
		return ctrl.Result{}, r.writeStatus(ctx, &xs, status)
	}
	prev, ok := r.Config.Get()
	changed := !ok || prev.Interval != snap.Interval || !slices.Equal(prev.ExcludeNamespaces, snap.ExcludeNamespaces) ||
		!slices.Equal(prev.ExcludeImages, snap.ExcludeImages) || !slices.Equal(prev.WorkloadLabels, snap.WorkloadLabels) ||
		!slices.Equal(prev.WorkloadKinds, snap.WorkloadKinds) ||
		prev.NamespaceSelector.String() != snap.NamespaceSelector.String() ||
		!equality.Semantic.DeepEqual(prev.IgnoreProducts, snap.IgnoreProducts)
	r.Config.Set(snap)
	if changed {
		log.Info("scan config applied", "interval", snap.Interval.String(), "namespaceSelector", snap.NamespaceSelector.String(),
			"excludeNamespaces", snap.ExcludeNamespaces, "excludeImages", snap.ExcludeImages, "workloadKinds", snap.WorkloadKinds, "workloadLabels", snap.WorkloadLabels,
			"ignoreRules", len(snap.IgnoreProducts))
		// After Set: Pods reconciled before this point saw no (or the old) config.
		select {
		case r.Config.Changed <- event.GenericEvent{Object: &xs}:
		default: // nil channel, or a re-evaluation is already pending; it will read the new config
		}
	}

	r.fillStatus(status, xs.Generation)
	return ctrl.Result{RequeueAfter: statusRefresh}, r.writeStatus(ctx, &xs, status)
}

// SnapshotFrom parses an XeolScanner spec, compiling its selectors and ignore rules.
func SnapshotFrom(spec *xeolv1alpha1.XeolScannerSpec) (ConfigSnapshot, error) {
	sel := labels.Everything()
	if spec.NamespaceSelector != nil {
		var err error
		if sel, err = metav1.LabelSelectorAsSelector(spec.NamespaceSelector); err != nil {
			return ConfigSnapshot{}, err
		}
	}
	ignores, err := compileIgnores(spec.IgnoreProducts)
	if err != nil {
		return ConfigSnapshot{}, err
	}
	interval := spec.Interval.Duration
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	return ConfigSnapshot{NamespaceSelector: sel, ExcludeNamespaces: spec.ExcludeNamespaces,
		ExcludeImages: spec.ExcludeImages, Interval: interval, WorkloadKinds: spec.WorkloadKinds, WorkloadLabels: spec.WorkloadLabels,
		IgnoreProducts: spec.IgnoreProducts, ignores: ignores}, nil
}

func (r *XeolScannerReconciler) fillStatus(status *xeolv1alpha1.XeolScannerStatus, gen int64) {
	now := r.Now()
	cfg, _ := r.Config.Get()
	images := r.Inventory.Images()
	var scanned, eol int32
	var oldest time.Time
	for _, img := range images {
		if img.Scanned.IsZero() {
			continue
		}
		scanned++
		if oldest.IsZero() || img.Scanned.Before(oldest) {
			oldest = img.Scanned
		}
		for _, f := range img.Findings {
			if f.EOL.Before(now) && !cfg.Ignored(f, img.Workloads) {
				eol++
			}
		}
	}
	status.ImagesScanned, status.EOLFindings = scanned, eol
	// A sweep is complete once every running digest has a result; its time is the oldest result.
	if scanned > 0 && int(scanned) == len(images) {
		status.LastSweepTime = &metav1.Time{Time: oldest.Truncate(time.Second)}
	}

	ready := metav1.Condition{Type: xeolv1alpha1.ConditionReady, Status: metav1.ConditionTrue,
		Reason: "Scanning", Message: "EOL database ready", ObservedGeneration: gen}
	if err := r.DB.Ready(nil); err != nil {
		ready.Status, ready.Reason, ready.Message = metav1.ConditionFalse, "DBNotReady", err.Error()
	}
	meta.SetStatusCondition(&status.Conditions, ready)

	stale := metav1.Condition{Type: xeolv1alpha1.ConditionDBStale, Status: metav1.ConditionFalse,
		Reason: "Fresh", ObservedGeneration: gen}
	if built, err := r.DB.Built(); err == nil {
		status.DBBuilt = &metav1.Time{Time: built.Truncate(time.Second)}
		if now.Sub(built) > eoldb.StaleAfter {
			stale.Status, stale.Reason = metav1.ConditionTrue, "OlderThan30Days"
			stale.Message = "EOL database built " + built.Format(time.DateOnly)
		}
	} else {
		stale.Status, stale.Reason, stale.Message = metav1.ConditionUnknown, "NoDatabase", err.Error()
	}
	meta.SetStatusCondition(&status.Conditions, stale)
}

func (r *XeolScannerReconciler) writeStatus(ctx context.Context, xs *xeolv1alpha1.XeolScanner, status *xeolv1alpha1.XeolScannerStatus) error {
	if equality.Semantic.DeepEqual(&xs.Status, status) {
		return nil
	}
	xs.Status = *status
	return r.Status().Update(ctx, xs)
}

// SetupWithManager sets up the controller with the Manager.
func (r *XeolScannerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Now == nil {
		r.Now = time.Now
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&xeolv1alpha1.XeolScanner{}).
		Named("xeolscanner").
		Complete(r)
}
