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
