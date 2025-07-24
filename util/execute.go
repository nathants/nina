// execute.go handles execution of bash commands and file changes from Nina output
package util

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ExecuteBash runs a bash command and returns the result
func ExecuteBash(cmd BashCommand) CommandResult {
	// Create command with bash -c
	bashCmd := exec.Command("bash", "-c", cmd.Command)

	// Capture output
	var stdout, stderr bytes.Buffer
	bashCmd.Stdout = &stdout
	bashCmd.Stderr = &stderr

	// Run command
	err := bashCmd.Run()

	// Get exit code
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			// If we can't get exit code, use -1
			exitCode = -1
			stderr.WriteString(fmt.Sprintf("\nError running command: %v", err))
		}
	}

	cwd, _ := os.Getwd()
	return CommandResult{
		Command:  cmd.Command,
		Cmd:      cmd.Command,
		Cwd:      cwd,
		Args:     cmd.Args,
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	}
}

// ApplyFileChange applies a file update and returns the result
func ApplyFileChange(update FileUpdate, sessionState *SessionState) ChangeResult {
	result := ChangeResult{
		FilePath: update.FileName,
		Args:     []string{},
	}

	// Validate the update
	err := ValidateFileUpdate(update, sessionState)
	if err != nil {
		result.Stderr = fmt.Sprintf("Validation error: %v", err)
		return result
	}

	// Get the resolved path
	resolvedPath := update.FileName
	if sessionState != nil && sessionState.PathMap != nil {
		if mapped, ok := sessionState.PathMap[update.FileName]; ok {
			resolvedPath = mapped
		}
	}

	// Read current content
	currentContent := ""
	fileData, err := os.ReadFile(resolvedPath)
	if err != nil && !os.IsNotExist(err) {
		result.Stderr = fmt.Sprintf("Error reading file: %v", err)
		return result
	}
	if err == nil {
		currentContent = string(fileData)
	}

	// Apply the update
	var newContent string
	if sessionState != nil && sessionState.OrigFiles != nil {
		// Use original content for search/replace
		origPath := resolvedPath
		if mapped, ok := sessionState.PathMap[update.FileName]; ok {
			// Find the original path
			for orig, dest := range sessionState.PathMap {
				if dest == mapped {
					origPath = orig
					break
				}
			}
		}

		originalContent := ""
		if orig, ok := sessionState.OrigFiles[origPath]; ok {
			originalContent = StripLineNumbers(orig)
		}

		// Apply updates with search
		var errs []error
		newContent, errs = ApplyUpdatesWithSearch(originalContent, currentContent, []FileUpdate{update})
		if len(errs) > 0 {
			var errStrs []string
			for _, e := range errs {
				errStrs = append(errStrs, e.Error())
			}
			result.Stderr = strings.Join(errStrs, "\n")
			return result
		}
	} else {
		// Simple apply without session state
		newContent, err = ApplyFileUpdates(currentContent, []FileUpdate{update})
		if err != nil {
			result.Stderr = fmt.Sprintf("Error applying update: %v", err)
			return result
		}
	}

	// Ensure directory exists
	dir := filepath.Dir(resolvedPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		result.Stderr = fmt.Sprintf("Error creating directory: %v", err)
		return result
	}

	// Write the file
	if err := os.WriteFile(resolvedPath, []byte(newContent), 0644); err != nil {
		result.Stderr = fmt.Sprintf("Error writing file: %v", err)
		return result
	}

	result.Stdout = fmt.Sprintf("Successfully updated %s", update.FileName)
	return result
}

// FormatNinaResult formats a command or change result as NinaResult XML
func FormatNinaResult(cmdResult *CommandResult, changeResult *ChangeResult) string {
	var buf strings.Builder
	buf.WriteString("<NinaResult>\n")

	if cmdResult != nil {
		buf.WriteString(fmt.Sprintf("<NinaCmd>%s</NinaCmd>\n", cmdResult.Command))
		buf.WriteString(fmt.Sprintf("<NinaExit>%d</NinaExit>\n", cmdResult.ExitCode))
		if len(cmdResult.Args) > 0 {
			buf.WriteString(fmt.Sprintf("<args>%s</args>\n", strings.Join(cmdResult.Args, " ")))
		}
		buf.WriteString(fmt.Sprintf("<NinaStdout>%s</NinaStdout>\n", cmdResult.Stdout))
		buf.WriteString(fmt.Sprintf("<NinaStderr>%s</NinaStderr>\n", cmdResult.Stderr))
	} else if changeResult != nil {
		buf.WriteString(fmt.Sprintf("<NinaChange>%s</NinaChange>\n", changeResult.FilePath))
		if len(changeResult.Args) > 0 {
			buf.WriteString(fmt.Sprintf("<args>%s</args>\n", strings.Join(changeResult.Args, " ")))
		}
		buf.WriteString(fmt.Sprintf("<NinaStdout>%s</NinaStdout>\n", changeResult.Stdout))
		buf.WriteString(fmt.Sprintf("<NinaStderr>%s</NinaStderr>\n", changeResult.Stderr))
	}

	buf.WriteString("</NinaResult>")
	return buf.String()
}

// ExecuteBash executes a bash command and returns the result
func ExecuteBashWithArgs(command string, args []string) CommandResult {
	bashCmd := BashCommand{
		Command: command,
		Args:    args,
	}
	return ExecuteBash(bashCmd)
}

// ExecuteChange applies a file change and returns the result
func ExecuteChange(filepath, searchText, replaceText string) ChangeResult {
	// Expand home directory if needed
	if strings.HasPrefix(filepath, "~/") {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			filepath = strings.Replace(filepath, "~/", homeDir+"/", 1)
		}
	}

	// Read the file
	content, err := os.ReadFile(filepath)
	if err != nil {
		return ChangeResult{
			FilePath: filepath,
			Stderr:   fmt.Sprintf("Failed to read file: %v", err),
		}
	}

	// Create file update
	update := FileUpdate{
		FileName: filepath,
	}

	var newContent string
	if searchText == "" {
		// Full file replacement
		update.ReplaceLines = strings.Split(replaceText, "\n")
		newContent, err = ApplyFileUpdates(string(content), []FileUpdate{update})
	} else {
		// Search/replace update
		update.SearchLines = TrimBlankLines(strings.Split(searchText, "\n"))
		update.ReplaceLines = TrimBlankLines(strings.Split(replaceText, "\n"))

		// Apply the update directly without AI conversion
		newContent, err = ApplyFileUpdates(string(content), []FileUpdate{update})
	}

	if err != nil {
		return ChangeResult{
			FilePath: filepath,
			Stderr:   fmt.Sprintf("Failed to apply changes: %v", err),
		}
	}

	// Count changed lines
	oldLines := strings.Split(string(content), "\n")
	newLines := strings.Split(newContent, "\n")
	linesChanged := 0
	maxLen := len(oldLines)
	if len(newLines) > maxLen {
		maxLen = len(newLines)
	}

	for i := 0; i < maxLen; i++ {
		if i >= len(oldLines) || i >= len(newLines) || oldLines[i] != newLines[i] {
			linesChanged++
		}
	}

	// Write the file
	err = os.WriteFile(filepath, []byte(newContent), 0644)
	if err != nil {
		return ChangeResult{
			FilePath: filepath,
			Stderr:   fmt.Sprintf("Failed to write file: %v", err),
		}
	}

	return ChangeResult{
		FilePath:     filepath,
		LinesChanged: linesChanged,
	}
}
