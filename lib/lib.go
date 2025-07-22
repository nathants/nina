// lib provides common utilities and command registration for nina cli
// commands register themselves in init() using Commands and Args maps
// ArgsStruct interface ensures all commands provide a description
package lib

import (
	"context"
	"encoding/json"
	"fmt"
	"nina/prompts"
	"nina/providers/claude"
	"nina/providers/gemini"
	"nina/providers/grok"
	"nina/providers/openai"
	"nina/util"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type ArgsStruct interface {
	Description() string
}

var Commands = make(map[string]func())
var Args = map[string]ArgsStruct{}

// AIProvider defines the interface for AI model providers like OpenAI and Claude
// implementations maintain conversation history and handle API calls
type AIProvider interface {
	CallWithStore(ctx context.Context, model, systemPrompt, userMessage string) (any, error)
	GetTokenUsage(resp any) (promptTokens, completionTokens, totalTokens int)
	GetDetailedUsage(resp any) TokenUsage // New method for cache tokens
	CompactMessages(messagePairs int) CompactionResult // Compact message history when approaching token limit
}

// AIResponse is a generic interface for provider-specific responses
type AIResponse interface {
	GetText() string
}

var logNumber int64
var sessionTimestamp string
var sessionOnce sync.Once

// InitializeSession sets up the session timestamp and log numbering
// For continue sessions, it finds the latest timestamp directory and highest log number
func InitializeSession(continueSession bool) {
	sessionOnce.Do(func() {
		if continueSession {
			// Find the latest timestamp directory
			apiDir := util.GetAgentsSubdir("api")
			entries, err := os.ReadDir(apiDir)
			if err == nil && len(entries) > 0 {
				// Find the latest timestamp directory
				var latestTimestamp string
				for _, entry := range entries {
					if entry.IsDir() && len(entry.Name()) == 15 { // YYYYMMDD-HHMMSS format
						if entry.Name() > latestTimestamp {
							latestTimestamp = entry.Name()
						}
					}
				}

				if latestTimestamp != "" {
					sessionTimestamp = latestTimestamp

					// Find the highest log number in that directory
					timestampDir := filepath.Join(apiDir, latestTimestamp)
					files, err := filepath.Glob(filepath.Join(timestampDir, "*.input.json"))
					if err == nil {
						atomic.StoreInt64(&logNumber, int64(len(files)))
					}
					return
				}
			}
		}

		// New session
		sessionTimestamp = time.Now().Format("20060102-150405")
		atomic.StoreInt64(&logNumber, 0)
	})
}

// GetNextAPILogNumber returns the next log number for the current session
func GetNextAPILogNumber() int {
	return int(atomic.AddInt64(&logNumber, 1))
}

// GetSessionTimestamp returns the current session timestamp
func GetSessionTimestamp() string {
	InitializeSession(false) // Ensure initialization
	return sessionTimestamp
}

// GetTimestampedAgentsPath returns path with session timestamp for the given subdir
func GetTimestampedAgentsPath(subdir string, filename string) string {
	timestamp := GetSessionTimestamp()
	agentsDir := util.GetAgentsSubdir(filepath.Join(subdir, timestamp))
	_ = os.MkdirAll(agentsDir, 0755)
	return filepath.Join(agentsDir, filename)
}

// FormatNumberK formats numbers with k suffix for thousands (e.g. 8226 -> 8k)
// numbers under 1000 are shown as-is, numbers >= 1000 use k suffix
func FormatNumberK(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%dk", n/1000)
}


// ConvertToRangeUpdates converts search/replace updates to range-based updates by using AI to find
// exact line numbers for search text. Processes updates in parallel (max 5 concurrent) and converts
// each update by sending file content + search text to AI which returns start/end line numbers.
// Range updates are passed through unchanged. Returns converted updates or error if any fail.
func ConvertToRangeUpdates(ctx context.Context, updates []util.FileUpdate, session *util.SessionState, reasoningCallback func(string)) ([]util.FileUpdate, error) {
	// Load the converter prompt

	data, err := prompts.EmbeddedFiles.ReadFile("CONVERT.md")

	if err != nil {
		return nil, fmt.Errorf("failed to read converter prompt: %v", err)
	}
	converterPrompt := string(data)

	// Prepare result slice and synchronization
	convertedUpdates := make([]util.FileUpdate, len(updates))
	var convertErrors []error
	var errMutex sync.Mutex
	var wg sync.WaitGroup

	sem := make(chan error, 10)

	// Process each update in parallel
	for i, update := range updates {
		// Skip range updates (already converted)
		if update.StartLine > 0 || update.EndLine > 0 {
			convertedUpdates[i] = update
			continue
		}

		// Skip conversion for new file creation (empty search)
		if len(update.SearchLines) == 0 {
			convertedUpdates[i] = update
			continue
		}

		wg.Add(1)

		go func(idx int, upd util.FileUpdate) {
			defer util.LogRecover()
			defer wg.Done()

			// Acquire semaphore
			sem <- nil
			defer func() { <-sem }()

			// Get the file content
			path, ok := session.PathMap[upd.FileName]
			if !ok && len(upd.SearchLines) != 0 {
				errMutex.Lock()
				convertErrors = append(convertErrors, fmt.Errorf("missing pathMap for file: %s", upd.FileName))
				errMutex.Unlock()
				return
			}

			// Use SelectedFiles which have line numbers - this is what the AI saw
			content, ok := session.SelectedFiles[path]
			if !ok {
				content, ok = session.OrigFiles[path]
				content = util.AddLineNumbers(content)
				if !ok {
					errMutex.Lock()
					convertErrors = append(convertErrors, fmt.Errorf("file not found in session: %s", path))
					errMutex.Unlock()
					return
				}
			}

			// Try conversion with retry on line range mismatch
			maxAttempts := 2
			var lastError error

			searchText := strings.Join(util.TrimBlankLines(upd.SearchLines), "\n")

			for attempt := 1; attempt <= maxAttempts; attempt++ {
				// Build the conversion request following convert.md format
				var b strings.Builder
				b.WriteString("\n\n# Input data\n\n")

				// If this is a retry due to line range mismatch, include the error history
				if attempt > 1 && lastError != nil {
					fmt.Println("attempt", attempt)
					b.WriteString("<NinaHistory>\n")
					b.WriteString(lastError.Error())
					b.WriteString("\n</NinaHistory>\n\n")
				}

				b.WriteString("<NinaFile>\n")
				b.WriteString(strings.TrimSpace(content))
				b.WriteString("\n</NinaFile>\n")
				b.WriteString("\n\n")
				b.WriteString("<NinaSearch>\n")
				b.WriteString(searchText)
				b.WriteString("\n</NinaSearch>\n")

				converterMessage := b.String()

				converterReq := AiRequest{
					// Model: "o3",
					// Model:  "o4-mini",
					Model: "sonnet",
					// Model: "gemini-2.5-pro",
					Effort: "medium",
					System:  converterPrompt,
					Message: converterMessage,
				}

				if attempt > 1 {
					converterReq.ThinkingBudget = 22000
				}

				output, err := generateResponse(ctx, converterReq, reasoningCallback)
				if err != nil {
					errMutex.Lock()
					convertErrors = append(convertErrors, fmt.Errorf("converter error for %s: %v", upd.FileName, err))
					errMutex.Unlock()
					return
				}

				// Parse the single line JSON output
				output = strings.TrimSpace(output)
				// Remove markdown code fences if present
				output = util.RemoveFencedBlocks(output)
				var rangeResult util.RangeResult
				err = json.Unmarshal([]byte(output), &rangeResult)
				if err != nil {
					if attempt < maxAttempts {
						continue
					}
					errMutex.Lock()
					convertErrors = append(convertErrors, fmt.Errorf("failed to parse converter output for %s: %v", upd.FileName, err))
					errMutex.Unlock()
					return
				}

				// Check if converter indicated an error with -1 values
				if rangeResult.Start == -1 || rangeResult.End == -1 {
					if attempt < maxAttempts {
						continue
					}
					errMutex.Lock()
					convertErrors = append(convertErrors, fmt.Errorf("converter could not find search text in file %s (idx=%d)", upd.FileName, idx))
					errMutex.Unlock()
					return
				}

				// Calculate line range and search line count for validation
				lineRange := rangeResult.End - rangeResult.Start + 1
				searchLineCount := len(strings.Split(searchText, "\n"))

				// Validate that line range matches search line count
				if lineRange != searchLineCount {
					// Store the error for potential retry

					lastError = fmt.Errorf("line range mismatch for %s (idx=%d): range is %d lines (end:%d - start:%d) but search has %d lines\nsearch: %s\nreplace: %s",
						upd.FileName, idx, lineRange, rangeResult.End, rangeResult.Start, searchLineCount, util.Pformat(upd.SearchLines), util.Pformat(upd.ReplaceLines))

					// If this is not the last attempt, retry
					if attempt < maxAttempts {
						// fmt.Printf("Retrying converter due to line range mismatch (attempt %d/%d): %v\n", attempt, maxAttempts, lastError)
						continue
					}

					// Final attempt failed, record the error
					errMutex.Lock()
					convertErrors = append(convertErrors, lastError)
					errMutex.Unlock()
					return
				}

				// Success! Create the converted update
				convertedUpdate := util.FileUpdate{
					FileName:     upd.FileName,
					ReplaceLines: upd.ReplaceLines,
					StartLine:    rangeResult.Start,
					EndLine:      rangeResult.End,
				}
				convertedUpdates[idx] = convertedUpdate
				break // Success, exit retry loop
			}
		}(i, update)
	}

	// Wait for all goroutines to complete
	wg.Wait()

	// Check if any errors occurred
	if len(convertErrors) > 0 {
		// Return the first error
		// fmt.Println("error with convert to ranges:", convertErrors[0])
		return nil, convertErrors[0]
	}

	return convertedUpdates, nil
}

// PrepareAndValidateUpdates takes raw updates, converts them to range updates, validates them,
// and returns them organized and ready for application. This is used by both HandleChat and cmd/apply.
func PrepareAndValidateUpdates(ctx context.Context, updates []util.FileUpdate, session *util.SessionState, reasoningCallback func(string)) ([]util.FileUpdate, error) {
	if len(updates) == 0 {
		return updates, nil
	}

	// Convert to range updates
	rangeUpdates, err := ConvertToRangeUpdates(ctx, updates, session, reasoningCallback)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to range updates: %v", err)
	}

	// Validate updates
	finalUpdates := []util.FileUpdate{}
	for _, update := range rangeUpdates {
		err := util.ValidateFileUpdate(update, session)
		if err != nil {
			return nil, fmt.Errorf("failed to validate update for %s: %v", update.FileName, err)
		}
		finalUpdates = append(finalUpdates, update)
	}

	// Group updates by file and sort for proper application
	groupedUpdates := util.GroupUpdatesByFile(finalUpdates)
	organizedUpdates := []util.FileUpdate{}
	for _, fileUpdates := range groupedUpdates {
		// Sort updates for this file by StartLine descending
		sortedFileUpdates := util.SortUpdatesForApplication(fileUpdates)
		organizedUpdates = append(organizedUpdates, sortedFileUpdates...)
	}

	return organizedUpdates, nil
}

type AssistantBlock struct {
	Type            string `json:"type"`
	Content         string `json:"content"`
	Tokens          int    `json:"tokens"`
	UpdateAvailable bool   `json:"updateAvailable,omitempty"`
}

type FileData struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type AiRequest struct {
	System         string   `json:"system"`
	Session        string   `json:"session"`
	Message        string   `json:"message"`
	Files          []string `json:"files"`
	Model          string   `json:"model,omitempty"`
	Effort         string   `json:"effort,omitempty"`
	NoThoughts     bool     `json:"noThoughts"`
	ThinkingBudget int      `json:"thinkingBudget"`
	ReadingMode    bool     `json:"readingMode"`
}

func generateResponse(ctx context.Context, req AiRequest, reasoningCallback func(string)) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}

	if req.Model == "" {
		panic("no model")
	}

	if req.System == "" {
		panic("no system prompt")
	}

	systemPrompt := req.System

	if strings.HasPrefix(req.Model, "gemini-") {
		msg, err := gemini.Handle(
			ctx,
			req.Model,
			systemPrompt,
			[]string{req.Message},
			nil,
			reasoningCallback,
			req.NoThoughts,
			req.ThinkingBudget,
		)
		if err != nil {
			fmt.Println("error:", err)
		}
		return msg, err
	} else if req.Model == "gemini" {
		msg, err := gemini.Handle(
			ctx,
			"gemini-2.5-pro",
			systemPrompt,
			[]string{req.Message},
			nil,
			reasoningCallback,
			req.NoThoughts,
			req.ThinkingBudget,
		)
		if err != nil {
			fmt.Println("error:", err)
		}
		return msg, err
	} else if req.Model == "grok" {
		messages := []grok.Message{
			{
				Role:    "system",
				Content: systemPrompt,
			},
			{
				Role:    "user",
				Content: req.Message,
			},
		}
		grokReq := grok.Request{
			Model:       "grok-4-0709",
			Messages:    messages,
			Stream:      false,
			Temperature: 0,
		}
		return grok.Handle(ctx, grokReq)
	} else if req.Model == "sonnet" {
		messages := []claude.Message{
			{
				Role: "user",
				Content: []claude.Text{
					{Type: "text", Text: req.Message},
				},
			},
		}
		request := claude.Request{
			Model: "claude-sonnet-4-20250514",
			System: []claude.Text{
				{
					Type:  "text",
					Text:  systemPrompt,
					Cache: &claude.CacheControl{Type: "ephemeral"},
				},
			},
			Messages:  messages,
			MaxTokens: 32000,
			Stream:    true,
		}
		if req.ThinkingBudget > 0 {
			request.Thinking = &claude.Thinking{
				Type:         "enabled",
				BudgetTokens: req.ThinkingBudget,
			}
		}
		msg, err := claude.Handle(ctx, request, reasoningCallback)
		if err != nil {
			return "", err
		}
		return msg.Text, nil
	} else if req.Model == "opus" {
		messages := []claude.Message{
			{
				Role:    "user",
				Content: []claude.Text{{Type: "text", Text: req.Message}},
			},
		}
		msg, err := claude.Handle(ctx, claude.Request{
			Model: "claude-opus-4-20250514",
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
				BudgetTokens: 22000,
			},
			Stream: true,
		}, reasoningCallback)
		if err != nil {
			return "", err
		}
		return msg.Text, nil
	} else if req.Model == "sonnet-batch" {
		messages := []claude.Message{
			{
				Role:    "user",
				Content: []claude.Text{claude.Text{Type: "text", Text: req.Message}},
			},
		}
		batchReq := claude.BatchRequestItem{
			CustomID: "0",
			Params: claude.BatchParams{
				Model:     "claude-sonnet-4-20250514",
				System:    systemPrompt,
				Messages:  messages,
				MaxTokens: 32000,
				Thinking: &claude.Thinking{
					Type:         "enabled",
					BudgetTokens: 22000,
				},
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
	} else if req.Model == "opus-batch" {
		messages := []claude.Message{
			{
				Role:    "user",
				Content: []claude.Text{claude.Text{Type: "text", Text: req.Message}},
			},
		}
		batchReq := claude.BatchRequestItem{
			CustomID: "0",
			Params: claude.BatchParams{
				Model:     "claude-opus-4-20250514",
				System:    systemPrompt,
				Messages:  messages,
				MaxTokens: 32000,
				Thinking: &claude.Thinking{
					Type:         "enabled",
					BudgetTokens: 22000,
				},
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
	} else if strings.HasSuffix(req.Model, "-batch") {
		baseModel := strings.TrimSuffix(req.Model, "-batch")

		devMsg := openai.ChatMessage{
			Type: "message",
			Role: "developer",
			Content: []openai.ContentPart{
				{
					Type: "input_text",
					Text: systemPrompt,
				},
			},
		}
		userMsg := openai.ChatMessage{
			Type: "message",
			Role: "user",
			Content: []openai.ContentPart{
				{
					Type: "input_text",
					Text: req.Message,
				},
			},
		}

		openaiReq := openai.Request{
			Model:  baseModel,
			Input:  []openai.ChatMessage{devMsg, userMsg},
			Stream: false,
			Store:  false,
			User:   "nina",
		}

		batchItem := openai.BatchRequestItem{
			CustomID: "0",
			Params:   openaiReq,
		}

		results, err := openai.HandleBatch(ctx, []openai.BatchRequestItem{batchItem})
		if err != nil {
			return "", err
		}
		if len(results) == 0 {
			return "", fmt.Errorf("openai batch empty result")
		}

		first := results[0]
		if first.Error != nil {
			return "", fmt.Errorf("openai batch error: %v", first.Error)
		}
		if first.Response == nil {
			return "", fmt.Errorf("openai batch no response")
		}
		body := first.Response["body"]

		respBytes, err := json.Marshal(body)
		if err != nil {
			return "", err
		}

		var openaiResp openai.Response
		err = json.Unmarshal(respBytes, &openaiResp)
		if err != nil {
			return "", err
		}

		for _, output := range openaiResp.Output {
			if output.Type == "message" && len(output.Content) > 0 {
				return output.Content[0].Text, nil
			}
		}
		return "", fmt.Errorf("openai batch no message output")
	}

	inputMsgs := []openai.ChatMessage{
		{
			Type: "message",
			Role: "developer",
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
					Text: req.Message,
				},
			},
		},
	}

	modelName := req.Model
	serviceTier := "auto"
	if strings.HasSuffix(req.Model, "-flex") {
		modelName = strings.TrimSuffix(req.Model, "-flex")
		serviceTier = "flex"
	}

	input := openai.Request{
		Model:           modelName,
		Input:           inputMsgs,
		Stream:          true,
		Store:           false,
		User:            "nina",
		MaxOutputTokens: util.Ptr(100000),
		ServiceTier:     serviceTier,
	}
	if input.Model == "o4-mini" {
		input.Reasoning = &openai.ReasoningRequest{
			Summary: "auto",
			Effort:  "medium",
		}
	} else if strings.HasPrefix(input.Model, "o3") {
		input.Reasoning = &openai.ReasoningRequest{
			Summary: "auto",
			Effort:  "high",
		}
	} else {
		input.Temperature = util.Ptr(0.3)
	}
	if req.Effort != "" {
		input.Reasoning.Effort = req.Effort
	}
	callback := func(data string) {
		if reasoningCallback != nil {
			reasoningCallback(data)
		}
	}

	resp, err := openai.Handle(ctx, input, callback)
	if err != nil {
		return "", err
	}
	// lib.Logger.Println("usage:", util.Format(resp.Usage))
	return resp.Text, nil
}

// GetDebugFilePath ensures ~/.nina-debug exists and returns path for debug file
// with given prefix and timestamp. Returns empty string if not local environment.
func GetDebugFilePath(name string, timestamp time.Time) string {
	if os.Getenv("DEBUG") == "" {
		return "" // Not local environment
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("failed to get home dir: %v", err)
		return ""
	}

	debugDir := filepath.Join(homeDir, ".nina-debug")
	err = os.MkdirAll(debugDir, 0755)
	if err != nil {
		fmt.Printf("failed to create debug dir: %v", err)
		return ""
	}

	isoTimestamp := timestamp.Format("2006-01-02T15-04-05Z")
	return filepath.Join(debugDir, isoTimestamp+"-"+name)
}
