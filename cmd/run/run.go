package run

import (
	"io"
	"os"
	"strings"

	"github.com/alexflint/go-arg"
	"github.com/nathants/nina/lib"
	"github.com/nathants/nina/lib/processors"
)

func init() {
	lib.Commands["run"] = run
	lib.Args["run"] = runArgs{}
}

type runArgs struct {
	Model     string `arg:"-m,--model" default:"o3" help:"o3, gemini, opus, sonnet, grok, k2"`
	MaxTokens int    `arg:"--max-tokens" default:"200000" help:"Maximum tokens to use"`
	Debug     bool   `arg:"-d,--debug" help:"Show raw NinaInput and NinaOutput XML content"`
	UUID      string `arg:"--uuid" help:"UUID for process tracking (used by integration tests)"`
	Continue  bool   `arg:"-c,--continue" help:"Continue the last conversation from agents/api/*.input.json"`
	Thinking  bool   `arg:"-t,--thinking" help:"Enable thinking mode for supported models"`
}

func (runArgs) Description() string {
	return `Run nina`
}

func run() {
	var args runArgs
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

	// Create loop configuration with XML tool processor
	config := lib.LoopConfig{
		Model:         args.Model,
		MaxTokens:     args.MaxTokens,
		Debug:         args.Debug,
		UUID:          args.UUID,
		Continue:      args.Continue,
		ToolProcessor: &processors.XMLToolProcessor{},
		StdinContent:  stdinContent,
		Thinking:      args.Thinking,
	}

	// Run the main loop
	if err := lib.RunLoop(config); err != nil {
		lib.LogStderr("Error: %v", err)
		os.Exit(1)
	}
}
