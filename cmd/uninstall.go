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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/aeneasr/lumen/internal/config"
	"github.com/spf13/cobra"
)

func init() {
	defaultName := filepath.Base(os.Args[0])
	uninstallCmd.Flags().StringP("mcp-name", "n", defaultName, "MCP server name to remove")
uninstallCmd.Flags().Bool("dry-run", false, "print actions without executing them")
	uninstallCmd.Flags().Bool("no-mcp", false, "skip MCP removal")
	uninstallCmd.Flags().Bool("no-rules", false, "skip rules file removal")
	uninstallCmd.Flags().Bool("no-hooks", false, "skip SessionStart hook removal")
	uninstallCmd.Flags().Bool("purge-data", false, "remove ALL lumen index data (~/.local/share/lumen/)")
	rootCmd.AddCommand(uninstallCmd)
}

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove lumen MCP server registration, code search directives, and optionally index data",
	Args:  cobra.NoArgs,
	RunE:  runUninstall,
}

func runUninstall(cmd *cobra.Command, args []string) error {
	mcpName, _ := cmd.Flags().GetString("mcp-name")
dryRun, _ := cmd.Flags().GetBool("dry-run")
	noMCP, _ := cmd.Flags().GetBool("no-mcp")
	noRules, _ := cmd.Flags().GetBool("no-rules")
	noHooks, _ := cmd.Flags().GetBool("no-hooks")
	purgeData, _ := cmd.Flags().GetBool("purge-data")

	// Phase 1: Remove MCP registration
	if !noMCP {
		if err := removeMCP(mcpName, dryRun); err != nil {
			return err
		}
	}

	// Phase 2: Remove rules file
	if !noRules {
		if err := removeRulesFile(mcpName, dryRun); err != nil {
			return err
		}
	}

	// Phase 3: Remove SessionStart hook
	if !noHooks {
		if err := removeHook(mcpName, dryRun); err != nil {
			return err
		}
	}

	// Phase 4: Purge index data
	if purgeData {
		if err := purgeIndexData(dryRun); err != nil {
			return err
		}
	}

	return nil
}

// --- Phase 1: Remove MCP registration ---

func removeMCP(mcpName string, dryRun bool) error {
	fmt.Fprintln(os.Stderr, "Removing MCP server registration...")

	claudeErr := unregisterClaudeCode(mcpName, dryRun)
	codexErr := unregisterCodex(mcpName, dryRun)

	if claudeErr != nil && !isNotFound(claudeErr) {
		fmt.Fprintf(os.Stderr, "  Warning: claude removal failed: %v\n", claudeErr)
	}
	if codexErr != nil && !isNotFound(codexErr) {
		fmt.Fprintf(os.Stderr, "  Warning: codex removal failed: %v\n", codexErr)
	}

	return nil
}

func unregisterClaudeCode(mcpName string, dryRun bool) error {
	if _, err := exec.LookPath("claude"); err != nil {
		fmt.Fprintf(os.Stderr, "  ! Claude Code  (claude not in PATH — skipping)\n")
		return err
	}

	args := []string{"mcp", "remove", mcpName}
	cmdStr := "claude " + strings.Join(args, " ")

	if dryRun {
		fmt.Fprintf(os.Stderr, "  [dry-run] %s\n", cmdStr)
		return nil
	}

	out, err := exec.Command("claude", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", cmdStr, strings.TrimSpace(string(out)))
	}
	fmt.Fprintf(os.Stderr, "  ✓ Claude Code  (%s)\n", cmdStr)
	return nil
}

func unregisterCodex(mcpName string, dryRun bool) error {
	if _, err := exec.LookPath("codex"); err != nil {
		// Codex not in PATH: skip silently
		return err
	}

	args := []string{"mcp", "remove", mcpName}
	cmdStr := "codex " + strings.Join(args, " ")

	if dryRun {
		fmt.Fprintf(os.Stderr, "  [dry-run] %s\n", cmdStr)
		return nil
	}

	out, err := exec.Command("codex", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", cmdStr, strings.TrimSpace(string(out)))
	}
	fmt.Fprintf(os.Stderr, "  ✓ Codex        (%s)\n", cmdStr)
	return nil
}

// --- Phase 2: Remove rules file ---

func removeRulesFile(mcpName string, dryRun bool) error {
	targetFile := rulesFilePath(mcpName)

	fmt.Fprintln(os.Stderr, "\nRemoving rules file...")

	if !fileExists(targetFile) {
		fmt.Fprintf(os.Stderr, "  %s does not exist — nothing to remove.\n", targetFile)
		return nil
	}

	if dryRun {
		fmt.Fprintf(os.Stderr, "  [dry-run] Would delete %s\n", targetFile)
		return nil
	}

	if err := os.Remove(targetFile); err != nil {
		return fmt.Errorf("remove rules file: %w", err)
	}
	fmt.Fprintf(os.Stderr, "  ✓ Deleted %s\n", targetFile)
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// --- Phase 3: Remove SessionStart hook ---

func removeHook(mcpName string, dryRun bool) error {
	binaryPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve binary path: %w", err)
	}

	settingsPath := claudeSettingsPath()

	fmt.Fprintln(os.Stderr, "\nRemoving SessionStart hook...")

	if !fileExists(settingsPath) {
		fmt.Fprintf(os.Stderr, "  %s does not exist — nothing to remove.\n", settingsPath)
		return nil
	}

	if dryRun {
		fmt.Fprintf(os.Stderr, "  [dry-run] Would remove SessionStart hook from %s\n", settingsPath)
		return nil
	}

	settings, err := readSettings(settingsPath)
	if err != nil {
		return err
	}

	if removeSessionStartHook(settings, binaryPath, mcpName) {
		if err := writeSettings(settingsPath, settings); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "  ✓ Removed SessionStart hook from %s\n", settingsPath)
	} else {
		fmt.Fprintf(os.Stderr, "  No lumen SessionStart hook found — nothing to remove.\n")
	}

	return nil
}

// removeSessionStartHook removes any SessionStart hook entries whose command
// references the given binary path or mcpName. Returns true if any were removed.
func removeSessionStartHook(settings map[string]any, binaryPath, mcpName string) bool {
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		return false
	}

	sessionStartHooks, ok := hooks["SessionStart"].([]any)
	if !ok {
		return false
	}

	filtered := make([]any, 0, len(sessionStartHooks))
	for _, entry := range sessionStartHooks {
		if !hookEntryMatchesBinary(entry, binaryPath) && !hookEntryMatchesMCPName(entry, mcpName) {
			filtered = append(filtered, entry)
		}
	}

	if len(filtered) == len(sessionStartHooks) {
		return false
	}

	if len(filtered) == 0 {
		delete(hooks, "SessionStart")
	} else {
		hooks["SessionStart"] = filtered
	}

	// Clean up empty hooks map.
	if len(hooks) == 0 {
		delete(settings, "hooks")
	}

	return true
}

// hookEntryMatchesMCPName returns true if a hook entry's command contains the
// given MCP name in a "hook session-start <name>" pattern.
func hookEntryMatchesMCPName(entry any, mcpName string) bool {
	m, ok := entry.(map[string]any)
	if !ok {
		return false
	}
	hooksList, ok := m["hooks"].([]any)
	if !ok {
		return false
	}
	for _, h := range hooksList {
		hm, ok := h.(map[string]any)
		if !ok {
			continue
		}
		cmd, ok := hm["command"].(string)
		if ok && strings.Contains(cmd, "hook session-start "+mcpName) {
			return true
		}
	}
	return false
}

// --- Phase 4: Purge index data ---

func purgeIndexData(dryRun bool) error {
	dataDir := filepath.Join(config.XDGDataDir(), "lumen")

	if !fileExists(dataDir) {
		fmt.Fprintln(os.Stderr, "\nNo index data found — nothing to purge.")
		return nil
	}

	if dryRun {
		fmt.Fprintf(os.Stderr, "\n  [dry-run] Would remove %s\n", dataDir)
		return nil
	}

	if err := os.RemoveAll(dataDir); err != nil {
		return fmt.Errorf("remove index data: %w", err)
	}
	fmt.Fprintf(os.Stderr, "\n  ✓ Removed index data (%s)\n", dataDir)
	return nil
}
