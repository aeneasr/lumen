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
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- addSessionStartHook tests ---

func TestAddSessionStartHook_EmptySettings(t *testing.T) {
	settings := map[string]any{}
	addSessionStartHook(settings, "/usr/local/bin/lumen", "lumen")

	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		t.Fatal("expected hooks key in settings")
	}
	sessionStart, ok := hooks["SessionStart"].([]any)
	if !ok || len(sessionStart) != 1 {
		t.Fatalf("expected 1 SessionStart hook, got %v", hooks["SessionStart"])
	}

	entry := sessionStart[0].(map[string]any)
	if entry["matcher"] != "startup|resume|clear|compact" {
		t.Errorf("unexpected matcher: %v", entry["matcher"])
	}
	hooksList := entry["hooks"].([]any)
	if len(hooksList) != 1 {
		t.Fatalf("expected 1 hook entry, got %d", len(hooksList))
	}
	cmd := hooksList[0].(map[string]any)["command"].(string)
	if !strings.Contains(cmd, "/usr/local/bin/lumen") {
		t.Errorf("command should contain binary path, got: %s", cmd)
	}
	if !strings.Contains(cmd, "hook session-start lumen") {
		t.Errorf("command should contain 'hook session-start lumen', got: %s", cmd)
	}
}

func TestAddSessionStartHook_ReplacesExisting(t *testing.T) {
	settings := map[string]any{}
	addSessionStartHook(settings, "/usr/local/bin/lumen", "lumen")
	addSessionStartHook(settings, "/usr/local/bin/lumen", "lumen")

	hooks := settings["hooks"].(map[string]any)
	sessionStart := hooks["SessionStart"].([]any)
	if len(sessionStart) != 1 {
		t.Errorf("expected 1 SessionStart hook after re-add, got %d", len(sessionStart))
	}
}

func TestAddSessionStartHook_PreservesOtherHooks(t *testing.T) {
	settings := map[string]any{
		"hooks": map[string]any{
			"SessionStart": []any{
				map[string]any{
					"matcher": "startup",
					"hooks": []any{
						map[string]any{"type": "command", "command": "/other/tool hook"},
					},
				},
			},
		},
	}

	addSessionStartHook(settings, "/usr/local/bin/lumen", "lumen")

	hooks := settings["hooks"].(map[string]any)
	sessionStart := hooks["SessionStart"].([]any)
	if len(sessionStart) != 2 {
		t.Errorf("expected 2 SessionStart hooks (other + lumen), got %d", len(sessionStart))
	}
}

// --- readSettings / writeSettings round-trip ---

func TestReadWriteSettings_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	settings := map[string]any{"foo": "bar"}
	if err := writeSettings(path, settings); err != nil {
		t.Fatal(err)
	}

	got, err := readSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if got["foo"] != "bar" {
		t.Errorf("expected foo=bar, got %v", got["foo"])
	}

	data, _ := os.ReadFile(path)
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("written file is not valid JSON: %v", err)
	}
}

func TestReadSettings_MissingFile(t *testing.T) {
	got, err := readSettings("/nonexistent/path/settings.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map for missing file, got %v", got)
	}
}

// --- generateSnippet tests ---

func TestGenerateSnippet(t *testing.T) {
	cases := []struct {
		name    string
		wantRef string
	}{
		{"lumen", "mcp__lumen__semantic_search"},
		{"my-custom-server", "mcp__my-custom-server__semantic_search"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snippet := generateSnippet(tc.name)
			if !strings.Contains(snippet, tc.wantRef) {
				t.Errorf("expected %q in snippet, got: %s", tc.wantRef, snippet)
			}
			if !strings.HasPrefix(snippet, "# Code Search") {
				t.Error("snippet should start with '# Code Search'")
			}
		})
	}
}

// --- rulesFilePath tests ---

func TestRulesFilePath_Default(t *testing.T) {
	got := rulesFilePath("lumen")
	if !strings.HasSuffix(got, filepath.Join(".claude", "rules", "lumen.md")) {
		t.Errorf("expected default rules path, got %q", got)
	}
}

func TestRulesFilePath_CustomName(t *testing.T) {
	got := rulesFilePath("my-server")
	if !strings.HasSuffix(got, filepath.Join(".claude", "rules", "my-server.md")) {
		t.Errorf("expected default rules path with custom name, got %q", got)
	}
}
