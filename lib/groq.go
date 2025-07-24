// Groq integration via nina-providers with API key authentication for fast LLM
// inference. Supports models like moonshotai/kimi-k2-instruct via OpenAI-compatible
// API with reasoning features. Maintains full message history.
package lib

import (
	"context"
	"encoding/json"
	"fmt"
	groq "github.com/nathants/nina/providers/groq"
	util "github.com/nathants/nina/util"
	"os"
	"path/filepath"
	"strings"
)

// GroqClient wraps the nina-providers Groq functionality with store support
// and maintains the full message history for each conversation
type GroqClient struct {
	messages []groq.Message
}

// NewGroqClient creates a new Groq client with an empty message history
func NewGroqClient() (*GroqClient, error) {
	// Check for API key
	apiKey := os.Getenv("GROQ_API_KEY")
	if apiKey == "" {
		// Fallback to old name for backward compatibility
		apiKey = os.Getenv("GROQ_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("GROQ_API_KEY environment variable not set")
	}

	client := &GroqClient{
		messages: []groq.Message{},
	}

	return client, nil
}

// builds request with system prompt and history, sends request to Groq API
// returns response wrapped in groq.HandleResponse, tracks conversation history
// logs request/response to agents directory for debugging and analysis
func (c *GroqClient) CallWithStore(ctx context.Context, model, systemPrompt, userMessage string) (any, error) {
	var groqModel string
	switch model {
	case "k2":
		groqModel = "moonshotai/kimi-k2-instruct"
	default:
		return nil, fmt.Errorf("unknown Groq model: %s", model)
	}

	// Add system message if this is the first message
	if len(c.messages) == 0 && systemPrompt != "" {
		systemMsg := groq.Message{
			Role:    "system",
			Content: systemPrompt,
		}
		c.messages = append(c.messages, systemMsg)
	}

	// Add user message
	userMsg := groq.Message{
		Role:    "user",
		Content: userMessage,
	}
	c.messages = append(c.messages, userMsg)

	// Create request
	request := groq.Request{
		Model:    groqModel,
		Messages: c.messages,
		Stream:   false,
	}

	// Log request
	logFile := GetTimestampedAgentsPath("api", fmt.Sprintf("%d.input.json", getMessageCount()+1))
	if err := logRequest(logFile, request); err != nil {
		ColoredStderr("Failed to log request: %v", err)
	}

	// Call Groq API with context
	response, err := groq.Handle(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("groq API error: %w", err)
	}

	// Add assistant response to history
	if response.Text != "" {
		assistantMsg := groq.Message{
			Role:    "assistant",
			Content: response.Text,
		}
		c.messages = append(c.messages, assistantMsg)
	}

	// Log response
	logFile = GetTimestampedAgentsPath("api", fmt.Sprintf("%d.output.json", getMessageCount()))
	if err := logResponse(logFile, response); err != nil {
		ColoredStderr("Failed to log response: %v", err)
	}

	// Log text format
	logTextConversation(c.messages)

	return response, nil
}

// GetTokenUsage extracts token usage information from Groq response
func (c *GroqClient) GetTokenUsage(resp any) (int, int, int) {
	if r, ok := resp.(*groq.HandleResponse); ok && r.Usage != nil {
		return r.Usage.PromptTokens, r.Usage.CompletionTokens, r.Usage.TotalTokens
	}
	return 0, 0, 0
}

// GetDetailedUsage returns detailed token usage for the response
func (c *GroqClient) GetDetailedUsage(resp any) TokenUsage {
	usage := TokenUsage{}
	if r, ok := resp.(*groq.HandleResponse); ok && r.Usage != nil {
		usage.Input = r.Usage.PromptTokens
		usage.Output = r.Usage.CompletionTokens
		// Groq doesn't provide cache metrics currently
		usage.Cache.Read = 0
		usage.Cache.Write = 0
	}
	return usage
}

// CompactMessages removes old messages keeping recent context
func (c *GroqClient) CompactMessages(keepRecentPairs int) CompactionResult {
	result := CompactionResult{}

	// Keep system message + specified number of recent message pairs
	keepCount := 1 + (keepRecentPairs * 2) // system + user/assistant pairs
	if len(c.messages) <= keepCount {
		return result // Nothing to compact
	}

	// Estimate tokens for removed messages (rough approximation)
	removedMessages := c.messages[1 : len(c.messages)-keepCount+1]
	for _, msg := range removedMessages {
		result.TokensRemoved += len(msg.Content) / 4 // Rough token estimate
	}
	result.MessagesRemoved = len(removedMessages)

	// Keep system message and recent messages
	newMessages := []groq.Message{c.messages[0]} // System message
	newMessages = append(newMessages, c.messages[len(c.messages)-keepCount+1:]...)
	c.messages = newMessages

	return result
}

// helper function to get message count for logging
func getMessageCount() int {
	agentsDir := util.GetAgentsDir()
	pattern := filepath.Join(agentsDir, "api", "*.input.json")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return 0
	}
	return len(files)
}

// helper function to log request to file
func logRequest(filename string, request groq.Request) error {
	data, err := json.MarshalIndent(request, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filename, data, 0644)
}

// helper function to log response to file
func logResponse(filename string, response *groq.HandleResponse) error {
	data, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filename, data, 0644)
}

// helper function to log text format conversation
func logTextConversation(messages []groq.Message) {
	var content strings.Builder
	for _, msg := range messages {
		switch msg.Role {
		case "system":
			content.WriteString(util.NinaSystemPromptStart + "\n")
			content.WriteString(msg.Content + "\n")
			content.WriteString(util.NinaSystemPromptEnd + "\n\n")
		case "user":
			content.WriteString(util.NinaInputStart + "\n")
			content.WriteString(msg.Content + "\n")
			content.WriteString(util.NinaInputEnd + "\n\n")
		case "assistant":
			content.WriteString(msg.Content + "\n\n")
		}
	}

	logFile := GetTimestampedAgentsPath("text", fmt.Sprintf("%d.txt", getMessageCount()))
	if err := os.WriteFile(logFile, []byte(content.String()), 0644); err != nil {
		ColoredStderr("Failed to write text log: %v", err)
	}
}

// GetGroqResponseText extracts the text content from a Groq response
func GetGroqResponseText(resp *groq.HandleResponse) string {
	return resp.Text
}

// Call makes a basic API call without store functionality.
func (c *GroqClient) Call(ctx context.Context, model, systemPrompt, userMessage string) (any, error) {
	return c.CallWithStore(ctx, model, systemPrompt, userMessage)
}

// SupportsTools returns false as Groq doesn't currently support native tool calling.
func (c *GroqClient) SupportsTools() bool {
	return false
}

// CallWithTools returns an error as Groq doesn't support tool calling.
func (c *GroqClient) CallWithTools(ctx context.Context, model, systemPrompt, userMessage string, tools []any) (any, error) {
	return nil, fmt.Errorf("groq does not support tool calling")
}
