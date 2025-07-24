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

	"github.com/nathants/nina/prompts"
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
			apiKey := os.Getenv("ANTHROPIC_API_KEY")
			if apiKey == "" {
				// Fallback to old name for backward compatibility
				apiKey = os.Getenv("CLAUDE_KEY")
			}
			req.Header.Set("x-api-key", apiKey)
			if !logOnce {
				logOnce = true
				_, _ = fmt.Fprintf(os.Stderr, "OAuth requested but token not found, falling back to API key authentication\n")
			}
		}
	} else {
		// Use API key authentication
		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		if apiKey == "" {
			// Fallback to old name for backward compatibility
			apiKey = os.Getenv("CLAUDE_KEY")
		}
		req.Header.Set("x-api-key", apiKey)
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

// Tool represents a tool definition for Claude's tool use feature
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// ToolChoice specifies which tools Claude can use
type ToolChoice struct {
	Type string `json:"type"` // "auto", "any", or "tool"
	Name string `json:"name,omitempty"` // Only for type "tool"
}

// ToolUse represents a tool call in Claude's response
type ToolUse struct {
	Type  string         `json:"type"` // "tool_use"
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

// ToolResult represents the result of executing a tool
type ToolResult struct {
	Type      string `json:"type"` // "tool_result"
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
}

// RequestWithTools extends Request to include tools
type RequestWithTools struct {
	Model      string      `json:"model"`
	System     []Text      `json:"system"`
	Messages   []Message   `json:"messages"`
	MaxTokens  int         `json:"max_tokens,omitempty"`
	Tools      []Tool      `json:"tools,omitempty"`
	ToolChoice *ToolChoice `json:"tool_choice,omitempty"`
	Stream     bool        `json:"stream,omitempty"`
}

// ResponseWithTools extends Response to handle tool use
type ResponseWithTools struct {
	Content    []any  `json:"content"` // Can be ContentBlock or ToolUse
	Usage      Usage  `json:"usage"`
	StopReason string `json:"stop_reason"`
}

// HandleToolsResponse contains the response from HandleTools
type HandleToolsResponse struct {
	Text      string
	Usage     Usage
	ToolCalls []ToolUse
}




// TranslateToClaudeTools converts generic tool definitions to Claude's expected format.
// Maps tool names and field names to match Claude's API requirements.
func TranslateToClaudeTools(tools []prompts.ToolDefinition) []map[string]any {
	claudeTools := make([]map[string]any, 0, len(tools))
	
	for _, tool := range tools {
		// Map our tool names to Claude's expected names
		claudeName := tool.Name
		claudeDescription := tool.Description
		
		// For NinaBash, Claude expects "execute_bash"
		if tool.Name == "NinaBash" {
			claudeName = "execute_bash"
		}
		// For NinaChange, we'll use "change_file"
		if tool.Name == "NinaChange" {
			claudeName = "change_file"
		}
		
		// Build properties for input schema
		properties := make(map[string]any)
		required := make([]string, 0)
		
		for _, field := range tool.InputSchema.Fields {
			fieldName := field.Name
			// Map field names for Claude
			if tool.Name == "NinaBash" && field.Name == "command" {
				// Keep as "command" for execute_bash
			} else if tool.Name == "NinaChange" {
				// Map NinaPath -> path, NinaSearch -> search, NinaReplace -> replace
				switch field.Name {
				case "NinaPath":
					fieldName = "path"
				case "NinaSearch":
					fieldName = "search"
				case "NinaReplace":
					fieldName = "replace"
				}
			}
			
			properties[fieldName] = map[string]any{
				"type":        field.Type,
				"description": field.Description,
			}
			
			if field.Required {
				required = append(required, fieldName)
			}
		}
		
		claudeTool := map[string]any{
			"name":        claudeName,
			"description": claudeDescription,
			"input_schema": map[string]any{
				"type":       "object",
				"properties": properties,
				"required":   required,
			},
		}
		
		claudeTools = append(claudeTools, claudeTool)
	}
	
	return claudeTools
}

// HandleTools processes a request with tool support
func HandleTools(ctx context.Context, model, systemPrompt, userPrompt string, maxTokens int, debug bool) (*HandleToolsResponse, error) {
	// Get tool definitions from centralized source and translate to Claude format
	toolDefs := prompts.GetToolDefinitions()
	claudeToolDefs := TranslateToClaudeTools(toolDefs)
	
	// Convert to Claude's Tool type
	tools := make([]Tool, 0, len(claudeToolDefs))
	for _, def := range claudeToolDefs {
		tool := Tool{
			Name:        def["name"].(string),
			Description: def["description"].(string),
			InputSchema: def["input_schema"].(map[string]any),
		}
		tools = append(tools, tool)
	}

	// Create messages array - we'll use the any type for content
	messages := []map[string]any{{
		"role": "user",
		"content": userPrompt,
	}}

	// Tool execution loop
	var totalUsage Usage
	var allToolCalls []ToolUse
	var finalText string

	for {

		// Create request with proper structure
		reqBody := map[string]any{
			"model":      model,
			"system":     systemPrompt,
			"messages":   messages,
			"max_tokens": maxTokens,
			"tools":      tools,
		}

		// Send request
		resp, err := sendToolRequestRaw(ctx, reqBody, debug)
		if err != nil {
			return nil, fmt.Errorf("failed to send request: %w", err)
		}

		// Accumulate usage
		totalUsage.InputTokens += resp.Usage.InputTokens
		totalUsage.OutputTokens += resp.Usage.OutputTokens

		// Process response content
		var textParts []string
		var toolCalls []ToolUse
		var assistantContent []any

		for _, content := range resp.Content {
			switch v := content.(type) {
			case map[string]any:
				contentType, _ := v["type"].(string)
				switch contentType {
				case "text":
					if text, ok := v["text"].(string); ok {
						textParts = append(textParts, text)
						assistantContent = append(assistantContent, v)
					}
				case "tool_use":
					toolCall := ToolUse{
						Type:  "tool_use",
						ID:    v["id"].(string),
						Name:  v["name"].(string),
						Input: v["input"].(map[string]any),
					}
					toolCalls = append(toolCalls, toolCall)
					allToolCalls = append(allToolCalls, toolCall)
					assistantContent = append(assistantContent, v)
				}
			}
		}

		finalText = strings.Join(textParts, "\n")

		// Add assistant message to history
		messages = append(messages, map[string]any{
			"role":    "assistant",
			"content": assistantContent,
		})

		// If no tool calls, we're done
		if len(toolCalls) == 0 || resp.StopReason == "end_turn" {
			break
		}

		// Execute tools and prepare results
		var userContent []any
		for _, toolCall := range toolCalls {
			if debug {
				fmt.Fprintf(os.Stderr, "Executing tool: %s\n", toolCall.Name)
			}

			result, err := executeToolCall(toolCall)
			if err != nil {
				result = fmt.Sprintf("Error: %v", err)
			}

			userContent = append(userContent, map[string]any{
				"type":        "tool_result",
				"tool_use_id": toolCall.ID,
				"content":     result,
			})
		}

		// Add tool results as user message
		messages = append(messages, map[string]any{
			"role":    "user",
			"content": userContent,
		})
	}

	return &HandleToolsResponse{
		Text:      finalText,
		Usage:     totalUsage,
		ToolCalls: allToolCalls,
	}, nil
}

// sendToolRequestRaw sends a raw request map to Claude API
func sendToolRequestRaw(ctx context.Context, reqBody map[string]any, debug bool) (*ResponseWithTools, error) {
	// Tools API doesn't support OAuth, always use API key
	useOAuth := false

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("json marshal error: %v", err)
	}

	if debug {
		fmt.Fprintf(os.Stderr, "Request: %s\n", string(body))
	}

	outReq, err := http.NewRequestWithContext(
		ctx,
		"POST",
		"https://api.anthropic.com/v1/messages",
		bytes.NewBuffer(body),
	)
	if err != nil {
		return nil, err
	}

	setupClaudeAuth(outReq, useOAuth)
	outReq.Header.Set("anthropic-version", "2023-06-01")
	outReq.Header.Set("anthropic-beta", "tools-2024-04-04")
	outReq.Header.Set("Content-Type", "application/json")

	client := providers.LongTimeoutClient
	resp, err := client.Do(outReq)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if debug {
		fmt.Fprintf(os.Stderr, "Response: %s\n", string(respBody))
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error: %s - %s", resp.Status, string(respBody))
	}

	var response ResponseWithTools
	err = json.Unmarshal(respBody, &response)
	if err != nil {
		return nil, fmt.Errorf("json unmarshal error: %v", err)
	}

	return &response, nil
}


// executeToolCall executes a tool and returns the result matching XML.md format
func executeToolCall(toolCall ToolUse) (string, error) {
	switch toolCall.Name {
	case "execute_bash":
		command, ok := toolCall.Input["command"].(string)
		if !ok {
			return "", fmt.Errorf("invalid command parameter")
		}

		// Execute bash command as defined in XML.md: bash -c "$cmd"
		cmd := exec.Command("bash", "-c", command)
		output, err := cmd.CombinedOutput()
		
		// Format result to match NinaResult structure from XML.md
		exitCode := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = 1
			}
		}
		
		// Split output into stdout/stderr (combined output doesn't separate them)
		// For now, put all in stdout as we're using CombinedOutput
		return fmt.Sprintf("NinaCmd: %s\nNinaExit: %d\nNinaStdout: %s\nNinaStderr: ", 
			command, exitCode, string(output)), nil

	case "change_file":
		// Map from Claude field names back to Nina field names
		path, ok := toolCall.Input["path"].(string)
		if !ok {
			return "", fmt.Errorf("invalid path parameter")
		}
		search, ok := toolCall.Input["search"].(string)
		if !ok {
			return "", fmt.Errorf("invalid search parameter")
		}
		replace, ok := toolCall.Input["replace"].(string)
		if !ok {
			return "", fmt.Errorf("invalid replace parameter")
		}

		// Read file
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Sprintf("NinaChange: %s\nNinaError: %v", path, err), nil
		}

		// Perform search/replace once
		fileStr := string(content)
		if !strings.Contains(fileStr, search) {
			return fmt.Sprintf("NinaChange: %s\nNinaError: search text not found", path), nil
		}
		
		// Replace first occurrence only
		newContent := strings.Replace(fileStr, search, replace, 1)
		
		// Write back
		err = os.WriteFile(path, []byte(newContent), 0644)
		if err != nil {
			return fmt.Sprintf("NinaChange: %s\nNinaError: %v", path, err), nil
		}
		
		return fmt.Sprintf("NinaChange: %s", path), nil

	default:
		return "", fmt.Errorf("unknown tool: %s", toolCall.Name)
	}
}
