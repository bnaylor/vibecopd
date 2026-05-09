package daemon

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bnaylor/vibecop/internal/config"
)

func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "vc*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func newTestDaemon(t *testing.T) (*Daemon, string) {
	t.Helper()
	// Use /tmp directly — t.TempDir() paths under /var/folders can exceed
	// the 104-byte macOS Unix socket path limit.
	dir := shortTempDir(t)
	socketPath := filepath.Join(dir, "d.sock")
	cfg := config.DefaultConfig()
	d := New(socketPath, cfg)
	d.OnPermission(func(req Request) Verdict {
		return Verdict{Verdict: "approve", Reason: "test handler"}
	})
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Stop() })
	return d, socketPath
}

func TestStartStop(t *testing.T) {
	d, socketPath := newTestDaemon(t)

	// Should be listening.
	if _, err := os.Stat(socketPath); os.IsNotExist(err) {
		t.Error("socket should exist after Start")
	}

	// Stop cleanly.
	if err := d.Stop(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Error("socket should be removed after Stop")
	}
}

func TestPermissionRequest(t *testing.T) {
	d, socketPath := newTestDaemon(t)
	defer d.Stop()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	req := Request{
		Type:        TypePermissionRequest,
		ProjectPath: "/tmp/test-project",
		Tool:        "Bash",
		Input:       "echo hello",
		SessionID:   "sess-1",
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		t.Fatal(err)
	}

	var resp Verdict
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Verdict != "approve" {
		t.Errorf("expected approve, got %s", resp.Verdict)
	}
	if resp.Reason != "test handler" {
		t.Errorf("expected test handler reason, got %s", resp.Reason)
	}
}

func TestPermissionRequestNoHandler(t *testing.T) {
		dir := shortTempDir(t)
		socketPath := filepath.Join(dir, "d.sock")
	cfg := config.DefaultConfig()
	d := New(socketPath, cfg)
	// No handler registered.
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Stop()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	json.NewEncoder(conn).Encode(Request{
		Type:  TypePermissionRequest,
		Tool:  "Bash",
		Input: "rm -rf /",
	})

	var resp Verdict
	json.NewDecoder(conn).Decode(&resp)
	if resp.Verdict != "escalate" {
		t.Errorf("expected escalate without handler, got %s", resp.Verdict)
	}
}

func TestUnknownRequestType(t *testing.T) {
	d, socketPath := newTestDaemon(t)
	defer d.Stop()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	json.NewEncoder(conn).Encode(Request{
		Type: "nonexistent_type",
	})

	var resp Verdict
	json.NewDecoder(conn).Decode(&resp)
	if resp.Verdict != "escalate" {
		t.Errorf("expected escalate for unknown type, got %s", resp.Verdict)
	}
}

func TestInvalidJSON(t *testing.T) {
	d, socketPath := newTestDaemon(t)
	defer d.Stop()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	conn.Write([]byte("{invalid json}\n"))

	var resp Verdict
	json.NewDecoder(conn).Decode(&resp)
	if resp.Verdict != "escalate" {
		t.Errorf("expected escalate for invalid json, got %s", resp.Verdict)
	}
}

func TestTUISubscribe(t *testing.T) {
	d, socketPath := newTestDaemon(t)
	defer d.Stop()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Subscribe.
	json.NewEncoder(conn).Encode(Request{Type: TypeTUISubscribe})

	// Give the subscription a moment to register.
	time.Sleep(50 * time.Millisecond)

	// Emit an event.
	d.EmitEvent(Event{
		Tool:    "Bash",
		Input:   "go test",
		Verdict: "approve",
		Level:   "info",
	})

	var evt Event
	if err := json.NewDecoder(conn).Decode(&evt); err != nil {
		t.Fatal(err)
	}
	if evt.Tool != "Bash" || evt.Verdict != "approve" {
		t.Errorf("got event %+v", evt)
	}
}

func TestPIDFile(t *testing.T) {
	d, socketPath := newTestDaemon(t)
	defer d.Stop()

	pid, err := ReadPID(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	if pid <= 0 {
		t.Errorf("expected valid pid, got %d", pid)
	}
	if !ProcessExists(pid) {
		t.Error("our own process should exist")
	}
}

func TestDefaultSocketPath(t *testing.T) {
	path := DefaultSocketPath("/tmp/.vibecop")
	if path != "/tmp/.vibecop/daemon.sock" {
		t.Errorf("unexpected path: %s", path)
	}
}

// TestHandlerPanicRecovered verifies that a panic inside a registered
// handler does not crash the daemon process — the AGENTS.md fail-open
// invariant requires that vibecop never blocks an agent (or kills its
// own daemon) due to its own malfunction.
func TestHandlerPanicRecovered(t *testing.T) {
	dir := shortTempDir(t)
	socketPath := filepath.Join(dir, "d.sock")
	cfg := config.DefaultConfig()
	d := New(socketPath, cfg)
	d.OnListPending(func() ([]PendingEntry, bool) {
		panic("simulated handler panic")
	})
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Stop()

	// First connection — handler panics. Connection drops; we don't
	// require a specific response shape here, only that the daemon
	// stays alive.
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	json.NewEncoder(conn).Encode(Request{Type: TypeListPending})
	// Drain whatever (or nothing) comes back.
	io.ReadAll(conn)
	conn.Close()

	// Second connection on the same daemon — must succeed, proving
	// the daemon survived the panic.
	conn2, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("daemon died after handler panic: %v", err)
	}
	defer conn2.Close()
	conn2.SetDeadline(time.Now().Add(2 * time.Second))

	json.NewEncoder(conn2).Encode(Request{
		Type:  TypePermissionRequest,
		Tool:  "Bash",
		Input: "echo hi",
	})
	var resp Verdict
	if err := json.NewDecoder(conn2).Decode(&resp); err != nil {
		t.Fatalf("subsequent permission_request failed: %v", err)
	}
	// onPerm not registered → fall-through escalate is fine; we just
	// care that the daemon answered at all.
	if resp.Verdict == "" {
		t.Error("expected a verdict from the surviving daemon")
	}
}

func TestListPending(t *testing.T) {
	dir := shortTempDir(t)
	socketPath := filepath.Join(dir, "d.sock")
	cfg := config.DefaultConfig()
	d := New(socketPath, cfg)
	d.OnListPending(func() ([]PendingEntry, bool) {
		return []PendingEntry{
			{Key: "k1", ProjectHash: "h1", Tool: "Bash", Input: "rm", Verdict: "escalate", Reason: "scary"},
			{Key: "k2", ProjectHash: "h2", Tool: "Read", Input: "/etc/passwd", Verdict: "error"},
		}, true
	})
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Stop()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	json.NewEncoder(conn).Encode(Request{Type: TypeListPending})
	var resp PendingResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Pending) != 2 {
		t.Fatalf("expected 2 pending, got %d", len(resp.Pending))
	}
	if resp.Pending[0].Key != "k1" || resp.Pending[1].Key != "k2" {
		t.Errorf("unexpected entries: %+v", resp.Pending)
	}
	if !resp.AuditEnabled {
		t.Error("expected audit_enabled=true to round-trip")
	}
}

func TestListPendingAuditDisabled(t *testing.T) {
	dir := shortTempDir(t)
	socketPath := filepath.Join(dir, "d.sock")
	cfg := config.DefaultConfig()
	d := New(socketPath, cfg)
	d.OnListPending(func() ([]PendingEntry, bool) { return nil, false })
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Stop()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	json.NewEncoder(conn).Encode(Request{Type: TypeListPending})
	var resp PendingResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.AuditEnabled {
		t.Error("expected audit_enabled=false when handler reports disabled")
	}
}

func TestListPendingNoHandler(t *testing.T) {
	d, socketPath := newTestDaemon(t)
	defer d.Stop()
	// No OnListPending registered — should still return an empty list, not escalate.

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	json.NewEncoder(conn).Encode(Request{Type: TypeListPending})
	var resp PendingResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Pending == nil || len(resp.Pending) != 0 {
		t.Errorf("expected empty (non-nil) slice, got %v", resp.Pending)
	}
}

func TestCompletePendingRoundTrip(t *testing.T) {
	dir := shortTempDir(t)
	socketPath := filepath.Join(dir, "d.sock")
	cfg := config.DefaultConfig()
	d := New(socketPath, cfg)

	var (
		gotHash, gotKey, gotDecision string
	)
	d.OnCompletePending(func(hash, key, decision string) error {
		gotHash, gotKey, gotDecision = hash, key, decision
		return nil
	})
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Stop()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	json.NewEncoder(conn).Encode(Request{
		Type:          TypeCompletePending,
		Key:           "k1",
		ProjectHash:   "h1",
		HumanDecision: "approved",
	})
	var resp CompleteResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Errorf("expected OK, got error %q", resp.Error)
	}
	if gotHash != "h1" || gotKey != "k1" || gotDecision != "approved" {
		t.Errorf("handler args mismatch: hash=%q key=%q decision=%q", gotHash, gotKey, gotDecision)
	}
}

func TestCompletePendingMissingFields(t *testing.T) {
	dir := shortTempDir(t)
	socketPath := filepath.Join(dir, "d.sock")
	cfg := config.DefaultConfig()
	d := New(socketPath, cfg)
	d.OnCompletePending(func(hash, key, decision string) error { return nil })
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Stop()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	json.NewEncoder(conn).Encode(Request{Type: TypeCompletePending, Key: "k1"})
	var resp CompleteResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.OK {
		t.Error("expected OK=false for missing fields")
	}
}

func TestCompletePendingNoHandler(t *testing.T) {
	d, socketPath := newTestDaemon(t)
	defer d.Stop()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	json.NewEncoder(conn).Encode(Request{
		Type:          TypeCompletePending,
		Key:           "k1",
		ProjectHash:   "h1",
		HumanDecision: "approved",
	})
	var resp CompleteResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.OK {
		t.Error("expected OK=false when no handler is registered")
	}
}

func TestGetConfigRoundTrip(t *testing.T) {
	dir := shortTempDir(t)
	socketPath := filepath.Join(dir, "d.sock")
	cfg := config.DefaultConfig()
	d := New(socketPath, cfg)
	d.OnGetConfig(func() ConfigResponse {
		return ConfigResponse{
			Endpoint:     "https://example.com/v1/chat",
			APIFormat:    "openai",
			Model:        "test-model",
			TimeoutMs:    5000,
			AuditEnabled: true,
		}
	})
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Stop()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	json.NewEncoder(conn).Encode(Request{Type: TypeGetConfig})
	var resp ConfigResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Endpoint != "https://example.com/v1/chat" || resp.Model != "test-model" || resp.TimeoutMs != 5000 || !resp.AuditEnabled {
		t.Errorf("config did not round-trip: %+v", resp)
	}
}

func TestGetConfigNoHandler(t *testing.T) {
	d, socketPath := newTestDaemon(t)
	defer d.Stop()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	json.NewEncoder(conn).Encode(Request{Type: TypeGetConfig})
	var resp ConfigResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	// Zero-value response is acceptable when no handler is registered.
	if resp.Endpoint != "" || resp.Model != "" || resp.TimeoutMs != 0 || resp.AuditEnabled {
		t.Errorf("expected zero-valued response, got %+v", resp)
	}
}
