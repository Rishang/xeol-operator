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
	"errors"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/event"

	xeolv1alpha1 "github.com/Rishang/xeol-operator/api/v1alpha1"
	"github.com/Rishang/xeol-operator/internal/scanner"
)

type fakeDB struct {
	built time.Time
	err   error
}

func (f fakeDB) Built() (time.Time, error) { return f.built, f.err }
func (f fakeDB) Ready(*http.Request) error { return f.err }

var _ = Describe("XeolScanner controller", func() {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

	newReconciler := func(db DB) (*XeolScannerReconciler, *Config, *Inventory) {
		cfg, inv := &Config{Changed: make(chan event.GenericEvent, 1)}, NewInventory()
		return &XeolScannerReconciler{Client: k8sClient, Config: cfg, Inventory: inv, DB: db,
			Now: func() time.Time { return now }}, cfg, inv
	}
	get := func(name string) *xeolv1alpha1.XeolScanner {
		var xs xeolv1alpha1.XeolScanner
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, &xs)).To(Succeed())
		return &xs
	}

	AfterEach(func() {
		Expect(k8sClient.DeleteAllOf(ctx, &xeolv1alpha1.XeolScanner{})).To(Succeed())
	})

	It("applies CRD defaults to an empty spec", func() {
		Expect(k8sClient.Create(ctx, &xeolv1alpha1.XeolScanner{ObjectMeta: metav1.ObjectMeta{Name: "default"}})).To(Succeed())
		Expect(get("default").Spec.Interval.Duration).To(Equal(24 * time.Hour))
		Expect(get("default").Spec.WorkloadKinds).To(Equal([]string{"Deployment", "StatefulSet", "DaemonSet", "Job", "CronJob"}))
	})

	It("loads config from XeolScanner/default and reports status", func() {
		r, cfg, inv := newReconciler(fakeDB{built: now.Add(-time.Hour)})
		Expect(k8sClient.Create(ctx, &xeolv1alpha1.XeolScanner{ObjectMeta: metav1.ObjectMeta{Name: "default"},
			Spec: xeolv1alpha1.XeolScannerSpec{WorkloadLabels: []string{"team"}}})).To(Succeed())

		pod := types.NamespacedName{Namespace: "a", Name: "p"}
		inv.SetPod(pod, []PodImage{{Digest: "sha256:a"}, {Digest: "sha256:b"}})
		past, future := now.AddDate(-1, 0, 0), now.AddDate(1, 0, 0)
		inv.SetResult("sha256:a", []scanner.Finding{{EOL: past}, {EOL: future}}, "", now.Add(-time.Minute), now.Add(time.Hour))
		inv.SetResult("sha256:b", []scanner.Finding{{EOL: past}}, "", now, now.Add(time.Hour))

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "default"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(Equal(statusRefresh))

		snap, ok := cfg.Get()
		Expect(ok).To(BeTrue())
		Expect(snap.WorkloadLabels).To(Equal([]string{"team"}))
		Expect(snap.Interval).To(Equal(24 * time.Hour))
		Expect(cfg.Changed).To(HaveLen(1), "Pods are re-evaluated once the new config is set")
		<-cfg.Changed
		_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "default"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Changed).To(BeEmpty(), "an unchanged spec doesn't re-evaluate every Pod")

		st := get("default").Status
		Expect(st.ImagesScanned).To(Equal(int32(2)))
		Expect(st.EOLFindings).To(Equal(int32(2)), "only findings already past EOL count")
		Expect(st.LastSweepTime.Time).To(BeTemporally("==", now.Add(-time.Minute)))
		Expect(meta.IsStatusConditionTrue(st.Conditions, xeolv1alpha1.ConditionReady)).To(BeTrue())
		Expect(meta.IsStatusConditionFalse(st.Conditions, xeolv1alpha1.ConditionDBStale)).To(BeTrue())
	})

	It("sets DBStale for a database older than 30 days and Ready=False when the DB isn't ready", func() {
		r, _, _ := newReconciler(fakeDB{built: now.AddDate(0, 0, -100)})
		Expect(k8sClient.Create(ctx, &xeolv1alpha1.XeolScanner{ObjectMeta: metav1.ObjectMeta{Name: "default"}})).To(Succeed())
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "default"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(meta.IsStatusConditionTrue(get("default").Status.Conditions, xeolv1alpha1.ConditionDBStale)).To(BeTrue())

		r.DB = fakeDB{err: errors.New("missing")}
		_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "default"}})
		Expect(err).NotTo(HaveOccurred())
		c := meta.FindStatusCondition(get("default").Status.Conditions, xeolv1alpha1.ConditionReady)
		Expect(c.Status).To(Equal(metav1.ConditionFalse))
		Expect(c.Reason).To(Equal("DBNotReady"))
	})

	It("applies ignoreProducts to status without rescanning", func() {
		r, cfg, inv := newReconciler(fakeDB{built: now})
		Expect(k8sClient.Create(ctx, &xeolv1alpha1.XeolScanner{ObjectMeta: metav1.ObjectMeta{Name: "default"}})).To(Succeed())
		inv.SetPod(types.NamespacedName{Namespace: "test", Name: "p"}, []PodImage{{Digest: "sha256:a",
			Workload: Workload{Namespace: "test", Kind: "Deployment", Name: "python38"}}})
		past := now.AddDate(-1, 0, 0)
		inv.SetResult("sha256:a", []scanner.Finding{{Product: "Perl", EOL: past, URL: "https://endoflife.date/perl"},
			{Product: "Python", Cycle: "3.8", EOL: past, URL: "https://endoflife.date/python"}}, "", now, now.Add(time.Hour))
		reconcile := func() {
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "default"}})
			Expect(err).NotTo(HaveOccurred())
		}
		reconcile()
		<-cfg.Changed
		Expect(get("default").Status.EOLFindings).To(Equal(int32(2)))

		xs := get("default")
		xs.Spec.IgnoreProducts = []xeolv1alpha1.IgnoreRule{{Products: []string{"perl"}, Namespaces: []string{"test"}}}
		Expect(k8sClient.Update(ctx, xs)).To(Succeed())
		reconcile()
		Expect(cfg.Changed).To(HaveLen(1), "a new rule counts as a config change")
		Expect(get("default").Status.EOLFindings).To(Equal(int32(1)))
		Expect(inv.Due(now)).To(BeEmpty(), "no rescan")

		xs = get("default")
		xs.Spec.IgnoreProducts[0].Selector = &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{
			{Key: "team", Operator: "Bogus"}}}
		Expect(k8sClient.Update(ctx, xs)).To(Succeed())
		reconcile()
		c := meta.FindStatusCondition(get("default").Status.Conditions, xeolv1alpha1.ConditionReady)
		Expect(c.Reason).To(Equal("InvalidSpec"))

		xs = get("default")
		xs.Spec.IgnoreProducts = []xeolv1alpha1.IgnoreRule{{}}
		Expect(k8sClient.Update(ctx, xs)).NotTo(Succeed(), "products is required")
	})

	It("ignores XeolScanners not named default", func() {
		r, cfg, _ := newReconciler(fakeDB{built: now})
		Expect(k8sClient.Create(ctx, &xeolv1alpha1.XeolScanner{ObjectMeta: metav1.ObjectMeta{Name: "other"}})).To(Succeed())
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "other"}})
		Expect(err).NotTo(HaveOccurred())
		_, ok := cfg.Get()
		Expect(ok).To(BeFalse())
		c := meta.FindStatusCondition(get("other").Status.Conditions, xeolv1alpha1.ConditionReady)
		Expect(c.Reason).To(Equal("NotSingleton"))
	})

	It("stops scanning when XeolScanner/default is deleted", func() {
		r, cfg, inv := newReconciler(fakeDB{built: now})
		cfg.Set(ConfigSnapshot{})
		inv.SetPod(types.NamespacedName{Namespace: "a", Name: "p"}, []PodImage{{Digest: "sha256:a"}})
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "default"}})
		Expect(err).NotTo(HaveOccurred())
		_, ok := cfg.Get()
		Expect(ok).To(BeFalse())
		Expect(inv.Images()).To(BeEmpty())
	})
})
