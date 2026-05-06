package evaluator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// Verdict from the LLM evaluation.
type Verdict struct {
	Verdict string `json:"verdict"`
	Reason  string `json:"reason"`
}

// ToolRequest is the request body sent to the LLM.
type ToolRequest struct {
	Tool           string         `json:"tool"`
	Input          string         `json:"input"`
	RecentActivity []VerdictEntry `json:"recent_activity,omitempty"`
}

// VerdictEntry is a single entry in the recent activity window.
type VerdictEntry struct {
	Tool    string `json:"tool"`
	Verdict string `json:"verdict"`
}

const (
	apiFormatOpenAI    = "openai"
	apiFormatAnthropic = "anthropic"
	HarnessClaude      = "claude"
	HarnessGemini      = "gemini"
)

// Client evaluates tool-use requests by calling an LLM.
type Client struct {
	endpoint   string
	apiKey     string
	apiFormat  string
	model      string
	timeout    time.Duration
	httpClient *http.Client
	// ollamaCoT, when true, injects "think":false into OpenAI-format requests
	// to suppress chain-of-thought tokens on Ollama CoT models (qwen3, deepseek-r1, etc.).
	ollamaCoT bool
}

// Timeout returns the configured per-request timeout.
func (c *Client) Timeout() time.Duration { return c.timeout }

// isOllamaEndpoint returns true if the endpoint looks like a local Ollama instance.
func isOllamaEndpoint(endpoint string) bool {
	return strings.Contains(endpoint, "localhost") || strings.Contains(endpoint, "127.0.0.1")
}

// New creates an evaluator client from model configuration and timeout.
// For OpenAI-format requests to local (Ollama) endpoints, think:false is
// automatically injected to suppress CoT tokens on models that support it.
func New(endpoint, apiKey, apiFormat, model string, timeout time.Duration) *Client {
	return &Client{
		endpoint:   endpoint,
		apiKey:     apiKey,
		apiFormat:  apiFormat,
		model:      model,
		timeout:    timeout,
		httpClient: &http.Client{
			Timeout:   timeout,
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		},
		ollamaCoT:  apiFormat == apiFormatOpenAI && isOllamaEndpoint(endpoint),
	}
}

// Evaluate sends a tool-use request to the LLM and returns the verdict.
// The systemPrompt is the Guardian or Baseline prompt.
func (c *Client) Evaluate(ctx context.Context, req ToolRequest, systemPrompt string) (Verdict, error) {
	if c.endpoint == "" {
		return Verdict{}, fmt.Errorf("no endpoint configured")
	}

	var httpReq *http.Request
	var err error

	switch c.apiFormat {
	case apiFormatOpenAI:
		httpReq, err = c.buildOpenAIRequest(ctx, req, systemPrompt)
	case apiFormatAnthropic:
		httpReq, err = c.buildAnthropicRequest(ctx, req, systemPrompt)
	default:
		return Verdict{}, fmt.Errorf("unsupported api_format: %s", c.apiFormat)
	}
	if err != nil {
		return Verdict{}, fmt.Errorf("build request: %w", err)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return Verdict{}, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Verdict{}, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return Verdict{}, fmt.Errorf("api returned %d: %s", resp.StatusCode, string(body))
	}

	return c.parseResponse(body)
}

// buildOpenAIRequest constructs an OpenAI-compatible chat completions request.
func (c *Client) buildOpenAIRequest(ctx context.Context, treq ToolRequest, systemPrompt string) (*http.Request, error) {
	userContent, _ := json.Marshal(treq)

	body := map[string]any{
		"model": c.model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": string(userContent)},
		},
		"max_tokens": 256,
	}

	// Inject think:false only for local Ollama endpoints — suppresses CoT tokens.
	if c.ollamaCoT {
		body["think"] = false
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	hreq, err := http.NewRequestWithContext(ctx, "POST", c.endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Authorization", "Bearer "+c.apiKey)
	return hreq, nil
}

// buildAnthropicRequest constructs an Anthropic Messages API request.
func (c *Client) buildAnthropicRequest(ctx context.Context, treq ToolRequest, systemPrompt string) (*http.Request, error) {
	userContent, _ := json.Marshal(treq)

	body := map[string]any{
		"model":    c.model,
		"system":   systemPrompt,
		"messages": []map[string]any{
			{"role": "user", "content": string(userContent)},
		},
		"max_tokens": 256,
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	hreq, err := http.NewRequestWithContext(ctx, "POST", c.endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("x-api-key", c.apiKey)
	hreq.Header.Set("anthropic-version", "2023-06-01")
	return hreq, nil
}

// parseResponse extracts a verdict from the LLM response body.
func (c *Client) parseResponse(body []byte) (Verdict, error) {
	switch c.apiFormat {
	case apiFormatOpenAI:
		return parseOpenAIResponse(body)
	case apiFormatAnthropic:
		return parseAnthropicResponse(body)
	default:
		return Verdict{}, fmt.Errorf("unsupported api_format: %s", c.apiFormat)
	}
}

// openAIResponse is the expected shape of an OpenAI chat completion response.
type openAIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func parseOpenAIResponse(body []byte) (Verdict, error) {
	var resp openAIResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return Verdict{}, fmt.Errorf("parse openai response: %w", err)
	}
	if len(resp.Choices) == 0 {
		return Verdict{}, fmt.Errorf("openai response: no choices")
	}
	return extractVerdict([]byte(resp.Choices[0].Message.Content))
}

// anthropicResponse is the expected shape of an Anthropic Messages response.
type anthropicResponse struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
}

func parseAnthropicResponse(body []byte) (Verdict, error) {
	var resp anthropicResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return Verdict{}, fmt.Errorf("parse anthropic response: %w", err)
	}
	if len(resp.Content) == 0 {
		return Verdict{}, fmt.Errorf("anthropic response: no content blocks")
	}
	return extractVerdict([]byte(resp.Content[0].Text))
}

// extractVerdict extracts a Verdict from a byte slice that may contain
// markdown fences or surrounding prose.
func extractVerdict(data []byte) (Verdict, error) {
	// Strip markdown JSON fences if present.
	text := strings.TrimSpace(string(data))
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	text = strings.TrimSpace(text)

	var v Verdict
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		return Verdict{}, fmt.Errorf("parse verdict json: %w\nraw: %s", err, text)
	}

	switch v.Verdict {
	case "approve", "deny", "escalate":
		return v, nil
	default:
		return Verdict{}, fmt.Errorf("invalid verdict %q: must be approve, deny, or escalate", v.Verdict)
	}
}
