package lib

import (
	"fmt"
	"os"
	"strings"

	// Removed lib/tools import - functions moved to util
	"github.com/nathants/nina/prompts"
	util "github.com/nathants/nina/util"
)

// ProcessorResult contains the result of processing output
type ProcessorResult struct {
	StopReason string
	Events     []ProcessorEvent
	Results    []string // NinaResult strings to feed back to AI
	Error      error    // Error if NinaOutput is missing or invalid
}

// ProcessorEvent represents an action that occurred during processing
type ProcessorEvent struct {
	Type         string
	Filepath     string
	LinesChanged int
	Cwd          string
	Cmd          string
	Args         []string
	ExitCode     int
	Stdout       string
	Stderr       string
	Reason       string
}

// Event represents a logged event (for stdout output)
type Event struct {
	Name        string
	TokensUsed  int
	MaxTokens   int
	Description string
}

// ProcessOutput processes the AI output and executes any commands
func ProcessOutput(output string, _ *LoopState, _ bool) ProcessorResult {
	result := ProcessorResult{
		Events: []ProcessorEvent{},
	}

	// Check for NinaStop but don't return immediately
	stopReason, err := util.ParseNinaStop(output)
	if err != nil {
		// Convert NinaStop parsing errors to feedback
		errorMsg := fmt.Sprintf("Failed to parse NinaStop: %v", err)
		fmt.Fprintf(os.Stderr, "%s\n", errorMsg)

		// Add error feedback to results so AI can fix it
		resultStr := fmt.Sprintf("%s\n<NinaSuggestion>%s. Please check your XML formatting.</NinaSuggestion>\n%s",
			util.NinaResultStart, errorMsg, util.NinaResultEnd)
		result.Results = append(result.Results, resultStr)
	}
	foundNinaStop := stopReason != ""

	// Process NinaChange blocks
	ninaOutput, err := util.ExtractSingle(output, util.NinaOutputStart, util.NinaOutputEnd)
	if err != nil {
		result.Error = fmt.Errorf("failed to extract NinaOutput: %v", err)
		return result
	}
	if ninaOutput == "" {
		result.Error = fmt.Errorf("no valid NinaOutput found in response")
		return result
	}

	changes, err := util.ExtractAll(ninaOutput, util.NinaStart, util.NinaEnd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to extract NinaChange blocks: %v\n", err)
	}
	for _, change := range changes {
		event := applyNinaChange(change)
		result.Events = append(result.Events, event)
		// Add result to be returned for feedback
		resultStr := fmt.Sprintf("%s\n<NinaChange>%s</NinaChange>\n%s", util.NinaResultStart, event.Filepath, util.NinaResultEnd)
		if event.Reason != "" {
			resultStr = fmt.Sprintf("%s\n<NinaChange>%s</NinaChange>\n<NinaError>%s</NinaError>\n%s", util.NinaResultStart, event.Filepath, event.Reason, util.NinaResultEnd)
		}
		result.Results = append(result.Results, resultStr)
		// Also print to stdout for immediate visibility
	}

	// Process NinaBash blocks
	bashCmds, err := util.ParseNinaBash(output)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to parse NinaBash: %v\n", err)
	}
	for _, bashCmd := range bashCmds {
		fmt.Fprintf(os.Stderr, "%s| Bash [%s %s] |%s\n", ColorBlue, bashCmd.Command, strings.Join(bashCmd.Args, " "), ColorReset)
		event := executeNinaBash(bashCmd)
		result.Events = append(result.Events, event)
		// Add result to be returned for feedback
		cmdStr := bashCmd.Command
		if len(bashCmd.Args) > 0 {
			cmdStr = bashCmd.Command + " " + strings.Join(bashCmd.Args, " ")
		}
		resultStr := fmt.Sprintf("%s\n<NinaCmd>%s</NinaCmd>\n<NinaExit>%d</NinaExit>\n<NinaStdout>%s</NinaStdout>\n<NinaStderr>%s</NinaStderr>\n%s",
			util.NinaResultStart, cmdStr, event.ExitCode, event.Stdout, event.Stderr, util.NinaResultEnd)
		result.Results = append(result.Results, resultStr)
	}

	// Check if we should stop - only if NinaStop was found and no other events occurred
	if foundNinaStop {
		result.StopReason = stopReason
		// Add event for logging in processResponse
		result.Events = append(result.Events, ProcessorEvent{
			Type:   "NinaStop",
			Reason: stopReason,
		})
	}

	return result
}

func applyNinaChange(change string) ProcessorEvent {
	// Extract NinaPath
	filepath, err := util.ExtractSingle(change, util.NinaPathStart, util.NinaPathEnd)
	if err != nil || filepath == "" {
		return ProcessorEvent{
			Type:   "NinaChange",
			Reason: "Missing NinaPath",
		}
	}
	filepath = strings.TrimSpace(filepath)

	// Extract NinaSearch
	searchText, _ := util.ExtractSingle(change, util.NinaSearchStart, util.NinaSearchEnd)
	searchText = strings.TrimSpace(searchText)

	// Extract NinaReplace
	replaceText, err := util.ExtractSingle(change, util.NinaReplaceStart, util.NinaReplaceEnd)
	if err != nil || replaceText == "" {
		return ProcessorEvent{
			Type:     "NinaChange",
			Filepath: filepath,
			Reason:   "Missing NinaReplace",
		}
	}

	// Use shared executor
	result := util.ExecuteChange(filepath, searchText, replaceText)

	if result.Error != "" {
		return ProcessorEvent{
			Type:     "NinaChange",
			Filepath: result.FilePath,
			Reason:   result.Error,
		}
	}

	return ProcessorEvent{
		Type:         "NinaChange",
		Filepath:     result.FilePath,
		LinesChanged: result.LinesChanged,
	}
}

func executeNinaBash(bashCmd util.BashCommand) ProcessorEvent {
	// Use shared executor
	result := util.ExecuteBash(bashCmd)

	return ProcessorEvent{
		Type:     "NinaBash",
		Cwd:      result.Cwd,
		Cmd:      result.Cmd,
		Args:     result.Args,
		ExitCode: result.ExitCode,
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
	}
}

// LoopState is now defined in loop.go

// LogEvent logs an event to stdout in the required format
func LogEvent(event Event) {
	fmt.Printf("| %s, %s\n",
		event.Name,
		event.Description,
	)
}

func IsDebugMode() bool {
	return os.Getenv("DEBUG") != ""
}

// LoadSystemPromptWithXML loads SYSTEM.md and XML.md prompts.
// This is the standard system prompt for both XML and JSON processors.
func LoadSystemPromptWithXML() string {
	systemPrompt, err := prompts.EmbeddedFiles.ReadFile("SYSTEM.md")
	if err != nil {
		panic(err)
	}
	xmlPrompt, err := prompts.EmbeddedFiles.ReadFile("XML.md")
	if err != nil {
		panic(err)
	}
	return string(systemPrompt) + "\n" + string(xmlPrompt)
}
