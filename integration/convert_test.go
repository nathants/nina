//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nina/lib"
	util "nina/util"
)

// setupTestSession creates a session state with the given file content for testing.
func setupTestSession(fileName, fileContent string) (*util.SessionState, string) {
	session := &util.SessionState{
		PathMap:       make(map[string]string),
		SelectedFiles: make(map[string]string),
		OrigFiles:     make(map[string]string),
	}

	path := filepath.Join(os.TempDir(), fileName)
	session.PathMap[fileName] = path

	// Add line numbers to content as the converter expects
	numberedContent := util.AddLineNumbers(fileContent)
	session.SelectedFiles[path] = numberedContent
	session.OrigFiles[path] = fileContent

	return session, path
}

func TestConvertToRangeUpdatesIntegration(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		update        util.FileUpdate
		fileContent   string
		expectedStart int
		expectedEnd   int
		expectError   bool
		errorContains string
	}{
		{
			name: "exact match single line",
			update: util.FileUpdate{
				FileName:     "test.go",
				SearchLines:  []string{`    fmt.Println("Hello, World!")`},
				ReplaceLines: []string{`    fmt.Println("Hello, Universe!")`},
			},
			fileContent: `package main

import "fmt"

func main() {
    fmt.Println("Hello, World!")
}`,
			expectedStart: 6,
			expectedEnd:   6,
			expectError:   false,
		},
		{
			name: "exact match multiple lines",
			update: util.FileUpdate{
				FileName: "test.go",
				SearchLines: []string{
					`func helper() {`,
					`    return 42`,
					`}`,
				},
				ReplaceLines: []string{
					`func helper() int {`,
					`    return 42`,
					`}`,
				},
			},
			fileContent: `package main

func main() {
    println("test")
}

func helper() {
    return 42
}

func another() {
    println("end")
}`,
			expectedStart: 7,
			expectedEnd:   9,
			expectError:   false,
		},
		{
			name: "search with minor whitespace differences",
			update: util.FileUpdate{
				FileName: "test.go",
				SearchLines: []string{
					`func process(data string)  {`, // Extra space before {
					`    fmt.Println(data)`,
					`}`,
				},
				ReplaceLines: []string{
					`func process(data string) error {`,
					`    fmt.Println(data)`,
					`    return nil`,
					`}`,
				},
			},
			fileContent: `package main

import "fmt"

func process(data string) {
    fmt.Println(data)
}

func main() {
    process("test")
}`,
			expectedStart: 5,
			expectedEnd:   7,
			expectError:   false,
		},
		{
			name: "search not found",
			update: util.FileUpdate{
				FileName:     "test.go",
				SearchLines:  []string{`func notExists() {`},
				ReplaceLines: []string{`func exists() {`},
			},
			fileContent: `package main

func main() {
    println("test")
}`,
			expectedStart: -1,
			expectedEnd:   -1,
			expectError:   true,
			errorContains: "converter could not find search text",
		},
		{
			name: "search with tabs vs spaces",
			update: util.FileUpdate{
				FileName: "test.py",
				SearchLines: []string{
					`def calculate(x, y):`,
					`	return x + y`, // Tab instead of spaces
				},
				ReplaceLines: []string{
					`def calculate(x, y):`,
					`    return x * y`,
				},
			},
			fileContent: `class Math:
    def calculate(x, y):
        return x + y

    def subtract(x, y):
        return x - y`,
			expectedStart: 2,
			expectedEnd:   3,
			expectError:   false,
		},
	}

	// No-op reasoning callback
	reasoningCallback := func(_ string) {}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Create a context for this test
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			// Setup session files
			session, _ := setupTestSession(tt.update.FileName, tt.fileContent)

			// Create updates slice
			updates := []util.FileUpdate{tt.update}

			// Call ConvertToRangeUpdates with real API
			t.Logf("API call starting: ConvertToRangeUpdates for test case '%s'", tt.name)
			startTime := time.Now()
			convertedUpdates, err := lib.ConvertToRangeUpdates(ctx, updates, session, reasoningCallback)
			duration := time.Since(startTime)
			t.Logf("API call completed: ConvertToRangeUpdates for test case '%s' (duration: %v)", tt.name, duration)

			// Check error
			if tt.expectError {
				if err == nil {
					t.Fatalf("expected error containing '%s', got nil", tt.errorContains)
				}
				if tt.errorContains != "" && !strings.Contains(err.Error(), tt.errorContains) {
					t.Fatalf("expected error containing '%s', got: %v", tt.errorContains, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Verify result
			if len(convertedUpdates) != 1 {
				t.Fatalf("expected 1 converted update, got %d", len(convertedUpdates))
			}

			result := convertedUpdates[0]
			if result.StartLine != tt.expectedStart {
				t.Errorf("expected start line %d, got %d", tt.expectedStart, result.StartLine)
			}
			if result.EndLine != tt.expectedEnd {
				t.Errorf("expected end line %d, got %d", tt.expectedEnd, result.EndLine)
			}
		})
	}
}
