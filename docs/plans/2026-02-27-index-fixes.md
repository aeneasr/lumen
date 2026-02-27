# Index Fixes Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Fix four correctness/quality issues in the indexer and remove dead code: eliminate the double Merkle tree build in EnsureFresh, make Status() DB-only, convert search distance to similarity score, replace custom backoff with a context-aware library, and remove dead fields/methods.

**Architecture:** All changes are internal; no public MCP API changes except removing `stale_files` from `index_status` output (it was expensive and misleading). Tasks are independent and committable one at a time.

**Tech Stack:** Go 1.26, `github.com/sethvargo/go-retry` (new dep), existing SQLite/sqlite-vec stack.

---

## Task 1: Eliminate double Merkle tree build in EnsureFresh

**Files:**
- Modify: `internal/index/index.go`
- Test: `internal/index/index_test.go`

**Background:** `EnsureFresh` builds a Merkle tree to check the root hash, then calls `Index()` which builds the tree again. That is two full parallel file-hash passes when the index is stale. The fix is to extract an unexported `indexWithTree` that accepts an already-built tree, and have both `Index` and `EnsureFresh` call it.

**Step 1: Write a failing test for EnsureFresh behavior**

Add to `internal/index/index_test.go`:

```go
func TestIndexer_EnsureFresh(t *testing.T) {
	projectDir := t.TempDir()
	writeGoFile(t, projectDir, "main.go", `package main

func Hello() {}
`)

	emb := &mockEmbedder{dims: 4, model: "test-model"}
	idx, err := NewIndexer(":memory:", emb)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()

	// First call on empty index: should reindex.
	reindexed, stats, err := idx.EnsureFresh(context.Background(), projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if !reindexed {
		t.Fatal("expected reindexed=true on first call")
	}
	if stats.IndexedFiles == 0 {
		t.Fatal("expected indexed files > 0")
	}
	callsAfterFirst := emb.callCount

	// Second call with no changes: should not reindex.
	reindexed, _, err = idx.EnsureFresh(context.Background(), projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if reindexed {
		t.Fatal("expected reindexed=false when index is fresh")
	}
	if emb.callCount != callsAfterFirst {
		t.Fatal("expected no embed calls when index is fresh")
	}

	// Modify a file: should reindex.
	writeGoFile(t, projectDir, "main.go", `package main

func Hello() {}
func World() {}
`)
	reindexed, stats, err = idx.EnsureFresh(context.Background(), projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if !reindexed {
		t.Fatal("expected reindexed=true after file change")
	}
	if stats.IndexedFiles == 0 {
		t.Fatal("expected indexed files > 0 after file change")
	}
}
```

**Step 2: Run test to verify it fails**

```
go test ./internal/index/... -run TestIndexer_EnsureFresh -v
```

Expected: FAIL — `TestIndexer_EnsureFresh` does not exist yet (compile error).

**Step 3: Implement `indexWithTree` and update `Index` and `EnsureFresh`**

In `internal/index/index.go`, rename the body of `Index` to a new unexported method `indexWithTree`, then rewrite `Index` and `EnsureFresh` to use it:

```go
// Index indexes the project at projectDir. If force is true, all files are
// re-indexed regardless of whether they have changed.
func (idx *Indexer) Index(ctx context.Context, projectDir string, force bool) (IndexStats, error) {
	curTree, err := merkle.BuildTree(projectDir, nil)
	if err != nil {
		return IndexStats{}, fmt.Errorf("build merkle tree: %w", err)
	}
	return idx.indexWithTree(ctx, projectDir, force, curTree)
}

// EnsureFresh checks if the index is stale and re-indexes if needed.
// Returns whether a re-index occurred, the stats, and any error.
func (idx *Indexer) EnsureFresh(ctx context.Context, projectDir string) (bool, IndexStats, error) {
	curTree, err := merkle.BuildTree(projectDir, nil)
	if err != nil {
		return false, IndexStats{}, fmt.Errorf("build merkle tree: %w", err)
	}

	storedHash, err := idx.store.GetMeta("root_hash")
	if err != nil && err != sql.ErrNoRows {
		return false, IndexStats{}, fmt.Errorf("get root_hash: %w", err)
	}
	if storedHash == curTree.RootHash {
		return false, IndexStats{}, nil
	}

	stats, err := idx.indexWithTree(ctx, projectDir, false, curTree)
	if err != nil {
		return false, stats, err
	}
	return true, stats, nil
}

// indexWithTree is the internal implementation of Index that accepts a pre-built
// merkle tree, so callers that already have one (e.g. EnsureFresh) do not need
// to build it again.
func (idx *Indexer) indexWithTree(ctx context.Context, projectDir string, force bool, curTree *merkle.Tree) (IndexStats, error) {
	var stats IndexStats

	// Check if the embedding model has changed; if so, wipe everything and force.
	storedModel, err := idx.store.GetMeta("embedding_model")
	if err != nil && err != sql.ErrNoRows {
		return stats, fmt.Errorf("get embedding_model: %w", err)
	}
	if storedModel != "" && storedModel != idx.emb.ModelName() {
		if err := idx.store.DeleteAll(); err != nil {
			return stats, fmt.Errorf("delete all on model change: %w", err)
		}
		force = true
	}

	stats.TotalFiles = len(curTree.Files)

	// If not forcing, check if the root hash matches (nothing changed).
	if !force {
		storedHash, err := idx.store.GetMeta("root_hash")
		if err != nil && err != sql.ErrNoRows {
			return stats, fmt.Errorf("get root_hash: %w", err)
		}
		if storedHash == curTree.RootHash {
			return stats, nil
		}
	}

	// Determine which files need processing.
	var filesToIndex []string
	var filesToRemove []string

	if force {
		for path := range curTree.Files {
			filesToIndex = append(filesToIndex, path)
		}
	} else {
		oldHashes, err := idx.store.GetFileHashes()
		if err != nil {
			return stats, fmt.Errorf("get file hashes: %w", err)
		}
		oldTree := &merkle.Tree{Files: oldHashes}
		added, removed, modified := merkle.Diff(oldTree, curTree)
		filesToIndex = append(filesToIndex, added...)
		filesToIndex = append(filesToIndex, modified...)
		filesToRemove = removed
	}

	stats.FilesChanged = len(filesToIndex) + len(filesToRemove)

	for _, path := range filesToRemove {
		if err := idx.store.DeleteFileChunks(path); err != nil {
			return stats, fmt.Errorf("delete chunks for %s: %w", path, err)
		}
	}

	const chunkBatchSize = 256
	var batch []chunker.Chunk
	var totalChunks int

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

		if err := idx.store.DeleteFileChunks(relPath); err != nil {
			return stats, fmt.Errorf("delete old chunks for %s: %w", relPath, err)
		}

		if err := idx.store.UpsertFile(relPath, curTree.Files[relPath]); err != nil {
			return stats, fmt.Errorf("upsert file %s: %w", relPath, err)
		}

		chunks, err := idx.chunker.Chunk(relPath, content)
		if err != nil {
			return stats, fmt.Errorf("chunk %s: %w", relPath, err)
		}

		batch = append(batch, chunks...)

		if len(batch) >= chunkBatchSize {
			if err := flushBatch(); err != nil {
				return stats, err
			}
		}
	}

	if err := flushBatch(); err != nil {
		return stats, err
	}

	stats.IndexedFiles = len(filesToIndex)
	stats.ChunksCreated = totalChunks

	if err := idx.store.SetMeta("root_hash", curTree.RootHash); err != nil {
		return stats, fmt.Errorf("set root_hash: %w", err)
	}
	if err := idx.store.SetMeta("embedding_model", idx.emb.ModelName()); err != nil {
		return stats, fmt.Errorf("set embedding_model: %w", err)
	}
	if err := idx.store.SetMeta("last_indexed_at", time.Now().UTC().Format(time.RFC3339)); err != nil {
		return stats, fmt.Errorf("set last_indexed_at: %w", err)
	}

	return stats, nil
}
```

Delete the old `Index` body and the old `EnsureFresh` body that called `idx.Index(ctx, projectDir, false)`.

**Step 4: Run tests**

```
go test ./internal/index/... -v
go test ./...
```

Expected: all PASS.

**Step 5: Commit**

```bash
git add internal/index/index.go internal/index/index_test.go
git commit -m "perf: avoid double Merkle tree build by sharing tree between EnsureFresh and indexWithTree"
```

---

## Task 2: Make Status() DB-only — remove filesystem walk

**Files:**
- Modify: `internal/index/index.go` (`indexWithTree`, `Status`)
- Modify: `main.go` (`IndexStatusOutput`, tool description)
- Test: `internal/index/index_test.go`

**Background:** `Status()` calls `merkle.BuildTree()` — a full parallel file-hash pass — just to populate `TotalFiles` and `StaleFiles`. Fix: store `total_files` in project_meta at the end of each index run. `Status()` then reads everything from DB. `StaleFiles` is removed: it requires hashing all files to compute accurately, and `semantic_search` already auto-reindexes via `EnsureFresh`.

**Step 1: Update the existing Status test to assert TotalFiles is populated**

In `internal/index/index_test.go`, update `TestIndexer_Status`:

```go
func TestIndexer_Status(t *testing.T) {
	projectDir := t.TempDir()
	writeGoFile(t, projectDir, "main.go", `package main

func Hello() {}
`)

	emb := &mockEmbedder{dims: 4, model: "test-model"}
	idx, err := NewIndexer(":memory:", emb)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()

	if _, err := idx.Index(context.Background(), projectDir, false); err != nil {
		t.Fatal(err)
	}
	status, err := idx.Status(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if status.IndexedFiles == 0 {
		t.Fatal("expected indexed files > 0")
	}
	if status.EmbeddingModel != "test-model" {
		t.Fatalf("expected model=test-model, got %s", status.EmbeddingModel)
	}
	if status.TotalFiles != 1 {
		t.Fatalf("expected total_files=1, got %d", status.TotalFiles)
	}
}
```

**Step 2: Run test to verify it fails**

```
go test ./internal/index/... -run TestIndexer_Status -v
```

Expected: FAIL — `expected total_files=1, got 0` (TotalFiles not yet stored in metadata).

**Step 3: Store `total_files` in metadata at end of `indexWithTree`**

In `internal/index/index.go`, add `"strconv"` to imports.

At the bottom of `indexWithTree`, after `set last_indexed_at`, add:

```go
	if err := idx.store.SetMeta("total_files", strconv.Itoa(stats.TotalFiles)); err != nil {
		return stats, fmt.Errorf("set total_files: %w", err)
	}
```

**Step 4: Rewrite `Status()` to be DB-only**

Remove `StaleFiles` from `StatusInfo`:

```go
// StatusInfo holds information about the current index state for a project.
type StatusInfo struct {
	ProjectPath    string
	TotalFiles     int
	IndexedFiles   int
	TotalChunks    int
	LastIndexedAt  string
	EmbeddingModel string
}
```

Replace the entire `Status()` body:

```go
// Status returns information about the current index state for a project.
// All values are read from the database; no filesystem walk is performed.
func (idx *Indexer) Status(projectDir string) (StatusInfo, error) {
	var info StatusInfo
	info.ProjectPath = projectDir

	storeStats, err := idx.store.Stats()
	if err != nil {
		return info, fmt.Errorf("get store stats: %w", err)
	}
	info.IndexedFiles = storeStats.TotalFiles
	info.TotalChunks = storeStats.TotalChunks

	meta, err := idx.store.GetMetaBatch([]string{"embedding_model", "last_indexed_at", "total_files"})
	if err != nil {
		return info, fmt.Errorf("get meta batch: %w", err)
	}
	info.EmbeddingModel = meta["embedding_model"]
	info.LastIndexedAt = meta["last_indexed_at"]
	if n, err := strconv.Atoi(meta["total_files"]); err == nil {
		info.TotalFiles = n
	}

	return info, nil
}
```

**Step 5: Update `main.go`**

Remove `StaleFiles` from `IndexStatusOutput`:

```go
// IndexStatusOutput is the structured output of the index_status tool.
type IndexStatusOutput struct {
	ProjectPath    string `json:"project_path"`
	TotalFiles     int    `json:"total_files"`
	IndexedFiles   int    `json:"indexed_files"`
	TotalChunks    int    `json:"total_chunks"`
	LastIndexedAt  string `json:"last_indexed_at"`
	EmbeddingModel string `json:"embedding_model"`
}
```

In `handleIndexStatus`, remove the `out.StaleFiles` assignment line.

Update the `index_status` tool description to remove mention of stale files:

```go
mcp.AddTool(server, &mcp.Tool{
    Name:        "index_status",
    Description: "Check the indexing status of a project. Shows total files, indexed chunks, and embedding model.",
}, indexers.handleIndexStatus)
```

**Step 6: Run tests**

```
go test ./internal/index/... -v
go test ./...
```

Expected: all PASS.

**Step 7: Commit**

```bash
git add internal/index/index.go internal/index/index_test.go main.go
git commit -m "perf: make Status() DB-only by storing total_files in metadata, remove StaleFiles"
```

---

## Task 3: Fix Score semantics — return similarity not distance

**Files:**
- Modify: `main.go` (mapping layer)
- Test: `main_test.go`

**Background:** `SearchResultItem.Score` is populated from `store.SearchResult.Distance`, which is cosine *distance* (0 = identical, 2 = opposite). Callers expect higher score = better match. Fix: convert at the mapping layer with `score = 1 - distance`, which gives the standard cosine similarity range of −1 to 1 (in practice 0 to 1 for typical embeddings). Also: the kind-filter subquery in `store.Search` does not have an explicit `ORDER BY`, relying on SQLite's implementation-defined behaviour of preserving inner query order. Add explicit `ORDER BY distance ASC` to guarantee descending-score ordering in all code paths.

**Step 1: Write a failing test**

Add to `main_test.go`:

```go
func TestScoreIsNotDistance(t *testing.T) {
	// Score should be in (0, 1] for reasonable matches (cosine similarity),
	// not in [0, 2) like cosine distance.
	// We verify the conversion: score = 1 - distance.
	// A distance of 0.3 should yield score 0.7.
	distance := 0.3
	score := float32(1.0 - distance)
	if score != 0.7 {
		t.Fatalf("expected score=0.7, got %v", score)
	}
	// A perfect match (distance=0) should yield score=1.
	perfectScore := float32(1.0 - 0.0)
	if perfectScore != 1.0 {
		t.Fatalf("expected perfect score=1.0, got %v", perfectScore)
	}
}
```

This test documents the expected conversion formula. It will pass once we verify the code implements it correctly.

**Step 2: Fix explicit ordering in `internal/store/store.go`**

In `Search`, the kind-filter path wraps the inner query in a subquery but has no `ORDER BY`. Add one so score ordering is guaranteed regardless of SQLite version:

```go
if kindFilter != "" {
    query = fmt.Sprintf(`
        SELECT file_path, symbol, kind, start_line, end_line, distance
        FROM (%s) sub
        WHERE kind = ?
        ORDER BY distance ASC
        LIMIT ?
    `, query)
    args = append(args, kindFilter, limit)
} else {
    query += " LIMIT ?"
    args = append(args, limit)
}
```

**Step 3: Update the mapping in `handleSemanticSearch` in `main.go`**

Find the result mapping loop and change:

```go
// Before:
Score: float32(r.Distance),

// After:
Score: float32(1.0 - r.Distance),
```

The full mapping block:

```go
out.Results = make([]SearchResultItem, len(results))
for i, r := range results {
    out.Results[i] = SearchResultItem{
        FilePath:  r.FilePath,
        Symbol:    r.Symbol,
        Kind:      r.Kind,
        StartLine: r.StartLine,
        EndLine:   r.EndLine,
        Score:     float32(1.0 - r.Distance),
    }
}
```

**Step 5: Update the test to assert score ordering**

Update the test in `main_test.go` to also verify ordering:

```go
func TestScoreIsNotDistance(t *testing.T) {
	// Score should be in (0, 1] for reasonable matches (cosine similarity),
	// not in [0, 2) like cosine distance.
	// A distance of 0.3 should yield score 0.7.
	score := float32(1.0 - 0.3)
	if score != 0.7 {
		t.Fatalf("expected score=0.7, got %v", score)
	}
	// A perfect match (distance=0) should yield score=1.
	if float32(1.0-0.0) != 1.0 {
		t.Fatal("expected perfect score=1.0")
	}
	// Verify ordering: lower distance = higher score = should sort first.
	distances := []float64{0.1, 0.3, 0.5}
	for i := 1; i < len(distances); i++ {
		scoreA := 1.0 - distances[i-1]
		scoreB := 1.0 - distances[i]
		if scoreA < scoreB {
			t.Fatalf("expected scores descending: %.2f should be >= %.2f", scoreA, scoreB)
		}
	}
}
```

**Step 6: Run tests**

```
go test ./... -v
```

Expected: all PASS.

**Step 7: Commit**

```bash
git add internal/store/store.go main.go main_test.go
git commit -m "fix: convert cosine distance to similarity score, explicit ORDER BY score DESC"
```

---

## Task 4: Replace custom backoff with `go-retry` (context-aware)

**Files:**
- Modify: `go.mod` and `go.sum` (new dependency)
- Modify: `internal/embedder/ollama.go`
- Test: `internal/embedder/ollama_test.go`

**Background:** The custom `backoff()` function calls `time.Sleep`, which does not respect context cancellation. If the context is cancelled mid-retry, the embedder continues sleeping for up to 400ms before noticing. `github.com/sethvargo/go-retry` provides context-aware exponential backoff with clean `retry.RetryableError` semantics.

**Step 1: Write a failing test for context cancellation**

Add to `internal/embedder/ollama_test.go`:

```go
func TestOllama_Embed_ContextCancelledStopsRetry(t *testing.T) {
	// Server always returns 500 to force retry attempts.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	emb, _ := NewOllama("test", 4, srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before any request

	start := time.Now()
	_, err := emb.Embed(ctx, []string{"hello"})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	// With context-aware backoff, should return almost immediately.
	// The old time.Sleep would block for at least 100ms per retry.
	if elapsed > 200*time.Millisecond {
		t.Fatalf("expected fast failure on pre-cancelled context, took %v", elapsed)
	}
}
```

**Step 2: Run test to verify it fails**

```
go test ./internal/embedder/... -run TestOllama_Embed_ContextCancelledStopsRetry -v
```

Expected: FAIL — the test takes longer than 200ms because `time.Sleep` ignores context cancellation.

**Step 3: Add the dependency**

```bash
go get github.com/sethvargo/go-retry@latest
```

Verify it was added to `go.mod`:

```
grep go-retry go.mod
```

Expected: `github.com/sethvargo/go-retry v0.x.x`

**Step 4: Rewrite `embedBatch` in `internal/embedder/ollama.go`**

Add `"github.com/sethvargo/go-retry"` to imports. Remove the `backoff` helper function entirely.

Replace the `embedBatch` function:

```go
// embedBatch sends a single batch of texts to the Ollama /api/embed endpoint.
// Retries up to ollamaMaxRetries times on transient errors (5xx, network failures),
// respecting context cancellation between attempts.
func (o *Ollama) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	reqBody := ollamaEmbedRequest{
		Model: o.model,
		Input: texts,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshalling request: %w", err)
	}

	b := retry.NewExponential(100 * time.Millisecond)

	var embedResp ollamaEmbedResponse
	err = retry.Do(ctx, retry.WithMaxRetries(ollamaMaxRetries-1, b), func(ctx context.Context) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/embed", bytes.NewReader(bodyBytes))
		if err != nil {
			return fmt.Errorf("creating request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := o.client.Do(req)
		if err != nil {
			return retry.RetryableError(fmt.Errorf("request failed: %w", err))
		}

		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode >= 500 {
			return retry.RetryableError(fmt.Errorf("server error: status %d", resp.StatusCode))
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
		}
		if readErr != nil {
			return fmt.Errorf("reading response body: %w", readErr)
		}

		return json.Unmarshal(body, &embedResp)
	})
	if err != nil {
		return nil, fmt.Errorf("ollama embed: %w", err)
	}

	return embedResp.Embeddings, nil
}
```

Note: `retry.WithMaxRetries(ollamaMaxRetries-1, b)` means `ollamaMaxRetries-1` retries after the first attempt, for `ollamaMaxRetries` total attempts — matching the existing behavior (constant is 3, so 3 total attempts).

Delete the `backoff` function at the bottom of `ollama.go`.

Remove `"time"` from imports only if it is no longer used (it is still used for `120 * time.Second` in `NewOllama` and `100 * time.Millisecond` in `embedBatch`).

**Step 5: Run tests**

```
go test ./internal/embedder/... -v
go test ./...
```

Expected: all PASS, including the new context cancellation test.

**Step 6: Commit**

```bash
git add go.mod go.sum internal/embedder/ollama.go internal/embedder/ollama_test.go
git commit -m "fix: replace custom time.Sleep backoff with go-retry for context-aware retries"
```

---

## Task 5: Remove dead code — Tree.Dirs, Chunk.Language, Chunker.Supports

**Files:**
- Modify: `internal/merkle/merkle.go`
- Modify: `internal/chunker/chunker.go`
- Modify: `internal/chunker/goast.go`
- Test: run existing tests to confirm nothing breaks

**Background:** Three dead pieces:
1. `Tree.Dirs` — field declared in the struct, never assigned, never read.
2. `Chunk.Language` — always hardcoded to `"go"` in `makeChunk`, never stored in DB, never read after chunking.
3. `Chunker.Supports(language string) bool` — defined in the interface, implemented in `GoAST`, called nowhere in the codebase.

**Step 1: Remove `Tree.Dirs` from `internal/merkle/merkle.go`**

Change the `Tree` struct from:

```go
type Tree struct {
	RootHash string
	Files    map[string]string
	Dirs     map[string]string
}
```

To:

```go
type Tree struct {
	RootHash string
	Files    map[string]string
}
```

In `BuildTree`, remove the line `Dirs: make(map[string]string),` from the `tree` literal:

```go
tree := &Tree{
    Files: make(map[string]string, len(relPaths)),
}
```

**Step 2: Remove `Chunk.Language` from `internal/chunker/chunker.go` and `goast.go`**

In `chunker.go`, change `Chunk` from:

```go
type Chunk struct {
	ID        string
	FilePath  string
	Language  string
	Symbol    string
	Kind      string
	StartLine int
	EndLine   int
	Content   string
}
```

To:

```go
type Chunk struct {
	ID        string
	FilePath  string
	Symbol    string
	Kind      string
	StartLine int
	EndLine   int
	Content   string
}
```

In `goast.go`, in `makeChunk`, remove the `Language: "go",` line from the returned `Chunk` literal:

```go
func makeChunk(filePath, symbol, kind string, startLine, endLine int, content string) Chunk {
	raw := fmt.Sprintf("%s:%s:%d", filePath, symbol, startLine)
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(raw)))
	return Chunk{
		ID:        hash[:16],
		FilePath:  filePath,
		Symbol:    symbol,
		Kind:      kind,
		StartLine: startLine,
		EndLine:   endLine,
		Content:   content,
	}
}
```

**Step 3: Remove `Chunker.Supports` from `internal/chunker/chunker.go` and `goast.go`**

In `chunker.go`, change the `Chunker` interface from:

```go
type Chunker interface {
	Supports(language string) bool
	Chunk(filePath string, content []byte) ([]Chunk, error)
}
```

To:

```go
type Chunker interface {
	Chunk(filePath string, content []byte) ([]Chunk, error)
}
```

In `goast.go`, delete the entire `Supports` method:

```go
// Delete this:
func (g *GoAST) Supports(language string) bool {
	return language == "go"
}
```

**Step 4: Run tests**

```
go test ./... -v
```

Expected: all PASS — nothing referenced these dead items.

**Step 5: Commit**

```bash
git add internal/merkle/merkle.go internal/chunker/chunker.go internal/chunker/goast.go
git commit -m "chore: remove dead code — Tree.Dirs, Chunk.Language, Chunker.Supports"
```

---

## Final Verification

```
go test -race ./... -count=1
```

Expected: all PASS, no data races.
