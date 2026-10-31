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

package results

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Rishang/xeol-operator/internal/scanner"
)

func TestSaveLoadDeleteSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	eol := time.Date(2024, 10, 7, 0, 0, 0, 0, time.UTC)
	rec := Record{Scanned: time.Unix(1000, 0), Next: time.Unix(2000, 0),
		Findings: []scanner.Finding{{Product: "Python", Cycle: "3.8", EOL: eol, URL: "https://endoflife.date/python"}}}
	if err := db.Save("sha256:a", rec); err != nil {
		t.Fatal(err)
	}
	rec.ErrReason = "auth" // overwrite keeps one row per digest
	if err := db.Save("sha256:a", rec); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	// A restart reopens the same file.
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	got, ok, err := db.Load("sha256:a")
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	if got.ErrReason != "auth" || !got.Next.Equal(rec.Next) || len(got.Findings) != 1 ||
		!got.Findings[0].EOL.Equal(eol) || got.Findings[0].URL != rec.Findings[0].URL {
		t.Fatalf("got %+v", got)
	}
	if _, ok, _ := db.Load("sha256:missing"); ok {
		t.Fatal("missing digest found")
	}
	// A save prunes records more than a week overdue, e.g. images that stopped running.
	later := rec
	later.Scanned = rec.Next.Add(8 * 24 * time.Hour)
	if err := db.Save("sha256:b", later); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := db.Load("sha256:a"); ok {
		t.Fatal("stale record not pruned")
	}
}

func TestVulnsRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "results.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	vulns := []scanner.Vuln{{ID: "CVE-2023-5363", Severity: "HIGH", Package: "libcrypto3", Version: "3.1.2-r0", FixedVersion: "3.1.4-r0"}}
	for digest, v := range map[string][]scanner.Vuln{"sha256:v": vulns, "sha256:empty": {}, "sha256:never": nil} {
		if err := db.Save(digest, Record{Scanned: time.Unix(1000, 0), Next: time.Unix(2000, 0), Vulns: v}); err != nil {
			t.Fatal(err)
		}
	}
	got, _, err := db.Load("sha256:v")
	if err != nil || len(got.Vulns) != 1 || got.Vulns[0] != vulns[0] {
		t.Fatalf("got %+v, err %v", got.Vulns, err)
	}
	// Scanned with nothing found and never scanned with trivy stay distinct.
	if got, _, _ := db.Load("sha256:empty"); got.Vulns == nil {
		t.Error("empty vulns loaded as nil")
	}
	if got, _, _ := db.Load("sha256:never"); got.Vulns != nil {
		t.Errorf("never-scanned vulns loaded as %#v", got.Vulns)
	}
}
