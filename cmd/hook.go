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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/spf13/cobra"
)

// NOTE: Hooks are now declared in hooks/hooks.json (plugin system).
// The hook subcommands remain as the execution targets for those declarations.

func init() {
	rootCmd.AddCommand(hookCmd)
	hookCmd.AddCommand(hookSessionStartCmd)
	hookCmd.AddCommand(hookPreToolUseCmd)
}

var hookCmd = &cobra.Command{
	Use:   "hook",
	Short: "Hook handlers for AI coding agent integration",
}

var hookSessionStartCmd = &cobra.Command{
	Use:   "session-start [mcp-name]",
	Short: "Output SessionStart hook JSON for Claude Code",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runHookSessionStart,
}

// hookOutput is the JSON structure Claude Code expects from a synchronous hook.
type hookOutput struct {
	HookSpecificOutput hookSpecificOutput `json:"hookSpecificOutput"`
}

type hookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext"`
}

func runHookSessionStart(_ *cobra.Command, args []string) error {
	mcpName := filepath.Base(os.Args[0])
	if len(args) > 0 {
		mcpName = args[0]
	}

	content := generateHookContent(mcpName)

	out := hookOutput{
		HookSpecificOutput: hookSpecificOutput{
			HookEventName:     "SessionStart",
			AdditionalContext: content,
		},
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	return enc.Encode(out)
}

// generateHookContent builds the EXTREMELY_IMPORTANT-wrapped directive for the
// SessionStart hook. This is a stronger version of generateSnippet designed to
// achieve near-100% tool trigger rate.
func generateHookContent(mcpName string) string {
	toolRef := "`mcp__" + mcpName + "__semantic_search`"
	return "<EXTREMELY_IMPORTANT>\n" +
		"# Lumen Semantic Search\n\n" +
		"ALWAYS pass your current working directory as the `cwd` parameter when calling " + toolRef + ".\n\n" +
		"ALWAYS use " + toolRef + " as the FIRST tool for code discovery.\n\n" +
		"Before using Grep, Glob, Find, or Read for any search, stop and ask:\n" +
		"\"Do I already know the exact literal string I'm searching for?\"\n" +
		"If not, use " + toolRef + ".\n\n" +
		"## Red Flags — if you think any of these, STOP:\n" +
		"| Thought | Reality |\n" +
		"|---------|--------|\n" +
		fmt.Sprintf("| \"I'll just grep quickly\" | %s is faster for discovery |\n", toolRef) +
		"| \"I know the file name\" | You might not know the best match |\n" +
		"| \"Glob is faster for this\" | Only if you have an exact filename pattern |\n" +
		"| \"This is a simple search\" | Simple searches benefit most from semantic |\n\n" +
		"If semantic search is unavailable, Grep/Glob are acceptable fallbacks.\n" +
		"</EXTREMELY_IMPORTANT>"
}

// --- PreToolUse hook ---

var hookPreToolUseCmd = &cobra.Command{
	Use:   "pre-tool-use [mcp-name]",
	Short: "Intercept Grep/Glob calls and suggest semantic search when appropriate",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runHookPreToolUse,
}

// preToolUseInput is the JSON structure Claude Code sends to PreToolUse hooks.
type preToolUseInput struct {
	ToolName string         `json:"tool_name"`
	Input    map[string]any `json:"tool_input"`
}

// preToolUseOutput is the JSON structure Claude Code expects from a PreToolUse hook.
type preToolUseOutput struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason,omitempty"`
}

func runHookPreToolUse(_ *cobra.Command, args []string) error {
	mcpName := filepath.Base(os.Args[0])
	if len(args) > 0 {
		mcpName = args[0]
	}

	var input preToolUseInput
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		// If we can't parse input, approve silently to avoid blocking.
		return json.NewEncoder(os.Stdout).Encode(preToolUseOutput{Decision: "approve"})
	}

	decision := evaluateToolCall(input, mcpName)

	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	return enc.Encode(decision)
}

// evaluateToolCall determines whether a Grep/Glob call should be intercepted
// with a suggestion to use semantic search instead.
func evaluateToolCall(input preToolUseInput, mcpName string) preToolUseOutput {
	switch input.ToolName {
	case "Grep", "Glob":
		pattern := extractPattern(input)
		if pattern != "" && looksLikeNaturalLanguage(pattern) {
			toolRef := "mcp__" + mcpName + "__semantic_search"
			return preToolUseOutput{
				Decision: "suggest",
				Reason: fmt.Sprintf(
					"This pattern looks like a natural language query. "+
						"Consider using %s instead for better results. "+
						"Grep/Glob work best with exact literal strings, "+
						"while semantic search understands concepts and intent.",
					toolRef,
				),
			}
		}
	}
	return preToolUseOutput{Decision: "approve"}
}

// extractPattern pulls the search pattern from a Grep or Glob tool input.
func extractPattern(input preToolUseInput) string {
	if p, ok := input.Input["pattern"].(string); ok {
		return p
	}
	if p, ok := input.Input["query"].(string); ok {
		return p
	}
	return ""
}

// looksLikeNaturalLanguage returns true if a pattern appears to be a natural
// language query rather than an exact string or regex pattern. Heuristics:
// - Contains spaces (multi-word)
// - No regex metacharacters
// - Longer than 40 characters
// - Predominantly alphabetic characters
func looksLikeNaturalLanguage(pattern string) bool {
	if !strings.Contains(pattern, " ") {
		return false
	}
	if len(pattern) <= 40 {
		return false
	}
	// Regex metacharacters indicate an intentional pattern.
	if strings.ContainsAny(pattern, `.*+?^${}()|[]\`) {
		return false
	}
	// Check that the majority of non-space characters are letters.
	var letters, total int
	for _, r := range pattern {
		if !unicode.IsSpace(r) {
			total++
			if unicode.IsLetter(r) {
				letters++
			}
		}
	}
	if total == 0 {
		return false
	}
	return float64(letters)/float64(total) > 0.7
}
