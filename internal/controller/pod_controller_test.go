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
	"fmt"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Rishang/xeol-operator/internal/config"
	"github.com/Rishang/xeol-operator/internal/results"
	"github.com/Rishang/xeol-operator/internal/scanner"
)

const testDigest = "sha256:1d52838af602b4b5a831beb13a0e4d073280665ea7be7f69ce2382f29c5a613f"

var nsCounter int

func newNamespace(labels map[string]string) string {
	nsCounter++
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("pods-%d", nsCounter), Labels: labels}}
	Expect(k8sClient.Create(ctx, ns)).To(Succeed())
	return ns.Name
}

func podTemplate(labels map[string]string) corev1.PodTemplateSpec {
	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: labels},
		Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyOnFailure,
			Containers: []corev1.Container{{Name: "app", Image: "python:3.8-slim"}}},
	}
}

func controllerRef(obj client.Object, apiVersion, kind string) *metav1.OwnerReference {
	return &metav1.OwnerReference{APIVersion: apiVersion, Kind: kind, Name: obj.GetName(), UID: obj.GetUID(), Controller: new(true)}
}

// runningPod creates a Pod and sets its status as a kubelet would.
func runningPod(ns, name string, owner *metav1.OwnerReference, imageID string, mutate ...func(*corev1.Pod)) types.NamespacedName {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}, Spec: podTemplate(nil).Spec}
	if owner != nil {
		pod.OwnerReferences = []metav1.OwnerReference{*owner}
	}
	for _, m := range mutate {
		m(pod)
	}
	Expect(k8sClient.Create(ctx, pod)).To(Succeed())
	pod.Status = corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{
		Name: "app", Image: "docker.io/library/python:3.8-slim", ImageID: imageID,
	}}}
	Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())
	return types.NamespacedName{Namespace: ns, Name: name}
}

var _ = Describe("Pod controller", func() {
	var (
		inv      *Inventory
		cfg      *Config
		enqueued []string
		r        *PodReconciler
	)

	BeforeEach(func() {
		inv, cfg, enqueued = NewInventory(), &Config{}, nil
		snap, err := SnapshotFrom(&config.Config{ExcludeNamespaces: []string{"kube-*"}})
		Expect(err).NotTo(HaveOccurred())
		cfg.Set(snap)
		r = &PodReconciler{Client: k8sClient, APIReader: k8sClient, Config: cfg, Inventory: inv,
			Enqueue: func(d string) { enqueued = append(enqueued, d) }}
	})

	reconcile := func(key types.NamespacedName) {
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
	}

	It("resolves a Deployment through its ReplicaSet and keeps the Deployment's labels", func() {
		ns := newNamespace(nil)
		sel := map[string]string{"app": "api"}
		dep := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "api", Labels: map[string]string{"team": "pay"}},
			Spec:       appsv1.DeploymentSpec{Selector: &metav1.LabelSelector{MatchLabels: sel}, Template: podTemplate(sel)},
		}
		dep.Spec.Template.Spec.RestartPolicy = corev1.RestartPolicyAlways
		Expect(k8sClient.Create(ctx, dep)).To(Succeed())
		rs := &appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "api-7d9f",
				OwnerReferences: []metav1.OwnerReference{*controllerRef(dep, "apps/v1", "Deployment")}},
			Spec: appsv1.ReplicaSetSpec{Selector: &metav1.LabelSelector{MatchLabels: sel}, Template: dep.Spec.Template},
		}
		Expect(k8sClient.Create(ctx, rs)).To(Succeed())
		key := runningPod(ns, "api-7d9f-x", controllerRef(rs, "apps/v1", "ReplicaSet"), "docker.io/library/python@"+testDigest)

		reconcile(key)
		img, ok := inv.Get(testDigest)
		Expect(ok).To(BeTrue())
		Expect(img.Repo).To(Equal("docker.io/library/python"))
		Expect(img.Ref).To(Equal("python:3.8-slim"))
		Expect(img.Workloads).To(HaveLen(1))
		Expect(img.Workloads[0].Kind).To(Equal("Deployment"))
		Expect(img.Workloads[0].Name).To(Equal("api"))
		Expect(img.Workloads[0].Labels).To(HaveKeyWithValue("team", "pay"))
		Expect(enqueued).To(Equal([]string{testDigest}))

		By("reconciling again without changes, nothing is re-queued")
		reconcile(key)
		Expect(enqueued).To(HaveLen(1))

		By("deleting the Pod, the digest is dropped")
		Expect(k8sClient.Delete(ctx, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: key.Name}},
			client.GracePeriodSeconds(0))).To(Succeed())
		Eventually(func() bool {
			reconcile(key)
			_, ok := inv.Get(testDigest)
			return ok
		}).Should(BeFalse())
	})

	It("resolves a CronJob through its Job", func() {
		ns := newNamespace(nil)
		cj := &batchv1.CronJob{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "backup"},
			Spec: batchv1.CronJobSpec{Schedule: "0 * * * *",
				JobTemplate: batchv1.JobTemplateSpec{Spec: batchv1.JobSpec{Template: podTemplate(nil)}}},
		}
		Expect(k8sClient.Create(ctx, cj)).To(Succeed())
		job := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "backup-2891",
				OwnerReferences: []metav1.OwnerReference{*controllerRef(cj, "batch/v1", "CronJob")}},
			Spec: batchv1.JobSpec{Template: podTemplate(nil)},
		}
		Expect(k8sClient.Create(ctx, job)).To(Succeed())
		reconcile(runningPod(ns, "backup-2891-x", controllerRef(job, "batch/v1", "Job"), "docker.io/library/python@"+testDigest))

		img, _ := inv.Get(testDigest)
		Expect(img.Workloads).To(ConsistOf(HaveField("Kind", "CronJob")))
		Expect(img.Workloads[0].Name).To(Equal("backup"))
	})

	It("records a bare Pod as itself and dedupes a digest shared across workloads", func() {
		ns := newNamespace(nil)
		reconcile(runningPod(ns, "one", nil, "docker.io/library/python@"+testDigest))
		reconcile(runningPod(ns, "two", nil, "docker-pullable://python@"+testDigest))

		Expect(inv.Images()).To(HaveLen(1))
		img, _ := inv.Get(testDigest)
		Expect(img.Workloads).To(HaveLen(2))
		Expect(img.Workloads[0].Kind).To(Equal("Pod"))
		Expect(enqueued).To(HaveLen(1))
	})

	It("reports an owner it cannot read by name", func() {
		ns := newNamespace(nil)
		ghost := &metav1.OwnerReference{APIVersion: "argoproj.io/v1alpha1", Kind: "Rollout", Name: "web", UID: "u1", Controller: new(true)}
		reconcile(runningPod(ns, "web-x", ghost, "docker.io/library/python@"+testDigest))
		img, _ := inv.Get(testDigest)
		Expect(img.Workloads).To(ConsistOf(HaveField("Name", "web")))
		Expect(img.Workloads[0].Kind).To(Equal("Rollout"))
	})

	It("collects pull secrets from the Pod and its ServiceAccount", func() {
		ns := newNamespace(nil)
		sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "builder"},
			ImagePullSecrets: []corev1.LocalObjectReference{{Name: "sa-cred"}}}
		Expect(k8sClient.Create(ctx, sa)).To(Succeed())
		reconcile(runningPod(ns, "p", nil, "docker.io/library/python@"+testDigest, func(p *corev1.Pod) {
			p.Spec.ServiceAccountName = "builder"
			p.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: "pod-cred"}}
		}))
		img, _ := inv.Get(testDigest)
		Expect(img.PullSecrets).To(ConsistOf(
			types.NamespacedName{Namespace: ns, Name: "pod-cred"},
			types.NamespacedName{Namespace: ns, Name: "sa-cred"}))
	})

	It("skips excluded namespaces, excluded images, local-only images, and unselected namespaces", func() {
		excludedNS := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kube-test"}}
		Expect(k8sClient.Create(ctx, excludedNS)).To(Succeed())
		reconcile(runningPod("kube-test", "p", nil, "docker.io/library/python@"+testDigest))

		ns := newNamespace(nil)
		reconcile(runningPod(ns, "local", nil, testDigest)) // k3d/kind-loaded image: no registry
		Expect(inv.Images()).To(BeEmpty())

		snap, _ := SnapshotFrom(&config.Config{
			ExcludeImages:     []string{"python:*"},
			NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"scan": "yes"}},
		})
		cfg.Set(snap)
		reconcile(runningPod(ns, "excluded-image", nil, "docker.io/library/python@"+testDigest))
		other := newNamespace(map[string]string{"scan": "no"})
		reconcile(runningPod(other, "unselected", nil, "docker.io/library/node@sha256:aa"))
		Expect(inv.Images()).To(BeEmpty())
	})

	It("tracks only the configured top-level workload kinds", func() {
		ns := newNamespace(nil)
		snap, _ := SnapshotFrom(&config.Config{WorkloadKinds: []string{"Deployment", "CronJob"}})
		cfg.Set(snap)
		prom := &metav1.OwnerReference{APIVersion: "monitoring.coreos.com/v1", Kind: "Prometheus", Name: "k8s", UID: "u2", Controller: new(true)}
		key := runningPod(ns, "prometheus-k8s-0", prom, "docker.io/library/python@"+testDigest)
		reconcile(key)
		reconcile(runningPod(ns, "bare", nil, "docker.io/library/python@"+testDigest))
		Expect(inv.Images()).To(BeEmpty(), "Prometheus and bare Pod kinds are not in the list")

		snap, _ = SnapshotFrom(&config.Config{}) // empty list: every kind
		cfg.Set(snap)
		reconcile(key)
		img, _ := inv.Get(testDigest)
		Expect(img.Workloads).To(ConsistOf(HaveField("Kind", "Prometheus")))

		snap, _ = SnapshotFrom(&config.Config{WorkloadKinds: []string{"Deployment"}})
		cfg.Set(snap)
		reconcile(key) // a kind dropped from the list drops its Pods on re-evaluation
		Expect(inv.Images()).To(BeEmpty())
	})

	It("restores saved results after a restart instead of rescanning, and keeps them when the image goes", func() {
		db, err := results.Open(filepath.Join(GinkgoT().TempDir(), "results.db"))
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = db.Close() }()
		r.Results = db
		saved := results.Record{Scanned: time.Now().Add(-time.Hour), Next: time.Now().Add(time.Hour),
			Findings: []scanner.Finding{{Product: "Python", Cycle: "3.8"}}}
		Expect(db.Save(testDigest, saved)).To(Succeed())
		Expect(db.Save("sha256:overdue", results.Record{Scanned: time.Now().Add(-48 * time.Hour), Next: time.Now().Add(-time.Hour)})).To(Succeed())

		ns := newNamespace(nil)
		key := runningPod(ns, "p", nil, "docker.io/library/python@"+testDigest)
		reconcile(key)
		img, ok := inv.Get(testDigest)
		Expect(ok).To(BeTrue())
		Expect(img.Findings).To(HaveLen(1))
		Expect(enqueued).To(BeEmpty(), "a current saved result needs no rescan")

		reconcile(runningPod(ns, "q", nil, "docker.io/library/python@sha256:overdue"))
		Expect(enqueued).To(Equal([]string{"sha256:overdue"}), "an overdue saved result is rescanned")

		Expect(k8sClient.Delete(ctx, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "p"}},
			client.GracePeriodSeconds(0))).To(Succeed())
		Eventually(func() bool {
			reconcile(key)
			_, running := inv.Get(testDigest)
			return running
		}).Should(BeFalse())
		_, found, _ := db.Load(testDigest)
		Expect(found).To(BeTrue(), "kept so the next run (e.g. a CronJob) is restored, not rescanned")

		By("the image coming back, the saved result is restored without a rescan")
		enqueued = nil
		reconcile(runningPod(ns, "p2", nil, "docker.io/library/python@"+testDigest))
		Expect(enqueued).To(BeEmpty())
		img, _ = inv.Get(testDigest)
		Expect(img.Findings).To(HaveLen(1))
	})
})
