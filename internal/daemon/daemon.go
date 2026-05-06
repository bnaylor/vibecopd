package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/bnaylor/vibecop/internal/config"
)

// Message types.
const (
	TypePermissionRequest = "permission_request"
	TypeTUISubscribe      = "tui_subscribe"
)

// Request from a hook or TUI client.
type Request struct {
	Type        string `json:"type"`
	ProjectPath string `json:"project_path,omitempty"`
	Tool        string `json:"tool,omitempty"`
	Input       string `json:"input,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
}

// Verdict returned to a hook.
type Verdict struct {
	Verdict string `json:"verdict"`
	Reason  string `json:"reason,omitempty"`
}

// Event streamed to TUI subscribers.
type Event struct {
	Tool      string `json:"tool,omitempty"`
	Input     string `json:"input,omitempty"`
	Verdict   string `json:"verdict,omitempty"`
	Reason    string `json:"reason,omitempty"`
	LatencyMs int64  `json:"latency_ms,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
	Level     string `json:"level,omitempty"` // "info", "warn", "error"
	Message   string `json:"message,omitempty"`
}

// permissionHandler is called when a permission_request arrives.
type permissionHandler func(req Request) Verdict

// Daemon is the UDS-based IPC server.
type Daemon struct {
	socketPath  string
	cfg         config.Config
	listener    net.Listener
	evtCh       chan Event
	subs        map[chan Event]struct{}
	subsMu      sync.Mutex
	otlpCh      chan Event
	onPerm      permissionHandler
	wg          sync.WaitGroup
	quit        chan struct{}
	stopOnce    sync.Once
	shutdownErr error
	shutdownMu  sync.Mutex
}

// New creates a new daemon, but does not start it.
func New(socketPath string, cfg config.Config) *Daemon {
	return &Daemon{
		socketPath: socketPath,
		cfg:        cfg,
		evtCh:      make(chan Event, 64),
		subs:       make(map[chan Event]struct{}),
		quit:       make(chan struct{}),
	}
}

// OnPermission registers the handler for permission_request messages.
func (d *Daemon) OnPermission(h permissionHandler) { d.onPerm = h }

// RegisterOTLPSubscriber returns a buffered channel that receives every
// daemon event for OTLP export. Peers with TUI subscribers in
// broadcastEvents — same drop-on-full backpressure. The channel is closed
// during daemon shutdown. Idempotent: subsequent calls return the same
// channel. Must be called before Start (the broadcaster captures d.otlpCh
// once).
func (d *Daemon) RegisterOTLPSubscriber() <-chan Event {
	if d.otlpCh == nil {
		d.otlpCh = make(chan Event, 64)
	}
	return d.otlpCh
}

// Start binds the socket and begins accepting connections.
func (d *Daemon) Start() error {
	// Remove stale socket.
	if err := os.Remove(d.socketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale socket: %w", err)
	}

	// Ensure parent directory exists.
	if err := os.MkdirAll(filepath.Dir(d.socketPath), 0755); err != nil {
		return fmt.Errorf("create socket dir: %w", err)
	}

	var err error
	d.listener, err = net.Listen("unix", d.socketPath)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	// Write PID file.
	pidPath := d.pidPath()
	if err := os.MkdirAll(filepath.Dir(pidPath), 0755); err != nil {
		d.listener.Close()
		return fmt.Errorf("create pid dir: %w", err)
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())+"\n"), 0644); err != nil {
		d.listener.Close()
		return fmt.Errorf("write pid: %w", err)
	}

	log.Printf("daemon: listening on %s (pid %d)", d.socketPath, os.Getpid())

	// Start the event broadcaster.
	go d.broadcastEvents()

	// Accept loop.
	go func() {
		for {
			conn, err := d.listener.Accept()
			if err != nil {
				select {
				case <-d.quit:
					return
				default:
					log.Printf("daemon: accept error: %v", err)
					continue
				}
			}
			d.wg.Add(1)
			go d.handleConn(conn)
		}
	}()

	return nil
}

// Run starts the accept loop and blocks until Stop is called.
func (d *Daemon) Run() error {
	if d.listener == nil {
		return fmt.Errorf("daemon not started")
	}

	// Handle OS signals for graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Wait for shutdown signal.
	select {
	case sig := <-sigCh:
		log.Printf("daemon: received signal %v", sig)
	case <-d.quit:
	}
	return d.shutdown()
}

// Stop signals the daemon to shut down. Idempotent.
func (d *Daemon) Stop() error {
	d.stopOnce.Do(func() {
		close(d.quit)
	})
	return d.shutdown()
}

func (d *Daemon) shutdown() error {
	d.shutdownMu.Lock()
	defer d.shutdownMu.Unlock()

	// Only run once.
	if d.listener == nil && d.evtCh == nil {
		return d.shutdownErr
	}

	// Stop accepting.
	if d.listener != nil {
		d.listener.Close()
		d.listener = nil
	}

	// Close all subscriber channels so TUI handler goroutines can exit.
	d.subsMu.Lock()
	for ch := range d.subs {
		close(ch)
	}
	d.subs = nil
	d.subsMu.Unlock()

	// Close event channel so broadcaster exits.
	close(d.evtCh)
	d.evtCh = nil

	// Wait for in-flight connections (now unblocked).
	d.wg.Wait()

	// Remove socket.
	if err := os.Remove(d.socketPath); err != nil && !os.IsNotExist(err) {
		d.shutdownErr = fmt.Errorf("remove socket: %w", err)
		return d.shutdownErr
	}
	// Remove PID file.
	if err := os.Remove(d.pidPath()); err != nil && !os.IsNotExist(err) {
		d.shutdownErr = fmt.Errorf("remove pid: %w", err)
		return d.shutdownErr
	}
	return nil
}

func (d *Daemon) handleConn(conn net.Conn) {
	defer d.wg.Done()
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	// Increase token limit for potentially large inputs.
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	if !scanner.Scan() {
		return
	}

	var req Request
	if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
		log.Printf("daemon: invalid request: %v", err)
		json.NewEncoder(conn).Encode(Verdict{
			Verdict: "escalate",
			Reason:  "VibeCop: failed to parse request",
		})
		return
	}

	switch req.Type {
	case TypePermissionRequest:
		handlePermission(conn, req, d.onPerm)
	case TypeTUISubscribe:
		handleTUISubscribe(conn, d)
	default:
		log.Printf("daemon: unknown request type: %s", req.Type)
		json.NewEncoder(conn).Encode(Verdict{
			Verdict: "escalate",
			Reason:  "VibeCop: unknown request type",
		})
	}
}

func handlePermission(conn net.Conn, req Request, handler permissionHandler) {
	if handler != nil {
		v := handler(req)
		json.NewEncoder(conn).Encode(v)
	} else {
		// No handler registered — fall through to human.
		json.NewEncoder(conn).Encode(Verdict{
			Verdict: "escalate",
			Reason:  "VibeCop: evaluator not available",
		})
	}
}

func handleTUISubscribe(conn net.Conn, d *Daemon) {
	ch := make(chan Event, 64)

	d.subsMu.Lock()
	if d.subs == nil {
		d.subsMu.Unlock()
		return // daemon shutting down
	}
	d.subs[ch] = struct{}{}
	d.subsMu.Unlock()

	// Send events until connection dies.
	for evt := range ch {
		data, err := json.Marshal(evt)
		if err != nil {
			continue
		}
		data = append(data, '\n')
		if _, err := conn.Write(data); err != nil {
			break
		}
	}

	// Cleanup.
	d.subsMu.Lock()
	delete(d.subs, ch)
	d.subsMu.Unlock()
}

func (d *Daemon) broadcastEvents() {
	for evt := range d.evtCh {
		d.subsMu.Lock()
		for ch := range d.subs {
			select {
			case ch <- evt:
			default:
				// Drop for slow subscribers.
			}
		}
		d.subsMu.Unlock()

		if d.otlpCh != nil {
			select {
			case d.otlpCh <- evt:
			default:
				// Drop for slow OTLP exporter — fail-open.
			}
		}
	}
	if d.otlpCh != nil {
		close(d.otlpCh)
		d.otlpCh = nil
	}
}

// EmitEvent sends an event to all TUI subscribers.
func (d *Daemon) EmitEvent(evt Event) {
	select {
	case d.evtCh <- evt:
	default:
	}
}

// SocketPath returns the daemon's socket path.
func (d *Daemon) SocketPath() string { return d.socketPath }

// pidPath derives the PID file path from the socket path.
func (d *Daemon) pidPath() string {
	return PIDPath(d.socketPath)
}

// DefaultSocketPath returns the default UDS path.
func DefaultSocketPath(vibecopDir string) string {
	return filepath.Join(vibecopDir, "daemon.sock")
}

// PIDPath returns the PID file path for a given socket path.
func PIDPath(socketPath string) string {
	return filepath.Join(filepath.Dir(socketPath), "daemon.pid")
}

// ReadPID reads the PID from the PID file alongside the given socket path.
func ReadPID(socketPath string) (int, error) {
	data, err := os.ReadFile(PIDPath(socketPath))
	if err != nil {
		return 0, fmt.Errorf("read pid: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("parse pid: %w", err)
	}
	return pid, nil
}

// ProcessExists checks whether a process with the given PID is running.
func ProcessExists(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds; signal 0 checks liveness.
	return p.Signal(syscall.Signal(0)) == nil
}
