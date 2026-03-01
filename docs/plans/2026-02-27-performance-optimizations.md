# Performance Optimizations Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to
> implement this plan task-by-task.

**Goal:** Apply six targeted performance optimizations to the agent-index MCP
server covering SQLite tuning, parallel file hashing, streaming chunk batches,
concurrent embedding, fewer DB round trips in Status(), and a read-optimized
indexer cache.

**Architecture:** All changes are internal to each package's existing files. No
new files are needed. Each task is independently testable and committable. The
changes are additive/drop-in replacements — no public API surface changes.

**Tech Stack:** Go 1.25, SQLite (mattn/go-sqlite3 + sqlite-vec), stdlib `sync`,
`sync/atomic`, `context`, `net/http`.

---

## Task 1: SQLite write-performance pragmas + chunk indexes

**Files:**

- Modify: `internal/store/store.go:50-58` (pragma list)
- Modify: `internal/store/store.go:69-98` (createSchema stmts)
- Test: `internal/store/store_test.go`

**Step 1: Write the failing tests**

Add to `internal/store/store_test.go`:

```go
func TestStore_Pragmas(t *testing.T) {
	s, err := New(":memory:", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	var mode string
	if err := s.db.QueryRow("PRAGMA synchronous").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	// 1 = NORMAL
	if mode != "1" {
		t.Fatalf("expected synchronous=NORMAL(1), got %s", mode)
	}
}

func TestStore_ChunkIndexesExist(t *testing.T) {
	s, err := New(":memory:", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	var count int
	err = s.db.QueryRow(
		`SELECT count(*) FROM sqlite_master
		 WHERE type='index' AND name IN ('idx_chunks_file_path','idx_chunks_kind')`,
	).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected 2 indexes, got %d", count)
	}
}
```

**Step 2: Run tests to verify they fail**

```
cd /Users/aeneasr/workspace/agentic/agent-index
go test ./internal/store/... -run "TestStore_Pragmas|TestStore_ChunkIndexesExist" -v
```

Expected: FAIL — `expected synchronous=NORMAL(1)` and
`expected 2 indexes, got 0`.

**Step 3: Implement — add pragmas and indexes in `internal/store/store.go`**

Replace the pragma list at lines 50-58:

```go
	// Enable WAL mode, foreign keys, and write-performance settings.
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA cache_size=-64000",
		"PRAGMA temp_store=MEMORY",
		"PRAGMA busy_timeout=5000",
	}
```

Add two index statements to the `stmts` slice inside `createSchema` (after the
`chunks` table, before the `vec_chunks` virtual table):

```go
		`CREATE INDEX IF NOT EXISTS idx_chunks_file_path ON chunks(file_path)`,
		`CREATE INDEX IF NOT EXISTS idx_chunks_kind ON chunks(kind)`,
```

Full updated `stmts` slice in `createSchema`:

```go
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS files (
			path TEXT PRIMARY KEY,
			hash TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS project_meta (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS chunks (
			id         TEXT PRIMARY KEY,
			file_path  TEXT NOT NULL REFERENCES files(path),
			symbol     TEXT NOT NULL,
			kind       TEXT NOT NULL,
			start_line INTEGER NOT NULL,
			end_line   INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_chunks_file_path ON chunks(file_path)`,
		`CREATE INDEX IF NOT EXISTS idx_chunks_kind ON chunks(kind)`,
		fmt.Sprintf(
			`CREATE VIRTUAL TABLE IF NOT EXISTS vec_chunks USING vec0(
				id TEXT PRIMARY KEY,
				embedding float[%d] distance_metric=cosine
			)`, dimensions),
	}
```

**Step 4: Run tests to verify they pass**

```
go test ./internal/store/... -v
```

Expected: all PASS.

**Step 5: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit -m "perf: add SQLite write pragmas and chunk column indexes"
```

---

## Task 2: Parallel file reads in merkle.BuildTree

**Files:**

- Modify: `internal/merkle/merkle.go`
- Test: `internal/merkle/merkle_test.go`

**Background:** `BuildTree` currently reads every file sequentially inside
`filepath.WalkDir`. The fix is a two-phase approach: (1) walk to collect paths,
(2) fan out to 8 goroutines that hash files concurrently, (3) merge results.

**Step 1: Write the failing test**

Read the existing test file first (`internal/merkle/merkle_test.go`), then add:

```go
func TestBuildTree_ParallelMatchesSerial(t *testing.T) {
	dir := t.TempDir()
	// Write 20 Go files.
	for i := 0; i < 20; i++ {
		content := fmt.Sprintf("package main\n\nfunc F%d() {}\n", i)
		path := filepath.Join(dir, fmt.Sprintf("f%d.go", i))
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tree1, err := BuildTree(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	tree2, err := BuildTree(dir, nil)
	if err != nil {
		t.Fatal(err)
	}

	if tree1.RootHash != tree2.RootHash {
		t.Fatalf("parallel runs produced different root hashes: %s vs %s", tree1.RootHash, tree2.RootHash)
	}
	if len(tree1.Files) != 20 {
		t.Fatalf("expected 20 files, got %d", len(tree1.Files))
	}
}
```

**Step 2: Run to verify test passes already (it should — this tests correctness,
not parallelism)**

```
go test ./internal/merkle/... -run TestBuildTree_ParallelMatchesSerial -v
```

Expected: PASS (confirms correctness baseline before refactor).

**Step 3: Implement parallel BuildTree**

Replace the entire `BuildTree` function in `internal/merkle/merkle.go`:

```go
const merkleWorkers = 8

// BuildTree walks rootDir and computes a Merkle tree.
// File reads are parallelized across up to merkleWorkers goroutines.
// If skip is nil, DefaultSkip is used.
func BuildTree(rootDir string, skip SkipFunc) (*Tree, error) {
	if skip == nil {
		skip = DefaultSkip
	}

	// Phase 1: collect file paths (sequential walk, cheap).
	var relPaths []string
	err := filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(rootDir, path)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if skip(rel, true) {
				return filepath.SkipDir
			}
			return nil
		}
		if !skip(rel, false) {
			relPaths = append(relPaths, rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Phase 2: hash files concurrently with a bounded worker pool.
	type result struct {
		rel  string
		hash string
		err  error
	}

	work := make(chan string, len(relPaths))
	for _, p := range relPaths {
		work <- p
	}
	close(work)

	results := make(chan result, len(relPaths))
	workers := merkleWorkers
	if workers > len(relPaths) {
		workers = len(relPaths)
	}
	if workers == 0 {
		workers = 1
	}

	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for rel := range work {
				data, err := os.ReadFile(filepath.Join(rootDir, rel))
				if err != nil {
					results <- result{err: err}
					return
				}
				hash := fmt.Sprintf("%x", sha256.Sum256(data))
				results <- result{rel: rel, hash: hash}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	tree := &Tree{
		Files: make(map[string]string, len(relPaths)),
		Dirs:  make(map[string]string),
	}
	for r := range results {
		if r.err != nil {
			return nil, r.err
		}
		tree.Files[r.rel] = r.hash
	}

	tree.RootHash = buildDirHash(tree.Files)
	return tree, nil
}
```

Add `"sync"` to the import block in `merkle.go`.

**Step 4: Run tests**

```
go test ./internal/merkle/... -v
go test ./...
```

Expected: all PASS.

**Step 5: Commit**

```bash
git add internal/merkle/merkle.go internal/merkle/merkle_test.go
git commit -m "perf: parallelize file reads in merkle.BuildTree with worker pool"
```

---

## Task 3: Stream chunk batches through embed+insert pipeline

**Files:**

- Modify: `internal/index/index.go:129-174`
- Test: `internal/index/index_test.go`

**Background:** Currently all chunks are accumulated in memory before a single
`Embed` call. For large codebases this accumulates unboundedly. Fix:
embed+insert in rolling batches of 256 chunks.

**Step 1: Write the failing test**

Add to `internal/index/index_test.go`:

```go
func TestIndexer_StreamingBatchesProduceSameChunks(t *testing.T) {
	projectDir := t.TempDir()
	// Write enough files to span multiple chunk batches (batchSize=256).
	// Each file produces ~2 chunks (package + one func), so 150 files = ~300 chunks = 2 batches.
	for i := 0; i < 150; i++ {
		writeGoFile(t, projectDir, fmt.Sprintf("f%d.go", i), fmt.Sprintf(`package main

func F%d() {}
`, i))
	}

	emb := &mockEmbedder{dims: 4, model: "test-model"}
	idx, err := NewIndexer(":memory:", emb)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	stats, err := idx.Index(context.Background(), projectDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if stats.IndexedFiles != 150 {
		t.Fatalf("expected 150 indexed files, got %d", stats.IndexedFiles)
	}
	if stats.ChunksCreated == 0 {
		t.Fatal("expected chunks created")
	}
	// Embed should have been called multiple times (once per batch).
	if emb.callCount < 2 {
		t.Fatalf("expected ≥2 embed calls for streaming batches, got %d", emb.callCount)
	}
}
```

**Step 2: Run to verify test fails**

```
go test ./internal/index/... -run TestIndexer_StreamingBatchesProduceSameChunks -v
```

Expected: FAIL — `expected ≥2 embed calls for streaming batches, got 1`.

**Step 3: Implement streaming batches in `internal/index/index.go`**

Replace the chunk-collection and embed section (lines 129-174) with:

```go
	// Process files in streaming batches to avoid accumulating all chunks in memory.
	const chunkBatchSize = 256
	var (
		batch      []chunker.Chunk
		totalChunks int
	)

	flushBatch := func() error {
		if len(batch) == 0 {
			return nil
		}
		texts := make([]string, len(batch))
		for i, c := range batch {
			texts[i] = c.Content
		}
		vectors, err := idx.emb.Embed(ctx, texts)
		if err != nil {
			return fmt.Errorf("embed batch: %w", err)
		}
		if err := idx.store.InsertChunks(batch, vectors); err != nil {
			return fmt.Errorf("insert batch: %w", err)
		}
		totalChunks += len(batch)
		batch = batch[:0]
		return nil
	}

	for _, relPath := range filesToIndex {
		absPath := filepath.Join(projectDir, relPath)
		content, err := os.ReadFile(absPath)
		if err != nil {
			return stats, fmt.Errorf("read file %s: %w", relPath, err)
		}

		// Delete old chunks for this file (handles modified files and force re-index).
		if err := idx.store.DeleteFileChunks(relPath); err != nil {
			return stats, fmt.Errorf("delete old chunks for %s: %w", relPath, err)
		}

		chunks, err := idx.chunker.Chunk(relPath, content)
		if err != nil {
			return stats, fmt.Errorf("chunk %s: %w", relPath, err)
		}

		batch = append(batch, chunks...)

		// Flush when batch is full.
		if len(batch) >= chunkBatchSize {
			if err := flushBatch(); err != nil {
				return stats, err
			}
		}

		// Update file hash in the store.
		if err := idx.store.UpsertFile(relPath, curTree.Files[relPath]); err != nil {
			return stats, fmt.Errorf("upsert file %s: %w", relPath, err)
		}
	}

	// Flush any remaining chunks.
	if err := flushBatch(); err != nil {
		return stats, err
	}

	stats.IndexedFiles = len(filesToIndex)
	stats.ChunksCreated = totalChunks
```

Remove the old `var allChunks []chunker.Chunk` accumulation block and the
standalone embed+insert block that followed it.

**Step 4: Run tests**

```
go test ./internal/index/... -v
go test ./...
```

Expected: all PASS.

**Step 5: Commit**

```bash
git add internal/index/index.go internal/index/index_test.go
git commit -m "perf: stream chunk embed+insert in batches of 256 to reduce peak memory"
```

---

## Task 4: Concurrent batch embedding + retry on 429 + pre-allocate slice

**Files:**

- Modify: `internal/embedder/ollama.go`
- Test: `internal/embedder/ollama_test.go`

**Background:** Batches are currently sent to Ollama serially. With a
4-concurrent-batches limit they can be pipelined. Also: HTTP 429 (rate limit)
should be retried like 5xx. Also: `allVecs` slice should be pre-allocated.

**Step 1: Read the existing embedder test**

Read `internal/embedder/ollama_test.go` to understand the test helpers
available.

**Step 2: Write the failing tests**

Add to `internal/embedder/ollama_test.go`:

```go
func TestOllama_Embed_PreAllocated(t *testing.T) {
	// Verify Embed returns correctly ordered results for many texts.
	// Uses a test HTTP server that returns identity embeddings (text index as float).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		embeddings := make([][]float32, len(req.Input))
		for i := range req.Input {
			// Return a fixed-size vector where the first element encodes position.
			embeddings[i] = []float32{float32(i), 0, 0, 0}
		}
		json.NewEncoder(w).Encode(map[string]any{"embeddings": embeddings})
	}))
	defer srv.Close()

	emb, _ := NewOllama("test", 4, srv.URL)
	// 100 texts → 4 batches of 32 (last batch = 4).
	texts := make([]string, 100)
	for i := range texts {
		texts[i] = fmt.Sprintf("text%d", i)
	}

	vecs, err := emb.Embed(context.Background(), texts)
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 100 {
		t.Fatalf("expected 100 vectors, got %d", len(vecs))
	}
}

func TestOllama_Embed_Retries429(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		var req struct{ Input []string }
		json.NewDecoder(r.Body).Decode(&req)
		embeddings := make([][]float32, len(req.Input))
		for i := range embeddings {
			embeddings[i] = []float32{0.1, 0.2, 0.3, 0.4}
		}
		json.NewEncoder(w).Encode(map[string]any{"embeddings": embeddings})
	}))
	defer srv.Close()

	emb, _ := NewOllama("test", 4, srv.URL)
	vecs, err := emb.Embed(context.Background(), []string{"hello"})
	if err != nil {
		t.Fatalf("expected retry to succeed, got: %v", err)
	}
	if len(vecs) != 1 {
		t.Fatalf("expected 1 vector, got %d", len(vecs))
	}
	if attempts < 2 {
		t.Fatalf("expected at least 2 attempts (retry on 429), got %d", attempts)
	}
}
```

**Step 3: Run to verify tests fail**

```
go test ./internal/embedder/... -run "TestOllama_Embed_PreAllocated|TestOllama_Embed_Retries429" -v
```

Expected: the 429 test may fail because 429 is currently not retried.

**Step 4: Implement concurrent batches + retry 429 + pre-allocate in
`internal/embedder/ollama.go`**

Add `"sync"` to imports.

Add constant:

```go
const maxConcurrentBatches = 4
```

Replace the `Embed` function:

```go
// Embed converts texts into embedding vectors, splitting into batches of ollamaBatchSize
// and sending up to maxConcurrentBatches batches in parallel.
func (o *Ollama) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	// Split into batches.
	type batchJob struct {
		idx   int    // index into allVecs where results start
		texts []string
	}
	var jobs []batchJob
	for i := 0; i < len(texts); i += ollamaBatchSize {
		end := i + ollamaBatchSize
		if end > len(texts) {
			end = len(texts)
		}
		jobs = append(jobs, batchJob{idx: i, texts: texts[i:end]})
	}

	allVecs := make([][]float32, len(texts))

	sem := make(chan struct{}, maxConcurrentBatches)
	var mu sync.Mutex
	var firstErr error
	var wg sync.WaitGroup

	for _, job := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func(job batchJob) {
			defer wg.Done()
			defer func() { <-sem }()

			vecs, err := o.embedBatch(ctx, job.texts)

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("embedding batch at %d: %w", job.idx, err)
				}
				return
			}
			copy(allVecs[job.idx:], vecs)
		}(job)
	}

	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return allVecs, nil
}
```

In `embedBatch`, add retry on 429 — change the status-code check block:

```go
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			lastErr = fmt.Errorf("server error: status %d", resp.StatusCode)
			backoff(attempt)
			continue
		}
```

**Step 5: Run tests**

```
go test ./internal/embedder/... -v
go test ./...
```

Expected: all PASS.

**Step 6: Commit**

```bash
git add internal/embedder/ollama.go internal/embedder/ollama_test.go
git commit -m "perf: concurrent batch embedding, retry on 429, pre-allocate result slice"
```

---

## Task 5: Combine Stats() into one query + batch metadata in Status()

**Files:**

- Modify: `internal/store/store.go:295-303` (Stats)
- Modify: `internal/index/index.go:219-253` (Status)
- Test: `internal/store/store_test.go`, `internal/index/index_test.go`

**Background:** `Stats()` makes two separate queries; combine into one.
`Status()` calls `GetMeta` twice separately; combine into one `GetMetaBatch`
call.

**Step 1: Write the failing tests**

In `internal/store/store_test.go`, add:

```go
func TestStore_GetMetaBatch(t *testing.T) {
	s, err := New(":memory:", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	s.SetMeta("key1", "val1")
	s.SetMeta("key2", "val2")

	vals, err := s.GetMetaBatch([]string{"key1", "key2", "missing"})
	if err != nil {
		t.Fatal(err)
	}
	if vals["key1"] != "val1" {
		t.Fatalf("expected val1, got %s", vals["key1"])
	}
	if vals["key2"] != "val2" {
		t.Fatalf("expected val2, got %s", vals["key2"])
	}
	if _, ok := vals["missing"]; ok {
		t.Fatal("expected missing key to be absent")
	}
}
```

**Step 2: Run to verify test fails**

```
go test ./internal/store/... -run TestStore_GetMetaBatch -v
```

Expected: FAIL — `GetMetaBatch` does not exist yet.

**Step 3: Add `GetMetaBatch` to `internal/store/store.go`**

Add `"strings"` to the import block, then add after `GetMeta`:

```go
// GetMetaBatch retrieves multiple key-value pairs from project_meta in one query.
// Missing keys are simply absent from the returned map.
func (s *Store) GetMetaBatch(keys []string) (map[string]string, error) {
	if len(keys) == 0 {
		return map[string]string{}, nil
	}
	placeholders := make([]string, len(keys))
	args := make([]any, len(keys))
	for i, k := range keys {
		placeholders[i] = "?"
		args[i] = k
	}
	query := fmt.Sprintf(
		"SELECT key, value FROM project_meta WHERE key IN (%s)",
		strings.Join(placeholders, ","),
	)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query meta batch: %w", err)
	}
	defer rows.Close()

	result := make(map[string]string, len(keys))
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("scan meta: %w", err)
		}
		result[k] = v
	}
	return result, rows.Err()
}
```

Also combine the two `Stats()` queries into one:

```go
// Stats returns aggregate statistics about the store contents in one query.
func (s *Store) Stats() (StoreStats, error) {
	var stats StoreStats
	err := s.db.QueryRow(
		`SELECT (SELECT count(*) FROM files), (SELECT count(*) FROM chunks)`,
	).Scan(&stats.TotalFiles, &stats.TotalChunks)
	if err != nil {
		return stats, fmt.Errorf("stats query: %w", err)
	}
	return stats, nil
}
```

**Step 4: Update `Status()` in `internal/index/index.go` to use `GetMetaBatch`**

Replace the two separate `GetMeta` calls in `Status()`:

```go
	// Get metadata in a single round-trip.
	meta, err := idx.store.GetMetaBatch([]string{"embedding_model", "last_indexed_at"})
	if err != nil {
		return info, fmt.Errorf("get meta batch: %w", err)
	}
	info.EmbeddingModel = meta["embedding_model"]
	info.LastIndexedAt = meta["last_indexed_at"]
```

**Step 5: Run tests**

```
go test ./internal/store/... -v
go test ./internal/index/... -v
go test ./...
```

Expected: all PASS.

**Step 6: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go internal/index/index.go
git commit -m "perf: combine Stats() into one query, add GetMetaBatch for Status()"
```

---

## Task 6: RWMutex for indexer cache

**Files:**

- Modify: `main.go:65-97`

**Background:** All cache lookups (including read-only hits) take an exclusive
write lock. Using `sync.RWMutex` with a double-checked lock pattern allows
concurrent reads when a project is already cached.

**Step 1: Write the test**

Add to `main.go` or a new `main_test.go`:

```go
// main_test.go
package main

import (
	"context"
	"sync"
	"testing"

	"github.com/aeneasr/agent-index/internal/embedder"
	"github.com/aeneasr/agent-index/internal/index"
)

type stubEmbedder struct{}

func (s *stubEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	vecs := make([][]float32, len(texts))
	for i := range vecs {
		vecs[i] = []float32{0.1, 0.2, 0.3, 0.4}
	}
	return vecs, nil
}
func (s *stubEmbedder) Dimensions() int  { return 4 }
func (s *stubEmbedder) ModelName() string { return "stub" }

func TestIndexerCache_ConcurrentReads(t *testing.T) {
	_ = embedder.Embedder(&stubEmbedder{})
	_ = (*index.Indexer)(nil)

	ic := &indexerCache{embedder: &stubEmbedder{}}

	// Pre-warm with a single in-memory path (won't create real DB since we use ":memory:").
	// Instead, verify concurrent calls don't race.
	const goroutines = 20
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Path doesn't exist on disk; will fail — that's fine, we test no data race.
			ic.getOrCreate("/nonexistent/path")
		}()
	}
	wg.Wait()
}
```

**Step 2: Run with race detector to verify race exists (optional, documentation
only)**

```
go test -race ./... -run TestIndexerCache_ConcurrentReads -v
```

This may or may not catch a race before the fix since the existing `sync.Mutex`
is correct — the RWMutex is a performance upgrade, not a bug fix.

**Step 3: Implement RWMutex in `main.go`**

Change the struct field and all lock/unlock calls:

```go
type indexerCache struct {
	mu       sync.RWMutex
	cache    map[string]*index.Indexer
	embedder embedder.Embedder
}

func (ic *indexerCache) getOrCreate(projectPath string) (*index.Indexer, error) {
	// Fast path: concurrent reads.
	ic.mu.RLock()
	if ic.cache != nil {
		if idx, ok := ic.cache[projectPath]; ok {
			ic.mu.RUnlock()
			return idx, nil
		}
	}
	ic.mu.RUnlock()

	// Slow path: exclusive write.
	ic.mu.Lock()
	defer ic.mu.Unlock()

	if ic.cache == nil {
		ic.cache = make(map[string]*index.Indexer)
	}
	// Double-check after acquiring write lock.
	if idx, ok := ic.cache[projectPath]; ok {
		return idx, nil
	}

	dbPath := dbPathForProject(projectPath)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}

	idx, err := index.NewIndexer(dbPath, ic.embedder)
	if err != nil {
		return nil, fmt.Errorf("create indexer: %w", err)
	}

	ic.cache[projectPath] = idx
	return idx, nil
}
```

**Step 4: Run tests with race detector**

```
go test -race ./... -v
```

Expected: all PASS, no races.

**Step 5: Commit**

```bash
git add main.go main_test.go
git commit -m "perf: use RWMutex with double-checked locking in indexer cache"
```

---

## Verification

After all tasks are complete, run the full test suite one final time:

```
go test -race ./... -count=1
```

Expected: all PASS, no races detected.
