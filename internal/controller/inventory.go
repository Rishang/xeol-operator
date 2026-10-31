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
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/types"

	"github.com/Rishang/xeol-operator/internal/scanner"
)

// Workload is the top-level owner of a Pod (Deployment, CronJob, ... or the Pod itself).
type Workload struct {
	Namespace string
	Kind      string
	Name      string
	Labels    map[string]string
}

// PodImage is one running container image as seen in a Pod's status.
type PodImage struct {
	Digest      string // sha256:...
	Repo        string // docker.io/library/python
	Ref         string // the image as written in the Pod spec, for display
	Platform    string // linux/amd64
	Workload    Workload
	PullSecrets []types.NamespacedName
}

// Image is a snapshot of one digest: who runs it and its last scan.
type Image struct {
	Digest      string
	Repo        string
	Ref         string
	Platform    string
	Workloads   []Workload
	PullSecrets []types.NamespacedName
	Scanned     time.Time
	Next        time.Time
	Findings    []scanner.Finding
	// Vulns is nil until the digest is scanned with trivy.
	Vulns     []scanner.Vuln
	ErrReason string
}

type entry struct {
	pods          map[types.NamespacedName]PodImage
	scanned, next time.Time
	findings      []scanner.Finding
	vulns         []scanner.Vuln
	errReason     string
}

// Inventory is the in-memory set of running digests and their scan results.
// ponytail: one global lock; split per digest if scrape latency shows contention.
type Inventory struct {
	mu     sync.RWMutex
	images map[string]*entry
	pods   map[types.NamespacedName][]string
}

// NewInventory returns an empty inventory.
func NewInventory() *Inventory {
	return &Inventory{images: map[string]*entry{}, pods: map[types.NamespacedName][]string{}}
}

// SetPod replaces a Pod's images and returns digests new to the inventory and digests no Pod runs any more.
func (inv *Inventory) SetPod(pod types.NamespacedName, imgs []PodImage) (added, removed []string) {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	old := inv.pods[pod]
	// Detach the Pod without dropping its digests yet, so an unchanged Pod keeps its scan results.
	for _, d := range old {
		if e, ok := inv.images[d]; ok {
			delete(e.pods, pod)
		}
	}
	defer func() {
		for _, d := range old {
			if e, ok := inv.images[d]; ok && len(e.pods) == 0 {
				delete(inv.images, d)
				removed = append(removed, d)
			}
		}
	}()
	digests := make([]string, 0, len(imgs))
	for _, img := range imgs {
		e, ok := inv.images[img.Digest]
		if !ok {
			e = &entry{pods: map[types.NamespacedName]PodImage{}}
			inv.images[img.Digest] = e
			added = append(added, img.Digest)
		}
		e.pods[pod] = img
		digests = append(digests, img.Digest)
	}
	if len(digests) > 0 {
		inv.pods[pod] = digests
	} else {
		delete(inv.pods, pod)
	}
	return added, removed
}

// DeletePod removes a Pod and returns digests no Pod runs any more; they are dropped with their results.
func (inv *Inventory) DeletePod(pod types.NamespacedName) (removed []string) {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	for _, d := range inv.pods[pod] {
		if e, ok := inv.images[d]; ok {
			delete(e.pods, pod)
			if len(e.pods) == 0 {
				delete(inv.images, d)
				removed = append(removed, d)
			}
		}
	}
	delete(inv.pods, pod)
	return removed
}

// Get returns one digest's snapshot.
func (inv *Inventory) Get(digest string) (Image, bool) {
	inv.mu.RLock()
	defer inv.mu.RUnlock()
	e, ok := inv.images[digest]
	if !ok {
		return Image{}, false
	}
	return snapshot(digest, e), true
}

// Images returns a snapshot of every digest, sorted by digest.
func (inv *Inventory) Images() []Image {
	inv.mu.RLock()
	defer inv.mu.RUnlock()
	out := make([]Image, 0, len(inv.images))
	for d, e := range inv.images {
		out = append(out, snapshot(d, e))
	}
	slices.SortFunc(out, func(a, b Image) int { return strings.Compare(a.Digest, b.Digest) })
	return out
}

// Match returns the running images whose ref (as written in the Pod spec) or repository matches any
// glob, with excludeImages' glob syntax; every image when globs is empty. The globs are compiled per
// call, not cached: they come from rescan requests, not the config file.
func (inv *Inventory) Match(globs []string) []Image {
	res := make([]*regexp.Regexp, 0, len(globs))
	for _, g := range globs {
		res = append(res, globRegexp(g))
	}
	var out []Image
	for _, img := range inv.Images() {
		if len(res) == 0 || slices.ContainsFunc(res, func(re *regexp.Regexp) bool {
			return re.MatchString(img.Ref) || re.MatchString(img.Repo)
		}) {
			out = append(out, img)
		}
	}
	return out
}

func snapshot(digest string, e *entry) Image {
	img := Image{Digest: digest, Scanned: e.scanned, Next: e.next, Findings: e.findings, Vulns: e.vulns, ErrReason: e.errReason}
	seenW := map[string]bool{}
	seenS := map[types.NamespacedName]bool{}
	var first string
	for key, p := range e.pods {
		// Repo, ref and platform come from a Pod still running the digest (the lowest key, so it is
		// stable), not the first one ever seen: that Pod's repo may be gone along with its pull secret.
		if k := key.String(); first == "" || k < first {
			first, img.Repo, img.Ref, img.Platform = k, p.Repo, p.Ref, p.Platform
		}
		k := p.Workload.Namespace + "/" + p.Workload.Kind + "/" + p.Workload.Name
		if !seenW[k] {
			seenW[k] = true
			img.Workloads = append(img.Workloads, p.Workload)
		}
		for _, s := range p.PullSecrets {
			if !seenS[s] {
				seenS[s] = true
				img.PullSecrets = append(img.PullSecrets, s)
			}
		}
	}
	slices.SortFunc(img.Workloads, func(a, b Workload) int {
		return strings.Compare(a.Namespace+"/"+a.Kind+"/"+a.Name, b.Namespace+"/"+b.Kind+"/"+b.Name)
	})
	return img
}

// SetResult stores a scan result and when to scan next. A failure without findings (a failed scan)
// keeps the previous ones; a restored record brings its own. Nil vulns (trivy off, or its step
// failed) keep the previous ones too.
func (inv *Inventory) SetResult(digest string, r scanner.Result, errReason string, at, next time.Time) {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	e, ok := inv.images[digest]
	if !ok {
		return // stopped running while being scanned
	}
	e.scanned, e.next, e.errReason = at, next, errReason
	if errReason == "" || r.Findings != nil {
		e.findings = r.Findings
	}
	if r.Vulns != nil {
		e.vulns = r.Vulns
	}
}

// Due returns digests never scanned or past their next scan time.
func (inv *Inventory) Due(now time.Time) []string {
	inv.mu.RLock()
	defer inv.mu.RUnlock()
	var due []string
	for d, e := range inv.images {
		if e.next.IsZero() || !now.Before(e.next) {
			due = append(due, d)
		}
	}
	slices.Sort(due)
	return due
}

// Rescan queues every running image matching globs (all of them with none) for a scan now and returns
// their refs. The scan queue dedupes, so a digest already queued or being scanned is not scanned twice.
func (inv *Inventory) Rescan(globs []string, enqueue func(digest string)) []string {
	imgs := inv.Match(globs)
	refs := make([]string, 0, len(imgs))
	for _, img := range imgs {
		enqueue(img.Digest)
		refs = append(refs, img.Ref)
	}
	return refs
}
