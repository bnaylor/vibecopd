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

// editConfigFile drives the edit loop:
//
//  1. Copy the live config.toml into a tempfile.
//  2. Suspend the TUI and exec $EDITOR (fallback `vi`) on the tempfile,
//     positioning the cursor at the violating line on retry when the
//     editor accepts `+<lineno>`.
//  3. After the editor returns, read the tempfile and run
//     toml.DecodeFile against a fresh config.Config — this is the same
//     parser the daemon uses, so semantic agreement is guaranteed.
//  4. On parse failure: extract the line number from
//     toml.ParseError.Position, inject a `# VIBECOP_VALIDATION_ERROR:
//     <msg>` comment at that line, and re-launch the editor. Loop
//     until parse succeeds or the user aborts (saves no changes /
//     exits non-zero).
//  5. On parse success: atomically rename the tempfile over the live
//     config.toml. Surface a one-line confirmation in the configFileView.
//
// The edit happens inside app.Suspend so the editor owns the terminal
// — tview restores its state when Suspend returns. The function is a
// no-op if no config_path has landed yet (defensive against early-Enter
// before the first get_config response).
func (a *App) editConfigFile() {
	if a.app == nil || a.configPath == "" {
		return
	}

	livePath := a.configPath
	tmpPath, err := copyToTemp(livePath)
	if err != nil {
		a.flashConfigStatus(fmt.Sprintf("(prepare temp file failed: %v)", err))
		return
	}
	defer os.Remove(tmpPath)

	// Loop until the user produces a valid file or backs out.
	const maxRetries = 5
	for attempt := 0; attempt < maxRetries; attempt++ {
		errorLine := 0
		ok := a.app.Suspend(func() {
			runEditor(tmpPath, errorLine)
		})
		if !ok {
			a.flashConfigStatus("(editor suspend failed — TUI not running?)")
			return
		}

		var cfg config.Config
		if _, derr := toml.DecodeFile(tmpPath, &cfg); derr == nil {
			// Validation passed. Atomic rename over the live file.
			if err := os.Rename(tmpPath, livePath); err != nil {
				a.flashConfigStatus(fmt.Sprintf("(rename to %s failed: %v)", livePath, err))
				return
			}
			a.flashConfigStatus(fmt.Sprintf("(saved %s)", livePath))
			// Re-open the read-only view to reflect the new contents.
			a.openConfigView()
			return
		} else {
			// Annotate the failing line so the editor reopens with
			// guidance baked into the file, not just a transient
			// banner the user has to remember. Position lookup uses
			// BurntSushi's typed ParseError; on other error types
			// we still surface the message at the top of the file.
			line, msg := extractTomlPosition(derr)
			if line > 0 {
				errorLine = line
			}
			if err := annotateValidationError(tmpPath, line, msg); err != nil {
				a.flashConfigStatus(fmt.Sprintf("(annotate failed: %v)", err))
				return
			}
			// Loop — runEditor will re-open the (now-annotated) tmp
			// file with cursor at errorLine when the editor supports
			// `+<lineno>` (vi/vim/nvim/nano).
		}
	}

	a.flashConfigStatus("(validation failed after several attempts — discarded)")
}

// runEditor execs the user's $EDITOR on path, with `+<line>` prepended
// to argv when the editor is one of the common editors that supports
// it. Blocks until the editor exits.
func runEditor(path string, line int) {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	args := []string{}
	if line > 0 && supportsPlusLineFlag(editor) {
		args = append(args, fmt.Sprintf("+%d", line))
	}
	args = append(args, path)
	cmd := exec.Command(editor, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
}

// supportsPlusLineFlag reports whether the binary basename is one of
// the editors that accept `+<lineno>` argv. Conservative — emacs and
// helix use different conventions; falling back to "no positioning"
// is acceptable.
func supportsPlusLineFlag(editor string) bool {
	fields := strings.Fields(editor)
	if len(fields) == 0 {
		return false
	}
	base := filepath.Base(fields[0])
	switch base {
	case "vi", "vim", "nvim", "nano", "view", "vimx", "ex":
		return true
	}
	return false
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

// annotateValidationError edits the temp file in place: prepends a
// header line summarizing the error, and (when line > 0) inserts a
// pointer comment immediately above the violating line. Idempotent —
// pre-existing VIBECOP_VALIDATION_ERROR markers are stripped first so
// repeated retries don't accumulate.
func annotateValidationError(path string, line int, message string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	cleaned := make([]string, 0, len(lines))
	for _, l := range lines {
		if strings.HasPrefix(l, "# VIBECOP_VALIDATION_ERROR") || strings.HasPrefix(l, "# VIBECOP_VALIDATION_HEADER") {
			continue
		}
		cleaned = append(cleaned, l)
	}

	header := fmt.Sprintf("# VIBECOP_VALIDATION_HEADER: %s", strings.ReplaceAll(message, "\n", " "))
	cleaned = append([]string{header}, cleaned...)

	// Translate the error line from the original file's coordinate
	// space to the annotated buffer (header pushes everything down by
	// one line) and inject a pointer comment.
	if line > 0 {
		adjusted := line + 1 // account for the header we just prepended
		marker := fmt.Sprintf("# VIBECOP_VALIDATION_ERROR (line %d): %s", line, strings.ReplaceAll(message, "\n", " "))
		if adjusted-1 >= 0 && adjusted-1 < len(cleaned) {
			out := make([]string, 0, len(cleaned)+1)
			out = append(out, cleaned[:adjusted-1]...)
			out = append(out, marker)
			out = append(out, cleaned[adjusted-1:]...)
			cleaned = out
		}
	}

	return os.WriteFile(path, []byte(strings.Join(cleaned, "\n")), 0644)
}

// flashConfigStatus pushes a one-line status message into the read-
// only configFileView so the user gets feedback after an edit attempt
// without needing a separate modal. Safe from any goroutine — uses
// QueueUpdateDraw.
func (a *App) flashConfigStatus(msg string) {
	if a.app == nil || a.configFileView == nil {
		return
	}
	a.app.QueueUpdateDraw(func() {
		body := a.readConfigFileForView()
		a.configFileView.SetText(msg + "\n\n" + body)
	})
}
