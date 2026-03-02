#!/usr/bin/env bash
# bench-mcp.sh — benchmark baseline vs lumen MCP across questions and models
set -eufo pipefail

REPO="$(cd "$(dirname "$0")" && pwd)"
FIXTURES_GO="$REPO/testdata/fixtures/go"
FIXTURES_PY="$REPO/testdata/fixtures/python"
FIXTURES_TS="$REPO/testdata/fixtures/ts"
BINARY="$REPO/lumen"

# ── Questions (3 languages × 1 hard question each) ───────────────────────────
QUESTIONS=(
  # Go (Prometheus fixtures)
  "How does TSDB compaction work end-to-end? Explain the Compactor interface, LeveledCompactor, and how the DB triggers compaction. Show relevant types, interfaces, and key method signatures."
  # Python (Django + Flask fixtures)
  "How does the Django QuerySet evaluation and filtering pipeline work? Explain QuerySet chaining, lazy evaluation, the Query class, how lookups and filters are compiled into SQL, and how the Manager ties it all together. Show key classes and method signatures."
  # TypeScript (VSCode base library fixtures)
  "How do async operations, cancellation, and resource lifecycle management work together? Explain CancelablePromise, CancellationToken, the async utilities (throttle, debounce, retry), how they integrate with the disposable lifecycle system, and how event-driven patterns compose with async flows. Show key interfaces and class relationships."
)
Q_SLUGS=(
  "go-tsdb-compaction"
  "py-django-queryset"
  "ts-async-lifecycle"
)
Q_LANG=(
  "go"
  "python"
  "typescript"
)
Q_FIXTURES=(
  "$FIXTURES_GO"
  "$FIXTURES_PY"
  "$FIXTURES_TS"
)
Q_DIFFICULTY=(
  "hard"
  "hard"
  "hard"
)

# ── Models ────────────────────────────────────────────────────────────────────
MODELS=("sonnet" "opus")
FILTER_MODELS=()
FILTER_QUESTIONS=()

# ── CLI flags ─────────────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --claude-model) FILTER_MODELS+=("$2");    shift 2 ;;
    --question)     FILTER_QUESTIONS+=("$2"); shift 2 ;;
    --embed-model)  EMBED_MODEL="$2";         shift 2 ;;
    *) echo "Unknown arg: $1"; exit 1 ;;
  esac
done

[[ ${#FILTER_MODELS[@]} -gt 0 ]] && MODELS=("${FILTER_MODELS[@]}")

# ── Embedding model / backend ──────────────────────────────────────────────
EMBED_MODEL="${EMBED_MODEL:-ordis/jina-embeddings-v2-base-code}"

# Auto-detect backend: nomic-ai/* → lmstudio, everything else → ollama
case "$EMBED_MODEL" in
  nomic-ai/*) EMBED_BACKEND="lmstudio" ;;
  *)          EMBED_BACKEND="ollama"   ;;
esac

# Model slug for directory name: text after last '/', colons replaced with '-'
MODEL_SLUG="${EMBED_MODEL##*/}"
MODEL_SLUG="${MODEL_SLUG//:/-}"

# Build filtered question index
Q_INDICES=()
for i in "${!Q_SLUGS[@]}"; do
  if [[ ${#FILTER_QUESTIONS[@]} -eq 0 ]]; then
    Q_INDICES+=("$i")
  else
    for fq in "${FILTER_QUESTIONS[@]}"; do
      if [[ "${Q_SLUGS[$i]}" == "$fq" ]]; then
        Q_INDICES+=("$i")
        break
      fi
    done
  fi
done

# ── Build ──────────────────────────────────────────────────────────────────────
echo "Building lumen..."
CGO_ENABLED=1 go build -o lumen .

# ── Index ─────────────────────────────────────────────────────────────────────
echo "Indexing fixtures..."
for fx_dir in "$FIXTURES_GO" "$FIXTURES_PY" "$FIXTURES_TS"; do
  LUMEN_BACKEND="$EMBED_BACKEND" LUMEN_EMBED_MODEL="$EMBED_MODEL" \
    ./lumen index "$fx_dir" 2>&1 | tail -1
done

# ── MCP configs ───────────────────────────────────────────────────────────────
MCP_ENABLED=$(mktemp /tmp/bench-mcp-enabled-XXXXXX).json
MCP_EMPTY=$(mktemp /tmp/bench-mcp-empty-XXXXXX).json
trap 'rm -f "$MCP_ENABLED" "$MCP_EMPTY"' EXIT

cat > "$MCP_ENABLED" <<EOF
{"mcpServers":{"lumen":{"command":"$BINARY","args":["stdio"],"env":{"LUMEN_BACKEND":"$EMBED_BACKEND","LUMEN_EMBED_MODEL":"$EMBED_MODEL"}}}}
EOF
echo '{"mcpServers":{}}' > "$MCP_EMPTY"

# ── Results dir ───────────────────────────────────────────────────────────────
RESULTS_DIR="$REPO/bench-results/$(date +%Y%m%d-%H%M%S)-${EMBED_BACKEND}-${MODEL_SLUG}"
mkdir -p "$RESULTS_DIR"

# ── Run one scenario ──────────────────────────────────────────────────────────
run() {
  local mcp_cfg="$1" model="$2" q_idx="$3" scenario="$4" disable_builtin_tools="$5"
  local lang="${Q_LANG[$q_idx]}"
  local fixtures="${Q_FIXTURES[$q_idx]}"
  local slug="${Q_SLUGS[$q_idx]}-${model}-${scenario}"
  local prompt="The ${lang} project is at $fixtures. Answer this question about the code: ${QUESTIONS[$q_idx]}"
  local raw="$RESULTS_DIR/$slug-raw.jsonl"
  local answer_file="$RESULTS_DIR/$slug-answer.txt"

  printf "  %-28s %-12s %-10s ... " "${Q_SLUGS[$q_idx]}" "$model" "$scenario"

  local tools_arg=()
  [[ -n "$disable_builtin_tools" ]] && tools_arg=(--tools "")

  local allowed_tools_arg=()
  [[ "$mcp_cfg" == "$MCP_ENABLED" ]] && allowed_tools_arg=(--allowedTools "mcp__lumen__semantic_search,mcp__lumen__index_status")

  DISABLE_PROMPT_CACHING=1 claude \
    --output-format stream-json \
    --verbose \
    --model "$model" \
    --effort medium \
    --strict-mcp-config \
    --mcp-config "$mcp_cfg" \
    ${tools_arg[@]:+"${tools_arg[@]}"} \
    ${allowed_tools_arg[@]:+"${allowed_tools_arg[@]}"} \
    -p "$prompt" \
  > "$raw" 2>&1 || true

  # Strip home directory and username from raw snapshots to avoid PII in committed results
  [[ -f "$raw" ]] && sed -i '' -e "s|${HOME}|~|g" -e "s|${USER}|<user>|g" "$raw"

  local result_line
  result_line=$(grep -m1 '"type":"result"' "$raw" || true)
  if [[ -n "$result_line" ]]; then
    local cost duration_ms input_tokens cache_read cache_created output_tokens
    cost=$(echo "$result_line"          | jq -r '.total_cost_usd')
    duration_ms=$(echo "$result_line"   | jq -r '.duration_ms')
    input_tokens=$(echo "$result_line"  | jq -r '.usage.input_tokens // 0')
    cache_read=$(echo "$result_line"    | jq -r '.usage.cache_read_input_tokens // 0')
    cache_created=$(echo "$result_line" | jq -r '.usage.cache_creation_input_tokens // 0')
    output_tokens=$(echo "$result_line" | jq -r '.usage.output_tokens // 0')

    echo "$result_line" | jq -r '.result' | sed -e "s|${HOME}|~|g" -e "s|${USER}|<user>|g" > "$answer_file"
    echo "{\"cost_usd\":$cost,\"duration_ms\":$duration_ms,\"input_tokens\":$input_tokens,\"cache_read\":$cache_read,\"cache_created\":$cache_created,\"output_tokens\":$output_tokens}" \
      > "$RESULTS_DIR/$slug-metrics.json"

    local cost_fmt dur_s
    cost_fmt=$(printf "%.4f" "$cost")
    dur_s=$(echo "scale=1; $duration_ms/1000" | bc)
    printf "done  [%6ss  \$%s  in=%s+%scr  out=%s]\n" \
      "$dur_s" "$cost_fmt" "$input_tokens" "$cache_read" "$output_tokens"
  else
    echo "FAILED (no result event)"
  fi
}

# ── Extract winner from judge brief file ──────────────────────────────────────
extract_winner() {
  local brief_file="$1"
  grep -oE '\*\*Winner: [^*]+' "$brief_file" 2>/dev/null | sed 's/\*\*Winner: //' | tr -d ' \n' || echo "unknown"
}

# ── Run LLM judge for one question ────────────────────────────────────────────
run_judge() {
  local q_idx="$1"
  local slug="${Q_SLUGS[$q_idx]}"
  local question="${QUESTIONS[$q_idx]}"
  local judge_file="$RESULTS_DIR/$slug-judge.md"
  local judge_brief_file="$RESULTS_DIR/$slug-judge-brief.md"

  # Collect answers and metrics
  local all_answers=""
  local metrics_table="| Run | Duration | Input Tok | Cache Read | Output Tok | Cost (USD) |"$'\n'
  metrics_table+="|----|----------|-----------|------------|------------|------------|"$'\n'
  local have_answers=false

  for model in "${MODELS[@]}"; do
    for scenario in baseline mcp-only mcp-full; do
      local af="$RESULTS_DIR/${slug}-${model}-${scenario}-answer.txt"
      local mf="$RESULTS_DIR/${slug}-${model}-${scenario}-metrics.json"
      if [[ -f "$af" ]]; then
        have_answers=true
        all_answers+="
Answer [${model} / ${scenario}]:
$(cat "$af")
"
      fi
      if [[ -f "$mf" ]]; then
        local in_tok cr_tok out_tok cost_usd dur_ms cost_fmt dur_s
        in_tok=$(jq -r '.input_tokens'   "$mf")
        cr_tok=$(jq -r '.cache_read'     "$mf")
        out_tok=$(jq -r '.output_tokens' "$mf")
        cost_usd=$(jq -r '.cost_usd'     "$mf")
        dur_ms=$(jq -r '.duration_ms'    "$mf")
        cost_fmt=$(printf "%.5f" "$cost_usd")
        dur_s=$(echo "scale=1; $dur_ms/1000" | bc)
        metrics_table+="| ${model} / ${scenario} | ${dur_s}s | $in_tok | $cr_tok | $out_tok | \$$cost_fmt |"$'\n'
      fi
    done
  done

  if ! $have_answers; then
    return
  fi

  printf "  Judging %-28s ... " "$slug"

  # Brief verdict for summary (content quality + efficiency)
  claude -p --model claude-opus-4-6 --effort medium \
    "You are a judge evaluating AI answers to a codebase question. Be concise.

Question: $question

$all_answers

Metrics:
$metrics_table

Evaluate in three sections:

## Content Quality
Rank the answers [model/scenario] from best to worst. One sentence per answer covering correctness, completeness, and use of specific file/line references.

## Efficiency
One or two sentences comparing runtime, token usage, and cost across scenarios. Note which scenario offers the best quality-to-cost tradeoff.

## Verdict
On the very last line write exactly: **Winner: model/scenario**
Choose the single run that offers the best combination of answer quality, token usage, cost, and runtime. All three efficiency dimensions (tokens, cost, time) matter equally alongside quality.
Example: **Winner: sonnet/mcp-only**" \
    > "$judge_brief_file" 2>&1 || echo "_Judge unavailable_" > "$judge_brief_file"

  # Detailed analysis for detail report
  claude -p --model claude-opus-4-6 --effort medium \
    "You are a judge evaluating AI answers to a question about a codebase.

Question: $question

$all_answers

Metrics:
$metrics_table

Evaluate in two sections:

## Content Quality
Rank the answers from best to worst. For each, write a paragraph covering: (1) correctness, (2) completeness, (3) precision of file/line references, (4) whether it used the right tools/approach to find information.

## Efficiency Analysis
Compare runtime, token usage, and cost across all scenarios. Identify which scenarios were most efficient, note any surprising differences, and recommend the best quality-to-cost tradeoff." \
    > "$judge_file" 2>&1 || echo "_Judge unavailable_" > "$judge_file"

  echo "done"
}

# ── Emit aggregate stats table across all questions ───────────────────────────
emit_overall_stats() {
  echo "## Overall: Aggregated by Scenario"
  echo ""
  echo "Totals across all ${#Q_INDICES[@]} questions × ${#MODELS[@]} models."
  echo ""
  echo "| Model | Scenario | Total Time | Total Input Tok | Total Output Tok | Total Cost (USD) |"
  echo "|-------|----------|------------|-----------------|------------------|------------------|"

  for model in "${MODELS[@]}"; do
    for scenario in baseline mcp-only mcp-full; do
      local total_dur_ms=0 total_in=0 total_out=0 total_cost_scaled=0 count=0
      for q_idx in "${Q_INDICES[@]}"; do
        local mf="$RESULTS_DIR/${Q_SLUGS[$q_idx]}-${model}-${scenario}-metrics.json"
        if [[ -f "$mf" ]]; then
          total_dur_ms=$(( total_dur_ms + $(jq -r '.duration_ms'    "$mf") ))
          total_in=$(( total_in         + $(jq -r '.input_tokens'   "$mf") ))
          total_out=$(( total_out       + $(jq -r '.output_tokens'  "$mf") ))
          # cost: multiply by 100000 to keep integer arithmetic, divide at end
          local cost_scaled
          cost_scaled=$(jq -r '(.cost_usd * 100000) | round' "$mf")
          total_cost_scaled=$(( total_cost_scaled + cost_scaled ))
          (( count++ )) || true
        fi
      done
      if [[ $count -gt 0 ]]; then
        local dur_s cost_fmt
        dur_s=$(echo "scale=1; $total_dur_ms/1000" | bc)
        cost_fmt=$(printf "%.4f" "$(echo "scale=5; $total_cost_scaled/100000" | bc)")
        echo "| **$model** | $scenario | ${dur_s}s | $total_in | $total_out | \$$cost_fmt |"
      else
        echo "| **$model** | $scenario | — | — | — | — |"
      fi
    done
  done
  echo ""
}

# ── Emit overall algorithm comparison table ───────────────────────────────────
emit_overall_comparison() {
  echo "## Overall: Algorithm Comparison"
  echo ""
  echo "| Question | Language | Difficulty | 🏆 Winner | Runner-up |"
  echo "|----------|----------|------------|-----------|-----------|"

  local wins_baseline=0
  local wins_mcp_only=0
  local wins_mcp_full=0

  local runner_ups=()

  for q_idx in "${Q_INDICES[@]}"; do
    local slug="${Q_SLUGS[$q_idx]}"
    local difficulty="${Q_DIFFICULTY[$q_idx]}"
    local brief_file="$RESULTS_DIR/$slug-judge-brief.md"
    local winner="unknown"
    [[ -f "$brief_file" ]] && winner=$(extract_winner "$brief_file")

    # Tally wins per scenario
    local winner_scenario="${winner#*/}"
    if [[ -n "$winner_scenario" && "$winner" != "unknown" ]]; then
      case "$winner_scenario" in
        baseline) (( wins_baseline++ )) || true ;;
        mcp-only) (( wins_mcp_only++ )) || true ;;
        mcp-full) (( wins_mcp_full++ )) || true ;;
      esac
    fi

    # Find runner-up: second-lowest cost among runs that have metrics, excluding winner
    local runner_up="—"
    local best_cost_scaled=999999999
    for model in "${MODELS[@]}"; do
      for scenario in baseline mcp-only mcp-full; do
        local run_key="${model}/${scenario}"
        [[ "$run_key" == "$winner" ]] && continue
        local mf="$RESULTS_DIR/${slug}-${model}-${scenario}-metrics.json"
        if [[ -f "$mf" ]]; then
          local cost_scaled
          cost_scaled=$(jq -r '(.cost_usd * 100000) | round' "$mf")
          if (( cost_scaled < best_cost_scaled )); then
            best_cost_scaled=$cost_scaled
            runner_up="$run_key"
          fi
        fi
      done
    done

    local lang="${Q_LANG[$q_idx]}"
    echo "| $slug | $lang | $difficulty | $winner | $runner_up |"
  done

  echo ""
  echo "**Scenario Win Counts** (across all ${#Q_INDICES[@]} questions):"
  echo ""
  echo "| Scenario | Wins |"
  echo "|----------|------|"

  local overall_winner_scenario=""
  local overall_winner_count=0

  for scenario in baseline mcp-only mcp-full; do
    local wins
    case "$scenario" in
      baseline) wins=$wins_baseline ;;
      mcp-only) wins=$wins_mcp_only ;;
      mcp-full) wins=$wins_mcp_full ;;
    esac
    echo "| $scenario | $wins |"
    if (( wins > overall_winner_count )); then
      overall_winner_count=$wins
      overall_winner_scenario=$scenario
    fi
  done

  echo ""
  if [[ -n "$overall_winner_scenario" && $overall_winner_count -gt 0 ]]; then
    echo "**Overall winner: $overall_winner_scenario** — won $overall_winner_count of ${#Q_INDICES[@]} questions."
  else
    echo "**Overall winner: undetermined** (no judge results available)."
  fi
  echo ""
}

# ── Generate reports ───────────────────────────────────────────────────────────
generate_reports() {
  local summary_file="$RESULTS_DIR/summary-report.md"
  local detail_file="$RESULTS_DIR/detail-report.md"

  # ── Summary report ────────────────────────────────────────────────────────
  {
    echo "# Benchmark Summary"
    echo ""
    echo "Generated: $(date -u '+%Y-%m-%d %H:%M UTC')  |  Results: \`$(basename "$RESULTS_DIR")\`"
    echo ""
    echo "| Scenario | Description |"
    echo "|----------|-------------|"
    echo "| **baseline** | All default Claude tools, no MCP |"
    echo "| **mcp-only** | \`semantic_search\` MCP tool only |"
    echo "| **mcp-full** | All default tools + MCP |"
    echo ""
    emit_overall_stats
    echo "---"
    echo ""

    for q_idx in "${Q_INDICES[@]}"; do
      local slug="${Q_SLUGS[$q_idx]}"
      local difficulty="${Q_DIFFICULTY[$q_idx]}"
      local question="${QUESTIONS[$q_idx]}"

      local lang="${Q_LANG[$q_idx]}"
      echo "## $slug [$lang / $difficulty]"
      echo ""
      echo "> $question"
      echo ""
      echo "### Time & Tokens"
      echo ""
      echo "| Model | Scenario | Duration | Input Tok | Cache Read | Output Tok | Cost (USD) | Winner |"
      echo "|-------|----------|----------|-----------|------------|------------|------------|--------|"

      local judge_brief_file="$RESULTS_DIR/$slug-judge-brief.md"
      local winner=""
      [[ -f "$judge_brief_file" ]] && winner=$(extract_winner "$judge_brief_file")

      for model in "${MODELS[@]}"; do
        for scenario in baseline mcp-only mcp-full; do
          local metrics_file="$RESULTS_DIR/${slug}-${model}-${scenario}-metrics.json"
          if [[ -f "$metrics_file" ]]; then
            local in_tok cr_tok out_tok cost_usd dur_ms cost_fmt dur_s badge
            in_tok=$(jq -r '.input_tokens'   "$metrics_file")
            cr_tok=$(jq -r '.cache_read'     "$metrics_file")
            out_tok=$(jq -r '.output_tokens' "$metrics_file")
            cost_usd=$(jq -r '.cost_usd'     "$metrics_file")
            dur_ms=$(jq -r '.duration_ms'    "$metrics_file")
            cost_fmt=$(printf "%.4f" "$cost_usd")
            dur_s=$(echo "scale=1; $dur_ms/1000" | bc)
            local run_key="${model}/${scenario}"
            badge=""
            [[ -n "$winner" && "$winner" == "$run_key" ]] && badge="🏆 Winner"
            echo "| **$model** | $scenario | ${dur_s}s | $in_tok | $cr_tok | $out_tok | \$$cost_fmt | $badge |"
          else
            echo "| **$model** | $scenario | — | — | — | — | — | |"
          fi
        done
      done

      echo ""

      if [[ -f "$judge_brief_file" ]]; then
        echo "### Quality Ranking (Opus 4.6)"
        echo ""
        cat "$judge_brief_file"
        echo ""
      fi

      echo "---"
      echo ""
    done

    emit_overall_comparison
    echo "_Full answers and detailed analysis: \`detail-report.md\`_"
  } > "$summary_file"

  # ── Detail report ─────────────────────────────────────────────────────────
  {
    echo "# Benchmark Detail Report"
    echo ""
    echo "Generated: $(date -u '+%Y-%m-%d %H:%M UTC')  |  Results: \`$(basename "$RESULTS_DIR")\`"
    echo ""

    for q_idx in "${Q_INDICES[@]}"; do
      local slug="${Q_SLUGS[$q_idx]}"
      local difficulty="${Q_DIFFICULTY[$q_idx]}"
      local question="${QUESTIONS[$q_idx]}"

      echo "---"
      echo ""
      local lang="${Q_LANG[$q_idx]}"
      echo "## $slug [$lang / $difficulty]"
      echo ""
      echo "**Question:** $question"
      echo ""

      echo "### Metrics"
      echo ""
      echo "| Model | Scenario | Duration | Input Tok | Cache Read | Cache Created | Output Tok | Cost (USD) |"
      echo "|-------|----------|----------|-----------|------------|---------------|------------|------------|"

      for model in "${MODELS[@]}"; do
        for scenario in baseline mcp-only mcp-full; do
          local metrics_file="$RESULTS_DIR/${slug}-${model}-${scenario}-metrics.json"
          if [[ -f "$metrics_file" ]]; then
            local in_tok cr_tok cc_tok out_tok cost_usd dur_ms cost_fmt dur_s
            in_tok=$(jq -r '.input_tokens'    "$metrics_file")
            cr_tok=$(jq -r '.cache_read'      "$metrics_file")
            cc_tok=$(jq -r '.cache_created'   "$metrics_file")
            out_tok=$(jq -r '.output_tokens'  "$metrics_file")
            cost_usd=$(jq -r '.cost_usd'      "$metrics_file")
            dur_ms=$(jq -r '.duration_ms'     "$metrics_file")
            cost_fmt=$(printf "%.5f" "$cost_usd")
            dur_s=$(echo "scale=1; $dur_ms/1000" | bc)
            echo "| **$model** | $scenario | ${dur_s}s | $in_tok | $cr_tok | $cc_tok | $out_tok | \$$cost_fmt |"
          else
            echo "| **$model** | $scenario | — | — | — | — | — | — |"
          fi
        done
      done

      echo ""

      for model in "${MODELS[@]}"; do
        for scenario in baseline mcp-only mcp-full; do
          local af="$RESULTS_DIR/${slug}-${model}-${scenario}-answer.txt"
          if [[ -f "$af" ]]; then
            echo "### Answer: \`$model\` / \`$scenario\`"
            echo ""
            cat "$af"
            echo ""
          fi
        done
      done

      local judge_file="$RESULTS_DIR/$slug-judge.md"
      if [[ -f "$judge_file" ]]; then
        echo "### Full Judge Analysis (Opus 4.6)"
        echo ""
        cat "$judge_file"
        echo ""
      fi
    done
  } > "$detail_file"

  echo ""
  echo "Reports written:"
  echo "  Summary : $summary_file"
  echo "  Detail  : $detail_file"
}

# ── Main loop ─────────────────────────────────────────────────────────────────
echo ""
echo "Running benchmarks (${#MODELS[@]} models × ${#Q_INDICES[@]} questions × 3 scenarios)..."
echo ""

for model in "${MODELS[@]}"; do
  echo "── Model: $model ──────────────────────────────────────────"
  for q_idx in "${Q_INDICES[@]}"; do
    _t1=$(mktemp) _t2=$(mktemp) _t3=$(mktemp)
    run "$MCP_EMPTY"   "$model" "$q_idx" "baseline" ""  >"$_t1" 2>&1 & _p1=$!
    run "$MCP_ENABLED" "$model" "$q_idx" "mcp-only"  "1" >"$_t2" 2>&1 & _p2=$!
    run "$MCP_ENABLED" "$model" "$q_idx" "mcp-full"  ""  >"$_t3" 2>&1 & _p3=$!
    wait "$_p1" || true; cat "$_t1"; rm -f "$_t1"
    wait "$_p2" || true; cat "$_t2"; rm -f "$_t2"
    wait "$_p3" || true; cat "$_t3"; rm -f "$_t3"
  done
  echo ""
done

echo "── Generating LLM judge reports ──────────────────────────────"
_judge_pids=() _judge_tmps=()
for q_idx in "${Q_INDICES[@]}"; do
  _jt=$(mktemp)
  _judge_tmps+=("$_jt")
  run_judge "$q_idx" >"$_jt" 2>&1 &
  _judge_pids+=($!)
done
for _ji in "${!_judge_pids[@]}"; do
  wait "${_judge_pids[$_ji]}" || true
  cat "${_judge_tmps[$_ji]}"
  rm -f "${_judge_tmps[$_ji]}"
done

generate_reports
