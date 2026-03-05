package harness

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const cloneTimeout = 5 * time.Minute

// SetupWorkspace clones a repository and checks out the specified commit.
// Returns the workspace directory and a cleanup function.
func SetupWorkspace(ctx context.Context, repo, commit string) (string, func(), error) {
	tmpDir, err := os.MkdirTemp("", "bench-workspace-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temp dir: %w", err)
	}

	cleanup := func() { os.RemoveAll(tmpDir) }

	workDir := filepath.Join(tmpDir, "repo")

	cloneCtx, cancel := context.WithTimeout(ctx, cloneTimeout)
	defer cancel()

	if err := runGit(cloneCtx, "", "clone", "--filter=blob:none", repo, workDir); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("git clone %s: %w", repo, err)
	}

	if err := runGit(ctx, workDir, "checkout", commit); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("git checkout %s: %w", commit, err)
	}

	return workDir, cleanup, nil
}

// GetDiff returns the git diff of changes in the workspace.
func GetDiff(ctx context.Context, workDir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "diff")
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git diff: %w", err)
	}
	return string(out), nil
}

func runGit(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
