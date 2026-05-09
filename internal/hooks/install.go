package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// hookCommand returns the shell command string to install. An empty
// vibecopPath uses the bare "vibecop" binary (resolved from $PATH);
// a non-empty path is used verbatim — callers should resolve it to
// absolute via filepath.Abs first if they want CWD-independent hooks.
func hookCommand(vibecopPath string) string {
	if vibecopPath == "" {
		return "vibecop hook"
	}
	return vibecopPath + " hook"
}

// isVibecopHookCommand reports whether cmd looks like a vibecop hook
// invocation — either the default "vibecop hook" or "<path>/vibecop hook".
// Used to detect previously-installed entries so install can replace them
// (rather than appending a duplicate) and uninstall can remove either form.
func isVibecopHookCommand(cmd string) bool {
	fields := strings.Fields(cmd)
	if len(fields) != 2 || fields[1] != "hook" {
		return false
	}
	bin := fields[0]
	return bin == "vibecop" || strings.HasSuffix(bin, "/vibecop")
}

// --- Claude Code settings.json types ---
//
// https://code.claude.com/docs/en/hooks

type claudeSettings struct {
	Hooks *claudeHooks `json:"hooks,omitempty"`
}

type claudeHooks struct {
	PreToolUse []claudePreToolEntry `json:"PreToolUse,omitempty"`
}

type claudePreToolEntry struct {
	Matcher string       `json:"matcher"`
	Hooks   []claudeHook `json:"hooks"`
}

type claudeHook struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// --- Gemini CLI settings.json types ---
//
// https://github.com/google-gemini/gemini-cli/blob/main/docs/hooks/reference.md
//
// Gemini's settings.json shape mirrors Claude's: each event (PascalCase)
// maps to an array of {matcher, hooks: [{type, command}]} entries.

type geminiSettings struct {
	Hooks *geminiHooks `json:"hooks,omitempty"`
}

type geminiHooks struct {
	BeforeTool []geminiEntry `json:"BeforeTool,omitempty"`
}

type geminiEntry struct {
	Matcher string       `json:"matcher,omitempty"`
	Hooks   []geminiHook `json:"hooks"`
}

type geminiHook struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// --- Codex CLI hooks.json types ---
//
// https://developers.openai.com/codex/hooks
//
// Codex's hooks.json mirrors Claude's settings.json shape under `hooks` —
// each event is an array of {matcher, hooks: [{type, command}]} entries.
// Codex registers under PreToolUse AND PermissionRequest because PreToolUse
// cannot allow.

type codexSettings struct {
	Hooks *codexHooks `json:"hooks,omitempty"`
}

type codexHooks struct {
	PreToolUse        []codexEntry `json:"PreToolUse,omitempty"`
	PermissionRequest []codexEntry `json:"PermissionRequest,omitempty"`
}

type codexEntry struct {
	Matcher string      `json:"matcher"`
	Hooks   []codexHook `json:"hooks"`
}

type codexHook struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// --- Copilot CLI settings.json types ---
//
// https://docs.github.com/en/copilot/reference/hooks-configuration
// https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-config-dir-reference
//
// Copilot's settings.json uses a flat array of hook definitions per event,
// no matcher field, and the command lives under the "bash" key (with an
// optional "powershell" sibling for cross-platform setups).

type copilotSettings struct {
	Version int           `json:"version,omitempty"`
	Hooks   *copilotHooks `json:"hooks,omitempty"`
}

type copilotHooks struct {
	PreToolUse []copilotHook `json:"preToolUse,omitempty"`
}

type copilotHook struct {
	Type string `json:"type"`
	Bash string `json:"bash,omitempty"`
}

// Default paths.
func claudeSettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

func geminiSettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".gemini", "settings.json"), nil
}

func codexSettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex", "hooks.json"), nil
}

func copilotSettingsPath() (string, error) {
	// COPILOT_HOME (when set) replaces the entire ~/.copilot path.
	// Per docs.github.com/en/copilot/reference/copilot-cli-reference/cli-config-dir-reference.
	if h := os.Getenv("COPILOT_HOME"); h != "" {
		return filepath.Join(h, "settings.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".copilot", "settings.json"), nil
}

// InstallHooks adds vibecop hooks to the specified harness's settings.
// vibecopPath is the path to the vibecop binary the hook should invoke;
// empty means rely on $PATH (writes "vibecop hook"). A previously installed
// vibecop entry is replaced if its path differs, keeping installs idempotent
// across path changes.
func InstallHooks(harness, vibecopPath string) error {
	switch harness {
	case HarnessClaude:
		return installClaudeHooks(vibecopPath)
	case HarnessGemini:
		return installGeminiHooks(vibecopPath)
	case HarnessCodex:
		return installCodexHooks(vibecopPath)
	case HarnessCopilot:
		return installCopilotHooks(vibecopPath)
	default:
		return fmt.Errorf("unsupported harness: %s", harness)
	}
}

func installClaudeHooks(vibecopPath string) error {
	path, err := claudeSettingsPath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}

	raw := readRawJSON(path)
	hooksMap := rawHooksMap(raw)
	entries := unmarshalHookEntries[claudePreToolEntry](hooksMap["PreToolUse"])

	want := hookCommand(vibecopPath)

	// Walk existing entries: if there's already a vibecop hook, update its
	// command in place (idempotent on equal paths, replace on differing
	// ones) instead of appending a duplicate.
	for i, e := range entries {
		if len(e.Hooks) == 0 {
			continue
		}
		if !isVibecopHookCommand(e.Hooks[0].Command) {
			continue
		}
		if e.Hooks[0].Command == want {
			return nil
		}
		entries[i].Hooks[0].Command = want
		hooksMap["PreToolUse"] = entries
		raw["hooks"] = hooksMap
		return writeRawJSON(path, raw)
	}

	entries = append(entries, claudePreToolEntry{
		Matcher: "",
		Hooks:   []claudeHook{{Type: "command", Command: want}},
	})
	hooksMap["PreToolUse"] = entries
	raw["hooks"] = hooksMap
	return writeRawJSON(path, raw)
}

func installGeminiHooks(vibecopPath string) error {
	path, err := geminiSettingsPath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}

	raw := readRawJSON(path)
	hooksMap := rawHooksMap(raw)

	// Drop legacy snake_case before_tool key from older vibecop installs so
	// uninstalling it on upgrade is automatic.
	delete(hooksMap, "before_tool")

	entries := unmarshalHookEntries[geminiEntry](hooksMap["BeforeTool"])

	want := hookCommand(vibecopPath)
	for i, e := range entries {
		if len(e.Hooks) == 0 {
			continue
		}
		if !isVibecopHookCommand(e.Hooks[0].Command) {
			continue
		}
		if e.Hooks[0].Command == want {
			return nil
		}
		entries[i].Hooks[0].Command = want
		hooksMap["BeforeTool"] = entries
		raw["hooks"] = hooksMap
		return writeRawJSON(path, raw)
	}

	entries = append(entries, geminiEntry{
		Hooks: []geminiHook{{Type: "command", Command: want}},
	})
	hooksMap["BeforeTool"] = entries
	raw["hooks"] = hooksMap
	return writeRawJSON(path, raw)
}

// UninstallHooks removes vibecop hooks from the specified harness's settings.
func UninstallHooks(harness string) error {
	switch harness {
	case HarnessClaude:
		return uninstallClaudeHooks()
	case HarnessGemini:
		return uninstallGeminiHooks()
	case HarnessCodex:
		return uninstallCodexHooks()
	case HarnessCopilot:
		return uninstallCopilotHooks()
	default:
		return fmt.Errorf("unsupported harness: %s", harness)
	}
}

func uninstallClaudeHooks() error {
	path, err := claudeSettingsPath()
	if err != nil {
		return err
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}

	raw := readRawJSON(path)
	hooksMap := rawHooksMap(raw)
	entries := unmarshalHookEntries[claudePreToolEntry](hooksMap["PreToolUse"])

	filtered := slices.DeleteFunc(entries, func(e claudePreToolEntry) bool {
		return len(e.Hooks) > 0 && isVibecopHookCommand(e.Hooks[0].Command)
	})

	if len(filtered) == len(entries) {
		return nil
	}

	if len(filtered) == 0 {
		delete(hooksMap, "PreToolUse")
	} else {
		hooksMap["PreToolUse"] = filtered
	}

	if len(hooksMap) == 0 {
		delete(raw, "hooks")
	} else {
		raw["hooks"] = hooksMap
	}

	return writeRawJSON(path, raw)
}

func uninstallGeminiHooks() error {
	path, err := geminiSettingsPath()
	if err != nil {
		return err
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}

	raw := readRawJSON(path)
	hooksMap := rawHooksMap(raw)

	// Strip legacy snake_case before_tool key (pre-VCOP-13 install shape) so
	// the upgrade path leaves a clean settings file.
	delete(hooksMap, "before_tool")

	entries := unmarshalHookEntries[geminiEntry](hooksMap["BeforeTool"])
	filtered := slices.DeleteFunc(entries, func(e geminiEntry) bool {
		return len(e.Hooks) > 0 && isVibecopHookCommand(e.Hooks[0].Command)
	})

	if len(filtered) == 0 {
		delete(hooksMap, "BeforeTool")
	} else {
		hooksMap["BeforeTool"] = filtered
	}

	if len(hooksMap) == 0 {
		delete(raw, "hooks")
	} else {
		raw["hooks"] = hooksMap
	}
	return writeRawJSON(path, raw)
}

func installCodexHooks(vibecopPath string) error {
	path, err := codexSettingsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}

	raw := readRawJSON(path)
	hooksMap := rawHooksMap(raw)

	preEntries := unmarshalHookEntries[codexEntry](hooksMap["PreToolUse"])
	permEntries := unmarshalHookEntries[codexEntry](hooksMap["PermissionRequest"])

	want := hookCommand(vibecopPath)

	updatedPre, changedPre := upsertCodexEntry(preEntries, want)
	updatedPerm, changedPerm := upsertCodexEntry(permEntries, want)
	if !changedPre && !changedPerm {
		return nil
	}
	hooksMap["PreToolUse"] = updatedPre
	hooksMap["PermissionRequest"] = updatedPerm
	raw["hooks"] = hooksMap
	return writeRawJSON(path, raw)
}

// upsertCodexEntry replaces a vibecop hook in entries (matching by command
// shape) or appends a new one. Returns the updated slice and a `changed`
// flag so callers can skip the on-disk write when content already matches.
func upsertCodexEntry(entries []codexEntry, want string) ([]codexEntry, bool) {
	for i, e := range entries {
		if len(e.Hooks) == 0 {
			continue
		}
		if !isVibecopHookCommand(e.Hooks[0].Command) {
			continue
		}
		if e.Hooks[0].Command == want {
			return entries, false
		}
		entries[i].Hooks[0].Command = want
		return entries, true
	}
	return append(entries, codexEntry{
		Matcher: "",
		Hooks:   []codexHook{{Type: "command", Command: want}},
	}), true
}

func installCopilotHooks(vibecopPath string) error {
	path, err := copilotSettingsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}

	raw := readRawJSON(path)

	// Preserve the `version` key explicitly — write it back as 1 if absent
	// so Copilot can parse the file.
	if _, ok := raw["version"]; !ok {
		raw["version"] = 1
	}

	hooksMap := rawHooksMap(raw)
	entries := unmarshalHookEntries[copilotHook](hooksMap["preToolUse"])

	want := hookCommand(vibecopPath)

	updated, changed := upsertCopilotEntry(entries, want)
	if !changed {
		return nil
	}
	hooksMap["preToolUse"] = updated
	raw["hooks"] = hooksMap
	return writeRawJSON(path, raw)
}

func upsertCopilotEntry(entries []copilotHook, want string) ([]copilotHook, bool) {
	for i, e := range entries {
		if !isVibecopHookCommand(e.Bash) {
			continue
		}
		if e.Bash == want {
			return entries, false
		}
		entries[i].Bash = want
		return entries, true
	}
	return append(entries, copilotHook{Type: "command", Bash: want}), true
}

func uninstallCodexHooks() error {
	path, err := codexSettingsPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}

	raw := readRawJSON(path)
	hooksMap := rawHooksMap(raw)

	preEntries := unmarshalHookEntries[codexEntry](hooksMap["PreToolUse"])
	permEntries := unmarshalHookEntries[codexEntry](hooksMap["PermissionRequest"])

	filteredPre := removeVibecopCodexEntries(preEntries)
	filteredPerm := removeVibecopCodexEntries(permEntries)

	if len(filteredPre) == 0 {
		delete(hooksMap, "PreToolUse")
	} else {
		hooksMap["PreToolUse"] = filteredPre
	}
	if len(filteredPerm) == 0 {
		delete(hooksMap, "PermissionRequest")
	} else {
		hooksMap["PermissionRequest"] = filteredPerm
	}

	if len(hooksMap) == 0 {
		delete(raw, "hooks")
	} else {
		raw["hooks"] = hooksMap
	}
	return writeRawJSON(path, raw)
}

func removeVibecopCodexEntries(entries []codexEntry) []codexEntry {
	return slices.DeleteFunc(entries, func(e codexEntry) bool {
		return len(e.Hooks) > 0 && isVibecopHookCommand(e.Hooks[0].Command)
	})
}

func uninstallCopilotHooks() error {
	path, err := copilotSettingsPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}

	raw := readRawJSON(path)
	hooksMap := rawHooksMap(raw)
	entries := unmarshalHookEntries[copilotHook](hooksMap["preToolUse"])

	filtered := slices.DeleteFunc(entries, func(h copilotHook) bool {
		return isVibecopHookCommand(h.Bash)
	})

	if len(filtered) == 0 {
		delete(hooksMap, "preToolUse")
	} else {
		hooksMap["preToolUse"] = filtered
	}

	if len(hooksMap) == 0 {
		delete(raw, "hooks")
	} else {
		raw["hooks"] = hooksMap
	}
	return writeRawJSON(path, raw)
}

// readRawJSON reads a JSON file as a raw map, preserving all keys.
// Returns an empty map if the file doesn't exist or is malformed.
func readRawJSON(path string) map[string]any {
	raw := map[string]any{}
	data, err := os.ReadFile(path)
	if err != nil {
		return raw
	}
	json.Unmarshal(data, &raw) //nolint:errcheck
	return raw
}

// rawHooksMap returns the "hooks" sub-map from raw as a mutable
// map[string]any, creating a new empty map if the key is absent or holds a
// non-map value. Callers must write the returned map back via
// raw["hooks"] = m after modifying it, because a newly-created map is not
// automatically inserted.
func rawHooksMap(raw map[string]any) map[string]any {
	if m, ok := raw["hooks"].(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

// unmarshalHookEntries unmarshals a raw value (from a hooks map) into a
// typed slice. Returns a nil slice without error on missing/null input.
func unmarshalHookEntries[T any](v any) []T {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out []T
	json.Unmarshal(b, &out) //nolint:errcheck
	return out
}

// writeRawJSON writes v to path with standard indentation.
func writeRawJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
