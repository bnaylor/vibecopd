package hooks

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallClaudeHooks(t *testing.T) {
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)

	if err := InstallHooks(HarnessClaude, ""); err != nil {
		t.Fatal(err)
	}

	// Verify the settings file was created.
	path := filepath.Join(tmpHome, ".claude", "settings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var cfg claudeSettings
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}

	if cfg.Hooks == nil || len(cfg.Hooks.PreToolUse) == 0 {
		t.Fatal("expected PreToolUse entries")
	}

	entry := cfg.Hooks.PreToolUse[0]
	if entry.Matcher != "" {
		t.Errorf("expected empty matcher, got %q", entry.Matcher)
	}
	if len(entry.Hooks) == 0 || entry.Hooks[0].Command != "vibecop hook" {
		t.Errorf("expected vibecop hook command, got %+v", entry.Hooks)
	}
}

func TestInstallClaudeHooksIdempotent(t *testing.T) {
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)

	if err := InstallHooks(HarnessClaude, ""); err != nil {
		t.Fatal(err)
	}
	if err := InstallHooks(HarnessClaude, ""); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(tmpHome, ".claude", "settings.json")
	data, _ := os.ReadFile(path)

	var cfg claudeSettings
	json.Unmarshal(data, &cfg)

	if len(cfg.Hooks.PreToolUse) != 1 {
		t.Errorf("expected 1 entry after two installs, got %d", len(cfg.Hooks.PreToolUse))
	}
}

func TestInstallGeminiHooks(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tmpHome := os.Getenv("HOME")

	if err := InstallHooks(HarnessGemini, ""); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(tmpHome, ".gemini", "settings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var cfg geminiSettings
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}

	if cfg.Hooks == nil || len(cfg.Hooks.BeforeTool) != 1 {
		t.Fatalf("expected one BeforeTool entry, got %#v", cfg.Hooks)
	}
	got := cfg.Hooks.BeforeTool[0]
	if len(got.Hooks) != 1 || got.Hooks[0].Type != "command" || got.Hooks[0].Command != "vibecop hook" {
		t.Errorf("unexpected BeforeTool entry: %#v", got)
	}

	// Confirm raw JSON uses PascalCase wire key, not legacy snake_case.
	if !bytes.Contains(data, []byte("\"BeforeTool\"")) {
		t.Errorf("expected PascalCase BeforeTool key in raw JSON: %s", string(data))
	}
	if bytes.Contains(data, []byte("\"before_tool\"")) {
		t.Errorf("legacy before_tool key leaked into raw JSON: %s", string(data))
	}
}

func TestInstallGeminiHooksMigratesLegacyBeforeToolKey(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tmpHome := os.Getenv("HOME")

	geminiDir := filepath.Join(tmpHome, ".gemini")
	os.MkdirAll(geminiDir, 0755)
	// Write the legacy snake_case form a previous vibecop install would have left.
	os.WriteFile(filepath.Join(geminiDir, "settings.json"),
		[]byte(`{"hooks":{"before_tool":"vibecop hook","other":"keep"}}`), 0644)

	if err := InstallHooks(HarnessGemini, ""); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(geminiDir, "settings.json"))
	if bytes.Contains(data, []byte("before_tool")) {
		t.Errorf("legacy before_tool key not removed: %s", string(data))
	}
	if !bytes.Contains(data, []byte("BeforeTool")) {
		t.Errorf("PascalCase BeforeTool not written: %s", string(data))
	}
}

// Regression test for F-C1 review finding: a user who manually configured a
// non-vibecop hook under the legacy `before_tool` key (e.g. ported a
// third-party hook using the old shape) must NOT have it silently nuked
// when vibecop installs.
func TestInstallGeminiHooksPreservesNonVibecopLegacyBeforeTool(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tmpHome := os.Getenv("HOME")

	geminiDir := filepath.Join(tmpHome, ".gemini")
	os.MkdirAll(geminiDir, 0755)
	os.WriteFile(filepath.Join(geminiDir, "settings.json"),
		[]byte(`{"hooks":{"before_tool":"some-third-party-tool"}}`), 0644)

	if err := InstallHooks(HarnessGemini, ""); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(geminiDir, "settings.json"))
	if !bytes.Contains(data, []byte("some-third-party-tool")) {
		t.Errorf("user's non-vibecop before_tool value lost: %s", string(data))
	}
	if !bytes.Contains(data, []byte("BeforeTool")) {
		t.Errorf("PascalCase BeforeTool not written alongside legacy: %s", string(data))
	}
}

// Regression test for E-H3: writeRawJSON must be atomic — temp file +
// rename. A crash mid-write must not corrupt the user's settings.json.
// We can't induce a real crash, but we can assert that the tmp file is
// gone after a successful write (no leftover) and that the resulting
// file is valid JSON.
func TestWriteRawJSONIsAtomicAndLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	original := map[string]any{"keep": "me"}
	if err := writeRawJSON(path, original); err != nil {
		t.Fatal(err)
	}
	tmp := path + ".vibecop.tmp"
	if _, err := os.Stat(tmp); err == nil {
		t.Errorf("temp file %s still exists after successful write", tmp)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("written file is not valid JSON: %v\n%s", err, string(data))
	}
	if got["keep"] != "me" {
		t.Errorf("content lost: %v", got)
	}
}

func TestInstallUnsupportedHarness(t *testing.T) {
	err := InstallHooks("deepseek", "")
	if err == nil {
		t.Fatal("expected error for unsupported harness")
	}
}

func TestUninstallClaudeHooks(t *testing.T) {
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)

	// Install first.
	InstallHooks(HarnessClaude, "")

	// Then uninstall.
	if err := UninstallHooks(HarnessClaude); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(tmpHome, ".claude", "settings.json")

	// Read back — should have empty hooks or no hooks key.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	json.Unmarshal(data, &raw)
	if _, ok := raw["hooks"]; ok {
		var cfg claudeSettings
		json.Unmarshal(data, &cfg)
		if cfg.Hooks != nil && len(cfg.Hooks.PreToolUse) > 0 {
			t.Error("expected no PreToolUse entries after uninstall")
		}
	}
}

func TestUninstallGeminiHooks(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tmpHome := os.Getenv("HOME")

	InstallHooks(HarnessGemini, "")
	if err := UninstallHooks(HarnessGemini); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(tmpHome, ".gemini", "settings.json")
	data, _ := os.ReadFile(path)

	var raw map[string]any
	json.Unmarshal(data, &raw)
	if _, ok := raw["hooks"]; ok {
		var cfg geminiSettings
		json.Unmarshal(data, &cfg)
		if cfg.Hooks != nil && len(cfg.Hooks.BeforeTool) > 0 {
			t.Error("expected empty BeforeTool after uninstall")
		}
	}
}

func TestUninstallWhenNotInstalled(t *testing.T) {
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)

	// Uninstall without installing first — should be a no-op.
	if err := UninstallHooks(HarnessClaude); err != nil {
		t.Fatal(err)
	}
}

func TestInstallPreservesExistingSettings(t *testing.T) {
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)

	// Create Claude settings with pre-existing content.
	claudeDir := filepath.Join(tmpHome, ".claude")
	os.MkdirAll(claudeDir, 0755)

	existing := map[string]any{
		"theme": "dark",
		"hooks": map[string]any{
			"PreToolUse": []map[string]any{
				{
					"matcher": "Read",
					"hooks": []map[string]any{
						{"type": "command", "command": "some-other-tool"},
					},
				},
			},
		},
	}
	data, _ := json.MarshalIndent(existing, "", "  ")
	os.WriteFile(filepath.Join(claudeDir, "settings.json"), data, 0644)

	// Install vibecop hooks.
	if err := InstallHooks(HarnessClaude, ""); err != nil {
		t.Fatal(err)
	}

	// Verify both hooks exist.
	path := filepath.Join(claudeDir, "settings.json")
	result, _ := os.ReadFile(path)

	var cfg claudeSettings
	json.Unmarshal(result, &cfg)

	if len(cfg.Hooks.PreToolUse) != 2 {
		t.Errorf("expected 2 PreToolUse entries, got %d", len(cfg.Hooks.PreToolUse))
	}
}

func TestInstallClaudePreservesExtraKeys(t *testing.T) {
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)

	claudeDir := filepath.Join(tmpHome, ".claude")
	os.MkdirAll(claudeDir, 0755)

	// Write a settings file with keys our struct doesn't know about.
	existing := `{"theme":"dark","model":"claude-opus-4-5","preferredLanguage":"en"}`
	os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(existing), 0644)

	if err := InstallHooks(HarnessClaude, ""); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["theme"] != "dark" {
		t.Errorf("theme was lost: got %v", raw["theme"])
	}
	if raw["model"] != "claude-opus-4-5" {
		t.Errorf("model was lost: got %v", raw["model"])
	}
	if raw["preferredLanguage"] != "en" {
		t.Errorf("preferredLanguage was lost: got %v", raw["preferredLanguage"])
	}
}

func TestUninstallClaudePreservesExtraKeys(t *testing.T) {
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)

	claudeDir := filepath.Join(tmpHome, ".claude")
	os.MkdirAll(claudeDir, 0755)

	// Install first.
	os.WriteFile(filepath.Join(claudeDir, "settings.json"),
		[]byte(`{"theme":"dark"}`), 0644)
	InstallHooks(HarnessClaude, "")

	// Now uninstall.
	if err := UninstallHooks(HarnessClaude); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	var raw map[string]any
	json.Unmarshal(data, &raw)
	if raw["theme"] != "dark" {
		t.Errorf("theme was lost after uninstall: got %v", raw["theme"])
	}
}

func TestInstallGeminiPreservesExtraKeys(t *testing.T) {
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)

	geminiDir := filepath.Join(tmpHome, ".gemini")
	os.MkdirAll(geminiDir, 0755)

	existing := `{"theme":"dark","timeout":30}`
	os.WriteFile(filepath.Join(geminiDir, "settings.json"), []byte(existing), 0644)

	if err := InstallHooks(HarnessGemini, ""); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(geminiDir, "settings.json"))
	var raw map[string]any
	json.Unmarshal(data, &raw)
	if raw["theme"] != "dark" {
		t.Errorf("theme was lost: got %v", raw["theme"])
	}
	if raw["timeout"] != float64(30) {
		t.Errorf("timeout was lost: got %v", raw["timeout"])
	}
}

func TestIsVibecopHookCommand(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{"vibecop hook", true},
		{"/usr/local/bin/vibecop hook", true},
		{"/Users/me/Projects/vibecop/vibecop hook", true},
		{"./vibecop hook", true},
		{"vibecop", false},
		{"vibecop hook --debug", false},
		{"some-other-tool", false},
		{"/path/to/something hook", false},
		{"vibecopper hook", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isVibecopHookCommand(tc.cmd); got != tc.want {
			t.Errorf("isVibecopHookCommand(%q) = %v, want %v", tc.cmd, got, tc.want)
		}
	}
}

func TestInstallClaudeHooksCustomPath(t *testing.T) {
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)

	custom := "/opt/local/bin/vibecop"
	if err := InstallHooks(HarnessClaude, custom); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(tmpHome, ".claude", "settings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg claudeSettings
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Hooks == nil || len(cfg.Hooks.PreToolUse) != 1 {
		t.Fatalf("expected 1 PreToolUse entry, got %#v", cfg.Hooks)
	}
	got := cfg.Hooks.PreToolUse[0].Hooks[0].Command
	want := "/opt/local/bin/vibecop hook"
	if got != want {
		t.Errorf("command = %q, want %q", got, want)
	}
}

func TestInstallClaudeHooksReplacesOnPathChange(t *testing.T) {
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)

	// First install: default path.
	if err := InstallHooks(HarnessClaude, ""); err != nil {
		t.Fatal(err)
	}
	// Second install: custom path. Should replace, not append.
	custom := "/Users/me/Projects/vibecop/vibecop"
	if err := InstallHooks(HarnessClaude, custom); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(tmpHome, ".claude", "settings.json"))
	var cfg claudeSettings
	json.Unmarshal(data, &cfg)
	if len(cfg.Hooks.PreToolUse) != 1 {
		t.Fatalf("expected 1 entry after path change, got %d", len(cfg.Hooks.PreToolUse))
	}
	got := cfg.Hooks.PreToolUse[0].Hooks[0].Command
	want := custom + " hook"
	if got != want {
		t.Errorf("command = %q, want %q", got, want)
	}
}

func TestInstallClaudeHooksIdempotentCustomPath(t *testing.T) {
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)

	custom := "/opt/vibecop"
	if err := InstallHooks(HarnessClaude, custom); err != nil {
		t.Fatal(err)
	}
	if err := InstallHooks(HarnessClaude, custom); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(tmpHome, ".claude", "settings.json"))
	var cfg claudeSettings
	json.Unmarshal(data, &cfg)
	if len(cfg.Hooks.PreToolUse) != 1 {
		t.Errorf("expected 1 entry after two installs of same path, got %d", len(cfg.Hooks.PreToolUse))
	}
}

func TestUninstallClaudeHooksRemovesCustomPath(t *testing.T) {
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)

	custom := "/Users/me/build/vibecop"
	if err := InstallHooks(HarnessClaude, custom); err != nil {
		t.Fatal(err)
	}
	// Uninstall with the default arg shouldn't matter — uninstall should
	// remove either form.
	if err := UninstallHooks(HarnessClaude); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(tmpHome, ".claude", "settings.json"))
	var cfg claudeSettings
	json.Unmarshal(data, &cfg)
	if cfg.Hooks != nil && len(cfg.Hooks.PreToolUse) > 0 {
		t.Errorf("expected no entries after uninstall, got %d", len(cfg.Hooks.PreToolUse))
	}
}

func TestInstallGeminiHooksCustomPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tmpHome := os.Getenv("HOME")

	custom := "/opt/vibecop"
	if err := InstallHooks(HarnessGemini, custom); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(tmpHome, ".gemini", "settings.json"))
	var cfg geminiSettings
	json.Unmarshal(data, &cfg)
	want := custom + " hook"
	if cfg.Hooks == nil || len(cfg.Hooks.BeforeTool) != 1 || cfg.Hooks.BeforeTool[0].Hooks[0].Command != want {
		t.Errorf("expected single BeforeTool entry with command %q, got %#v", want, cfg.Hooks)
	}
}

func TestInstallGeminiHooksReplacesOnPathChange(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tmpHome := os.Getenv("HOME")

	if err := InstallHooks(HarnessGemini, ""); err != nil {
		t.Fatal(err)
	}
	custom := "/opt/vibecop"
	if err := InstallHooks(HarnessGemini, custom); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(tmpHome, ".gemini", "settings.json"))
	var cfg geminiSettings
	json.Unmarshal(data, &cfg)
	want := custom + " hook"
	if len(cfg.Hooks.BeforeTool) != 1 || cfg.Hooks.BeforeTool[0].Hooks[0].Command != want {
		t.Errorf("expected single BeforeTool entry with command %q after replace, got %#v", want, cfg.Hooks)
	}
}

func TestUninstallGeminiHooksRemovesCustomPath(t *testing.T) {
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)

	if err := InstallHooks(HarnessGemini, "/opt/vibecop"); err != nil {
		t.Fatal(err)
	}
	if err := UninstallHooks(HarnessGemini); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(tmpHome, ".gemini", "settings.json"))
	var raw map[string]any
	json.Unmarshal(data, &raw)
	if _, ok := raw["hooks"]; ok {
		var cfg geminiSettings
		json.Unmarshal(data, &cfg)
		if cfg.Hooks != nil && len(cfg.Hooks.BeforeTool) > 0 {
			t.Errorf("expected empty BeforeTool after uninstall, got %#v", cfg.Hooks)
		}
	}
}

// --- Codex install/uninstall ---

func TestInstallCodexHooksRegistersBothEvents(t *testing.T) {
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)

	if err := InstallHooks(HarnessCodex, ""); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(tmpHome, ".codex", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}

	var cfg codexSettings
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}

	if cfg.Hooks == nil {
		t.Fatal("expected hooks block")
	}
	if len(cfg.Hooks.PreToolUse) != 1 {
		t.Errorf("PreToolUse: expected 1 entry, got %d", len(cfg.Hooks.PreToolUse))
	}
	if len(cfg.Hooks.PermissionRequest) != 1 {
		t.Errorf("PermissionRequest: expected 1 entry, got %d", len(cfg.Hooks.PermissionRequest))
	}
	for _, ev := range []string{"PreToolUse", "PermissionRequest"} {
		var entries []codexEntry
		switch ev {
		case "PreToolUse":
			entries = cfg.Hooks.PreToolUse
		case "PermissionRequest":
			entries = cfg.Hooks.PermissionRequest
		}
		if len(entries) == 0 || len(entries[0].Hooks) == 0 {
			t.Fatalf("%s: missing hook entry", ev)
		}
		if entries[0].Hooks[0].Command != "vibecop hook" {
			t.Errorf("%s: command = %q", ev, entries[0].Hooks[0].Command)
		}
	}
}

func TestInstallCodexHooksIdempotent(t *testing.T) {
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)

	if err := InstallHooks(HarnessCodex, ""); err != nil {
		t.Fatal(err)
	}
	if err := InstallHooks(HarnessCodex, ""); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(tmpHome, ".codex", "hooks.json"))
	var cfg codexSettings
	json.Unmarshal(data, &cfg)
	if len(cfg.Hooks.PreToolUse) != 1 || len(cfg.Hooks.PermissionRequest) != 1 {
		t.Errorf("expected one entry per event after two installs; got pre=%d perm=%d",
			len(cfg.Hooks.PreToolUse), len(cfg.Hooks.PermissionRequest))
	}
}

func TestInstallCodexHooksReplacesOnPathChange(t *testing.T) {
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)

	if err := InstallHooks(HarnessCodex, ""); err != nil {
		t.Fatal(err)
	}
	custom := "/Users/me/build/vibecop"
	if err := InstallHooks(HarnessCodex, custom); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(tmpHome, ".codex", "hooks.json"))
	var cfg codexSettings
	json.Unmarshal(data, &cfg)
	want := custom + " hook"
	for _, ev := range [][]codexEntry{cfg.Hooks.PreToolUse, cfg.Hooks.PermissionRequest} {
		if len(ev) != 1 {
			t.Errorf("expected single entry after replace, got %d", len(ev))
			continue
		}
		if ev[0].Hooks[0].Command != want {
			t.Errorf("command = %q, want %q", ev[0].Hooks[0].Command, want)
		}
	}
}

func TestUninstallCodexHooks(t *testing.T) {
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)

	if err := InstallHooks(HarnessCodex, ""); err != nil {
		t.Fatal(err)
	}
	if err := UninstallHooks(HarnessCodex); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(tmpHome, ".codex", "hooks.json"))
	var raw map[string]any
	json.Unmarshal(data, &raw)
	if _, ok := raw["hooks"]; ok {
		var cfg codexSettings
		json.Unmarshal(data, &cfg)
		if cfg.Hooks != nil && (len(cfg.Hooks.PreToolUse) > 0 || len(cfg.Hooks.PermissionRequest) > 0) {
			t.Errorf("entries remained after uninstall: pre=%d perm=%d",
				len(cfg.Hooks.PreToolUse), len(cfg.Hooks.PermissionRequest))
		}
	}
}

func TestInstallCodexPreservesExtraKeys(t *testing.T) {
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)

	codexDir := filepath.Join(tmpHome, ".codex")
	os.MkdirAll(codexDir, 0755)
	existing := `{"trustedProjects":["~/work"],"someOtherKey":42}`
	os.WriteFile(filepath.Join(codexDir, "hooks.json"), []byte(existing), 0644)

	if err := InstallHooks(HarnessCodex, ""); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(codexDir, "hooks.json"))
	var raw map[string]any
	json.Unmarshal(data, &raw)
	if _, ok := raw["trustedProjects"]; !ok {
		t.Errorf("trustedProjects was lost: got %v", raw)
	}
	if raw["someOtherKey"] != float64(42) {
		t.Errorf("someOtherKey was lost: got %v", raw["someOtherKey"])
	}
}

func TestInstallCodexCoexistsWithExistingHooks(t *testing.T) {
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)

	codexDir := filepath.Join(tmpHome, ".codex")
	os.MkdirAll(codexDir, 0755)

	existing := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []map[string]any{
				{
					"matcher": "Bash",
					"hooks": []map[string]any{
						{"type": "command", "command": "/usr/bin/policy-check"},
					},
				},
			},
		},
	}
	data, _ := json.MarshalIndent(existing, "", "  ")
	os.WriteFile(filepath.Join(codexDir, "hooks.json"), data, 0644)

	if err := InstallHooks(HarnessCodex, ""); err != nil {
		t.Fatal(err)
	}

	out, _ := os.ReadFile(filepath.Join(codexDir, "hooks.json"))
	var cfg codexSettings
	json.Unmarshal(out, &cfg)
	if len(cfg.Hooks.PreToolUse) != 2 {
		t.Errorf("expected 2 PreToolUse entries (existing + vibecop), got %d", len(cfg.Hooks.PreToolUse))
	}
	if len(cfg.Hooks.PermissionRequest) != 1 {
		t.Errorf("expected 1 PermissionRequest entry, got %d", len(cfg.Hooks.PermissionRequest))
	}
}

// --- Copilot install/uninstall ---

func TestInstallCopilotHooks(t *testing.T) {
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)

	if err := InstallHooks(HarnessCopilot, ""); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(tmpHome, ".copilot", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}

	var cfg copilotSettings
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}

	if cfg.Version != 1 {
		t.Errorf("expected version=1, got %d", cfg.Version)
	}
	if cfg.Hooks == nil || len(cfg.Hooks.PreToolUse) != 1 {
		t.Fatalf("expected one preToolUse hook, got %#v", cfg.Hooks)
	}
	got := cfg.Hooks.PreToolUse[0]
	if got.Type != "command" || got.Bash != "vibecop hook" {
		t.Errorf("unexpected hook entry: %#v", got)
	}
}

func TestInstallCopilotHooksIdempotent(t *testing.T) {
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)

	if err := InstallHooks(HarnessCopilot, ""); err != nil {
		t.Fatal(err)
	}
	if err := InstallHooks(HarnessCopilot, ""); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(tmpHome, ".copilot", "settings.json"))
	var cfg copilotSettings
	json.Unmarshal(data, &cfg)
	if len(cfg.Hooks.PreToolUse) != 1 {
		t.Errorf("expected 1 entry after two installs, got %d", len(cfg.Hooks.PreToolUse))
	}
}

func TestInstallCopilotHooksReplacesOnPathChange(t *testing.T) {
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)

	if err := InstallHooks(HarnessCopilot, ""); err != nil {
		t.Fatal(err)
	}
	custom := "/opt/vibecop"
	if err := InstallHooks(HarnessCopilot, custom); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(tmpHome, ".copilot", "settings.json"))
	var cfg copilotSettings
	json.Unmarshal(data, &cfg)
	want := custom + " hook"
	if len(cfg.Hooks.PreToolUse) != 1 || cfg.Hooks.PreToolUse[0].Bash != want {
		t.Errorf("expected single entry with bash=%q, got %#v", want, cfg.Hooks.PreToolUse)
	}
}

func TestUninstallCopilotHooks(t *testing.T) {
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)

	if err := InstallHooks(HarnessCopilot, ""); err != nil {
		t.Fatal(err)
	}
	if err := UninstallHooks(HarnessCopilot); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(tmpHome, ".copilot", "settings.json"))
	var raw map[string]any
	json.Unmarshal(data, &raw)
	if _, ok := raw["hooks"]; ok {
		var cfg copilotSettings
		json.Unmarshal(data, &cfg)
		if cfg.Hooks != nil && len(cfg.Hooks.PreToolUse) > 0 {
			t.Errorf("expected no entries after uninstall, got %d", len(cfg.Hooks.PreToolUse))
		}
	}
}

func TestInstallCopilotPreservesExtraKeys(t *testing.T) {
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)

	copilotDir := filepath.Join(tmpHome, ".copilot")
	os.MkdirAll(copilotDir, 0755)

	existing := map[string]any{
		"version":         1,
		"someUserSetting": "yes",
		"hooks": map[string]any{
			"preToolUse": []map[string]any{
				{"type": "command", "bash": "/usr/local/bin/audit"},
			},
		},
	}
	data, _ := json.MarshalIndent(existing, "", "  ")
	os.WriteFile(filepath.Join(copilotDir, "settings.json"), data, 0644)

	if err := InstallHooks(HarnessCopilot, ""); err != nil {
		t.Fatal(err)
	}

	out, _ := os.ReadFile(filepath.Join(copilotDir, "settings.json"))
	var raw map[string]any
	json.Unmarshal(out, &raw)
	if raw["someUserSetting"] != "yes" {
		t.Errorf("someUserSetting lost: %v", raw["someUserSetting"])
	}

	var cfg copilotSettings
	json.Unmarshal(out, &cfg)
	if len(cfg.Hooks.PreToolUse) != 2 {
		t.Errorf("expected 2 hooks (existing + vibecop), got %d: %#v", len(cfg.Hooks.PreToolUse), cfg.Hooks.PreToolUse)
	}
}

// --- Hooks sub-key preservation (regression for key-loss bug) ---
//
// All harnesses must preserve hook types they don't manage (e.g. PostToolUse
// for Claude, postToolUse for Copilot) when installing or uninstalling.

func TestInstallClaudePreservesOtherHookTypes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	claudeDir := filepath.Join(os.Getenv("HOME"), ".claude")
	os.MkdirAll(claudeDir, 0755)

	// Pre-existing settings with a PostToolUse entry that vibecop must not touch.
	existing := `{
		"theme": "dark",
		"hooks": {
			"PostToolUse": [{"matcher":"","hooks":[{"type":"command","command":"audit.sh"}]}]
		}
	}`
	os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(existing), 0644)

	if err := InstallHooks(HarnessClaude, ""); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	var raw map[string]any
	json.Unmarshal(data, &raw)

	hooksAny, ok := raw["hooks"].(map[string]any)
	if !ok {
		t.Fatal("hooks key missing or wrong type after install")
	}
	if _, ok := hooksAny["PostToolUse"]; !ok {
		t.Error("PostToolUse hook type was dropped by install")
	}
	if _, ok := hooksAny["PreToolUse"]; !ok {
		t.Error("PreToolUse (vibecop) not written")
	}
}

func TestUninstallClaudePreservesOtherHookTypes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	claudeDir := filepath.Join(os.Getenv("HOME"), ".claude")
	os.MkdirAll(claudeDir, 0755)

	// Install, then add a PostToolUse entry manually.
	os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(`{
		"hooks": {
			"PreToolUse": [{"matcher":"","hooks":[{"type":"command","command":"vibecop hook"}]}],
			"PostToolUse": [{"matcher":"","hooks":[{"type":"command","command":"audit.sh"}]}]
		}
	}`), 0644)

	if err := UninstallHooks(HarnessClaude); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	var raw map[string]any
	json.Unmarshal(data, &raw)

	// hooks key must survive because PostToolUse is still in it.
	hooksAny, ok := raw["hooks"].(map[string]any)
	if !ok {
		t.Fatal("hooks key was deleted, but PostToolUse should have kept it alive")
	}
	if _, ok := hooksAny["PostToolUse"]; !ok {
		t.Error("PostToolUse was removed during vibecop uninstall")
	}
}

func TestInstallGeminiPreservesOtherHookTypes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	geminiDir := filepath.Join(os.Getenv("HOME"), ".gemini")
	os.MkdirAll(geminiDir, 0755)

	// Hypothetical future AfterTool hook that vibecop should leave alone.
	existing := `{
		"hooks": {
			"AfterTool": [{"matcher":"","hooks":[{"type":"command","command":"post.sh"}]}]
		}
	}`
	os.WriteFile(filepath.Join(geminiDir, "settings.json"), []byte(existing), 0644)

	if err := InstallHooks(HarnessGemini, ""); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(geminiDir, "settings.json"))
	var raw map[string]any
	json.Unmarshal(data, &raw)

	hooksAny, ok := raw["hooks"].(map[string]any)
	if !ok {
		t.Fatal("hooks key missing after gemini install")
	}
	if _, ok := hooksAny["AfterTool"]; !ok {
		t.Error("AfterTool hook type was dropped by gemini install")
	}
}

func TestUninstallGeminiPreservesOtherHookTypes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	geminiDir := filepath.Join(os.Getenv("HOME"), ".gemini")
	os.MkdirAll(geminiDir, 0755)

	os.WriteFile(filepath.Join(geminiDir, "settings.json"), []byte(`{
		"hooks": {
			"BeforeTool": [{"hooks":[{"type":"command","command":"vibecop hook"}]}],
			"AfterTool":  [{"hooks":[{"type":"command","command":"post.sh"}]}]
		}
	}`), 0644)

	if err := UninstallHooks(HarnessGemini); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(geminiDir, "settings.json"))
	var raw map[string]any
	json.Unmarshal(data, &raw)

	hooksAny, ok := raw["hooks"].(map[string]any)
	if !ok {
		t.Fatal("hooks key deleted but AfterTool should keep it alive")
	}
	if _, ok := hooksAny["AfterTool"]; !ok {
		t.Error("AfterTool was removed during gemini uninstall")
	}
}

// Regression test for D-Critical: previously the install path round-tripped
// each hook entry through a typed struct that knew only about `type` + `bash`,
// silently dropping `powershell`, `cwd`, `timeoutSec`, and any other fields
// the user authored on a hook entry. This locks in the raw-walk preservation.
func TestInstallCopilotPreservesUnknownFieldsOnHookEntries(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	copilotDir := filepath.Join(os.Getenv("HOME"), ".copilot")
	os.MkdirAll(copilotDir, 0755)

	// User has a non-vibecop preToolUse hook with cross-platform siblings
	// (powershell, cwd) and a custom timeoutSec. None of these belong in
	// vibecop's struct definition; all must survive install.
	existing := `{
		"version": 1,
		"hooks": {
			"preToolUse": [
				{
					"type": "command",
					"bash": "/usr/local/bin/audit.sh",
					"powershell": "C:\\scripts\\audit.ps1",
					"cwd": "/home/me/scripts",
					"timeoutSec": 45
				}
			]
		}
	}`
	os.WriteFile(filepath.Join(copilotDir, "settings.json"), []byte(existing), 0644)

	if err := InstallHooks(HarnessCopilot, ""); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(copilotDir, "settings.json"))
	var raw map[string]any
	json.Unmarshal(data, &raw)

	hooks := raw["hooks"].(map[string]any)
	preEntries, _ := hooks["preToolUse"].([]any)
	if len(preEntries) != 2 {
		t.Fatalf("expected 2 preToolUse entries (existing + vibecop), got %d", len(preEntries))
	}

	// Find the user's entry by powershell field.
	var user map[string]any
	for _, e := range preEntries {
		m := e.(map[string]any)
		if _, has := m["powershell"]; has {
			user = m
			break
		}
	}
	if user == nil {
		t.Fatalf("user entry missing entirely: %s", string(data))
	}
	if user["powershell"] != "C:\\scripts\\audit.ps1" {
		t.Errorf("powershell sibling lost or mutated: %v", user["powershell"])
	}
	if user["cwd"] != "/home/me/scripts" {
		t.Errorf("cwd sibling lost: %v", user["cwd"])
	}
	if user["timeoutSec"] != float64(45) {
		t.Errorf("timeoutSec sibling lost: %v", user["timeoutSec"])
	}
	if user["bash"] != "/usr/local/bin/audit.sh" {
		t.Errorf("user's bash command was overwritten: %v", user["bash"])
	}
}

// Regression test for D-Critical (matcher-group shape): timeout, name, and
// statusMessage on inner hooks are not in vibecop's struct definitions but
// must survive install on Claude/Codex/Gemini.
func TestInstallClaudePreservesUnknownFieldsOnInnerHook(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	claudeDir := filepath.Join(os.Getenv("HOME"), ".claude")
	os.MkdirAll(claudeDir, 0755)

	existing := `{
		"hooks": {
			"PreToolUse": [
				{
					"matcher": "Bash",
					"hooks": [
						{
							"type": "command",
							"command": "/usr/local/bin/policy-check",
							"timeout": 60,
							"name": "policy"
						}
					],
					"customMatcherFlag": true
				}
			]
		}
	}`
	os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(existing), 0644)

	if err := InstallHooks(HarnessClaude, ""); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	var raw map[string]any
	json.Unmarshal(data, &raw)

	hooks := raw["hooks"].(map[string]any)
	pre, _ := hooks["PreToolUse"].([]any)
	if len(pre) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(pre))
	}

	// User's entry: keyed by the customMatcherFlag we set.
	var user map[string]any
	for _, e := range pre {
		m := e.(map[string]any)
		if _, has := m["customMatcherFlag"]; has {
			user = m
			break
		}
	}
	if user == nil {
		t.Fatalf("user entry missing: %s", string(data))
	}
	if user["customMatcherFlag"] != true {
		t.Errorf("customMatcherFlag sibling lost on top-level entry: %v", user["customMatcherFlag"])
	}
	innerArr, _ := user["hooks"].([]any)
	if len(innerArr) != 1 {
		t.Fatalf("expected 1 inner hook, got %d", len(innerArr))
	}
	inner := innerArr[0].(map[string]any)
	if inner["timeout"] != float64(60) {
		t.Errorf("inner timeout lost: %v", inner["timeout"])
	}
	if inner["name"] != "policy" {
		t.Errorf("inner name lost: %v", inner["name"])
	}
}

func TestInstallCopilotPreservesOtherHookEventTypes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	copilotDir := filepath.Join(os.Getenv("HOME"), ".copilot")
	os.MkdirAll(copilotDir, 0755)

	// Copilot has many hook types. postToolUse and sessionStart should survive.
	existing := `{
		"version": 1,
		"hooks": {
			"postToolUse":  [{"type":"command","bash":"audit.sh"}],
			"sessionStart": [{"type":"command","bash":"start.sh"}]
		}
	}`
	os.WriteFile(filepath.Join(copilotDir, "settings.json"), []byte(existing), 0644)

	if err := InstallHooks(HarnessCopilot, ""); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(copilotDir, "settings.json"))
	var raw map[string]any
	json.Unmarshal(data, &raw)

	hooksAny, ok := raw["hooks"].(map[string]any)
	if !ok {
		t.Fatal("hooks key missing after copilot install")
	}
	if _, ok := hooksAny["postToolUse"]; !ok {
		t.Error("postToolUse dropped by copilot install")
	}
	if _, ok := hooksAny["sessionStart"]; !ok {
		t.Error("sessionStart dropped by copilot install")
	}
	if _, ok := hooksAny["preToolUse"]; !ok {
		t.Error("preToolUse (vibecop) not written")
	}
}

func TestUninstallCopilotPreservesOtherHookEventTypes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	copilotDir := filepath.Join(os.Getenv("HOME"), ".copilot")
	os.MkdirAll(copilotDir, 0755)

	os.WriteFile(filepath.Join(copilotDir, "settings.json"), []byte(`{
		"version": 1,
		"hooks": {
			"preToolUse":  [{"type":"command","bash":"vibecop hook"}],
			"postToolUse": [{"type":"command","bash":"audit.sh"}]
		}
	}`), 0644)

	if err := UninstallHooks(HarnessCopilot); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(copilotDir, "settings.json"))
	var raw map[string]any
	json.Unmarshal(data, &raw)

	hooksAny, ok := raw["hooks"].(map[string]any)
	if !ok {
		t.Fatal("hooks key deleted but postToolUse should keep it alive")
	}
	if _, ok := hooksAny["postToolUse"]; !ok {
		t.Error("postToolUse dropped by copilot uninstall")
	}
}
