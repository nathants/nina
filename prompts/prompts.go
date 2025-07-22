// Package prompts provides access to system prompts for various CLI tools.
package prompts

import (
	"embed"
)

//go:embed ASK.md
var askPrompt string

//go:embed CHOOSE.md
var choosePrompt string

//go:embed *.md
var EmbeddedFiles embed.FS

// Ask returns the system prompt for the ask CLI
func Ask() string {
	return askPrompt
}

// Choose returns the system prompt for the choose CLI
func Choose() string {
	return choosePrompt
}
