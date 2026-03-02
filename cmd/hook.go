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

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(hookCmd)
	hookCmd.AddCommand(hookSessionStartCmd)
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
