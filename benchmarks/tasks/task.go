package tasks

// Task represents a single benchmark task derived from SWE-bench or similar.
type Task struct {
	ID               string     `json:"id"`
	Source           string     `json:"source"`
	Repo             string     `json:"repo"`
	BaseCommit       string     `json:"base_commit"`
	ProblemStatement string     `json:"problem_statement"`
	Language         string     `json:"language"`
	Difficulty       string     `json:"difficulty"`
	Category         string     `json:"category"`
	Validation       Validation `json:"validation"`
	ExpectedFiles    []string   `json:"expected_files_changed,omitempty"`
	MaxBudgetUSD     float64    `json:"max_budget_usd"`
	MaxTurns         int        `json:"max_turns"`
}

// Validation defines how to verify a task was completed correctly.
type Validation struct {
	TestCmd    string   `json:"test_cmd"`
	FailToPass []string `json:"fail_to_pass"`
	PassToPass []string `json:"pass_to_pass,omitempty"`
}

// Scenario represents an experimental condition.
type Scenario string

const (
	ScenarioBaseline Scenario = "baseline"
	ScenarioMCPOnly  Scenario = "mcp-only"
	ScenarioMCPFull  Scenario = "mcp-full"
)

// AllScenarios returns all benchmark scenarios.
func AllScenarios() []Scenario {
	return []Scenario{ScenarioBaseline, ScenarioMCPOnly, ScenarioMCPFull}
}
