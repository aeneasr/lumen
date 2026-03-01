// Copyright 2026 Aeneas Rekkas
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build fts5

package store

import (
	"database/sql"
	"testing"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
)

// TestHybridCTE_VecAndFTS5InCTE verifies that vec0 and FTS5 MATCH clauses
// both work inside CTEs and can be combined via UNION + RRF in a single query.
func TestHybridCTE_VecAndFTS5InCTE(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close: %v", err)
		}
	}()

	// Create a minimal schema: regular chunks table + vec0 + FTS5.
	stmts := []string{
		`CREATE TABLE files (path TEXT PRIMARY KEY, hash TEXT NOT NULL)`,
		`CREATE TABLE chunks (
			id         TEXT PRIMARY KEY,
			file_path  TEXT NOT NULL,
			symbol     TEXT NOT NULL,
			kind       TEXT NOT NULL,
			start_line INTEGER NOT NULL,
			end_line   INTEGER NOT NULL
		)`,
		`CREATE VIRTUAL TABLE vec_chunks USING vec0(
			id TEXT PRIMARY KEY,
			embedding float[4] distance_metric=cosine
		)`,
		`CREATE VIRTUAL TABLE fts_chunks USING fts5(
			id UNINDEXED,
			content,
			content='',
			contentless_delete=1
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("exec %q: %v", s, err)
		}
	}

	// Insert test data.
	if _, err := db.Exec(`INSERT INTO files VALUES ('main.go', 'abc')`); err != nil {
		t.Fatal(err)
	}
	chunks := []struct {
		id, symbol, content string
		vec                 []float32
	}{
		{"c1", "ValidateToken", "func ValidateToken(tok string) bool { return tok != \"\" }", []float32{1, 0, 0, 0}},
		{"c2", "AuthenticateUser", "func AuthenticateUser(user, pass string) error { return nil }", []float32{0.9, 0.1, 0, 0}},
		{"c3", "QueryDatabase", "func QueryDatabase(q string) ([]Row, error) { return nil, nil }", []float32{0, 0, 1, 0}},
	}
	for _, c := range chunks {
		if _, err := db.Exec(
			`INSERT INTO chunks (id, file_path, symbol, kind, start_line, end_line) VALUES (?, 'main.go', ?, 'function', 1, 5)`,
			c.id, c.symbol,
		); err != nil {
			t.Fatalf("insert chunk %s: %v", c.id, err)
		}
		blob, err := sqlite_vec.SerializeFloat32(c.vec)
		if err != nil {
			t.Fatalf("serialize vec %s: %v", c.id, err)
		}
		if _, err := db.Exec(`INSERT INTO vec_chunks (id, embedding) VALUES (?, ?)`, c.id, blob); err != nil {
			t.Fatalf("insert vec %s: %v", c.id, err)
		}
		if _, err := db.Exec(`INSERT INTO fts_chunks (id, content) VALUES (?, ?)`, c.id, c.content); err != nil {
			t.Fatalf("insert fts %s: %v", c.id, err)
		}
	}

	// Run the RRF CTE query: vector query for "authenticate" direction + FTS for "ValidateToken".
	queryVec, err := sqlite_vec.SerializeFloat32([]float32{1, 0, 0, 0}) // points toward c1
	if err != nil {
		t.Fatal(err)
	}
	ftsQuery := "ValidateToken"

	const hybridSQL = `
	WITH vector_results AS (
		SELECT id, ROW_NUMBER() OVER (ORDER BY distance) AS rank
		FROM vec_chunks
		WHERE embedding MATCH ? AND k = ?
	),
	fts_results AS (
		SELECT id, ROW_NUMBER() OVER (ORDER BY bm25(fts_chunks)) AS rank
		FROM fts_chunks
		WHERE fts_chunks MATCH ?
		LIMIT ?
	),
	all_ids AS (
		SELECT id FROM vector_results
		UNION
		SELECT id FROM fts_results
	),
	rrf AS (
		SELECT
			a.id,
			COALESCE(1.0 / (60.0 + vr.rank), 0.0) +
			COALESCE(1.0 / (60.0 + fr.rank), 0.0) AS rrf_score
		FROM all_ids a
		LEFT JOIN vector_results vr ON a.id = vr.id
		LEFT JOIN fts_results fr ON a.id = fr.id
	)
	SELECT r.id, c.symbol, r.rrf_score
	FROM rrf r
	JOIN chunks c ON r.id = c.id
	ORDER BY r.rrf_score DESC
	LIMIT ?
	`

	rows, err := db.Query(hybridSQL, queryVec, 3, ftsQuery, 3, 3)
	if err != nil {
		t.Fatalf("hybrid CTE query failed: %v", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("rows.Close: %v", err)
		}
	}()

	type result struct {
		id, symbol string
		score      float64
	}
	var results []result
	for rows.Next() {
		var r result
		if err := rows.Scan(&r.id, &r.symbol, &r.score); err != nil {
			t.Fatalf("scan: %v", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows err: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected results, got none")
	}

	// c1 (ValidateToken) should rank first: it wins on both vector (distance=0) and FTS (exact match).
	if results[0].id != "c1" {
		t.Errorf("expected c1 (ValidateToken) to rank first, got %s (%s, score=%.4f)", results[0].id, results[0].symbol, results[0].score)
	}

	t.Logf("hybrid results:")
	for i, r := range results {
		t.Logf("  [%d] %s (%s) rrf=%.4f", i+1, r.symbol, r.id, r.score)
	}
}
