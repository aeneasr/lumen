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
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/aeneasr/lumen/internal/config"
	"github.com/aeneasr/lumen/internal/embedder"
	"github.com/spf13/cobra"
)

func init() {
	defaultName := filepath.Base(os.Args[0])
	installCmd.Flags().StringP("mcp-name", "n", defaultName, "name to register with claude mcp add")
	installCmd.Flags().StringP("model", "m", "", "skip interactive model selection, use this model")
installCmd.Flags().Bool("dry-run", false, "print actions without executing them")
	installCmd.Flags().Bool("no-mcp", false, "skip MCP registration, only write the rules file")
	installCmd.Flags().Bool("no-rules", false, "skip rules file update, only register MCP")
	installCmd.Flags().Bool("no-hooks", false, "skip SessionStart hook registration")
	rootCmd.AddCommand(installCmd)
}

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install lumen MCP server and configure code search directives",
	Args:  cobra.NoArgs,
	RunE:  runInstall,
}

func runInstall(cmd *cobra.Command, args []string) error {
	mcpName, _ := cmd.Flags().GetString("mcp-name")
	modelFlag, _ := cmd.Flags().GetString("model")
dryRun, _ := cmd.Flags().GetBool("dry-run")
	noMCP, _ := cmd.Flags().GetBool("no-mcp")
	noRules, _ := cmd.Flags().GetBool("no-rules")
	noHooks, _ := cmd.Flags().GetBool("no-hooks")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Phase 1: Service detection
	backend, host, err := detectAndSelectService(ctx)
	if err != nil {
		return err
	}

	// Phase 2: Model selection
	selectedModel, err := selectModel(ctx, backend, host, modelFlag)
	if err != nil {
		return err
	}

	// Phase 3: MCP registration
	if !noMCP {
		if err := registerMCP(mcpName, backend, selectedModel, dryRun); err != nil {
			return err
		}
	}

	// Phase 4: rules file upsert
	if !noRules {
		if err := upsertRules(mcpName, dryRun); err != nil {
			return err
		}
	}

	// Phase 5: SessionStart hook registration
	if !noHooks {
		if err := upsertHook(mcpName, dryRun); err != nil {
			return err
		}
	}

	return nil
}

// --- Phase 1: Service detection ---

func detectServices(ctx context.Context) (ollamaOK, lmstudioOK bool) {
	ollamaHost := config.EnvOrDefault("OLLAMA_HOST", "http://localhost:11434")
	lmstudioHost := config.EnvOrDefault("LM_STUDIO_HOST", "http://localhost:1234")

	type result struct {
		name string
		ok   bool
	}

	ch := make(chan result, 2)
	go func() { ch <- result{"ollama", probeService(ctx, ollamaHost+"/api/tags")} }()
	go func() { ch <- result{"lmstudio", probeService(ctx, lmstudioHost+"/v1/models")} }()

	for range 2 {
		r := <-ch
		switch r.name {
		case "ollama":
			ollamaOK = r.ok
		case "lmstudio":
			lmstudioOK = r.ok
		}
	}

	return ollamaOK, lmstudioOK
}

func probeService(ctx context.Context, url string) bool {
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode < 500
}

func detectAndSelectService(ctx context.Context) (backend, host string, err error) {
	ollamaHost := config.EnvOrDefault("OLLAMA_HOST", "http://localhost:11434")
	lmstudioHost := config.EnvOrDefault("LM_STUDIO_HOST", "http://localhost:1234")

	fmt.Fprintln(os.Stderr, "Detecting embedding services...")

	ollamaOK, lmstudioOK := detectServices(ctx)

	printServiceStatus("Ollama", ollamaHost, ollamaOK)
	printServiceStatus("LM Studio", lmstudioHost, lmstudioOK)

	switch {
	case !ollamaOK && !lmstudioOK:
		return "", "", fmt.Errorf(
			"no embedding service detected\n" +
				"  Install Ollama:    https://ollama.com\n" +
				"  Install LM Studio: https://lmstudio.ai",
		)
	case ollamaOK && !lmstudioOK:
		return config.BackendOllama, ollamaHost, nil
	case !ollamaOK && lmstudioOK:
		return config.BackendLMStudio, lmstudioHost, nil
	default:
		// Both available: prompt
		return promptServiceSelection(ollamaHost, lmstudioHost)
	}
}

func printServiceStatus(name, host string, ok bool) {
	trimHost := strings.TrimPrefix(strings.TrimPrefix(host, "http://"), "https://")
	if ok {
		fmt.Fprintf(os.Stderr, "  \u2713 %-12s (%s)\n", name, trimHost)
	} else {
		fmt.Fprintf(os.Stderr, "  \u2717 %-12s (%s \u2014 not running)\n", name, trimHost)
	}
}

func promptServiceSelection(ollamaHost, lmstudioHost string) (backend, host string, err error) {
	if !stdinIsTTY() {
		return "", "", fmt.Errorf("stdin is not a terminal — use OLLAMA_HOST or LM_STUDIO_HOST env vars to disambiguate")
	}

	fmt.Fprintln(os.Stderr, "\nBoth services are available. Which backend should be used?")
	fmt.Fprintln(os.Stderr, "  1. Ollama")
	fmt.Fprintln(os.Stderr, "  2. LM Studio")
	fmt.Fprint(os.Stderr, "Pick a service [1]: ")

	line, err := readLine()
	if err != nil {
		return "", "", fmt.Errorf("read input: %w", err)
	}

	line = strings.TrimSpace(line)
	if line == "" || line == "1" {
		return config.BackendOllama, ollamaHost, nil
	}
	if line == "2" {
		return config.BackendLMStudio, lmstudioHost, nil
	}
	return "", "", fmt.Errorf("invalid selection %q: enter 1 or 2", line)
}

// --- Phase 2: Model selection ---

type ollamaTagsResponse struct {
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
}

type lmstudioModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

func fetchJSON[T any](ctx context.Context, url string) (T, error) {
	var zero T
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return zero, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return zero, err
	}
	defer func() { _ = resp.Body.Close() }()

	var data T
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return zero, err
	}
	return data, nil
}

func fetchOllamaModels(ctx context.Context, host string) ([]string, error) {
	data, err := fetchJSON[ollamaTagsResponse](ctx, host+"/api/tags")
	if err != nil {
		return nil, err
	}
	names := make([]string, len(data.Models))
	for i, m := range data.Models {
		names[i] = m.Name
	}
	return names, nil
}

func fetchLMStudioModels(ctx context.Context, host string) ([]string, error) {
	data, err := fetchJSON[lmstudioModelsResponse](ctx, host+"/v1/models")
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(data.Data))
	for i, m := range data.Data {
		ids[i] = m.ID
	}
	return ids, nil
}

// modelEntry holds a supported model and whether it is locally available.
type modelEntry struct {
	Name      string
	Spec      embedder.ModelSpec
	Available bool
}

func selectModel(ctx context.Context, backend, host, modelFlag string) (string, error) {
	// Fetch locally available models from the service.
	var available []string
	var err error
	if backend == config.BackendOllama {
		available, err = fetchOllamaModels(ctx, host)
	} else {
		available, err = fetchLMStudioModels(ctx, host)
	}
	if err != nil {
		return "", fmt.Errorf("list models: %w", err)
	}

	if modelFlag != "" {
		if _, known := lookupModelSpec(modelFlag); !known {
			fmt.Fprintf(os.Stderr, "Warning: model %q is not a supported model and may not work correctly\n", modelFlag)
		}
		if !isModelAvailable(modelFlag, available) {
			fmt.Fprintf(os.Stderr, "Warning: model %q not found locally — you may need to pull it first\n", modelFlag)
		}
		return modelFlag, nil
	}

	return promptModelSelection(available, backend)
}

// isModelAvailable checks if a model name (or its alias/canonical form) is in
// the available list.
func isModelAvailable(name string, available []string) bool {
	canonical := canonicalModelName(name)
	for _, a := range available {
		if a == name || canonicalModelName(a) == canonical {
			return true
		}
	}
	return false
}

// supportedModelsForBackend returns all KnownModels entries that match the
// given backend, annotated with local availability.
func supportedModelsForBackend(backend string, available []string) []modelEntry {
	defaultModel := embedder.DefaultOllamaModel
	if backend == config.BackendLMStudio {
		defaultModel = embedder.DefaultLMStudioModel
	}

	var entries []modelEntry
	for name, spec := range embedder.KnownModels {
		if spec.Backend != "" && spec.Backend != backend {
			continue
		}
		entries = append(entries, modelEntry{
			Name:      name,
			Spec:      spec,
			Available: isModelAvailable(name, available),
		})
	}

	// Sort: default first, then available before not-available, then alphabetically.
	slices.SortFunc(entries, func(a, b modelEntry) int {
		aDefault := modelMatchesDefault(a.Name, defaultModel)
		bDefault := modelMatchesDefault(b.Name, defaultModel)
		if aDefault != bDefault {
			if aDefault {
				return -1
			}
			return 1
		}
		if a.Available != b.Available {
			if a.Available {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})

	return entries
}

func promptModelSelection(available []string, backend string) (string, error) {
	if !stdinIsTTY() {
		return "", fmt.Errorf("stdin is not a terminal — use --model to specify a model non-interactively")
	}

	entries := supportedModelsForBackend(backend, available)
	if len(entries) == 0 {
		return "", fmt.Errorf("no supported models for backend %q", backend)
	}

	defaultModel := embedder.DefaultOllamaModel
	if backend == config.BackendLMStudio {
		defaultModel = embedder.DefaultLMStudioModel
	}

	backendLabel := "Ollama"
	pullCmd := "ollama pull"
	if backend == config.BackendLMStudio {
		backendLabel = "LM Studio"
		pullCmd = "lms get"
	}

	fmt.Fprintf(os.Stderr, "\nSupported models (%s):\n", backendLabel)
	for i, e := range entries {
		status := "\u2713 ready"
		if !e.Available {
			status = "\u2717 needs pull"
		}
		recommended := ""
		if modelMatchesDefault(e.Name, defaultModel) {
			recommended = "  [recommended]"
		}
		fmt.Fprintf(os.Stderr, "  %d. %-40s %4d dims  %5d ctx  %-13s%s\n",
			i+1, e.Name, e.Spec.Dims, e.Spec.CtxLength, status, recommended)
	}

	fmt.Fprint(os.Stderr, "\nPick a model [1]: ")

	line, err := readLine()
	if err != nil {
		return "", fmt.Errorf("read input: %w", err)
	}

	line = strings.TrimSpace(line)
	idx := 0
	if line == "" {
		idx = 0
	} else if n, err := strconv.Atoi(line); err == nil {
		if n < 1 || n > len(entries) {
			return "", fmt.Errorf("invalid selection %d: enter 1-%d", n, len(entries))
		}
		idx = n - 1
	} else {
		// Try model name directly.
		found := false
		for i, e := range entries {
			if e.Name == line {
				idx = i
				found = true
				break
			}
		}
		if !found {
			return "", fmt.Errorf("invalid selection %q", line)
		}
	}

	selected := entries[idx]
	if !selected.Available {
		fmt.Fprintf(os.Stderr, "\nModel %q is not available locally.\n", selected.Name)
		fmt.Fprintf(os.Stderr, "Pull it with: %s %s\n", pullCmd, selected.Name)
		return "", fmt.Errorf("model %q not available — pull it first", selected.Name)
	}

	return selected.Name, nil
}

// --- Phase 3: MCP registration ---

func registerMCP(mcpName, backend, model string, dryRun bool) error {
	binaryPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve binary path: %w", err)
	}

	fmt.Fprintln(os.Stderr, "\nRegistering MCP server...")

	claudeErr := registerClaudeCode(mcpName, binaryPath, backend, model, dryRun)
	codexErr := registerCodex(mcpName, binaryPath, backend, model, dryRun)

	if claudeErr != nil && !isNotFound(claudeErr) {
		fmt.Fprintf(os.Stderr, "  Warning: claude registration failed: %v\n", claudeErr)
	}
	if codexErr != nil && !isNotFound(codexErr) {
		fmt.Fprintf(os.Stderr, "  Warning: codex registration failed: %v\n", codexErr)
	}

	return nil
}

func registerClaudeCode(mcpName, binaryPath, backend, model string, dryRun bool) error {
	if _, err := exec.LookPath("claude"); err != nil {
		fmt.Fprintf(os.Stderr, "  ! Claude Code  (claude not in PATH — skipping)\n")
		return err
	}

	// Remove existing entry first (ignore errors — may not exist).
	if !dryRun {
		_ = exec.Command("claude", "mcp", "remove", "--scope", "user", mcpName).Run()
	}

	addArgs := []string{
		"mcp", "add",
		"--scope", "user",
		"-eLUMEN_BACKEND=" + backend,
		"-eLUMEN_EMBED_MODEL=" + model,
		mcpName, binaryPath, "--", "stdio",
	}

	cmdStr := "claude " + strings.Join(addArgs, " ")
	if dryRun {
		fmt.Fprintf(os.Stderr, "  [dry-run] %s\n", cmdStr)
		return nil
	}

	out, err := exec.Command("claude", addArgs...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", cmdStr, strings.TrimSpace(string(out)))
	}
	fmt.Fprintf(os.Stderr, "  \u2713 Claude Code  (%s)\n", cmdStr)
	return nil
}

func registerCodex(mcpName, binaryPath, backend, model string, dryRun bool) error {
	if _, err := exec.LookPath("codex"); err != nil {
		// Codex not in PATH: skip silently
		return err
	}

	// Remove existing entry first (ignore errors — may not exist).
	if !dryRun {
		_ = exec.Command("codex", "mcp", "remove", mcpName).Run()
	}

	addArgs := []string{
		"mcp", "add",
		"--env", "LUMEN_BACKEND=" + backend,
		"--env", "LUMEN_EMBED_MODEL=" + model,
		mcpName, binaryPath, "stdio",
	}

	cmdStr := "codex " + strings.Join(addArgs, " ")
	if dryRun {
		fmt.Fprintf(os.Stderr, "  [dry-run] %s\n", cmdStr)
		return nil
	}

	out, err := exec.Command("codex", addArgs...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", cmdStr, strings.TrimSpace(string(out)))
	}
	fmt.Fprintf(os.Stderr, "  \u2713 Codex        (%s)\n", cmdStr)
	return nil
}

func isNotFound(err error) bool {
	return errors.Is(err, exec.ErrNotFound)
}

// --- Phase 4: rules file upsert ---

func upsertRules(mcpName string, dryRun bool) error {
	targetFile := rulesFilePath(mcpName)

	fmt.Fprintf(os.Stderr, "\nWriting rules file...\n")

	content := generateSnippet(mcpName)

	if dryRun {
		fmt.Fprintf(os.Stderr, "  [dry-run] Would write rules to %s (mcp-name: %s)\n", targetFile, mcpName)
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(targetFile), 0o755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	if err := os.WriteFile(targetFile, []byte(content+"\n"), 0o644); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	fmt.Fprintf(os.Stderr, "  \u2713 Wrote rules to %s (mcp-name: %s)\n", targetFile, mcpName)
	return nil
}

// rulesFilePath returns ~/.claude/rules/{mcpName}.md.
func rulesFilePath(mcpName string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".claude", "rules", mcpName+".md")
}

// generateSnippet returns the code search directive for the given MCP server name.
func generateSnippet(mcpName string) string {
	toolRef := "`mcp__" + mcpName + "__semantic_search`"
	return "# Code Search\n\n" +
		"ALWAYS use " + toolRef + " as the FIRST tool for code discovery and exploration.\n" +
		"Do NOT default to Grep, Glob, or Read for search tasks — only use them for exact literal string lookups.\n\n" +
		"Before using Grep, Glob, Find, or Read for any search, stop and ask:\n\n" +
		"> \"Do I already know the exact literal string I'm searching for?\"\n\n" +
		"- **No** — understanding how something works, finding where something is implemented, exploring\n" +
		"  unfamiliar code → use " + toolRef + "\n" +
		"- **Yes** — a specific function name, import path, variable name, or error message → Grep/Glob is acceptable\n\n" +
		"If semantic search is unavailable, Grep/Glob are acceptable fallbacks."
}

// canonicalModelName resolves a model name to its canonical form by stripping
// the ":latest" tag and checking the alias map.
func canonicalModelName(name string) string {
	stripped := strings.TrimSuffix(name, ":latest")
	if canonical, ok := embedder.ModelAliases[stripped]; ok {
		return canonical
	}
	return stripped
}

// modelMatchesDefault reports whether a model name matches a default, ignoring
// the ":latest" tag that Ollama appends and resolving known aliases.
func modelMatchesDefault(model, defaultModel string) bool {
	return canonicalModelName(model) == defaultModel
}

// lookupModelSpec looks up a model in the KnownModels registry, falling back
// to a lookup with the ":latest" tag stripped and alias resolution.
func lookupModelSpec(name string) (embedder.ModelSpec, bool) {
	if spec, ok := embedder.KnownModels[name]; ok {
		return spec, true
	}
	spec, ok := embedder.KnownModels[canonicalModelName(name)]
	return spec, ok
}

// --- Phase 5: SessionStart hook registration ---

// upsertHook registers a SessionStart hook in ~/.claude/settings.json that
// injects EXTREMELY_IMPORTANT-wrapped directives into every conversation.
func upsertHook(mcpName string, dryRun bool) error {
	binaryPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve binary path: %w", err)
	}

	settingsPath := claudeSettingsPath()

	fmt.Fprintln(os.Stderr, "\nRegistering SessionStart hook...")

	if dryRun {
		fmt.Fprintf(os.Stderr, "  [dry-run] Would register SessionStart hook in %s\n", settingsPath)
		return nil
	}

	settings, err := readSettings(settingsPath)
	if err != nil {
		return err
	}

	addSessionStartHook(settings, binaryPath, mcpName)

	if err := writeSettings(settingsPath, settings); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "  ✓ Registered SessionStart hook in %s\n", settingsPath)
	return nil
}

// claudeSettingsPath returns ~/.claude/settings.json.
func claudeSettingsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".claude", "settings.json")
}

// readSettings reads and parses ~/.claude/settings.json, returning an empty
// map if the file does not exist.
func readSettings(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("read settings: %w", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("parse settings: %w", err)
	}
	return settings, nil
}

// writeSettings marshals settings with indentation and writes to path,
// creating parent directories if needed.
func writeSettings(path string, settings map[string]any) error {
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create settings directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	return nil
}

// addSessionStartHook merges a lumen SessionStart hook entry into the settings
// map, replacing any existing hook whose command references the same binary.
func addSessionStartHook(settings map[string]any, binaryPath, mcpName string) {
	hookCommand := binaryPath + " hook session-start " + mcpName

	hookEntry := map[string]any{
		"type":    "command",
		"command": hookCommand,
	}

	matcherEntry := map[string]any{
		"matcher": "startup|resume|clear|compact",
		"hooks":   []any{hookEntry},
	}

	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		hooks = map[string]any{}
		settings["hooks"] = hooks
	}

	sessionStartHooks, ok := hooks["SessionStart"].([]any)
	if !ok {
		sessionStartHooks = []any{}
	}

	// Remove any existing lumen hooks (matching mcpName or binary path in command).
	filtered := make([]any, 0, len(sessionStartHooks))
	for _, entry := range sessionStartHooks {
		if !hookEntryMatchesMCPName(entry, mcpName) && !hookEntryMatchesBinary(entry, binaryPath) {
			filtered = append(filtered, entry)
		}
	}

	hooks["SessionStart"] = append(filtered, matcherEntry)
}

// hookEntryMatchesBinary returns true if a hook entry's command contains the
// given binary path.
func hookEntryMatchesBinary(entry any, binaryPath string) bool {
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
		if ok && strings.Contains(cmd, binaryPath) {
			return true
		}
	}
	return false
}

// --- Helpers ---

func stdinIsTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func readLine() (string, error) {
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return scanner.Text(), nil
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", nil
}
