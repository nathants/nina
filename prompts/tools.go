// Package prompts provides tool definitions that mirror prompts/XML.md exactly.
// These definitions are used by providers to translate to their specific formats.
package prompts

// ToolDefinition represents a tool that can be invoked by the AI.
type ToolDefinition struct {
	Name        string
	Description string
	InputSchema ToolInputSchema
	ResultSchema ToolResultSchema
}

// ToolInputSchema defines the expected input for a tool.
type ToolInputSchema struct {
	// For NinaBash: the bash command string
	// For NinaChange: path, search, and replace strings
	Fields []ToolField
}

// ToolResultSchema defines the expected result from a tool.
type ToolResultSchema struct {
	// For NinaBash: cmd, exit, stdout, stderr
	// For NinaChange: change (filepath), error (optional)
	Fields []ToolField
}

// ToolField represents a single field in a tool's input or result.
type ToolField struct {
	Name        string
	Type        string // "string", "int"
	Required    bool
	Description string
}

// GetToolDefinitions returns the tool definitions that mirror XML.md exactly.
func GetToolDefinitions() []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        "NinaBash",
			Description: "bash string that will be run as `bash -c \"$cmd\"`",
			InputSchema: ToolInputSchema{
				Fields: []ToolField{
					{
						Name:        "command",
						Type:        "string",
						Required:    true,
						Description: "The bash command to execute",
					},
				},
			},
			ResultSchema: ToolResultSchema{
				Fields: []ToolField{
					{
						Name:        "NinaCmd",
						Type:        "string",
						Required:    true,
						Description: "your command",
					},
					{
						Name:        "NinaExit",
						Type:        "int",
						Required:    true,
						Description: "the exit code",
					},
					{
						Name:        "NinaStdout",
						Type:        "string",
						Required:    true,
						Description: "the stdout",
					},
					{
						Name:        "NinaStderr",
						Type:        "string",
						Required:    true,
						Description: "the stderr",
					},
				},
			},
		},
		{
			Name:        "NinaChange",
			Description: "search/replace once in a single file",
			InputSchema: ToolInputSchema{
				Fields: []ToolField{
					{
						Name:        "NinaPath",
						Type:        "string",
						Required:    true,
						Description: "the absolute filepath to changes (starts with `/` or `~/`)",
					},
					{
						Name:        "NinaSearch",
						Type:        "string",
						Required:    true,
						Description: "a block of entire contiguous lines to change",
					},
					{
						Name:        "NinaReplace",
						Type:        "string",
						Required:    true,
						Description: "the new text to replace that block",
					},
				},
			},
			ResultSchema: ToolResultSchema{
				Fields: []ToolField{
					{
						Name:        "NinaChange",
						Type:        "string",
						Required:    true,
						Description: "the filepath",
					},
					{
						Name:        "NinaError",
						Type:        "string",
						Required:    false,
						Description: "error if any",
					},
				},
			},
		},
	}
}

// TranslateToClaudeTools converts our tool definitions to Claude's format.
