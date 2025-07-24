// Claude integration via nina-providers with OAuth or API key authentication.
// Maintains full message history and provides CallWithStore with cache handling.
// Applies cache=ephemeral to system prompts and newest messages, logs API calls.
package lib

import (
	"context"
	"fmt"
	claude "github.com/nathants/nina/providers/claude"
	oauth "github.com/nathants/nina/providers/oauth"
	util "github.com/nathants/nina/util"
	"os"
	"os/exec"
	"strings"
)

// ClaudeClient wraps the nina-providers Claude functionality with store support
// and maintains the full message history for each conversation
type ClaudeClient struct {
	messages []*claude.Message
}

// NewClaudeClient creates a new Claude client with an empty message history
func NewClaudeClient() (*ClaudeClient, error) {
	// Try to load OAuth token from storage first
	oauthToken := os.Getenv("ANTHROPIC_OAUTH_TOKEN")
	if oauthToken == "" {
		// Try to get OAuth token from stored credentials
		if token, err := oauth.AnthropicAccess(); err == nil && token != "" {
			_ = os.Setenv("ANTHROPIC_OAUTH_TOKEN", token)
			oauthToken = token
		}
	}

	// Fall back to API key if OAuth not available
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		// Fallback to old name for backward compatibility
		apiKey = os.Getenv("CLAUDE_KEY")
	}

	if oauthToken == "" && apiKey == "" {
		return nil, fmt.Errorf("neither OAuth credentials nor ANTHROPIC_API_KEY environment variable available")
	}

	client := &ClaudeClient{
		messages: []*claude.Message{},
	}

	return client, nil
}

// RestoreMessages restores conversation history from a previous session for continuation
func (c *ClaudeClient) RestoreMessages(messages []any) error {
	c.messages = []*claude.Message{}

	for _, msg := range messages {
		msgMap, ok := msg.(map[string]any)
		if !ok {
			continue
		}

		role, _ := msgMap["role"].(string)

		// Handle content which can be either string or array format
		var textContent string
		if contentStr, ok := msgMap["content"].(string); ok {
			textContent = contentStr
		} else if contentArr, ok := msgMap["content"].([]any); ok {
			// Extract text from array format [{type: "text", text: "..."}]
			for _, item := range contentArr {
				if itemMap, ok := item.(map[string]any); ok {
					if itemType, _ := itemMap["type"].(string); itemType == "text" {
						if text, ok := itemMap["text"].(string); ok {
							textContent += text
						}
					}
				}
			}
		}

		if role != "" && textContent != "" {
			claudeMsg := &claude.Message{
				Role: role,
				Content: []claude.Text{
					{
						Type: "text",
						Text: textContent,
					},
				},
			}
			c.messages = append(c.messages, claudeMsg)
		}
	}

	return nil
}

// builds request incl system prompt, history; caches system prompt and newest messages
// sends synchronous request to Claude, returns response struct, tracks token usage
// appends assistant reply to history and logs request/response to agents directory
func (c *ClaudeClient) CallWithStore(ctx context.Context, model, systemPrompt, userMessage string) (any, error) {
	// Map nina model names to Claude models
	var claudeModel string
	var thinking *claude.Thinking

	// Check if thinking is enabled in context
	thinkingEnabled := false
	if val := ctx.Value(contextKey("thinking")); val != nil {
		thinkingEnabled = val.(bool)
	}

	switch model {
	case "sonnet", "4-sonnet":
		claudeModel = "claude-sonnet-4-20250514"
		if thinkingEnabled {
			thinking = &claude.Thinking{
				Type:         "enabled",
				BudgetTokens: 24000,
			}
		}
	case "opus", "4-opus":
		claudeModel = "claude-opus-4-20250514"
		if thinkingEnabled {
			thinking = &claude.Thinking{
				Type:         "enabled",
				BudgetTokens: 24000,
			}
		}
	default:
		return nil, fmt.Errorf("unknown Claude model: %s", model)
	}

	// Build request
	req := claude.Request{
		Model:     claudeModel,
		MaxTokens: 32000,
		Thinking:  thinking,
		Stream:    true,
	}

	// Add system prompt with cache control for efficiency
	req.System = []claude.Text{
		{
			Type:  "text",
			Text:  systemPrompt,
			Cache: &claude.CacheControl{Type: "ephemeral"},
		},
	}

	if len(c.messages) != 0 {
		req.System[0].Cache = nil
	}

	// Add the new user message to history
	if userMessage != "" {
		userMsg := claude.Message{
			Role:    "user",
			Content: []claude.Text{{Type: "text", Text: userMessage}},
		}
		c.messages = append(c.messages, &userMsg)
	}

	maxCachedMessages := 3
	totalMessages := len(c.messages)

	// Calculate the starting index for cached messages (newest)
	cacheStartIndex := max(0, totalMessages-maxCachedMessages)
	for i, msg := range c.messages {
		msg.Content[0].Cache = nil
		msgCopy := *msg
		if i >= cacheStartIndex {
			for j := range msgCopy.Content {
				if msgCopy.Content[j].Text != "" {
					msgCopy.Content[j].Cache = &claude.CacheControl{Type: "ephemeral"}
				}
			}
		}
		req.Messages = append(req.Messages, msgCopy)
	}

	handleResp, err := claude.Handle(ctx, req, func(data string) {
		// fmt.Printf("%s%s%s\n", ColorGreen, data, ColorReset)
	})
	if err != nil {
		return nil, err
	}

	// Create a response structure compatible with the rest of the code
	resp := &claude.Response{
		Content: []claude.ContentBlock{
			{
				Type: "text",
				Text: handleResp.Text,
			},
		},
		Usage: handleResp.Usage,
	}

	// Message ID is stored separately in handleResp, not in Usage

	if handleResp.Text != "" {
		assistantMsg := claude.Message{
			Role:    "assistant",
			Content: []claude.Text{{Type: "text", Text: handleResp.Text}},
		}
		c.messages = append(c.messages, &assistantMsg)
	}

	// Log API call
	err = c.logAPICall(&req, resp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to log API call: %v\n", err)
	}

	return resp, nil
}

func (c *ClaudeClient) logAPICall(req *claude.Request, resp *claude.Response) error {
	// Get the next log number
	logNum := GetNextAPILogNumber()

	// Create copies of request and response for modification
	reqCopy := *req
	respCopy := *resp

	// Extract and save text content from request
	if len(reqCopy.Messages) > 0 {
		var inputTexts []string
		for i, msg := range reqCopy.Messages {
			// Check if Content is a string (for user messages)
			// Collect all input texts
			inputTexts = append(inputTexts, fmt.Sprintf("=== Message %d (Role: %s) ===\n%s", i+1, msg.Role, msg.Content[0].Text))
		}
		// Save all input texts to a single file
		if len(inputTexts) > 0 {
			textPath := GetTimestampedAgentsPath("text", fmt.Sprintf("%05d.input.txt", logNum))
			combinedText := strings.Join(inputTexts, "\n\n")
			if err := os.WriteFile(textPath, []byte(combinedText), 0644); err != nil {
				return fmt.Errorf("failed to write input text: %w", err)
			}
		}
	}

	// Also handle system prompt if present
	if len(reqCopy.System) > 0 {
		for _, text := range reqCopy.System {
			if text.Text != "" {
				textPath := GetTimestampedAgentsPath("text", fmt.Sprintf("%05d.system.txt", logNum))
				if err := os.WriteFile(textPath, []byte(text.Text), 0644); err != nil {
					return fmt.Errorf("failed to write system text: %w", err)
				}
			}
		}
	}

	// Extract and save text content from response
	if len(respCopy.Content) > 0 {
		var outputText string
		for _, content := range respCopy.Content {
			if content.Type == "text" && content.Text != "" {
				outputText += content.Text
			}
		}
		if outputText != "" {
			textPath := GetTimestampedAgentsPath("text", fmt.Sprintf("%05d.output.txt", logNum))
			if err := os.WriteFile(textPath, []byte(outputText), 0644); err != nil {
				return fmt.Errorf("failed to write output text: %w", err)
			}
		}
	}

	// fmt.Println(GetTimestampedAgentsPath("api", fmt.Sprintf("%05d.*.json", logNum)))

	jsonPath := GetTimestampedAgentsPath("api", fmt.Sprintf("%05d.input.json", logNum))
	err := os.WriteFile(jsonPath, []byte(util.Pformat(reqCopy)), 0644)
	if err != nil {
		return fmt.Errorf("failed to write API log: %w", err)
	}

	jsonPath = GetTimestampedAgentsPath("api", fmt.Sprintf("%05d.output.json", logNum))

	// Create output structure that includes messages for continuation support
	outputData := map[string]any{
		"response": respCopy,
		"messages": c.messages,
	}

	err = os.WriteFile(jsonPath, []byte(util.Pformat(outputData)), 0644)
	if err != nil {
		return fmt.Errorf("failed to write API log: %w", err)
	}

	return nil
}

// GetTokenUsage returns the token usage from the last response
func (c *ClaudeClient) GetTokenUsage(resp any) (promptTokens, completionTokens, totalTokens int) {
	cResp := resp.(*claude.Response)
	promptTokens = cResp.Usage.InputTokens
	completionTokens = cResp.Usage.OutputTokens
	totalTokens = promptTokens + completionTokens
	return
}

// GetDetailedUsage returns detailed token usage including cache tokens from Claude API
// This method extracts actual API-returned values for accurate tracking and monitoring
func (c *ClaudeClient) GetDetailedUsage(resp any) TokenUsage {
	cResp := resp.(*claude.Response)
	return TokenUsage{
		Input:  cResp.Usage.InputTokens,
		Output: cResp.Usage.OutputTokens,
		Cache: CacheUsage{
			Read:  cResp.Usage.CacheReadTokens,
			Write: cResp.Usage.CacheWriteTokens,
		},
	}
}

// GetClaudeResponseText extracts the text content from a Claude response
func GetClaudeResponseText(resp *claude.Response) string {
	if len(resp.Content) > 0 {
		var text string
		for _, content := range resp.Content {
			if content.Type == "text" {
				text += content.Text
			}
		}
		return text
	}
	return ""
}

// CompactMessages removes old messages keeping only recent pairs when approaching token limit.
// Keeps the most recent messagePairs (user+assistant pairs), preserving conversation context.
// Returns the number of messages removed for logging purposes.
func (c *ClaudeClient) CompactMessages(messagePairs int) CompactionResult {
	if true {
		panic("no compaction yet")
	}
	// Ensure we have at least one complete pair
	if len(c.messages) < 2 {
		return CompactionResult{MessagesRemoved: 0, TokensRemoved: 0}
	}

	// Calculate how many messages to keep (pairs * 2)
	keepMessages := messagePairs * 2

	// If we have fewer messages than we want to keep, do nothing
	if len(c.messages) <= keepMessages {
		return CompactionResult{MessagesRemoved: 0, TokensRemoved: 0}
	}

	// Find the last complete pair boundary
	totalMessages := len(c.messages)
	// If odd number of messages, exclude the last unpaired message
	if totalMessages%2 == 1 {
		totalMessages--
	}

	// Calculate removal point
	removeCount := totalMessages - keepMessages
	if removeCount <= 0 {
		return CompactionResult{MessagesRemoved: 0, TokensRemoved: 0}
	}

	// Count tokens in messages to be removed
	tokensRemoved := 0
	for i := range removeCount {
		message := c.messages[i]
		// Convert message content to string for token counting
		var contentBuilder strings.Builder
		for _, text := range message.Content {
			contentBuilder.WriteString(text.Text)
			contentBuilder.WriteString(" ")
		}
		contentStr := contentBuilder.String()

		// Use tokens utility to count tokens
		cmd := exec.Command("tokens")
		cmd.Stdin = strings.NewReader(contentStr)
		output, err := cmd.Output()
		if err != nil {
			// Log error but continue with zero tokens for this message
			fmt.Fprintf(os.Stderr, "Warning: Failed to count tokens for message %d: %v\n", i, err)
		} else {
			tokens := 0
			_, _ = fmt.Sscanf(string(output), "%d", &tokens)
			tokensRemoved += tokens
		}

		// Fallback estimation if tokens command failed or returned 0
		if tokensRemoved == 0 && len(contentStr) > 0 {
			// Rough estimation: ~4 characters per token
			tokensRemoved += len(contentStr) / 4
		}
	}

	// Remove old messages
	c.messages = c.messages[removeCount:]

	return CompactionResult{
		MessagesRemoved: removeCount / 2,
		TokensRemoved:   tokensRemoved,
	}
}

// Call makes a basic API call without store functionality.
func (c *ClaudeClient) Call(ctx context.Context, model, systemPrompt, userMessage string) (any, error) {
	return c.CallWithStore(ctx, model, systemPrompt, userMessage)
}

// SupportsTools returns true as Claude supports native tool calling.
func (c *ClaudeClient) SupportsTools() bool {
	return true
}

// CallWithTools calls Claude API with tool definitions.
func (c *ClaudeClient) CallWithTools(ctx context.Context, model, systemPrompt, userMessage string, tools []any) (any, error) {
	// For now, just call the regular method
	// In a full implementation, this would add tools to the request
	return c.CallWithStore(ctx, model, systemPrompt, userMessage)
}
