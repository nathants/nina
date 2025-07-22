package util

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/tiktoken-go/tokenizer"
)

var (
	NinaOutputStart  = "<" + "NinaOutput" + ">"
	NinaOutputEnd    = "</" + "NinaOutput" + ">"
	NinaPathStart    = "<" + "NinaPath" + ">"
	NinaPathEnd      = "</" + "NinaPath" + ">"
	NinaContentStart = "<" + "NinaContent" + ">"
	NinaContentEnd   = "</" + "NinaContent" + ">"
	NinaStart        = "<" + "NinaChange" + ">"
	NinaEnd          = "</" + "NinaChange" + ">"
	NinaSearchStart  = "<" + "NinaSearch" + ">"
	NinaSearchEnd    = "</" + "NinaSearch" + ">"
	NinaReplaceStart = "<" + "NinaReplace" + ">"
	NinaReplaceEnd   = "</" + "NinaReplace" + ">"

	NinaChangeRangeStartStart = "<" + "NinaStart" + ">"
	NinaChangeRangeStartEnd   = "</" + "NinaStart" + ">"
	NinaChangeRangeEndStart   = "<" + "NinaEnd" + ">"
	NinaChangeRangeEndEnd     = "</" + "NinaEnd" + ">"

	NinaMessageStart = "<" + "NinaMessage" + ">"
	NinaMessageEnd   = "</" + "NinaMessage" + ">"
	NinaInputStart   = "<" + "NinaInput" + ">"
	NinaInputEnd     = "</" + "NinaInput" + ">"

	NinaPromptStart = "<" + "NinaPrompt" + ">"
	NinaPromptEnd   = "</" + "NinaPrompt" + ">"
	NinaSystemPromptStart = "<" + "NinaSystemPrompt" + ">"
	NinaSystemPromptEnd   = "</" + "NinaSystemPrompt" + ">"
	NinaFileStart   = "<" + "NinaFile" + ">"
	NinaFileEnd     = "</" + "NinaFile" + ">"
	NinaPatchStart  = "<" + "NinaPatch" + ">"
	NinaPatchEnd    = "</" + "NinaPatch" + ">"

	NinaBashStart   = "<" + "NinaBash" + ">"
	NinaBashEnd     = "</" + "NinaBash" + ">"
	NinaStopStart   = "<" + "NinaStop" + ">"
	NinaStopEnd     = "</" + "NinaStop" + ">"
	NinaResultStart = "<" + "NinaResult" + ">"
	NinaResultEnd   = "</" + "NinaResult" + ">"

	NinaSuggestionStart = "<" + "NinaSuggestion" + ">"
	NinaSuggestionEnd   = "</" + "NinaSuggestion" + ">"
)

type SessionUpdateData struct {
	Created       time.Time         `json:"created"`
	Type          string            `json:"type"`
	Updates       []FileUpdate      `json:"updates"`
	OrigFiles     map[string]string `json:"origFiles"`
	SelectedFiles map[string]string `json:"selectedFiles"`
	PathMap       map[string]string `json:"pathMap"`
}

type SessionState struct {
	ID            string            `json:"id"`
	OrigFiles     map[string]string `json:"origFiles"`
	SelectedFiles map[string]string `json:"selectedFiles"`
	Updates       []FileUpdate      `json:"updates"`
	PathMap       map[string]string `json:"pathMap"`
	TempDir       string            `json:"tempDir"`
	Applied       bool              `json:"applied"`
}

type FileUpdate struct {
	FileName     string
	SearchLines  []string
	ReplaceLines []string
	StartLine    int // 1-based inclusive start line for range updates
	EndLine      int // 1-based inclusive end line for range updates
}

// BashCommand represents a bash command to execute
type BashCommand struct {
	Command string
	Args    []string
}

// CommandResult represents the result of executing a command
type CommandResult struct {
	Command  string
	Args     []string
	ExitCode int
	Stdout   string
	Stderr   string
}

// ChangeResult represents the result of applying a file change
type ChangeResult struct {
	FilePath string
	Args     []string
	Stdout   string
	Stderr   string
}

func TrimBlankLines(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func ParseFileUpdates(output string) ([]FileUpdate, error) {
	ninaOutput, err := ExtractSingle(output, NinaOutputStart, NinaOutputEnd)
	if err != nil {
		return nil, err
	}
	if ninaOutput == "" {
		ninaOutput = output
	}

	var updates []FileUpdate

	// Handle range-based changes
	changeChunks, err := ExtractAll(ninaOutput, NinaStart, NinaEnd)
	if err != nil {
		return nil, err
	}

	for _, chunk := range changeChunks {
		path, err := ExtractSingle(chunk, NinaPathStart, NinaPathEnd)
		if err != nil {
			return nil, err
		}
		if path == "" {
			return nil, fmt.Errorf("missing NinaPath in NinaChange")
		}

		// Parse range format
		search, err := ExtractSingle(chunk, NinaSearchStart, NinaSearchEnd)
		if err != nil {
			return nil, err
		}

		replace, err := ExtractSingle(chunk, NinaReplaceStart, NinaReplaceEnd)
		if err != nil {
			return nil, err
		}

		path = strings.TrimSpace(path)

		searchLines := TrimBlankLines(strings.Split(search, "\n"))
		replaceLines := TrimBlankLines(strings.Split(replace, "\n"))

		updates = append(updates, FileUpdate{
			FileName:     path,
			SearchLines:  searchLines,
			ReplaceLines: replaceLines,
		})
	}

	if len(updates) == 0 {
		return nil, nil // no changes is not an error
	}

	return updates, nil
}

func ExtractNinaMessage(output string) (string, error) {
	ninaOutput, err := ExtractSingle(output, NinaOutputStart, NinaOutputEnd)
	if err != nil {
		return "", err
	}
	if ninaOutput == "" {
		ninaOutput = output
	}

	message, err := ExtractSingle(ninaOutput, NinaMessageStart, NinaMessageEnd)
	if err != nil {
		// This is a real error from ExtractSingle, not just a missing message
		return "", err
	}

	// If we didn't find the message tags at all, that's OK (it's optional)
	if message == "" && !strings.Contains(ninaOutput, NinaMessageStart) {
		return "", nil
	}

	return strings.TrimSpace(message), nil
}

// substring between start and stop tags, returns empty if start missing
// errors when stop not found, no external state, safe helper
// generic tag parse shared by proxy and server code
func ExtractSingle(src, start, stop string) (string, error) {
	i := strings.Index(src, start)
	if i == -1 {
		return "", nil
	}
	i += len(start)
	j := strings.Index(src[i:], stop)
	if j == -1 {
		return "", fmt.Errorf("tag error: %s", start)
	}
	return src[i : i+j], nil
}

// collects all substrings between start and stop tags across src
// returns error on malformed tags, keeps order, no state
// utility for iterating tag-separated data in nina components
func ExtractAll(src, start, stop string) ([]string, error) {
	var out []string
	for {
		i := strings.Index(src, start)
		if i == -1 {
			break
		}
		i += len(start)
		j := strings.Index(src[i:], stop)
		if j == -1 {
			return nil, fmt.Errorf("tag error: %s", start)
		}
		out = append(out, src[i:i+j])
		src = src[i+j+len(stop):]
	}
	return out, nil
}

func ValidateRangeReplace(update FileUpdate, sessionState *SessionState) error {
	// Line-range replacement mode.
	if update.StartLine > 0 || update.EndLine > 0 {
		path, ok := sessionState.PathMap[update.FileName]
		if !ok {
			return fmt.Errorf("missing pathMap for range update: %s", update.FileName)
		}

		data, ok := sessionState.OrigFiles[path]
		if !ok {
			return fmt.Errorf("file not found in session state: %s", path)
		}

		// Strip line numbers since SelectedFiles has them but we need actual line count
		data = StripLineNumbers(data)

		fileLines := strings.Split(data, "\n")

		// Validate range (1-based inclusive start, inclusive end).
		if update.StartLine <= 0 || update.EndLine < update.StartLine || update.EndLine > len(fileLines) {
			return fmt.Errorf("invalid line range [%d,%d] for %s (%d lines)", update.StartLine, update.EndLine, update.FileName, len(fileLines))
		}

		// fmt.Println("line-range validation passed for", update.FileName, "=>", path)
		return nil
	}

	// This is a rewrite (empty range) - just validate the path
	fileName := update.FileName

	parent, ok := sessionState.PathMap["_parent"]
	if ok {
		session, ok2 := sessionState.PathMap["_session"]
		if !ok2 {
			return fmt.Errorf("both of _parent and _session must be in pathMap or neither")
		}

		orig := fileName
		rel, err := filepath.Rel(parent, fileName)
		useRel := err == nil && !strings.Contains(rel, "..")

		tempDir := filepath.Join("/app/files", session)
		var dest string
		if useRel {
			dest = filepath.Join(tempDir, rel)
		} else {
			dest = filepath.Join(tempDir, filepath.Base(fileName))
		}
		sessionState.PathMap[orig] = dest
		fmt.Println("validated rewrite path:", orig, "=>", dest)
	} else {
		fmt.Println("validated rewrite path:", fileName)
	}

	return nil
}

func ValidateFileUpdate(update FileUpdate, sessionState *SessionState) error {
	err := ValidateRangeReplace(update, sessionState)
	if err != nil {
		// fmt.Println("error: validation failed for", update.FileName, err)
		return err
	}

	return nil
}

// GroupUpdatesByFile groups updates by filename while preserving order within each file
func GroupUpdatesByFile(updates []FileUpdate) map[string][]FileUpdate {
	grouped := make(map[string][]FileUpdate)
	for _, update := range updates {
		grouped[update.FileName] = append(grouped[update.FileName], update)
	}
	return grouped
}

// SortUpdatesForApplication sorts updates within a file by StartLine descending
// This ensures that later edits don't affect earlier edit line numbers
func SortUpdatesForApplication(updates []FileUpdate) []FileUpdate {
	sorted := make([]FileUpdate, len(updates))
	copy(sorted, updates)
	slices.SortFunc(sorted, func(a, b FileUpdate) int {
		// For full rewrites (StartLine == 0), treat them as line 0
		aStart := a.StartLine
		if aStart == 0 && a.EndLine == 0 {
			aStart = 0
		}
		bStart := b.StartLine
		if bStart == 0 && b.EndLine == 0 {
			bStart = 0
		}
		// Sort by StartLine descending (higher lines first)
		return bStart - aStart
	})
	return sorted
}

// ApplyFileUpdates applies a sorted list of updates to file content
// Updates should be pre-sorted by StartLine descending to avoid line number shifts
func ApplyFileUpdates(content string, updates []FileUpdate) (string, error) {
	for _, update := range updates {
		// For line-range updates
		if update.StartLine > 0 || update.EndLine > 0 {
			lines := strings.Split(content, "\n")
			if update.StartLine <= 0 || update.EndLine < update.StartLine || update.EndLine > len(lines) {
				return "", fmt.Errorf("invalid line range [%d,%d] for %d lines",
					update.StartLine, update.EndLine, len(lines))
			}

			before := lines[:update.StartLine-1]
			after := lines[update.EndLine:]
			newLines := slices.Clone(before)
			newLines = append(newLines, update.ReplaceLines...)
			newLines = append(newLines, after...)
			content = strings.Join(newLines, "\n")
		} else {
			// Full file rewrite
			content = strings.Join(update.ReplaceLines, "\n")
		}
	}
	return content, nil
}

// ApplyRangeReplace applies a single FileUpdate (range or full rewrite) and
// returns the updated content.
//
// Historical versions of the code (and the tests) expected the signature
//
//	func ApplyRangeReplace(update FileUpdate, dryRun bool, state *SessionState)
//
// while the most recent refactor changed it to
//
//	func ApplyRangeReplace(content string, update FileUpdate)
//
// which broke compilation of the test suite.
//
// To restore compatibility we re-introduce the original three-parameter
// signature.  When a SessionState is supplied we attempt to locate the
// original file content in the session so the helper can operate on the same
// data the AI saw.  If the lookup fails, an empty string is used.  The dryRun
// flag is accepted for API stability but currently has no behavioural effect.
func ApplyRangeReplace(update FileUpdate, dryRun bool, sessionState *SessionState) (string, error) {
	_ = dryRun // kept for API compatibility
	var content string
	if sessionState != nil {
		if path, ok := sessionState.PathMap[update.FileName]; ok {
			if data, ok2 := sessionState.OrigFiles[path]; ok2 {
				content = StripLineNumbers(data)
			}
		}
	}
	return ApplyFileUpdates(content, []FileUpdate{update})
}

// ApplyUpdatesWithSearch applies updates to currentContent using exact text search/replace
// based on the original content. Returns the updated content and any errors.
func ApplyUpdatesWithSearch(originalContent, currentContent string, updates []FileUpdate) (string, []error) {
	var errs []error

	// Work with the current content as a slice of lines so we can perform
	// replacements without accidentally matching substrings inside longer lines.
	resultLines := strings.Split(currentContent, "\n")

	for _, update := range updates {
		// Handle full-file rewrites immediately.
		if update.StartLine == 0 && update.EndLine == 0 {
			resultLines = slices.Clone(update.ReplaceLines)
			continue
		}

		// Validate the requested line range against the ORIGINAL content that
		// the AI saw.  We intentionally keep using originalContent here so
		// that line numbers remain stable even after earlier edits have been
		// applied to resultLines.
		origLines := strings.Split(originalContent, "\n")
		if update.StartLine <= 0 ||
			update.EndLine < update.StartLine ||
			update.EndLine > len(origLines) {
			errs = append(errs, fmt.Errorf(
				"invalid line range [%d,%d] for %d lines",
				update.StartLine, update.EndLine, len(origLines),
			))
			continue
		}

		searchLines := origLines[update.StartLine-1 : update.EndLine]
		replaceLines := update.ReplaceLines

		// Locate the *exact* sequence of searchLines within the current result.
		matchIdx := -1
		matchCount := 0
		for i := 0; i+len(searchLines) <= len(resultLines); i++ {
			if slices.Equal(searchLines, resultLines[i:i+len(searchLines)]) {
				matchCount++
				if matchIdx == -1 {
					matchIdx = i
				}
			}
		}
		switch {
		case matchCount == 0:
			errs = append(errs, fmt.Errorf(
				"search text not found in current file (lines %d-%d)",
				update.StartLine, update.EndLine-1,
			))
			continue
		case matchCount > 1:
			errs = append(errs, fmt.Errorf(
				"search text found %d times in current file (lines %d-%d), must be unique",
				matchCount, update.StartLine, update.EndLine-1,
			))
			continue
		default:
			// exactly one match found; proceed with replacement
		}

		// Perform the replacement.
		newResult := append([]string{}, resultLines[:matchIdx]...)
		newResult = append(newResult, replaceLines...)
		newResult = append(newResult, resultLines[matchIdx+len(searchLines):]...)
		resultLines = newResult
	}

	return strings.Join(resultLines, "\n"), errs
}

// addLineNumbers prefixes each line with a 1-based index for LLM context.
func AddLineNumbers(content string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = fmt.Sprintf("%d: %s", i+1, line)
	}
	return strings.Join(lines, "\n")
}

// stripLineNumbers removes the "<num>: " prefix inserted by addLineNumbers.
func StripLineNumbers(content string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if idx := strings.Index(line, ": "); idx > 0 {
			if lineNum := line[:idx]; IsNumeric(lineNum) {
				lines[i] = line[idx+2:]
			}
		}
	}
	return strings.Join(lines, "\n")
}

// IsNumeric reports whether the string is entirely ASCII digits.
func IsNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// removeFencedBlocks extracts code from markdown ``` fences discarding prose.
func RemoveFencedBlocks(content string) string {
	pat := regexp.MustCompile("(?s)```(?:json|jsonl)?\\s*\n(.*?)```")
	matches := pat.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return content
	}
	var parts []string
	for _, m := range matches {
		if len(m) > 1 {
			parts = append(parts, strings.TrimSuffix(m[1], "\n"))
		}
	}
	return strings.Join(parts, "\n")
}

// CalculateTokens returns approximate token count using o200k tokenizer.
func CalculateTokens(text string) int {
	enc, err := tokenizer.Get(tokenizer.O200kBase)
	if err != nil {
		panic(err)
	}
	ids, _, err := enc.Encode(text)
	if err != nil {
		return 0
	}
	return len(ids)
}

// CalculateMessageTokens counts tokens for a message with role and content.
// Includes overhead for message structure in the API format.
func CalculateMessageTokens(role, content string) int {
	// Account for message structure overhead (role, content wrapper)
	// Typical overhead is ~4 tokens per message for structure
	structureOverhead := 4
	return structureOverhead + CalculateTokens(role) + CalculateTokens(content)
}

// CalculateToolDefinitionTokens counts tokens for tool definitions.
// Tools are sent as JSON structures with name, description, and parameters.
func CalculateToolDefinitionTokens(toolJSON string) int {
	// Tool definitions have additional JSON structure overhead
	structureOverhead := 10 // Estimate for JSON wrapper and schema
	return structureOverhead + CalculateTokens(toolJSON)
}

// CalculateSystemPromptTokens counts tokens for system prompts.
// System prompts may have additional formatting or wrapper overhead.
func CalculateSystemPromptTokens(systemPrompt string) int {
	// System prompts typically have minimal overhead
	structureOverhead := 2
	return structureOverhead + CalculateTokens(systemPrompt)
}

// CalculateTotalTokens counts all tokens for a complete API request.
// Includes system prompt, messages, and tool definitions.
func CalculateTotalTokens(systemPrompt string, messages []map[string]string, toolDefs []string) int {
	total := 0

	// Count system prompt tokens
	if systemPrompt != "" {
		total += CalculateSystemPromptTokens(systemPrompt)
	}

	// Count message tokens
	for _, msg := range messages {
		role := msg["role"]
		content := msg["content"]
		total += CalculateMessageTokens(role, content)
	}

	// Count tool definition tokens
	for _, toolDef := range toolDefs {
		total += CalculateToolDefinitionTokens(toolDef)
	}

	return total
}

// SharedParentDir computes common parent directory across paths in pathMap.
func SharedParentDir(pathMap map[string]string) string {
	if len(pathMap) == 0 {
		return ""
	}
	if len(pathMap) == 1 {
		for path := range pathMap {
			return filepath.Dir(path)
		}
	}
	paths := make([]string, 0, len(pathMap))
	for path := range pathMap {
		paths = append(paths, path)
	}
	firstClean := filepath.Clean(paths[0])
	absolute := filepath.IsAbs(firstClean)
	common := strings.Split(firstClean, string(os.PathSeparator))
	for _, p := range paths[1:] {
		parts := strings.Split(filepath.Clean(p), string(os.PathSeparator))
		max := min(len(parts), len(common))
		i := 0
		for i < max && common[i] == parts[i] {
			i++
		}
		common = common[:i]
		if len(common) == 0 {
			break
		}
	}
	dir := filepath.Join(common...)
	if absolute && dir != "" && !filepath.IsAbs(dir) {
		dir = string(os.PathSeparator) + dir
	}
	return dir

}

type RangeResult struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// ParseNinaBash extracts bash commands from NinaOutput
func ParseNinaBash(output string) ([]BashCommand, error) {
	ninaOutput, err := ExtractSingle(output, NinaOutputStart, NinaOutputEnd)
	if err != nil {
		return nil, err
	}
	if ninaOutput == "" {
		ninaOutput = output
	}

	var commands []BashCommand
	bashChunks, err := ExtractAll(ninaOutput, NinaBashStart, NinaBashEnd)
	if err != nil {
		return nil, err
	}

	for _, chunk := range bashChunks {
		// Parse bash command - for now just store the raw content
		// Could be enhanced to parse args separately if needed
		cmd := strings.TrimSpace(chunk)
		if cmd != "" {
			commands = append(commands, BashCommand{
				Command: cmd,
				Args:    []string{},
			})
		}
	}

	return commands, nil
}

// ParseNinaStop extracts stop reason from NinaOutput
func ParseNinaStop(output string) (string, error) {
	ninaOutput, err := ExtractSingle(output, NinaOutputStart, NinaOutputEnd)
	if err != nil {
		return "", err
	}
	if ninaOutput == "" {
		ninaOutput = output
	}

	stopReason, err := ExtractSingle(ninaOutput, NinaStopStart, NinaStopEnd)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(stopReason), nil
}

// ParseNinaResult extracts command/change results from input
func ParseNinaResult(input string) (*CommandResult, *ChangeResult, error) {
	result, err := ExtractSingle(input, NinaResultStart, NinaResultEnd)
	if err != nil {
		return nil, nil, err
	}
	if result == "" {
		return nil, nil, nil
	}

	// Check if it's a command result
	cmd, _ := ExtractSingle(result, "<NinaCmd>", "</NinaCmd>")
	if cmd != "" {
		exitStr, _ := ExtractSingle(result, "<NinaExit>", "</NinaExit>")
		exitCode := 0
		if exitStr != "" {
			_, _ = fmt.Sscanf(exitStr, "%d", &exitCode)
		}

		stdout, _ := ExtractSingle(result, "<NinaStdout>", "</NinaStdout>")
		stderr, _ := ExtractSingle(result, "<NinaStderr>", "</NinaStderr>")
		argsStr, _ := ExtractSingle(result, "<args>", "</args>")

		var args []string
		if argsStr != "" {
			args = strings.Fields(argsStr)
		}

		return &CommandResult{
			Command:  cmd,
			Args:     args,
			ExitCode: exitCode,
			Stdout:   stdout,
			Stderr:   stderr,
		}, nil, nil
	}

	// Check if it's a change result
	change, _ := ExtractSingle(result, "<NinaChange>", "</NinaChange>")
	if change != "" {
		stdout, _ := ExtractSingle(result, "<NinaStdout>", "</NinaStdout>")
		stderr, _ := ExtractSingle(result, "<NinaStderr>", "</NinaStderr>")
		argsStr, _ := ExtractSingle(result, "<args>", "</args>")

		var args []string
		if argsStr != "" {
			args = strings.Fields(argsStr)
		}

		return nil, &ChangeResult{
			FilePath: change,
			Args:     args,
			Stdout:   stdout,
			Stderr:   stderr,
		}, nil
	}

	return nil, nil, fmt.Errorf("unknown result type")
}
