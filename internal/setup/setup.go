package setup

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Provider choices.
const (
	ProviderAnthropic = "1"
	ProviderOpenAI    = "2"
	ProviderOllama    = "3"
)

// Result is the outcome of a successful setup wizard run.
type Result struct {
	ConfigPath string
	ConfigData string
}

// Run starts the interactive setup wizard.
// It returns the path and content of the generated config file.
func Run() (*Result, error) {
	r := bufio.NewReader(os.Stdin)

	fmt.Println()
	boldLine("vibecop setup")
	fmt.Println()
	fmt.Println("No configuration found. Let's get you set up.")
	fmt.Println()

	// ---- Step 1: Provider ----
	fmt.Println("1. LLM Provider")
	fmt.Println("   Which LLM should vibecop use for second opinions?")
	fmt.Println()
	fmt.Println("   [1] Anthropic API (claude-haiku-4-5)")
	fmt.Println("   [2] OpenAI-compatible (any provider)")
	fmt.Println("   [3] Ollama (local)")
	fmt.Print("   Choice [1]: ")

	provider, _ := r.ReadString('\n')
	provider = strings.TrimSpace(provider)
	if provider == "" {
		provider = ProviderAnthropic
	}

	endpoint := ""
	apiFormat := ""
	model := ""
	apiKey := ""
	promptForAPIKey := false

	switch provider {
	case ProviderAnthropic:
		apiFormat = "anthropic"
		endpoint = promptDefault(r, "   Endpoint", "https://api.anthropic.com/v1/messages")
		model = promptDefault(r, "   Model", "claude-haiku-4-5")
		promptForAPIKey = true

	case ProviderOpenAI:
		apiFormat = "openai"
		endpoint = promptRequired(r, "   Endpoint URL", "https://api.openai.com/v1/chat/completions")
		model = promptRequired(r, "   Model name", "gpt-4o-mini")
		fmt.Print("   API key (leave blank if not needed): ")
		key, _ := r.ReadString('\n')
		apiKey = strings.TrimSpace(key)

	case ProviderOllama:
		apiFormat = "openai"
		endpoint = promptDefault(r, "   Ollama endpoint", "http://localhost:11434/v1/chat/completions")
		fmt.Println()
		fmt.Println("   Detecting Ollama models...")
		models := detectOllamaModels(strings.TrimSuffix(endpoint, "/v1/chat/completions"))
		if len(models) > 0 {
			fmt.Println("   Available models:")
			for i, m := range models {
				fmt.Printf("   [%d] %s\n", i+1, m)
			}
			fmt.Print("   Choice [1]: ")
			choice, _ := r.ReadString('\n')
			choice = strings.TrimSpace(choice)
			idx := 0
			if choice != "" {
				idx, _ = strconv.Atoi(choice)
				idx--
			}
			if idx >= 0 && idx < len(models) {
				model = models[idx]
			}
		}
		if model == "" {
			model = promptRequired(r, "   Model name", "qwen3:14b")
		}
		promptForAPIKey = false

	default:
		return nil, fmt.Errorf("invalid choice: %s", provider)
	}

	if promptForAPIKey {
		fmt.Print("   API key: ")
		key, _ := r.ReadString('\n')
		apiKey = strings.TrimSpace(key)
	}

	// ---- Step 2: Timeout ----
	fmt.Println()
	timeoutStr := promptDefault(r, "2. Timeout (ms)", "5000")
	timeout, _ := strconv.Atoi(timeoutStr)
	if timeout <= 0 {
		timeout = 5000
	}

	// ---- Step 3: Activity window ----
	windowStr := promptDefault(r, "3. Activity window", "10")
	window, _ := strconv.Atoi(windowStr)
	if window <= 0 {
		window = 10
	}

	// ---- Write config ----
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("home dir: %w", err)
	}

	configDir := filepath.Join(home, ".vibecop")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}

	configData := fmt.Sprintf(`# vibecop configuration
# See https://github.com/bnaylor/vibecop for documentation.

[daemon]
enabled        = true
timeout_ms     = %d
activity_window = %d
audit_enabled  = false

[model]
endpoint   = %q
api_format = %q
model      = %q
`, timeout, window, endpoint, apiFormat, model)

	if apiKey != "" {
		configData += fmt.Sprintf("api_key    = %q\n", apiKey)
	}

	configPath := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configPath, []byte(configData), 0600); err != nil {
		return nil, fmt.Errorf("write config: %w", err)
	}

	fmt.Println()
	fmt.Println("Configuration saved to", configPath)
	fmt.Println()

	return &Result{
		ConfigPath: configPath,
		ConfigData: configData,
	}, nil
}

func promptDefault(r *bufio.Reader, label, def string) string {
	fmt.Printf("   %s [%s]: ", label, def)
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

func promptRequired(r *bufio.Reader, label, hint string) string {
	for {
		fmt.Printf("   %s: ", label)
		line, _ := r.ReadString('\n')
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
		fmt.Printf("   (required — e.g. %s)\n", hint)
	}
}

func detectOllamaModels(baseURL string) []string {
	// Best-effort: check the tags endpoint.
	// If it fails, return empty — user enters manually.
	resp, err := fetch(baseURL + "/api/tags")
	if err != nil {
		return nil
	}

	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := parseJSON(resp, &result); err != nil {
		return nil
	}

	var names []string
	for _, m := range result.Models {
		names = append(names, m.Name)
	}
	return names
}

func boldLine(text string) {
	fmt.Printf("=== %s ===\n", text)
}

// IsFirstRun returns true if no config.toml exists yet.
func IsFirstRun() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	path := filepath.Join(home, ".vibecop", "config.toml")
	_, err = os.Stat(path)
	return os.IsNotExist(err)
}

// ConfigExists returns the path to the config file if it exists.
func ConfigPath() (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	path := filepath.Join(home, ".vibecop", "config.toml")
	_, err = os.Stat(path)
	if os.IsNotExist(err) {
		return path, false
	}
	return path, true
}

// ShowConfig prints the current config to stderr.
func ShowConfig(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, string(data))
	return nil
}

// ValidateConfig checks if the config has a usable endpoint.
func ValidateConfig(path string) error {
	var raw struct {
		Model struct {
			Endpoint string `toml:"endpoint"`
		} `toml:"model"`
	}
	if _, err := toml.DecodeFile(path, &raw); err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	if raw.Model.Endpoint == "" {
		return fmt.Errorf("no endpoint configured — run 'vibecop setup'")
	}
	return nil
}
