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

// Package eoldb builds xeol's EOL database (schema 1) from endoflife.date and manages it at runtime.
package eoldb

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure Go: keeps the image cgo-free

	"github.com/Rishang/xeol-operator/internal/dbloop"
)

// DefaultURL is endoflife.date's full product dump: every product, release and identifier.
const DefaultURL = "https://endoflife.date/api/v1/products/full"

// schemaDir is xeol's DB schema version; xeol reads <cache dir>/1/xeol.db.
const schemaDir = "1"

// schema mirrors xeol v0.10.8's DB exactly; xeol queries these tables directly.
const schema = `
CREATE TABLE products (id integer PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, permalink TEXT NOT NULL);
CREATE TABLE cycles (id integer PRIMARY KEY AUTOINCREMENT, lts boolean, release_cycle TEXT NOT NULL, eol date, eol_bool boolean,
  latest_release TEXT, latest_release_date date, release_date date, support boolean, product_id integer NOT NULL
  CHECK (eol IS NOT NULL OR eol_bool IS NOT NULL));
CREATE TABLE vulns (id integer PRIMARY KEY AUTOINCREMENT, version TEXT NOT NULL, issue_count integer NOT NULL, issues TEXT NOT NULL, product_id integer NOT NULL);
CREATE TABLE purls (id integer PRIMARY KEY AUTOINCREMENT, purl TEXT NOT NULL, product_id integer NOT NULL);
CREATE TABLE cpes (id integer PRIMARY KEY AUTOINCREMENT, cpe TEXT NOT NULL, product_id integer NOT NULL);
CREATE TABLE id (build_timestamp TEXT NOT NULL, schema_version INTEGER NOT NULL);
CREATE INDEX idx_purls_product ON purls(purl, product_id);
CREATE INDEX idx_cpes_product ON cpes(cpe, product_id);
CREATE INDEX idx_cycles_product ON cycles(product_id);
CREATE INDEX idx_vulns_product ON vulns(product_id);
`

type productsFull struct {
	SchemaVersion string `json:"schema_version"`
	Result        []struct {
		Name        string `json:"name"`
		Label       string `json:"label"`
		Identifiers []struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		} `json:"identifiers"`
		Releases []struct {
			Name         string  `json:"name"`
			ReleaseDate  *string `json:"releaseDate"`
			IsLts        *bool   `json:"isLts"`
			IsEol        *bool   `json:"isEol"`
			EolFrom      *string `json:"eolFrom"`
			IsMaintained *bool   `json:"isMaintained"`
			Latest       *struct {
				Name string  `json:"name"`
				Date *string `json:"date"`
			} `json:"latest"`
		} `json:"releases"`
	} `json:"result"`
}

type metadata struct {
	Version  int       `json:"version"`
	Built    time.Time `json:"built"`
	Checksum string    `json:"checksum"`
}

// Fetch downloads the endoflife.date product dump.
func Fetch(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// Build writes <dir>/1/xeol.db and <dir>/1/metadata.json from an endoflife.date product dump.
// It builds in a temp dir and swaps it in, so an existing DB is untouched on any error.
func Build(data []byte, dir string, now time.Time) error {
	var src productsFull
	if err := json.Unmarshal(data, &src); err != nil {
		return fmt.Errorf("parsing endoflife.date data: %w", err)
	}
	if !strings.HasPrefix(src.SchemaVersion, "1.") || len(src.Result) == 0 {
		return fmt.Errorf("unexpected endoflife.date data: schema %q, %d products", src.SchemaVersion, len(src.Result))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(dir, ".build-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	dbPath := filepath.Join(tmp, "xeol.db")
	if err := writeDB(dbPath, &src, now); err != nil {
		return err
	}
	b, err := os.ReadFile(dbPath)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(b)
	meta, err := json.Marshal(metadata{Version: 1, Built: now.UTC(), Checksum: "sha256:" + hex.EncodeToString(sum[:])})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, "metadata.json"), meta, 0o644); err != nil {
		return err
	}
	return dbloop.Swap(tmp, filepath.Join(dir, schemaDir))
}

func writeDB(path string, src *productsFull, now time.Time) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, p := range src.Result {
		res, err := tx.Exec(`INSERT INTO products(name, permalink) VALUES (?, ?)`, p.Label, "https://endoflife.date/"+p.Name)
		if err != nil {
			return err
		}
		pid, err := res.LastInsertId()
		if err != nil {
			return err
		}
		for _, r := range p.Releases {
			// xeol requires an EOL date or flag; releases with neither carry no EOL information.
			if r.EolFrom == nil && r.IsEol == nil {
				continue
			}
			var eolBool *bool
			if r.EolFrom == nil {
				eolBool = r.IsEol
			}
			var latest, latestDate *string
			if r.Latest != nil {
				latest, latestDate = &r.Latest.Name, r.Latest.Date
			}
			if _, err := tx.Exec(`INSERT INTO cycles(lts, release_cycle, eol, eol_bool, latest_release, latest_release_date, release_date, support, product_id)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				r.IsLts, r.Name, r.EolFrom, eolBool, latest, latestDate, r.ReleaseDate, r.IsMaintained, pid); err != nil {
				return err
			}
		}
		for _, id := range p.Identifiers {
			switch id.Type {
			case "purl":
				_, err = tx.Exec(`INSERT INTO purls(purl, product_id) VALUES (?, ?)`, id.ID, pid)
			case "cpe":
				_, err = tx.Exec(`INSERT INTO cpes(cpe, product_id) VALUES (?, ?)`, id.ID, pid)
			}
			if err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec(`INSERT INTO id(build_timestamp, schema_version) VALUES (?, 1)`, now.UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	return tx.Commit()
}

// Built returns the build time recorded in <dir>/1/metadata.json.
func Built(dir string) (time.Time, error) {
	b, err := os.ReadFile(filepath.Join(dir, schemaDir, "metadata.json"))
	if err != nil {
		return time.Time{}, err
	}
	var m metadata
	if err := json.Unmarshal(b, &m); err != nil {
		return time.Time{}, err
	}
	return m.Built, nil
}
