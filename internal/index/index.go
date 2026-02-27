package index

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
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
	StaleFiles     int
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
		chunker: chunker.NewGoAST(),
	}, nil
}

// Close closes the underlying store.
func (idx *Indexer) Close() error {
	return idx.store.Close()
}

// Index indexes the project at projectDir. If force is true, all files are
// re-indexed regardless of whether they have changed. Returns statistics
// about the indexing run.
func (idx *Indexer) Index(ctx context.Context, projectDir string, force bool) (IndexStats, error) {
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

	// Build the current merkle tree.
	curTree, err := merkle.BuildTree(projectDir, nil)
	if err != nil {
		return stats, fmt.Errorf("build merkle tree: %w", err)
	}
	stats.TotalFiles = len(curTree.Files)

	// If not forcing, check if the root hash matches (nothing changed).
	if !force {
		storedHash, err := idx.store.GetMeta("root_hash")
		if err != nil && err != sql.ErrNoRows {
			return stats, fmt.Errorf("get root_hash: %w", err)
		}
		if storedHash == curTree.RootHash {
			// Everything is up to date.
			return stats, nil
		}
	}

	// Determine which files need processing.
	var filesToIndex []string
	var filesToRemove []string

	if force {
		// Index everything.
		for path := range curTree.Files {
			filesToIndex = append(filesToIndex, path)
		}
	} else {
		// Diff against stored file hashes.
		oldHashes, err := idx.store.GetFileHashes()
		if err != nil {
			return stats, fmt.Errorf("get file hashes: %w", err)
		}
		oldTree := &merkle.Tree{Files: oldHashes}
		added, removed, modified := merkle.Diff(oldTree, curTree)
		filesToIndex = append(added, modified...)
		filesToRemove = removed
	}

	stats.FilesChanged = len(filesToIndex) + len(filesToRemove)

	// Remove chunks for deleted files.
	for _, path := range filesToRemove {
		if err := idx.store.DeleteFileChunks(path); err != nil {
			return stats, fmt.Errorf("delete chunks for %s: %w", path, err)
		}
	}

	// Chunk all files that need indexing.
	var allChunks []chunker.Chunk
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

		allChunks = append(allChunks, chunks...)

		// Update file hash in the store.
		if err := idx.store.UpsertFile(relPath, curTree.Files[relPath]); err != nil {
			return stats, fmt.Errorf("upsert file %s: %w", relPath, err)
		}
	}

	stats.IndexedFiles = len(filesToIndex)
	stats.ChunksCreated = len(allChunks)

	// Embed all chunks in a batch.
	if len(allChunks) > 0 {
		texts := make([]string, len(allChunks))
		for i, c := range allChunks {
			texts[i] = c.Content
		}

		vectors, err := idx.emb.Embed(ctx, texts)
		if err != nil {
			return stats, fmt.Errorf("embed chunks: %w", err)
		}

		if err := idx.store.InsertChunks(allChunks, vectors); err != nil {
			return stats, fmt.Errorf("insert chunks: %w", err)
		}
	}

	// Update metadata.
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

// Search performs a vector similarity search against the index.
func (idx *Indexer) Search(ctx context.Context, projectDir string, queryVec []float32, limit int, kindFilter string) ([]store.SearchResult, error) {
	return idx.store.Search(queryVec, limit, kindFilter)
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

	stats, err := idx.Index(ctx, projectDir, false)
	if err != nil {
		return false, stats, err
	}
	return true, stats, nil
}

// Status returns information about the current index state for a project.
func (idx *Indexer) Status(projectDir string) (StatusInfo, error) {
	var info StatusInfo
	info.ProjectPath = projectDir

	// Get store statistics.
	storeStats, err := idx.store.Stats()
	if err != nil {
		return info, fmt.Errorf("get store stats: %w", err)
	}
	info.IndexedFiles = storeStats.TotalFiles
	info.TotalChunks = storeStats.TotalChunks

	// Get metadata.
	info.EmbeddingModel, _ = idx.store.GetMeta("embedding_model")
	info.LastIndexedAt, _ = idx.store.GetMeta("last_indexed_at")

	// Build current tree to get total files and detect stale files.
	curTree, err := merkle.BuildTree(projectDir, nil)
	if err != nil {
		return info, fmt.Errorf("build merkle tree: %w", err)
	}
	info.TotalFiles = len(curTree.Files)

	// Diff to find stale files.
	oldHashes, err := idx.store.GetFileHashes()
	if err != nil {
		return info, fmt.Errorf("get file hashes: %w", err)
	}
	oldTree := &merkle.Tree{Files: oldHashes}
	added, removed, modified := merkle.Diff(oldTree, curTree)
	info.StaleFiles = len(added) + len(removed) + len(modified)

	return info, nil
}
