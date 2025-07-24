// XML tool processor for nina run command using embedded XML tool calling.
package processors

import (
	"fmt"
	"os"
	"strings"

	"github.com/nathants/nina/lib"
	"github.com/nathants/nina/util"
)

// XMLToolProcessor processes AI responses containing XML tool invocations.
type XMLToolProcessor struct{}

// ProcessResponse processes the AI response and executes any XML tools.
func (x *XMLToolProcessor) ProcessResponse(response string, state *lib.LoopState) lib.ProcessorResult {
	// Use existing ProcessOutput function
	return lib.ProcessOutput(response, state, false)
}

// GetSystemPrompt loads the system prompt for XML tool usage.
func (x *XMLToolProcessor) GetSystemPrompt() string {
	return lib.LoadSystemPromptWithXML()
}

// FormatUserMessage formats the user message with NinaInput/NinaResult XML tags.
func (x *XMLToolProcessor) FormatUserMessage(state *lib.LoopState, content string) (string, error) {
	var promptContent []string

	// Check for SUGGEST.md
	suggestPath := util.GetGitRoot() + "/SUGGEST.md"
	if data, err := os.ReadFile(suggestPath); err == nil && len(data) > 0 {
		suggestContent := strings.TrimSpace(string(data))
		if suggestContent != "" {
			promptContent = append(promptContent, fmt.Sprintf("\n\n%s\n%s\n%s", util.NinaSuggestionStart, suggestContent, util.NinaSuggestionEnd))
		}
		// Truncate SUGGEST.md after reading
		if err := os.Truncate(suggestPath, 0); err != nil {
			lib.LogStderr("Failed to truncate SUGGEST.md: %v", err)
		}
	}

	if len(promptContent) == 0 {
		suggestContent := "continue, you have not output <NinaStop> yet"
		promptContent = append(promptContent, fmt.Sprintf("\n\n%s\n%s\n%s", util.NinaSuggestionStart, suggestContent, util.NinaSuggestionEnd))
	}

	// Create NinaPrompt tag only on first message
	if len(promptContent) > 0 {
		if state.StepNumber == 1 && content != "" {
			promptContent[0] = fmt.Sprintf("%s\n%s\n%s", util.NinaPromptStart, content, util.NinaPromptEnd) + promptContent[0]
		}
	} else if state.StepNumber == 1 && content != "" {
		promptContent = append(promptContent, fmt.Sprintf("%s\n%s\n%s", util.NinaPromptStart, content, util.NinaPromptEnd))
	}

	// Add last results if any
	promptContent = append(promptContent, state.LastResults...)

	// Build user message inside NinaInput tags
	userMessage := util.NinaInputStart + "\n"
	if state.InitialPrompt != "" && state.StepNumber > 1 {
		// Include initial prompt for context on subsequent messages
		userMessage += fmt.Sprintf("%s\n%s\n%s\n", util.NinaPromptStart, state.InitialPrompt, util.NinaPromptEnd)
	}
	userMessage += strings.Join(promptContent, "\n") + "\n"
	userMessage += util.NinaInputEnd

	return userMessage, nil
}
