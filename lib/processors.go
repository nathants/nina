package lib

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	util "nina/util"
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

	// Expand home directory if filepath starts with ~
	if strings.HasPrefix(filepath, "~/") {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			filepath = strings.Replace(filepath, "~", homeDir, 1)
		}
	}

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

	// Read existing file content
	existingData, err := os.ReadFile(filepath)
	var content string
	existingLines := 0

	if err != nil {
		// If file doesn't exist and search is empty, create new file
		if os.IsNotExist(err) && searchText == "" {
			content = ""
		} else {
			return ProcessorEvent{
				Type:     "NinaChange",
				Filepath: filepath,
				Reason:   fmt.Sprintf("Failed to read file: %v", err),
			}
		}
	} else {
		content = string(existingData)
		existingLines = len(strings.Split(content, "\n"))
	}

	// Create FileUpdate structure for nina-util
	update := util.FileUpdate{
		FileName: filepath,
	}

	var newContent string
	if searchText == "" {
		// Full file replacement
		update.ReplaceLines = strings.Split(replaceText, "\n")
		newContent, err = util.ApplyFileUpdates(content, []util.FileUpdate{update})
	} else {
		// Search/replace update - use ConvertToRangeUpdates for AI line conversion
		update.SearchLines = util.TrimBlankLines(strings.Split(searchText, "\n"))
		update.ReplaceLines = util.TrimBlankLines(strings.Split(replaceText, "\n"))

		// Create session state for conversion
		session := &util.SessionState{
			OrigFiles:     map[string]string{filepath: content},
			SelectedFiles: map[string]string{},
			PathMap:       map[string]string{filepath: filepath},
		}

		// Convert search/replace to range updates using AI
		ctx := context.Background()
		rangeUpdates, err := ConvertToRangeUpdates(ctx, []util.FileUpdate{update}, session, nil)
		if err != nil {
			return ProcessorEvent{
				Type:     "NinaChange",
				Filepath: filepath,
				Reason:   fmt.Sprintf("Failed to convert to range updates: %v", err),
			}
		}

		// Apply the converted range updates
		newContent, err = util.ApplyFileUpdates(content, rangeUpdates)
		if err != nil {
			return ProcessorEvent{
				Type:     "NinaChange",
				Filepath: filepath,
				Reason:   fmt.Sprintf("Failed to apply range updates: %v", err),
			}
		}
	}

	if err != nil {
		return ProcessorEvent{
			Type:     "NinaChange",
			Filepath: filepath,
			Reason:   fmt.Sprintf("Failed to apply update: %v", err),
		}
	}

	// Write the new content
	err = os.WriteFile(filepath, []byte(newContent), 0644)
	if err != nil {
		return ProcessorEvent{
			Type:     "NinaChange",
			Filepath: filepath,
			Reason:   fmt.Sprintf("Failed to write file: %v", err),
		}
	}

	// Count new lines
	newLines := len(strings.Split(newContent, "\n"))
	linesChanged := abs(newLines - existingLines)

	return ProcessorEvent{
		Type:         "NinaChange",
		Filepath:     filepath,
		LinesChanged: linesChanged,
	}
}

func executeNinaBash(bashCmd util.BashCommand) ProcessorEvent {
	cwd, _ := os.Getwd()

	// Build command
	var command *exec.Cmd
	if len(bashCmd.Args) > 0 {
		// Command with args
		command = exec.Command(bashCmd.Command, bashCmd.Args...)
	} else {
		// Single command string - execute via bash
		command = exec.Command("timeout", "600s", "bash", "-c", bashCmd.Command)
	}

	stdout, err := command.Output()

	exitCode := 0
	stderr := ""

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
			stderr = string(exitErr.Stderr)
		} else {
			exitCode = -1
			stderr = err.Error()
		}
	}

	// Determine what was actually executed for logging
	cmd := bashCmd.Command
	args := bashCmd.Args
	if len(bashCmd.Args) == 0 {
		// If executed via bash -c
		cmd = "bash"
		args = []string{"-c", bashCmd.Command}
	}

	return ProcessorEvent{
		Type:     "NinaBash",
		Cwd:      cwd,
		Cmd:      cmd,
		Args:     args,
		ExitCode: exitCode,
		Stdout:   string(stdout),
		Stderr:   stderr,
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// LoopState is referenced from run.go
type LoopState struct {
	ResponseID    string
	TokensUsed    int
	MaxTokens     int
	StartTime     time.Time
	MessagesCount int
}

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
