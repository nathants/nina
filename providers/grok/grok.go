// grok.go provides integration with X.AI's Grok models via their chat completions API
// supporting both regular messages and proper streaming with model grok-4-0709.

package grok

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	providers "github.com/nathants/nina/providers"
	"os"
)

func init() {
	providers.InitAllHTTPClients()
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Request struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Stream      bool      `json:"stream"`
	Temperature float64   `json:"temperature"`
}

type ChoiceMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Choice struct {
	Index        int           `json:"index"`
	Message      ChoiceMessage `json:"message"`
	FinishReason string        `json:"finish_reason"`
}

type Response struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   any      `json:"usage"`
}

func Handle(ctx context.Context, req Request) (string, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("grok: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		"https://api.x.ai/v1/chat/completions",
		bytes.NewBuffer(body),
	)
	if err != nil {
		return "", fmt.Errorf("grok: create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	apiKey := os.Getenv("XAI_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("GROK_TOKEN")
	}
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	cli := providers.LongTimeoutClient
	resp, err := cli.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("grok: do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("grok: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("grok: api error (status %d): %s", resp.StatusCode, string(rawBody))
	}

	var grokResp Response
	if err := json.Unmarshal(rawBody, &grokResp); err != nil {
		return "", fmt.Errorf("grok: unmarshal response: %w", err)
	}

	if len(grokResp.Choices) == 0 {
		return "", fmt.Errorf("grok: no choices in response")
	}

	return grokResp.Choices[0].Message.Content, nil
}
