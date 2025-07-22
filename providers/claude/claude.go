package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/nathants/nina/providers"
	util "github.com/nathants/nina/util"
)

func init() {
	providers.InitAllHTTPClients()
}

type ImageSource struct {
	Type string `json:"type"` // must be "url"
	URL  string `json:"url"`
}

type ImageBlock struct {
	Type   string      `json:"type"` // must be "image"
	Source ImageSource `json:"source"`
}

type CacheControl struct {
	Type string `json:"type"`
}

type Text struct {
	Type  string        `json:"type"`
	Text  string        `json:"text"`
	Cache *CacheControl `json:"cache_control,omitempty"`
}

// Message represents a single chat message for the Anthropic API.
type Message struct {
	Role    string `json:"role"`
	Content []Text `json:"content"`
}

type Thinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

type Request struct {
	Model     string    `json:"model"`
	System    []Text    `json:"system"`
	Messages  []Message `json:"messages"`
	MaxTokens int       `json:"max_tokens,omitempty"`
	Thinking  *Thinking `json:"thinking,omitempty"`
	Stream    bool      `json:"stream,omitempty"`
}

// ContentBlock is a single "content" element in the response.
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// Usage represents token usage information from the Claude API
type Usage struct {
	InputTokens      int `json:"input_tokens"`
	OutputTokens     int `json:"output_tokens"`
	CacheReadTokens  int `json:"cache_read_input_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_write_input_tokens,omitempty"`
}

// Response is the top level response type returned by Anthropic.
type Response struct {
	Content []ContentBlock `json:"content"`
	Usage   Usage          `json:"usage"`
}

// HandleResponse holds the response data from the Handle function including
// the response text, usage statistics, and message ID for conversation state.
type HandleResponse struct {
	Text      string
	Usage     Usage
	MessageID string
}

// Batch API types
type BatchParams struct {
	Model     string    `json:"model"`
	System    string    `json:"system,omitempty"`
	Messages  []Message `json:"messages"`
	MaxTokens int       `json:"max_tokens,omitempty"`
	Thinking  *Thinking `json:"thinking,omitempty"`
	UseOAuth  bool      `json:"-"` // Not part of API request, just for auth selection
}

type BatchRequestItem struct {
	CustomID string      `json:"custom_id"`
	Params   BatchParams `json:"params"`
}

type BatchCreateRequest struct {
	Requests []BatchRequestItem `json:"requests"`
}

type BatchRequestCounts struct {
	Processing int `json:"processing"`
	Succeeded  int `json:"succeeded"`
	Errored    int `json:"errored"`
	Canceled   int `json:"canceled"`
	Expired    int `json:"expired"`
}

type BatchResponse struct {
	ID                string             `json:"id"`
	Type              string             `json:"type"`
	ProcessingStatus  string             `json:"processing_status"`
	RequestCounts     BatchRequestCounts `json:"request_counts"`
	EndedAt           *string            `json:"ended_at"`
	CreatedAt         string             `json:"created_at"`
	ExpiresAt         string             `json:"expires_at"`
	CancelInitiatedAt *string            `json:"cancel_initiated_at"`
	ResultsURL        *string            `json:"results_url"`
}

type BatchResultMessage struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	Role         string         `json:"role"`
	Model        string         `json:"model"`
	Content      []ContentBlock `json:"content"`
	StopReason   string         `json:"stop_reason"`
	StopSequence *string        `json:"stop_sequence"`
	Usage        Usage          `json:"usage"`
}

type BatchResult struct {
	Type    string              `json:"type"`
	Message *BatchResultMessage `json:"message,omitempty"`
	Error   map[string]any      `json:"error,omitempty"`
}

type BatchIndividualResult struct {
	CustomID string      `json:"custom_id"`
	Result   BatchResult `json:"result"`
}

// GetClaudeVersion returns the Claude CLI version, checking cache first
func GetClaudeVersion() string {
	versionFile := "/tmp/claude-version"

	// Check if file exists and is less than 24 hours old
	if fileInfo, err := os.Stat(versionFile); err == nil {
		if time.Since(fileInfo.ModTime()) < 24*time.Hour {
			// Read and return cached version
			if content, err := os.ReadFile(versionFile); err == nil {
				return strings.TrimSpace(string(content))
			}
		}
	}

	// Fetch new version
	cmd := exec.Command("npm", "view", "@anthropic-ai/claude-code", "version")
	output, err := cmd.Output()
	if err != nil {
		panic(err)
	}

	version := strings.TrimSpace(string(output))

	err = os.WriteFile(versionFile, []byte(version), 0644)
	if err != nil {
		panic(err)
	}

	return version
}

var logOnce bool

// setupClaudeAuth configures authentication headers for Claude API requests
func setupClaudeAuth(req *http.Request, useOAuth bool) {
	if useOAuth {
		// Check for OAuth token in environment
		oauthToken := os.Getenv("ANTHROPIC_OAUTH_TOKEN")
		if oauthToken != "" {
			req.Header.Set("Authorization", "Bearer "+oauthToken)
			req.Header.Set("anthropic-beta", "oauth-2025-04-20")
			req.Header.Set("User-Agent", fmt.Sprintf("claude-cli/%s (external, cli)", GetClaudeVersion()))
			if !logOnce {
				logOnce = true
				_, _ = fmt.Fprintln(os.Stderr, "Using OAuth authentication for Claude API")
			}
		} else {

			// Fall back to API key if OAuth token not available
			req.Header.Set("x-api-key", os.Getenv("CLAUDE_KEY"))
			if !logOnce {
				logOnce = true
				_, _ = fmt.Fprintf(os.Stderr, "OAuth requested but token not found, falling back to API key authentication\n")
			}
		}
	} else {
		// Use API key authentication
		req.Header.Set("x-api-key", os.Getenv("CLAUDE_KEY"))
		if !logOnce {
			logOnce = true
			_, _ = fmt.Fprintln(os.Stderr, "Using API key authentication for Claude API")
		}
	}
}

var ClaudeCode = "You are Claude Code, Anthropic's official CLI for Claude."
var logModelOnce sync.Once

func Handle(ctx context.Context, req Request, reasoningCallback func(data string)) (*HandleResponse, error) {
	logModelOnce.Do(func() {
		// fmt.Fprintln(os.Stderr, "model:", req.Model)
	})

	useOAuth := os.Getenv("ANTHROPIC_OAUTH_TOKEN") != ""

	if useOAuth {
		system := req.System
		req.System = append([]Text{}, Text{
			Type:  "text",
			Text:  ClaudeCode,
			Cache: &CacheControl{Type: "ephemeral"},
		})
		req.System = append(req.System, system...)
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("json marshal error: %v", err)
	}

	// fmt.Println(lib.Pformat(req))

	outReq, err := http.NewRequestWithContext(
		ctx,
		"POST",
		"https://api.anthropic.com/v1/messages",
		bytes.NewBuffer(body),
	)
	if err != nil {
		return nil, fmt.Errorf("request creation error: %v", err)
	}

	outReq.Header.Set("Content-Type", "application/json")
	setupClaudeAuth(outReq, useOAuth)
	outReq.Header.Set("anthropic-version", "2023-06-01")
	if req.Stream {
		outReq.Header.Set("Accept", "text/event-stream")
	}

	// fmt.Println("headers:", lib.Pformat(outReq.Header))

	client := providers.LongTimeoutClient
	resp, err := client.Do(outReq)
	if err != nil {
		return nil, fmt.Errorf("do request error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		resBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("api error: %s", string(resBody))
	}

	if !req.Stream {
		go func() {
			// defer func() {}()
			<-ctx.Done()
			_ = resp.Body.Close()
		}()

		resBody, err := io.ReadAll(resp.Body)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("error reading response: %v", err)
		}

		var cr Response
		err = json.Unmarshal(resBody, &cr)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal json: %v", err)
		}

		data, err := json.Marshal(cr.Usage)
		if err != nil {
			panic(err)
		}
		_, _ = fmt.Fprintln(os.Stderr, string(data))

		var builder strings.Builder
		for i, blk := range cr.Content {
			if blk.Type != "text" {
				continue
			}
			if i > 0 {
				builder.WriteString("\n")
			}
			builder.WriteString(blk.Text)
		}

		// Message ID might be in response metadata, but not in usage
		// For now, we'll leave it empty since it's not in the standard response
		var messageID string

		return &HandleResponse{
			Text:      builder.String(),
			Usage:     cr.Usage,
			MessageID: messageID,
		}, nil
	}

	// Streaming logic
	reader := bufio.NewReader(resp.Body)
	var answerBuilder strings.Builder
	var thinkingBuilder strings.Builder
	var eventData strings.Builder
	var currentContentIndex int
	var contentBlockTypes = map[int]string{}
	var messageID string
	var usage Usage

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

		if strings.HasPrefix(line, "event:") {
			// Event type line - we don't need to store this as it's also in the data
			continue
		}

		if strings.HasPrefix(line, "data:") {
			eventData.WriteString(strings.TrimSpace(line[5:]))
			continue
		}

		if line == "" && eventData.Len() > 0 {
			// End of event, process it
			eventJSON := eventData.String()
			eventData.Reset()

			if eventJSON == "[DONE]" {
				break
			}

			var event map[string]any
			if err := json.Unmarshal([]byte(eventJSON), &event); err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "unmarshal event error: %v\n", err)
				continue
			}

			eventType, _ := event["type"].(string)

			switch eventType {
			case "message_start":
				// Extract message ID from the start event
				if message, ok := event["message"].(map[string]any); ok {
					if id, ok := message["id"].(string); ok {
						messageID = id
					}
					// Extract usage information from message_start
					if usageData, ok := message["usage"].(map[string]any); ok {
						if v, ok := usageData["input_tokens"].(float64); ok {
							usage.InputTokens = int(v)
						}
						if v, ok := usageData["output_tokens"].(float64); ok {
							usage.OutputTokens = int(v)
						}
						if v, ok := usageData["cache_read_input_tokens"].(float64); ok {
							usage.CacheReadTokens = int(v)
						}
						if v, ok := usageData["cache_creation_input_tokens"].(float64); ok {
							usage.CacheWriteTokens = int(v)
						}
					}
				}

			case "content_block_start":
				index, _ := event["index"].(float64)
				contentBlock, ok := event["content_block"].(map[string]any)
				if ok {
					blockType, _ := contentBlock["type"].(string)
					contentBlockTypes[int(index)] = blockType
				}

			case "content_block_delta":
				index, _ := event["index"].(float64)
				delta, ok := event["delta"].(map[string]any)
				if !ok {
					continue
				}

				blockType := contentBlockTypes[int(index)]

				switch blockType {
				case "thinking":
					deltaType, _ := delta["type"].(string)
					if deltaType == "thinking_delta" {
						thinking, _ := delta["thinking"].(string)
						thinkingBuilder.WriteString(thinking)
					}
				case "text":
					deltaType, _ := delta["type"].(string)
					if deltaType == "text_delta" {
						text, _ := delta["text"].(string)
						answerBuilder.WriteString(text)
					}
				default:
					panic("unknown block type: " + blockType)
				}

			case "content_block_stop":
				index, _ := event["index"].(float64)
				if contentBlockTypes[int(index)] == "thinking" && thinkingBuilder.Len() > 0 {
					if ctx.Err() == nil && reasoningCallback != nil {
						reasoningCallback("**reasoning**\n\n" + thinkingBuilder.String())
					}
					thinkingBuilder.Reset()
				}
				currentContentIndex++

			case "message_delta":
				// Extract usage information from message_delta event (at top level, not in delta)
				if usageData, ok := event["usage"].(map[string]any); ok {
					if v, ok := usageData["output_tokens"].(float64); ok {
						usage.OutputTokens = int(v)
					}
					// Update other fields if present
					if v, ok := usageData["input_tokens"].(float64); ok {
						usage.InputTokens = int(v)
					}
					if v, ok := usageData["cache_read_input_tokens"].(float64); ok {
						usage.CacheReadTokens = int(v)
					}
					if v, ok := usageData["cache_creation_input_tokens"].(float64); ok {
						usage.CacheWriteTokens = int(v)
					}
				}

			case "message_stop":
			case "error":
				return nil, fmt.Errorf("api stream error: %s", util.Pformat(event))

			case "ping":
				// Ping event, ignore

			default:
				// Unknown event type, ignore per docs
			}
		}
	}

	return &HandleResponse{
		Text:      answerBuilder.String(),
		Usage:     usage,
		MessageID: messageID,
	}, nil
}

// HandleBatch sends multiple requests to Anthropic using the batch API
// and returns the results after polling for completion.
func HandleBatch(ctx context.Context, requests []BatchRequestItem) ([]BatchIndividualResult, error) {

	// Create batch
	createReq := BatchCreateRequest{Requests: requests}
	body, err := json.Marshal(createReq)
	if err != nil {
		return nil, fmt.Errorf("json marshal error: %v", err)
	}

	outReq, err := http.NewRequestWithContext(
		ctx,
		"POST",
		"https://api.anthropic.com/v1/messages/batches",
		bytes.NewBuffer(body),
	)
	if err != nil {
		return nil, fmt.Errorf("request creation error: %v", err)
	}

	outReq.Header.Set("Content-Type", "application/json")
	// Determine if any request uses OAuth
	useOAuth := false
	for _, req := range requests {
		if req.Params.UseOAuth {
			useOAuth = true
			break
		}
	}

	if useOAuth {
		return nil, fmt.Errorf("oauth not supported for batch")
	}

	setupClaudeAuth(outReq, useOAuth)
	outReq.Header.Set("anthropic-version", "2023-06-01")

	client := providers.LongTimeoutClient
	resp, err := client.Do(outReq)
	if err != nil {
		return nil, fmt.Errorf("do request error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		resBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("api error: %s", string(resBody))
	}

	resBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response: %v", err)
	}

	var batchResp BatchResponse
	err = json.Unmarshal(resBody, &batchResp)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal json: %v", err)
	}

	batchStartTime := time.Now()
	fmt.Printf("Created batch %s with %d requests\n", batchResp.ID, len(requests))

	// Poll for completion
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		if batchResp.ProcessingStatus == "ended" {
			break
		}

		elapsed := time.Since(batchStartTime).Seconds()
		fmt.Printf("Batch %s status: %s (succeeded: %d, errored: %d, processing: %d) %.1f seconds\n",
			batchResp.ID, batchResp.ProcessingStatus,
			batchResp.RequestCounts.Succeeded,
			batchResp.RequestCounts.Errored,
			batchResp.RequestCounts.Processing,
			elapsed)

		// Wait before polling again
		time.Sleep(5 * time.Second)

		// Get batch status
		statusReq, err := http.NewRequestWithContext(
			ctx,
			"GET",
			fmt.Sprintf("https://api.anthropic.com/v1/messages/batches/%s", batchResp.ID),
			nil,
		)
		if err != nil {
			return nil, fmt.Errorf("status request creation error: %v", err)
		}

		setupClaudeAuth(statusReq, useOAuth)
		statusReq.Header.Set("anthropic-version", "2023-06-01")

		statusResp, err := client.Do(statusReq)
		if err != nil {
			return nil, fmt.Errorf("status request error: %v", err)
		}

		if statusResp.StatusCode != http.StatusOK {
			statusResBody, _ := io.ReadAll(statusResp.Body)
			_ = statusResp.Body.Close()
			return nil, fmt.Errorf("status api error: %s", string(statusResBody))
		}

		statusResBody, err := io.ReadAll(statusResp.Body)
		_ = statusResp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("error reading status response: %v", err)
		}

		err = json.Unmarshal(statusResBody, &batchResp)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal status json: %v", err)
		}
	}

	elapsed := time.Since(batchStartTime).Seconds()
	fmt.Printf("Batch %s completed. Final counts - succeeded: %d, errored: %d, canceled: %d, expired: %d %.1f seconds\n",
		batchResp.ID, batchResp.RequestCounts.Succeeded, batchResp.RequestCounts.Errored,
		batchResp.RequestCounts.Canceled, batchResp.RequestCounts.Expired, elapsed)

	// Get results
	if batchResp.ResultsURL == nil {
		return nil, fmt.Errorf("no results URL available")
	}

	resultsReq, err := http.NewRequestWithContext(
		ctx,
		"GET",
		*batchResp.ResultsURL,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("results request creation error: %v", err)
	}

	setupClaudeAuth(resultsReq, useOAuth)
	resultsReq.Header.Set("anthropic-version", "2023-06-01")

	resultsResp, err := client.Do(resultsReq)
	if err != nil {
		return nil, fmt.Errorf("results request error: %v", err)
	}
	defer func() { _ = resultsResp.Body.Close() }()

	if resultsResp.StatusCode != http.StatusOK {
		resultsResBody, _ := io.ReadAll(resultsResp.Body)
		return nil, fmt.Errorf("results api error: %s", string(resultsResBody))
	}

	// Parse JSONL results
	var results []BatchIndividualResult
	scanner := bufio.NewScanner(resultsResp.Body)
	buf := make([]byte, 0, 1024*1024) // 1 MB initial buffer
	scanner.Buffer(buf, 10*1024*1024) // allow lines up to 10 MB
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var result BatchIndividualResult
		err := json.Unmarshal([]byte(line), &result)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal result line: %v", err)
		}

		results = append(results, result)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading results: %v", err)
	}

	return results, nil
}
