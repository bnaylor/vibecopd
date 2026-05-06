package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Daemon.TimeoutMs != DefaultTimeoutMs {
		t.Errorf("expected timeout %d, got %d", DefaultTimeoutMs, cfg.Daemon.TimeoutMs)
	}
	if cfg.Model.APIFormat != DefaultAPIFormat {
		t.Errorf("expected api_format %q, got %q", DefaultAPIFormat, cfg.Model.APIFormat)
	}
}

func TestLoadMissingFile(t *testing.T) {
	cfg, err := Load("/nonexistent/path.toml")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Daemon.Enabled {
		t.Error("expected defaults when file missing")
	}
}

func TestLoadCustomFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	os.WriteFile(path, []byte(`
[daemon]
timeout_ms = 999
activity_window = 3

[model]
endpoint = "http://test:1234/v1"
api_format = "openai"
model = "some-model"
api_key = "secret"
`), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Daemon.TimeoutMs != 999 {
		t.Errorf("timeout_ms: got %d", cfg.Daemon.TimeoutMs)
	}
	if cfg.Daemon.ActivityWindow != 3 {
		t.Errorf("activity_window: got %d", cfg.Daemon.ActivityWindow)
	}
	if cfg.Model.Endpoint != "http://test:1234/v1" {
		t.Errorf("endpoint: got %q", cfg.Model.Endpoint)
	}
	if cfg.Model.APIFormat != "openai" {
		t.Errorf("api_format: got %q", cfg.Model.APIFormat)
	}
	if cfg.Model.Model != "some-model" {
		t.Errorf("model: got %q", cfg.Model.Model)
	}
	if cfg.Model.APIKey != "secret" {
		t.Errorf("api_key: got %q", cfg.Model.APIKey)
	}
}

func TestTelemetryDefaults(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Telemetry.Enabled {
		t.Error("telemetry should be disabled by default")
	}
	if cfg.Telemetry.ServiceName != DefaultServiceName {
		t.Errorf("service_name: got %q, want %q", cfg.Telemetry.ServiceName, DefaultServiceName)
	}
	if len(cfg.Telemetry.Targets) != 0 {
		t.Errorf("targets: got %d, want 0", len(cfg.Telemetry.Targets))
	}
}

func TestLoadTelemetryMultipleTargets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	os.WriteFile(path, []byte(`
[telemetry]
enabled = true
service_name = "vibecop-test"

[[telemetry.targets]]
endpoint = "localhost:4317"
protocol = "grpc"
insecure = true

[[telemetry.targets]]
endpoint = "https://collector.example.com:4318"
protocol = "http"
insecure = false
`), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Telemetry.Enabled {
		t.Error("expected telemetry enabled")
	}
	if cfg.Telemetry.ServiceName != "vibecop-test" {
		t.Errorf("service_name: got %q", cfg.Telemetry.ServiceName)
	}
	if len(cfg.Telemetry.Targets) != 2 {
		t.Fatalf("targets: got %d, want 2", len(cfg.Telemetry.Targets))
	}
	if cfg.Telemetry.Targets[0].Endpoint != "localhost:4317" {
		t.Errorf("targets[0] endpoint: got %q", cfg.Telemetry.Targets[0].Endpoint)
	}
	if cfg.Telemetry.Targets[0].Protocol != "grpc" || !cfg.Telemetry.Targets[0].Insecure {
		t.Errorf("targets[0]: protocol/insecure mismatch: %+v", cfg.Telemetry.Targets[0])
	}
	if cfg.Telemetry.Targets[1].Protocol != "http" || cfg.Telemetry.Targets[1].Insecure {
		t.Errorf("targets[1]: protocol/insecure mismatch: %+v", cfg.Telemetry.Targets[1])
	}
}

func TestProjectHash(t *testing.T) {
	h1 := ProjectHash("/some/path")
	h2 := ProjectHash("/some/path")
	h3 := ProjectHash("/some/other/path")
	if h1 != h2 {
		t.Error("same path should produce same hash")
	}
	if h1 == h3 {
		t.Error("different paths should produce different hashes")
	}
	if len(h1) != 64 {
		t.Errorf("expected 64-char hex sha256, got %d chars", len(h1))
	}
}

func TestProjectDir(t *testing.T) {
	dir, err := ProjectDir("abc123")
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Base(dir)
	if base != "abc123" {
		t.Errorf("expected .../projects/abc123, got %s", dir)
	}

	// Ensure ensure creates
	created, err := EnsureProjectDir("test-create")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(created); os.IsNotExist(err) {
		t.Error("EnsureProjectDir should create directory")
	}
	os.RemoveAll(created)
}

func TestPathHelpers(t *testing.T) {
	h := "deadbeef"
	sp, err := SystemPromptPath(h)
	if err != nil || filepath.Base(sp) != "system-prompt.md" {
		t.Errorf("system-prompt.md expected, got %s", sp)
	}
	al, err := ActivityLogPath(h)
	if err != nil || filepath.Base(al) != "activity.jsonl" {
		t.Errorf("activity.jsonl expected, got %s", al)
	}
	si, err := SkipInitPath(h)
	if err != nil || filepath.Base(si) != ".skip-init" {
		t.Errorf(".skip-init expected, got %s", si)
	}
	ad, err := AuditDir(h)
	if err != nil || filepath.Base(ad) != "audit" {
		t.Errorf("audit expected, got %s", ad)
	}
}
