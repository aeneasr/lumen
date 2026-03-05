package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/ory/lumen/benchmarks/tasks"
)

// RunOpts configures a benchmark run.
type RunOpts struct {
	Model        string
	LumenBinary  string
	EmbedModel   string
	EmbedBackend string
	OutputDir    string
}

// RunResult holds the complete outcome of running a single task in one scenario.
type RunResult struct {
	TaskID     string           `json:"task_id"`
	Scenario   tasks.Scenario   `json:"scenario"`
	Metrics    RunMetrics       `json:"metrics"`
	Validation ValidationResult `json:"validation"`
	StartedAt  time.Time        `json:"started_at"`
	Duration   time.Duration    `json:"duration"`
	Error      string           `json:"error,omitempty"`
}

// mcpConfig is the JSON structure for --mcp-config.
type mcpConfig struct {
	MCPServers map[string]mcpServer `json:"mcpServers"`
}

type mcpServer struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env,omitempty"`
}

// RunTask executes a single task in a single scenario and returns the result.
func RunTask(ctx context.Context, task tasks.Task, scenario tasks.Scenario, opts RunOpts) (*RunResult, error) {
	result := &RunResult{
		TaskID:    task.ID,
		Scenario:  scenario,
		StartedAt: time.Now(),
	}

	defer func() {
		result.Duration = time.Since(result.StartedAt)
	}()

	// Set up isolated workspace.
	workDir, cleanup, err := SetupWorkspace(ctx, task.Repo, task.BaseCommit)
	if err != nil {
		result.Error = fmt.Sprintf("workspace setup: %v", err)
		return result, err
	}
	defer cleanup()

	// Pre-index if Lumen is involved.
	if scenario != tasks.ScenarioBaseline {
		if err := preIndex(ctx, opts, workDir); err != nil {
			result.Error = fmt.Sprintf("pre-index: %v", err)
			return result, err
		}
	}

	// Write MCP config files.
	mcpCfgPath, err := writeMCPConfig(opts, scenario)
	if err != nil {
		result.Error = fmt.Sprintf("write mcp config: %v", err)
		return result, err
	}
	defer os.Remove(mcpCfgPath)

	// Build Claude CLI arguments.
	args := buildClaudeArgs(task, scenario, opts, mcpCfgPath)

	// Prepare output file for raw stream.
	slug := fmt.Sprintf("%s-%s", task.ID, scenario)
	rawPath := filepath.Join(opts.OutputDir, slug+"-raw.jsonl")
	rawFile, err := os.Create(rawPath)
	if err != nil {
		result.Error = fmt.Sprintf("create raw file: %v", err)
		return result, err
	}
	defer rawFile.Close()

	// Run Claude.
	collector := NewMetricsCollector()
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = workDir
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		result.Error = fmt.Sprintf("stdout pipe: %v", err)
		return result, err
	}

	if err := cmd.Start(); err != nil {
		result.Error = fmt.Sprintf("start claude: %v", err)
		return result, err
	}

	if err := collector.ProcessStream(stdout, rawFile); err != nil {
		result.Error = fmt.Sprintf("process stream: %v", err)
	}

	if err := cmd.Wait(); err != nil {
		// Non-zero exit is expected when Claude hits budget/turn limits.
		result.Error = fmt.Sprintf("claude exit: %v", err)
	}

	result.Metrics = collector.Metrics()

	// Validate.
	result.Validation = Validate(ctx, task, workDir)

	// Save result JSON.
	resultPath := filepath.Join(opts.OutputDir, slug+"-result.json")
	resultData, _ := json.MarshalIndent(result, "", "  ")
	_ = os.WriteFile(resultPath, resultData, 0o644)

	return result, nil
}

func buildClaudeArgs(task tasks.Task, scenario tasks.Scenario, opts RunOpts, mcpCfgPath string) []string {
	prompt := fmt.Sprintf(
		"You are working in this repository. Fix the following issue:\n\n%s\n\n"+
			"Make the minimal changes necessary. Do not add tests unless required.",
		task.ProblemStatement,
	)

	args := []string{
		"-p", prompt,
		"--output-format", "stream-json",
		"--verbose",
		"--dangerously-skip-permissions",
		"--strict-mcp-config",
		"--mcp-config", mcpCfgPath,
		"--max-turns", strconv.Itoa(task.MaxTurns),
		"--max-budget-usd", fmt.Sprintf("%.2f", task.MaxBudgetUSD),
		"--no-session-persistence",
		"--model", opts.Model,
	}

	switch scenario {
	case tasks.ScenarioMCPOnly:
		args = append(args, "--tools", "")
		args = append(args, "--allowedTools", "mcp__lumen__semantic_search,mcp__lumen__index_status")
	case tasks.ScenarioMCPFull:
		args = append(args, "--allowedTools", "mcp__lumen__semantic_search,mcp__lumen__index_status")
	}

	return args
}

func writeMCPConfig(opts RunOpts, scenario tasks.Scenario) (string, error) {
	var cfg mcpConfig

	if scenario == tasks.ScenarioBaseline {
		cfg = mcpConfig{MCPServers: map[string]mcpServer{}}
	} else {
		cfg = mcpConfig{
			MCPServers: map[string]mcpServer{
				"lumen": {
					Command: opts.LumenBinary,
					Args:    []string{"stdio"},
					Env: map[string]string{
						"LUMEN_BACKEND":     opts.EmbedBackend,
						"LUMEN_EMBED_MODEL": opts.EmbedModel,
					},
				},
			},
		}
	}

	f, err := os.CreateTemp("", "bench-mcp-*.json")
	if err != nil {
		return "", err
	}

	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(cfg); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}

	f.Close()
	return f.Name(), nil
}

func preIndex(ctx context.Context, opts RunOpts, workDir string) error {
	cmd := exec.CommandContext(ctx, opts.LumenBinary, "index", workDir)
	cmd.Env = append(os.Environ(),
		"LUMEN_BACKEND="+opts.EmbedBackend,
		"LUMEN_EMBED_MODEL="+opts.EmbedModel,
	)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
