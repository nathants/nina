// Core conversation loop functionality shared by nina run and nina tools commands.
package lib

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nathants/nina/util"

	claude "github.com/nathants/nina/providers/claude"
	grok "github.com/nathants/nina/providers/grok"
	groq "github.com/nathants/nina/providers/groq"
	openai "github.com/nathants/nina/providers/openai"
)

// LoopState tracks the state of the running conversation loop.
type LoopState struct {
	TokensUsed    int // Cumulative tokens used across ALL iterations
	MaxTokens     int // Maximum allowed tokens for entire session
	StartTime     time.Time
	IterStartTime time.Time
	MessagesCount int
	LastResults   []string   // Store last command results for feedback
	AIProvider    AIProvider // Generic AI provider (OpenAI or Claude)
	Model         string     // Model name for display
	// Timing info for status bar
	IterDuration  time.Duration
	TotalDuration time.Duration
	CachedTokens  int // Tokens cached in CURRENT prompt only
	PromptTokens  int // Total tokens in CURRENT prompt (cached + new)
	// Session-wide cache tracking
	TotalCachedTokens int // Cumulative cached tokens across ALL iterations
	// Initial prompt content to include in every message
	InitialPrompt string // Combined stdin + task.md content from first message
	StepNumber    int    // Current step/iteration number
	// Accurate API token tracking for input limits and cache ratio
	SessionUsage SessionUsage // Tracks cumulative input and cache metrics
}

// ToolProcessor defines how tools are handled in the conversation loop.
// Implementations handle different tool calling formats (XML tags vs JSON).
// The processor is responsible for parsing AI responses, executing tools,
// and formatting results for the next conversation turn.
type ToolProcessor interface {
	// ProcessResponse parses the AI response and executes any tool calls.
	// It extracts tool invocations (NinaBash, NinaChange, etc), executes them,
	// and returns results to be included in the next user message.
	// Also handles special cases like NinaStop to end the conversation.
	ProcessResponse(response string, state *LoopState) ProcessorResult

	// GetSystemPrompt returns the system prompt for this tool format.
	// This includes instructions on how to use tools, formatting requirements,
	// and any mode-specific behavior (e.g., XML tags vs JSON tool calls).
	GetSystemPrompt() string

	// FormatUserMessage formats the user input for the next conversation turn.
	// On the first message (StepNumber == 1), it wraps the initial prompt.
	// On subsequent messages, it includes tool execution results and suggestions.
	// Handles SUGGEST.md content and maintains conversation context.
	FormatUserMessage(state *LoopState, content string) (string, error)
}

// ProcessorResult is defined in processors.go

// LoopConfig configures the conversation loop behavior.
type LoopConfig struct {
	Model         string
	MaxTokens     int
	Debug         bool
	UUID          string
	Continue      bool
	ToolProcessor ToolProcessor
	StdinContent  string // Initial content from stdin
}

// LogStderr logs a message to stderr with timestamp.
func LogStderr(format string, args ...any) {
	timestamp := time.Now().Format("15:04:05")
	message := fmt.Sprintf(format, args...)
	fmt.Fprintf(os.Stderr, "[%s] %s\n", timestamp, message)
}

// FormatTokens formats token counts for display (e.g., 1500 -> "1k").
func FormatTokens(tokens int) string {
	if tokens >= 1000 {
		return fmt.Sprintf("%dk", tokens/1000)
	}
	return fmt.Sprintf("%d", tokens)
}

// PrintStatusBar prints the conversation status bar to stderr.
func PrintStatusBar(state *LoopState) {
	// Calculate and format timing info
	iterMinutes := int(state.IterDuration.Minutes())
	iterSeconds := int(state.IterDuration.Seconds()) % 60
	totalMinutes := int(state.TotalDuration.Minutes())
	totalSeconds := int(state.TotalDuration.Seconds()) % 60

	// Use SessionInput for display (cumulative input tokens only)
	inputTokensDisplay := FormatTokens(state.SessionUsage.SessionInput)
	maxTokensDisplay := FormatTokens(state.MaxTokens)
	inputPercent := 0
	if state.MaxTokens > 0 {
		inputPercent = (state.SessionUsage.SessionInput * 100) / state.MaxTokens
	}

	// Format time strings
	totalTime := fmt.Sprintf("%dm%ds", totalMinutes, totalSeconds)
	if totalMinutes == 0 {
		totalTime = fmt.Sprintf("%ds", totalSeconds)
	}
	iterTime := fmt.Sprintf("%dm%ds", iterMinutes, iterSeconds)
	if iterMinutes == 0 {
		iterTime = fmt.Sprintf("%ds", iterSeconds)
	}

	// Calculate cumulative cache percentage (what % of all session input came from cache)
	cumulativeCachePercent := int(state.SessionUsage.CacheHitRatio)

	content := fmt.Sprintf(" %s [%d %s %s] [cached %d%%] [%s/%s input (%d%%)] ",
		state.Model,
		state.StepNumber,
		totalTime, iterTime,
		cumulativeCachePercent,
		inputTokensDisplay, maxTokensDisplay, inputPercent)

	// Calculate separator length to match content
	separatorLen := len(content) + 4
	separator := strings.Repeat("=", separatorLen)

	// Print top separator in yellow
	fmt.Fprintf(os.Stderr, "%s%s%s\n", ColorYellow, separator, ColorReset)

	// Print content with padding in yellow
	fmt.Fprintf(os.Stderr, "%s| %s |%s\n", ColorYellow, content, ColorReset)

	// Print bottom separator in yellow
	fmt.Fprintf(os.Stderr, "%s%s%s\n", ColorYellow, separator, ColorReset)
}

// RunLoop runs the main conversation loop with the given configuration.
func RunLoop(config LoopConfig) error {
	// Set UUID env var if provided
	if config.UUID != "" {
		_ = os.Setenv("NINA_UUID", config.UUID)
	}

	// Create AI provider based on model selection
	provider, model, err := CreateProviderForModel(config.Model)
	if err != nil {
		return fmt.Errorf("failed to create provider: %w", err)
	}

	// Validate ToolProcessor is set
	if config.ToolProcessor == nil {
		return fmt.Errorf("ToolProcessor is required but not set")
	}

	// Initialize state
	state := &LoopState{
		MaxTokens:     config.MaxTokens,
		StartTime:     time.Now(),
		AIProvider:    provider,
		Model:         model,
		SessionUsage:  SessionUsage{},
		InitialPrompt: config.StdinContent,
	}

	// Handle continuation if requested
	if err := HandleContinuation(config, provider); err != nil {
		return err
	}
	// Get system prompt from tool processor
	systemPrompt := config.ToolProcessor.GetSystemPrompt()

	// Track stdin content for first message
	stdinContent := config.StdinContent

	// Main loop
	for {
		// Increment step counter
		state.StepNumber++

		// Track iteration start time
		state.IterStartTime = time.Now()

		// Build message using tool processor
		userMessage, err := config.ToolProcessor.FormatUserMessage(state, stdinContent)
		if err != nil {
			return fmt.Errorf("failed to build user message: %w", err)
		}

		// Show input in debug mode
		if config.Debug {
			highlighted := HighlightNinaTags(userMessage + "\n")
			fmt.Fprintf(os.Stderr, "%s", highlighted)
		}

		// Call AI provider
		response, err := CallAIProvider(provider, model, systemPrompt, userMessage, state)
		if err != nil {
			return fmt.Errorf("failed to call AI provider: %w", err)
		}

		// Show output in debug mode
		if config.Debug {
			highlighted := HighlightNinaTags(response + "\n")
			fmt.Fprintf(os.Stderr, "%s", highlighted)
		}

		// Process response using tool processor
		result := config.ToolProcessor.ProcessResponse(response, state)

		// Store results for next input
		state.LastResults = result.Results

		// Update timings
		state.IterDuration = time.Since(state.IterStartTime)
		state.TotalDuration = time.Since(state.StartTime)

		// Print status bar after processing
		PrintStatusBar(state)

		// Check for stop condition
		if result.StopReason != "" {
			LogStderr("%s", result.StopReason)
			break
		}

		// Clear stdin content after first message
		if state.StepNumber == 1 {
			stdinContent = ""
		}
	}

	return nil
}

// CreateProviderForModel creates the appropriate AI provider for the given model.
func CreateProviderForModel(model string) (AIProvider, string, error) {
	switch model {
	case "sonnet", "4-sonnet", "opus", "4-opus":
		provider, err := NewClaudeClient()
		if err != nil {
			return nil, "", err
		}
		return provider, model, nil

	case "o3", "o3-flex", "o4-mini", "o4-mini-flex", "gpt-4.1":
		provider, err := NewOpenAIClient()
		if err != nil {
			return nil, "", err
		}
		return provider, model, nil

	case "grok":
		provider, err := NewGrokClient()
		if err != nil {
			return nil, "", err
		}
		return provider, "grok-beta", nil

	case "k2":
		provider, err := NewGroqClient()
		if err != nil {
			return nil, "", err
		}
		return provider, "moonshotai/kimi-k2-instruct", nil

	case "gemini":
		provider, err := NewGeminiClient()
		if err != nil {
			return nil, "", err
		}
		return provider, "gemini-2.5-pro", nil

	default:
		return nil, "", fmt.Errorf("unknown model: %s", model)
	}
}

// CallAIProvider calls the AI provider with the given parameters.
func CallAIProvider(provider AIProvider, model, systemPrompt, userMessage string, state *LoopState) (string, error) {
	ctx := context.Background()

	// Track API call timing
	callStart := time.Now()

	// Call the provider
	resp, err := provider.Call(ctx, model, systemPrompt, userMessage)
	if err != nil {
		return "", err
	}

	// Update timing
	state.IterDuration = time.Since(callStart)

	// Extract response text based on provider type
	var responseText string
	switch r := resp.(type) {
	case *claude.Response:
		responseText = r.Content[0].Text
		// Update token tracking
		cachedTokens := r.Usage.CacheWriteTokens + r.Usage.CacheReadTokens
		updateTokenTracking(state, r.Usage.InputTokens, r.Usage.OutputTokens, cachedTokens)
		updateCacheHitRatio(state, r.Usage.CacheReadTokens, r.Usage.InputTokens)
	case *openai.Response:
		if len(r.Output) > 0 && len(r.Output[0].Content) > 0 && r.Output[0].Content[0].Text != "" {
			responseText = r.Output[0].Content[0].Text
		}
		// Update token tracking
		updateTokenTracking(state, r.Usage.InputTokens, r.Usage.OutputTokens, r.Usage.InputTokensDetails.CachedTokens)
		updateCacheHitRatio(state, r.Usage.InputTokensDetails.CachedTokens, r.Usage.InputTokens)

	case *grok.Response:
		if len(r.Choices) > 0 && r.Choices[0].Message.Content != "" {
			responseText = r.Choices[0].Message.Content
		}
		// Update token tracking
		if usage, ok := r.Usage.(map[string]any); ok {
			if v, ok := usage["completion_tokens"].(float64); ok {
				state.TokensUsed += int(v)
			}
			if v, ok := usage["prompt_tokens"].(float64); ok {
				state.PromptTokens = int(v)
			}
		}

	case *groq.Response:
		if len(r.Choices) > 0 && r.Choices[0].Message.Content != "" {
			responseText = r.Choices[0].Message.Content
		}
		// Update token tracking
		updateTokenTracking(state, r.Usage.PromptTokens, r.Usage.CompletionTokens, 0)

	case *GeminiResponse:
		responseText = r.Text
		// Gemini doesn't provide exact token counts, so we estimate
		// This is handled in GetTokenUsage method

	default:
		return "", fmt.Errorf("unknown response type: %T", resp)
	}

	return responseText, nil
}

// updateTokenTracking updates token usage statistics for the current step.
func updateTokenTracking(state *LoopState, inputTokens, outputTokens, cachedTokens int) {
	if inputTokens > 0 || outputTokens > 0 {
		state.TokensUsed += outputTokens
		state.PromptTokens = inputTokens
		if cachedTokens > 0 {
			state.CachedTokens = cachedTokens
			state.TotalCachedTokens += cachedTokens
		}
		// Update session usage
		state.SessionUsage.SessionInput += inputTokens
		state.SessionUsage.CurrentInput = inputTokens
	}
}

// updateCacheHitRatio calculates and updates the cache hit ratio.
func updateCacheHitRatio(state *LoopState, cachedTokens, inputTokens int) {
	if cachedTokens > 0 {
		totalNeeded := cachedTokens + inputTokens
		if totalNeeded > 0 {
			state.SessionUsage.CacheHitRatio = float64(cachedTokens) / float64(totalNeeded) * 100
		}
	}
}

type PreviousConversation struct {
	Model        string `json:"model"`
	Messages     []any  `json:"messages"`    // Generic messages for Claude/Grok
	ResponseID   string `json:"response_id"` // For OpenAI continuation
	SystemPrompt string `json:"system_prompt"`
}

// findLatestConversation finds the most recent input.json and output.json files
func findLatestConversation() (inputPath string, outputPath string, err error) {
	// Find the latest timestamp directory
	apiDir := util.GetAgentsSubdir("api")

	entries, err := os.ReadDir(apiDir)
	if err != nil {
		return "", "", fmt.Errorf("failed to read api directory: %w", err)
	}

	// Filter and sort timestamp directories
	var timestamps []string
	for _, entry := range entries {
		if entry.IsDir() && len(entry.Name()) == 19 { // YYYY-MM-DD_HH-MM-SS format
			timestamps = append(timestamps, entry.Name())
		}
	}

	if len(timestamps) == 0 {
		return "", "", fmt.Errorf("no previous conversations found")
	}

	// Sort to get the latest
	sort.Strings(timestamps)
	latest := timestamps[len(timestamps)-1]

	// Find the latest numbered files in that directory
	timestampDir := filepath.Join(apiDir, latest)
	entries, err = os.ReadDir(timestampDir)
	if err != nil {
		return "", "", fmt.Errorf("failed to read timestamp directory: %w", err)
	}

	var inputFiles, outputFiles []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, ".input.json") {
			inputFiles = append(inputFiles, name)
		} else if strings.HasSuffix(name, ".output.json") {
			outputFiles = append(outputFiles, name)
		}
	}

	if len(inputFiles) == 0 {
		return "", "", fmt.Errorf("no input files found in latest conversation")
	}

	// Sort to get the latest numbered files
	sort.Strings(inputFiles)
	sort.Strings(outputFiles)

	inputPath = filepath.Join(timestampDir, inputFiles[len(inputFiles)-1])
	if len(outputFiles) > 0 {
		outputPath = filepath.Join(timestampDir, outputFiles[len(outputFiles)-1])
	}

	return inputPath, outputPath, nil
}

// loadPreviousConversation loads the previous conversation state
func loadPreviousConversation() (*PreviousConversation, error) {
	inputPath, outputPath, err := findLatestConversation()
	if err != nil {
		return nil, err
	}

	// Read input file
	inputData, err := os.ReadFile(inputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read input file: %w", err)
	}

	// Parse input JSON to get model and messages
	var inputJSON map[string]any
	if err := json.Unmarshal(inputData, &inputJSON); err != nil {
		return nil, fmt.Errorf("failed to parse input JSON: %w", err)
	}

	prev := &PreviousConversation{}

	// Extract model
	if model, ok := inputJSON["model"].(string); ok {
		prev.Model = model
	}

	// Extract system prompt
	if system, ok := inputJSON["system"].([]any); ok && len(system) > 0 {
		if systemMsg, ok := system[0].(map[string]any); ok {
			if text, ok := systemMsg["text"].(string); ok {
				prev.SystemPrompt = text
			}
		}
	}

	// Read output file to get messages and response ID
	if outputPath != "" {
		outputData, err := os.ReadFile(outputPath)
		if err == nil {
			var outputJSON map[string]any
			if err := json.Unmarshal(outputData, &outputJSON); err == nil {
				// For Claude, extract messages from output.json
				if messages, ok := outputJSON["messages"].([]any); ok {
					prev.Messages = messages
				}
				// For OpenAI, get response ID
				if id, ok := outputJSON["id"].(string); ok {
					prev.ResponseID = id
				}
			}
		}
	}

	return prev, nil

}

// HandleContinuation handles loading and restoring previous conversation state
func HandleContinuation(config LoopConfig, provider AIProvider) error {
	if !config.Continue {
		return nil
	}

	prev, err := loadPreviousConversation()
	if err != nil {
		LogStderr("Warning: Could not load previous conversation: %v", err)
		LogStderr("Starting new conversation instead.")
		return nil
	}

	// Map API model names to CLI model names
	modelMap := map[string]string{
		"claude-opus-4-20250514":      "opus",
		"claude-sonnet-4-20250514":    "sonnet",
		"claude-sonnet-4-20241022":    "4-sonnet",
		"o3":                          "o3",
		"o4-mini":                     "o4-mini",
		"grok-4-0709":                 "grok",
		"moonshotai/kimi-k2-instruct": "k2",
		"gemini-2.5-pro":              "gemini",
	}

	prevModel := modelMap[prev.Model]
	if prevModel == "" {
		prevModel = prev.Model // Use as-is if not in map
	}

	// Check for model mismatch - continuation only works with same model
	if prevModel != config.Model {
		return fmt.Errorf("cannot continue conversation from model '%s' with model '%s'", prevModel, config.Model)
	}

	// Restore conversation state based on provider type
	switch p := provider.(type) {
	case *OpenAIClient:
		if prev.ResponseID != "" {
			p.RestoreResponseID(prev.ResponseID)
			LogStderr("Successfully restored OpenAI conversation with response ID: %s", prev.ResponseID)
		} else if len(prev.Messages) > 0 {
			if err := p.RestoreMessages(prev.Messages); err != nil {
				return fmt.Errorf("failed to restore OpenAI messages: %w", err)
			}
			LogStderr("Successfully restored OpenAI conversation with %d messages", len(prev.Messages))
		}
	case *ClaudeClient:
		if len(prev.Messages) > 0 {
			if err := p.RestoreMessages(prev.Messages); err != nil {
				return fmt.Errorf("failed to restore Claude conversation: %w", err)
			}
			LogStderr("Successfully restored Claude conversation with %d messages", len(prev.Messages))
		}
	default:
		// For other providers, log a warning
		LogStderr("Warning: Continuation not supported for this provider type")
	}

	return nil
}
