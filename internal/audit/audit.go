package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/bnaylor/vibecop/internal/config"
)

// AuditRecord is a single permanent audit log entry.
type AuditRecord struct {
	Timestamp     string  `json:"timestamp"`
	ToolName      string  `json:"toolName"`
	ToolInput     string  `json:"toolInput"`
	Verdict       string  `json:"verdict"`
	Reason        string  `json:"reason,omitempty"`
	HumanDecision *string `json:"humanDecision"`
	LatencyMs     *int64  `json:"latencyMs,omitempty"`
}

// PendingEscalation is a snapshot of an in-memory pending audit record,
// suitable for sending to the TUI escalation queue. It carries the
// project hash so the operator can finalise it via Logger.CompletePending
// without the TUI having to know which project it came from.
type PendingEscalation struct {
	Key         string `json:"key"`
	ProjectHash string `json:"projectHash"`
	Timestamp   string `json:"timestamp"`
	Tool        string `json:"tool"`
	Input       string `json:"input,omitempty"`
	Verdict     string `json:"verdict"`
	Reason      string `json:"reason,omitempty"`
}

// Logger writes structured audit records to daily files.
type Logger struct {
	projectHash string
	enabled     bool
	pending     map[string]*AuditRecord
	pendingSeq  uint64
	mu          sync.Mutex
}

// NewLogger creates an audit logger. When enabled is false, all methods are no-ops.
func NewLogger(projectHash string, enabled bool) *Logger {
	return &Logger{
		projectHash: projectHash,
		enabled:     enabled,
		pending:     make(map[string]*AuditRecord),
	}
}

// Write appends an audit record to today's file immediately.
func (l *Logger) Write(rec AuditRecord) error {
	if !l.enabled {
		return nil
	}

	path, err := l.auditFilePath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create audit dir: %w", err)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open audit file: %w", err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	if err := enc.Encode(rec); err != nil {
		return fmt.Errorf("encode audit record: %w", err)
	}
	return nil
}

// WritePending stores a partial audit record (for escalate/timeout).
// Returns an opaque key that can be used to complete the record later.
func (l *Logger) WritePending(rec AuditRecord) (string, error) {
	if !l.enabled {
		return "", nil
	}

	l.mu.Lock()
	l.pendingSeq++
	key := fmt.Sprintf("%s|%s|%d", rec.ToolName, rec.Timestamp, l.pendingSeq)
	l.pending[key] = &rec
	l.mu.Unlock()
	return key, nil
}

// CompletePending finalizes a pending record with the human's decision
// and writes it to the audit file.
func (l *Logger) CompletePending(key string, humanDecision string) error {
	if !l.enabled {
		return nil
	}

	l.mu.Lock()
	rec, ok := l.pending[key]
	delete(l.pending, key)
	l.mu.Unlock()

	if !ok {
		return fmt.Errorf("pending record not found: %s", key)
	}

	rec.HumanDecision = &humanDecision
	return l.Write(*rec)
}

// ListPending returns a snapshot of the in-memory pending records.
// Each entry carries the logger's projectHash so callers can route
// completions back via the right Logger. Returns nil when disabled.
func (l *Logger) ListPending() []PendingEscalation {
	if !l.enabled {
		return nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	out := make([]PendingEscalation, 0, len(l.pending))
	for key, rec := range l.pending {
		out = append(out, PendingEscalation{
			Key:         key,
			ProjectHash: l.projectHash,
			Timestamp:   rec.Timestamp,
			Tool:        rec.ToolName,
			Input:       rec.ToolInput,
			Verdict:     rec.Verdict,
			Reason:      rec.Reason,
		})
	}
	return out
}

// ProjectHash returns the project hash this logger was constructed with.
func (l *Logger) ProjectHash() string { return l.projectHash }

// FlushPending writes all pending records with a default decision.
// Called on daemon shutdown to avoid losing uncompleted records.
func (l *Logger) FlushPending(defaultDecision string) {
	l.mu.Lock()
	pending := make(map[string]*AuditRecord)
	for k, v := range l.pending {
		pending[k] = v
	}
	l.pending = make(map[string]*AuditRecord)
	l.mu.Unlock()

	for key, rec := range pending {
		rec.HumanDecision = &defaultDecision
		if err := l.Write(*rec); err != nil {
			fmt.Fprintf(os.Stderr, "audit: flush error for %s: %v\n", key, err)
		}
	}
}

// auditFilePath returns today's audit file path: ~/.vibecop/projects/<hash>/audit/YYYY-MM-DD.jsonl
func (l *Logger) auditFilePath() (string, error) {
	dir, err := config.AuditDir(l.projectHash)
	if err != nil {
		return "", err
	}
	today := time.Now().UTC().Format("2006-01-02")
	return filepath.Join(dir, today+".jsonl"), nil
}
