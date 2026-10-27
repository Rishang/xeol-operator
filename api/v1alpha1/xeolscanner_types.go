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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// DefaultName is the only XeolScanner the operator acts on.
const DefaultName = "default"

// Condition types.
const (
	ConditionReady   = "Ready"
	ConditionDBStale = "DBStale"
)

// XeolScannerSpec configures which running images are scanned and how results are labelled.
// Pod-level settings (scan workers, EOL database source) are operator flags set by the Helm chart.
type XeolScannerSpec struct {
	// namespaceSelector limits scanning to namespaces with matching labels. Empty selects all.
	// +optional
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty"`

	// excludeNamespaces are namespace name globs to skip, e.g. "kube-*". "*" matches any characters, "?" one.
	// +optional
	ExcludeNamespaces []string `json:"excludeNamespaces,omitempty"`

	// excludeImages are globs on the image as written in the Pod spec or its repository, e.g.
	// "registry.internal/*" ("*" matches any characters, "/" included; "?" one character).
	// +optional
	ExcludeImages []string `json:"excludeImages,omitempty"`

	// interval is how often each image digest is rescanned.
	// +kubebuilder:default="24h"
	// +optional
	Interval metav1.Duration `json:"interval,omitzero"`

	// workloadKinds are the top-level workload kinds to scan (a Pod's owner chain, e.g. Pod → ReplicaSet →
	// Deployment, Job → CronJob), matched exactly. Pods of other kinds (Prometheus, Alertmanager, a bare Pod) are
	// skipped. An explicit empty list scans every kind.
	// +kubebuilder:default={Deployment,StatefulSet,DaemonSet,Job,CronJob}
	// +optional
	WorkloadKinds []string `json:"workloadKinds,omitempty"`

	// workloadLabels are workload label keys copied onto metrics as k8s_label_<key>, e.g. "team".
	// +optional
	WorkloadLabels []string `json:"workloadLabels,omitempty"`

	// ignoreProducts hides accepted findings from metrics, the JSON API and status. Images are still scanned.
	// +optional
	IgnoreProducts []IgnoreRule `json:"ignoreProducts,omitempty"`
}

// IgnoreRule hides findings of some products, optionally only some cycles, namespaces or workloads.
// A rule applies to a workload only when both namespaces and selector match; with neither it applies everywhere.
type IgnoreRule struct {
	// products are endoflife.date product slugs (https://endoflife.date/<slug>, e.g. "nodejs") or display names
	// (e.g. "Node.js", "Gorilla Toolkit"). Case-insensitive.
	// +kubebuilder:validation:MinItems=1
	Products []string `json:"products"`

	// cycles limits the rule to these release cycles, e.g. "3.8". Empty means every cycle.
	// +optional
	Cycles []string `json:"cycles,omitempty"`

	// namespaces limits the rule to namespace name globs, e.g. "test". Empty means every namespace.
	// +optional
	Namespaces []string `json:"namespaces,omitempty"`

	// selector limits the rule to workloads (Deployment, DaemonSet, ...; a bare Pod) whose own labels match.
	// +optional
	Selector *metav1.LabelSelector `json:"selector,omitempty"`
}

// XeolScannerStatus reports scan progress and health.
type XeolScannerStatus struct {
	// lastSweepTime is when every known image last had a completed scan.
	// +optional
	LastSweepTime *metav1.Time `json:"lastSweepTime,omitempty"`

	// imagesScanned is the number of running image digests with a scan result.
	// +optional
	ImagesScanned int32 `json:"imagesScanned,omitempty"`

	// eolFindings is the number of EOL findings across running images.
	// +optional
	EOLFindings int32 `json:"eolFindings,omitempty"`

	// dbBuilt is when the EOL database in use was built.
	// +optional
	DBBuilt *metav1.Time `json:"dbBuilt,omitempty"`

	// conditions: Ready (operator is scanning) and DBStale (EOL database older than 30 days).
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Images",type=integer,JSONPath=`.status.imagesScanned`
// +kubebuilder:printcolumn:name="EOL",type=integer,JSONPath=`.status.eolFindings`
// +kubebuilder:printcolumn:name="DB Built",type=date,JSONPath=`.status.dbBuilt`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`

// XeolScanner is the Schema for the xeolscanners API
type XeolScanner struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec configures scanning
	// +kubebuilder:default={}
	// +optional
	Spec XeolScannerSpec `json:"spec,omitzero"`

	// status defines the observed state of XeolScanner
	// +optional
	Status XeolScannerStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// XeolScannerList contains a list of XeolScanner
type XeolScannerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []XeolScanner `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &XeolScanner{}, &XeolScannerList{})
		return nil
	})
}
