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
	"bufio"
	"cmp"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/ory/lumen/internal/config"
	"github.com/ory/lumen/internal/embedder"
	"github.com/ory/lumen/internal/index"
	"github.com/ory/lumen/internal/merkle"
	"github.com/ory/lumen/internal/store"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(stdioCmd)
}

var stdioCmd = &cobra.Command{
	Use:   "stdio",
	Short: "Start the MCP server on stdin/stdout",
	RunE:  runStdio,
}

// --- Tool input/output types ---

// SemanticSearchInput defines the parameters for the semantic_search tool.
type SemanticSearchInput struct {
	Query        string   `json:"query" jsonschema:"Natural language search query"`
	Path         string   `json:"path" jsonschema:"Absolute path to search in. Defaults to cwd. When a subdirectory of cwd, results are filtered to that subtree."`
	Cwd          string   `json:"cwd,omitempty" jsonschema:"The current working directory / project root. Used as index root when provided."`
	Limit        int      `json:"limit,omitempty" jsonschema:"Max results to return, default 20"`
	MinScore     *float64 `json:"min_score,omitempty" jsonschema:"Minimum score threshold (-1 to 1). Results below this score are excluded. Default 0.5. Use -1 to return all results."`
	ForceReindex bool     `json:"force_reindex,omitempty" jsonschema:"Force full re-index before searching"`
	Summary      bool     `json:"summary,omitempty" jsonschema:"When true, return only file path, symbol, kind, line range, and score — no code content. Useful for location-only queries."`
	MaxLines     int      `json:"max_lines,omitempty" jsonschema:"Truncate each code snippet to this many lines. Default: unlimited."`
}

// SearchResultItem represents a single search result returned to the caller.
type SearchResultItem struct {
	FilePath  string  `json:"file_path"`
	Symbol    string  `json:"symbol"`
	Kind      string  `json:"kind"`
	StartLine int     `json:"start_line"`
	EndLine   int     `json:"end_line"`
	Score     float32 `json:"score"`
	Content   string  `json:"content,omitempty"`
}

// SemanticSearchOutput is the structured output of the semantic_search tool.
type SemanticSearchOutput struct {
	Results      []SearchResultItem `json:"results"`
	Reindexed    bool               `json:"reindexed"`
	IndexedFiles int                `json:"indexed_files,omitempty"`
}

// IndexStatusInput defines the parameters for the index_status tool.
type IndexStatusInput struct {
	Path string `json:"path" jsonschema:"Absolute path to the project root. Defaults to cwd."`
	Cwd  string `json:"cwd,omitempty" jsonschema:"The current working directory / project root. Used as index root when provided."`
}

// IndexStatusOutput is the structured output of the index_status tool.
type IndexStatusOutput struct {
	ProjectPath    string `json:"project_path"`
	TotalFiles     int    `json:"total_files"`
	IndexedFiles   int    `json:"indexed_files"`
	TotalChunks    int    `json:"total_chunks"`
	LastIndexedAt  string `json:"last_indexed_at"`
	EmbeddingModel string `json:"embedding_model"`
	Stale          bool   `json:"stale"`
}

// HealthCheckInput defines the parameters for the health_check tool.
type HealthCheckInput struct{}

// HealthCheckOutput is the structured output of the health_check tool.
type HealthCheckOutput struct {
	Backend   string `json:"backend"`
	Host      string `json:"host"`
	Model     string `json:"model"`
	Reachable bool   `json:"reachable"`
	Message   string `json:"message"`
}

// --- indexerCache ---

// cacheEntry holds an indexer together with the effective root directory it
// was created for. When a subdirectory is aliased to a parent index, both
// the parent path and the subdirectory path map to the same cacheEntry, but
// effectiveRoot always points at the parent.
type cacheEntry struct {
	idx           *index.Indexer
	effectiveRoot string
}

// indexerCache manages one *index.Indexer per project path, creating them
// lazily with a shared embedder.
type indexerCache struct {
	mu       sync.RWMutex
	cache    map[string]cacheEntry
	embedder embedder.Embedder
	model    string
	cfg      config.Config
}

// findEffectiveRoot walks up the directory tree from path's parent to find an
// existing parent index (either in cache or on disk). Returns path unchanged
// if no parent index is found. Must be called under ic.mu write lock.
//
// A candidate parent is skipped when the relative path from that parent to
// path passes through a directory in merkle.SkipDirs (e.g. "testdata"). Such
// a parent index would never contain path's files, so it is not useful.
func (ic *indexerCache) findEffectiveRoot(path string) string {
	candidate := filepath.Dir(path)
	for {
		if !pathCrossesSkipDir(candidate, path) {
			if _, ok := ic.cache[candidate]; ok {
				return candidate
			}
			if _, err := os.Stat(config.DBPathForProject(candidate, ic.model)); err == nil {
				return candidate
			}
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			break
		}
		candidate = parent
	}
	return path
}

// pathCrossesSkipDir reports whether the relative path from root to sub passes
// through any directory whose base name is in merkle.SkipDirs.
func pathCrossesSkipDir(root, sub string) bool {
	rel, err := filepath.Rel(root, sub)
	if err != nil {
		return false
	}
	for part := range strings.SplitSeq(rel, string(filepath.Separator)) {
		if merkle.SkipDirs[part] {
			return true
		}
	}
	return false
}

// getOrCreate returns an existing Indexer for the given project path (or a
// parent index if one exists), along with the effective root directory used by
// the indexer. Creates a new indexer if none exists.
//
// When preferredRoot is non-empty it is used as the effective root directly,
// bypassing the findEffectiveRoot walk. This lets callers pass the known
// project root (e.g. cwd from Claude) so that sub-directory paths index the
// whole project.
func (ic *indexerCache) getOrCreate(projectPath string, preferredRoot string) (*index.Indexer, string, error) {
	// Fast path: read lock for already-cached indexers.
	ic.mu.RLock()
	if ic.cache != nil {
		if entry, ok := ic.cache[projectPath]; ok {
			ic.mu.RUnlock()
			return entry.idx, entry.effectiveRoot, nil
		}
	}
	ic.mu.RUnlock()

	// Slow path: acquire write lock to create.
	ic.mu.Lock()
	defer ic.mu.Unlock()

	if ic.cache == nil {
		ic.cache = make(map[string]cacheEntry)
	}
	// Double-check: another goroutine may have created it while we waited.
	if entry, ok := ic.cache[projectPath]; ok {
		return entry.idx, entry.effectiveRoot, nil
	}

	// Determine the effective root: prefer explicit root, then walk up.
	var effectiveRoot string
	if preferredRoot != "" {
		effectiveRoot = filepath.Clean(preferredRoot)
	} else {
		effectiveRoot = ic.findEffectiveRoot(projectPath)
	}

	// If a parent index is already cached, alias and return.
	if effectiveRoot != projectPath {
		if entry, ok := ic.cache[effectiveRoot]; ok {
			ic.cache[projectPath] = cacheEntry{idx: entry.idx, effectiveRoot: effectiveRoot}
			return entry.idx, effectiveRoot, nil
		}
	}

	dbPath := config.DBPathForProject(effectiveRoot, ic.model)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, "", fmt.Errorf("create db directory: %w", err)
	}

	idx, err := index.NewIndexer(dbPath, ic.embedder, ic.cfg.MaxChunkTokens)
	if err != nil {
		return nil, "", fmt.Errorf("create indexer: %w", err)
	}

	ic.cache[effectiveRoot] = cacheEntry{idx: idx, effectiveRoot: effectiveRoot}
	if effectiveRoot != projectPath {
		ic.cache[projectPath] = cacheEntry{idx: idx, effectiveRoot: effectiveRoot}
	}
	return idx, effectiveRoot, nil
}

// handleSemanticSearch is the tool handler for the semantic_search tool.
// Uses Out=any so the SDK does not set StructuredContent — the LLM sees
// only the plaintext in Content.
func (ic *indexerCache) handleSemanticSearch(ctx context.Context, req *mcp.CallToolRequest, input SemanticSearchInput) (*mcp.CallToolResult, any, error) {
	if err := validateSearchInput(&input); err != nil {
		return nil, nil, err
	}

	idx, effectiveRoot, err := ic.getOrCreate(input.Path, input.Cwd)
	if err != nil {
		return nil, nil, fmt.Errorf("get indexer: %w", err)
	}

	progress := buildProgressFunc(ctx, req)

	out, err := ic.ensureIndexed(ctx, idx, input, effectiveRoot, progress)
	if err != nil {
		return nil, nil, err
	}

	queryVec, err := ic.embedQuery(ctx, input.Query)
	if err != nil {
		return nil, nil, err
	}

	maxDistance := computeMaxDistance(input.MinScore)

	// When searching a subdirectory, filter results to that prefix only.
	var pathPrefix string
	if input.Path != effectiveRoot {
		if rel, relErr := filepath.Rel(effectiveRoot, input.Path); relErr == nil && rel != "." {
			pathPrefix = rel
		}
	}

	results, err := idx.Search(ctx, effectiveRoot, queryVec, input.Limit, maxDistance, pathPrefix)
	if err != nil {
		return nil, nil, fmt.Errorf("search: %w", err)
	}

	out.Results = make([]SearchResultItem, len(results))
	var snippets []string
	if !input.Summary {
		snippets = extractSnippets(effectiveRoot, results)
	}
	for i, r := range results {
		var content string
		if snippets != nil {
			content = snippets[i]
			if input.MaxLines > 0 && content != "" {
				content = truncateLines(content, input.MaxLines)
			}
		}
		out.Results[i] = SearchResultItem{
			FilePath:  r.FilePath,
			Symbol:    r.Symbol,
			Kind:      r.Kind,
			StartLine: r.StartLine,
			EndLine:   r.EndLine,
			Score:     float32(1.0 - r.Distance),
			Content:   content,
		}
	}

	text := formatSearchResults(input.Path, out)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}, nil, nil
}

func validateSearchInput(input *SemanticSearchInput) error {
	if input.Cwd != "" {
		input.Cwd = filepath.Clean(input.Cwd)
		if !filepath.IsAbs(input.Cwd) {
			return fmt.Errorf("cwd must be an absolute path")
		}
	}

	if input.Path == "" && input.Cwd != "" {
		input.Path = input.Cwd
	}
	if input.Path == "" {
		return fmt.Errorf("path is required (or provide cwd)")
	}

	if input.Cwd != "" && input.Path != input.Cwd {
		rel, err := filepath.Rel(input.Cwd, input.Path)
		if err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("path must be equal to or under cwd")
		}
	}

	if input.Query == "" {
		return fmt.Errorf("query is required")
	}
	if input.Limit <= 0 {
		input.Limit = 20
	}
	return nil
}

func buildProgressFunc(ctx context.Context, req *mcp.CallToolRequest) index.ProgressFunc {
	token := req.Params.GetProgressToken()
	if token == nil {
		return nil
	}
	return func(current, total int, message string) {
		_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
			ProgressToken: token,
			Progress:      float64(current),
			Total:         float64(total),
			Message:       message,
		})
	}
}

func (ic *indexerCache) ensureIndexed(ctx context.Context, idx *index.Indexer, input SemanticSearchInput, projectDir string, progress index.ProgressFunc) (SemanticSearchOutput, error) {
	out := SemanticSearchOutput{}
	if input.ForceReindex {
		stats, err := idx.Index(ctx, projectDir, true, progress)
		if err != nil {
			return out, fmt.Errorf("force reindex: %w", err)
		}
		out.Reindexed = true
		out.IndexedFiles = stats.IndexedFiles
		return out, nil
	}

	reindexed, stats, err := idx.EnsureFresh(ctx, projectDir, progress)
	if err != nil {
		return out, fmt.Errorf("ensure fresh: %w", err)
	}
	out.Reindexed = reindexed
	if reindexed {
		out.IndexedFiles = stats.IndexedFiles
	}
	return out, nil
}

func (ic *indexerCache) embedQuery(ctx context.Context, query string) ([]float32, error) {
	vecs, err := ic.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("embedder returned no vectors")
	}
	return vecs[0], nil
}

func computeMaxDistance(minScore *float64) float64 {
	if minScore == nil {
		return 0.5 // Default: 0.5 min_score
	}
	if *minScore > -1 {
		return 1.0 - *minScore
	}
	return 0 // -1 means no filter
}

// handleIndexStatus is the tool handler for the index_status tool.
// Uses Out=any so the SDK does not set StructuredContent.
func (ic *indexerCache) handleIndexStatus(_ context.Context, _ *mcp.CallToolRequest, input IndexStatusInput) (*mcp.CallToolResult, any, error) {
	if input.Path == "" && input.Cwd != "" {
		input.Path = input.Cwd
	}
	if input.Path == "" {
		return nil, nil, fmt.Errorf("path is required (or provide cwd)")
	}

	idx, effectiveRoot, err := ic.getOrCreate(input.Path, input.Cwd)
	if err != nil {
		return nil, nil, fmt.Errorf("get indexer: %w", err)
	}

	info, err := idx.Status(effectiveRoot)
	if err != nil {
		return nil, nil, fmt.Errorf("get status: %w", err)
	}

	out := IndexStatusOutput{
		ProjectPath:    info.ProjectPath,
		TotalFiles:     info.TotalFiles,
		IndexedFiles:   info.IndexedFiles,
		TotalChunks:    info.TotalChunks,
		LastIndexedAt:  info.LastIndexedAt,
		EmbeddingModel: info.EmbeddingModel,
	}

	fresh, err := idx.IsFresh(effectiveRoot)
	if err != nil {
		return nil, nil, fmt.Errorf("check freshness: %w", err)
	}
	out.Stale = !fresh

	text := formatIndexStatus(out)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}, nil, nil
}

// handleHealthCheck pings the configured embedding service and reports status.
func (ic *indexerCache) handleHealthCheck(ctx context.Context, _ *mcp.CallToolRequest, _ HealthCheckInput) (*mcp.CallToolResult, any, error) {
	host := ic.cfg.OllamaHost
	probeURL := host + "/api/tags"
	if ic.cfg.Backend == config.BackendLMStudio {
		host = ic.cfg.LMStudioHost
		probeURL = host + "/v1/models"
	}

	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, probeURL, nil)
	if err != nil {
		return healthResult(ic.cfg.Backend, host, ic.cfg.Model, false,
			fmt.Sprintf("failed to create request: %v", err)), nil, nil
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return healthResult(ic.cfg.Backend, host, ic.cfg.Model, false,
			fmt.Sprintf("service unreachable: %v", err)), nil, nil
	}
	_ = resp.Body.Close()

	if resp.StatusCode >= 500 {
		return healthResult(ic.cfg.Backend, host, ic.cfg.Model, false,
			fmt.Sprintf("service returned HTTP %d", resp.StatusCode)), nil, nil
	}

	return healthResult(ic.cfg.Backend, host, ic.cfg.Model, true, "service is healthy"), nil, nil
}

func healthResult(backend, host, model string, reachable bool, message string) *mcp.CallToolResult {
	status := "OK"
	if !reachable {
		status = "ERROR"
	}
	text := fmt.Sprintf("Backend: %s\nHost: %s\nModel: %s\nStatus: %s\nMessage: %s",
		backend, host, model, status, message)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
		IsError: !reachable,
	}
}

func extractSnippets(projectPath string, results []store.SearchResult) []string {
	snippets := make([]string, len(results))
	filesByPath := groupResultsByFile(results)

	for filePath, refs := range filesByPath {
		lines := readFileLines(projectPath, filePath)
		extractForFile(snippets, lines, refs)
	}

	return snippets
}

type resultRef struct {
	idx       int
	startLine int
	endLine   int
}

func groupResultsByFile(results []store.SearchResult) map[string][]resultRef {
	byFile := make(map[string][]resultRef)
	for i, r := range results {
		byFile[r.FilePath] = append(byFile[r.FilePath], resultRef{i, r.StartLine, r.EndLine})
	}
	return byFile
}

func readFileLines(projectPath, filePath string) []string {
	absPath := filepath.Join(projectPath, filePath)
	f, err := os.Open(absPath)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines
}

func extractForFile(snippets []string, lines []string, refs []resultRef) {
	for _, ref := range refs {
		start, end := normalizeLineRange(ref.startLine, ref.endLine, len(lines))
		if start >= end {
			continue
		}
		snippets[ref.idx] = strings.Join(lines[start:end], "\n")
	}
}

// truncateLines returns at most maxLines lines from a string.
func truncateLines(s string, maxLines int) string {
	lines := strings.SplitN(s, "\n", maxLines+1)
	if len(lines) <= maxLines {
		return s
	}
	return strings.Join(lines[:maxLines], "\n")
}

func normalizeLineRange(startLine, endLine, totalLines int) (int, int) {
	start := max(startLine-1, 0)
	end := min(endLine, totalLines)
	return start, end
}

var xmlEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
)

// formatSearchResults builds an XML-tagged representation of search results
// for LLM consumption. File paths are shown relative to the project root.
// Chunks from the same file are grouped under a <result:file> element to
// reduce repetition.
func formatSearchResults(projectPath string, out SemanticSearchOutput) string {
	if len(out.Results) == 0 {
		var b strings.Builder
		b.WriteString("No results found.")
		if out.Reindexed {
			fmt.Fprintf(&b, " (indexed %d files)", out.IndexedFiles)
		}
		return b.String()
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Found %d results", len(out.Results))
	if out.Reindexed {
		fmt.Fprintf(&b, " (indexed %d files)", out.IndexedFiles)
	}
	b.WriteString(":\n")

	// Group results by relative file path.
	type fileGroup struct {
		rel      string
		results  []SearchResultItem
		maxScore float32
	}
	var order []string
	groups := make(map[string]*fileGroup)
	for _, r := range out.Results {
		rel, err := filepath.Rel(projectPath, r.FilePath)
		if err != nil {
			rel = r.FilePath
		}
		if _, ok := groups[rel]; !ok {
			order = append(order, rel)
			groups[rel] = &fileGroup{rel: rel}
		}
		g := groups[rel]
		g.results = append(g.results, r)
		if r.Score > g.maxScore {
			g.maxScore = r.Score
		}
	}

	// Sort files by best chunk score descending.
	slices.SortFunc(order, func(a, b string) int {
		return cmp.Compare(groups[b].maxScore, groups[a].maxScore)
	})

	for _, rel := range order {
		g := groups[rel]
		// Sort chunks within each file by score descending.
		slices.SortFunc(g.results, func(a, b SearchResultItem) int {
			return cmp.Compare(b.Score, a.Score)
		})
		fmt.Fprintf(&b, "\n<result:file filename=\"%s\">\n", xmlEscaper.Replace(g.rel))
		for _, r := range g.results {
			fmt.Fprintf(&b, "  <result:chunk line-start=\"%d\" line-end=\"%d\" symbol=\"%s\" kind=\"%s\" score=\"%.2f\">\n",
				r.StartLine, r.EndLine, xmlEscaper.Replace(r.Symbol), xmlEscaper.Replace(r.Kind), r.Score)
			if r.Content != "" {
				b.WriteString(r.Content)
				b.WriteByte('\n')
			}
			b.WriteString("  </result:chunk>\n")
		}
		b.WriteString("</result:file>")
	}

	return b.String()
}

// formatIndexStatus builds a compact plaintext representation of index status.
func formatIndexStatus(out IndexStatusOutput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Index: %s\n", out.ProjectPath)
	fmt.Fprintf(&b, "Files: %d | Indexed: %d | Chunks: %d | Model: %s\n", out.TotalFiles, out.IndexedFiles, out.TotalChunks, out.EmbeddingModel)
	stale := "no"
	if out.Stale {
		stale = "yes"
	}
	lastIndexed := out.LastIndexedAt
	if lastIndexed == "" {
		lastIndexed = "never"
	}
	fmt.Fprintf(&b, "Last indexed: %s | Stale: %s", lastIndexed, stale)
	return b.String()
}

func runStdio(_ *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	emb, err := newEmbedder(cfg)
	if err != nil {
		return fmt.Errorf("create embedder: %w", err)
	}

	indexers := &indexerCache{embedder: emb, model: cfg.Model, cfg: cfg}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "lumen",
		Version: "0.1.0",
	}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name: "semantic_search",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: true,
			Title:        "Semantic Code Search",
		},
		Description: `Search indexed codebase using natural language. ALWAYS use semantic_search as the FIRST tool for code discovery and exploration.

Do NOT default to Grep, Glob, or Read for search tasks — only use them for exact literal string lookups.

Before using Search, Grep, Glob, Find, or Read for any search, stop and ask:

> "Do I already know the exact literal string I'm searching for?"

- **No** — understanding how something works, finding where something is implemented, exploring
  unfamiliar code → use **semantic search**
- **Yes** — a specific function name, import path, variable name, or error message you already
  know exists → Grep/Glob is acceptable for that exact string only

# ALWAYS use semantic search as the first tool for code discovery

This includes:

- Understanding how a system or feature works
- Finding where functionality is implemented
- Discovering what calls what or how components connect
- Locating code related to a concept or domain term
- Finding relevant code before making changes

Auto-indexes if the index is stale or empty.

Tip: If a search returns no results, retry with a lower min_score (e.g. 0.0 or -1) before trying a completely different query.`,
	}, indexers.handleSemanticSearch)

	mcp.AddTool(server, &mcp.Tool{
		Name: "health_check",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: true,
			Title:        "Embedding Service Health Check",
		},
		Description: `Check if the configured embedding service (Ollama or LM Studio) is reachable and healthy.

Reports backend type, host, model name, and connection status. Use this to diagnose embedding failures or verify service availability.`,
	}, indexers.handleHealthCheck)

	mcp.AddTool(server, &mcp.Tool{
		Name: "index_status",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: true,
			Title:        "Code Index Status",
		},
		Description: `Check the indexing status of a project. Shows total files, indexed chunks, and embedding model.

Use this to verify a project is indexed before searching, or to check if the index is up to date.

Note: You do NOT need to call index_status before semantic_search. Semantic search auto-indexes automatically. Only use this tool when the user explicitly asks about index status, or to diagnose why search results seem incomplete.`,
	}, indexers.handleIndexStatus)

	return server.Run(context.Background(), &mcp.StdioTransport{})
}
