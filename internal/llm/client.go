// Package llm provides a shared client for OpenAI Chat Completions API.
// Governing: ADR-0008 (OpenAI API choice), issue #119
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ChatMessage represents a single message in a chat completion request.
type ChatMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // string or []ContentPart for multi-modal
}

// ContentPart is used for multi-modal (text + image) messages.
type ContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

// ImageURL holds an image URL for vision requests.
type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// ResponseFormat specifies the format of the response from OpenAI.
type ResponseFormat struct {
	Type string `json:"type"`
}

// ChatRequest is the payload for POST /v1/chat/completions.
type ChatRequest struct {
	Model          string          `json:"model"`
	Messages       []ChatMessage   `json:"messages"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	Temperature    float64         `json:"temperature,omitempty"`
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
}

// ChatChoice is one completion choice in the response.
type ChatChoice struct {
	Index   int `json:"index"`
	Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
	FinishReason string `json:"finish_reason"`
}

// ChatUsage contains token usage information from the API response.
type ChatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ChatError is the error object returned by the OpenAI API on failure.
type ChatError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

// ChatResponse is the response from POST /v1/chat/completions.
type ChatResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []ChatChoice `json:"choices"`
	Usage   *ChatUsage   `json:"usage"`
	Error   *ChatError   `json:"error,omitempty"`
}

// ClientConfig holds the configuration needed to create a Client.
type ClientConfig struct {
	APIKey  string
	BaseURL string // Base URL including /v1 path (e.g., "https://api.openai.com/v1")
	Timeout time.Duration
}

// Client is a minimal OpenAI Chat Completions client.
type Client struct {
	cfg        ClientConfig
	httpClient *http.Client
}

// NewClient creates a new LLM client using the given config.
func NewClient(cfg ClientConfig) *Client {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	baseURL = strings.TrimSuffix(baseURL, "/")

	return &Client{
		cfg: ClientConfig{
			APIKey:  cfg.APIKey,
			BaseURL: baseURL,
			Timeout: timeout,
		},
		httpClient: &http.Client{Timeout: timeout},
	}
}

// Chat sends a chat completion request and returns the response.
func (c *Client) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if chatResp.Error != nil {
		return nil, fmt.Errorf("API error: %s", chatResp.Error.Message)
	}

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	return &chatResp, nil
}
