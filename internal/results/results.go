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

// Package results persists each image digest's last scan result in a local SQLite file, so an
// operator restart serves the last results immediately instead of rescanning every image.
package results

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	_ "modernc.org/sqlite" // pure Go: keeps the image cgo-free

	"github.com/Rishang/xeol-operator/internal/scanner"
)

// Record is one digest's last scan.
type Record struct {
	Scanned   time.Time
	Next      time.Time
	ErrReason string
	Findings  []scanner.Finding
	// Vulns is nil if the digest was never scanned with trivy.
	Vulns []scanner.Vuln
}

// retention is how long past its next scan a record is kept for an image that stopped running.
const retention = 7 * 24 * time.Hour

// DB is the results store. Safe for concurrent use.
type DB struct{ db *sql.DB }

// Open opens (or creates) the SQLite file at path.
func Open(path string) (*DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // ponytail: one writer; scans are minutes apart, contention is irrelevant
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS results (
		digest     TEXT PRIMARY KEY,
		scanned_at INTEGER NOT NULL,
		next_at    INTEGER NOT NULL,
		err_reason TEXT NOT NULL,
		findings   TEXT NOT NULL,
		vulns      TEXT NOT NULL -- JSON; null if never scanned with trivy
	);
	CREATE INDEX IF NOT EXISTS results_next_at ON results(next_at)`); err != nil { // for the prune in Save
		_ = db.Close()
		return nil, err
	}
	return &DB{db: db}, nil
}

// Close closes the file.
func (d *DB) Close() error { return d.db.Close() }

// Save replaces a digest's record.
func (d *DB) Save(digest string, r Record) error {
	f, err := json.Marshal(r.Findings)
	if err != nil {
		return err
	}
	v, err := json.Marshal(r.Vulns)
	if err != nil {
		return err
	}
	// ponytail: prune on write; rows of images that stopped running (CronJob images between runs
	// included) stay until they are a week overdue, so a recurring image is restored, not rescanned.
	if _, err := d.db.Exec(`DELETE FROM results WHERE next_at < ?`, r.Scanned.Add(-retention).Unix()); err != nil {
		return err
	}
	_, err = d.db.Exec(`INSERT INTO results(digest, scanned_at, next_at, err_reason, findings, vulns) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(digest) DO UPDATE SET scanned_at = excluded.scanned_at, next_at = excluded.next_at,
		err_reason = excluded.err_reason, findings = excluded.findings, vulns = excluded.vulns`,
		digest, r.Scanned.Unix(), r.Next.Unix(), r.ErrReason, string(f), string(v))
	return err
}

// Load returns a digest's record, or false if it was never saved.
func (d *DB) Load(digest string) (Record, bool, error) {
	var scanned, next int64
	var reason, f, v string
	err := d.db.QueryRow(`SELECT scanned_at, next_at, err_reason, findings, vulns FROM results WHERE digest = ?`, digest).
		Scan(&scanned, &next, &reason, &f, &v)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, err
	}
	r := Record{Scanned: time.Unix(scanned, 0), Next: time.Unix(next, 0), ErrReason: reason}
	if err := json.Unmarshal([]byte(f), &r.Findings); err != nil {
		return Record{}, false, err
	}
	if err := json.Unmarshal([]byte(v), &r.Vulns); err != nil {
		return Record{}, false, err
	}
	return r, true, nil
}
