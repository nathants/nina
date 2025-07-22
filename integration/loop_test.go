//go:build integration

// loop_test.go contains table-driven integration tests for nina.
package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func TestLoopSimpleEdit(t *testing.T) {
	t.Parallel()
	if os.Getenv("FAST") != "" {
		t.Skip("skipping integration test in FAST mode")
	}
	tests := []struct {
		name        string
		files       map[string]string
		prompt      string
		wantContent map[string]string // exact content match
		wantExists  []string          // files that should exist
	}{
		{
			name: "edit readme",
			files: map[string]string{
				"README.md": "hello",
			},
			prompt: "change hello to hi in README.md",
			wantContent: map[string]string{
				"README.md": "hi",
			},
			wantExists: []string{}, // o4-mini doesn't create TODO.md or REPORT.md
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fmt.Printf("\n+================================================================+\n")
			fmt.Printf("| TEST: %-56s |\n", tt.name)
			fmt.Printf("+================================================================+\n")
			fmt.Printf("\n[TEST DETAILS]\n")
			fmt.Printf("Purpose: Test simple file editing capability\n")
			fmt.Printf("Prompt: %s\n", tt.prompt)
			fmt.Printf("Initial files: %v\n", mapKeys(tt.files))
			fmt.Printf("Expected outcome:\n")
			for path, content := range tt.wantContent {
				fmt.Printf("  - %s should contain exactly: %q\n", path, content)
			}
			for _, path := range tt.wantExists {
				fmt.Printf("  - %s should exist\n", path)
			}

			repo := CreateTempRepo(t, tt.files)
			if err := RunNinaLoopWithFiles(t, repo, tt.prompt, tt.files, ""); err != nil {
				t.Fatalf("nina run: %v", err)
			}

			fmt.Printf("\n[VERIFICATION] Checking test results...\n")
			// Check exact content matches
			for path, want := range tt.wantContent {
				fullPath := filepath.Join(repo, path)
				got, err := os.ReadFile(fullPath)
				if err != nil {
					fmt.Printf("FAIL Failed to read %s: %v\n", path, err)
					t.Fatalf("read %s: %v", path, err)
				}
				fmt.Printf("Verifying %s:\n", path)
				fmt.Printf("  Want: %q\n", want)
				fmt.Printf("  Got:  %q\n", string(got))
				if string(got) != want {
					fmt.Printf("  FAIL MISMATCH - content differs\n")
					t.Fatalf("file %s got=%q want=%q", path, got, want)
				}
				fmt.Printf("  OK OK - exact match\n")
			}
			// Check files exist
			for _, path := range tt.wantExists {
				fullPath := filepath.Join(repo, path)
				fmt.Printf("Checking existence of %s: ", path)
				if _, err := os.Stat(fullPath); err != nil {
					fmt.Printf("FAIL NOT FOUND\n")
					t.Fatalf("file %s should exist: %v", path, err)
				}
				fmt.Printf("OK EXISTS\n")
			}

			fmt.Printf("\n[TEST PASSED] All verifications successful\n")
			fmt.Printf("=================================================================\n")
		})
	}
}

func TestLoopContinuation(t *testing.T) {
	t.Parallel()
	if os.Getenv("FAST") != "" {
		t.Skip("skipping integration test in FAST mode")
	}

	// Test continuation for both o4-mini and sonnet models
	models := []string{"o4-mini", "sonnet"}

	for _, model := range models {
		t.Run(fmt.Sprintf("continue_%s", model), func(t *testing.T) {
			fmt.Printf("\n+================================================================+\n")
			fmt.Printf("| TEST: %-56s |\n", fmt.Sprintf("continue_%s", model))
			fmt.Printf("+================================================================+\n")
			fmt.Printf("\n[TEST DETAILS]\n")
			fmt.Printf("Purpose: Test --continue functionality with %s model\n", model)
			fmt.Printf("Expected outcome: Second command should continue from first\n")

			// Create a temp repo with a simple file
			files := map[string]string{
				"number.txt": "0",
			}
			repo := CreateTempRepo(t, files)

			// First command: ask the model to pick a random number and write it
			fmt.Printf("\n[STEP 1] Initial prompt to pick a number\n")
			prompt1 := "pick a random number between 1 and 10 and write it to number.txt"
			if err := RunNinaLoopWithModel(t, repo, prompt1, files, "", model); err != nil {
				t.Fatalf("first nina run: %v", err)
			}

			// Read the number that was written
			numberPath := filepath.Join(repo, "number.txt")
			content1, err := os.ReadFile(numberPath)
			if err != nil {
				t.Fatalf("read number.txt after first run: %v", err)
			}
			number := strings.TrimSpace(string(content1))
			fmt.Printf("Number written in first run: %s\n", number)

			// Verify it's a number between 1 and 10
			if number == "0" || number == "" {
				t.Fatalf("model didn't update number.txt in first run")
			}

			// Second command: use --continue to ask what the number was
			fmt.Printf("\n[STEP 2] Using --continue to ask about the number\n")
			prompt2 := "what was the number I asked you to pick? write it to answer.txt"

			// Create a custom context with continue flag
			ctx := context.WithValue(context.Background(), "continue", true)
			if err := RunNinaLoopWithModelAndContext(ctx, t, repo, prompt2, files, "", model); err != nil {
				t.Fatalf("second nina run with --continue: %v", err)
			}

			// Check if answer.txt was created with the same number
			answerPath := filepath.Join(repo, "answer.txt")
			content2, err := os.ReadFile(answerPath)
			if err != nil {
				t.Fatalf("read answer.txt after continue: %v", err)
			}
			answer := strings.TrimSpace(string(content2))
			fmt.Printf("Number written in continue run: %s\n", answer)

			// The answer should contain the original number
			if !strings.Contains(answer, number) {
				t.Fatalf("continuation failed: original number %s not found in answer %s", number, answer)
			}

			fmt.Printf("\n[TEST PASSED] Continuation worked correctly for %s\n", model)
			fmt.Printf("=================================================================\n")
		})
	}
}

func TestLoopCreateFile(t *testing.T) {
	t.Parallel()
	if os.Getenv("FAST") != "" {
		t.Skip("skipping integration test in FAST mode")
	}
	tests := []struct {
		name         string
		files        map[string]string
		prompt       string
		wantContains map[string][]string
		wantExists   []string
	}{
		{
			name:   "create config file",
			files:  map[string]string{},
			prompt: "create a config.json file with {\"version\": \"1.0\"}",
			wantContains: map[string][]string{
				"config.json": {`"version": "1.0"`},
			},
			wantExists: []string{"config.json"},
		},
		{
			name: "create go file",
			files: map[string]string{
				"main.go": "package main\n",
			},
			prompt: "create hello.go with a simple hello world function",
			wantContains: map[string][]string{
				"hello.go": {"package main", "func"},
			},
			wantExists: []string{"hello.go", "main.go"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fmt.Printf("\n+================================================================+\n")
			fmt.Printf("| TEST: %-56s |\n", tt.name)
			fmt.Printf("+================================================================+\n")
			fmt.Printf("\n[TEST DETAILS]\n")
			fmt.Printf("Purpose: Test file creation capability\n")
			fmt.Printf("Prompt: %s\n", tt.prompt)
			fmt.Printf("Initial files: %v\n", mapKeys(tt.files))
			fmt.Printf("Expected outcome:\n")
			for path, strings := range tt.wantContains {
				fmt.Printf("  - %s should contain: %v\n", path, strings)
			}
			for _, path := range tt.wantExists {
				fmt.Printf("  - %s should exist\n", path)
			}

			repo := CreateTempRepo(t, tt.files)
			if err := RunNinaLoopWithFiles(t, repo, tt.prompt, tt.files, ""); err != nil {
				t.Fatalf("nina run: %v", err)
			}

			fmt.Printf("\n[VERIFICATION] Checking test results...\n")
			// Check file contains expected strings
			for path, wantStrings := range tt.wantContains {
				fullPath := filepath.Join(repo, path)
				got, err := os.ReadFile(fullPath)
				if err != nil {
					fmt.Printf("FAIL Failed to read %s: %v\n", path, err)
					t.Fatalf("read %s: %v", path, err)
				}
				fmt.Printf("Verifying %s contains %d required strings:\n", path, len(wantStrings))
				content := string(got)
				allFound := true
				for i, want := range wantStrings {
					if strings.Contains(content, want) {
						fmt.Printf("  [%d] OK Found: %q\n", i+1, want)
					} else {
						fmt.Printf("  [%d] FAIL Missing: %q\n", i+1, want)
						allFound = false
					}
				}
				if !allFound {
					fmt.Printf("  File content (%d bytes):\n", len(content))
					fmt.Printf("  ---\n%s\n  ---\n", content)
					t.Fatalf("file %s missing required strings", path)
				}
			}
			// Check files exist
			for _, path := range tt.wantExists {
				fullPath := filepath.Join(repo, path)
				fmt.Printf("Checking existence of %s: ", path)
				if info, err := os.Stat(fullPath); err != nil {
					fmt.Printf("FAIL NOT FOUND\n")
					t.Fatalf("file %s should exist: %v", path, err)
				} else {
					fmt.Printf("OK EXISTS (%d bytes)\n", info.Size())
				}
			}

			fmt.Printf("\n[TEST PASSED] All verifications successful\n")
			fmt.Printf("=================================================================\n")
		})
	}
}

func TestLoopAppendFile(t *testing.T) {
	t.Parallel()
	if os.Getenv("FAST") != "" {
		t.Skip("skipping integration test in FAST mode")
	}
	tests := []struct {
		name         string
		files        map[string]string
		prompt       string
		wantContains map[string][]string // check file contains these strings
		wantExists   []string
	}{
		{
			name: "append to readme",
			files: map[string]string{
				"README.md": "# Project\n\nThis is my project.",
			},
			prompt: "append a new section '## Features' with a bullet point '- Fast' to README.md",
			wantContains: map[string][]string{
				"README.md": {"# Project", "## Features", "- Fast"},
			},
			wantExists: []string{},
		},
		{
			name: "append to gitignore",
			files: map[string]string{
				".gitignore": "*.log\n*.tmp\n",
			},
			prompt: "add *.bak and node_modules/ to .gitignore",
			wantContains: map[string][]string{
				".gitignore": {"*.log", "*.tmp", "*.bak", "node_modules/"},
			},
			wantExists: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fmt.Printf("\n+================================================================+\n")
			fmt.Printf("| TEST: %-56s |\n", tt.name)
			fmt.Printf("+================================================================+\n")
			fmt.Printf("\n[TEST DETAILS]\n")
			fmt.Printf("Purpose: Test appending content to existing files\n")
			fmt.Printf("Prompt: %s\n", tt.prompt)
			fmt.Printf("Initial files: %v\n", mapKeys(tt.files))
			fmt.Printf("Expected outcome:\n")
			for path, strings := range tt.wantContains {
				fmt.Printf("  - %s should contain all of: %v\n", path, strings)
			}

			repo := CreateTempRepo(t, tt.files)

			// Show original content for comparison
			fmt.Printf("\n[ORIGINAL CONTENT]\n")
			for path, content := range tt.files {
				fmt.Printf("File %s:\n", path)
				fmt.Printf("---\n%s\n---\n", content)
			}

			if err := RunNinaLoopWithFiles(t, repo, tt.prompt, tt.files, ""); err != nil {
				t.Fatalf("nina run: %v", err)
			}

			fmt.Printf("\n[VERIFICATION] Checking test results...\n")
			// Check file contains expected strings
			for path, wantStrings := range tt.wantContains {
				fullPath := filepath.Join(repo, path)
				got, err := os.ReadFile(fullPath)
				if err != nil {
					fmt.Printf("FAIL Failed to read %s: %v\n", path, err)
					t.Fatalf("read %s: %v", path, err)
				}
				fmt.Printf("Verifying %s contains %d required strings:\n", path, len(wantStrings))
				content := string(got)
				allFound := true
				for i, want := range wantStrings {
					if strings.Contains(content, want) {
						fmt.Printf("  [%d] OK Found: %q\n", i+1, want)
					} else {
						fmt.Printf("  [%d] FAIL Missing: %q\n", i+1, want)
						allFound = false
					}
				}

				fmt.Printf("\n[MODIFIED CONTENT]\n")
				fmt.Printf("File %s after modification:\n", path)
				fmt.Printf("---\n%s\n---\n", content)

				if !allFound {
					t.Fatalf("file %s missing required strings", path)
				}
			}
			// Check files exist
			for _, path := range tt.wantExists {
				fullPath := filepath.Join(repo, path)
				fmt.Printf("Checking existence of %s: ", path)
				if info, err := os.Stat(fullPath); err != nil {
					fmt.Printf("FAIL NOT FOUND\n")
					t.Fatalf("file %s should exist: %v", path, err)
				} else {
					fmt.Printf("OK EXISTS (%d bytes)\n", info.Size())
				}
			}

			fmt.Printf("\n[TEST PASSED] All verifications successful\n")
			fmt.Printf("=================================================================\n")
		})
	}
}

func TestLoopMultiLineEdit(t *testing.T) {
	if os.Getenv("FAST") != "" {
		t.Skip("skipping integration test in FAST mode")
	}
	tests := []struct {
		name            string
		files           map[string]string
		prompt          string
		wantContains    map[string][]string
		wantNotContains map[string][]string
	}{
		{
			name: "replace multiple lines in go file",
			files: map[string]string{
				"main.go": `package main

import "fmt"

func main() {
	fmt.Println("Hello")
	fmt.Println("World")
	fmt.Println("From Go")
}`,
			},
			prompt: "replace the three fmt.Println lines with a single fmt.Printf that prints 'Hello World from Go!' with a newline",
			wantContains: map[string][]string{
				"main.go": {
					"package main",
					"import",
					"fmt.Printf",
					"Hello World from Go!",
				},
			},
			wantNotContains: map[string][]string{
				"main.go": {
					`fmt.Println("Hello")`,
					`fmt.Println("World")`,
					`fmt.Println("From Go")`,
				},
			},
		},
		{
			name: "replace function body",
			files: map[string]string{
				"calc.go": `package main

func add(a, b int) int {
	sum := a + b
	// Log the operation
	println("Adding", a, "and", b)
	println("Result is", sum)
	return sum
}`,
			},
			prompt: "simplify the add function to just return a + b without any logging",
			wantContains: map[string][]string{
				"calc.go": {
					"func add(a, b int) int",
					"return a + b",
				},
			},
			wantNotContains: map[string][]string{
				"calc.go": {
					"sum :=",
					"println",
					"// Log the operation",
				},
			},
		},
		{
			name: "replace config block",
			files: map[string]string{
				"config.json": `{
//   "server": {
//     "host": "localhost",
//     "port": 8080,
//     "debug": true,
//     "logs": {
//       "level": "debug",
//       "file": "/tmp/app.log"
//     }
//   },
//   "database": {
//     "host": "localhost",
//     "port": 5432
//   }
}`,
			},
			prompt: "replace the entire logs section with a simple logLevel: 'info' at the server level",
			wantContains: map[string][]string{
				"config.json": {
					`"server"`,
					`"logLevel": "info"`,
					`"database"`,
				},
			},
			wantNotContains: map[string][]string{
				"config.json": {
					`"logs"`,
					`"level": "debug"`,
					`"file": "/tmp/app.log"`,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fmt.Printf("\n+================================================================+\n")
			fmt.Printf("| TEST: %-56s |\n", tt.name)
			fmt.Printf("+================================================================+\n")
			fmt.Printf("\n[TEST DETAILS]\n")
			fmt.Printf("Purpose: Test multi-line editing and block replacement\n")
			fmt.Printf("Prompt: %s\n", tt.prompt)
			fmt.Printf("Initial files: %v\n", mapKeys(tt.files))
			fmt.Printf("Expected outcome:\n")
			fmt.Printf("  Should contain:\n")
			for path, strings := range tt.wantContains {
				fmt.Printf("    - %s: %v\n", path, strings)
			}
			fmt.Printf("  Should NOT contain:\n")
			for path, strings := range tt.wantNotContains {
				fmt.Printf("    - %s: %v\n", path, strings)
			}

			repo := CreateTempRepo(t, tt.files)
			if err := RunNinaLoopWithFiles(t, repo, tt.prompt, tt.files, ""); err != nil {
				t.Fatalf("nina run: %v", err)
			}

			fmt.Printf("\n[VERIFICATION] Checking test results...\n")
			// Check file contains expected strings
			for path, wantStrings := range tt.wantContains {
				fullPath := filepath.Join(repo, path)
				got, err := os.ReadFile(fullPath)
				if err != nil {
					fmt.Printf("FAIL Failed to read %s: %v\n", path, err)
					t.Fatalf("read %s: %v", path, err)
				}
				content := string(got)
				fmt.Printf("\nVerifying %s CONTAINS %d required strings:\n", path, len(wantStrings))
				allFound := true
				for i, want := range wantStrings {
					if strings.Contains(content, want) {
						fmt.Printf("  [%d] OK Found: %q\n", i+1, want)
					} else {
						fmt.Printf("  [%d] FAIL Missing: %q\n", i+1, want)
						allFound = false
					}
				}

				if !allFound {
					fmt.Printf("\n[ACTUAL CONTENT] %s:\n", path)
					fmt.Printf("---\n%s\n---\n", content)
					t.Fatalf("file %s missing required strings", path)
				}
			}
			// Check file doesn't contain removed strings
			for path, notWantStrings := range tt.wantNotContains {
				fullPath := filepath.Join(repo, path)
				got, err := os.ReadFile(fullPath)
				if err != nil {
					t.Fatalf("read %s: %v", path, err)
				}
				content := string(got)
				fmt.Printf("\nVerifying %s does NOT contain %d strings:\n", path, len(notWantStrings))
				anyFound := false
				for i, notWant := range notWantStrings {
					if !strings.Contains(content, notWant) {
						fmt.Printf("  [%d] OK Correctly removed: %q\n", i+1, notWant)
					} else {
						fmt.Printf("  [%d] FAIL Still present: %q\n", i+1, notWant)
						anyFound = true
					}
				}

				if anyFound {
					fmt.Printf("\n[ACTUAL CONTENT] %s:\n", path)
					fmt.Printf("---\n%s\n---\n", content)
					t.Fatalf("file %s still contains strings that should be removed", path)
				}
			}

			fmt.Printf("\n[TEST PASSED] All verifications successful\n")
			fmt.Printf("=================================================================\n")
		})
	}
}

func TestLoopDeleteFile(t *testing.T) {
	t.Parallel()
	if os.Getenv("FAST") != "" {
		t.Skip("skipping integration test in FAST mode")
	}
	tests := []struct {
		name        string
		files       map[string]string
		prompt      string
		wantDeleted []string // files that should not exist after
		wantExists  []string // files that should still exist
	}{
		{
			name: "delete single file",
			files: map[string]string{
				"temp.txt":   "temporary file",
				"keeper.txt": "keep this file",
			},
			prompt:      "delete temp.txt",
			wantDeleted: []string{"temp.txt"},
			wantExists:  []string{"keeper.txt"},
		},
		{
			name: "delete backup files",
			files: map[string]string{
				"main.go":     "package main",
				"main.go.bak": "old backup",
				"data.json":   `{"value": 1}`,
			},
			prompt:      "remove main.go.bak",
			wantDeleted: []string{"main.go.bak"},
			wantExists:  []string{"main.go", "data.json"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fmt.Printf("\n+================================================================+\n")
			fmt.Printf("| TEST: %-56s |\n", tt.name)
			fmt.Printf("+================================================================+\n")
			fmt.Printf("\n[TEST DETAILS]\n")
			fmt.Printf("Purpose: Test file deletion capability\n")
			fmt.Printf("Prompt: %s\n", tt.prompt)
			fmt.Printf("Initial files: %v\n", mapKeys(tt.files))
			fmt.Printf("Expected outcome:\n")
			fmt.Printf("  Files to be deleted: %v\n", tt.wantDeleted)
			fmt.Printf("  Files to remain: %v\n", tt.wantExists)

			repo := CreateTempRepo(t, tt.files)

			// Show initial file state
			fmt.Printf("\n[INITIAL STATE]\n")
			for _, path := range append(tt.wantDeleted, tt.wantExists...) {
				fullPath := filepath.Join(repo, path)
				if info, err := os.Stat(fullPath); err == nil {
					fmt.Printf("  %s: exists (%d bytes)\n", path, info.Size())
				}
			}

			if err := RunNinaLoopWithFiles(t, repo, tt.prompt, tt.files, ""); err != nil {
				t.Fatalf("nina run: %v", err)
			}

			fmt.Printf("\n[VERIFICATION] Checking test results...\n")
			// Check files were deleted
			fmt.Printf("\nVerifying deleted files:\n")
			for _, path := range tt.wantDeleted {
				fullPath := filepath.Join(repo, path)
				if _, err := os.Stat(fullPath); !os.IsNotExist(err) {
					if err == nil {
						fmt.Printf("  FAIL %s: STILL EXISTS (should be deleted)\n", path)
						// Show content if it still exists
						if content, readErr := os.ReadFile(fullPath); readErr == nil {
							fmt.Printf("    Content: %q\n", string(content))
						}
					} else {
						fmt.Printf("  FAIL %s: Error checking: %v\n", path, err)
					}
					t.Fatalf("file %s should be deleted but still exists", path)
				} else {
					fmt.Printf("  OK %s: Successfully deleted\n", path)
				}
			}

			// Check files still exist
			fmt.Printf("\nVerifying preserved files:\n")
			for _, path := range tt.wantExists {
				fullPath := filepath.Join(repo, path)
				if info, err := os.Stat(fullPath); err != nil {
					fmt.Printf("  FAIL %s: NOT FOUND (should exist)\n", path)
					t.Fatalf("file %s should exist: %v", path, err)
				} else {
					fmt.Printf("  OK %s: Still exists (%d bytes)\n", path, info.Size())
				}
			}

			fmt.Printf("\n[TEST PASSED] All verifications successful\n")
			fmt.Printf("=================================================================\n")
		})
	}
}

func TestLoopPrependFile(t *testing.T) {
	t.Parallel()
	if os.Getenv("FAST") != "" {
		t.Skip("skipping integration test in FAST mode")
	}
	tests := []struct {
		name         string
		files        map[string]string
		prompt       string
		wantContains map[string][]string
	}{
		{
			name: "prepend copyright to source file",
			files: map[string]string{
				"main.go": `package main

// func main() {
	println("Hello")
}`,
			},
			prompt: "add '// Copyright 2025' at the very beginning of main.go",
			wantContains: map[string][]string{
				"main.go": {"// Copyright 2025", "package main", "func main()"},
			},
		},
		{
			name: "prepend header to markdown",
			files: map[string]string{
				"README.md": `## About

// This is a project.`,
			},
			prompt: "add '# Project Title' at the start of README.md",
			wantContains: map[string][]string{
				"README.md": {"# Project Title", "## About", "This is a project."},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fmt.Printf("\n+================================================================+\n")
			fmt.Printf("| TEST: %-56s |\n", tt.name)
			fmt.Printf("+================================================================+\n")
			fmt.Printf("\n[TEST DETAILS]\n")
			fmt.Printf("Purpose: Test prepending content to beginning of files\n")
			fmt.Printf("Prompt: %s\n", tt.prompt)
			fmt.Printf("Initial files: %v\n", mapKeys(tt.files))
			fmt.Printf("Expected outcome:\n")
			for path, strings := range tt.wantContains {
				fmt.Printf("  - %s should contain (in order): %v\n", path, strings)
			}

			repo := CreateTempRepo(t, tt.files)

			// Show original content
			fmt.Printf("\n[ORIGINAL CONTENT]\n")
			for path, content := range tt.files {
				fmt.Printf("File %s:\n", path)
				fmt.Printf("---\n%s\n---\n", content)
			}

			if err := RunNinaLoopWithFiles(t, repo, tt.prompt, tt.files, ""); err != nil {
				t.Fatalf("nina run: %v", err)
			}

			fmt.Printf("\n[VERIFICATION] Checking test results...\n")
			// Check file contains expected strings in order
			for path, wantStrings := range tt.wantContains {
				fullPath := filepath.Join(repo, path)
				got, err := os.ReadFile(fullPath)
				if err != nil {
					fmt.Printf("FAIL Failed to read %s: %v\n", path, err)
					t.Fatalf("read %s: %v", path, err)
				}
				content := string(got)

				fmt.Printf("\nVerifying %s has %d strings in correct order:\n", path, len(wantStrings))
				fmt.Printf("[MODIFIED CONTENT]\n")
				fmt.Printf("---\n%s\n---\n", content)

				lastIndex := -1
				allInOrder := true
				for i, want := range wantStrings {
					index := strings.Index(content, want)
					if index == -1 {
						fmt.Printf("  [%d] FAIL Missing: %q\n", i+1, want)
						allInOrder = false
						break
					}
					if index <= lastIndex {
						fmt.Printf("  [%d] FAIL Wrong order: %q (found at %d, previous at %d)\n", i+1, want, index, lastIndex)
						allInOrder = false
						break
					}
					fmt.Printf("  [%d] OK Found at position %d: %q\n", i+1, index, want)
					lastIndex = index
				}

				if !allInOrder {
					t.Fatalf("file %s content not in expected order", path)
				}
			}

			fmt.Printf("\n[TEST PASSED] All verifications successful\n")
			fmt.Printf("=================================================================\n")
		})
	}
}

func TestLoopInsertMiddle(t *testing.T) {
	t.Parallel()
	if os.Getenv("FAST") != "" {
		t.Skip("skipping integration test in FAST mode")
	}
	tests := []struct {
		name         string
		files        map[string]string
		prompt       string
		wantContains map[string][]string
	}{
		{
			name: "insert comment between functions",
			files: map[string]string{
				"utils.go": `package main

// func foo() {
	println("foo")
}

// func bar() {
	println("bar")
}`,
			},
			prompt: "add '// Helper functions below' between foo and bar functions in utils.go",
			wantContains: map[string][]string{
				"utils.go": {"func foo()", "// Helper functions below", "func bar()"},
			},
		},
		{
			name: "insert section in config",
			files: map[string]string{
				"config.yaml": `server:
//   port: 8080

// database:
//   host: localhost`,
			},
			prompt: "insert 'cache:\n  enabled: true' between server and database sections in config.yaml",
			wantContains: map[string][]string{
				"config.yaml": {"server:", "port: 8080", "cache:", "enabled: true", "database:", "host: localhost"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fmt.Printf("\n+================================================================+\n")
			fmt.Printf("| TEST: %-56s |\n", tt.name)
			fmt.Printf("+================================================================+\n")
			fmt.Printf("\n[TEST DETAILS]\n")
			fmt.Printf("Purpose: Test inserting content in the middle of files\n")
			fmt.Printf("Prompt: %s\n", tt.prompt)
			fmt.Printf("Initial files: %v\n", mapKeys(tt.files))
			fmt.Printf("Expected outcome:\n")
			for path, strings := range tt.wantContains {
				fmt.Printf("  - %s should contain (in order): %v\n", path, strings)
			}

			repo := CreateTempRepo(t, tt.files)

			// Show original content with line numbers
			fmt.Printf("\n[ORIGINAL CONTENT]\n")
			for path, content := range tt.files {
				fmt.Printf("File %s:\n", path)
				lines := strings.Split(content, "\n")
				for i, line := range lines {
					fmt.Printf("%3d: %s\n", i+1, line)
				}
			}

			if err := RunNinaLoopWithFiles(t, repo, tt.prompt, tt.files, ""); err != nil {
				t.Fatalf("nina run: %v", err)
			}

			fmt.Printf("\n[VERIFICATION] Checking test results...\n")
			// Check file contains expected strings in order
			for path, wantStrings := range tt.wantContains {
				fullPath := filepath.Join(repo, path)
				got, err := os.ReadFile(fullPath)
				if err != nil {
					fmt.Printf("FAIL Failed to read %s: %v\n", path, err)
					t.Fatalf("read %s: %v", path, err)
				}
				content := string(got)

				fmt.Printf("\nVerifying %s has %d strings in correct order:\n", path, len(wantStrings))
				fmt.Printf("[MODIFIED CONTENT]\n")
				lines := strings.Split(content, "\n")
				for i, line := range lines {
					fmt.Printf("%3d: %s\n", i+1, line)
				}

				lastIndex := -1
				allInOrder := true
				for i, want := range wantStrings {
					index := strings.Index(content, want)
					if index == -1 {
						fmt.Printf("  [%d] FAIL Missing: %q\n", i+1, want)
						allInOrder = false
						break
					}
					if index <= lastIndex {
						fmt.Printf("  [%d] FAIL Wrong order: %q (found at %d, previous at %d)\n", i+1, want, index, lastIndex)
						allInOrder = false
						break
					}
					// Find line number for better context
					lineNum := 1 + strings.Count(content[:index], "\n")
					fmt.Printf("  [%d] OK Found at line %d, pos %d: %q\n", i+1, lineNum, index, want)
					lastIndex = index
				}

				if !allInOrder {
					t.Fatalf("file %s content not in expected order", path)
				}
			}

			fmt.Printf("\n[TEST PASSED] All verifications successful\n")
			fmt.Printf("=================================================================\n")
		})
	}
}

func TestLoopMultipleOccurrences(t *testing.T) {
	t.Parallel()
	if os.Getenv("FAST") != "" {
		t.Skip("skipping integration test in FAST mode")
	}
	tests := []struct {
		name            string
		files           map[string]string
		prompt          string
		wantContains    map[string][]string
		wantNotContains map[string][]string
	}{
		{
			name: "replace all print statements",
			files: map[string]string{
				"debug.go": `package main

// func debug1() {
	print("debug 1")
	print("more info")
}

// func debug2() {
	print("debug 2")
}`,
			},
			prompt: "replace all print( with log.Print( in debug.go",
			wantContains: map[string][]string{
				"debug.go": {"log.Print(\"debug 1\")", "log.Print(\"more info\")", "log.Print(\"debug 2\")"},
			},
			wantNotContains: map[string][]string{
				"debug.go": {"print(\"debug"},
			},
		},
		{
			name: "update all TODO comments",
			files: map[string]string{
				"tasks.go": `package main

// // TODO: implement this
// func feature1() {}

// // TODO: implement this
// func feature2() {}

// // TODO: implement this
// func feature3() {}`,
			},
			prompt: "change all 'TODO: implement this' to 'DONE: implemented' in tasks.go",
			wantContains: map[string][]string{
				"tasks.go": {"// DONE: implemented", "func feature1", "func feature2", "func feature3"},
			},
			wantNotContains: map[string][]string{
				"tasks.go": {"TODO: implement this"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fmt.Printf("\n+================================================================+\n")
			fmt.Printf("| TEST: %-56s |\n", tt.name)
			fmt.Printf("+================================================================+\n")
			fmt.Printf("\n[TEST DETAILS]\n")
			fmt.Printf("Purpose: Test replacing multiple occurrences across file\n")
			fmt.Printf("Prompt: %s\n", tt.prompt)
			fmt.Printf("Initial files: %v\n", mapKeys(tt.files))
			fmt.Printf("Expected outcome:\n")
			fmt.Printf("  Should contain:\n")
			for path, strings := range tt.wantContains {
				fmt.Printf("    - %s: %v\n", path, strings)
			}
			fmt.Printf("  Should NOT contain:\n")
			for path, strings := range tt.wantNotContains {
				fmt.Printf("    - %s: %v\n", path, strings)
			}

			repo := CreateTempRepo(t, tt.files)
			if err := RunNinaLoopWithFiles(t, repo, tt.prompt, tt.files, ""); err != nil {
				t.Fatalf("nina run: %v", err)
			}

			fmt.Printf("\n[VERIFICATION] Checking test results...\n")
			// Check file contains expected strings
			for path, wantStrings := range tt.wantContains {
				fullPath := filepath.Join(repo, path)
				got, err := os.ReadFile(fullPath)
				if err != nil {
					fmt.Printf("FAIL Failed to read %s: %v\n", path, err)
					t.Fatalf("read %s: %v", path, err)
				}
				content := string(got)
				fmt.Printf("\nVerifying %s CONTAINS %d required strings:\n", path, len(wantStrings))
				allFound := true
				for i, want := range wantStrings {
					if strings.Contains(content, want) {
						fmt.Printf("  [%d] OK Found: %q\n", i+1, want)
					} else {
						fmt.Printf("  [%d] FAIL Missing: %q\n", i+1, want)
						allFound = false
					}
				}

				if !allFound {
					fmt.Printf("\n[ACTUAL CONTENT] %s:\n", path)
					fmt.Printf("---\n%s\n---\n", content)
					t.Fatalf("file %s missing required strings", path)
				}
			}
			// Check file doesn't contain removed strings
			for path, notWantStrings := range tt.wantNotContains {
				fullPath := filepath.Join(repo, path)
				got, err := os.ReadFile(fullPath)
				if err != nil {
					t.Fatalf("read %s: %v", path, err)
				}
				content := string(got)
				fmt.Printf("\nVerifying %s does NOT contain %d strings:\n", path, len(notWantStrings))
				anyFound := false
				for i, notWant := range notWantStrings {
					if !strings.Contains(content, notWant) {
						fmt.Printf("  [%d] OK Correctly removed: %q\n", i+1, notWant)
					} else {
						fmt.Printf("  [%d] FAIL Still present: %q\n", i+1, notWant)
						anyFound = true
					}
				}

				if anyFound {
					fmt.Printf("\n[ACTUAL CONTENT] %s:\n", path)
					fmt.Printf("---\n%s\n---\n", content)
					t.Fatalf("file %s still contains strings that should be removed", path)
				}
			}

			fmt.Printf("\n[TEST PASSED] All verifications successful\n")
			fmt.Printf("=================================================================\n")
		})
	}
}

func TestLoopErrorHandling(t *testing.T) {
	t.Parallel()
	if os.Getenv("FAST") != "" {
		t.Skip("skipping integration test in FAST mode")
	}
	tests := []struct {
		name         string
		files        map[string]string
		prompt       string
		wantErr      bool
		checkContent map[string]string // files that should remain unchanged
	}{
		{
			name: "edit non-existent file",
			files: map[string]string{
				"exists.txt": "original content",
			},
			prompt:  "change 'foo' to 'bar' in missing.txt",
			wantErr: false, // o4-mini might handle gracefully
			checkContent: map[string]string{
				"exists.txt": "original content", // should remain unchanged
			},
		},
		{
			name: "delete non-existent file",
			files: map[string]string{
				"real.txt": "keep this",
			},
			prompt:  "delete phantom.txt",
			wantErr: false, // o4-mini might handle gracefully
			checkContent: map[string]string{
				"real.txt": "keep this", // should remain unchanged
			},
		},
		{
			name:    "edit in empty directory",
			files:   map[string]string{},
			prompt:  "add 'hello' to new.txt",
			wantErr: false, // should create the file
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fmt.Printf("\n+================================================================+\n")
			fmt.Printf("| TEST: %-56s |\n", tt.name)
			fmt.Printf("+================================================================+\n")
			fmt.Printf("\n[TEST DETAILS]\n")
			fmt.Printf("Purpose: Test error handling and graceful failure scenarios\n")
			fmt.Printf("Prompt: %s\n", tt.prompt)
			fmt.Printf("Initial files: %v\n", mapKeys(tt.files))
			fmt.Printf("Expected outcome:\n")
			if tt.wantErr {
				fmt.Printf("  - Should produce an error\n")
			} else {
				fmt.Printf("  - Should handle gracefully (no error)\n")
			}
			for path, content := range tt.checkContent {
				fmt.Printf("  - %s should remain unchanged: %q\n", path, content)
			}

			repo := CreateTempRepo(t, tt.files)
			err := RunNinaLoopWithFiles(t, repo, tt.prompt, tt.files, "")

			fmt.Printf("\n[VERIFICATION] Checking test results...\n")

			// Check error expectations
			if tt.wantErr && err == nil {
				fmt.Printf("FAIL Expected error but got none\n")
				t.Fatalf("expected error but got none")
			}
			if !tt.wantErr && err != nil && err != context.DeadlineExceeded {
				fmt.Printf("FAIL Unexpected error: %v\n", err)
				t.Fatalf("unexpected error: %v", err)
			}
			if err == nil {
				fmt.Printf("OK No error (as expected)\n")
			} else if err == context.DeadlineExceeded {
				fmt.Printf("OK Timeout occurred (acceptable)\n")
			} else if tt.wantErr {
				fmt.Printf("OK Got expected error: %v\n", err)
			}

			// Check that certain files remain unchanged
			for path, wantContent := range tt.checkContent {
				fullPath := filepath.Join(repo, path)
				got, err := os.ReadFile(fullPath)
				if err != nil {
					fmt.Printf("FAIL Failed to read %s: %v\n", path, err)
					t.Fatalf("read %s: %v", path, err)
				}
				fmt.Printf("Verifying %s remained unchanged:\n", path)
				fmt.Printf("  Want: %q\n", wantContent)
				fmt.Printf("  Got:  %q\n", string(got))
				if string(got) != wantContent {
					fmt.Printf("  FAIL MISMATCH - file was modified\n")
					t.Fatalf("file %s changed unexpectedly, got=%q want=%q", path, got, wantContent)
				}
				fmt.Printf("  OK OK - unchanged as expected\n")
			}

			fmt.Printf("\n[TEST PASSED] All verifications successful\n")
			fmt.Printf("=================================================================\n")
		})
	}
}

func TestLoopMultipleFiles(t *testing.T) {
	t.Parallel()
	if os.Getenv("FAST") != "" {
		t.Skip("skipping integration test in FAST mode")
	}
	tests := []struct {
		name         string
		files        map[string]string
		prompt       string
		wantContains map[string][]string
		wantExists   []string
	}{
		{
			name: "update multiple config files",
			files: map[string]string{
				"config.json":  `{"version": "1.0", "debug": false}`,
				"package.json": `{"name": "myapp", "version": "0.1.0"}`,
			},
			prompt: "update version to 2.0 in config.json and version to 1.0.0 in package.json",
			wantContains: map[string][]string{
				"config.json":  {`"version": "2.0"`},
				"package.json": {`"version": "1.0.0"`},
			},
			wantExists: []string{},
		},
		{
			name: "refactor go files",
			files: map[string]string{
				"main.go": `package main

// func main() {
	println("Hello")
}`,
				"utils.go": `package main

// func greet(name string) {
	println("Hi " + name)
}`,
			},
			prompt: "change println to fmt.Println in both main.go and utils.go, add import fmt where needed",
			wantContains: map[string][]string{
				"main.go":  {"import", "fmt", "fmt.Println"},
				"utils.go": {"import", "fmt", "fmt.Println"},
			},
			wantExists: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fmt.Printf("\n+================================================================+\n")
			fmt.Printf("| TEST: %-56s |\n", tt.name)
			fmt.Printf("+================================================================+\n")
			fmt.Printf("\n[TEST DETAILS]\n")
			fmt.Printf("Purpose: Test editing multiple files in a single operation\n")
			fmt.Printf("Prompt: %s\n", tt.prompt)
			fmt.Printf("Initial files: %v\n", mapKeys(tt.files))
			fmt.Printf("Expected outcome:\n")
			for path, strings := range tt.wantContains {
				fmt.Printf("  - %s should contain: %v\n", path, strings)
			}
			for _, path := range tt.wantExists {
				fmt.Printf("  - %s should exist\n", path)
			}

			repo := CreateTempRepo(t, tt.files)
			if err := RunNinaLoopWithFiles(t, repo, tt.prompt, tt.files, ""); err != nil {
				t.Fatalf("nina run: %v", err)
			}

			fmt.Printf("\n[VERIFICATION] Checking test results...\n")
			// Check file contains expected strings
			for path, wantStrings := range tt.wantContains {
				fullPath := filepath.Join(repo, path)
				got, err := os.ReadFile(fullPath)
				if err != nil {
					fmt.Printf("FAIL Failed to read %s: %v\n", path, err)
					t.Fatalf("read %s: %v", path, err)
				}
				content := string(got)
				fmt.Printf("\nVerifying %s contains %d required strings:\n", path, len(wantStrings))
				allFound := true
				for i, want := range wantStrings {
					if strings.Contains(content, want) {
						fmt.Printf("  [%d] OK Found: %q\n", i+1, want)
					} else {
						fmt.Printf("  [%d] FAIL Missing: %q\n", i+1, want)
						allFound = false
					}
				}

				if !allFound {
					fmt.Printf("\n[ACTUAL CONTENT] %s:\n", path)
					fmt.Printf("---\n%s\n---\n", content)
					t.Fatalf("file %s missing required strings", path)
				}
			}
			// Check files exist
			for _, path := range tt.wantExists {
				fullPath := filepath.Join(repo, path)
				fmt.Printf("Checking existence of %s: ", path)
				if info, err := os.Stat(fullPath); err != nil {
					fmt.Printf("FAIL NOT FOUND\n")
					t.Fatalf("file %s should exist: %v", path, err)
				} else {
					fmt.Printf("OK EXISTS (%d bytes)\n", info.Size())
				}
			}

			fmt.Printf("\n[TEST PASSED] All verifications successful\n")
			fmt.Printf("=================================================================\n")
		})
	}
}
