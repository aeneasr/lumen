// Package index orchestrates chunking, embedding, and storage for code indexes.
package index

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/foobar/agent-index-go/internal/chunker"
	"github.com/foobar/agent-index-go/internal/embedder"
	"github.com/foobar/agent-index-go/internal/merkle"
	"github.com/foobar/agent-index-go/internal/store"
)

// IndexStats holds statistics from an indexing run.
type IndexStats struct {
	TotalFiles    int
	IndexedFiles  int
	ChunksCreated int
	FilesChanged  int
}

// StatusInfo holds information about the current index state for a project.
type StatusInfo struct {
	ProjectPath    string
	TotalFiles     int
	IndexedFiles   int
	TotalChunks    int
	LastIndexedAt  string
	EmbeddingModel string
}

// Indexer orchestrates chunking, embedding, and storage for a code index.
type Indexer struct {
	store   *store.Store
	emb     embedder.Embedder
	chunker chunker.Chunker
}

// NewIndexer creates a new Indexer backed by a SQLite store at dsn,
// using the given embedder for vector generation.
func NewIndexer(dsn string, emb embedder.Embedder) (*Indexer, error) {
	s, err := store.New(dsn, emb.Dimensions())
	if err != nil {
		return nil, fmt.Errorf("create store: %w", err)
	}
	return &Indexer{
		store:   s,
		emb:     emb,
		chunker: chunker.NewMultiChunker(chunker.DefaultLanguages()),
	}, nil
}

// Close closes the underlying store.
func (idx *Indexer) Close() error {
	return idx.store.Close()
}

// Index indexes the project at projectDir. If force is true, all files are
// re-indexed regardless of whether they have changed.
func (idx *Indexer) Index(ctx context.Context, projectDir string, force bool) (IndexStats, error) {
	curTree, err := merkle.BuildTree(projectDir, merkle.MakeSkip(projectDir, chunker.SupportedExtensions()))
	if err != nil {
		return IndexStats{}, fmt.Errorf("build merkle tree: %w", err)
	}
	// If not forcing, check root hash before doing any work.
	if !force {
		storedHash, err := idx.store.GetMeta("root_hash")
		if err != nil && err != sql.ErrNoRows {
			return IndexStats{}, fmt.Errorf("get root_hash: %w", err)
		}
		if storedHash == curTree.RootHash {
			return IndexStats{}, nil
		}
	}
	return idx.indexWithTree(ctx, projectDir, force, curTree)
}

// EnsureFresh checks if the index is stale and re-indexes if needed.
// Returns whether a re-index occurred, the stats, and any error.
func (idx *Indexer) EnsureFresh(ctx context.Context, projectDir string) (bool, IndexStats, error) {
	curTree, err := merkle.BuildTree(projectDir, merkle.MakeSkip(projectDir, chunker.SupportedExtensions()))
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

	stats.TotalFiles = len(curTree.Files)

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
	if err := idx.store.SetMeta("total_files", strconv.Itoa(stats.TotalFiles)); err != nil {
		return stats, fmt.Errorf("set total_files: %w", err)
	}

	return stats, nil
}

// Search performs a vector similarity search against the index.
func (idx *Indexer) Search(ctx context.Context, projectDir string, queryVec []float32, limit int) ([]store.SearchResult, error) {
	return idx.store.Search(queryVec, limit)
}

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
