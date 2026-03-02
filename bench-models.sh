#!/usr/bin/env bash
# bench-models.sh — run bench-mcp.sh for three embedding model configurations
#   1. qwen3-embedding:8b     (Ollama)
#   2. ordis/jina-embeddings-v2-base-code  (Ollama default)
#   3. nomic-ai/nomic-embed-code-GGUF      (LM Studio default)
set -eufo pipefail

REPO="$(cd "$(dirname "$0")" && pwd)"

# Forward any extra flags (e.g. --claude-model, --question) to each run
EXTRA_ARGS=("$@")

run_bench() {
  local label="$1"
  local embed_model="$2"
  echo ""
  echo "════════════════════════════════════════════════════════════"
  echo "  Embed model: $label"
  echo "════════════════════════════════════════════════════════════"
  bash "$REPO/bench-mcp.sh" --embed-model "$embed_model" "${EXTRA_ARGS[@]:+${EXTRA_ARGS[@]}}"
}

run_bench "ordis/jina-embeddings-v2-base-code (Ollama)" "ordis/jina-embeddings-v2-base-code"
run_bench "qwen3-embedding:8b (Ollama)"                 "qwen3-embedding:8b"
run_bench "qwen3-embedding:8b (Ollama)"                 "qwen3-embedding:4b"
run_bench "nomic-ai/nomic-embed-code-GGUF (LM Studio)"  "nomic-ai/nomic-embed-code-GGUF"
