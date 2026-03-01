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
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/aeneasr/agent-index/internal/config"
	"github.com/aeneasr/agent-index/internal/embedder"
	"github.com/aeneasr/agent-index/internal/index"
	"github.com/spf13/cobra"
)

func init() {
	indexCmd.Flags().StringP("model", "m", "", "embedding model (default: $AGENT_INDEX_EMBED_MODEL or "+embedder.DefaultModel+")")
	indexCmd.Flags().BoolP("force", "f", false, "force full re-index")
	rootCmd.AddCommand(indexCmd)
}

var indexCmd = &cobra.Command{
	Use:   "index <project-path>",
	Short: "Index a project for semantic search",
	Args:  cobra.ExactArgs(1),
	RunE:  runIndex,
}

func runIndex(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	if m, _ := cmd.Flags().GetString("model"); m != "" {
		spec, ok := embedder.KnownModels[m]
		if !ok {
			return fmt.Errorf("unknown embedding model %q", m)
		}
		cfg.Model = m
		cfg.Dims = spec.Dims
		cfg.CtxLength = spec.CtxLength
	}

	projectPath, err := filepath.Abs(args[0])
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}

	emb, err := embedder.NewOllama(cfg.Model, cfg.Dims, cfg.CtxLength, cfg.OllamaHost)
	if err != nil {
		return fmt.Errorf("create embedder: %w", err)
	}

	dbPath := config.DBPathForProject(projectPath, cfg.Model)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return fmt.Errorf("create db directory: %w", err)
	}

	idx, err := index.NewIndexer(dbPath, emb, cfg.MaxChunkTokens)
	if err != nil {
		return fmt.Errorf("create indexer: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Indexing %s (model: %s, dims: %d)\n", projectPath, cfg.Model, cfg.Dims)

	progress := func(current, total int, message string) {
		fmt.Fprintf(os.Stderr, "  [%d/%d] %s\n", current, total, message)
	}

	start := time.Now()

	force, _ := cmd.Flags().GetBool("force")
	var stats index.IndexStats
	if force {
		stats, err = idx.Index(context.Background(), projectPath, true, progress)
	} else {
		var reindexed bool
		reindexed, stats, err = idx.EnsureFresh(context.Background(), projectPath, progress)
		if err == nil && !reindexed {
			fmt.Fprintln(os.Stdout, "Index is already up to date.")
			return nil
		}
	}
	if err != nil {
		return fmt.Errorf("indexing: %w", err)
	}

	fmt.Fprintf(os.Stdout, "Done. Indexed %d files, %d chunks in %s.\n",
		stats.IndexedFiles, stats.ChunksCreated, time.Since(start).Round(time.Millisecond))
	return nil
}
