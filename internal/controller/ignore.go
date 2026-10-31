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
	"path"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/Rishang/xeol-operator/internal/config"
	"github.com/Rishang/xeol-operator/internal/scanner"
)

// scope is where an ignore rule applies: namespace globs and a workload label selector.
type scope struct {
	namespaces []string        // globs; empty = every namespace
	selector   labels.Selector // nil = every workload
}

func newScope(field string, i int, namespaces []string, sel *metav1.LabelSelector) (scope, error) {
	sc := scope{namespaces: namespaces}
	if sel != nil {
		var err error
		if sc.selector, err = metav1.LabelSelectorAsSelector(sel); err != nil {
			return scope{}, fmt.Errorf("%s[%d].selector: %w", field, i, err)
		}
	}
	return sc, nil
}

func (sc scope) global() bool { return len(sc.namespaces) == 0 && sc.selector == nil }

func (sc scope) appliesTo(w Workload) bool {
	return (len(sc.namespaces) == 0 || excluded(sc.namespaces, w.Namespace)) &&
		(sc.selector == nil || sc.selector.Matches(labels.Set(w.Labels)))
}

// rule is a compiled ignore rule: what it hides (match) and where (scope).
type rule[T any] struct {
	scope
	match func(T) bool
}

// hiddenFor reports whether x is hidden for workload w.
func hiddenFor[T any](rules []rule[T], x T, w Workload) bool {
	for _, r := range rules {
		if r.match(x) && r.appliesTo(w) {
			return true
		}
	}
	return false
}

// hiddenForImage reports whether x is hidden for an image as a whole: by a global rule, or for every
// workload running it. Metrics are per digest, so a rule scoped to some of its workloads keeps it there.
func hiddenForImage[T any](rules []rule[T], x T, workloads []Workload) bool {
	for _, r := range rules {
		if r.match(x) && r.global() {
			return true
		}
	}
	if len(workloads) == 0 {
		return false
	}
	for _, w := range workloads {
		if !hiddenFor(rules, x, w) {
			return false
		}
	}
	return true
}

func compileIgnores(specs []config.IgnoreRule) ([]rule[scanner.Finding], error) {
	rules := make([]rule[scanner.Finding], 0, len(specs))
	for i, s := range specs {
		sc, err := newScope("ignoreProducts", i, s.Namespaces, s.Selector)
		if err != nil {
			return nil, err
		}
		products, cycles := lowerSet(s.Products), lowerSet(s.Cycles)
		rules = append(rules, rule[scanner.Finding]{scope: sc, match: func(f scanner.Finding) bool {
			// Either the slug ("gorilla") or the display name ("Gorilla Toolkit"), as shown on the dashboard.
			return (products[productSlug(f)] || products[strings.ToLower(f.Product)]) &&
				(len(cycles) == 0 || cycles[strings.ToLower(f.Cycle)])
		}})
	}
	return rules, nil
}

func compileVulnIgnores(specs []config.VulnIgnoreRule) ([]rule[scanner.Vuln], error) {
	rules := make([]rule[scanner.Vuln], 0, len(specs))
	for i, s := range specs {
		sc, err := newScope("ignoreVulnerabilities", i, s.Namespaces, s.Selector)
		if err != nil {
			return nil, err
		}
		ids, pkgs := lowerSet(s.IDs), lowerSet(s.Packages)
		rules = append(rules, rule[scanner.Vuln]{scope: sc, match: func(v scanner.Vuln) bool {
			return (len(ids) == 0 || ids[strings.ToLower(v.ID)]) && (len(pkgs) == 0 || pkgs[strings.ToLower(v.Package)])
		}})
	}
	return rules, nil
}

func lowerSet(vals []string) map[string]bool {
	m := make(map[string]bool, len(vals))
	for _, v := range vals {
		m[strings.ToLower(strings.TrimSpace(v))] = true
	}
	return m
}

// productSlug is the endoflife.date slug of a finding's product: "nodejs" for https://endoflife.date/nodejs.
func productSlug(f scanner.Finding) string {
	if f.URL != "" {
		return strings.ToLower(path.Base(f.URL))
	}
	return strings.ToLower(f.Product)
}

// IgnoredFor reports whether f is hidden for workload w.
func (s ConfigSnapshot) IgnoredFor(f scanner.Finding, w Workload) bool {
	return hiddenFor(s.ignores, f, w)
}

// Ignored reports whether f is hidden for an image as a whole: by a global rule, or for every workload running it.
func (s ConfigSnapshot) Ignored(f scanner.Finding, workloads []Workload) bool {
	return hiddenForImage(s.ignores, f, workloads)
}

// VulnsFor is an image's vulnerabilities as workload w sees them: without those its rules hide.
func (s ConfigSnapshot) VulnsFor(vulns []scanner.Vuln, w Workload) []scanner.Vuln {
	return s.keepVulns(vulns, func(v scanner.Vuln) bool { return !hiddenFor(s.vulnIgnores, v, w) })
}

// ImageVulns is an image's vulnerabilities for per-digest views (metrics, image counts): without those
// hidden by a global rule or for every workload running it.
func (s ConfigSnapshot) ImageVulns(vulns []scanner.Vuln, workloads []Workload) []scanner.Vuln {
	return s.keepVulns(vulns, func(v scanner.Vuln) bool { return !hiddenForImage(s.vulnIgnores, v, workloads) })
}

// keepVulns returns vulns unchanged (no copy) when there are no rules.
func (s ConfigSnapshot) keepVulns(vulns []scanner.Vuln, keep func(scanner.Vuln) bool) []scanner.Vuln {
	if len(s.vulnIgnores) == 0 {
		return vulns
	}
	out := make([]scanner.Vuln, 0, len(vulns))
	for _, v := range vulns {
		if keep(v) {
			out = append(out, v)
		}
	}
	return out
}
