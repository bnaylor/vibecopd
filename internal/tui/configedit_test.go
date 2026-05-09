package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestSupportsPlusLineFlag(t *testing.T) {
	cases := map[string]bool{
		"vi":         true,
		"vim":        true,
		"nvim":       true,
		"nano":       true,
		"emacs":      false,
		"code":       false,
		"helix":      false,
		"":           false,
		"/usr/bin/vim": true,
	}
	for editor, want := range cases {
		if got := supportsPlusLineFlag(editor); got != want {
			t.Errorf("supportsPlusLineFlag(%q) = %v, want %v", editor, got, want)
		}
	}
}

func TestExtractTomlPositionParseError(t *testing.T) {
	// Force a parse error from BurntSushi: an unclosed string is a
	// classic ParseError with line/col set.
	bad := "key = \"unterminated"
	var dst map[string]any
	_, err := toml.Decode(bad, &dst)
	if err == nil {
		t.Fatal("expected toml decode to fail on unterminated string")
	}
	line, msg := extractTomlPosition(err)
	if line < 1 {
		t.Errorf("expected non-zero line for ParseError, got %d", line)
	}
	if msg == "" {
		t.Error("expected non-empty message")
	}
}

func TestExtractTomlPositionNonParseError(t *testing.T) {
	plain := errors.New("not a toml parse error")
	line, msg := extractTomlPosition(plain)
	if line != 0 {
		t.Errorf("expected 0 line for non-ParseError, got %d", line)
	}
	if msg == "" {
		t.Error("expected message to round-trip")
	}
}

func TestCopyToTempRoundTrip(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "config.toml")
	body := "endpoint = \"https://example.test\"\n"
	if err := os.WriteFile(src, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	tmp, err := copyToTemp(src)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp)

	if filepath.Dir(tmp) != filepath.Dir(src) {
		t.Errorf("expected tmp in same dir as src for atomic rename, got tmp=%s src=%s", tmp, src)
	}
	got, err := os.ReadFile(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Errorf("temp content mismatch: got %q want %q", got, body)
	}
}

func TestAnnotateValidationErrorInjectsMarker(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.toml")
	original := "key1 = \"ok\"\nkey2 = \"broken\nline3\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	if err := annotateValidationError(path, 2, "expected closing quote"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	if !strings.Contains(out, "VIBECOP_VALIDATION_HEADER") {
		t.Errorf("expected header banner, got: %q", out)
	}
	if !strings.Contains(out, "VIBECOP_VALIDATION_ERROR (line 2)") {
		t.Errorf("expected per-line marker, got: %q", out)
	}

	// Re-running should be idempotent — markers must not accumulate.
	if err := annotateValidationError(path, 2, "expected closing quote"); err != nil {
		t.Fatal(err)
	}
	data2, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data2), "VIBECOP_VALIDATION_HEADER") != 1 {
		t.Errorf("annotate should be idempotent on header; got: %q", data2)
	}
	if strings.Count(string(data2), "VIBECOP_VALIDATION_ERROR (line 2)") != 1 {
		t.Errorf("annotate should be idempotent on per-line marker; got: %q", data2)
	}
}

func TestReadConfigFileBodyMissing(t *testing.T) {
	got := readConfigFileBody(filepath.Join(t.TempDir(), "missing.toml"))
	if !strings.Contains(got, "failed to read") {
		t.Errorf("expected error banner for missing file, got: %q", got)
	}
}
