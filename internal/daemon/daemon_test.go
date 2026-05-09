package daemon

import (
	"encoding/json"
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

func TestRequestHarnessAndHookEventRoundTrip(t *testing.T) {
	dir := shortTempDir(t)
	socketPath := filepath.Join(dir, "d.sock")
	cfg := config.DefaultConfig()
	d := New(socketPath, cfg)

	got := make(chan Request, 1)
	d.OnPermission(func(req Request) Verdict {
		got <- req
		return Verdict{Verdict: "approve"}
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
		Type:      TypePermissionRequest,
		Tool:      "Bash",
		Input:     "go test",
		Harness:   "codex",
		HookEvent: "PermissionRequest",
	})

	var resp Verdict
	json.NewDecoder(conn).Decode(&resp)

	select {
	case r := <-got:
		if r.Harness != "codex" {
			t.Errorf("Harness = %q, want codex", r.Harness)
		}
		if r.HookEvent != "PermissionRequest" {
			t.Errorf("HookEvent = %q, want PermissionRequest", r.HookEvent)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler not invoked")
	}
}

func TestEventHarnessAndHookEventRoundTrip(t *testing.T) {
	d, socketPath := newTestDaemon(t)
	defer d.Stop()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	json.NewEncoder(conn).Encode(Request{Type: TypeTUISubscribe})
	time.Sleep(50 * time.Millisecond)

	d.EmitEvent(Event{
		Tool:      "Bash",
		Verdict:   "approve",
		Harness:   "claude",
		HookEvent: "PreToolUse",
	})

	var evt Event
	if err := json.NewDecoder(conn).Decode(&evt); err != nil {
		t.Fatal(err)
	}
	if evt.Harness != "claude" || evt.HookEvent != "PreToolUse" {
		t.Errorf("Event lost harness fields: %#v", evt)
	}
}
