package tasks

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	// SWE-bench Lite dataset on HuggingFace (JSONL format).
	sweBenchLiteURL = "https://huggingface.co/datasets/princeton-nlp/SWE-bench_Lite/resolve/main/data/test-00000-of-00001.parquet"
	// We use the JSONL endpoint instead for easier parsing.
	sweBenchLiteJSONL = "https://datasets-server.huggingface.co/rows?dataset=princeton-nlp%2FSWE-bench_Lite&config=default&split=test&offset=0&length=100"
)

// sweBenchRow represents a single row from the SWE-bench Lite dataset.
type sweBenchRow struct {
	InstanceID       string `json:"instance_id"`
	Repo             string `json:"repo"`
	BaseCommit       string `json:"base_commit"`
	ProblemStatement string `json:"problem_statement"`
	TestPatch        string `json:"test_patch"`
	FailToPassStr    string `json:"FAIL_TO_PASS"`
	PassToPassStr    string `json:"PASS_TO_PASS"`
}

// hfResponse wraps the HuggingFace datasets API response.
type hfResponse struct {
	Rows []struct {
		Row sweBenchRow `json:"row"`
	} `json:"rows"`
}

// FetchSWEBenchLite downloads SWE-bench Lite tasks from HuggingFace and writes
// them as individual JSON files to outputDir.
func FetchSWEBenchLite(outputDir string, count int) (int, error) {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return 0, fmt.Errorf("create output dir: %w", err)
	}

	url := fmt.Sprintf(
		"https://datasets-server.huggingface.co/rows?dataset=princeton-nlp%%2FSWE-bench_Lite&config=default&split=test&offset=0&length=%d",
		count,
	)

	resp, err := http.Get(url)
	if err != nil {
		return 0, fmt.Errorf("fetch dataset: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("HuggingFace API returned %d: %s", resp.StatusCode, string(body))
	}

	var hfResp hfResponse
	if err := json.NewDecoder(resp.Body).Decode(&hfResp); err != nil {
		return 0, fmt.Errorf("decode response: %w", err)
	}

	written := 0
	for _, row := range hfResp.Rows {
		task := convertSWEBenchRow(row.Row)
		if task == nil {
			continue
		}

		data, err := json.MarshalIndent(task, "", "  ")
		if err != nil {
			continue
		}

		filename := filepath.Join(outputDir, task.ID+".json")
		if err := os.WriteFile(filename, data, 0o644); err != nil {
			return written, fmt.Errorf("write %s: %w", filename, err)
		}
		written++
	}

	return written, nil
}

func convertSWEBenchRow(row sweBenchRow) *Task {
	if row.InstanceID == "" || row.Repo == "" || row.BaseCommit == "" {
		return nil
	}

	failToPass := parseTestList(row.FailToPassStr)
	if len(failToPass) == 0 {
		return nil
	}

	passToPass := parseTestList(row.PassToPassStr)

	repoURL := "https://github.com/" + row.Repo

	// Detect language from repo name.
	lang := detectLanguage(row.Repo)

	// Detect test command based on repo.
	testCmd := detectTestCmd(row.Repo)

	return &Task{
		ID:               row.InstanceID,
		Source:           "swe-bench-lite",
		Repo:             repoURL,
		BaseCommit:       row.BaseCommit,
		ProblemStatement: row.ProblemStatement,
		Language:         lang,
		Difficulty:       "medium",
		Category:         "bug_fix",
		Validation: Validation{
			TestCmd:    testCmd,
			FailToPass: failToPass,
			PassToPass: passToPass,
		},
		MaxBudgetUSD: 2.0,
		MaxTurns:     30,
	}
}

func parseTestList(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" || s == "[]" {
		return nil
	}

	// SWE-bench stores test lists as JSON arrays or Python repr.
	var result []string
	if err := json.Unmarshal([]byte(s), &result); err == nil {
		return result
	}

	// Fallback: try Python repr format ['test1', 'test2'].
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	parts := strings.Split(s, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.Trim(p, "'\"")
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func detectLanguage(repo string) string {
	repo = strings.ToLower(repo)
	switch {
	case strings.Contains(repo, "django"),
		strings.Contains(repo, "flask"),
		strings.Contains(repo, "sympy"),
		strings.Contains(repo, "scikit"),
		strings.Contains(repo, "matplotlib"),
		strings.Contains(repo, "requests"),
		strings.Contains(repo, "sphinx"),
		strings.Contains(repo, "astropy"),
		strings.Contains(repo, "pylint"),
		strings.Contains(repo, "pytest"),
		strings.Contains(repo, "python"):
		return "python"
	case strings.Contains(repo, "typescript"),
		strings.Contains(repo, "node"):
		return "typescript"
	default:
		return "python" // SWE-bench Lite is Python-heavy
	}
}

func detectTestCmd(repo string) string {
	repo = strings.ToLower(repo)
	switch {
	case strings.Contains(repo, "django"):
		return "python -m django test --settings=tests.test_sqlite --parallel 1"
	case strings.Contains(repo, "pytest"):
		return "python -m pytest"
	default:
		return "python -m pytest"
	}
}
