// GeminiClient wraps the nina-providers Gemini functionality with conversation history
// maintains full message history and sends all messages to the API on each call
// supports Gemini's thinking models with reasoning callbacks
package lib

import (
	"context"
	"fmt"
	gemini "github.com/nathants/nina/providers/gemini"
	util "github.com/nathants/nina/util"
	"os"
	"strings"
)

// GeminiClient wraps the nina-providers Gemini functionality with conversation management
type GeminiClient struct {
	messages []string
	system   string
}

// NewGeminiClient creates a new Gemini client
func NewGeminiClient() (*GeminiClient, error) {
	// Check for credentials
	token := os.Getenv("GEMINI_OAUTH_TOKEN")
	apiKey := os.Getenv("GOOGLE_API_KEY")
	if apiKey == "" {
		// Fallback to old name for backward compatibility
		apiKey = os.Getenv("GOOGLE_AISTUDIO_TOKEN")
	}

	if token == "" && apiKey == "" {
		return nil, fmt.Errorf("GEMINI_OAUTH_TOKEN or GOOGLE_API_KEY environment variable not set")
	}

	return &GeminiClient{
		messages: []string{},
	}, nil
}

// CallWithStore calls Gemini API maintaining conversation history
func (c *GeminiClient) CallWithStore(ctx context.Context, model, systemPrompt, userMessage string) (any, error) {
	// Store system prompt on first call
	if c.system == "" {
		c.system = systemPrompt
	}

	// Add user message to history
	c.messages = append(c.messages, userMessage)

	// Map model names if needed
	geminiModel := model
	switch model {
	case "gemini":
		geminiModel = "gemini-2.5-pro"
	}

	// Call Gemini API with all messages
	var responseText strings.Builder
	reasoningText := strings.Builder{}

	// Reasoning callback to capture thinking output
	reasoningCallback := func(reasoning string) {
		if reasoning != "" {
			reasoningText.WriteString(reasoning)
			reasoningText.WriteString("\n")
		}
	}

	// Call Gemini with thinking enabled
	result, err := gemini.Handle(
		ctx,
		geminiModel,
		c.system,
		c.messages,
		[]string{},
		reasoningCallback,
		false,
		32000,
	)
	if err != nil {
		return nil, err
	}

	responseText.WriteString(result)

	// Add assistant response to history
	c.messages = append(c.messages, result)

	_ = reasoningText.String()

	// Create response structure
	resp := &GeminiResponse{
		Model:      geminiModel,
		Text:       responseText.String(),
		Reasoning:  "",
		InputCount: len(c.messages) - 1, // Approximate token count
	}

	// Print response with color
	fmt.Printf("%s%s%s\n", ColorGreen, result, ColorReset)

	// Log API call
	err = c.logAPICall(model, systemPrompt, userMessage, resp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to log API call: %v\n", err)
	}

	return resp, nil
}

// GeminiResponse represents a response from Gemini API
type GeminiResponse struct {
	Model      string
	Text       string
	Reasoning  string
	InputCount int // Approximate message count as we don't get token counts
}

// logAPICall logs the API request and response
func (c *GeminiClient) logAPICall(model, system, userMessage string, resp *GeminiResponse) error {
	// Get the next log number
	logNum := GetNextAPILogNumber()

	// Save input text
	inputPath := GetTimestampedAgentsPath("text", fmt.Sprintf("%05d.input.txt", logNum))

	inputText := fmt.Sprintf("=== System ===\n%s\n\n=== User Message ===\n%s\n\n=== Previous Messages ===\n%s",
		system, userMessage, strings.Join(c.messages[:len(c.messages)-2], "\n---\n"))

	if err := os.WriteFile(inputPath, []byte(inputText), 0644); err != nil {
		return fmt.Errorf("failed to write input text: %w", err)
	}

	// Save output text
	outputPath := GetTimestampedAgentsPath("text", fmt.Sprintf("%05d.output.txt", logNum))
	outputText := resp.Text
	if resp.Reasoning != "" {
		outputText = fmt.Sprintf("=== Reasoning ===\n%s\n\n=== Response ===\n%s", resp.Reasoning, resp.Text)
	}

	if err := os.WriteFile(outputPath, []byte(outputText), 0644); err != nil {
		return fmt.Errorf("failed to write output text: %w", err)
	}

	// Save request JSON
	reqJSON := map[string]any{
		"model":    model,
		"system":   system,
		"messages": c.messages,
	}

	jsonPath := GetTimestampedAgentsPath("api", fmt.Sprintf("%05d.input.json", logNum))
	if err := os.WriteFile(jsonPath, []byte(util.Pformat(reqJSON)), 0644); err != nil {
		return fmt.Errorf("failed to write API request log: %w", err)
	}

	// Save response JSON
	respJSON := map[string]any{
		"model":     resp.Model,
		"text":      resp.Text,
		"reasoning": resp.Reasoning,
	}

	jsonPath = GetTimestampedAgentsPath("api", fmt.Sprintf("%05d.output.json", logNum))
	if err := os.WriteFile(jsonPath, []byte(util.Pformat(respJSON)), 0644); err != nil {
		return fmt.Errorf("failed to write API response log: %w", err)
	}

	return nil
}

// GetTokenUsage returns estimated token usage (Gemini doesn't provide exact counts)
func (c *GeminiClient) GetTokenUsage(resp any) (promptTokens, completionTokens, totalTokens int) {
	// Estimate tokens based on message count and response length
	// This is a rough approximation since Gemini doesn't return token counts
	gResp := resp.(*GeminiResponse)

	// Rough estimation: 1 token ~= 4 characters
	promptTokens = len(strings.Join(c.messages, " ")) / 4
	completionTokens = len(gResp.Text) / 4
	totalTokens = promptTokens + completionTokens

	return
}

// GetDetailedUsage returns detailed token usage (estimated for Gemini)
func (c *GeminiClient) GetDetailedUsage(resp any) TokenUsage {
	prompt, completion, _ := c.GetTokenUsage(resp)
	return TokenUsage{
		Input:  prompt,
		Output: completion,
		Cache: CacheUsage{
			Read:  0, // Gemini doesn't provide cache metrics
			Write: 0,
		},
	}
}

// CompactMessages removes old messages when approaching token limit
// CompactMessages removes old messages when approaching token limit
func (c *GeminiClient) CompactMessages(messagePairs int) CompactionResult {
	// Keep system prompt and remove oldest message pairs
	toRemove := messagePairs * 2 // Each pair is user + assistant
	if toRemove >= len(c.messages) {
		toRemove = len(c.messages) - 2 // Keep at least the last exchange
	}

	if toRemove <= 0 {
		return CompactionResult{MessagesRemoved: 0, TokensRemoved: 0}
	}

	// Estimate tokens being removed
	removedText := strings.Join(c.messages[:toRemove], " ")
	tokensRemoved := len(removedText) / 4 // Rough estimate

	// Remove messages
	c.messages = c.messages[toRemove:]

	return CompactionResult{
		MessagesRemoved: toRemove,
		TokensRemoved:   tokensRemoved,
	}
}

// GetGeminiResponseText extracts the text content from a Gemini response
func GetGeminiResponseText(resp *GeminiResponse) string {
	return resp.Text
}

// Call makes a basic API call without store functionality.
func (c *GeminiClient) Call(ctx context.Context, model, systemPrompt, userMessage string) (any, error) {
	return c.CallWithStore(ctx, model, systemPrompt, userMessage)
}

// SupportsTools returns true as Gemini supports function calling.
func (c *GeminiClient) SupportsTools() bool {
	return true
}

// CallWithTools calls Gemini API with tool definitions.
func (c *GeminiClient) CallWithTools(ctx context.Context, model, systemPrompt, userMessage string, tools []any) (any, error) {
	// For now, just call the regular method
	// In a full implementation, this would add function declarations to the request
	return c.CallWithStore(ctx, model, systemPrompt, userMessage)
}
