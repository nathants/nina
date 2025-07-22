package util

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestParseFileUpdatesMissingFilenameSearchReplace(t *testing.T) {
	bad := strings.Join([]string{
		NinaOutputStart, NinaStart,
		NinaSearchStart, "foo\nbar", NinaSearchEnd,
		NinaReplaceStart, "qux", NinaReplaceEnd,
		NinaEnd, NinaOutputEnd,
	}, "")
	_, err := ParseFileUpdates(bad)
	if err == nil {
		t.Fatalf("expected error for missing filename")
	}
	if !strings.Contains(err.Error(), "missing NinaPath in NinaChange") {
		t.Fatalf("unexpected error message: %v", err)
	}

	good := strings.Join([]string{
		NinaOutputStart, NinaStart,
		NinaPathStart, "~/repos/nina/file.txt", NinaPathEnd,
		NinaSearchStart, "old line 1\nold line 2", NinaSearchEnd,
		NinaReplaceStart, "new line 1\nnew line 2", NinaReplaceEnd,
		NinaEnd, NinaOutputEnd,
	}, "")
	updates, err := ParseFileUpdates(good)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(updates))
	}
	expectedSearch := []string{"old line 1", "old line 2"}
	expectedReplace := []string{"new line 1", "new line 2"}
	if !reflect.DeepEqual(updates[0].SearchLines, expectedSearch) {
		t.Fatalf("search lines mismatch.\nexpected: %#v\ngot: %#v", expectedSearch, updates[0].SearchLines)
	}

	if !reflect.DeepEqual(updates[0].ReplaceLines, expectedReplace) {
		t.Fatalf("replace lines mismatch.\nexpected: %#v\ngot: %#v", expectedReplace, updates[0].ReplaceLines)
	}
}

func TestParseMultipleBlocks(t *testing.T) {
	input := fmt.Sprintf(`
this is prose and should be ignored
%s
%s
%s~/repos/nina/target.txt%s
%ssearch A%s
%sreplace B%s
%s
%s
%s~/repos/nina/target.txt%s
%ssearch C%s
%sreplace D%s
%s
%s
more prose to be ignored
`,
		NinaOutputStart,
		NinaStart,
		NinaPathStart, NinaPathEnd,
		NinaSearchStart, NinaSearchEnd,
		NinaReplaceStart, NinaReplaceEnd,
		NinaEnd,
		NinaStart,
		NinaPathStart, NinaPathEnd,
		NinaSearchStart, NinaSearchEnd,
		NinaReplaceStart, NinaReplaceEnd,
		NinaEnd,
		NinaOutputEnd,
	)

	updates, err := ParseFileUpdates(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(updates) != 2 {
		t.Fatalf("expected 2 updates, got %d", len(updates))
	}

	want := []FileUpdate{
		{
			FileName:     "~/repos/nina/target.txt",
			SearchLines:  []string{"search A"},
			ReplaceLines: []string{"replace B"},
		},
		{
			FileName:     "~/repos/nina/target.txt",
			SearchLines:  []string{"search C"},
			ReplaceLines: []string{"replace D"},
		},
	}

	for i, update := range updates {
		if !reflect.DeepEqual(update, want[i]) {
			t.Errorf("update %d: got %+v, want %+v", i, update, want[i])
		}
	}
}

func TestParseFileUpdatesUnknownBlockType(t *testing.T) {
	input := strings.Join([]string{
		NinaOutputStart,
		NinaPatchStart,
		NinaPathStart, "~/repos/nina/file.txt", NinaPathEnd,
		NinaPatchEnd,
		NinaOutputEnd,
	}, "")
	updates, err := ParseFileUpdates(input)
	if err != nil {
		t.Fatalf("unexpected error for unknown block type: %v", err)
	}
	if len(updates) != 0 {
		t.Fatalf("expected 0 updates for unknown block type, got %d", len(updates))
	}
}

func TestSharedParentDir(t *testing.T) {
	tests := []struct {
		name     string
		paths    map[string]string
		expected string
	}{
		{
			name: "single file uses first parent",
			paths: map[string]string{
				"/a/b/c": "",
			},
			expected: "/a/b",
		},
		{
			name: "multiple with common prefix",
			paths: map[string]string{
				"/a/b/c": "",
				"/a/b/d": "",
			},
			expected: "/a/b",
		},
		{
			name:     "no paths returns empty",
			paths:    map[string]string{},
			expected: "",
		},
		{
			name: "diverging after root",
			paths: map[string]string{
				"/folder/sub1/file.txt": "",
				"/folder/sub2/file.txt": "",
			},
			expected: "/folder",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SharedParentDir(tc.paths)
			if got != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, got)
			}
		})
	}
}

func TestAddLineNumbers(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: "1: ",
		},
		{
			name:     "single line",
			input:    "hello",
			expected: "1: hello",
		},
		{
			name:     "multiple lines",
			input:    "line one\nline two\nline three",
			expected: "1: line one\n2: line two\n3: line three",
		},
		{
			name:     "blank lines",
			input:    "first\n\nthird",
			expected: "1: first\n2: \n3: third",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := AddLineNumbers(tc.input)
			if got != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, got)
			}
		})
	}
}

func TestStripLineNumbers(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple line numbers",
			input:    "1: hello\n2: world",
			expected: "hello\nworld",
		},
		{
			name:     "multi-digit line numbers",
			input:    "99: line ninety-nine\n100: line one hundred",
			expected: "line ninety-nine\nline one hundred",
		},
		{
			name:     "no line numbers",
			input:    "hello\nworld",
			expected: "hello\nworld",
		},
		{
			name:     "mixed with and without",
			input:    "1: first\nno number\n3: third",
			expected: "first\nno number\nthird",
		},
		{
			name:     "line with colon but not number",
			input:    "key: value\n2: second",
			expected: "key: value\nsecond",
		},
		{
			name:     "empty lines with numbers",
			input:    "1: \n2: hello\n3: ",
			expected: "\nhello\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := StripLineNumbers(tc.input)
			if got != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, got)
			}
		})
	}
}

func TestIsNumeric(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "single digit",
			input:    "1",
			expected: true,
		},
		{
			name:     "multiple digits",
			input:    "123",
			expected: true,
		},
		{
			name:     "zero",
			input:    "0",
			expected: true,
		},
		{
			name:     "empty string",
			input:    "",
			expected: false,
		},
		{
			name:     "letters",
			input:    "abc",
			expected: false,
		},
		{
			name:     "mixed",
			input:    "12a",
			expected: false,
		},
		{
			name:     "spaces",
			input:    "12 3",
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IsNumeric(tc.input)
			if got != tc.expected {
				t.Errorf("expected %v, got %v", tc.expected, got)
			}
		})
	}
}

func TestApplyRangeReplace(t *testing.T) {
	tests := []struct {
		name            string
		initialContent  string
		update          FileUpdate
		expectError     bool
		expectedContent string
	}{
		{
			name:           "line range replace middle of file",
			initialContent: "line 1\nline 2\nline 3\nline 4\nline 5",
			update: FileUpdate{
				FileName:     "/test/file.txt",
				ReplaceLines: []string{"new line 2", "new line 3"},
				StartLine:    2,
				EndLine:      3,
			},
			expectError:     false,
			expectedContent: "line 1\nnew line 2\nnew line 3\nline 4\nline 5",
		},
		{
			name:           "line range replace at start",
			initialContent: "line 1\nline 2\nline 3",
			update: FileUpdate{
				FileName:     "/test/file.txt",
				ReplaceLines: []string{"new first line"},
				StartLine:    1,
				EndLine:      1,
			},
			expectError:     false,
			expectedContent: "new first line\nline 2\nline 3",
		},
		{
			name:           "line range replace at end",
			initialContent: "line 1\nline 2\nline 3",
			update: FileUpdate{
				FileName:     "/test/file.txt",
				ReplaceLines: []string{"new last line"},
				StartLine:    3,
				EndLine:      3,
			},
			expectError:     false,
			expectedContent: "line 1\nline 2\nnew last line",
		},
		{
			name:           "line range replace entire file",
			initialContent: "line 1\nline 2\nline 3",
			update: FileUpdate{
				FileName:     "/test/file.txt",
				ReplaceLines: []string{"completely", "new", "content"},
				StartLine:    1,
				EndLine:      3,
			},
			expectError:     false,
			expectedContent: "completely\nnew\ncontent",
		},
		{
			name:           "line range with empty replacement",
			initialContent: "line 1\nline 2\nline 3\nline 4",
			update: FileUpdate{
				FileName:     "/test/file.txt",
				ReplaceLines: []string{},
				StartLine:    2,
				EndLine:      2,
			},
			expectError:     false,
			expectedContent: "line 1\nline 3\nline 4",
		},
		{
			name:           "invalid start line (zero)",
			initialContent: "line 1\nline 2\nline 3",
			update: FileUpdate{
				FileName:     "/test/file.txt",
				ReplaceLines: []string{"new"},
				StartLine:    0,
				EndLine:      2,
			},
			expectError:     true,
			expectedContent: "",
		},
		{
			name:           "invalid end line (beyond file)",
			initialContent: "line 1\nline 2\nline 3",
			update: FileUpdate{
				FileName:     "/test/file.txt",
				ReplaceLines: []string{"new"},
				StartLine:    2,
				EndLine:      4,
			},
			expectError:     true,
			expectedContent: "",
		},
		{
			name:           "rewrite (empty range) creates new file",
			initialContent: "",
			update: FileUpdate{
				FileName:     "/test/newfile.txt",
				ReplaceLines: []string{"brand", "new", "file"},
				StartLine:    0,
				EndLine:      0,
			},
			expectError:     false,
			expectedContent: "brand\nnew\nfile",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sessionState := &SessionState{
				PathMap:   map[string]string{tc.update.FileName: tc.update.FileName},
				OrigFiles: map[string]string{tc.update.FileName: tc.initialContent},
			}

			actualContent, err := ApplyRangeReplace(tc.update, false, sessionState)
			if tc.expectError {
				if err == nil {
					t.Fatalf("expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if actualContent != tc.expectedContent {
				t.Fatalf("content mismatch.\nexpected:\n%q\ngot:\n%q", tc.expectedContent, actualContent)
			}
		})
	}
}

func TestApplyUpdatesWithSearch(t *testing.T) {
	tests := []struct {
		name            string
		originalContent string
		currentContent  string
		updates         []FileUpdate
		expectedContent string
		expectErrors    bool
		errorCount      int
	}{
		{
			name:            "exact match replacement",
			originalContent: "line 1\nline 2\nline 3\nline 4\nline 5",
			currentContent:  "line 1\nline 2\nline 3\nline 4\nline 5",
			updates: []FileUpdate{
				{
					FileName:     "/test/file.txt",
					ReplaceLines: []string{"new line 2", "new line 3"},
					StartLine:    2,
					EndLine:      3,
				},
			},
			expectedContent: "line 1\nnew line 2\nnew line 3\nline 4\nline 5",
			expectErrors:    false,
		},
		{
			name:            "file changed but target text unchanged",
			originalContent: "line 1\nline 2\nline 3\nline 4\nline 5",
			currentContent:  "line 0\nline 1\nline 2\nline 3\nline 4\nline 5\nline 6",
			updates: []FileUpdate{
				{
					FileName:     "/test/file.txt",
					ReplaceLines: []string{"new line 2", "new line 3"},
					StartLine:    2,
					EndLine:      3,
				},
			},
			expectedContent: "line 0\nline 1\nnew line 2\nnew line 3\nline 4\nline 5\nline 6",
			expectErrors:    false,
		},
		{
			name:            "text not found",
			originalContent: "line 1\nline 2\nline 3",
			currentContent:  "different line 1\ndifferent line 2\ndifferent line 3",
			updates: []FileUpdate{
				{
					FileName:     "/test/file.txt",
					ReplaceLines: []string{"new line"},
					StartLine:    2,
					EndLine:      2,
				},
			},
			expectedContent: "different line 1\ndifferent line 2\ndifferent line 3",
			expectErrors:    true,
			errorCount:      1,
		},
		{
			name:            "text found multiple times",
			originalContent: "line 1\nline 2\nline 3",
			currentContent:  "line 2\nline 2\nline 3",
			updates: []FileUpdate{
				{
					FileName:     "/test/file.txt",
					ReplaceLines: []string{"new line"},
					StartLine:    2,
					EndLine:      2,
				},
			},
			expectedContent: "line 2\nline 2\nline 3",
			expectErrors:    true,
			errorCount:      1,
		},
		{
			name:            "multiple updates in same file",
			originalContent: "line 1\nline 2\nline 3\nline 4\nline 5",
			currentContent:  "line 1\nline 2\nline 3\nline 4\nline 5",
			updates: []FileUpdate{
				{
					FileName:     "/test/file.txt",
					ReplaceLines: []string{"new line 4"},
					StartLine:    4,
					EndLine:      4,
				},
				{
					FileName:     "/test/file.txt",
					ReplaceLines: []string{"new line 2"},
					StartLine:    2,
					EndLine:      2,
				},
			},
			expectedContent: "line 1\nnew line 2\nline 3\nnew line 4\nline 5",
			expectErrors:    false,
		},
		{
			name:            "full file rewrite",
			originalContent: "old content",
			currentContent:  "completely different content",
			updates: []FileUpdate{
				{
					FileName:     "/test/file.txt",
					ReplaceLines: []string{"brand", "new", "content"},
					StartLine:    0,
					EndLine:      0,
				},
			},
			expectedContent: "brand\nnew\ncontent",
			expectErrors:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, errors := ApplyUpdatesWithSearch(tc.originalContent, tc.currentContent, tc.updates)

			if tc.expectErrors {
				if len(errors) != tc.errorCount {
					t.Fatalf("expected %d errors but got %d: %v", tc.errorCount, len(errors), errors)
				}
			} else {
				if len(errors) > 0 {
					t.Fatalf("unexpected errors: %v", errors)
				}
			}

			if result != tc.expectedContent {
				t.Fatalf("content mismatch.\nexpected:\n%q\ngot:\n%q", tc.expectedContent, result)
			}
		})
	}
}

func TestExtractNinaMessage(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
		hasError bool
	}{
		{
			name: "simple message",
			input: fmt.Sprintf(`%s
%s
Hello there!
%s
%s`, NinaOutputStart, NinaMessageStart, NinaMessageEnd, NinaOutputEnd),
			expected: "Hello there!",
			hasError: false,
		},
		{
			name: "message with other blocks",
			input: fmt.Sprintf(`%s
%s
I made some changes.
%s
%s
...
%s
%s`, NinaOutputStart, NinaMessageStart, NinaMessageEnd, NinaStart, NinaEnd, NinaOutputEnd),
			expected: "I made some changes.",
			hasError: false,
		},
		{
			name: "no message tag",
			input: fmt.Sprintf(`%s
%s
...
%s
%s`, NinaOutputStart, NinaStart, NinaEnd, NinaOutputEnd),
			expected: "",
			hasError: false,
		},
		{
			name: "no ninaoutput tag",
			input: fmt.Sprintf(`%s
Hello there!
%s`, NinaMessageStart, NinaMessageEnd),
			expected: "Hello there!",
			hasError: false,
		},
		{
			name: "malformed xml - missing end tag",
			input: fmt.Sprintf(`%s
%s
Hello there!
%s`, NinaOutputStart, NinaMessageStart, NinaOutputEnd),
			expected: "",
			hasError: true,
		},
		{
			name: "empty message",
			input: fmt.Sprintf(`%s
%s
%s
%s`, NinaOutputStart, NinaMessageStart, NinaMessageEnd, NinaOutputEnd),
			expected: "",
			hasError: false,
		},
		{
			name:     "empty input",
			input:    "",
			expected: "",
			hasError: false,
		},
		{
			name: "message with whitespace",
			input: fmt.Sprintf(`%s
%s
  Hello there!
%s
%s`, NinaOutputStart, NinaMessageStart, NinaMessageEnd, NinaOutputEnd),
			expected: "Hello there!",
			hasError: false,
		},
		{
			name:     "message inline with tags",
			input:    fmt.Sprintf(`%s%sHello there!%s%s`, NinaOutputStart, NinaMessageStart, NinaMessageEnd, NinaOutputEnd),
			expected: "Hello there!",
			hasError: false,
		},
		{
			name: "message with newlines",
			input: fmt.Sprintf(`%s
%s
First line
Second line
Third line
%s
%s`, NinaOutputStart, NinaMessageStart, NinaMessageEnd, NinaOutputEnd),
			expected: "First line\nSecond line\nThird line",
			hasError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExtractNinaMessage(tc.input)
			if tc.hasError {
				if err == nil {
					t.Fatal("expected an error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.expected {
				t.Errorf("got %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestRemoveFencedBlocks(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "json fence with content",
			input: "```json\n" + `{"start": 1, "stop": 2707, "filePath": "src/util.tsx"}
{"start": 1, "stop": 2358, "filePath": "src/pages/app.tsx"}
{"start": 780, "stop": 996, "filePath": "cmd/proxy/main.go"}
{"start": 1012, "stop": 1403, "filePath": "nina.go"}` + "\n```",
			expected: `{"start": 1, "stop": 2707, "filePath": "src/util.tsx"}
{"start": 1, "stop": 2358, "filePath": "src/pages/app.tsx"}
{"start": 780, "stop": 996, "filePath": "cmd/proxy/main.go"}
{"start": 1012, "stop": 1403, "filePath": "nina.go"}`,
		},
		{
			name:     "plain fence with content",
			input:    "```\n" + `{"start": 1, "stop": 100, "filePath": "test.go"}` + "\n```",
			expected: `{"start": 1, "stop": 100, "filePath": "test.go"}`,
		},
		{
			name: "jsonl fence with content",
			input: "```jsonl\n" + `{"start": 1, "stop": 50, "filePath": "file1.go"}
{"start": 51, "stop": 100, "filePath": "file2.go"}` + "\n```",
			expected: `{"start": 1, "stop": 50, "filePath": "file1.go"}
{"start": 51, "stop": 100, "filePath": "file2.go"}`,
		},
		{
			name:     "no fence markers",
			input:    `{"start": 1, "stop": 100, "filePath": "test.go"}`,
			expected: `{"start": 1, "stop": 100, "filePath": "test.go"}`,
		},
		{
			name:  "multiple fenced blocks",
			input: "```json\n" + `{"start": 1, "stop": 10}` + "\n```\n\n```json\n" + `{"start": 11, "stop": 20}` + "\n```",
			expected: `{"start": 1, "stop": 10}
{"start": 11, "stop": 20}`,
		},
		{
			name:     "empty input",
			input:    "",
			expected: "",
		},
		{
			name:     "fence with no content",
			input:    "```\n```",
			expected: "",
		},
		{
			name:     "content before and after fence",
			input:    "prefix\n```json\n" + `{"test": true}` + "\n```\nsuffix",
			expected: `{"test": true}`,
		},
		{
			name: "jsonl fence with 5 lines",
			input: "```jsonl\n" + `{"start": 1, "stop": 2707, "filePath": "src/util.tsx"}
{"start": 1, "stop": 2358, "filePath": "src/pages/app.tsx"}
{"start": 780, "stop": 996, "filePath": "cmd/proxy/main.go"}
{"start": 1012, "stop": 1403, "filePath": "nina.go"}
{"start": 1, "stop": 1396, "filePath": "static/main.css"}` + "\n```",
			expected: `{"start": 1, "stop": 2707, "filePath": "src/util.tsx"}
{"start": 1, "stop": 2358, "filePath": "src/pages/app.tsx"}
{"start": 780, "stop": 996, "filePath": "cmd/proxy/main.go"}
{"start": 1012, "stop": 1403, "filePath": "nina.go"}
{"start": 1, "stop": 1396, "filePath": "static/main.css"}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := RemoveFencedBlocks(tc.input)
			if got != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, got)
			}
		})
	}
}

func TestParseNinaBash(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []BashCommand
		hasError bool
	}{
		{
			name: "single bash command",
			input: `<NinaOutput>
<NinaBash>ls -la</NinaBash>
</NinaOutput>`,
			expected: []BashCommand{
				{Command: "ls -la", Args: []string{}},
			},
		},
		{
			name: "multiple bash commands",
			input: `<NinaOutput>
<NinaBash>pwd</NinaBash>
<NinaBash>echo "hello world"</NinaBash>
</NinaOutput>`,
			expected: []BashCommand{
				{Command: "pwd", Args: []string{}},
				{Command: `echo "hello world"`, Args: []string{}},
			},
		},
		{
			name:     "no bash commands",
			input:    `<NinaOutput>Some other content</NinaOutput>`,
			expected: []BashCommand{},
		},
		{
			name:  "bash command without NinaOutput wrapper",
			input: `<NinaBash>cd /tmp</NinaBash>`,
			expected: []BashCommand{
				{Command: "cd /tmp", Args: []string{}},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseNinaBash(tc.input)
			if tc.hasError {
				if err == nil {
					t.Fatal("expected an error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.expected) {
				t.Errorf("got %d commands, want %d", len(got), len(tc.expected))
				return
			}
			for i := range got {
				if got[i].Command != tc.expected[i].Command {
					t.Errorf("command %d: got %q, want %q", i, got[i].Command, tc.expected[i].Command)
				}
			}
		})
	}
}

func TestParseNinaStop(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
		hasError bool
	}{
		{
			name: "stop with reason",
			input: `<NinaOutput>
<NinaStop>Task completed successfully</NinaStop>
</NinaOutput>`,
			expected: "Task completed successfully",
		},
		{
			name:     "no stop tag",
			input:    `<NinaOutput>Some other content</NinaOutput>`,
			expected: "",
		},
		{
			name:     "stop without NinaOutput wrapper",
			input:    `<NinaStop>User requested stop</NinaStop>`,
			expected: "User requested stop",
		},
		{
			name: "empty stop reason",
			input: `<NinaOutput>
<NinaStop></NinaStop>
</NinaOutput>`,
			expected: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseNinaStop(tc.input)
			if tc.hasError {
				if err == nil {
					t.Fatal("expected an error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.expected {
				t.Errorf("got %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestParseNinaResult(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		expectCmd    *CommandResult
		expectChange *ChangeResult
		hasError     bool
	}{
		{
			name: "command result",
			input: `<NinaResult>
<NinaCmd>ls -la</NinaCmd>
<NinaExit>0</NinaExit>
<NinaStdout>total 24
drwxr-xr-x 2 user user 4096 Jan 1 12:00 .</NinaStdout>
<NinaStderr></NinaStderr>
</NinaResult>`,
			expectCmd: &CommandResult{
				Command:  "ls -la",
				Args:     []string{},
				ExitCode: 0,
				Stdout:   "total 24\ndrwxr-xr-x 2 user user 4096 Jan 1 12:00 .",
				Stderr:   "",
			},
		},
		{
			name: "command result with error",
			input: `<NinaResult>
<NinaCmd>cat /nonexistent</NinaCmd>
<NinaExit>1</NinaExit>
<NinaStdout></NinaStdout>
<NinaStderr>cat: /nonexistent: No such file or directory</NinaStderr>
</NinaResult>`,
			expectCmd: &CommandResult{
				Command:  "cat /nonexistent",
				Args:     []string{},
				ExitCode: 1,
				Stdout:   "",
				Stderr:   "cat: /nonexistent: No such file or directory",
			},
		},
		{
			name: "change result",
			input: `<NinaResult>
<NinaChange>src/main.go</NinaChange>
<NinaStdout>Successfully updated src/main.go</NinaStdout>
<NinaStderr></NinaStderr>
</NinaResult>`,
			expectChange: &ChangeResult{
				FilePath: "src/main.go",
				Args:     []string{},
				Stdout:   "Successfully updated src/main.go",
				Stderr:   "",
			},
		},
		{
			name:  "no result",
			input: `Some other content`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotCmd, gotChange, err := ParseNinaResult(tc.input)
			if tc.hasError {
				if err == nil {
					t.Fatal("expected an error but got none")
				}
				return
			}
			if err != nil && (tc.expectCmd != nil || tc.expectChange != nil) {
				t.Fatalf("unexpected error: %v", err)
			}

			if tc.expectCmd != nil {
				if gotCmd == nil {
					t.Fatal("expected command result but got none")
				}
				if gotCmd.Command != tc.expectCmd.Command {
					t.Errorf("command: got %q, want %q", gotCmd.Command, tc.expectCmd.Command)
				}
				if gotCmd.ExitCode != tc.expectCmd.ExitCode {
					t.Errorf("exit code: got %d, want %d", gotCmd.ExitCode, tc.expectCmd.ExitCode)
				}
				if gotCmd.Stdout != tc.expectCmd.Stdout {
					t.Errorf("stdout: got %q, want %q", gotCmd.Stdout, tc.expectCmd.Stdout)
				}
				if gotCmd.Stderr != tc.expectCmd.Stderr {
					t.Errorf("stderr: got %q, want %q", gotCmd.Stderr, tc.expectCmd.Stderr)
				}
			}

			if tc.expectChange != nil {
				if gotChange == nil {
					t.Fatal("expected change result but got none")
				}
				if gotChange.FilePath != tc.expectChange.FilePath {
					t.Errorf("file path: got %q, want %q", gotChange.FilePath, tc.expectChange.FilePath)
				}
				if gotChange.Stdout != tc.expectChange.Stdout {
					t.Errorf("stdout: got %q, want %q", gotChange.Stdout, tc.expectChange.Stdout)
				}
				if gotChange.Stderr != tc.expectChange.Stderr {
					t.Errorf("stderr: got %q, want %q", gotChange.Stderr, tc.expectChange.Stderr)
				}
			}
		})
	}
}
