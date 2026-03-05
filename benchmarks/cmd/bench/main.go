package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/ory/lumen/benchmarks/harness"
	"github.com/ory/lumen/benchmarks/report"
	"github.com/ory/lumen/benchmarks/tasks"
	"github.com/spf13/cobra"
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "bench",
	Short: "Lumen benchmark harness — SWE-bench style evaluation",
}

// ── run command ─────────────────────────────────────────────────────────────

var (
	runSet          string
	runScenario     string
	runTask         string
	runModel        string
	runParallel     int
	runLumenBinary  string
	runEmbedModel   string
	runEmbedBackend string
	runOutputDir    string
	runTasksDir     string
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run benchmark tasks across scenarios",
	RunE:  runBenchmark,
}

func init() {
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(reportCmd)
	rootCmd.AddCommand(fetchCmd)

	runCmd.Flags().StringVar(&runSet, "set", "swe-bench-sm", "Task set directory name")
	runCmd.Flags().StringVar(&runScenario, "scenario", "all", "Scenario: baseline, mcp-only, mcp-full, or all")
	runCmd.Flags().StringVar(&runTask, "task", "", "Run a single task by ID")
	runCmd.Flags().StringVar(&runModel, "model", "claude-sonnet-4-6", "Claude model to use")
	runCmd.Flags().IntVar(&runParallel, "parallel", 1, "Concurrent task runs")
	runCmd.Flags().StringVar(&runLumenBinary, "lumen-binary", "./lumen", "Path to lumen binary")
	runCmd.Flags().StringVar(&runEmbedModel, "embed-model", "ordis/jina-embeddings-v2-base-code", "Embedding model")
	runCmd.Flags().StringVar(&runEmbedBackend, "embed-backend", "ollama", "Embedding backend (ollama or lmstudio)")
	runCmd.Flags().StringVar(&runOutputDir, "output", "", "Results output directory (auto-generated if empty)")
	runCmd.Flags().StringVar(&runTasksDir, "tasks-dir", "./benchmarks/tasks/sets", "Base directory for task sets")
}

func runBenchmark(cmd *cobra.Command, _ []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// Resolve task set directory.
	setDir := filepath.Join(runTasksDir, runSet)
	registry, err := tasks.LoadFromDir(setDir)
	if err != nil {
		return fmt.Errorf("load tasks from %s: %w", setDir, err)
	}

	// Filter to single task if requested.
	var taskList []tasks.Task
	if runTask != "" {
		t, ok := registry.ByID(runTask)
		if !ok {
			return fmt.Errorf("task %q not found in set %s", runTask, runSet)
		}
		taskList = []tasks.Task{t}
	} else {
		taskList = registry.All()
	}

	// Resolve scenarios.
	scenarios, err := resolveScenarios(runScenario)
	if err != nil {
		return err
	}

	// Resolve output directory.
	outputDir := runOutputDir
	if outputDir == "" {
		outputDir = filepath.Join("benchmarks", "results",
			time.Now().Format("20060102-150405")+"-"+runSet)
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	opts := harness.RunOpts{
		Model:        runModel,
		LumenBinary:  runLumenBinary,
		EmbedModel:   runEmbedModel,
		EmbedBackend: runEmbedBackend,
		OutputDir:    outputDir,
	}

	fmt.Printf("Running %d tasks × %d scenarios (model: %s)\n",
		len(taskList), len(scenarios), runModel)
	fmt.Printf("Results: %s\n\n", outputDir)

	total := len(taskList) * len(scenarios)
	completed := 0

	for _, task := range taskList {
		for _, scenario := range scenarios {
			completed++
			fmt.Printf("[%d/%d] %s / %s ... ", completed, total, task.ID, scenario)

			result, err := harness.RunTask(ctx, task, scenario, opts)
			if err != nil {
				fmt.Printf("ERROR: %v\n", err)
				continue
			}

			status := "FAIL"
			if result.Validation.Success {
				status = "PASS"
			} else if result.Validation.PartialSuccess {
				status = "PARTIAL"
			}

			fmt.Printf("%s [%.1fs $%.4f in=%d out=%d]\n",
				status,
				float64(result.Metrics.DurationMS)/1000,
				result.Metrics.CostUSD,
				result.Metrics.InputTokens,
				result.Metrics.OutputTokens)
		}
	}

	fmt.Printf("\nDone. Results in %s\n", outputDir)
	fmt.Println("Run 'bench report --dir " + outputDir + "' to generate comparison report.")
	return nil
}

func resolveScenarios(s string) ([]tasks.Scenario, error) {
	if s == "all" {
		return tasks.AllScenarios(), nil
	}
	parts := strings.Split(s, ",")
	var out []tasks.Scenario
	for _, p := range parts {
		sc := tasks.Scenario(strings.TrimSpace(p))
		switch sc {
		case tasks.ScenarioBaseline, tasks.ScenarioMCPOnly, tasks.ScenarioMCPFull:
			out = append(out, sc)
		default:
			return nil, fmt.Errorf("unknown scenario %q (valid: baseline, mcp-only, mcp-full, all)", p)
		}
	}
	return out, nil
}

// ── report command ──────────────────────────────────────────────────────────

var reportDir string

var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "Generate comparison report from results",
	RunE:  runReport,
}

func init() {
	reportCmd.Flags().StringVar(&reportDir, "dir", "", "Results directory to report on (required)")
	_ = reportCmd.MarkFlagRequired("dir")
}

func runReport(_ *cobra.Command, _ []string) error {
	r, err := report.LoadResults(reportDir)
	if err != nil {
		return err
	}

	outPath := filepath.Join(reportDir, "report.md")
	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("create report file: %w", err)
	}
	defer f.Close()

	r.GenerateMarkdown(f)

	fmt.Printf("Report written to %s\n", outPath)
	return nil
}

// ── fetch-tasks command ─────────────────────────────────────────────────────

var (
	fetchSource string
	fetchCount  int
	fetchOutput string
)

var fetchCmd = &cobra.Command{
	Use:   "fetch-tasks",
	Short: "Download and convert benchmark tasks from SWE-bench Lite",
	RunE:  runFetch,
}

func init() {
	fetchCmd.Flags().StringVar(&fetchSource, "source", "swe-bench-lite", "Task source (swe-bench-lite)")
	fetchCmd.Flags().IntVar(&fetchCount, "count", 25, "Number of tasks to fetch")
	fetchCmd.Flags().StringVar(&fetchOutput, "output", "", "Output directory (required)")
	_ = fetchCmd.MarkFlagRequired("output")
}

func runFetch(_ *cobra.Command, _ []string) error {
	switch fetchSource {
	case "swe-bench-lite":
		n, err := tasks.FetchSWEBenchLite(fetchOutput, fetchCount)
		if err != nil {
			return err
		}
		fmt.Printf("Fetched %d tasks to %s\n", n, fetchOutput)
		return nil
	default:
		return fmt.Errorf("unknown source %q (valid: swe-bench-lite)", fetchSource)
	}
}
