package harness

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/ory/lumen/benchmarks/tasks"
)

const testTimeout = 10 * time.Minute

// ValidationResult holds the outcome of test validation.
type ValidationResult struct {
	Success        bool         `json:"success"`
	PartialSuccess bool         `json:"partial_success"`
	HasChanges     bool         `json:"has_changes"`
	FailToPass     []TestResult `json:"fail_to_pass"`
	PassToPass     []TestResult `json:"pass_to_pass,omitempty"`
	Error          string       `json:"error,omitempty"`
}

// TestResult holds the pass/fail state of a single test.
type TestResult struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
}

// Validate checks whether a task was completed correctly by running tests.
func Validate(ctx context.Context, task tasks.Task, workDir string) ValidationResult {
	result := ValidationResult{}

	diff, err := GetDiff(ctx, workDir)
	if err != nil {
		result.Error = fmt.Sprintf("get diff: %v", err)
		return result
	}
	result.HasChanges = strings.TrimSpace(diff) != ""

	if !result.HasChanges {
		result.Error = "no changes made"
		return result
	}

	testCtx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	// Run fail_to_pass tests — these must pass after the fix.
	for _, testName := range task.Validation.FailToPass {
		passed := runSingleTest(testCtx, task.Validation.TestCmd, testName, workDir)
		result.FailToPass = append(result.FailToPass, TestResult{
			Name:   testName,
			Passed: passed,
		})
	}

	// Run pass_to_pass tests — these must still pass (no regression).
	for _, testName := range task.Validation.PassToPass {
		passed := runSingleTest(testCtx, task.Validation.TestCmd, testName, workDir)
		result.PassToPass = append(result.PassToPass, TestResult{
			Name:   testName,
			Passed: passed,
		})
	}

	// Determine success.
	allFailToPassPassed := true
	someFailToPassPassed := false
	for _, tr := range result.FailToPass {
		if !tr.Passed {
			allFailToPassPassed = false
		} else {
			someFailToPassPassed = true
		}
	}

	allPassToPassPassed := true
	for _, tr := range result.PassToPass {
		if !tr.Passed {
			allPassToPassPassed = false
			break
		}
	}

	result.Success = allFailToPassPassed && allPassToPassPassed
	result.PartialSuccess = someFailToPassPassed && !result.Success

	return result
}

// runSingleTest runs the test command and checks if the named test passes.
func runSingleTest(ctx context.Context, testCmd, testName, workDir string) bool {
	// Run the full test command. For pytest-style commands, we append -k filter.
	// For go test, we append -run filter.
	fullCmd := testCmd
	if strings.Contains(testCmd, "pytest") {
		fullCmd = fmt.Sprintf("%s -k %q", testCmd, testName)
	} else if strings.Contains(testCmd, "go test") {
		fullCmd = fmt.Sprintf("%s -run %q", testCmd, testName)
	}

	cmd := exec.CommandContext(ctx, "sh", "-c", fullCmd)
	cmd.Dir = workDir
	err := cmd.Run()
	return err == nil
}
