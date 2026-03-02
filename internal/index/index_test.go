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

package index

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// progressCall represents a progress function call for testing.
type progressCall struct {
	current int
	total   int
	message string
}

// mockEmbedder returns fixed vectors for testing.
type mockEmbedder struct {
	dims      int
	model     string
	callCount int
}

func (m *mockEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	m.callCount++
	vecs := make([][]float32, len(texts))
	for i := range texts {
		vec := make([]float32, m.dims)
		for j := range vec {
			vec[j] = float32(i+1) * 0.1
		}
		vecs[i] = vec
	}
	return vecs, nil
}

func (m *mockEmbedder) Dimensions() int   { return m.dims }
func (m *mockEmbedder) ModelName() string { return m.model }

func TestIndexer_IndexAndSearch(t *testing.T) {
	projectDir := t.TempDir()
	writeGoFile(t, projectDir, "main.go", `package main

import "fmt"

// Hello prints a greeting.
func Hello(name string) {
	fmt.Println("hello", name)
}

// Goodbye prints a farewell.
func Goodbye(name string) {
	fmt.Println("bye", name)
}
`)

	emb := &mockEmbedder{dims: 4, model: "test-model"}
	idx, err := NewIndexer(":memory:", emb, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()

	stats, err := idx.Index(context.Background(), projectDir, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.IndexedFiles == 0 {
		t.Fatal("expected at least 1 indexed file")
	}
	if stats.ChunksCreated == 0 {
		t.Fatal("expected at least 1 chunk created")
	}

	results, err := idx.Search(context.Background(), projectDir, []float32{0.1, 0.1, 0.1, 0.1}, 5, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected search results")
	}
}

func TestIndexer_IncrementalIndex(t *testing.T) {
	projectDir := t.TempDir()
	writeGoFile(t, projectDir, "main.go", `package main

func Hello() {}
`)

	emb := &mockEmbedder{dims: 4, model: "test-model"}
	idx, err := NewIndexer(":memory:", emb, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()

	if _, err := idx.Index(context.Background(), projectDir, false, nil); err != nil {
		t.Fatal(err)
	}
	firstCallCount := emb.callCount

	stats, err := idx.Index(context.Background(), projectDir, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if emb.callCount != firstCallCount {
		t.Fatal("expected no additional embedding calls for unchanged project")
	}
	if stats.ChunksCreated != 0 {
		t.Fatalf("expected 0 chunks created on re-index, got %d", stats.ChunksCreated)
	}
}

func TestIndexer_DetectsModifiedFiles(t *testing.T) {
	projectDir := t.TempDir()
	writeGoFile(t, projectDir, "main.go", `package main

func Hello() {}
`)

	emb := &mockEmbedder{dims: 4, model: "test-model"}
	idx, err := NewIndexer(":memory:", emb, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()

	if _, err := idx.Index(context.Background(), projectDir, false, nil); err != nil {
		t.Fatal(err)
	}
	firstCallCount := emb.callCount

	writeGoFile(t, projectDir, "main.go", `package main

func Hello() {}
func World() {}
`)

	stats, err := idx.Index(context.Background(), projectDir, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if emb.callCount == firstCallCount {
		t.Fatal("expected additional embedding calls after file change")
	}
	if stats.ChunksCreated == 0 {
		t.Fatal("expected new chunks after file change")
	}
}

func TestIndexer_ForceReindex(t *testing.T) {
	projectDir := t.TempDir()
	writeGoFile(t, projectDir, "main.go", `package main

func Hello() {}
`)

	emb := &mockEmbedder{dims: 4, model: "test-model"}
	idx, err := NewIndexer(":memory:", emb, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()

	if _, err := idx.Index(context.Background(), projectDir, false, nil); err != nil {
		t.Fatal(err)
	}
	firstCallCount := emb.callCount

	stats, err := idx.Index(context.Background(), projectDir, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if emb.callCount == firstCallCount {
		t.Fatal("expected re-embedding on force=true")
	}
	if stats.ChunksCreated == 0 {
		t.Fatal("expected chunks on force reindex")
	}
}

func TestIndexer_Status(t *testing.T) {
	projectDir := t.TempDir()
	writeGoFile(t, projectDir, "main.go", `package main

func Hello() {}
`)

	emb := &mockEmbedder{dims: 4, model: "test-model"}
	idx, err := NewIndexer(":memory:", emb, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()

	if _, err := idx.Index(context.Background(), projectDir, false, nil); err != nil {
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

func TestIndexer_StreamingBatchesProduceSameChunks(t *testing.T) {
	projectDir := t.TempDir()
	// 300 files × 1 function each = 300 chunks → spans 2 batches of 256.
	for i := range 300 {
		writeGoFile(t, projectDir, fmt.Sprintf("f%d.go", i), fmt.Sprintf(`package main

func F%d() {}
`, i))
	}

	emb := &mockEmbedder{dims: 4, model: "test-model"}
	idx, err := NewIndexer(":memory:", emb, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()

	stats, err := idx.Index(context.Background(), projectDir, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.IndexedFiles != 300 {
		t.Fatalf("expected 300 indexed files, got %d", stats.IndexedFiles)
	}
	if stats.ChunksCreated == 0 {
		t.Fatal("expected chunks created")
	}
	// With streaming, embed is called once per flush (multiple times).
	if emb.callCount < 2 {
		t.Fatalf("expected ≥2 embed calls for streaming batches, got %d", emb.callCount)
	}
}

func TestIndexer_EnsureFresh(t *testing.T) {
	projectDir := t.TempDir()
	writeGoFile(t, projectDir, "main.go", `package main

func Hello() {}
`)

	emb := &mockEmbedder{dims: 4, model: "test-model"}
	idx, err := NewIndexer(":memory:", emb, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()

	// First call on empty index: should reindex.
	reindexed, stats, err := idx.EnsureFresh(context.Background(), projectDir, nil)
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
	reindexed, _, err = idx.EnsureFresh(context.Background(), projectDir, nil)
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
	reindexed, stats, err = idx.EnsureFresh(context.Background(), projectDir, nil)
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

func TestIndexer_ProgressFunc(t *testing.T) {
	projectDir := t.TempDir()
	writeGoFile(t, projectDir, "a.go", `package main

func A() {}
`)
	writeGoFile(t, projectDir, "b.go", `package main

func B() {}
`)

	emb := &mockEmbedder{dims: 4, model: "test-model"}
	idx, err := NewIndexer(":memory:", emb, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()

	var calls []progressCall
	progress := func(current, total int, message string) {
		calls = append(calls, progressCall{current, total, message})
	}

	_, err = idx.Index(context.Background(), projectDir, false, progress)
	if err != nil {
		t.Fatal(err)
	}

	checkProgressCalls(t, calls)
}

func checkProgressCalls(t *testing.T, calls []progressCall) {
	if len(calls) == 0 {
		t.Fatal("expected progress calls, got none")
	}

	checkFirstCall(t, calls[0])
	checkAllCalls(t, calls)
	checkProcessingCalls(t, calls)
	checkEmbedCalls(t, calls)
	checkLastCall(t, calls[len(calls)-1])
}

func checkFirstCall(t *testing.T, first progressCall) {
	if first.current != 0 {
		t.Errorf("first call: expected current=0, got %d", first.current)
	}
	if first.total != 2 {
		t.Errorf("first call: expected total=2, got %d", first.total)
	}
}

func checkAllCalls(t *testing.T, calls []progressCall) {
	for i, c := range calls {
		if c.total != 2 {
			t.Errorf("call[%d]: expected total=2, got %d (message: %s)", i, c.total, c.message)
		}
		if c.current > c.total {
			t.Errorf("call[%d]: current (%d) > total (%d)", i, c.current, c.total)
		}
	}
}

func checkProcessingCalls(t *testing.T, calls []progressCall) {
	var processingCalls int
	for _, c := range calls {
		if strings.Contains(c.message, "Processing file") {
			processingCalls++
		}
	}
	if processingCalls != 2 {
		t.Fatalf("expected 2 processing progress calls, got %d", processingCalls)
	}
}

func checkEmbedCalls(t *testing.T, calls []progressCall) {
	var embedCalls int
	for _, c := range calls {
		if strings.Contains(c.message, "Embedded") {
			embedCalls++
		}
	}
	if embedCalls == 0 {
		t.Fatal("expected at least 1 embed progress call")
	}
}

func checkLastCall(t *testing.T, last progressCall) {
	if !strings.Contains(last.message, "Indexing complete") {
		t.Errorf("last call should contain 'Indexing complete', got %q", last.message)
	}
	if last.current != last.total {
		t.Errorf("last call: expected current == total, got current=%d total=%d", last.current, last.total)
	}
}

func TestIndexer_ProgressFunc_Nil(t *testing.T) {
	// Verify that passing nil progress doesn't panic.
	projectDir := t.TempDir()
	writeGoFile(t, projectDir, "main.go", `package main

func Hello() {}
`)

	emb := &mockEmbedder{dims: 4, model: "test-model"}
	idx, err := NewIndexer(":memory:", emb, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()

	if _, err := idx.Index(context.Background(), projectDir, false, nil); err != nil {
		t.Fatal(err)
	}
}

func TestIndexer_IsFresh(t *testing.T) {
	projectDir := t.TempDir()
	writeGoFile(t, projectDir, "main.go", `package main

func Hello() {}
`)

	emb := &mockEmbedder{dims: 4, model: "test-model"}
	idx, err := NewIndexer(":memory:", emb, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()

	// Never indexed: should return false.
	fresh, err := idx.IsFresh(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if fresh {
		t.Fatal("expected IsFresh=false for never-indexed project")
	}

	// Index the project.
	if _, err := idx.Index(context.Background(), projectDir, false, nil); err != nil {
		t.Fatal(err)
	}

	// After indexing with no changes: should return true.
	fresh, err = idx.IsFresh(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if !fresh {
		t.Fatal("expected IsFresh=true after indexing with no changes")
	}

	// Modify a file: should return false.
	writeGoFile(t, projectDir, "main.go", `package main

func Hello() {}
func World() {}
`)
	fresh, err = idx.IsFresh(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if fresh {
		t.Fatal("expected IsFresh=false after modifying a file")
	}
}

func writeGoFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
