// JSON tool processor for nina tools command using native JSON tool calling.
package processors

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/nathants/nina/lib"
	// Removed lib/tools import
	"github.com/nathants/nina/prompts"
	"github.com/nathants/nina/util"
)

// JSONToolProcessor processes AI responses containing JSON tool calls.
type JSONToolProcessor struct {
	Tools []prompts.ToolDefinition // Tool definitions for the AI
}

// ToolCall represents a tool invocation from the AI.
type ToolCall struct {
	Type      string                 `json:"type"`
	ID        string                 `json:"id"`
	Function  string                 `json:"function"`
	Arguments map[string]interface{} `json:"arguments"`
}

// ProcessResponse processes the AI response and executes any JSON tool calls.
func (j *JSONToolProcessor) ProcessResponse(response string, state *lib.LoopState) lib.ProcessorResult {
	result := lib.ProcessorResult{
		Events: []lib.ProcessorEvent{},
	}

	// For JSON tool calling, the response should contain tool_calls
	// This is a simplified version - in reality we'd need to parse the full API response
	// For now, we'll look for a specific format in the response

	// Check for stop condition first
	if strings.Contains(response, "I'll stop here") || strings.Contains(response, "task is complete") {
		result.StopReason = "Task completed"
		return result
	}

	// In a real implementation, we would parse the API response structure
	// For this prototype, we'll return empty results
	// The actual JSON tool execution would happen in the provider's CallWithTools method

	return result
}

func (x *JSONToolProcessor) GetSystemPrompt() string {
	return lib.LoadSystemPromptWithXML()
}

// FormatUserMessage formats the user message for JSON tool calling.
func (j *JSONToolProcessor) FormatUserMessage(state *lib.LoopState, content string) (string, error) {
	var message string

	// Check for SUGGEST.md
	suggestPath := util.GetGitRoot() + "/SUGGEST.md"
	if data, err := os.ReadFile(suggestPath); err == nil && len(data) > 0 {
		suggestContent := strings.TrimSpace(string(data))
		if suggestContent != "" {
			message = fmt.Sprintf("Suggestion: %s", suggestContent)
			// Truncate SUGGEST.md after reading
			if err := os.Truncate(suggestPath, 0); err != nil {
				lib.LogStderr("Failed to truncate SUGGEST.md: %v", err)
			}
		}
	}

	// On first message, include the initial content
	if state.StepNumber == 1 && content != "" {
		if message != "" {
			return fmt.Sprintf("%s\n\n%s", content, message), nil
		}
		return content, nil
	}

	return message, nil
}

// GetToolDefinitions returns the tool definitions for JSON tool calling.
func (j *JSONToolProcessor) GetToolDefinitions() []prompts.ToolDefinition {
	if j.Tools == nil {
		j.Tools = []prompts.ToolDefinition{
			{
				Name:        "NinaBash",
				Description: "bash string that will be run as `bash -c \"$cmd\"`",
				InputSchema: prompts.ToolInputSchema{
					Fields: []prompts.ToolField{
						{
							Name:        "command",
							Type:        "string",
							Description: "your command",
							Required:    true,
						},
					},
				},
			},
			{
				Name:        "NinaChange",
				Description: "search/replace once in a single file",
				InputSchema: prompts.ToolInputSchema{
					Fields: []prompts.ToolField{
						{
							Name:        "path",
							Type:        "string",
							Description: "the absolute filepath to changes (starts with `/` or `~/`)",
							Required:    true,
						},
						{
							Name:        "search",
							Type:        "string",
							Description: "a block of entire contiguous lines to change",
							Required:    true,
						},
						{
							Name:        "replace",
							Type:        "string",
							Description: "the new text to replace that block",
							Required:    true,
						},
					},
				},
			},
		}
	}
	return j.Tools
}

// ExecuteToolCall executes a single tool call and returns the result.
func (j *JSONToolProcessor) ExecuteToolCall(toolCall ToolCall) (string, error) {
	switch toolCall.Function {
	case "NinaBash":
		command, ok := toolCall.Arguments["command"].(string)
		if !ok {
			return "", fmt.Errorf("invalid command argument")
		}

		result := util.ExecuteBash(util.BashCommand{Command: command})

		// Format result as JSON
		resultData := map[string]interface{}{
			"exit_code": result.ExitCode,
			"stdout":    result.Stdout,
			"stderr":    result.Stderr,
		}

		jsonResult, err := json.Marshal(resultData)
		if err != nil {
			return "", err
		}

		return string(jsonResult), nil

	case "NinaChange":
		path, _ := toolCall.Arguments["path"].(string)
		search, _ := toolCall.Arguments["search"].(string)
		replace, _ := toolCall.Arguments["replace"].(string)

		result := util.ExecuteChange(path, search, replace)

		if result.Error != "" {
			return fmt.Sprintf(`{"error": "%s"}`, result.Error), nil
		}

		return fmt.Sprintf(`{"lines_changed": %d}`, result.LinesChanged), nil

	default:
		return "", fmt.Errorf("unknown tool: %s", toolCall.Function)
	}
}
