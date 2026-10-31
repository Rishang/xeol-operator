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
	"encoding/json"
	"maps"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Rishang/xeol-operator/internal/results"
	"github.com/Rishang/xeol-operator/internal/scanner"
)

// ResultStore persists each digest's last scan so a restart doesn't rescan everything.
type ResultStore interface {
	Load(digest string) (results.Record, bool, error)
	Save(digest string, r results.Record) error
}

// ScanStore adapts the Inventory to scanner.Store and resolves registry credentials.
type ScanStore struct {
	// Secrets must be an uncached reader (mgr.GetAPIReader()): caching would keep every Secret in the cluster in memory.
	Secrets   client.Reader
	Inventory *Inventory
	Config    *Config
	// Results is optional.
	Results ResultStore
	Log     logr.Logger
}

// +kubebuilder:rbac:groups="",resources=secrets,verbs=get

// Target resolves a digest to a scan target with merged pull-secret credentials.
func (s *ScanStore) Target(ctx context.Context, digest string) (scanner.Target, bool, error) {
	img, ok := s.Inventory.Get(digest)
	if !ok {
		return scanner.Target{}, false, nil
	}
	creds, err := dockerConfig(ctx, s.Secrets, img.PullSecrets)
	if err != nil {
		return scanner.Target{}, false, err
	}
	return scanner.Target{Ref: img.Repo + "@" + digest, Platform: img.Platform, DockerConfigJSON: creds}, true, nil
}

// SetResult stores a scan result in memory and, if configured, on disk.
func (s *ScanStore) SetResult(digest string, r scanner.Result, errReason string, at, next time.Time) {
	s.Inventory.SetResult(digest, r, errReason, at, next)
	if s.Results == nil {
		return
	}
	img, ok := s.Inventory.Get(digest) // on failure the inventory keeps the previous findings
	if !ok {
		return
	}
	rec := results.Record{Scanned: img.Scanned, Next: img.Next, ErrReason: img.ErrReason, Findings: img.Findings, Vulns: img.Vulns}
	if err := s.Results.Save(digest, rec); err != nil {
		s.Log.Error(err, "saving scan result", "digest", digest)
	}
}

// Due lists digests to scan now.
func (s *ScanStore) Due(now time.Time) []string { return s.Inventory.Due(now) }

// Interval is the configured rescan interval.
func (s *ScanStore) Interval() time.Duration {
	if cfg, ok := s.Config.Get(); ok {
		return cfg.Interval
	}
	return 24 * time.Hour
}

// dockerConfig merges pull secrets into one ~/.docker/config.json. Missing secrets are skipped;
// returns nil when there are no credentials.
func dockerConfig(ctx context.Context, c client.Reader, refs []types.NamespacedName) ([]byte, error) {
	auths := map[string]json.RawMessage{}
	for _, ref := range refs {
		var sec corev1.Secret
		if err := c.Get(ctx, ref, &sec); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return nil, err
		}
		switch sec.Type {
		case corev1.SecretTypeDockerConfigJson:
			var cfg struct {
				Auths map[string]json.RawMessage `json:"auths"`
			}
			if json.Unmarshal(sec.Data[corev1.DockerConfigJsonKey], &cfg) == nil {
				maps.Copy(auths, cfg.Auths)
			}
		case corev1.SecretTypeDockercfg:
			var legacy map[string]json.RawMessage
			if json.Unmarshal(sec.Data[corev1.DockerConfigKey], &legacy) == nil {
				maps.Copy(auths, legacy)
			}
		}
	}
	if len(auths) == 0 {
		return nil, nil
	}
	return json.Marshal(map[string]any{"auths": auths})
}
