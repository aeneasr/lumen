package report

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ory/lumen/benchmarks/harness"
	"github.com/ory/lumen/benchmarks/tasks"
)

// Report generates a markdown comparison report from benchmark results.
type Report struct {
	results map[string]map[tasks.Scenario]*harness.RunResult // taskID → scenario → result
}

// LoadResults loads all *-result.json files from a results directory.
func LoadResults(dir string) (*Report, error) {
	r := &Report{
		results: make(map[string]map[tasks.Scenario]*harness.RunResult),
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read results dir: %w", err)
	}

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), "-result.json") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}

		var result harness.RunResult
		if err := json.Unmarshal(data, &result); err != nil {
			continue
		}

		if r.results[result.TaskID] == nil {
			r.results[result.TaskID] = make(map[tasks.Scenario]*harness.RunResult)
		}
		r.results[result.TaskID][result.Scenario] = &result
	}

	if len(r.results) == 0 {
		return nil, fmt.Errorf("no results found in %s", dir)
	}

	return r, nil
}

// GenerateMarkdown writes a full comparison report to w.
func (r *Report) GenerateMarkdown(w io.Writer) {
	scenarios := tasks.AllScenarios()
	taskIDs := r.sortedTaskIDs()

	fmt.Fprintf(w, "# Lumen Benchmark Report\n\n")
	fmt.Fprintf(w, "Generated: %s\n\n", time.Now().UTC().Format("2006-01-02 15:04 UTC"))

	// Scenario descriptions.
	fmt.Fprintf(w, "| Scenario | Description |\n")
	fmt.Fprintf(w, "|----------|-------------|\n")
	fmt.Fprintf(w, "| **baseline** | All default Claude tools, no MCP |\n")
	fmt.Fprintf(w, "| **mcp-only** | Only semantic_search MCP tool |\n")
	fmt.Fprintf(w, "| **mcp-full** | All default tools + MCP |\n\n")

	// Summary table.
	r.writeSummaryTable(w, scenarios, taskIDs)

	// Statistical tests.
	r.writeStatisticalTests(w, taskIDs)

	// Per-task details.
	r.writePerTaskDetails(w, scenarios, taskIDs)
}

func (r *Report) writeSummaryTable(w io.Writer, scenarios []tasks.Scenario, taskIDs []string) {
	fmt.Fprintf(w, "## Summary\n\n")
	fmt.Fprintf(w, "| Metric | baseline | mcp-only | mcp-full |\n")
	fmt.Fprintf(w, "|--------|----------|----------|----------|\n")

	type agg struct {
		successCount int
		totalCount   int
		totalCost    float64
		totalDurMS   int64
		totalInTok   int64
		totalOutTok  int64
		totalTools   int
	}

	aggs := make(map[tasks.Scenario]*agg)
	for _, s := range scenarios {
		aggs[s] = &agg{}
	}

	for _, tid := range taskIDs {
		for _, s := range scenarios {
			res, ok := r.results[tid][s]
			if !ok {
				continue
			}
			a := aggs[s]
			a.totalCount++
			if res.Validation.Success {
				a.successCount++
			}
			a.totalCost += res.Metrics.CostUSD
			a.totalDurMS += res.Metrics.DurationMS
			a.totalInTok += res.Metrics.InputTokens
			a.totalOutTok += res.Metrics.OutputTokens
			for _, c := range res.Metrics.ToolCalls {
				a.totalTools += c
			}
		}
	}

	writeRow := func(label string, fn func(a *agg) string) {
		fmt.Fprintf(w, "| %s", label)
		for _, s := range scenarios {
			fmt.Fprintf(w, " | %s", fn(aggs[s]))
		}
		fmt.Fprintf(w, " |\n")
	}

	writeRow("Success Rate", func(a *agg) string {
		if a.totalCount == 0 {
			return "—"
		}
		return fmt.Sprintf("%d/%d (%.0f%%)", a.successCount, a.totalCount,
			100*float64(a.successCount)/float64(a.totalCount))
	})
	writeRow("Avg Cost (USD)", func(a *agg) string {
		if a.totalCount == 0 {
			return "—"
		}
		return fmt.Sprintf("$%.4f", a.totalCost/float64(a.totalCount))
	})
	writeRow("Avg Duration (s)", func(a *agg) string {
		if a.totalCount == 0 {
			return "—"
		}
		return fmt.Sprintf("%.1f", float64(a.totalDurMS)/float64(a.totalCount)/1000)
	})
	writeRow("Avg Input Tokens", func(a *agg) string {
		if a.totalCount == 0 {
			return "—"
		}
		return fmt.Sprintf("%d", a.totalInTok/int64(a.totalCount))
	})
	writeRow("Avg Output Tokens", func(a *agg) string {
		if a.totalCount == 0 {
			return "—"
		}
		return fmt.Sprintf("%d", a.totalOutTok/int64(a.totalCount))
	})
	writeRow("Avg Tool Calls", func(a *agg) string {
		if a.totalCount == 0 {
			return "—"
		}
		return fmt.Sprintf("%.1f", float64(a.totalTools)/float64(a.totalCount))
	})
	fmt.Fprintf(w, "\n")
}

func (r *Report) writeStatisticalTests(w io.Writer, taskIDs []string) {
	fmt.Fprintf(w, "## Statistical Significance (baseline vs mcp-full)\n\n")

	// Collect paired samples for tasks that have both baseline and mcp-full results.
	var costA, costB []float64
	var tokA, tokB []float64
	var durA, durB []float64
	var successA, successB []bool

	for _, tid := range taskIDs {
		base, hasBase := r.results[tid][tasks.ScenarioBaseline]
		full, hasFull := r.results[tid][tasks.ScenarioMCPFull]
		if !hasBase || !hasFull {
			continue
		}

		costA = append(costA, base.Metrics.CostUSD)
		costB = append(costB, full.Metrics.CostUSD)

		tokA = append(tokA, float64(base.Metrics.InputTokens+base.Metrics.OutputTokens))
		tokB = append(tokB, float64(full.Metrics.InputTokens+full.Metrics.OutputTokens))

		durA = append(durA, float64(base.Metrics.DurationMS))
		durB = append(durB, float64(full.Metrics.DurationMS))

		successA = append(successA, base.Validation.Success)
		successB = append(successB, full.Validation.Success)
	}

	if len(costA) < 2 {
		fmt.Fprintf(w, "Not enough paired samples for statistical tests (need at least 2, have %d).\n\n", len(costA))
		return
	}

	fmt.Fprintf(w, "Paired samples: %d tasks\n\n", len(costA))
	fmt.Fprintf(w, "| Metric | Mean Δ | t-stat | p-value | Significant? |\n")
	fmt.Fprintf(w, "|--------|--------|--------|---------|-------------|\n")

	for _, test := range []struct {
		name string
		a, b []float64
		unit string
	}{
		{"Cost (USD)", costA, costB, "$%.4f"},
		{"Total Tokens", tokA, tokB, "%.0f"},
		{"Duration (ms)", durA, durB, "%.0f"},
	} {
		result := PairedTTest(test.a, test.b)
		sig := ""
		if result.PValue < 0.05 {
			sig = "yes"
		} else {
			sig = "no"
		}
		fmt.Fprintf(w, "| %s | "+test.unit+" | %.3f | %.4f | %s |\n",
			test.name, result.MeanDiff, result.TStat, result.PValue, sig)
	}

	mcnemar := McNemarTest(successA, successB)
	fmt.Fprintf(w, "\nMcNemar's test (success rate): chi² = %.3f, p = %.4f\n\n", mcnemar.ChiSq, mcnemar.PValue)
}

func (r *Report) writePerTaskDetails(w io.Writer, scenarios []tasks.Scenario, taskIDs []string) {
	fmt.Fprintf(w, "## Per-Task Details\n\n")

	for _, tid := range taskIDs {
		fmt.Fprintf(w, "### %s\n\n", tid)
		fmt.Fprintf(w, "| Scenario | Success | Cost | Duration | In Tok | Out Tok | Tool Calls |\n")
		fmt.Fprintf(w, "|----------|---------|------|----------|--------|---------|------------|\n")

		for _, s := range scenarios {
			res, ok := r.results[tid][s]
			if !ok {
				fmt.Fprintf(w, "| %s | — | — | — | — | — | — |\n", s)
				continue
			}

			success := "no"
			if res.Validation.Success {
				success = "yes"
			} else if res.Validation.PartialSuccess {
				success = "partial"
			}

			toolStr := formatToolCalls(res.Metrics.ToolCalls)

			fmt.Fprintf(w, "| %s | %s | $%.4f | %.1fs | %d | %d | %s |\n",
				s, success, res.Metrics.CostUSD,
				float64(res.Metrics.DurationMS)/1000,
				res.Metrics.InputTokens, res.Metrics.OutputTokens,
				toolStr)
		}
		fmt.Fprintf(w, "\n")
	}
}

func (r *Report) sortedTaskIDs() []string {
	ids := make([]string, 0, len(r.results))
	for id := range r.results {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func formatToolCalls(calls map[string]int) string {
	if len(calls) == 0 {
		return "—"
	}
	var parts []string
	for name, count := range calls {
		parts = append(parts, fmt.Sprintf("%s:%d", name, count))
	}
	sort.Strings(parts)
	return strings.Join(parts, " ")
}
