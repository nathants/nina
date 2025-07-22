// arch combines architect, convert, and apply in one tool for AI-assisted coding
// reads prompt from stdin, sends to AI with ARCHITECT.md system prompt
// parses NinaChange tags and applies updates to specified files
package arch

import (
	"context"
	"fmt"
	"io"
	"nina/lib"
	"nina/prompts"
	util "nina/util"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alexflint/go-arg"
	"nina/providers/claude"
	"nina/providers/gemini"
	"nina/providers/grok"
	"nina/providers/openai"
)

func init() {
	lib.Commands["arch"] = arch
	lib.Args["arch"] = archArgs{}
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
}

type archArgs struct {
	Files   []string `arg:"positional" help:"files to include in the prompt"`
	Model   string   `arg:"-m,--model" default:"sonnet" help:"AI model to use"`
	DryRun  bool     `arg:"-n,--dry-run" help:"show changes without applying them"`
	Verbose bool     `arg:"-v,--verbose" help:"verbose output"`
}

func (archArgs) Description() string {
	return `arch - Architect tool for AI-assisted code modifications

Reads a prompt from stdin and applies AI-suggested changes to files.

Supported models (short names):
  - o3, o3-flex, o3-pro
  - opus, opus-batch, sonnet, sonnet-batch
  - o4-mini, o4-mini-flex
  - gemini, flash
  - 4.1, 4.1-mini
  - v0-md, v0-lg
  - ollama, grok

Example:
  echo "refactor this function to use async/await" | nina arch src/*.js
  echo "add error handling" | nina arch main.go util.go -m gemini`
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
	default:
		panic("unknown model: " + model)
	}
}

func readStdin() (string, error) {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("reading stdin: %w", err)
	}
	prompt := strings.TrimSpace(string(data))
	if prompt == "" {
		return "", fmt.Errorf("no prompt provided on stdin")
	}
	return prompt, nil
}

func readFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading file %s: %w", path, err)
	}
	return string(data), nil
}

func formatNinaInput(prompt string, files map[string]string) string {
	var builder strings.Builder
	builder.WriteString(util.NinaInputStart)
	builder.WriteString("\n\n")
	builder.WriteString(util.NinaPromptStart)
	builder.WriteString("\n")
	builder.WriteString(prompt)
	builder.WriteString("\n")
	builder.WriteString(util.NinaPromptEnd)
	builder.WriteString("\n")

	// Sort paths for consistent ordering
	var paths []string
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	for _, path := range paths {
		content := files[path]
		builder.WriteString("\n")
		builder.WriteString(util.NinaFileStart)
		builder.WriteString("\n\n")
		builder.WriteString(util.NinaPathStart)
		builder.WriteString("\n")
		builder.WriteString(path)
		builder.WriteString("\n")
		builder.WriteString(util.NinaPathEnd)
		builder.WriteString("\n\n")
		builder.WriteString(util.NinaContentStart)
		builder.WriteString("\n")
		builder.WriteString(content)
		builder.WriteString("\n")
		builder.WriteString(util.NinaContentEnd)
		builder.WriteString("\n\n")
		builder.WriteString(util.NinaFileEnd)
		builder.WriteString("\n")
	}

	builder.WriteString("\n")
	builder.WriteString(util.NinaInputEnd)
	return builder.String()
}

func callProvider(ctx context.Context, provider, modelID, systemPrompt, userMessage string) (string, error) {
	switch provider {
	case "claude":
		// Check if this is a batch model
		if strings.Contains(modelID, "batch") {
			// Handle batch models using HandleBatch
			messages := []claude.Message{
				{Role: "user", Content: []claude.Text{{Type: "text", Text: userMessage}}},
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
					UseOAuth: false,
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
		}

		// Handle regular Claude 4 models
		messages := []claude.Message{{
			Role: "user",
			Content: []claude.Text{{
				Type: "text",
				Text: userMessage,
			}},
		}}
		req := claude.Request{
			Model: convertClaudeModelID(modelID),
			System: []claude.Text{{
				Type: "text",
				Text: systemPrompt,
			}},
			Messages:  messages,
			MaxTokens: 32000,
		}
		if strings.Contains(modelID, "thinking") {
			req.Thinking = &claude.Thinking{
				Type:         "enabled",
				BudgetTokens: 24000,
			}
		}
		resp, err := claude.Handle(ctx, req, nil)
		if err != nil {
			return "", err
		}
		return resp.Text, nil

	case "openai":
		// Handle OpenAI models
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
						{Type: "input_text", Text: userMessage},
					},
				},
			},
			Stream: false,
		}
		if strings.HasSuffix(modelID, "-flex") {
			req.ServiceTier = "flex"
		}
		if strings.HasPrefix(modelID, "o3") {
			req.Reasoning = &openai.ReasoningRequest{
				Summary: "auto",
				Effort:  "high",
			}
		}
		if strings.HasPrefix(modelID, "o4-mini") {
			req.Reasoning = &openai.ReasoningRequest{
				Summary: "auto",
				Effort:  "medium",
			}
		}
		if strings.HasPrefix(modelID, "gpt-4.1") {
			temp := 0.5
			req.Temperature = &temp
		}
		resp, err := openai.Handle(ctx, req, nil)
		if err != nil {
			return "", err
		}
		return resp.Text, nil


	case "gemini":
		// Handle Gemini models
		messages := []string{userMessage}
		thinkingBudget := 0
		if modelID == "gemini-2.5-pro-32k-thinking" {
			thinkingBudget = 32000
		} else if modelID == "gemini-2.5-flash-24k-thinking" {
			thinkingBudget = 24000
		}
		return gemini.Handle(ctx, convertGeminiModelID(modelID), systemPrompt, messages, nil, nil, false, thinkingBudget)

	case "grok":
		// Handle Grok models
		messages := []grok.Message{
			{
				Role:    "system",
				Content: systemPrompt,
			},
			{
				Role:    "user",
				Content: userMessage,
			},
		}
		req := grok.Request{
			Model:       modelID,
			Messages:    messages,
			Stream:      false,
			Temperature: 0,
		}
		return grok.Handle(ctx, req)

	case "v0":
		return "", fmt.Errorf("v0 provider not yet implemented in arch")

	case "ollama":
		return "", fmt.Errorf("ollama provider not yet implemented in arch")

	default:
		return "", fmt.Errorf("unsupported provider: %s", provider)
	}
}

func convertOpenAIModelID(modelID string) string {
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
		panic("unknown OpenAI model: " + modelID)
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
		panic("unknown Claude model: " + modelID)
	}
}

func convertGeminiModelID(modelID string) string {
	switch modelID {
	case "gemini-2.5-pro-32k-thinking":
		return "gemini-2.5-pro"
	case "gemini-2.5-flash-24k-thinking":
		return "gemini-2.5-flash"
	default:
		panic("unknown Gemini model: " + modelID)
	}
}

func run(args archArgs) error {
	ctx := context.Background()

	prompt, err := readStdin()
	if err != nil {
		return err
	}

	// Read all files
	files := make(map[string]string)
	for _, pattern := range args.Files {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return fmt.Errorf("invalid pattern %s: %w", pattern, err)
		}
		if len(matches) == 0 {
			// Treat as literal filename if no glob matches
			matches = []string{pattern}
		}

		for _, path := range matches {
			absPath, err := filepath.Abs(path)
			if err != nil {
				return fmt.Errorf("getting absolute path for %s: %w", path, err)
			}
			content, err := readFile(path)
			if err != nil {
				return err
			}
			files[absPath] = content
		}
	}

	if args.Verbose {
		fmt.Fprintf(os.Stderr, "Processing %d files with prompt: %s\n", len(files), prompt)
	}

	// Validate we have files to process
	if len(files) == 0 {
		return fmt.Errorf("no files to process")
	}

	// Format input with CODING.md prompt
	codingPrompt, err := prompts.EmbeddedFiles.ReadFile("CODING.md")
	if err != nil {
		return fmt.Errorf("failed to read CODING.md prompt: %w", err)
	}

	ninaInput := formatNinaInput(prompt, files)
	fullUserMessage := string(codingPrompt) + "\n\n" + ninaInput

	// Load ARCHITECT.md system prompt
	architectPrompt, err := prompts.EmbeddedFiles.ReadFile("ARCHITECT.md")
	if err != nil {
		return fmt.Errorf("failed to read ARCHITECT.md prompt: %w", err)
	}

	// Parse model to get provider and modelID
	provider, modelID := parseModel(args.Model)

	if args.Verbose {
		fmt.Fprintf(os.Stderr, "Calling AI model: %s (provider: %s)\n", args.Model, provider)
	}

	// Call appropriate provider
	respText, err := callProvider(ctx, provider, modelID, string(architectPrompt), fullUserMessage)
	if err != nil {
		return fmt.Errorf("AI request failed: %w", err)
	}

	if args.Verbose {
		fmt.Fprintln(os.Stderr, "AI response received")
	}

	// Extract NinaChange entries from response
	updates, err := util.ParseFileUpdates(respText)
	if err != nil {
		return fmt.Errorf("failed to parse AI response: %w", err)
	}

	if args.Verbose {
		fmt.Fprintf(os.Stderr, "Found %d file updates\n", len(updates))
	}

	// Apply updates to each file
	for _, update := range updates {
		// Get original content
		origContent, exists := files[update.FileName]
		if !exists {
			// New file
			origContent = ""
		}

		// Create session state for this file
		session := &util.SessionState{
			OrigFiles:     map[string]string{update.FileName: origContent},
			SelectedFiles: map[string]string{},
			PathMap:       map[string]string{update.FileName: update.FileName},
		}

		// Convert to range updates
		rangeUpdates, err := lib.ConvertToRangeUpdates(ctx, []util.FileUpdate{update}, session, nil)
		if err != nil {
			return fmt.Errorf("failed to convert to range updates for %s: %w", update.FileName, err)
		}

		// Apply updates
		newContent, err := util.ApplyFileUpdates(origContent, rangeUpdates)
		if err != nil {
			return fmt.Errorf("failed to apply updates to %s: %w", update.FileName, err)
		}

		if args.DryRun {
			// Show diff
			fmt.Printf("=== %s ===\n", update.FileName)
			if exists {
				// Show changes
				fmt.Println("Changes to apply:")
				origLines := strings.Split(origContent, "\n")
				for i, ru := range rangeUpdates {
					fmt.Printf("Lines %d-%d:\n", ru.StartLine+1, ru.EndLine+1)
					// Show actual lines being replaced from the file
					for lineNum := ru.StartLine; lineNum <= ru.EndLine && lineNum < len(origLines); lineNum++ {
						fmt.Println("- " + origLines[lineNum])
					}
					// Show replacement lines
					if i < len(updates) && len(update.ReplaceLines) > 0 {
						for _, line := range update.ReplaceLines {
							fmt.Println("+ " + line)
						}
					}
					fmt.Println()
				}
			} else {
				// New file
				fmt.Println("New file:")
				fmt.Println(newContent)
			}
		} else {
			// Ensure directory exists for new files
			dir := filepath.Dir(update.FileName)
			if err := os.MkdirAll(dir, 0755); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", dir, err)
			}

			// Write to disk
			err = os.WriteFile(update.FileName, []byte(newContent), 0644)
			if err != nil {
				return fmt.Errorf("failed to write %s: %w", update.FileName, err)
			}
			if args.Verbose {
				fmt.Fprintf(os.Stderr, "Updated %s\n", update.FileName)
			}
		}
	}

	if !args.DryRun && len(updates) > 0 {
		fmt.Fprintf(os.Stderr, "Successfully applied %d file updates\n", len(updates))
	}

	return nil
}

func arch() {
	var args archArgs
	arg.MustParse(&args)

	// Validate model
	if !supportedModels[args.Model] {
		fmt.Fprintf(os.Stderr, "Error: unsupported model: %s\n\n", args.Model)
		fmt.Fprintf(os.Stderr, "Supported models:\n")
		models := make([]string, 0, len(supportedModels))
		for model := range supportedModels {
			models = append(models, model)
		}
		sort.Strings(models)
		for _, model := range models {
			fmt.Fprintf(os.Stderr, "  - %s\n", model)
		}
		os.Exit(1)
	}

	if err := run(args); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
