package config

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// DaemonConfig controls daemon behaviour.
type DaemonConfig struct {
	Enabled        bool `toml:"enabled"`
	TimeoutMs      int  `toml:"timeout_ms"`
	ActivityWindow int  `toml:"activity_window"`
	AuditEnabled   bool `toml:"audit_enabled"`
}

// ModelConfig describes the LLM endpoint.
type ModelConfig struct {
	Endpoint  string `toml:"endpoint"`
	APIFormat string `toml:"api_format"`
	Model     string `toml:"model"`
	APIKey    string `toml:"api_key"`
}

// TelemetryTarget is a single OTLP collector destination.
//
// Protocol must be "grpc" or "http". When Insecure is true the exporter uses
// plaintext (no TLS) — appropriate for localhost collectors only.
type TelemetryTarget struct {
	Endpoint string `toml:"endpoint"`
	Protocol string `toml:"protocol"`
	Insecure bool   `toml:"insecure"`
}

// TelemetryConfig controls OTLP export. Telemetry is fail-open: zero targets
// (or any init failure) means no export, never a blocked permission check.
type TelemetryConfig struct {
	Enabled     bool              `toml:"enabled"`
	ServiceName string            `toml:"service_name"`
	Targets     []TelemetryTarget `toml:"targets"`
}

// Config is the top-level vibecop configuration.
type Config struct {
	Daemon    DaemonConfig    `toml:"daemon"`
	Model     ModelConfig     `toml:"model"`
	Telemetry TelemetryConfig `toml:"telemetry"`
}

const (
	DefaultTimeoutMs       = 5000
	DefaultActivityWindow  = 10
	DefaultAPIFormat       = "anthropic"
	DefaultModel           = "claude-haiku-4-5"
	DefaultServiceName     = "vibecopd"
	DefaultTelemetryProto  = "grpc"

	vibecopDir  = ".vibecop"
	projectsDir = "projects"
	auditDir    = "audit"
)

func DefaultConfig() Config {
	return Config{
		Daemon: DaemonConfig{
			Enabled:        true,
			TimeoutMs:      DefaultTimeoutMs,
			ActivityWindow: DefaultActivityWindow,
			AuditEnabled:   false,
		},
		Model: ModelConfig{
			Endpoint:  "",
			APIFormat: DefaultAPIFormat,
			Model:     DefaultModel,
			APIKey:    "",
		},
		Telemetry: TelemetryConfig{
			Enabled:     false,
			ServiceName: DefaultServiceName,
			Targets:     nil,
		},
	}
}

// Load reads a TOML config file, merging with defaults.
func Load(path string) (Config, error) {
	cfg := DefaultConfig()

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()

	if _, err := toml.NewDecoder(f).Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("decode config: %w", err)
	}
	return cfg, nil
}

// DefaultConfigPath returns ~/.vibecop/config.toml.
func DefaultConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	return filepath.Join(home, vibecopDir, "config.toml"), nil
}

// VibecopDir returns ~/.vibecop.
func VibecopDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	return filepath.Join(home, vibecopDir), nil
}

// ProjectHash returns the hex-encoded SHA-256 of the absolute project path.
func ProjectHash(projectPath string) string {
	abs, err := filepath.Abs(projectPath)
	if err != nil {
		abs = projectPath
	}
	h := sha256.Sum256([]byte(abs))
	return fmt.Sprintf("%x", h)
}

// ProjectDir returns the per-project storage directory for the given hash.
func ProjectDir(projectHash string) (string, error) {
	vd, err := VibecopDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(vd, projectsDir, projectHash), nil
}

// ProjectDirForPath resolves an absolute project path to its storage directory.
func ProjectDirForPath(projectPath string) (string, error) {
	return ProjectDir(ProjectHash(projectPath))
}

// SystemPromptPath returns the path to the Guardian system prompt file.
func SystemPromptPath(projectHash string) (string, error) {
	pd, err := ProjectDir(projectHash)
	if err != nil {
		return "", err
	}
	return filepath.Join(pd, "system-prompt.md"), nil
}

// ActivityLogPath returns the path to the rolling activity log.
func ActivityLogPath(projectHash string) (string, error) {
	pd, err := ProjectDir(projectHash)
	if err != nil {
		return "", err
	}
	return filepath.Join(pd, "activity.jsonl"), nil
}

// SkipInitPath returns the path to the .skip-init marker file.
func SkipInitPath(projectHash string) (string, error) {
	pd, err := ProjectDir(projectHash)
	if err != nil {
		return "", err
	}
	return filepath.Join(pd, ".skip-init"), nil
}

// AuditDir returns the per-project audit log directory.
func AuditDir(projectHash string) (string, error) {
	pd, err := ProjectDir(projectHash)
	if err != nil {
		return "", err
	}
	return filepath.Join(pd, auditDir), nil
}

// EnsureProjectDir creates the per-project storage directory and returns its path.
func EnsureProjectDir(projectHash string) (string, error) {
	pd, err := ProjectDir(projectHash)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(pd, 0755); err != nil {
		return "", fmt.Errorf("create project dir: %w", err)
	}
	return pd, nil
}
