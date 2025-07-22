package lib

import (
	"context"
	"fmt"
	openai "github.com/nathants/nina/providers/openai"
	util "github.com/nathants/nina/util"
	"os"
	"strings"
)

// OpenAIClient wraps the nina-providers OpenAI functionality with store support
// maintains conversation state using previous_message_id for efficient API calls
// only sends the most recent message instead of full message history
type OpenAIClient struct {
	responseID string
	messages   []openai.ChatMessage // Full message history for logging
}

// NewOpenAIClient creates a new OpenAI client and loads any saved response ID
func NewOpenAIClient() (*OpenAIClient, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		// Fallback to old name for backward compatibility
		apiKey = os.Getenv("OPENAI_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY environment variable not set")
	}

	client := &OpenAIClient{}

	return client, nil
}

// RestoreResponseID restores the response ID from previous session for continuation
func (c *OpenAIClient) RestoreResponseID(responseID string) {
	c.responseID = responseID
}

// RestoreMessages restores conversation history from a previous session for continuation
func (c *OpenAIClient) RestoreMessages(messages []any) error {
	c.messages = []openai.ChatMessage{}

	for _, msg := range messages {
		msgMap, ok := msg.(map[string]any)
		if !ok {
			continue
		}

		role, _ := msgMap["role"].(string)
		if role == "" {
			continue
		}

		// Extract content based on role
		var text string
		if content, ok := msgMap["content"].([]any); ok && len(content) > 0 {
			if contentMap, ok := content[0].(map[string]any); ok {
				text, _ = contentMap["text"].(string)
			}
		}

		if text == "" {
			continue
		}

		chatMsg := openai.ChatMessage{
			Type: "message",
			Role: role,
			Content: []openai.ContentPart{
				{
					Type: "input_text",
					Text: text,
				},
			},
		}
		c.messages = append(c.messages, chatMsg)
	}

	return nil
}

// CallWithStore calls OpenAI API with store=true using previous_message_id for efficiency
func (c *OpenAIClient) CallWithStore(ctx context.Context, model, systemPrompt, userMessage string) (any, error) {
	var effort string
	var temp float64
	switch model {
	case "o3":
		effort = "medium"
	case "o4-mini", "o4-mini-flex":
		effort = "medium"
	case "gpt-4.1":
		temp = 0.6
	default:
		panic("unknown model: " + model)
	}
	// Build request using previous_message_id for efficiency
	req := openai.Request{
		Model:  model,
		Store:  true,
		Stream: true,
	}
	if temp != 0 {
		req.Temperature = &temp
	}
	// Set service tier for flex models and convert model name
	if strings.HasSuffix(model, "-flex") {
		req.ServiceTier = "flex"
		// Convert o4-mini-flex to o4-mini for API call
		req.Model = strings.TrimSuffix(model, "-flex")
	}
	if effort != "" {
		req.Reasoning = &openai.ReasoningRequest{
			Summary: "auto",
			Effort:  effort,
		}
	}

	// When using previous_message_id, only send the new user message
	if c.responseID != "" {
		req.PreviousID = c.responseID
		req.Input = []openai.ChatMessage{
			{
				Type: "message",
				Role: "user",
				Content: []openai.ContentPart{
					{
						Type: "input_text",
						Text: userMessage,
					},
				},
			},
		}
	} else {
		// First message - include system prompt
		req.Input = []openai.ChatMessage{
			{
				Type: "message",
				Role: "system",
				Content: []openai.ContentPart{
					{
						Type: "input_text",
						Text: systemPrompt,
					},
				},
			},
			{
				Type: "message",
				Role: "user",
				Content: []openai.ContentPart{
					{
						Type: "input_text",
						Text: userMessage,
					},
				},
			},
		}
	}

	// fmt.Println(util.Pformat(req))

	// Call OpenAI API using nina-providers
	handleResp, err := openai.Handle(ctx, req, func(data string) {
		// fmt.Printf("%s%s%s\n", ColorGreen, data, ColorReset)
	})
	if err != nil {
		return nil, err
	}

	// Create a response structure compatible with the rest of the code
	resp := &openai.Response{
		ID:    handleResp.ResponseID,
		Model: model,
		Usage: *handleResp.Usage,
		Output: []openai.Output{
			{
				Role:   "assistant",
				Status: "completed",
				Type:   "message",
				Content: []openai.Content{
					{
						Type: "output_text",
						Text: handleResp.Text,
					},
				},
			},
		},
	}

	// Store the new responseID only after successful API call
	if resp.ID != "" {
		c.responseID = resp.ID
	}

	// Add user message to history if not already there (for first message)
	if userMessage != "" {
		userMsg := openai.ChatMessage{
			Type: "message",
			Role: "user",
			Content: []openai.ContentPart{
				{
					Type: "input_text",
					Text: userMessage,
				},
			},
		}
		c.messages = append(c.messages, userMsg)
	}

	// Add assistant response to history
	if handleResp.Text != "" {
		assistantMsg := openai.ChatMessage{
			Type: "message",
			Role: "assistant",
			Content: []openai.ContentPart{
				{
					Type: "output_text",
					Text: handleResp.Text,
				},
			},
		}
		c.messages = append(c.messages, assistantMsg)
	}

	// Log API call
	err = c.logAPICall(&req, resp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to log API call: %v\n", err)
	}

	return resp, nil
}

func (c *OpenAIClient) logAPICall(req *openai.Request, resp *openai.Response) error {
	// Get the next log number
	logNum := GetNextAPILogNumber()

	// Create copies of request and response for modification
	reqCopy := *req
	respCopy := *resp

	// Log actual sent request to agents/debug/
	debugPath := GetTimestampedAgentsPath("debug", fmt.Sprintf("%05d.input.json", logNum))
	if err := os.WriteFile(debugPath, []byte(util.Pformat(reqCopy)), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write debug log: %v\n", err)
	}

	// For agents/api/, show full message history
	reqWithHistory := openai.Request{
		Model:       req.Model,
		Store:       req.Store,
		Stream:      req.Stream,
		ServiceTier: req.ServiceTier,
		Reasoning:   req.Reasoning,
		Input:       c.messages, // Use full message history
	}

	// Extract and save text content from request
	if len(reqCopy.Input) > 0 {
		var inputTexts []string
		for i, msg := range reqCopy.Input {
			for _, content := range msg.Content {
				if content.Type == "input_text" && content.Text != "" {
					// Collect all input texts
					inputTexts = append(inputTexts, fmt.Sprintf("=== Message %d (Role: %s) ===\n%s", i+1, msg.Role, content.Text))
				}
			}
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
	if len(respCopy.Output) > 0 {
		val := ""
		for _, output := range respCopy.Output {
			for _, content := range output.Content {
				if content.Type == "output_text" && content.Text != "" {
					val += content.Text + "\n"
				}
			}
		}
		textPath := GetTimestampedAgentsPath("text", fmt.Sprintf("%05d.output.txt", logNum))
		err := os.WriteFile(textPath, []byte(val), 0644)
		if err != nil {
			return fmt.Errorf("failed to write output text: %w", err)
		}
	}

	// fmt.Println(GetTimestampedAgentsPath("api", fmt.Sprintf("%05d.*.json", logNum)))

	jsonPath := GetTimestampedAgentsPath("api", fmt.Sprintf("%05d.input.json", logNum))
	err := os.WriteFile(jsonPath, []byte(util.Pformat(reqWithHistory)), 0644)
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
func (c *OpenAIClient) GetTokenUsage(resp any) (promptTokens, completionTokens, totalTokens int) {
	oResp := resp.(*openai.Response)
	promptTokens = oResp.Usage.InputTokens
	completionTokens = oResp.Usage.OutputTokens
	totalTokens = oResp.Usage.TotalTokens
	return
}

// GetDetailedUsage returns detailed token usage from OpenAI API responses
// OpenAI doesn't provide cache tokens, so cache fields are always zero
func (c *OpenAIClient) GetDetailedUsage(resp any) TokenUsage {
	oResp := resp.(*openai.Response)
	return TokenUsage{
		Input:  oResp.Usage.InputTokens,
		Output: oResp.Usage.OutputTokens,
		Cache: CacheUsage{
			Read:  0, // OpenAI doesn't provide cache metrics
			Write: 0,
		},
	}
}

// GetResponseText extracts the text content from an OpenAI response
func GetOpenAIResponseText(resp *openai.Response) string {
	if len(resp.Output) > 0 && len(resp.Output[0].Content) > 0 {
		return resp.Output[0].Content[0].Text
	}
	return ""
}

// CompactMessages is a no-op for OpenAI since it uses server-side conversation state.
// OpenAI tracks messages via previous_message_id, not local history.
// Returns 0 since no messages are removed locally.
func (c *OpenAIClient) CompactMessages(_ int) CompactionResult {
	if true {
		panic("no compaction yet")
	}
	return CompactionResult{MessagesRemoved: 0, TokensRemoved: 0}
}
