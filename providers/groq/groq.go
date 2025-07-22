// Groq provider for fast LLM inference with OpenAI-compatible API including
// support for reasoning models like moonshotai/kimi-k2-instruct.
package groq

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	providers "github.com/nathants/nina/providers"
	"os"
	"strings"
)

const apiURL = "https://api.groq.com/openai/v1/chat/completions"

func init() {
	providers.InitAllHTTPClients()
}

// getAuthToken retrieves the Groq API key from environment.
func getAuthToken() string {
	apiKey := os.Getenv("GROQ_API_KEY")
	if apiKey == "" {
		// Fallback to old name for backward compatibility
		apiKey = os.Getenv("GROQ_KEY")
	}
	return apiKey
}

// Message represents a single chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Request represents the request body for Groq API.
type Request struct {
	Model            string    `json:"model"`
	Messages         []Message `json:"messages"`
	Temperature      *float64  `json:"temperature,omitempty"`
	MaxTokens        *int      `json:"max_tokens,omitempty"`
	TopP             *float64  `json:"top_p,omitempty"`
	FrequencyPenalty *float64  `json:"frequency_penalty,omitempty"`
	PresencePenalty  *float64  `json:"presence_penalty,omitempty"`
	N                *int      `json:"n,omitempty"`
	Stream           bool      `json:"stream"`
	Stop             []string  `json:"stop,omitempty"`
	Seed             *int      `json:"seed,omitempty"`
	User             string    `json:"user,omitempty"`
	ResponseFormat   *Format   `json:"response_format,omitempty"`
	ReasoningFormat  string    `json:"reasoning_format,omitempty"`
	ReasoningEffort  string    `json:"reasoning_effort,omitempty"`
}

// Format represents response format options.
type Format struct {
	Type string `json:"type"` // "text" or "json_object"
}

// Choice represents a single response choice.
type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

// Usage represents token usage statistics.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Response represents the API response.
type Response struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

// StreamChoice represents a streaming response choice.
type StreamChoice struct {
	Index        int         `json:"index"`
	Delta        StreamDelta `json:"delta"`
	FinishReason *string     `json:"finish_reason"`
}

// StreamDelta represents the delta content in streaming responses.
type StreamDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

// StreamResponse represents a streaming API response chunk.
type StreamResponse struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []StreamChoice `json:"choices"`
}

// ErrorResponse represents API error response structure.
type ErrorResponse struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

// HandleResponse holds the response data from Handle function.
type HandleResponse struct {
	Text  string
	Usage *Usage
}

// Handle sends a request to Groq API and returns the response.
func Handle(ctx context.Context, req Request) (*HandleResponse, error) {

	if req.Temperature == nil {
		temp := 0.6
		req.Temperature = &temp
	}

	authToken := getAuthToken()
	if authToken == "" {
		return nil, fmt.Errorf("GROQ_API_KEY not set")
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("json marshal error: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		apiURL,
		bytes.NewBuffer(body),
	)
	if err != nil {
		return nil, fmt.Errorf("request creation error: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+authToken)
	if req.Stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	}

	client := providers.LongTimeoutClient
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("do request error: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		resBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("API error: status %s, failed to read body: %w", resp.Status, err)
		}

		var errResp ErrorResponse
		if err := json.Unmarshal(resBody, &errResp); err == nil && errResp.Error.Message != "" {
			return nil, fmt.Errorf("API error: status %s, %s: %s", resp.Status, errResp.Error.Type, errResp.Error.Message)
		}

		return nil, fmt.Errorf("API error: status %s, body: %s", resp.Status, string(resBody))
	}

	if !req.Stream {
		// Handle non-streaming response
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, err
		}

		var response Response
		err = json.Unmarshal(data, &response)
		if err != nil {
			return nil, fmt.Errorf("unmarshal error: %w", err)
		}

		if len(response.Choices) == 0 {
			return nil, fmt.Errorf("no choices in response")
		}

		return &HandleResponse{
			Text:  response.Choices[0].Message.Content,
			Usage: &response.Usage,
		}, nil
	}

	// Handle streaming response
	var textBuilder strings.Builder
	reader := bufio.NewReader(resp.Body)

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("stream read error: %w", err)
		}

		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var streamResp StreamResponse
		if err := json.Unmarshal([]byte(data), &streamResp); err != nil {
			return nil, fmt.Errorf("unmarshal stream error: %w", err)
		}

		if len(streamResp.Choices) > 0 && streamResp.Choices[0].Delta.Content != "" {
			textBuilder.WriteString(streamResp.Choices[0].Delta.Content)
		}
	}

	return &HandleResponse{
		Text:  textBuilder.String(),
		Usage: nil, // Groq doesn't provide usage in streaming mode
	}, nil
}
