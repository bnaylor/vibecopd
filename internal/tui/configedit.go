package tui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/bnaylor/vibecop/internal/config"
)

// maxEditRetries caps how many times editConfigFile re-launches the
// editor on a failed TOML parse before giving up. 5 is plenty for an
// interactive flow — if the user can't produce valid TOML in 5 tries
// they likely want to abort and try again from a clean shell.
const maxEditRetries = 5

// readConfigFileBody returns a scrollable representation of the file at
// path: success → raw contents; failure → a banner with the error so
// the user has something actionable rather than a blank screen.
func readConfigFileBody(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("(failed to read %s: %v)", path, err)
	}
	header := fmt.Sprintf("# %s\n# (read-only — press Esc to return, e to edit)\n\n", path)
	return header + string(data)
}

// editConfigFile drives the edit loop. Runs entirely on the tview main
// goroutine (it's invoked from input captures), so it must not call
// QueueUpdateDraw on itself — that would block the main loop waiting
// to drain an update the loop is currently busy executing. Direct
// SetText is fine here.
//
// Flow:
//
//  1. Copy the live config.toml into a sibling tempfile (same dir →
//     atomic rename later).
//  2. Suspend the TUI and exec $EDITOR (fallback `vi`) on the tempfile,
//     positioning the cursor at the violating line on retry when the
//     editor accepts `+<lineno>`.
//  3. After the editor returns: if the editor exited non-zero (the
//     user aborted, e.g. `:cq` in vim) → discard, no rename. Else
//     strip any pre-existing VIBECOP_VALIDATION_* markers from the
//     temp file (so a `:q!` after a prior failed pass doesn't carry
//     the comments forward into the live file) and run toml.DecodeFile
//     for validation against a fresh config.Config.
//  4. On parse failure: extract the line number from
//     toml.ParseError.Position, inject `# VIBECOP_VALIDATION_*`
//     comments at that line, and re-launch positioned at that line.
//     errorLine persists across iterations so retries land at the
//     latest violation.
//  5. On parse success: atomically rename the tempfile over the live
//     config.toml. If the rename itself fails, we DO NOT remove the
//     temp file — surface its path so the user can recover their
//     edits manually.
func (a *App) editConfigFile() {
	if a.app == nil || a.configPath == "" {
		return
	}

	livePath := a.configPath
	// Resolve symlinks so os.Rename replaces the file the link points
	// to, not the symlink entry itself. EvalSymlinks fails only when
	// the path doesn't exist or a loop is detected; fall back to the
	// original path in that case (current behaviour is better than
	// refusing to open the editor).
	if realPath, err := filepath.EvalSymlinks(livePath); err == nil {
		livePath = realPath
	}
	tmpPath, err := copyToTemp(livePath)
	if err != nil {
		a.flashConfigStatus(fmt.Sprintf("(prepare temp file failed: %v)", err))
		return
	}

	errorLine := 0
	for attempt := 0; attempt < maxEditRetries; attempt++ {
		exitCode, runErr := a.runEditorWithStatus(tmpPath, errorLine)

		// Editor itself failed to start (binary missing, permission
		// denied) → bail out. Tempfile is preserved so the user can
		// inspect what we tried to feed it, but we clean up since
		// nothing was ever edited.
		if runErr != nil {
			os.Remove(tmpPath)
			a.flashConfigStatus(fmt.Sprintf("(failed to launch editor: %v)", runErr))
			return
		}

		// Editor exited non-zero — interpreted as "user aborted"
		// (e.g. `:cq` in vim, ctrl-c in nano). Discard tempfile
		// without touching the live config.
		if exitCode != 0 {
			os.Remove(tmpPath)
			a.flashConfigStatus(fmt.Sprintf("(editor exited with code %d — discarded, no changes saved)", exitCode))
			return
		}

		// Strip any leftover validation markers from the previous
		// attempt before parsing — otherwise the user pressing `:q!`
		// (write nothing, exit clean) would carry our injected
		// comments into the live file on the next successful parse.
		if err := stripValidationMarkers(tmpPath); err != nil {
			os.Remove(tmpPath)
			a.flashConfigStatus(fmt.Sprintf("(failed to strip validation markers: %v)", err))
			return
		}

		var cfg config.Config
		_, derr := toml.DecodeFile(tmpPath, &cfg)
		if derr == nil {
			// Validation passed. Atomic rename. If rename fails (e.g.
			// EROFS or cross-device), preserve the tempfile and tell
			// the user where it lives — losing their work silently is
			// the worst outcome.
			if err := os.Rename(tmpPath, livePath); err != nil {
				a.flashConfigStatus(fmt.Sprintf("(rename to %s failed: %v — your edits are preserved at %s)", livePath, err, tmpPath))
				return
			}
			a.flashConfigStatus(fmt.Sprintf("(saved %s)", livePath))
			a.openConfigView()
			return
		}

		// Parse failed — annotate, update errorLine for cursor
		// positioning on the next launch, and loop.
		line, msg := extractTomlPosition(derr)
		if line > 0 {
			errorLine = line
		}
		if err := annotateValidationError(tmpPath, line, msg); err != nil {
			os.Remove(tmpPath)
			a.flashConfigStatus(fmt.Sprintf("(annotate failed: %v)", err))
			return
		}
	}

	// Hit the retry cap. Keep the tempfile so the user can recover
	// their last attempt.
	a.flashConfigStatus(fmt.Sprintf("(validation failed after %d attempts — your last edit is preserved at %s)", maxEditRetries, tmpPath))
}

// runEditorWithStatus execs the user's $EDITOR on path, with `+<line>`
// prepended to argv when the editor is one of the common editors that
// supports it. Returns (exitCode, error) — error is non-nil only when
// the OS-level exec failed (binary missing); a non-zero exitCode is
// treated as "user aborted" by the caller. Blocks until the editor
// exits.
func (a *App) runEditorWithStatus(path string, line int) (int, error) {
	editor, args := resolveEditor()
	if line > 0 && editorSupportsPlusLine(editor) {
		args = append(args, fmt.Sprintf("+%d", line))
	}
	args = append(args, path)

	var (
		exitCode int
		runErr   error
	)
	ok := a.app.Suspend(func() {
		cmd := exec.Command(editor, args...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				exitCode = ee.ExitCode()
			} else {
				runErr = err
			}
		}
	})
	if !ok {
		return 0, fmt.Errorf("app.Suspend refused to run (TUI not running?)")
	}
	return exitCode, runErr
}

// resolveEditor parses $EDITOR (fallback "vi") into (binary, leadingArgs).
// Splitting on whitespace lets users set EDITOR="vim --noplugin" or
// "env FOO=1 vim" and have us exec the right thing — whereas a literal
// exec.Command("vim --noplugin", path) would fail with "executable file
// not found" because exec.Command treats the first argument as a single
// program path. We do not invoke a shell, so this is not a metachar
// concern; but it does match supportsPlusLineFlag's parsing so they
// agree on what the program name is.
func resolveEditor() (binary string, leadingArgs []string) {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	fields := strings.Fields(editor)
	if len(fields) == 0 {
		return "vi", nil
	}
	return fields[0], append([]string(nil), fields[1:]...)
}

// editorSupportsPlusLine reports whether the binary basename is one of
// the editors that accept `+<lineno>`. Conservative — emacs and helix
// use different conventions; falling back to "no positioning" is
// acceptable.
func editorSupportsPlusLine(editor string) bool {
	if editor == "" {
		return false
	}
	switch filepath.Base(editor) {
	case "vi", "vim", "nvim", "nano", "view", "vimx", "ex":
		return true
	}
	return false
}

// supportsPlusLineFlag is the original API: takes the raw $EDITOR
// string and returns whether `+<line>` is supported. Kept for the
// existing test surface; new callers should use editorSupportsPlusLine
// against an already-parsed binary.
func supportsPlusLineFlag(editor string) bool {
	fields := strings.Fields(editor)
	if len(fields) == 0 {
		return false
	}
	return editorSupportsPlusLine(fields[0])
}

// copyToTemp writes the contents of src to a fresh temp file in the
// same directory, returning the temp file's path. Same-directory
// keeps the eventual atomic rename on the same filesystem.
func copyToTemp(src string) (string, error) {
	dir := filepath.Dir(src)
	f, err := os.CreateTemp(dir, ".vibecop-config-*.toml")
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := os.ReadFile(src)
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// extractTomlPosition returns (lineNumber, message) for a
// BurntSushi/toml parse error. lineNumber is 0 when the error is not a
// ParseError (e.g. unmarshal-into-struct mismatch); message always has
// content suitable for an in-file annotation.
func extractTomlPosition(err error) (int, string) {
	var pe toml.ParseError
	if errors.As(err, &pe) {
		return pe.Position.Line, pe.Message
	}
	return 0, err.Error()
}

// validationMarkerPrefixes is the set of comment prefixes that
// editConfigFile injects between attempts. They are stripped before
// every parse and after every successful edit so they never leak into
// the live config file.
var validationMarkerPrefixes = []string{
	"# VIBECOP_VALIDATION_HEADER",
	"# VIBECOP_VALIDATION_ERROR",
}

// stripValidationMarkers rewrites path with all VIBECOP_VALIDATION_*
// comment lines removed. Called before parsing so a user who quit the
// editor with `:q!` (no save) doesn't carry forward our injected
// comments from a prior failed pass.
func stripValidationMarkers(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	cleaned := make([]string, 0, len(lines))
	for _, l := range lines {
		if hasAnyPrefix(l, validationMarkerPrefixes) {
			continue
		}
		cleaned = append(cleaned, l)
	}
	if len(cleaned) == len(lines) {
		// No markers present — skip the rewrite to preserve mtime.
		return nil
	}
	return os.WriteFile(path, []byte(strings.Join(cleaned, "\n")), 0600)
}

// annotateValidationError edits the temp file in place: prepends a
// header line summarizing the error, and (when line > 0) inserts a
// pointer comment immediately above the violating line. Idempotent —
// pre-existing VIBECOP_VALIDATION_* markers are stripped first so
// repeated retries don't accumulate.
//
// Mode 0600 matches the upstream config file (typically created at
// 0600 by setup) — `os.WriteFile` only sets the mode on file create,
// so existing files keep their inode mode regardless, but the literal
// here documents intent.
func annotateValidationError(path string, line int, message string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	cleaned := make([]string, 0, len(lines))
	for _, l := range lines {
		if hasAnyPrefix(l, validationMarkerPrefixes) {
			continue
		}
		cleaned = append(cleaned, l)
	}

	header := fmt.Sprintf("# VIBECOP_VALIDATION_HEADER: %s", strings.ReplaceAll(message, "\n", " "))
	cleaned = append([]string{header}, cleaned...)

	if line > 0 {
		adjusted := line + 1
		marker := fmt.Sprintf("# VIBECOP_VALIDATION_ERROR (line %d): %s", line, strings.ReplaceAll(message, "\n", " "))
		if adjusted-1 >= 0 && adjusted-1 < len(cleaned) {
			out := make([]string, 0, len(cleaned)+1)
			out = append(out, cleaned[:adjusted-1]...)
			out = append(out, marker)
			out = append(out, cleaned[adjusted-1:]...)
			cleaned = out
		}
	}

	return os.WriteFile(path, []byte(strings.Join(cleaned, "\n")), 0600)
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// flashConfigStatus paints a one-line status message above a fresh
// re-render of the config file. Must be called from the tview main
// goroutine — the calling sites (input handlers, editConfigFile) all
// satisfy that. An earlier version used QueueUpdateDraw, which
// deadlocks when called from inside an input handler (the main loop
// is busy executing the handler that called us, so it can never
// drain the update). Direct SetText is correct here.
func (a *App) flashConfigStatus(msg string) {
	if a.configFileView == nil {
		return
	}
	body := a.readConfigFileForView()
	a.configFileView.SetText(msg + "\n\n" + body)
}
