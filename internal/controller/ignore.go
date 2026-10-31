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

// ignoreRule is a compiled ignoreProducts entry of the config file.
type ignoreRule struct {
	products   map[string]bool
	cycles     map[string]bool // empty = every cycle
	namespaces []string        // globs; empty = every namespace
	selector   labels.Selector // nil = every workload
}

func compileIgnores(specs []config.IgnoreRule) ([]ignoreRule, error) {
	rules := make([]ignoreRule, 0, len(specs))
	for i, s := range specs {
		r := ignoreRule{products: lowerSet(s.Products), cycles: lowerSet(s.Cycles), namespaces: s.Namespaces}
		if s.Selector != nil {
			sel, err := metav1.LabelSelectorAsSelector(s.Selector)
			if err != nil {
				return nil, fmt.Errorf("ignoreProducts[%d].selector: %w", i, err)
			}
			r.selector = sel
		}
		rules = append(rules, r)
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

func (r ignoreRule) global() bool { return len(r.namespaces) == 0 && r.selector == nil }

func (r ignoreRule) matches(f scanner.Finding) bool {
	// Either the slug ("gorilla") or the display name ("Gorilla Toolkit"), as shown on the dashboard.
	return (r.products[productSlug(f)] || r.products[strings.ToLower(f.Product)]) && (len(r.cycles) == 0 || r.cycles[strings.ToLower(f.Cycle)])
}

func (r ignoreRule) appliesTo(w Workload) bool {
	return (len(r.namespaces) == 0 || excluded(r.namespaces, w.Namespace)) &&
		(r.selector == nil || r.selector.Matches(labels.Set(w.Labels)))
}

// IgnoredFor reports whether f is hidden for workload w.
func (s ConfigSnapshot) IgnoredFor(f scanner.Finding, w Workload) bool {
	for _, r := range s.ignores {
		if r.matches(f) && r.appliesTo(w) {
			return true
		}
	}
	return false
}

// Ignored reports whether f is hidden for an image as a whole: by a global rule, or for every workload running it.
func (s ConfigSnapshot) Ignored(f scanner.Finding, workloads []Workload) bool {
	for _, r := range s.ignores {
		if r.matches(f) && r.global() {
			return true
		}
	}
	if len(workloads) == 0 {
		return false
	}
	for _, w := range workloads {
		if !s.IgnoredFor(f, w) {
			return false
		}
	}
	return true
}
