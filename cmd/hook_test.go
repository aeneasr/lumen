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
	"strings"
	"testing"
)

func TestGenerateHookContent(t *testing.T) {
	cases := []struct {
		mcpName string
		wantRef string
	}{
		{"lumen", "mcp__lumen__semantic_search"},
		{"my-custom-server", "mcp__my-custom-server__semantic_search"},
	}

	for _, tc := range cases {
		t.Run(tc.mcpName, func(t *testing.T) {
			content := generateHookContent(tc.mcpName)
			if !strings.HasPrefix(content, "<EXTREMELY_IMPORTANT>") {
				t.Error("content should start with <EXTREMELY_IMPORTANT>")
			}
			if !strings.HasSuffix(content, "</EXTREMELY_IMPORTANT>") {
				t.Error("content should end with </EXTREMELY_IMPORTANT>")
			}
			if !strings.Contains(content, tc.wantRef) {
				t.Errorf("expected %q in content, got: %s", tc.wantRef, content)
			}
			if !strings.Contains(content, "Red Flags") {
				t.Error("content should contain rationalization-blocking table")
			}
		})
	}
}

func TestEvaluateToolCall_GrepNaturalLanguage(t *testing.T) {
	input := preToolUseInput{
		ToolName: "Grep",
		Input:    map[string]any{"pattern": "how does the authentication middleware handle token refresh in this codebase"},
	}
	result := evaluateToolCall(input, "lumen")
	if result.Decision != "suggest" {
		t.Errorf("expected suggest for natural language pattern, got %q", result.Decision)
	}
	if !strings.Contains(result.Reason, "mcp__lumen__semantic_search") {
		t.Error("reason should reference semantic_search tool")
	}
}

func TestEvaluateToolCall_GrepExactString(t *testing.T) {
	input := preToolUseInput{
		ToolName: "Grep",
		Input:    map[string]any{"pattern": "handleSemanticSearch"},
	}
	result := evaluateToolCall(input, "lumen")
	if result.Decision != "approve" {
		t.Errorf("expected approve for exact string pattern, got %q", result.Decision)
	}
}

func TestEvaluateToolCall_GrepRegex(t *testing.T) {
	input := preToolUseInput{
		ToolName: "Grep",
		Input:    map[string]any{"pattern": `func\s+\w+Search.*context\.Context`},
	}
	result := evaluateToolCall(input, "lumen")
	if result.Decision != "approve" {
		t.Errorf("expected approve for regex pattern, got %q", result.Decision)
	}
}

func TestEvaluateToolCall_GlobApproved(t *testing.T) {
	input := preToolUseInput{
		ToolName: "Glob",
		Input:    map[string]any{"pattern": "**/*.go"},
	}
	result := evaluateToolCall(input, "lumen")
	if result.Decision != "approve" {
		t.Errorf("expected approve for glob pattern, got %q", result.Decision)
	}
}

func TestEvaluateToolCall_OtherToolApproved(t *testing.T) {
	input := preToolUseInput{
		ToolName: "Read",
		Input:    map[string]any{"path": "/some/file.go"},
	}
	result := evaluateToolCall(input, "lumen")
	if result.Decision != "approve" {
		t.Errorf("expected approve for non-Grep/Glob tool, got %q", result.Decision)
	}
}

func TestEvaluateToolCall_ShortPattern(t *testing.T) {
	input := preToolUseInput{
		ToolName: "Grep",
		Input:    map[string]any{"pattern": "find this function"},
	}
	result := evaluateToolCall(input, "lumen")
	if result.Decision != "approve" {
		t.Errorf("expected approve for short pattern (<=40 chars), got %q", result.Decision)
	}
}

func TestLooksLikeNaturalLanguage(t *testing.T) {
	cases := []struct {
		pattern string
		want    bool
	}{
		{"handleSemanticSearch", false}, // no spaces
		{"find this", false},            // too short
		{"how does the authentication system work in this project", true},
		{`func\s+\w+`, false}, // regex
		{"**/*.go", false},    // glob
		{"where is the database connection pool configured and initialized", true},
		{"1234567890 1234567890 1234567890 1234567890 12345", false}, // mostly digits
	}

	for _, tc := range cases {
		t.Run(tc.pattern, func(t *testing.T) {
			got := looksLikeNaturalLanguage(tc.pattern)
			if got != tc.want {
				t.Errorf("looksLikeNaturalLanguage(%q) = %v, want %v", tc.pattern, got, tc.want)
			}
		})
	}
}

func TestPreToolUseOutputJSON(t *testing.T) {
	out := preToolUseOutput{Decision: "suggest", Reason: "try semantic search"}
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if parsed["decision"] != "suggest" {
		t.Errorf("expected decision=suggest, got %v", parsed["decision"])
	}
}

func TestHookOutputJSON(t *testing.T) {
	content := generateHookContent("lumen")
	out := hookOutput{
		HookSpecificOutput: hookSpecificOutput{
			HookEventName:     "SessionStart",
			AdditionalContext: content,
		},
	}

	data, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	hso, ok := parsed["hookSpecificOutput"].(map[string]any)
	if !ok {
		t.Fatal("missing hookSpecificOutput key")
	}
	if hso["hookEventName"] != "SessionStart" {
		t.Errorf("hookEventName = %v, want SessionStart", hso["hookEventName"])
	}
	ctx, ok := hso["additionalContext"].(string)
	if !ok || !strings.Contains(ctx, "EXTREMELY_IMPORTANT") {
		t.Error("additionalContext should contain EXTREMELY_IMPORTANT")
	}
}
