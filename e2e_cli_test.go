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

//go:build e2e

package main

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/aeneasr/agent-index/internal/config"
	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	_ "github.com/mattn/go-sqlite3"
)

// runCLI runs the agent-index binary with the given args and returns stdout, stderr, and any error.
func runCLI(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	dataHome := t.TempDir()
	return runCLIWithDataHome(t, dataHome, args...)
}

// runCLIWithDataHome runs the agent-index binary using a specific XDG_DATA_HOME.
func runCLIWithDataHome(t *testing.T, dataHome string, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	ollamaHost := os.Getenv("OLLAMA_HOST")
	if ollamaHost == "" {
		ollamaHost = "http://localhost:11434"
	}

	cmd := exec.Command(serverBinary, args...)
	cmd.Env = []string{
		"OLLAMA_HOST=" + ollamaHost,
		"AGENT_INDEX_EMBED_MODEL=all-minilm",
		"XDG_DATA_HOME=" + dataHome,
		"HOME=" + os.Getenv("HOME"),
		"PATH=" + os.Getenv("PATH"),
	}

	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err = cmd.Run()
	return outBuf.String(), errBuf.String(), err
}

func TestE2E_CLI_IndexAndSearch(t *testing.T) {
	t.Skip("search is now MCP-only, CLI command removed")

	projectPath := sampleProjectPath(t)
	dataHome := t.TempDir()

	// Index the sample project.
	stdout, stderr, err := runCLIWithDataHome(t, dataHome, "index", projectPath)
	if err != nil {
		t.Fatalf("index failed: %v\nstderr: %s", err, stderr)
	}

	// Stderr should show progress.
	if !strings.Contains(stderr, "Indexing") {
		t.Errorf("expected stderr to contain 'Indexing', got: %s", stderr)
	}

	// Stdout should show completion stats.
	if !strings.Contains(stdout, "Done.") {
		t.Errorf("expected stdout to contain 'Done.', got: %s", stdout)
	}
	if !strings.Contains(stdout, "5 files") {
		t.Errorf("expected stdout to mention '5 files', got: %s", stdout)
	}

	// Search for something semantically relevant.
	stdout, stderr, err = runCLIWithDataHome(t, dataHome, "search", "authentication token validation", projectPath)
	if err != nil {
		t.Fatalf("search failed: %v\nstderr: %s", err, stderr)
	}

	// Should contain result headers with ── delimiters.
	if !strings.Contains(stdout, "──") {
		t.Fatalf("expected search output to contain '──' result headers, got stdout=%q stderr=%q", stdout, stderr)
	}
	if t.Failed() {
		return
	}

	// ValidateToken should appear in results.
	if !strings.Contains(stdout, "ValidateToken") {
		t.Errorf("expected ValidateToken in search results, got: %s", stdout)
	}

	// Should contain actual code snippets.
	if !strings.Contains(stdout, "func ") {
		t.Errorf("expected code snippets with 'func ' in output, got: %s", stdout)
	}

	// Should start with "Found N results".
	if !strings.Contains(stdout, "Found") || !strings.Contains(stdout, "results") {
		t.Errorf("expected 'Found N results' header, got: %s", stdout[:min(len(stdout), 200)])
	}

	// Parse result headers to extract scores.
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	headerRe := regexp.MustCompile(`^── .+:(\d+)-(\d+)\s+.+?\s+\(\w+\)\s+\[(-?\d+\.\d+)\] ──$`)
	var scores []float64
	for _, line := range lines {
		m := headerRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		score, _ := strconv.ParseFloat(m[3], 64)
		scores = append(scores, score)

		startLine, _ := strconv.Atoi(m[1])
		endLine, _ := strconv.Atoi(m[2])
		if startLine <= 0 {
			t.Errorf("start line should be > 0: %s", line)
		}
		if endLine < startLine {
			t.Errorf("end line should be >= start line: %s", line)
		}
		if score <= 0 || score > 1 {
			t.Errorf("score should be in (0, 1]: %s", line)
		}
	}

	if len(scores) == 0 {
		t.Fatal("no result headers found in search output")
	}

	// Scores should be descending.
	for i := 1; i < len(scores); i++ {
		if scores[i] > scores[i-1] {
			t.Errorf("scores not descending: %f > %f", scores[i], scores[i-1])
		}
	}
}

func TestE2E_CLI_SearchLimit(t *testing.T) {
	t.Skip("search is now MCP-only, CLI command removed")

	projectPath := sampleProjectPath(t)
	dataHome := t.TempDir()

	// Index first.
	_, stderr, err := runCLIWithDataHome(t, dataHome, "index", projectPath)
	if err != nil {
		t.Fatalf("index failed: %v\nstderr: %s", err, stderr)
	}

	// Search with limit=2. Use --min-score -1 to bypass score filtering (testing limit behavior).
	stdout, _, err := runCLIWithDataHome(t, dataHome, "search", "--limit", "2", "--min-score", "-1", "user", projectPath)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}

	headerRe := regexp.MustCompile(`^── .+ ──$`)
	var count int
	for _, line := range strings.Split(stdout, "\n") {
		if headerRe.MatchString(line) {
			count++
		}
	}
	if count != 2 {
		t.Errorf("expected 2 results with --limit 2, got %d\noutput: %s", count, stdout)
	}
}

func TestE2E_CLI_SearchNoIndex(t *testing.T) {
	t.Skip("search is now MCP-only, CLI command removed")

	tmpDir := t.TempDir()

	// Search without indexing first — should fail with helpful error.
	_, _, err := runCLI(t, "search", "anything", tmpDir)
	if err == nil {
		t.Fatal("expected error when searching without an index")
	}
}

func TestE2E_CLI_IndexIdempotent(t *testing.T) {
	projectPath := sampleProjectPath(t)
	dataHome := t.TempDir()

	// First index.
	_, _, err := runCLIWithDataHome(t, dataHome, "index", projectPath)
	if err != nil {
		t.Fatalf("first index failed: %v", err)
	}

	// Second index (no changes) should say up to date.
	stdout, _, err := runCLIWithDataHome(t, dataHome, "index", projectPath)
	if err != nil {
		t.Fatalf("second index failed: %v", err)
	}
	if !strings.Contains(stdout, "up to date") {
		t.Errorf("expected 'up to date' on second index, got: %s", stdout)
	}
}

func TestE2E_CLI_IndexForceReindex(t *testing.T) {
	projectPath := sampleProjectPath(t)
	dataHome := t.TempDir()

	// First index.
	_, _, err := runCLIWithDataHome(t, dataHome, "index", projectPath)
	if err != nil {
		t.Fatalf("first index failed: %v", err)
	}

	// Force reindex should re-index even with no changes.
	stdout, _, err := runCLIWithDataHome(t, dataHome, "index", "--force", projectPath)
	if err != nil {
		t.Fatalf("force index failed: %v", err)
	}
	if !strings.Contains(stdout, "Done.") {
		t.Errorf("expected 'Done.' on force reindex, got: %s", stdout)
	}
	if strings.Contains(stdout, "up to date") {
		t.Error("force reindex should not say 'up to date'")
	}
}

// openIndexDB opens the SQLite index database for a given project path and dataHome.
func openIndexDB(t *testing.T, dataHome, projectPath string) *sql.DB {
	t.Helper()
	sqlite_vec.Auto()

	// Temporarily override XDG_DATA_HOME so DBPathForProject computes the
	// correct path inside our test data home.
	orig := os.Getenv("XDG_DATA_HOME")
	os.Setenv("XDG_DATA_HOME", dataHome)
	dbPath := config.DBPathForProject(projectPath, "all-minilm")
	os.Setenv("XDG_DATA_HOME", orig)

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open index db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestE2E_CLI_SQLVerifySchema(t *testing.T) {
	projectPath := sampleProjectPath(t)
	dataHome := t.TempDir()

	// Index first.
	_, stderr, err := runCLIWithDataHome(t, dataHome, "index", projectPath)
	if err != nil {
		t.Fatalf("index failed: %v\nstderr: %s", err, stderr)
	}

	db := openIndexDB(t, dataHome, projectPath)

	// --- Verify tables exist ---
	expectedTables := []string{"files", "chunks", "project_meta", "vec_chunks"}
	for _, table := range expectedTables {
		var count int
		err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name = ?", table).Scan(&count)
		if err != nil {
			t.Fatalf("query sqlite_master for %s: %v", table, err)
		}
		if count == 0 {
			t.Errorf("expected table %q to exist", table)
		}
	}

	// --- Verify files table ---
	var fileCount int
	if err := db.QueryRow("SELECT count(*) FROM files").Scan(&fileCount); err != nil {
		t.Fatalf("count files: %v", err)
	}
	if fileCount != 5 {
		t.Errorf("expected 5 files, got %d", fileCount)
	}

	// All file paths should end in .go and have valid hashes.
	rows, err := db.Query("SELECT path, hash FROM files ORDER BY path")
	if err != nil {
		t.Fatalf("query files: %v", err)
	}
	defer rows.Close()
	var filePaths []string
	for rows.Next() {
		var path, hash string
		if err := rows.Scan(&path, &hash); err != nil {
			t.Fatalf("scan file row: %v", err)
		}
		filePaths = append(filePaths, path)
		if !strings.HasSuffix(path, ".go") {
			t.Errorf("file path should end in .go: %s", path)
		}
		if len(hash) != 64 { // SHA-256 hex
			t.Errorf("file hash should be 64 hex chars, got %d: %s", len(hash), hash)
		}
	}

	// Check expected filenames are present.
	joined := strings.Join(filePaths, "\n")
	for _, name := range []string{"auth.go", "config.go", "database.go", "handler.go", "models.go"} {
		if !strings.Contains(joined, name) {
			t.Errorf("expected %s in file paths", name)
		}
	}

	// --- Verify chunks table ---
	var chunkCount int
	if err := db.QueryRow("SELECT count(*) FROM chunks").Scan(&chunkCount); err != nil {
		t.Fatalf("count chunks: %v", err)
	}
	if chunkCount < 15 {
		t.Errorf("expected at least 15 chunks (fixture has ~20), got %d", chunkCount)
	}

	// Every chunk should reference a valid file and have sensible fields.
	chunkRows, err := db.Query(`
		SELECT c.id, c.file_path, c.symbol, c.kind, c.start_line, c.end_line
		FROM chunks c
		JOIN files f ON c.file_path = f.path
		ORDER BY c.file_path, c.start_line
	`)
	if err != nil {
		t.Fatalf("query chunks: %v", err)
	}
	defer chunkRows.Close()

	kindCounts := make(map[string]int)
	for chunkRows.Next() {
		var id, filePath, symbol, kind string
		var startLine, endLine int
		if err := chunkRows.Scan(&id, &filePath, &symbol, &kind, &startLine, &endLine); err != nil {
			t.Fatalf("scan chunk row: %v", err)
		}
		if id == "" {
			t.Error("chunk id should not be empty")
		}
		if symbol == "" {
			t.Error("chunk symbol should not be empty")
		}
		if !validChunkKinds[kind] {
			t.Errorf("invalid chunk kind %q for symbol %s", kind, symbol)
		}
		if startLine <= 0 {
			t.Errorf("chunk %s: start_line should be > 0, got %d", symbol, startLine)
		}
		if endLine < startLine {
			t.Errorf("chunk %s: end_line (%d) < start_line (%d)", symbol, endLine, startLine)
		}
		kindCounts[kind]++
	}

	// Should have a mix of kinds.
	if kindCounts["function"] == 0 {
		t.Error("expected at least one function chunk")
	}
	if kindCounts["type"] == 0 {
		t.Error("expected at least one type chunk")
	}
	if kindCounts["interface"] == 0 {
		t.Error("expected at least one interface chunk")
	}

	// --- Verify vec_chunks has same count as chunks ---
	var vecCount int
	if err := db.QueryRow("SELECT count(*) FROM vec_chunks").Scan(&vecCount); err != nil {
		t.Fatalf("count vec_chunks: %v", err)
	}
	if vecCount != chunkCount {
		t.Errorf("vec_chunks count (%d) should match chunks count (%d)", vecCount, chunkCount)
	}

	// --- Verify project_meta ---
	meta := make(map[string]string)
	metaRows, err := db.Query("SELECT key, value FROM project_meta")
	if err != nil {
		t.Fatalf("query project_meta: %v", err)
	}
	defer metaRows.Close()
	for metaRows.Next() {
		var key, value string
		if err := metaRows.Scan(&key, &value); err != nil {
			t.Fatalf("scan meta row: %v", err)
		}
		meta[key] = value
	}

	// root_hash should be a SHA-256 hex string.
	if rh, ok := meta["root_hash"]; !ok {
		t.Error("project_meta missing root_hash")
	} else if len(rh) != 64 {
		t.Errorf("root_hash should be 64 hex chars, got %d", len(rh))
	}

	// vec_dimensions should be "384" (all-minilm).
	if vd, ok := meta["vec_dimensions"]; !ok {
		t.Error("project_meta missing vec_dimensions")
	} else if vd != "384" {
		t.Errorf("expected vec_dimensions=384, got %s", vd)
	}

	// embedding_model should be "all-minilm".
	if em, ok := meta["embedding_model"]; !ok {
		t.Error("project_meta missing embedding_model")
	} else if em != "all-minilm" {
		t.Errorf("expected embedding_model=all-minilm, got %s", em)
	}

	// --- Verify no orphan chunks (chunks without matching files) ---
	var orphans int
	if err := db.QueryRow(`
		SELECT count(*) FROM chunks c
		LEFT JOIN files f ON c.file_path = f.path
		WHERE f.path IS NULL
	`).Scan(&orphans); err != nil {
		t.Fatalf("query orphan chunks: %v", err)
	}
	if orphans != 0 {
		t.Errorf("found %d orphan chunks (no matching file)", orphans)
	}

	// --- Verify specific known symbols exist ---
	knownSymbols := map[string]string{
		"ValidateToken":  "function",
		"HandleHealth":   "function",
		"QueryUsers":     "function",
		"User":           "type",
		"UserRepository": "interface",
	}
	for symbol, expectedKind := range knownSymbols {
		var kind string
		err := db.QueryRow("SELECT kind FROM chunks WHERE symbol = ?", symbol).Scan(&kind)
		if err == sql.ErrNoRows {
			t.Errorf("expected symbol %q to exist in chunks", symbol)
		} else if err != nil {
			t.Fatalf("query symbol %s: %v", symbol, err)
		} else if kind != expectedKind {
			t.Errorf("symbol %s: expected kind=%s, got %s", symbol, expectedKind, kind)
		}
	}
}

func TestE2E_CLI_SQLVerifyKNN(t *testing.T) {
	projectPath := sampleProjectPath(t)
	dataHome := t.TempDir()

	// Index the project.
	_, stderr, err := runCLIWithDataHome(t, dataHome, "index", projectPath)
	if err != nil {
		t.Fatalf("index failed: %v\nstderr: %s", err, stderr)
	}

	db := openIndexDB(t, dataHome, projectPath)

	// Grab the embedding vector of ValidateToken from vec_chunks.
	var tokenID string
	if err := db.QueryRow("SELECT id FROM chunks WHERE symbol = 'ValidateToken'").Scan(&tokenID); err != nil {
		t.Fatalf("get ValidateToken id: %v", err)
	}

	// Use ValidateToken's own vector as the query vector — it should be
	// the top result (distance ≈ 0, score ≈ 1).
	var vecBlob []byte
	if err := db.QueryRow("SELECT embedding FROM vec_chunks WHERE id = ?", tokenID).Scan(&vecBlob); err != nil {
		t.Fatalf("get ValidateToken embedding: %v", err)
	}

	// Run raw KNN query.
	rows, err := db.Query(`
		SELECT c.symbol, c.kind, v.distance
		FROM vec_chunks v
		JOIN chunks c ON v.id = c.id
		WHERE v.embedding MATCH ?
		AND v.k = 5
		ORDER BY v.distance
		LIMIT 5
	`, vecBlob)
	if err != nil {
		t.Fatalf("KNN query: %v", err)
	}
	defer rows.Close()

	type knnResult struct {
		symbol   string
		kind     string
		distance float64
	}
	var results []knnResult
	for rows.Next() {
		var r knnResult
		if err := rows.Scan(&r.symbol, &r.kind, &r.distance); err != nil {
			t.Fatalf("scan KNN row: %v", err)
		}
		results = append(results, r)
	}

	if len(results) == 0 {
		t.Fatal("KNN query returned no results")
	}

	// First result should be ValidateToken itself with distance ≈ 0.
	if results[0].symbol != "ValidateToken" {
		t.Errorf("expected first KNN result to be ValidateToken, got %s", results[0].symbol)
	}
	if results[0].distance > 0.01 {
		t.Errorf("self-similarity distance should be ≈ 0, got %f", results[0].distance)
	}

	// All distances should be non-negative and ordered ascending.
	for i, r := range results {
		if r.distance < 0 {
			t.Errorf("result[%d] %s: distance should be >= 0, got %f", i, r.symbol, r.distance)
		}
		if i > 0 && r.distance < results[i-1].distance {
			t.Errorf("results not ordered by distance: %s(%f) < %s(%f)",
				r.symbol, r.distance, results[i-1].symbol, results[i-1].distance)
		}
	}

	// Scores (1 - distance) should all be in (0, 1].
	for i, r := range results {
		score := 1.0 - r.distance
		if score <= 0 || score > 1 {
			t.Errorf("result[%d] %s: score should be in (0, 1], got %f", i, r.symbol, score)
		}
	}
}

func TestE2E_CLI_SQLVerifyIncremental(t *testing.T) {
	dataHome := t.TempDir()
	tmpDir := t.TempDir()
	copyDir(t, sampleProjectPath(t), tmpDir)

	// Index.
	_, _, err := runCLIWithDataHome(t, dataHome, "index", tmpDir)
	if err != nil {
		t.Fatalf("first index failed: %v", err)
	}

	db := openIndexDB(t, dataHome, tmpDir)

	// Count initial state.
	var initialFiles, initialChunks int
	db.QueryRow("SELECT count(*) FROM files").Scan(&initialFiles)
	db.QueryRow("SELECT count(*) FROM chunks").Scan(&initialChunks)

	if initialFiles != 5 {
		t.Fatalf("expected 5 initial files, got %d", initialFiles)
	}

	// Get initial root hash.
	var hash1 string
	db.QueryRow("SELECT value FROM project_meta WHERE key = 'root_hash'").Scan(&hash1)

	// Add a new file.
	newCode := "package project\n\n// Shutdown stops the server.\nfunc Shutdown() error { return nil }\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "shutdown.go"), []byte(newCode), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// Close DB before re-indexing (index opens its own connection).
	db.Close()

	// Re-index.
	_, _, err = runCLIWithDataHome(t, dataHome, "index", tmpDir)
	if err != nil {
		t.Fatalf("second index failed: %v", err)
	}

	db = openIndexDB(t, dataHome, tmpDir)

	// Verify file count increased.
	var newFileCount int
	db.QueryRow("SELECT count(*) FROM files").Scan(&newFileCount)
	if newFileCount != 6 {
		t.Errorf("expected 6 files after adding one, got %d", newFileCount)
	}

	// Verify chunk count increased.
	var newChunkCount int
	db.QueryRow("SELECT count(*) FROM chunks").Scan(&newChunkCount)
	if newChunkCount <= initialChunks {
		t.Errorf("expected more chunks after adding file: before=%d, after=%d", initialChunks, newChunkCount)
	}

	// Verify new symbol exists.
	var shutdownExists int
	db.QueryRow("SELECT count(*) FROM chunks WHERE symbol = 'Shutdown'").Scan(&shutdownExists)
	if shutdownExists == 0 {
		t.Error("expected Shutdown symbol in chunks after adding file")
	}

	// Verify vec_chunks stayed in sync.
	var vecCount, chunkCount int
	db.QueryRow("SELECT count(*) FROM vec_chunks").Scan(&vecCount)
	db.QueryRow("SELECT count(*) FROM chunks").Scan(&chunkCount)
	if vecCount != chunkCount {
		t.Errorf("vec_chunks (%d) out of sync with chunks (%d)", vecCount, chunkCount)
	}

	// Root hash should have changed.
	var hash2 string
	db.QueryRow("SELECT value FROM project_meta WHERE key = 'root_hash'").Scan(&hash2)
	if hash2 == hash1 {
		t.Error("root_hash should change after adding a file")
	}

	// Delete a file.
	db.Close()
	os.Remove(filepath.Join(tmpDir, "database.go"))

	_, _, err = runCLIWithDataHome(t, dataHome, "index", tmpDir)
	if err != nil {
		t.Fatalf("third index failed: %v", err)
	}

	db = openIndexDB(t, dataHome, tmpDir)

	// Verify file removed from files table.
	var dbFileExists int
	db.QueryRow("SELECT count(*) FROM files WHERE path LIKE '%database.go'").Scan(&dbFileExists)
	if dbFileExists != 0 {
		t.Error("database.go should be removed from files table after deletion")
	}

	// Verify QueryUsers chunks are gone.
	var queryUsersExists int
	db.QueryRow("SELECT count(*) FROM chunks WHERE symbol = 'QueryUsers'").Scan(&queryUsersExists)
	if queryUsersExists != 0 {
		t.Error("QueryUsers chunks should be removed after deleting database.go")
	}

	// Verify no orphan chunks.
	var orphans int
	db.QueryRow(`
		SELECT count(*) FROM chunks c
		LEFT JOIN files f ON c.file_path = f.path
		WHERE f.path IS NULL
	`).Scan(&orphans)
	if orphans != 0 {
		t.Errorf("found %d orphan chunks after file deletion", orphans)
	}
}
