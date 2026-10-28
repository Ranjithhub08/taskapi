// Package llm is a tiny client for any OpenAI-compatible chat API
// (xAI Grok, OpenAI, Gemini's compatibility endpoint, and others).
// The provider is chosen purely by environment variables, so switching
// providers never needs a code change.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Client holds the connection settings.
type Client struct {
	BaseURL string
	APIKey  string
	Model   string
	HTTP    *http.Client
}

// NewFromEnv builds a client from environment variables:
//
//	LLM_BASE_URL  default https://api.x.ai/v1
//	LLM_API_KEY   no default (if empty, the AI endpoint reports "not configured")
//	LLM_MODEL     default grok-4.3 (change it if your provider uses another name)
func NewFromEnv() *Client {
	return &Client{
		BaseURL: getenv("LLM_BASE_URL", "https://api.x.ai/v1"),
		APIKey:  os.Getenv("LLM_API_KEY"),
		Model:   getenv("LLM_MODEL", "grok-4.3"),
		HTTP:    &http.Client{Timeout: 60 * time.Second},
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// Enabled reports whether an API key is set.
func (c *Client) Enabled() bool { return c != nil && c.APIKey != "" }

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string    `json:"model"`
	Messages []message `json:"messages"`
}

type chatResponse struct {
	Choices []struct {
		Message message `json:"message"`
	} `json:"choices"`
}

// Chat sends one prompt and returns the model's text reply.
func (c *Client) Chat(ctx context.Context, prompt string) (string, error) {
	if !c.Enabled() {
		return "", errors.New("llm: LLM_API_KEY is not set")
	}

	body, err := json.Marshal(chatRequest{
		Model:    c.Model,
		Messages: []message{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return "", err
	}

	url := strings.TrimRight(c.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm: request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llm: provider returned status %d: %s", resp.StatusCode, truncate(string(data), 300))
	}

	var out chatResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("llm: bad response JSON: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", errors.New("llm: response had no choices")
	}
	return out.Choices[0].Message.Content, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
