package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

	raw, err := readRawJSON(path)
	if err != nil {
		return err
	}
	hooksMap := rawHooksMap(raw)
	entries := rawSlice(hooksMap["PreToolUse"])

	updated, changed := upsertNestedHook(entries, hookCommand(vibecopPath))
	if !changed {
		return nil
	}
	hooksMap["PreToolUse"] = updated
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

	raw, err := readRawJSON(path)
	if err != nil {
		return err
	}
	hooksMap := rawHooksMap(raw)

	// Strip legacy snake_case before_tool key only if it's vibecop's own
	// — never silently nuke a user-written value under that key.
	if v, ok := hooksMap["before_tool"].(string); ok && isVibecopHookCommand(v) {
		delete(hooksMap, "before_tool")
	}

	entries := rawSlice(hooksMap["BeforeTool"])

	updated, changed := upsertNestedHook(entries, hookCommand(vibecopPath))
	if !changed {
		return nil
	}
	hooksMap["BeforeTool"] = updated
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

	raw, err := readRawJSON(path)
	if err != nil {
		return err
	}
	hooksMap := rawHooksMap(raw)
	entries := rawSlice(hooksMap["PreToolUse"])
	filtered := removeNestedVibecopEntries(entries)

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

	raw, err := readRawJSON(path)
	if err != nil {
		return err
	}
	hooksMap := rawHooksMap(raw)

	// Strip legacy snake_case before_tool key only if it's vibecop's own —
	// the value-blind delete would nuke a non-vibecop hook a user manually
	// configured under the same legacy key.
	legacyRemoved := false
	if v, ok := hooksMap["before_tool"].(string); ok && isVibecopHookCommand(v) {
		delete(hooksMap, "before_tool")
		legacyRemoved = true
	}

	entries := rawSlice(hooksMap["BeforeTool"])
	filtered := removeNestedVibecopEntries(entries)

	if !legacyRemoved && len(filtered) == len(entries) {
		return nil
	}
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

	raw, err := readRawJSON(path)
	if err != nil {
		return err
	}
	hooksMap := rawHooksMap(raw)

	want := hookCommand(vibecopPath)

	updatedPre, changedPre := upsertNestedHook(rawSlice(hooksMap["PreToolUse"]), want)
	updatedPerm, changedPerm := upsertNestedHook(rawSlice(hooksMap["PermissionRequest"]), want)
	if !changedPre && !changedPerm {
		return nil
	}
	hooksMap["PreToolUse"] = updatedPre
	hooksMap["PermissionRequest"] = updatedPerm
	raw["hooks"] = hooksMap
	return writeRawJSON(path, raw)
}

func installCopilotHooks(vibecopPath string) error {
	path, err := copilotSettingsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}

	raw, err := readRawJSON(path)
	if err != nil {
		return err
	}

	// Preserve the `version` key explicitly — write it back as 1 if absent
	// so Copilot can parse the file.
	if _, ok := raw["version"]; !ok {
		raw["version"] = 1
	}

	hooksMap := rawHooksMap(raw)
	entries := rawSlice(hooksMap["preToolUse"])

	updated, changed := upsertCopilotHook(entries, hookCommand(vibecopPath))
	if !changed {
		return nil
	}
	hooksMap["preToolUse"] = updated
	raw["hooks"] = hooksMap
	return writeRawJSON(path, raw)
}

func uninstallCodexHooks() error {
	path, err := codexSettingsPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}

	raw, err := readRawJSON(path)
	if err != nil {
		return err
	}
	hooksMap := rawHooksMap(raw)

	preEntries := rawSlice(hooksMap["PreToolUse"])
	permEntries := rawSlice(hooksMap["PermissionRequest"])

	filteredPre := removeNestedVibecopEntries(preEntries)
	filteredPerm := removeNestedVibecopEntries(permEntries)

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

func uninstallCopilotHooks() error {
	path, err := copilotSettingsPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}

	raw, err := readRawJSON(path)
	if err != nil {
		return err
	}
	hooksMap := rawHooksMap(raw)
	entries := rawSlice(hooksMap["preToolUse"])
	filtered := removeCopilotVibecopEntries(entries)

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
// Returns an empty map if the file doesn't exist. Returns an error if
// the file exists but is not valid JSON — callers must abort rather than
// clobber a malformed user-authored config.
func readRawJSON(path string) (map[string]any, error) {
	raw := map[string]any{}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return raw, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) == 0 {
		return raw, nil
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return raw, nil
}

// rawHooksMap returns the "hooks" sub-map from raw as a mutable
// map[string]any, creating a new empty map if the key is absent or holds a
// non-map value. Callers must write the returned map back via
// raw["hooks"] = m after modifying it, because a newly-created map is not
// automatically inserted. Example:
//
//	m := rawHooksMap(raw)
//	m["PreToolUse"] = entries
//	raw["hooks"] = m
func rawHooksMap(raw map[string]any) map[string]any {
	if m, ok := raw["hooks"].(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

// rawSlice returns v coerced to []any, or nil if v is missing/null/wrong-typed.
// Used to walk the user's existing hook-entry array without losing unknown
// sibling fields on round-trip through typed structs.
func rawSlice(v any) []any {
	if v == nil {
		return nil
	}
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}

// upsertNestedHook handles the Claude/Codex/Gemini matcher-group shape
// where each entry is `{matcher, hooks: [{type, command, ...}]}`. It mutates
// matching entries in place — preserving every unknown sibling field on
// the entry, the inner hook, and any later inner hooks the user authored.
// Returns the (possibly extended) entries slice and a `changed` flag.
func upsertNestedHook(entries []any, want string) ([]any, bool) {
	for i, e := range entries {
		entry, ok := e.(map[string]any)
		if !ok {
			continue
		}
		inner := rawSlice(entry["hooks"])
		if len(inner) == 0 {
			continue
		}
		first, ok := inner[0].(map[string]any)
		if !ok {
			continue
		}
		cmd, _ := first["command"].(string)
		if !isVibecopHookCommand(cmd) {
			continue
		}
		if cmd == want {
			return entries, false
		}
		first["command"] = want
		inner[0] = first
		entry["hooks"] = inner
		entries[i] = entry
		return entries, true
	}
	return append(entries, map[string]any{
		"matcher": "",
		"hooks":   []any{map[string]any{"type": "command", "command": want}},
	}), true
}

// upsertCopilotHook handles Copilot's flat preToolUse shape where each
// entry is `{type, bash, powershell?, cwd?, timeoutSec?, ...}`. Mutating in
// place preserves the user's powershell sibling, cwd, timeout, etc.
func upsertCopilotHook(entries []any, want string) ([]any, bool) {
	for i, e := range entries {
		entry, ok := e.(map[string]any)
		if !ok {
			continue
		}
		bash, _ := entry["bash"].(string)
		if !isVibecopHookCommand(bash) {
			continue
		}
		if bash == want {
			return entries, false
		}
		entry["bash"] = want
		entries[i] = entry
		return entries, true
	}
	return append(entries, map[string]any{"type": "command", "bash": want}), true
}

// removeNestedVibecopEntries removes Claude/Codex/Gemini-style matcher-group
// entries whose first inner hook command is the vibecop hook. Other entries
// (and unknown sibling fields on them) are returned untouched.
func removeNestedVibecopEntries(entries []any) []any {
	out := entries[:0]
	for _, e := range entries {
		entry, ok := e.(map[string]any)
		if !ok {
			out = append(out, e)
			continue
		}
		inner := rawSlice(entry["hooks"])
		if len(inner) > 0 {
			if first, ok := inner[0].(map[string]any); ok {
				if cmd, _ := first["command"].(string); isVibecopHookCommand(cmd) {
					continue
				}
			}
		}
		out = append(out, e)
	}
	return out
}

// removeCopilotVibecopEntries removes Copilot flat-shape entries whose
// `bash` is a vibecop hook command. Unknown sibling fields on the
// surviving entries (powershell, cwd, etc.) are preserved.
func removeCopilotVibecopEntries(entries []any) []any {
	out := entries[:0]
	for _, e := range entries {
		entry, ok := e.(map[string]any)
		if !ok {
			out = append(out, e)
			continue
		}
		bash, _ := entry["bash"].(string)
		if isVibecopHookCommand(bash) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// writeRawJSON writes v to path atomically — temp file then rename — so
// a crash mid-write cannot leave the user's settings.json corrupted.
func writeRawJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	tmp := path + ".vibecop.tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		// Best-effort: try to remove the leftover temp file before bubbling
		// the rename error back.
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %s: %w", tmp, err)
	}
	return nil
}
