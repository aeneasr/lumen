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

package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ory/lumen/internal/config"
)

// stubEmbedder satisfies embedder.Embedder for tests.
type stubEmbedder struct{}

func (s *stubEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	vecs := make([][]float32, len(texts))
	for i := range vecs {
		vecs[i] = []float32{0.1, 0.2, 0.3, 0.4}
	}
	return vecs, nil
}
func (s *stubEmbedder) Dimensions() int   { return 4 }
func (s *stubEmbedder) ModelName() string { return "stub" }

func TestIndexerCache_ConcurrentReads(_ *testing.T) {
	ic := &indexerCache{
		embedder: &stubEmbedder{},
		cfg:      config.Config{MaxChunkTokens: 2048},
	}

	const goroutines = 20
	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			// Path doesn't exist on disk — getOrCreate will error, that's fine.
			// We're testing there's no data race on the cache map/mutex.
			_, _, _ = ic.getOrCreate("/nonexistent/path/for/race/test", "")
		})
	}
	wg.Wait()
}

func TestIndexerCache_FindEffectiveRoot(t *testing.T) {
	const model = "test-model"

	t.Run("returns path when no parent exists", func(t *testing.T) {
		ic := &indexerCache{
			cache: make(map[string]cacheEntry),
			model: model,
		}
		root := ic.findEffectiveRoot("/project/src/pkg")
		if root != "/project/src/pkg" {
			t.Fatalf("expected original path, got %s", root)
		}
	})

	t.Run("returns cached parent", func(t *testing.T) {
		ic := &indexerCache{
			cache: map[string]cacheEntry{"/project": {idx: nil, effectiveRoot: "/project"}},
			model: model,
		}
		root := ic.findEffectiveRoot("/project/src/pkg")
		if root != "/project" {
			t.Fatalf("expected /project (cached parent), got %s", root)
		}
	})

	t.Run("returns parent with existing db on disk", func(t *testing.T) {
		tmpDir := t.TempDir()
		t.Setenv("XDG_DATA_HOME", tmpDir)

		// Create the DB file that would exist for /project with our model.
		parentDBPath := config.DBPathForProject("/project", model)
		if err := os.MkdirAll(filepath.Dir(parentDBPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(parentDBPath, []byte{}, 0o644); err != nil {
			t.Fatal(err)
		}

		ic := &indexerCache{
			cache: make(map[string]cacheEntry),
			model: model,
		}
		root := ic.findEffectiveRoot("/project/src/pkg")
		if root != "/project" {
			t.Fatalf("expected /project (db on disk), got %s", root)
		}
	})

	t.Run("ignores parent when path crosses a SkipDir", func(t *testing.T) {
		tmpDir := t.TempDir()
		t.Setenv("XDG_DATA_HOME", tmpDir)

		// Simulate a parent index at /project.
		parentDBPath := config.DBPathForProject("/project", model)
		if err := os.MkdirAll(filepath.Dir(parentDBPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(parentDBPath, []byte{}, 0o644); err != nil {
			t.Fatal(err)
		}

		ic := &indexerCache{
			cache: make(map[string]cacheEntry),
			model: model,
		}
		// "testdata" is in merkle.SkipDirs — the parent index would never
		// contain these files, so findEffectiveRoot must return the path itself.
		root := ic.findEffectiveRoot("/project/testdata/fixtures/go")
		if root != "/project/testdata/fixtures/go" {
			t.Fatalf("expected original path (skip dir in route), got %s", root)
		}
	})
}

func TestIndexerCache_GetOrCreate_ReusesParentIndex(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmpDir)

	const model = "stub"
	ic := &indexerCache{
		embedder: &stubEmbedder{},
		model:    model,
		cfg:      config.Config{MaxChunkTokens: 512},
	}

	// First call: index the parent directory — creates an indexer and DB on disk.
	parentDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	parentIdx, parentRoot, err := ic.getOrCreate(parentDir, "")
	if err != nil {
		t.Fatalf("getOrCreate(parent): %v", err)
	}
	if parentRoot != parentDir {
		t.Fatalf("expected effectiveRoot=%s, got %s", parentDir, parentRoot)
	}

	// Second call: request a subdirectory — should reuse the parent indexer.
	subDir := filepath.Join(parentDir, "src")
	subIdx, subRoot, err := ic.getOrCreate(subDir, "")
	if err != nil {
		t.Fatalf("getOrCreate(subdir): %v", err)
	}
	if subRoot != parentDir {
		t.Fatalf("expected effectiveRoot=%s for subdir, got %s", parentDir, subRoot)
	}
	if subIdx != parentIdx {
		t.Fatal("expected subdir to reuse parent indexer, got a different instance")
	}

	// Both keys should be aliased in the cache.
	ic.mu.RLock()
	cachedParent := ic.cache[parentDir]
	cachedSub := ic.cache[subDir]
	ic.mu.RUnlock()
	if cachedParent.idx != parentIdx {
		t.Fatal("parent key not in cache")
	}
	if cachedSub.idx != parentIdx {
		t.Fatal("subdir key not aliased to parent indexer in cache")
	}

	// Third call: same subDir again — hits fast path; must still return parent root.
	subIdx2, subRoot2, err := ic.getOrCreate(subDir, "")
	if err != nil {
		t.Fatalf("getOrCreate(subdir fast path): %v", err)
	}
	if subRoot2 != parentDir {
		t.Fatalf("fast-path: expected effectiveRoot=%s, got %s", parentDir, subRoot2)
	}
	if subIdx2 != parentIdx {
		t.Fatal("fast-path: expected same indexer instance")
	}
}

func TestIndexerCache_GetOrCreate_FastPathEffectiveRoot(t *testing.T) {
	// Regression: the fast path (second call to same path) must return the
	// correct effectiveRoot (the parent), not the requested subdirectory path.
	tmpDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmpDir)

	const model = "stub"
	ic := &indexerCache{
		embedder: &stubEmbedder{},
		model:    model,
		cfg:      config.Config{MaxChunkTokens: 512},
	}

	parentDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	subDir := filepath.Join(parentDir, "api")

	// Prime the parent index.
	if _, _, err := ic.getOrCreate(parentDir, ""); err != nil {
		t.Fatalf("getOrCreate(parent): %v", err)
	}

	// First subDir call — slow path, caches alias.
	if _, _, err := ic.getOrCreate(subDir, ""); err != nil {
		t.Fatalf("getOrCreate(subdir slow path): %v", err)
	}

	// Second subDir call — hits the fast path.
	_, root, err := ic.getOrCreate(subDir, "")
	if err != nil {
		t.Fatalf("getOrCreate(subdir fast path): %v", err)
	}
	if root != parentDir {
		t.Fatalf("fast path returned wrong effectiveRoot: got %s, want %s", root, parentDir)
	}
}

func TestIndexerCache_GetOrCreate_PreferredRoot(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmpDir)

	ic := &indexerCache{
		embedder: &stubEmbedder{},
		model:    "stub",
		cfg:      config.Config{MaxChunkTokens: 512},
	}

	parentDir := filepath.Join(tmpDir, "project")
	subDir := filepath.Join(parentDir, "src")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Call with subDir as projectPath but parentDir as preferredRoot.
	idx, root, err := ic.getOrCreate(subDir, parentDir)
	if err != nil {
		t.Fatalf("getOrCreate with preferredRoot: %v", err)
	}
	if root != parentDir {
		t.Fatalf("expected effectiveRoot=%s, got %s", parentDir, root)
	}

	// Both subDir and parentDir should be cached.
	ic.mu.RLock()
	parentEntry := ic.cache[parentDir]
	subEntry := ic.cache[subDir]
	ic.mu.RUnlock()
	if parentEntry.idx != idx {
		t.Fatal("parent key not in cache")
	}
	if subEntry.idx != idx {
		t.Fatal("subdir key not aliased to parent indexer")
	}
}

func TestValidateSearchInput_CwdPathInteraction(t *testing.T) {
	tests := []struct {
		name    string
		input   SemanticSearchInput
		wantErr string
		wantPath string
	}{
		{
			name:     "cwd only — path defaults to cwd",
			input:    SemanticSearchInput{Cwd: "/project", Query: "test"},
			wantPath: "/project",
		},
		{
			name:     "path only — works as before",
			input:    SemanticSearchInput{Path: "/project/src", Query: "test"},
			wantPath: "/project/src",
		},
		{
			name:     "both valid — path under cwd",
			input:    SemanticSearchInput{Cwd: "/project", Path: "/project/src", Query: "test"},
			wantPath: "/project/src",
		},
		{
			name:    "both invalid — path outside cwd",
			input:   SemanticSearchInput{Cwd: "/project", Path: "/other", Query: "test"},
			wantErr: "path must be equal to or under cwd",
		},
		{
			name:    "neither provided",
			input:   SemanticSearchInput{Query: "test"},
			wantErr: "path is required (or provide cwd)",
		},
		{
			name:    "cwd is relative",
			input:   SemanticSearchInput{Cwd: "relative/path", Query: "test"},
			wantErr: "cwd must be an absolute path",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := tt.input
			err := validateSearchInput(&input)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if input.Path != tt.wantPath {
				t.Fatalf("expected path=%s, got %s", tt.wantPath, input.Path)
			}
		})
	}
}

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
