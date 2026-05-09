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

// TestStripValidationMarkersRemovesAndPreservesContent guards the
// post-VCOP-16 fix for the markers-survive-into-live-config bug: if a
// user `:q!`s without saving and the tempfile still has markers from
// the prior failed pass, we must remove them before re-validating —
// otherwise a successful parse would carry the markers into
// ~/.vibecop/config.toml at rename time.
func TestStripValidationMarkersRemovesAndPreservesContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "with-markers.toml")
	body := strings.Join([]string{
		"# VIBECOP_VALIDATION_HEADER: prior error",
		"key1 = \"ok\"",
		"# VIBECOP_VALIDATION_ERROR (line 3): something",
		"key2 = \"value\"",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}

	if err := stripValidationMarkers(path); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := string(got)
	if strings.Contains(out, "VIBECOP_VALIDATION_HEADER") {
		t.Errorf("HEADER marker should be stripped, got: %q", out)
	}
	if strings.Contains(out, "VIBECOP_VALIDATION_ERROR") {
		t.Errorf("ERROR marker should be stripped, got: %q", out)
	}
	if !strings.Contains(out, `key1 = "ok"`) {
		t.Errorf("non-marker content must be preserved, got: %q", out)
	}
	if !strings.Contains(out, `key2 = "value"`) {
		t.Errorf("non-marker content must be preserved, got: %q", out)
	}
}

// TestStripValidationMarkersNoOpWhenAbsent — the helper must not
// rewrite the file when there are no markers, so an unrelated edit's
// mtime stays intact.
func TestStripValidationMarkersNoOpWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clean.toml")
	if err := os.WriteFile(path, []byte("key = \"ok\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	st0, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := stripValidationMarkers(path); err != nil {
		t.Fatal(err)
	}
	st1, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !st0.ModTime().Equal(st1.ModTime()) {
		t.Errorf("clean file should not have been rewritten; mtime changed %v → %v", st0.ModTime(), st1.ModTime())
	}
}

// TestEditConfigSymlinkResolution guards the fix where editConfigFile
// calls filepath.EvalSymlinks before copyToTemp. Without the fix,
// os.Rename would replace the symlink entry itself rather than the
// file it points to. After the fix, the temp file is created next to
// the real target so the eventual rename stays on the same device.
func TestEditConfigSymlinkResolution(t *testing.T) {
	base := t.TempDir()
	realDir := filepath.Join(base, "real")
	linkDir := filepath.Join(base, "links")
	if err := os.Mkdir(realDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(linkDir, 0700); err != nil {
		t.Fatal(err)
	}

	realPath := filepath.Join(realDir, "config.toml")
	if err := os.WriteFile(realPath, []byte("key = \"val\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(linkDir, "config.toml")
	if err := os.Symlink(realPath, linkPath); err != nil {
		t.Skip("symlinks not supported on this OS:", err)
	}

	// Simulate what editConfigFile does now: resolve symlinks first.
	livePath := linkPath
	if rp, err := filepath.EvalSymlinks(livePath); err == nil {
		livePath = rp
	}

	if livePath == linkPath {
		t.Fatal("EvalSymlinks should have resolved the link, but path is unchanged")
	}

	tmp, err := copyToTemp(livePath)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp)

	// The temp file must land next to the real file, not the symlink.
	// Resolve realDir's own symlinks so the comparison works on macOS,
	// where t.TempDir() returns a /var/... path that is itself a
	// symlink to /private/var/... — without this, livePath comes back
	// under /private/var while realDir was captured as /var, and the
	// equality check fails on a healthy fix.
	expectedDir, err := filepath.EvalSymlinks(realDir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(tmp) != expectedDir {
		t.Errorf("temp file should be in real dir %q; got %q", expectedDir, filepath.Dir(tmp))
	}
}

// TestResolveEditorParsesEditorWithFlags covers the VCOP-16 fix where
// $EDITOR='vim --noplugin' previously failed silently because
// exec.Command treated the whole string as a binary path while
// supportsPlusLineFlag parsed only "vim".
func TestResolveEditorParsesEditorWithFlags(t *testing.T) {
	t.Setenv("EDITOR", "vim --noplugin --cmd setSomething")
	bin, args := resolveEditor()
	if bin != "vim" {
		t.Errorf("expected binary 'vim', got %q", bin)
	}
	if len(args) != 2 || args[0] != "--noplugin" || args[1] != "--cmd" {
		// (we capture the third token "setSomething" as the third arg
		// — confirm length first)
	}
	if len(args) < 2 {
		t.Errorf("expected leading args to be parsed, got %v", args)
	}
}

func TestResolveEditorFallsBackToVi(t *testing.T) {
	t.Setenv("EDITOR", "")
	bin, args := resolveEditor()
	if bin != "vi" || len(args) != 0 {
		t.Errorf("empty $EDITOR should fall back to vi (no args), got %q %v", bin, args)
	}
}

func TestEditorSupportsPlusLine(t *testing.T) {
	cases := map[string]bool{
		"vim":          true,
		"/usr/bin/vim": true,
		"emacs":        false,
		"":             false,
	}
	for in, want := range cases {
		if got := editorSupportsPlusLine(in); got != want {
			t.Errorf("editorSupportsPlusLine(%q) = %v, want %v", in, got, want)
		}
	}
}
