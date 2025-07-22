// Grok integration via nina-providers with API key authentication.
// Maintains full message history and provides CallWithStore functionality.
// Works with X.AI's Grok models including grok-4.
package lib

import (
	"context"
	"fmt"
	grok "github.com/nathants/nina/providers/grok"
	util "github.com/nathants/nina/util"
	"os"
	"os/exec"
	"strings"
)

// GrokClient wraps the nina-providers Grok functionality with store support
// and maintains the full message history for each conversation
type GrokClient struct {
	messages []grok.Message
}

// NewGrokClient creates a new Grok client with an empty message history
func NewGrokClient() (*GrokClient, error) {
	// Check for API key
	apiKey := os.Getenv("XAI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("XAI_API_KEY environment variable not set")
	}

	client := &GrokClient{
		messages: []grok.Message{},
	}

	return client, nil
}

// builds request with system prompt and history, sends request to Grok API
// returns response wrapped in grok.Response, tracks conversation history
// logs request/response to agents directory for debugging and analysis
func (c *GrokClient) CallWithStore(ctx context.Context, model, systemPrompt, userMessage string) (any, error) {
	_ = ctx // Context not used by current grok.Handle implementation
	var grokModel string
	switch model {
	case "grok":
		grokModel = "grok-4-0709"
	default:
		return nil, fmt.Errorf("unknown Grok model: %s", model)
	}

	// Add system message if this is the first message
	if len(c.messages) == 0 && systemPrompt != "" {
		systemMsg := grok.Message{
			Role:    "system",
			Content: systemPrompt,
		}
		c.messages = append(c.messages, systemMsg)
	}

	// Add the new user message
	userMsg := grok.Message{
		Role:    "user",
		Content: userMessage,
	}
	c.messages = append(c.messages, userMsg)

	// Build request with full message history
	req := grok.Request{
		Model:       grokModel,
		Messages:    c.messages,
		Stream:      false, // Grok streaming not implemented yet
		Temperature: 0.7,
	}

	// Call Grok API
	responseText, err := grok.Handle(ctx, req)
	if err != nil {
		return nil, err
	}

	// Create a response structure compatible with the rest of the code
	resp := &grok.Response{
		Model: grokModel,
		Choices: []grok.Choice{
			{
				Index: 0,
				Message: grok.ChoiceMessage{
					Role:    "assistant",
					Content: responseText,
				},
				FinishReason: "stop",
			},
		},
		// Usage data not available from current grok.Handle
		Usage: map[string]any{
			"prompt_tokens":     0,
			"completion_tokens": 0,
			"total_tokens":      0,
		},
	}

	// Add assistant response to message history
	assistantMsg := grok.Message{
		Role:    "assistant",
		Content: responseText,
	}
	c.messages = append(c.messages, assistantMsg)

	// Log API call
	err = c.logAPICall(&req, resp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to log API call: %v\n", err)
	}

	return resp, nil
}

func (c *GrokClient) logAPICall(req *grok.Request, resp *grok.Response) error {
	// Get the next log number
	logNum := GetNextAPILogNumber()

	// Create copies of request and response for modification
	reqCopy := *req
	respCopy := *resp

	// Extract and save text content from request
	if len(reqCopy.Messages) > 0 {
		var inputTexts []string
		for i, msg := range reqCopy.Messages {
			// Collect all input texts
			inputTexts = append(inputTexts, fmt.Sprintf("=== Message %d (Role: %s) ===\n%s", i+1, msg.Role, msg.Content))
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

	// Extract and save text content from response
	if len(respCopy.Choices) > 0 && respCopy.Choices[0].Message.Content != "" {
		textPath := GetTimestampedAgentsPath("text", fmt.Sprintf("%05d.output.txt", logNum))
		if err := os.WriteFile(textPath, []byte(respCopy.Choices[0].Message.Content), 0644); err != nil {
			return fmt.Errorf("failed to write output text: %w", err)
		}
	}

	// fmt.Println(filepath.Join("agents", "api", fmt.Sprintf("%05d.*.json", logNum)))

	jsonPath := GetTimestampedAgentsPath("api", fmt.Sprintf("%05d.input.json", logNum))
	err := os.WriteFile(jsonPath, []byte(util.Pformat(reqCopy)), 0644)
	if err != nil {
		return fmt.Errorf("failed to write API log: %w", err)
	}

	jsonPath = GetTimestampedAgentsPath("api", fmt.Sprintf("%05d.output.json", logNum))
	err = os.WriteFile(jsonPath, []byte(util.Pformat(respCopy)), 0644)
	if err != nil {
		return fmt.Errorf("failed to write API log: %w", err)
	}

	return nil
}

// GetTokenUsage returns the token usage from the last response
func (c *GrokClient) GetTokenUsage(resp any) (promptTokens, completionTokens, totalTokens int) {
	gResp := resp.(*grok.Response)
	if usage, ok := gResp.Usage.(map[string]any); ok {
		if v, ok := usage["prompt_tokens"].(float64); ok {
			promptTokens = int(v)
		}
		if v, ok := usage["completion_tokens"].(float64); ok {
			completionTokens = int(v)
		}
		if v, ok := usage["total_tokens"].(float64); ok {
			totalTokens = int(v)
		}
	}
	return
}

// GetDetailedUsage returns detailed token usage from Grok API responses
// Grok doesn't provide cache tokens, so cache fields are always zero
func (c *GrokClient) GetDetailedUsage(resp any) TokenUsage {
	gResp := resp.(*grok.Response)
	usage := TokenUsage{
		Cache: CacheUsage{
			Read:  0, // Grok doesn't provide cache metrics
			Write: 0,
		},
	}

	if respUsage, ok := gResp.Usage.(map[string]any); ok {
		if v, ok := respUsage["prompt_tokens"].(float64); ok {
			usage.Input = int(v)
		}
		if v, ok := respUsage["completion_tokens"].(float64); ok {
			usage.Output = int(v)
		}
	}

	return usage
}

// GetGrokResponseText extracts the text content from a Grok response
func GetGrokResponseText(resp *grok.Response) string {
	if len(resp.Choices) > 0 {
		return resp.Choices[0].Message.Content
	}
	return ""
}

// CompactMessages removes old messages keeping only recent pairs when approaching token limit.
// Keeps the most recent messagePairs (user+assistant pairs), preserving conversation context.
func (c *GrokClient) CompactMessages(messagePairs int) CompactionResult {
	if true {
		panic("no compaction yet")
	}
	// Skip system message if present
	startIdx := 0
	if len(c.messages) > 0 && c.messages[0].Role == "system" {
		startIdx = 1
	}

	// Count non-system messages
	nonSystemMessages := len(c.messages) - startIdx
	if nonSystemMessages < 2 {
		return CompactionResult{MessagesRemoved: 0, TokensRemoved: 0}
	}

	// Calculate how many messages to keep (pairs * 2)
	keepMessages := messagePairs * 2

	// If we have fewer messages than we want to keep, do nothing
	if nonSystemMessages <= keepMessages {
		return CompactionResult{MessagesRemoved: 0, TokensRemoved: 0}
	}

	// Find the last complete pair boundary
	totalMessages := nonSystemMessages
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
		message := c.messages[startIdx+i]
		// Use tokens utility to count tokens
		cmd := exec.Command("tokens")
		cmd.Stdin = strings.NewReader(message.Content)
		output, err := cmd.Output()
		if err != nil {
			// Log error but continue with zero tokens for this message
			fmt.Fprintf(os.Stderr, "Warning: Failed to count tokens for message %d: %v\n", startIdx+i, err)
		} else {
			tokens := 0
			_, _ = fmt.Sscanf(string(output), "%d", &tokens)
			tokensRemoved += tokens
		}

		// Fallback estimation if tokens command failed or returned 0
		if tokensRemoved == 0 && len(message.Content) > 0 {
			// Rough estimation: ~4 characters per token
			tokensRemoved += len(message.Content) / 4
		}
	}

	// Keep system message (if any) and recent messages
	if startIdx > 0 {
		// Keep system message and recent messages
		c.messages = append(c.messages[:1], c.messages[startIdx+removeCount:]...)
	} else {
		// No system message, just keep recent messages
		c.messages = c.messages[removeCount:]
	}

	return CompactionResult{
		MessagesRemoved: removeCount,
		TokensRemoved:   tokensRemoved,
	}
}
