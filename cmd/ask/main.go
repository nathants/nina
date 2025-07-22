// ask provides simple one-shot AI interface as nina subcommand
// reads prompt from stdin and outputs AI response with optional streaming
// supports multiple models across different providers
package ask

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"nina/lib"
	"nina/prompts"
	"nina/providers/claude"
	"nina/providers/gemini"
	"nina/providers/grok"
	"nina/providers/groq"
	"nina/providers/oauth"
	"nina/providers/openai"

	"github.com/alexflint/go-arg"
)

func init() {
	lib.Commands["ask"] = ask
	lib.Args["ask"] = askArgs{}
}

var nonAlphaNumRegex = regexp.MustCompile(`[^a-z0-9]+`)

type askArgs struct {
	Model    string `arg:"-m,--model" help:"AI model to use" default:"o3"`
	NoStream bool   `arg:"-r,--no-stream" help:"Disable streaming"`
	Search   bool   `arg:"-s,--search" help:"Enable web search capabilities"`
	Debug    bool   `arg:"-d,--debug" help:"Enable debug output (show JSON)"`
	NoOAuth  bool   `arg:"-n,--no-oauth" help:"Don't use OAuth token if available"`
}

func (askArgs) Description() string {
	return `ask - Simple one-shot AI interface

Reads prompt from stdin.

Supported models (short names):
  - o3
  - o3-flex
  - o3-pro
  - opus
  - opus-batch
  - sonnet
  - sonnet-batch
  - o4-mini
  - o4-mini-flex
  - gemini
  - flash
  - 4.1
  - 4.1-mini
  - v0-md
  - v0-lg
  - ollama
  - grok

Note: Models with -flex suffix use OpenAI's flexible service tier.
Long names also supported for backward compatibility.`
}

var supportedModels = map[string]bool{
	// Short names only
	"o3":           true,
	"o3-flex":      true,
	"o3-pro":       true,
	"opus":         true,
	"opus-batch":   true,
	"sonnet":       true,
	"sonnet-batch": true,
	"o4-mini":      true,
	"o4-mini-flex": true,
	"gemini":       true,
	"flash":        true,
	"4.1":          true,
	"4.1-mini":     true,
	"v0-md":        true,
	"v0-lg":        true,
	"ollama":       true,
	"grok":         true,
	"k2":           true,
}

func ask() {
	var args askArgs
	arg.MustParse(&args)

	// Initialize session for timestamp
	lib.InitializeSession(false)

	if !supportedModels[args.Model] {
		fmt.Fprintf(os.Stderr, "Error: unsupported model: %s\n\n", args.Model)
		fmt.Fprintf(os.Stderr, "Supported models:\n")
		for model := range supportedModels {
			fmt.Fprintf(os.Stderr, "  - %s\n", model)
		}
		os.Exit(1)
	}

	// Read prompt from stdin
	reader := bufio.NewReader(os.Stdin)
	var promptBuilder strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				promptBuilder.WriteString(line)
				break
			}
			fmt.Fprintf(os.Stderr, "Error reading stdin: %v\n", err)
			os.Exit(1)
		}
		promptBuilder.WriteString(line)
	}

	prompt := strings.TrimSpace(promptBuilder.String())
	if prompt == "" {
		fmt.Fprintf(os.Stderr, "Error: no prompt provided via stdin\n")
		os.Exit(1)
	}

	// Generate timestamp for both input and output files
	timestamp := time.Now().Format("2006-01-02T15:04:05")

	// Get git root and create log paths
	gitRoot := getGitRoot()
	sanitizedPrompt := sanitizePrompt(prompt)
	baseFilename := fmt.Sprintf("%s_%s", timestamp, sanitizedPrompt)

	// Create agents/ask directory with timestamp subdirectory
	sessionTimestamp := lib.GetSessionTimestamp()
	agentsDir := fmt.Sprintf("%s/agents/ask/%s", gitRoot, sessionTimestamp)
	err := os.MkdirAll(agentsDir, 0755)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating agents/ask directory: %v\n", err)
		// Fall back to /tmp with timestamp
		agentsDir = fmt.Sprintf("/tmp/ask-%s", sessionTimestamp)
		os.MkdirAll(agentsDir, 0755)
		baseFilename = fmt.Sprintf("ask-%s-%s", args.Model, timestamp)
	}

	// Save input prompt to file
	inputPath := fmt.Sprintf("%s/%s.input", agentsDir, baseFilename)
	err = os.WriteFile(inputPath, []byte(prompt), 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error saving input: %v\n", err)
		os.Exit(1)
	}

	err = runAsk(args.Model, prompt, !args.NoStream, !args.NoOAuth, args.Search, args.Debug, agentsDir, baseFilename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runAsk(model, prompt string, stream bool, useOAuth bool, search bool, debug bool, agentsDir, baseFilename string) error {
	// Set debug environment variable for providers
	if debug {
		_ = os.Setenv("DEBUG", "true")
	}
	// Parse model into provider and model ID
	provider, modelID := parseModel(model)

	var response string
	var err error

	response, err = callProvider(provider, modelID, prompt, stream, useOAuth, search)
	if err != nil {
		return err
	}

	// Save output response to file (only created if API call succeeds)
	outputPath := fmt.Sprintf("%s/%s.output", agentsDir, baseFilename)
	err = os.WriteFile(outputPath, []byte(response), 0644)
	if err != nil {
		return fmt.Errorf("failed to save output: %v", err)
	}

	// Output the response (skip for ollama streaming since it's already output)
	if !(provider == "ollama" && stream) {
		fmt.Print(response)
	}

	return nil
}

func parseModel(model string) (provider, modelID string) {
	switch model {
	case "o3":
		return "openai", "o3-high"
	case "o3-flex":
		return "openai", "o3-flex"
	case "o3-pro":
		return "openai", "o3-pro"
	case "opus":
		return "claude", "claude-4-opus-24k-thinking"
	case "opus-batch":
		return "claude", "claude-4-opus-batch-24k-thinking"
	case "sonnet":
		return "claude", "claude-4-sonnet-24k-thinking"
	case "sonnet-batch":
		return "claude", "claude-4-sonnet-batch-24k-thinking"
	case "o4-mini":
		return "openai", "o4-mini-medium"
	case "o4-mini-flex":
		return "openai", "o4-mini-flex"
	case "gemini":
		return "gemini", "gemini-2.5-pro-32k-thinking"
	case "flash":
		return "gemini", "gemini-2.5-flash-24k-thinking"
	case "4.1":
		return "openai", "gpt-4.1-0.5-temp"
	case "4.1-mini":
		return "openai", "gpt-4.1-mini-0.5-temp"
	case "v0-md":
		return "v0", "v0-1.5-md"
	case "v0-lg":
		return "v0", "v0-1.5-lg"
	case "ollama":
		return "ollama", "ollama"
	case "grok":
		return "grok", "grok-4-0709"
	case "k2":
		return "groq", "moonshotai/kimi-k2-instruct"
	default:
		panic("unknown model: " + model)
	}
}

func callProvider(prov, modelID, message string, stream bool, useOAuth bool, search bool) (string, error) {
	ctx := context.Background()
	systemPrompt := buildSystemPrompt()

	// Setup OAuth for Claude if requested
	if useOAuth && prov == "claude" {
		token, err := oauth.AnthropicAccess()
		if err == nil {
			_ = os.Setenv("ANTHROPIC_OAUTH_TOKEN", token)
		}
	}

	// Setup OAuth for OpenAI if requested
	if useOAuth && prov == "openai" {
		token, err := oauth.OpenAIAccess()
		if err == nil {
			_ = os.Setenv("OPENAI_OAUTH_API_KEY", token)
		}
	}

	// Setup OAuth for Gemini if requested
	if useOAuth && prov == "gemini" {
		token, err := oauth.GeminiAccess()
		if err == nil {
			_ = os.Setenv("GEMINI_OAUTH_TOKEN", token)
		}
	}

	// Create reasoning callback that writes to stderr when streaming is enabled
	var reasoningCallback func(string)
	if stream {
		if prov == "ollama" {
			// For ollama, write to stdout without newlines for inline streaming
			reasoningCallback = func(data string) {
				_, _ = fmt.Fprint(os.Stdout, data)
			}
		} else {
			// For other providers, write reasoning to stderr with newlines
			reasoningCallback = func(data string) {
				fmt.Fprintln(os.Stderr, data)
			}
		}
	}

	switch prov {
	case "openai":
		req := openai.Request{
			Model: convertOpenAIModelID(modelID),
			Input: []openai.ChatMessage{
				{
					Type: "message",
					Role: "system",
					Content: []openai.ContentPart{
						{Type: "input_text", Text: systemPrompt},
					},
				},
				{
					Type: "message",
					Role: "user",
					Content: []openai.ContentPart{
						{Type: "input_text", Text: message},
					},
				},
			},
			Stream: stream,
		}
		if isFlexModel(modelID) {
			req.ServiceTier = "flex"
		}
		if isO3Model(modelID) {
			req.Reasoning = &openai.ReasoningRequest{
				Summary: "auto",
				Effort:  "high",
			}
		}
		if isO4MiniModel(modelID) {
			req.Reasoning = &openai.ReasoningRequest{
				Summary: "auto",
				Effort:  "medium",
			}
		}
		if is41Model(modelID) {
			temp := 0.5
			req.Temperature = &temp
		}
		if search {
			// Use standard tool format for all models
			// req.Tools = []any{provider.CreateOpenAIExaTool()}
			panic("search disabled for now")
		}
		handleResp, err := openai.Handle(ctx, req, reasoningCallback)
		if err != nil {
			return "", err
		}
		// fmt.Fprintln(os.Stderr, "usage:", util.Format(handleResp.Usage))
		return handleResp.Text, nil

	case "claude":
		// Check if this is a batch model
		if strings.Contains(modelID, "batch") {
			// Handle batch models using HandleClaudeBatch
			messages := []claude.Message{
				{Role: "user", Content: []claude.Text{{Type: "text", Text: message}}},
			}
			batchReq := claude.BatchRequestItem{
				CustomID: "0",
				Params: claude.BatchParams{
					Model:     convertClaudeModelID(modelID),
					System:    systemPrompt,
					Messages:  messages,
					MaxTokens: 32000,
					Thinking: &claude.Thinking{
						Type:         "enabled",
						BudgetTokens: 24000,
					},
					UseOAuth: useOAuth,
				},
			}
			results, err := claude.HandleBatch(ctx, []claude.BatchRequestItem{batchReq})
			if err != nil {
				return "", err
			}
			if len(results) == 0 {
				return "", fmt.Errorf("claude batch empty result")
			}
			first := results[0]
			if first.Result.Error != nil {
				return "", fmt.Errorf("claude batch error: %v", first.Result.Error)
			}
			if first.Result.Message == nil {
				return "", fmt.Errorf("claude batch no message")
			}
			var sb strings.Builder
			for i, blk := range first.Result.Message.Content {
				if blk.Type != "text" {
					continue
				}
				if i > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString(blk.Text)
			}
			return sb.String(), nil
		} else {
			// Handle regular models using HandleClaudeChat
			messages := []claude.Message{
				{
					Role: "user",
					Content: []claude.Text{
						{
							Type:  "text",
							Text:  message,
							Cache: &claude.CacheControl{Type: "ephemeral"},
						},
					},
				},
			}
			req := claude.Request{
				Model: convertClaudeModelID(modelID),
				System: []claude.Text{
					{
						Type:  "text",
						Text:  systemPrompt,
						Cache: &claude.CacheControl{Type: "ephemeral"},
					},
				},
				Messages:  messages,
				MaxTokens: 32000,
				Thinking: &claude.Thinking{
					Type:         "enabled",
					BudgetTokens: 24000,
				},
			}
			if search {
				panic("search disabled for now")
				// req.Tools = []provider.ClaudeToolDefinition{provider.CreateClaudeExaTool()}
			}

			handleResp, err := claude.Handle(ctx, req, reasoningCallback)
			if err != nil {
				return "", err
			}
			// fmt.Fprintln(os.Stderr, "usage:", util.Format(handleResp.Usage))
			return handleResp.Text, nil
		}

	case "gemini":
		// Gemini expects messages as a slice of strings
		messages := []string{message}
		thinkingBudget := 0
		if modelID == "gemini-2.5-pro-32k-thinking" {
			thinkingBudget = 32000
		} else if modelID == "gemini-2.5-flash-24k-thinking" {
			thinkingBudget = 24000
		}

		if search {
			// Note: Gemini doesn't support streaming with our simulated web search approach
			// return provider.HandleGeminiChatWithSearch(ctx, convertGeminiModelID(modelID), systemPrompt, messages, nil, reasoningCallback, false, thinkingBudget, true)
			panic("search disabled for now")
		}
		return gemini.Handle(ctx, convertGeminiModelID(modelID), systemPrompt, messages, nil, reasoningCallback, false, thinkingBudget)

	case "grok":
		messages := []grok.Message{
			{
				Role:    "system",
				Content: systemPrompt,
			},
			{
				Role:    "user",
				Content: message,
			},
		}
		req := grok.Request{
			Model:       modelID,
			Messages:    messages,
			Stream:      false,
			Temperature: 0,
		}
		return grok.Handle(ctx, req)

	case "groq":
		messages := []groq.Message{
			{
				Role:    "system",
				Content: systemPrompt,
			},
			{
				Role:    "user",
				Content: message,
			},
		}
		temp := 0.6
		req := groq.Request{
			Model:       modelID,
			Messages:    messages,
			Stream:      stream,
			Temperature: &temp,
		}
		handleResp, err := groq.Handle(ctx, req)
		if err != nil {
			return "", err
		}
		return handleResp.Text, nil

	default:
		return "", fmt.Errorf("unknown provider: %s", prov)
	}
}

func buildSystemPrompt() string {
	return prompts.Ask()
}

func convertOpenAIModelID(modelID string) string {
	// Map internal model IDs to OpenAI model names
	switch modelID {
	case "o3-high":
		return "o3"
	case "o3-flex":
		return "o3"
	case "o3-pro":
		return "o3-pro"
	case "o4-mini-medium":
		return "o4-mini"
	case "o4-mini-flex":
		return "o4-mini"
	case "gpt-4.1-0.5-temp":
		return "gpt-4.1"
	case "gpt-4.1-mini-0.5-temp":
		return "gpt-4.1-mini"
	default:
		panic("unknown: " + modelID)
	}
}

func convertClaudeModelID(modelID string) string {
	switch modelID {
	case "claude-4-opus-24k-thinking":
		return "claude-opus-4-20250514"
	case "claude-4-opus-batch-24k-thinking":
		return "claude-opus-4-20250514"
	case "claude-4-sonnet-24k-thinking":
		return "claude-sonnet-4-20250514"
	case "claude-4-sonnet-batch-24k-thinking":
		return "claude-sonnet-4-20250514"
	default:
		panic("unknown: " + modelID)
	}
}

func convertGeminiModelID(modelID string) string {
	switch modelID {
	case "gemini-2.5-pro-32k-thinking":
		return "gemini-2.5-pro"
	case "gemini-2.5-flash-24k-thinking":
		return "gemini-2.5-flash"
	default:
		panic("unknown: " + modelID)
	}
}

func isO3Model(modelID string) bool {
	return modelID == "o3-high" || modelID == "o3-flex" || modelID == "o3-pro"
}

func isO4MiniModel(modelID string) bool {
	return modelID == "o4-mini-medium" || modelID == "o4-mini-flex"
}

func is41Model(modelID string) bool {
	return modelID == "gpt-4.1-0.5-temp" || modelID == "gpt-4.1-mini-0.5-temp"
}

func isFlexModel(modelID string) bool {
	return strings.HasSuffix(modelID, "-flex")
}

// Execute git rev-parse to find repository root, fallback to current directory
// if not in a git repo or if git command fails
func getGitRoot() string {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	if err != nil {
		// Not in a git repo, return current directory
		dir, err := os.Getwd()
		if err != nil {
			// If we can't get current directory, use /tmp as last resort
			return "/tmp"
		}
		return dir
	}
	return strings.TrimSpace(string(output))
}

// Convert prompt text to filesystem-safe string: lowercase alphanumeric with
// Convert prompt text to filesystem-safe string: lowercase alphanumeric with
// underscores, max 40 chars, defaults to "empty_prompt" if nothing remains
func sanitizePrompt(prompt string) string {
	// Convert to lowercase
	lower := strings.ToLower(prompt)

	// Replace non-alphanumeric with underscore
	sanitized := nonAlphaNumRegex.ReplaceAllString(lower, "_")

	// Trim leading/trailing underscores
	sanitized = strings.Trim(sanitized, "_")

	// Truncate to 40 characters
	if len(sanitized) > 40 {
		sanitized = sanitized[:40]
		// Trim any trailing underscores from truncation
		sanitized = strings.TrimRight(sanitized, "_")
	}

	// If empty after sanitization, use default
	if sanitized == "" {
		sanitized = "empty_prompt"
	}

	return sanitized
}
// ToolCallFunction represents a function in a tool call
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolCall represents a tool call from the API
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}
