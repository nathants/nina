package run

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/alexflint/go-arg"
	"github.com/nathants/nina/lib"
	"github.com/nathants/nina/prompts"
	claude "github.com/nathants/nina/providers/claude"
	grok "github.com/nathants/nina/providers/grok"
	groq "github.com/nathants/nina/providers/groq"
	openai "github.com/nathants/nina/providers/openai"
	util "github.com/nathants/nina/util"
)

func init() {
	lib.Commands["run"] = run
	lib.Args["run"] = runArgs{}
}

type runArgs struct {
	Model     string `arg:"-m,--model" default:"o3" help:"o3, gemini, opus, sonnet, grok, k2"`
	MaxTokens int    `arg:"-t,--max-tokens" default:"200000" help:"Maximum tokens to use"`
	Debug     bool   `arg:"-d,--debug" help:"Show raw NinaInput and NinaOutput XML content"`
	UUID      string `arg:"--uuid" help:"UUID for process tracking (used by integration tests)"`
	Continue  bool   `arg:"-c,--continue" help:"Continue the last conversation from agents/api/*.input.json"`
}

func (runArgs) Description() string {
	return `Run nina`
}

// PreviousConversation represents restored conversation state from a previous run
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

	// Find the latest timestamp directory
	var latestTimestamp string
	for _, entry := range entries {
		if entry.IsDir() && len(entry.Name()) == 15 { // YYYYMMDD-HHMMSS format
			if entry.Name() > latestTimestamp {
				latestTimestamp = entry.Name()
			}
		}
	}

	if latestTimestamp == "" {
		return "", "", fmt.Errorf("no previous conversation found")
	}

	// Find all input.json files in the latest timestamp directory
	pattern := filepath.Join(apiDir, latestTimestamp, "*.input.json")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return "", "", err
	}

	if len(files) == 0 {
		return "", "", fmt.Errorf("no conversation files found in %s", latestTimestamp)
	}

	// Sort files to get the latest (they're numbered sequentially)
	sort.Strings(files)
	inputPath = files[len(files)-1]

	// Get corresponding output file
	outputPath = strings.Replace(inputPath, ".input.json", ".output.json", 1)

	// Check if output file exists
	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		return "", "", fmt.Errorf("output file not found: %s", outputPath)
	}

	return inputPath, outputPath, nil
}

// loadPreviousConversation loads conversation history from the latest session
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

	return prev, nil
}

// LoopState tracks the state of the running loop
type LoopState struct {
	TokensUsed    int // Cumulative tokens used across ALL iterations
	MaxTokens     int // Maximum allowed tokens for entire session
	StartTime     time.Time
	IterStartTime time.Time
	MessagesCount int
	LastResults   []string       // Store last command results for feedback
	AIProvider    lib.AIProvider // Generic AI provider (OpenAI or Claude)
	Model         string         // Model name for display
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
	SessionUsage lib.SessionUsage // Tracks cumulative input and cache metrics
}

func run() {
	var args runArgs
	arg.MustParse(&args)

	// Initialize session for proper log numbering
	lib.InitializeSession(args.Continue)

	// Set NINA_UUID env var if UUID flag provided
	if args.UUID != "" {
		_ = os.Setenv("NINA_UUID", args.UUID)
	}

	// Create AI provider based on model selection
	var provider lib.AIProvider
	var err error

	switch args.Model {
	case "sonnet", "4-sonnet", "opus", "4-opus":
		provider, err = lib.NewClaudeClient()
		if err != nil {
			logStderr("Failed to create Claude client: %v", err)
			os.Exit(1)
		}
	case "o3", "o4-mini", "o4-mini-flex", "gpt-4.1":
		provider, err = lib.NewOpenAIClient()
		if err != nil {
			logStderr("Failed to create OpenAI client: %v", err)
			os.Exit(1)
		}
	case "grok":
		provider, err = lib.NewGrokClient()
		if err != nil {
			logStderr("Failed to create Grok client: %v", err)
			os.Exit(1)
		}
	case "k2":
		provider, err = lib.NewGroqClient()
		if err != nil {
			logStderr("Failed to create Groq client: %v", err)
			os.Exit(1)
		}
	case "gemini":
		provider, err = lib.NewGeminiClient()
		if err != nil {
			logStderr("Failed to create Gemini client: %v", err)
			os.Exit(1)
		}
	default:
		logStderr("Unknown model: %s", args.Model)
		os.Exit(1)
	}

	state := &LoopState{
		MaxTokens:  args.MaxTokens,
		StartTime:  time.Now(),
		AIProvider: provider,
		Model:      args.Model,
		SessionUsage: lib.SessionUsage{
			InputLimitStatus: "ok",
		},
	}

	// Initialize logging
	if err := initLogging(); err != nil {
		logStderr("Failed to initialize logging: %v", err)
		os.Exit(1)
	}

	// Restore conversation state if --continue flag is set
	if args.Continue {
		prev, err := loadPreviousConversation()
		if err != nil {
			logStderr("Warning: Could not load previous conversation: %v", err)
			logStderr("Starting new conversation instead.")
		} else {
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
			if prevModel != args.Model {
				logStderr("ERROR: Cannot continue conversation from model '%s' with model '%s'", prevModel, args.Model)
				logStderr("The --continue flag only works when using the same model as the previous conversation.")
				os.Exit(1)
			}

			// For OpenAI, use response ID for continuation
			if openaiClient, ok := provider.(*lib.OpenAIClient); ok {
				if prev.ResponseID != "" {
					openaiClient.RestoreResponseID(prev.ResponseID)
					logStderr("Successfully restored OpenAI conversation with response ID: %s", prev.ResponseID)

					// Count messages for state
					userMessages := 0
					for _, msg := range prev.Messages {
						if msgMap, ok := msg.(map[string]any); ok {
							if role, ok := msgMap["role"].(string); ok && role == "user" {
								userMessages++
							}
						}
					}
					state.MessagesCount = userMessages
				} else {
					logStderr("ERROR: No response ID found for OpenAI continuation")
					os.Exit(1)
				}
			} else if claudeClient, ok := provider.(*lib.ClaudeClient); ok {
				// For Claude providers, restore message history
				if len(prev.Messages) > 0 {
					if err := claudeClient.RestoreMessages(prev.Messages); err != nil {
						logStderr("ERROR: Failed to restore Claude conversation: %v", err)
						os.Exit(1)
					}
					logStderr("Successfully restored Claude conversation with %d messages", len(prev.Messages))

					// Count user messages for state
					userMessages := 0
					for _, msg := range prev.Messages {
						if msgMap, ok := msg.(map[string]any); ok {
							if role, ok := msgMap["role"].(string); ok && role == "user" {
								userMessages++
							}
						}
					}
					state.MessagesCount = userMessages
				} else {
					logStderr("ERROR: No messages found for Claude continuation")
					os.Exit(1)
				}
			} else {
				// For other providers, continuation is not supported
				logStderr("ERROR: Continuation is currently only supported for OpenAI models (o3, o4-mini) and Claude models (opus, sonnet)")
				logStderr("Other models do not support the --continue flag.")
				os.Exit(1)
			}
		}
	}

	// Load system prompt
	systemPrompt := loadSystemPrompt() + "\n\n" + loadXmlPrompt()

	// Show system prompt in debug mode
	if args.Debug {
		highlighted := lib.HighlightNinaTags(fmt.Sprintf("%s\n%s\n%s\n", util.NinaSystemPromptStart, systemPrompt, util.NinaSystemPromptEnd))
		fmt.Fprintf(os.Stderr, "%s", highlighted)
	}

	// Read initial message from stdin
	var stdinContent string
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			logStderr("Failed to read stdin: %v", err)
			os.Exit(1)
		}
		stdinContent = strings.TrimSpace(string(data))
	}

	// Main loop
	for {
		// Increment step counter
		state.StepNumber++

		// Track iteration start time
		state.IterStartTime = time.Now()

		// Build message
		userMessage, err := buildUserMessage(state, stdinContent, args.Debug)
		if err != nil {
			logStderr("Failed to build user message: %v", err)
			break
		}

		// Show NinaInput in debug mode
		if args.Debug {
			// userMessage already contains NinaInput wrapper
			highlighted := lib.HighlightNinaTags(userMessage + "\n")
			fmt.Fprintf(os.Stderr, "%s", highlighted)
		}

		// Call AI provider
		response, err := callAIProvider(state.AIProvider, args.Model, systemPrompt, userMessage, state)
		if err != nil {
			logStderr("Failed to call AI provider: %s %v", args.Model, err)
			break
		}

		// Show NinaOutput in debug mode
		if args.Debug {
			// Don't wrap in NinaOutput tags - response already contains them
			highlighted := lib.HighlightNinaTags(response + "\n")
			fmt.Fprintf(os.Stderr, "%s", highlighted)
		}

		// Process response
		stopReason := processResponse(response, state, args.Debug)

		// Print status bar after all content
		printStatusBar(state)

		if stopReason != "" {
			logStderr("%s", stopReason)
			break
		}
		// Clear stdin content after first message of this run
		if state.StepNumber == 1 {
			stdinContent = ""
		}

		state.MessagesCount++
	}
	// Final report
	logStderr("Loop completed after %s", time.Since(state.StartTime))
}

func initLogging() error {
	// Create agents directory if it doesn't exist
	agentsDir := util.GetAgentsDir()
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		return fmt.Errorf("failed to create agents directory: %v", err)
	}

	// Create subdirectories if they don't exist
	for _, subdir := range []string{"api", "text"} {
		dir := filepath.Join(agentsDir, subdir)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create %s directory: %v", dir, err)
		}
	}

	return nil
}
func loadSystemPrompt() string {
	fileName := "SYSTEM.md"
	fmt.Println("adding", fileName)
	data, err := prompts.EmbeddedFiles.ReadFile(fileName)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func loadXmlPrompt() string {
	fileName := "XML.md"
	fmt.Println("adding", fileName)
	data, err := prompts.EmbeddedFiles.ReadFile(fileName)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func loadMemoryPrompt() string {
	fileName := "MEMORY.md"
	fmt.Println("adding", fileName)
	data, err := prompts.EmbeddedFiles.ReadFile(fileName)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func loadCodingPrompt() string {
	fileName := "CODING.md"
	fmt.Println("adding", fileName)
	data, err := prompts.EmbeddedFiles.ReadFile(fileName)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func buildUserMessage(state *LoopState, stdinContent string, debug bool) (string, error) {
	var outerParts []string // Parts that go outside NinaInput
	var innerParts []string // Parts that go inside NinaInput

	// if state.MessagesCount == 0 {
	// 	outerParts = append(outerParts, loadProcessPrompt())
	// }

	// Build the NinaPrompt content by combining parts
	var promptContent []string

	// Add env tag (only on first message of this run, outside NinaInput)
	if state.StepNumber == 1 {
		projectDir := util.GetGitRoot()
		if projectDir == "" {
			// Fall back to current directory
			projectDir, _ = os.Getwd()
		}
		isGitRepo := "no"
		if util.GetGitRoot() != "" {
			isGitRepo = "yes"
		}
		envTag := fmt.Sprintf(`
<env>
  <projectDirectory>%s</projectDirectory>
  <isGitRepo>%s</isGitRepo>
  <date>%s</date>
</env>
`, projectDir, isGitRepo, time.Now().Format("Mon Jan 02 2006"))
		outerParts = append(outerParts, envTag)
	}

	if state.StepNumber == 1 {
		outerParts = append(outerParts, loadMemoryPrompt())
		outerParts = append(outerParts, loadCodingPrompt())
	}

	if state.StepNumber == 1 {
		taskContent := ""
		for _, path := range []string{"TASK.md", util.GetGitRoot() + "/TASK.md"} {
			if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
				taskContent = strings.TrimSpace(string(data))

				// Truncate the file
				if err := os.WriteFile(path, []byte{}, 0644); err != nil {
					logStderr("Failed to truncate %s: %v", path, err)
				}
				break
			}
		}
		// Store initial prompt content for future messages
		var initialParts []string
		if taskContent != "" {
			fmt.Println("adding TASK.md")
			promptContent = append(promptContent, taskContent)
			initialParts = append(initialParts, taskContent)
		}
		if stdinContent != "" {
			promptContent = append(promptContent, stdinContent)
			initialParts = append(initialParts, stdinContent)
		}
		if len(initialParts) > 0 {
			state.InitialPrompt = strings.Join(initialParts, "\n\n")
		}
	}


	if data, err := os.ReadFile("SUGGEST.md"); err == nil && len(data) > 0 {
		suggestContent := strings.TrimSpace(string(data))
		promptContent = append(promptContent, fmt.Sprintf("\n\n%s\n%s\n%s", util.NinaSuggestionStart, suggestContent, util.NinaSuggestionEnd))
		fmt.Println("adding SUGGEST.md")
		if err := os.WriteFile("SUGGEST.md", []byte{}, 0644); err != nil {
			logStderr("Failed to truncate SUGGEST.md: %v", err)
		}
	}

	if len(promptContent) == 0 {
		suggestContent := "continue, you have not output <NinaStop> yet"
		promptContent = append(promptContent, fmt.Sprintf("\n\n%s\n%s\n%s", util.NinaSuggestionStart, suggestContent, util.NinaSuggestionEnd))
	}

	// Create NinaPrompt tag only on first message
	if len(promptContent) > 0 {
		combinedPrompt := strings.Join(promptContent, "\n\n")
		if state.MessagesCount == 0 {
			// First message: wrap in NinaPrompt tags
			val := fmt.Sprintf("%s\n%s\n%s", util.NinaPromptStart, combinedPrompt, util.NinaPromptEnd)
			innerParts = append(innerParts, val)
		} else {
			// Subsequent messages: add content directly without NinaPrompt tags
			innerParts = append(innerParts, combinedPrompt)
		}
	}

	// Add last results if any
	if len(state.LastResults) > 0 {
		innerParts = append(innerParts, state.LastResults...)
		// Clear results after including them
		state.LastResults = nil
	}

	if debug {
		fmt.Println(strings.Join(innerParts, "\n"))
	}

	// Combine outer parts and wrapped inner parts
	var result []string
	result = append(result, outerParts...)
	if len(innerParts) > 0 {
		result = append(result, fmt.Sprintf("%s\n%s\n%s", util.NinaInputStart, strings.Join(innerParts, "\n\n"), util.NinaInputEnd))
	}

	val := strings.Join(result, "\n\n")
	return val, nil
}

func callAIProvider(provider lib.AIProvider, model, systemPrompt, userMessage string, state *LoopState) (string, error) {
	ctx := context.Background()
	// fmt.Println("call provider")
	// then := time.Now()
	resp, err := provider.CallWithStore(ctx, model, systemPrompt, userMessage)
	if err != nil {
		return "", err
	}
	// fmt.Println("provider returned", time.Since(then))
	// Update token usage with detailed tracking from API
	promptTokens, _, total := provider.GetTokenUsage(resp)

	// Get detailed usage for cache tracking and input limit monitoring
	detailedUsage := provider.GetDetailedUsage(resp)

	// Include cache read tokens in total token count
	cacheReadTokens := max(detailedUsage.Cache.Read, 0)
	totalWithCache := total + cacheReadTokens
	state.TokensUsed += totalWithCache
	state.TokensUsed += totalWithCache
	// Update session usage with accurate API values
	state.SessionUsage.TotalTokens.Input += detailedUsage.Input
	state.SessionUsage.TotalTokens.Output += detailedUsage.Output
	state.SessionUsage.TotalTokens.Cache.Read += detailedUsage.Cache.Read
	state.SessionUsage.TotalTokens.Cache.Write += detailedUsage.Cache.Write

	// Track cumulative input for limit monitoring (input + cache write)
	state.SessionUsage.SessionInput += detailedUsage.Input + detailedUsage.Cache.Write
	state.SessionUsage.CurrentInput = detailedUsage.Input + detailedUsage.Cache.Write

	// Trigger compaction at 85% of token limit to prevent API failures
	compactionThreshold := 0.85
	if float64(state.SessionUsage.SessionInput) >= float64(state.MaxTokens)*compactionThreshold &&

		false { // disabled for now

		// Compact messages keeping recent context (3 message pairs)
		result := provider.CompactMessages(3)
		if result.MessagesRemoved > 0 {
			// Adjust SessionInput by subtracting removed tokens
			state.SessionUsage.SessionInput -= result.TokensRemoved

			fmt.Printf("\n%s>>> Message compaction triggered at %.1f%% of token limit%s\n",
				lib.ColorMagenta,
				float64(state.SessionUsage.SessionInput+result.TokensRemoved)/float64(state.MaxTokens)*100,
				lib.ColorReset)
			fmt.Printf("%s>>> Removed %d old messages (~%d tokens) to preserve token budget%s\n",
				lib.ColorMagenta, result.MessagesRemoved, result.TokensRemoved, lib.ColorReset)
			fmt.Printf("%s>>> Token usage now at %.1f%% of limit%s\n\n",
				lib.ColorMagenta,
				float64(state.SessionUsage.SessionInput)/float64(state.MaxTokens)*100,
				lib.ColorReset)
		}
	}

	// Calculate cache hit ratio for the session
	// Calculate cache hit ratio for the session
	totalNeeded := state.SessionUsage.TotalTokens.Cache.Read + state.SessionUsage.TotalTokens.Input
	if totalNeeded > 0 && state.SessionUsage.TotalTokens.Cache.Read > 0 {
		state.SessionUsage.CacheHitRatio = float64(state.SessionUsage.TotalTokens.Cache.Read) / float64(totalNeeded) * 100
	}
	// Check input limit and show warnings
	status, showWarning := lib.CheckInputLimit(state.SessionUsage.SessionInput, state.MaxTokens)
	state.SessionUsage.InputLimitStatus = status

	_ = showWarning
	// if showWarning {
	// 	warningColor := lib.ColorYellow
	// 	if status == "alert" {
	// 		warningColor = lib.ColorMagenta
	// 	} else if status == "critical" {
	// 		warningColor = lib.ColorRed
	// 	}
	// 	fmt.Printf("\n%s>>> Input token limit %s: %d/%d tokens (%.1f%%)%s\n\n",
	// 		warningColor,
	// 		status,
	// 		state.SessionUsage.SessionInput,
	// 		state.MaxTokens,
	// 		float64(state.SessionUsage.SessionInput)/float64(state.MaxTokens)*100,
	// 		lib.ColorReset)
	// }

	// Store timing info in state for later display
	state.IterDuration = time.Since(state.IterStartTime)
	state.TotalDuration = time.Since(state.StartTime)
	state.PromptTokens = promptTokens

	// Update cache tokens for display (from detailed usage)
	state.CachedTokens = detailedUsage.Cache.Read

	// Extract response text based on provider type
	var response string
	switch r := resp.(type) {
	case *openai.Response:
		response = lib.GetOpenAIResponseText(r)
		state.CachedTokens = r.Usage.InputTokensDetails.CachedTokens
	case *claude.Response:
		response = lib.GetClaudeResponseText(r)
		// Use cache metrics from Claude response
		state.CachedTokens = r.Usage.CacheReadTokens
		// Total input = new input tokens + cached tokens read + cached tokens written
		totalInput := r.Usage.InputTokens + r.Usage.CacheReadTokens + r.Usage.CacheWriteTokens
		// Update prompt tokens to reflect total input for display
		state.PromptTokens = totalInput
	case *grok.Response:
		response = lib.GetGrokResponseText(r)
		// Grok doesn't provide cache metrics currently
		state.CachedTokens = 0
	case *groq.HandleResponse:
		response = lib.GetGroqResponseText(r)
		// Groq doesn't provide cache metrics currently
		state.CachedTokens = 0
	case *lib.GeminiResponse:
		response = lib.GetGeminiResponseText(r)
		// Gemini doesn't provide cache metrics currently
		state.CachedTokens = 0
	default:
		return "", fmt.Errorf("unknown response type: %T", resp)
	}

	return response, nil
}

func formatTokens(tokens int) string {
	if tokens >= 1000 {
		return fmt.Sprintf("%dk", tokens/1000)
	}
	return fmt.Sprintf("%d", tokens)
}

func printStatusBar(state *LoopState) {
	// Calculate and format timing info
	iterMinutes := int(state.IterDuration.Minutes())
	iterSeconds := int(state.IterDuration.Seconds()) % 60
	totalMinutes := int(state.TotalDuration.Minutes())
	totalSeconds := int(state.TotalDuration.Seconds()) % 60

	// Use SessionInput for display (cumulative input tokens only)
	inputTokensDisplay := formatTokens(state.SessionUsage.SessionInput)
	maxTokensDisplay := formatTokens(state.MaxTokens)
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
	fmt.Fprintf(os.Stderr, "%s%s%s\n", lib.ColorYellow, separator, lib.ColorReset)

	// Print content with red text and yellow == wrappers
	fmt.Fprintf(os.Stderr, "%s==%s%s%s%s%s==%s\n",
		lib.ColorYellow, lib.ColorReset, lib.ColorRed, content, lib.ColorReset, lib.ColorYellow, lib.ColorReset)

	// Print bottom separator in yellow
	fmt.Fprintf(os.Stderr, "%s%s%s\n", lib.ColorYellow, separator, lib.ColorReset)
}

func processResponse(response string, state *LoopState, debug bool) string {
	// Write response to stderr for reasoning output only in non-debug mode
	if !debug {
		// Extract only the NinaMessage content to display
		message, err := util.ExtractNinaMessage(response)
		if err != nil {
			// Log the error but don't break the loop - NinaMessage is optional
			logStderr("Warning: Failed to extract NinaMessage: %v", err)
			// Don't show the full response as fallback - it's too noisy
		} else if message != "" {
			// Show only the message content in magenta
			fmt.Fprintf(os.Stderr, "%s%s%s\n", lib.ColorMagenta, message, lib.ColorReset)
		}
		// If no NinaMessage found, don't show anything (it's optional)
	}

	// Process the output
	result := lib.ProcessOutput(response, nil, debug)

	// Check for NinaOutput error
	if result.Error != nil {
		// Convert parsing errors to NinaSuggestion instead of breaking the loop
		errorMsg := result.Error.Error()
		if strings.Contains(errorMsg, "failed to extract NinaOutput") ||
			strings.Contains(errorMsg, "no valid NinaOutput found") {
			// Create a suggestion to help the AI fix its output format
			suggestion := fmt.Sprintf("Your previous response had a formatting error: %s\n", errorMsg)
			suggestion += "Please ensure your response is wrapped in proper <NinaOutput> tags and try again.\n"
			suggestion += "Remember: Your entire response MUST be a single <NinaOutput> XML block."

			// Write suggestion to SUGGEST.md for the next iteration
			if err := os.WriteFile("SUGGEST.md", []byte(suggestion), 0644); err != nil {
				logStderr("Failed to write SUGGEST.md: %v", err)
			}

			// Log the error for visibility
			logStderr("Parsing error recovered: %s", errorMsg)

			// Return empty to continue the loop
			return ""
		}
		// For other errors, still break the loop
		return fmt.Sprintf("Error: %v", result.Error)
	}

	// Log events
	for _, event := range result.Events {
		description := ""
		switch event.Type {
		case "NinaStop":
			description = fmt.Sprintf("reason: %s", event.Reason)
		case "NinaChange":
			description = fmt.Sprintf("%s %d", event.Filepath, event.LinesChanged)
		case "NinaBash":
			description = strings.Join(event.Args[1:], " ")
		}
		if !debug {
			// Custom logging for different event types
			if event.Type == "NinaBash" {
				// Count output lines
				stdoutLines := 0
				if event.Stdout != "" {
					stdoutLines = strings.Count(event.Stdout, "\n")
					if !strings.HasSuffix(event.Stdout, "\n") {
						stdoutLines++
					}
				}
				stderrLines := 0
				if event.Stderr != "" {
					stderrLines = strings.Count(event.Stderr, "\n")
					if !strings.HasSuffix(event.Stderr, "\n") {
						stderrLines++
					}
				}

				fmt.Fprintf(os.Stderr, "%s| Done [%s] [%d %d %d] |%s\n",
					lib.ColorCyan, strings.Join(event.Args[1:], " "), event.ExitCode, stdoutLines, stderrLines, lib.ColorReset)

			} else if event.Type == "NinaChange" {
				// Print change in blue
				fmt.Fprintf(os.Stderr, "%s| Change [%s] |%s\n",
					lib.ColorGreen, description, lib.ColorReset)
			} else {
				// For other events, use original format
				logEvent(lib.Event{
					Name:        event.Type,
					TokensUsed:  state.TokensUsed,
					MaxTokens:   state.MaxTokens,
					Description: description,
				})
			}
		}
	}

	// Store results for next input
	state.LastResults = result.Results

	return result.StopReason
}

func logEvent(event lib.Event) {
	lib.LogEvent(event)
}

func logStderr(format string, args ...any) {
	lib.ColoredStderr(format, args...)
}
