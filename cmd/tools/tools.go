// Tools command that uses JSON tool calling with Claude API
package tools

import (
	"io"
	"os"
	"strings"

	"github.com/alexflint/go-arg"
	"github.com/nathants/nina/lib"
	"github.com/nathants/nina/lib/processors"
)

func init() {
	lib.Commands["tools"] = tools
	lib.Args["tools"] = toolsArgs{}
}

type toolsArgs struct {
	Model     string `arg:"-m,--model" default:"sonnet" help:"Model to use (e.g., sonnet, opus, o4-mini, gemini)"`
	MaxTokens int    `arg:"--max-tokens" default:"200000" help:"Maximum tokens to use"`
	Debug     bool   `arg:"-d,--debug" help:"Show debug output including tool calls"`
	UUID      string `arg:"--uuid" help:"UUID for process tracking (used by integration tests)"`
	Continue  bool   `arg:"-c,--continue" help:"Continue the last conversation from agents/api/*.input.json"`
	Thinking  bool   `arg:"-t,--thinking" help:"Enable thinking mode for supported models"`
}

func (toolsArgs) Description() string {
	return `Run nina with JSON tool calling`
}

func tools() {
	var args toolsArgs
	arg.MustParse(&args)

	// Initialize session for proper log numbering
	lib.InitializeSession(args.Continue)

	// Read stdin content
	stdinContent := ""
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		// Input is available (pipe or redirect)
		stdinBytes, err := io.ReadAll(os.Stdin)
		if err != nil {
			lib.LogStderr("Failed to read stdin: %v", err)
			os.Exit(1)
		}
		stdinContent = strings.TrimSpace(string(stdinBytes))
	}

	// Check for TASK.md and use as prompt if no stdin
	if stdinContent == "" {
		if taskData, err := os.ReadFile("TASK.md"); err == nil && len(taskData) > 0 {
			taskContent := strings.TrimSpace(string(taskData))
			if taskContent != "" {
				stdinContent = taskContent
				// Wipe TASK.md after reading
				if err := os.Truncate("TASK.md", 0); err != nil {
					lib.LogStderr("Failed to truncate TASK.md: %v", err)
				}
			}
		}
	}

	if stdinContent == "" {
		lib.LogStderr("Error: no input provided")
		os.Exit(1)
	}

	// Create loop configuration with JSON tool processor
	config := lib.LoopConfig{
		Model:         args.Model,
		MaxTokens:     args.MaxTokens,
		Debug:         args.Debug,
		UUID:          args.UUID,
		Continue:      args.Continue,
		ToolProcessor: &processors.JSONToolProcessor{},
		StdinContent:  stdinContent,
		Thinking:      args.Thinking,
	}

	// Run the main loop
	if err := lib.RunLoop(config); err != nil {
		lib.LogStderr("Error: %v", err)
		os.Exit(1)
	}
}
