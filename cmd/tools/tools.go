
// Tools command that uses JSON tool calling with Claude API
package tools

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/alexflint/go-arg"
	"github.com/nathants/nina/lib"
	claude "github.com/nathants/nina/providers/claude"
)

func init() {
	lib.Commands["tools"] = tools
	lib.Args["tools"] = toolsArgs{}
}

type toolsArgs struct {
	Model     string `arg:"-m,--model" default:"sonnet" help:"sonnet, opus, haiku"`
	MaxTokens int    `arg:"-t,--max-tokens" default:"4096" help:"Maximum tokens to use"`
	Debug     bool   `arg:"-d,--debug" help:"Show debug output including tool calls"`
}

func (toolsArgs) Description() string {
	return `Run nina with JSON tool calling`
}

func tools() {
	var args toolsArgs
	arg.MustParse(&args)

	// Initialize session for timestamp
	lib.InitializeSession(false)

	// Read input from stdin
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading stdin: %v\n", err)
		os.Exit(1)
	}
	if len(input) == 0 {
		fmt.Fprintf(os.Stderr, "Error: no input provided\n")
		os.Exit(1)
	}
	prompt := string(input)

	// Use a simple system prompt for JSON tools
	systemPrompt := "You are a helpful AI assistant with access to tools. Use the provided tools to help answer questions and complete tasks."

	// Map model names to Claude API model identifiers
	modelMap := map[string]string{
		"sonnet": "claude-3-5-sonnet-20241022",
		"opus":   "claude-3-opus-20240229",
		"haiku":  "claude-3-haiku-20240307",
	}
	
	claudeModel, ok := modelMap[args.Model]
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: unsupported model: %s\n", args.Model)
		fmt.Fprintf(os.Stderr, "Supported models: sonnet, opus, haiku\n")
		os.Exit(1)
	}

	// Call Claude with tools
	ctx := context.Background()
	response, err := claude.HandleTools(ctx, claudeModel, systemPrompt, prompt, args.MaxTokens, args.Debug)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error handling tools: %v\n", err)
		os.Exit(1)
	}

	// Output response
	fmt.Println(response.Text)
}
