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
	"path/filepath"
	"testing"
)

func TestRulesFilePath_UninstallDefault(t *testing.T) {
	got := rulesFilePath("my-server")
	want := filepath.Join(".claude", "rules", "my-server.md")
	if len(got) < len(want) || got[len(got)-len(want):] != want {
		t.Errorf("expected path ending in %q, got %q", want, got)
	}
}

// --- removeSessionStartHook tests ---

func TestRemoveSessionStartHook_MatchesByBinaryPath(t *testing.T) {
	settings := map[string]any{}
	addSessionStartHook(settings, "/usr/local/bin/lumen", "lumen")

	removed := removeSessionStartHook(settings, "/usr/local/bin/lumen", "lumen")
	if !removed {
		t.Error("expected hook to be removed")
	}

	// Hooks map should be cleaned up entirely.
	if _, ok := settings["hooks"]; ok {
		t.Error("expected hooks key to be removed when empty")
	}
}

func TestRemoveSessionStartHook_MatchesByMCPName(t *testing.T) {
	settings := map[string]any{
		"hooks": map[string]any{
			"SessionStart": []any{
				map[string]any{
					"matcher": "startup|resume|clear|compact",
					"hooks": []any{
						map[string]any{"type": "command", "command": "/different/path hook session-start my-name"},
					},
				},
			},
		},
	}

	removed := removeSessionStartHook(settings, "/not/matching/path", "my-name")
	if !removed {
		t.Error("expected hook to be removed by MCP name match")
	}
}

func TestRemoveSessionStartHook_PreservesOthers(t *testing.T) {
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

	removed := removeSessionStartHook(settings, "/usr/local/bin/lumen", "lumen")
	if !removed {
		t.Error("expected lumen hook to be removed")
	}

	hooks := settings["hooks"].(map[string]any)
	sessionStart := hooks["SessionStart"].([]any)
	if len(sessionStart) != 1 {
		t.Errorf("expected 1 remaining hook, got %d", len(sessionStart))
	}
}

func TestRemoveSessionStartHook_NoMatch(t *testing.T) {
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

	removed := removeSessionStartHook(settings, "/usr/local/bin/lumen", "lumen")
	if removed {
		t.Error("expected no removal when nothing matches")
	}
}

func TestRemoveSessionStartHook_EmptySettings(t *testing.T) {
	settings := map[string]any{}
	removed := removeSessionStartHook(settings, "/usr/local/bin/lumen", "lumen")
	if removed {
		t.Error("expected no removal from empty settings")
	}
}
