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
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// maxOwnerDepth bounds the owner walk: Pod -> ReplicaSet -> Deployment is 2; leave room for e.g. Argo Rollouts.
const maxOwnerDepth = 4

// PodReconciler keeps the Inventory in sync with running Pods.
type PodReconciler struct {
	client.Client
	// APIReader is an uncached reader for owners of kinds the operator doesn't watch: a cached Get
	// would start a cluster-wide informer for that kind, which RBAC forbids and which then blocks.
	APIReader client.Reader
	Config    *Config
	Inventory *Inventory
	// Results restores a digest's last scan after a restart; optional.
	Results ResultStore
	// Enqueue is called for digests new to the inventory that need a scan.
	Enqueue func(digest string)
}

// +kubebuilder:rbac:groups="",resources=pods;namespaces;serviceaccounts;nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=replicasets;deployments;statefulsets;daemonsets,verbs=get;list;watch
// +kubebuilder:rbac:groups=batch,resources=jobs;cronjobs,verbs=get;list;watch

// Reconcile records the running image digests of one Pod.
func (r *PodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	cfg, ok := r.Config.Get()
	if !ok {
		return ctrl.Result{}, nil // no XeolScanner/default: scan nothing
	}

	log := logf.FromContext(ctx)
	var pod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
		if apierrors.IsNotFound(err) {
			r.logRemoved(ctx, r.Inventory.DeletePod(req.NamespacedName))
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	if pod.DeletionTimestamp != nil || pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
		r.logRemoved(ctx, r.Inventory.DeletePod(req.NamespacedName))
		return ctrl.Result{}, nil
	}

	selected, err := r.namespaceSelected(ctx, cfg, pod.Namespace)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !selected {
		r.logRemoved(ctx, r.Inventory.DeletePod(req.NamespacedName))
		return ctrl.Result{}, nil
	}

	workload, err := r.resolveWorkload(ctx, &pod)
	if err != nil {
		return ctrl.Result{}, err
	}
	if len(cfg.WorkloadKinds) > 0 && !slices.Contains(cfg.WorkloadKinds, workload.Kind) {
		r.logRemoved(ctx, r.Inventory.DeletePod(req.NamespacedName))
		return ctrl.Result{}, nil
	}
	secrets, err := r.pullSecrets(ctx, &pod)
	if err != nil {
		return ctrl.Result{}, err
	}
	platform := r.platform(ctx, &pod)

	specImages := map[string]string{}
	for _, c := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
		specImages[c.Name] = c.Image
	}
	var imgs []PodImage
	for _, cs := range append(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses...) {
		repo, digest, ok := parseImageID(cs.ImageID)
		if !ok {
			// Not running yet, or a local-only image with no registry digest: nothing to pull and scan.
			continue
		}
		ref := specImages[cs.Name]
		if excluded(cfg.ExcludeImages, ref, repo) {
			continue
		}
		imgs = append(imgs, PodImage{Digest: digest, Repo: repo, Ref: ref, Platform: platform,
			Workload: workload, PullSecrets: secrets})
	}
	added, removed := r.Inventory.SetPod(req.NamespacedName, imgs)
	wl := workload.Namespace + "/" + workload.Kind + "/" + workload.Name
	for _, d := range added {
		img, _ := r.Inventory.Get(d)
		if r.restore(ctx, d) {
			log.Info("new image found, last scan restored", "image", img.Ref, "workload", wl)
			continue
		}
		log.Info("new image found, queued for scan", "image", img.Ref, "digest", d, "workload", wl)
		r.Enqueue(d)
	}
	r.logRemoved(ctx, removed)
	return ctrl.Result{}, nil
}

// restore loads a digest's saved scan into the inventory. It returns true when that scan is still
// current, so no rescan is needed; an overdue one is restored but still queued.
func (r *PodReconciler) restore(ctx context.Context, digest string) bool {
	if r.Results == nil {
		return false
	}
	rec, ok, err := r.Results.Load(digest)
	if err != nil {
		logf.FromContext(ctx).Error(err, "loading saved scan result", "digest", digest)
		return false
	}
	if !ok {
		return false
	}
	r.Inventory.SetResult(digest, rec.Findings, rec.ErrReason, rec.Scanned, rec.Next)
	return time.Now().Before(rec.Next)
}

// logRemoved logs digests no Pod runs any more. Their saved results stay, so an image that comes back
// (the next CronJob run, a Recreate rollout) is restored instead of rescanned.
func (r *PodReconciler) logRemoved(ctx context.Context, digests []string) {
	for _, d := range digests {
		logf.FromContext(ctx).Info("image no longer running, dropped from metrics", "digest", d)
	}
}

func (r *PodReconciler) namespaceSelected(ctx context.Context, cfg ConfigSnapshot, ns string) (bool, error) {
	if excluded(cfg.ExcludeNamespaces, ns) {
		return false, nil
	}
	if cfg.NamespaceSelector == nil || cfg.NamespaceSelector.Empty() {
		return true, nil
	}
	var n corev1.Namespace
	if err := r.Get(ctx, types.NamespacedName{Name: ns}, &n); err != nil {
		return false, client.IgnoreNotFound(err)
	}
	return cfg.NamespaceSelector.Matches(labels.Set(n.Labels)), nil
}

// resolveWorkload walks controller ownerReferences up to the top-level owner using metadata-only reads,
// so any kind works (Deployment, CronJob, Argo Rollout, ...). It stops at the first owner it can't read.
func (r *PodReconciler) resolveWorkload(ctx context.Context, pod *corev1.Pod) (Workload, error) {
	w := Workload{Namespace: pod.Namespace, Kind: "Pod", Name: pod.Name, Labels: pod.Labels}
	var obj metav1.Object = pod
	for range maxOwnerDepth {
		ref := metav1.GetControllerOf(obj)
		if ref == nil {
			break
		}
		gv, err := schema.ParseGroupVersion(ref.APIVersion)
		if err != nil {
			break
		}
		gvk := gv.WithKind(ref.Kind)
		owner := &metav1.PartialObjectMetadata{}
		owner.SetGroupVersionKind(gvk)
		reader := client.Reader(r.Client)
		if !cachedOwnerKinds[gvk.GroupKind()] {
			reader = r.APIReader
		}
		if err := reader.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: ref.Name}, owner); err != nil {
			if apierrors.IsNotFound(err) || apierrors.IsForbidden(err) || meta.IsNoMatchError(err) {
				// Owner gone or not readable: report it by name without labels.
				w = Workload{Namespace: pod.Namespace, Kind: ref.Kind, Name: ref.Name}
				break
			}
			return Workload{}, err
		}
		w = Workload{Namespace: pod.Namespace, Kind: ref.Kind, Name: ref.Name, Labels: owner.Labels}
		obj = owner
	}
	return w, nil
}

// cachedOwnerKinds are the owners the operator has list/watch RBAC for, so cached reads work.
var cachedOwnerKinds = map[schema.GroupKind]bool{
	{Group: appsv1.GroupName, Kind: "ReplicaSet"}: true, {Group: appsv1.GroupName, Kind: "Deployment"}: true,
	{Group: appsv1.GroupName, Kind: "StatefulSet"}: true, {Group: appsv1.GroupName, Kind: "DaemonSet"}: true,
	{Group: batchv1.GroupName, Kind: "Job"}: true, {Group: batchv1.GroupName, Kind: "CronJob"}: true,
}

// pullSecrets returns the Pod's imagePullSecrets plus its ServiceAccount's.
func (r *PodReconciler) pullSecrets(ctx context.Context, pod *corev1.Pod) ([]types.NamespacedName, error) {
	refs := append([]corev1.LocalObjectReference{}, pod.Spec.ImagePullSecrets...)
	sa := pod.Spec.ServiceAccountName
	if sa == "" {
		sa = "default"
	}
	var account corev1.ServiceAccount
	if err := r.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: sa}, &account); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, err
		}
	} else {
		refs = append(refs, account.ImagePullSecrets...)
	}
	out := make([]types.NamespacedName, 0, len(refs))
	for _, s := range refs {
		out = append(out, types.NamespacedName{Namespace: pod.Namespace, Name: s.Name})
	}
	return out, nil
}

// platform is "<os>/<arch>" of the Pod's node, so multi-arch images are scanned for the variant that runs.
func (r *PodReconciler) platform(ctx context.Context, pod *corev1.Pod) string {
	if pod.Spec.NodeName == "" {
		return ""
	}
	var node corev1.Node
	if err := r.Get(ctx, types.NamespacedName{Name: pod.Spec.NodeName}, &node); err != nil {
		return ""
	}
	goos, arch := node.Labels[corev1.LabelOSStable], node.Labels[corev1.LabelArchStable]
	if goos == "" || arch == "" {
		return ""
	}
	return goos + "/" + arch
}

// parseImageID splits a container status imageID into repository and digest.
// Accepts "docker.io/library/python@sha256:..." and "docker-pullable://python@sha256:...".
// A bare "sha256:..." (locally loaded image) has no repository to pull from and is rejected.
func parseImageID(id string) (repo, digest string, ok bool) {
	id = strings.TrimPrefix(id, "docker-pullable://")
	repo, digest, ok = strings.Cut(id, "@")
	if !ok || repo == "" || !strings.HasPrefix(digest, "sha256:") {
		return "", "", false
	}
	return repo, digest, true
}

// globCache holds compiled globs: excluded runs on every scrape and API request through ignore rules.
// ponytail: never evicted; globs come from one CR spec, so it stays tiny.
var globCache sync.Map

// excluded reports whether any value matches any glob. "*" matches any run of characters, "/"
// included, so "registry.internal/*" covers every repository under it; "?" matches one character.
// Every string is a valid glob: nothing but "*" and "?" is special.
func excluded(globs []string, values ...string) bool {
	for _, g := range globs {
		re, ok := globCache.Load(g)
		if !ok {
			pat := strings.NewReplacer(`\*`, ".*", `\?`, ".").Replace(regexp.QuoteMeta(g))
			re, _ = globCache.LoadOrStore(g, regexp.MustCompile("^"+pat+"$")) // QuoteMeta output always compiles
		}
		for _, v := range values {
			if v != "" && re.(*regexp.Regexp).MatchString(v) {
				return true
			}
		}
	}
	return false
}

// SetupWithManager watches Pods, and re-evaluates every Pod once a changed XeolScanner/default is applied.
func (r *PodReconciler) SetupWithManager(mgr ctrl.Manager) error {
	b := ctrl.NewControllerManagedBy(mgr).For(&corev1.Pod{}).Named("pod")
	if r.Config.Changed != nil {
		b = b.WatchesRawSource(source.Channel(r.Config.Changed, handler.EnqueueRequestsFromMapFunc(r.allPods)))
	}
	return b.Complete(r)
}

func (r *PodReconciler) allPods(ctx context.Context, _ client.Object) []ctrl.Request {
	var pods corev1.PodList
	if err := r.List(ctx, &pods); err != nil {
		logf.FromContext(ctx).Error(err, "listing pods after XeolScanner change")
		return nil
	}
	reqs := make([]ctrl.Request, 0, len(pods.Items))
	for _, p := range pods.Items {
		reqs = append(reqs, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: p.Namespace, Name: p.Name}})
	}
	return reqs
}
