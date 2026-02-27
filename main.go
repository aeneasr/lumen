package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/foobar/agent-index-go/internal/embedder"
	"github.com/foobar/agent-index-go/internal/index"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// --- Tool input/output types ---

// SemanticSearchInput defines the parameters for the semantic_search tool.
type SemanticSearchInput struct {
	Query        string `json:"query" jsonschema:"Natural language search query"`
	Path         string `json:"path" jsonschema:"Absolute path to the project root"`
	Limit        int    `json:"limit,omitempty" jsonschema:"Max results to return, default 10"`
	Kind         string `json:"kind,omitempty" jsonschema:"Filter by chunk kind: function method type interface const var"`
	ForceReindex bool   `json:"force_reindex,omitempty" jsonschema:"Force full re-index before searching"`
}

// SearchResultItem represents a single search result returned to the caller.
type SearchResultItem struct {
	FilePath  string  `json:"file_path"`
	Symbol    string  `json:"symbol"`
	Kind      string  `json:"kind"`
	StartLine int     `json:"start_line"`
	EndLine   int     `json:"end_line"`
	Score     float32 `json:"score"`
}

// SemanticSearchOutput is the structured output of the semantic_search tool.
type SemanticSearchOutput struct {
	Results      []SearchResultItem `json:"results"`
	Reindexed    bool               `json:"reindexed"`
	IndexedFiles int                `json:"indexed_files,omitempty"`
}

// IndexStatusInput defines the parameters for the index_status tool.
type IndexStatusInput struct {
	Path string `json:"path" jsonschema:"Absolute path to the project root"`
}

// IndexStatusOutput is the structured output of the index_status tool.
type IndexStatusOutput struct {
	ProjectPath    string `json:"project_path"`
	TotalFiles     int    `json:"total_files"`
	IndexedFiles   int    `json:"indexed_files"`
	TotalChunks    int    `json:"total_chunks"`
	StaleFiles     int    `json:"stale_files"`
	LastIndexedAt  string `json:"last_indexed_at"`
	EmbeddingModel string `json:"embedding_model"`
}

// --- indexerCache ---

// indexerCache manages one *index.Indexer per project path, creating them
// lazily with a shared embedder.
type indexerCache struct {
	mu       sync.RWMutex
	cache    map[string]*index.Indexer
	embedder embedder.Embedder
}

// getOrCreate returns an existing Indexer for the given project path, or
// creates a new one backed by a SQLite database in the XDG data directory.
func (ic *indexerCache) getOrCreate(projectPath string) (*index.Indexer, error) {
	// Fast path: read lock for already-cached indexers.
	ic.mu.RLock()
	if ic.cache != nil {
		if idx, ok := ic.cache[projectPath]; ok {
			ic.mu.RUnlock()
			return idx, nil
		}
	}
	ic.mu.RUnlock()

	// Slow path: acquire write lock to create.
	ic.mu.Lock()
	defer ic.mu.Unlock()

	if ic.cache == nil {
		ic.cache = make(map[string]*index.Indexer)
	}
	// Double-check: another goroutine may have created it while we waited.
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

// handleSemanticSearch is the tool handler for the semantic_search tool.
func (ic *indexerCache) handleSemanticSearch(ctx context.Context, req *mcp.CallToolRequest, input SemanticSearchInput) (*mcp.CallToolResult, SemanticSearchOutput, error) {
	var out SemanticSearchOutput

	if input.Path == "" {
		return nil, out, fmt.Errorf("path is required")
	}
	if input.Query == "" {
		return nil, out, fmt.Errorf("query is required")
	}
	if input.Limit <= 0 {
		input.Limit = 10
	}

	idx, err := ic.getOrCreate(input.Path)
	if err != nil {
		return nil, out, fmt.Errorf("get indexer: %w", err)
	}

	if input.ForceReindex {
		stats, err := idx.Index(ctx, input.Path, true)
		if err != nil {
			return nil, out, fmt.Errorf("force reindex: %w", err)
		}
		out.Reindexed = true
		out.IndexedFiles = stats.IndexedFiles
	} else {
		reindexed, stats, err := idx.EnsureFresh(ctx, input.Path)
		if err != nil {
			return nil, out, fmt.Errorf("ensure fresh: %w", err)
		}
		out.Reindexed = reindexed
		if reindexed {
			out.IndexedFiles = stats.IndexedFiles
		}
	}

	// Embed the query text.
	vecs, err := ic.embedder.Embed(ctx, []string{input.Query})
	if err != nil {
		return nil, out, fmt.Errorf("embed query: %w", err)
	}
	if len(vecs) == 0 {
		return nil, out, fmt.Errorf("embedder returned no vectors")
	}
	queryVec := vecs[0]

	// Search the index.
	results, err := idx.Search(ctx, input.Path, queryVec, input.Limit, input.Kind)
	if err != nil {
		return nil, out, fmt.Errorf("search: %w", err)
	}

	// Map store.SearchResult to SearchResultItem.
	out.Results = make([]SearchResultItem, len(results))
	for i, r := range results {
		out.Results[i] = SearchResultItem{
			FilePath:  r.FilePath,
			Symbol:    r.Symbol,
			Kind:      r.Kind,
			StartLine: r.StartLine,
			EndLine:   r.EndLine,
			Score:     float32(r.Distance),
		}
	}

	return nil, out, nil
}

// handleIndexStatus is the tool handler for the index_status tool.
func (ic *indexerCache) handleIndexStatus(ctx context.Context, req *mcp.CallToolRequest, input IndexStatusInput) (*mcp.CallToolResult, IndexStatusOutput, error) {
	var out IndexStatusOutput

	if input.Path == "" {
		return nil, out, fmt.Errorf("path is required")
	}

	idx, err := ic.getOrCreate(input.Path)
	if err != nil {
		return nil, out, fmt.Errorf("get indexer: %w", err)
	}

	info, err := idx.Status(input.Path)
	if err != nil {
		return nil, out, fmt.Errorf("get status: %w", err)
	}

	out.ProjectPath = info.ProjectPath
	out.TotalFiles = info.TotalFiles
	out.IndexedFiles = info.IndexedFiles
	out.TotalChunks = info.TotalChunks
	out.StaleFiles = info.StaleFiles
	out.LastIndexedAt = info.LastIndexedAt
	out.EmbeddingModel = info.EmbeddingModel

	return nil, out, nil
}

// --- Helpers ---

// dbPathForProject returns the SQLite database path for a given project,
// derived from a SHA-256 hash of the project path stored under the XDG
// data directory.
func dbPathForProject(projectPath string) string {
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(projectPath)))
	dataDir := xdgDataDir()
	return filepath.Join(dataDir, "agent-index", hash[:16], "index.db")
}

// xdgDataDir returns the XDG data home directory, defaulting to
// ~/.local/share if XDG_DATA_HOME is not set.
func xdgDataDir() string {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share")
}

// envOrDefault returns the value of the environment variable named by key,
// or fallback if the variable is not set or empty.
func envOrDefault(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

// --- main ---

func main() {
	model := envOrDefault("AGENT_INDEX_EMBED_MODEL", "nomic-embed-text")
	dims := 1024
	ollamaHost := envOrDefault("OLLAMA_HOST", "http://localhost:11434")

	emb, err := embedder.NewOllama(model, dims, ollamaHost)
	if err != nil {
		log.Fatalf("create embedder: %v", err)
	}

	indexers := &indexerCache{embedder: emb}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "agent-index",
		Version: "0.1.0",
	}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "semantic_search",
		Description: "Search indexed codebase using natural language. Returns file paths and line ranges of semantically matching code chunks. Auto-indexes if the index is stale or empty.",
	}, indexers.handleSemanticSearch)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "index_status",
		Description: "Check the indexing status of a project. Shows total files, indexed chunks, stale files, and embedding model.",
	}, indexers.handleIndexStatus)

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
