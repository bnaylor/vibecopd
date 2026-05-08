package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestActivityStoreAppend(t *testing.T) {
	s := NewActivityStore("test-hash", 3)
	s.Append("Bash", "go build", "approve")
	s.Append("Read", "main.go", "approve")
	s.Append("Bash", "rm -rf /", "deny")

	recent := s.Recent()
	if len(recent) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(recent))
	}
	if recent[2].Verdict != "deny" {
		t.Errorf("expected last entry to be deny, got %s", recent[2].Verdict)
	}
}

func TestActivityStoreWindow(t *testing.T) {
	s := NewActivityStore("test-hash", 2)
	s.Append("Bash", "a", "approve")
	s.Append("Bash", "b", "approve")
	s.Append("Bash", "c", "approve")

	recent := s.Recent()
	if len(recent) != 2 {
		t.Fatalf("expected 2 entries (window=2), got %d", len(recent))
	}
	if recent[0].Input != "b" {
		t.Errorf("expected oldest entry to be 'b', got %s", recent[0].Input)
	}
}

func TestActivityLoadAndSave(t *testing.T) {
	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)

	// Set up a project directory manually since ActivityStore uses config paths.
	projectHash := "test-activity"
	s := NewActivityStore(projectHash, 10)
	s.Append("Bash", "go build", "approve")
	s.Append("Read", "file.go", "approve")

	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	// Load into a new store.
	s2 := NewActivityStore(projectHash, 10)
	if err := s2.Load(); err != nil {
		t.Fatal(err)
	}

	recent := s2.Recent()
	if len(recent) != 2 {
		t.Fatalf("expected 2 entries after load, got %d", len(recent))
	}
	if recent[0].Tool != "Bash" || recent[1].Tool != "Read" {
		t.Error("loaded entries mismatch")
	}
}

func TestActivityLoadMissingFile(t *testing.T) {
	s := NewActivityStore("nonexistent-hash", 10)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	if len(s.Recent()) != 0 {
		t.Error("expected empty store after loading missing file")
	}
}

func TestTimestampOnAppend(t *testing.T) {
	s := NewActivityStore("t", 5)
	s.Append("Bash", "echo", "approve")
	recent := s.Recent()
	if recent[0].Timestamp == "" {
		t.Error("expected timestamp on appended entry")
	}
}

func TestAuditLoggerDisabled(t *testing.T) {
	l := NewLogger("hash", false)
	if err := l.Write(AuditRecord{}); err != nil {
		t.Fatal(err)
	}
	key, err := l.WritePending(AuditRecord{})
	if err != nil || key != "" {
		t.Fatal("expected empty key when disabled")
	}
}

func TestAuditLoggerWrite(t *testing.T) {
	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)

	lat := int64(42)
	l := NewLogger("test-audit-hash", true)
	rec := AuditRecord{
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
		ToolName:      "Bash",
		ToolInput:     "go test",
		Verdict:       "approve",
		Reason:        "all good",
		HumanDecision: nil,
		LatencyMs:     &lat,
	}
	if err := l.Write(rec); err != nil {
		t.Fatal(err)
	}

	// Verify the file was created.
	// The path is ~/.vibecop/projects/test-audit-hash/audit/YYYY-MM-DD.jsonl
	home := tmpHome
	today := time.Now().UTC().Format("2006-01-02")
	auditFile := filepath.Join(home, ".vibecop", "projects", "test-audit-hash", "audit", today+".jsonl")

	data, err := os.ReadFile(auditFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "go test") {
		t.Error("expected audit file to contain tool input")
	}
}

func TestAuditLoggerPending(t *testing.T) {
	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)

	l := NewLogger("test-pending", true)
	rec := AuditRecord{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		ToolName:  "Bash",
		ToolInput: "rm -rf /",
		Verdict:   "escalate",
	}

	key, err := l.WritePending(rec)
	if err != nil {
		t.Fatal(err)
	}
	if key == "" {
		t.Fatal("expected non-empty key for pending record")
	}

	// Complete the pending record.
	human := "blocked"
	if err := l.CompletePending(key, human); err != nil {
		t.Fatal(err)
	}

	// Verify the completed record is in the audit file.
	home := tmpHome
	today := time.Now().UTC().Format("2006-01-02")
	auditFile := filepath.Join(home, ".vibecop", "projects", "test-pending", "audit", today+".jsonl")

	data, err := os.ReadFile(auditFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "blocked") {
		t.Error("expected completed record to contain human decision")
	}
}

func TestAuditLoggerListPending(t *testing.T) {
	l := NewLogger("test-list", true)

	// No pendings yet.
	if got := l.ListPending(); len(got) != 0 {
		t.Fatalf("expected empty list, got %d entries", len(got))
	}

	rec1 := AuditRecord{Timestamp: "2026-05-07T10:00:00Z", ToolName: "Bash", ToolInput: "rm -rf /", Verdict: "escalate", Reason: "scary"}
	rec2 := AuditRecord{Timestamp: "2026-05-07T10:00:01Z", ToolName: "Read", ToolInput: "/etc/passwd", Verdict: "error", Reason: "evaluator down"}
	if _, err := l.WritePending(rec1); err != nil {
		t.Fatal(err)
	}
	if _, err := l.WritePending(rec2); err != nil {
		t.Fatal(err)
	}

	got := l.ListPending()
	if len(got) != 2 {
		t.Fatalf("expected 2 pending entries, got %d", len(got))
	}
	for _, p := range got {
		if p.ProjectHash != "test-list" {
			t.Errorf("expected projectHash propagated, got %q", p.ProjectHash)
		}
		if p.Key == "" {
			t.Error("expected non-empty key")
		}
		if p.Tool == "" || p.Verdict == "" {
			t.Errorf("missing fields in entry %+v", p)
		}
	}
}

func TestAuditLoggerListPendingDisabled(t *testing.T) {
	l := NewLogger("test-disabled", false)
	if got := l.ListPending(); got != nil {
		t.Errorf("expected nil when disabled, got %d entries", len(got))
	}
}

func TestAuditLoggerListPendingSnapshot(t *testing.T) {
	// Mutating the returned slice must not affect internal state.
	l := NewLogger("test-snap", true)
	l.WritePending(AuditRecord{Timestamp: "t1", ToolName: "Bash", Verdict: "escalate"})

	got := l.ListPending()
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d", len(got))
	}
	got[0].Tool = "MUTATED"

	got2 := l.ListPending()
	if got2[0].Tool != "Bash" {
		t.Errorf("internal state mutated by caller: got %q", got2[0].Tool)
	}
}

func TestAuditLoggerWritePendingUsesUniqueKeys(t *testing.T) {
	l := NewLogger("test-unique", true)
	rec := AuditRecord{
		Timestamp: "2026-05-07T10:00:00Z",
		ToolName:  "Bash",
		Verdict:   "escalate",
	}

	key1, err := l.WritePending(rec)
	if err != nil {
		t.Fatal(err)
	}
	key2, err := l.WritePending(rec)
	if err != nil {
		t.Fatal(err)
	}
	if key1 == key2 {
		t.Fatalf("expected unique keys, got %q twice", key1)
	}

	if got := l.ListPending(); len(got) != 2 {
		t.Fatalf("expected 2 pending entries after identical writes, got %d", len(got))
	}
}

func TestAuditLoggerFlushPending(t *testing.T) {
	l := NewLogger("test-flush", true)
	l.WritePending(AuditRecord{Timestamp: "t1", ToolName: "Bash", Verdict: "escalate"})
	l.WritePending(AuditRecord{Timestamp: "t2", ToolName: "Read", Verdict: "error"})

	// Flush should write all pending with the default decision.
	l.FlushPending("blocked")

	if len(l.pending) != 0 {
		t.Errorf("expected 0 pending after flush, got %d", len(l.pending))
	}
}
