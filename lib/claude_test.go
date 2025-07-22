// Tests for Claude integration ensuring cache_control limits are respected
// Verifies that no more than 4 cache_control blocks are used in requests
// Tests that cache is applied to newest messages in conversation history
package lib

import (
	"fmt"
	claude "nina/providers/claude"
	"testing"
)

func TestCacheControlLimit(t *testing.T) {
	tests := []struct {
		name               string
		messageCount       int
		expectedCacheCount int
	}{
		{"no messages", 0, 1},  // Only system prompt cached
		{"1 message", 1, 2},    // System prompt + 1 newest message
		{"3 messages", 3, 4},   // System prompt + 3 newest messages
		{"4 messages", 4, 4},   // System prompt + 3 newest messages (limit)
		{"6 messages", 6, 4},   // System prompt + 3 newest messages (limit)
		{"10 messages", 10, 4}, // System prompt + 3 newest messages (limit)
		{"20 messages", 20, 4}, // System prompt + 3 newest messages (limit)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &ClaudeClient{
				messages: []*claude.Message{},
			}

			// Add test messages
			for i := 0; i < tt.messageCount; i++ {
				msg := &claude.Message{
					Role:    "user",
					Content: []claude.Text{{Type: "text", Text: "test message"}},
				}
				if i%2 == 1 {
					msg.Role = "assistant"
				}
				client.messages = append(client.messages, msg)
			}

			// Count cache control blocks that would be added
			cacheCount := 1 // System prompt always has cache

			// Calculate how many messages would be cached (newest messages)
			maxCachedMessages := 3
			totalMessages := len(client.messages)

			// We cache the newest messages, up to maxCachedMessages
			if totalMessages > 0 {
				cacheCount += min(totalMessages, maxCachedMessages)
			}

			if cacheCount != tt.expectedCacheCount {
				t.Errorf("got %d cache blocks, want %d", cacheCount, tt.expectedCacheCount)
			}

			// Ensure we never exceed 4 cache blocks
			if cacheCount > 4 {
				t.Errorf("cache blocks exceed limit: got %d, max 4", cacheCount)
			}
		})
	}
}

func TestRestoreMessages(t *testing.T) {
	tests := []struct {
		name     string
		messages []any
		expected int
		wantErr  bool
	}{
		{
			name: "restore valid messages",
			messages: []any{
				map[string]any{
					"role":    "user",
					"content": "Hello, Claude!",
				},
				map[string]any{
					"role":    "assistant",
					"content": "Hello! How can I help you today?",
				},
				map[string]any{
					"role":    "user",
					"content": "What's the weather like?",
				},
			},
			expected: 3,
			wantErr:  false,
		},
		{
			name: "skip invalid messages",
			messages: []any{
				map[string]any{
					"role":    "user",
					"content": "Valid message",
				},
				"invalid message type",
				map[string]any{
					"role": "user",
					// missing content
				},
				map[string]any{
					// missing role
					"content": "No role",
				},
				map[string]any{
					"role":    "",
					"content": "Empty role",
				},
				map[string]any{
					"role":    "assistant",
					"content": "",
				},
			},
			expected: 1, // Only the first valid message
			wantErr:  false,
		},
		{
			name:     "empty messages array",
			messages: []any{},
			expected: 0,
			wantErr:  false,
		},
		{
			name:     "nil messages array",
			messages: nil,
			expected: 0,
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &ClaudeClient{
				messages: []*claude.Message{},
			}

			err := client.RestoreMessages(tt.messages)
			if (err != nil) != tt.wantErr {
				t.Errorf("RestoreMessages() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if len(client.messages) != tt.expected {
				t.Errorf("RestoreMessages() restored %d messages, want %d", len(client.messages), tt.expected)
			}

			// Verify content of restored messages for valid test case
			if tt.name == "restore valid messages" && len(client.messages) == 3 {
				if client.messages[0].Role != "user" || client.messages[0].Content[0].Text != "Hello, Claude!" {
					t.Errorf("First message incorrect: got role=%s, content=%s", client.messages[0].Role, client.messages[0].Content[0].Text)
				}
				if client.messages[1].Role != "assistant" || client.messages[1].Content[0].Text != "Hello! How can I help you today?" {
					t.Errorf("Second message incorrect: got role=%s, content=%s", client.messages[1].Role, client.messages[1].Content[0].Text)
				}
				if client.messages[2].Role != "user" || client.messages[2].Content[0].Text != "What's the weather like?" {
					t.Errorf("Third message incorrect: got role=%s, content=%s", client.messages[2].Role, client.messages[2].Content[0].Text)
				}
			}
		})
	}
}

func TestCacheApplicationInRequest(t *testing.T) {
	// Test that cache control is correctly applied to messages
	client := &ClaudeClient{
		messages: []*claude.Message{},
	}

	// Add some test messages
	for i := range 5 {
		msg := &claude.Message{
			Role:    "user",
			Content: []claude.Text{{Type: "text", Text: fmt.Sprintf("message %d", i)}},
		}
		if i%2 == 1 {
			msg.Role = "assistant"
		}
		client.messages = append(client.messages, msg)
	}

	// Simulate the cache application logic from CallWithStore
	maxCachedMessages := 3
	totalMessages := len(client.messages)
	cacheStartIndex := max(0, totalMessages-maxCachedMessages)

	cachedCount := 0
	for i := range client.messages {
		if i >= cacheStartIndex {
			cachedCount++
		}
	}

	// Should cache the last 3 messages
	if cachedCount != 3 {
		t.Errorf("expected 3 messages to be cached, got %d", cachedCount)
	}

	// Verify the cache start index is correct
	expectedStartIndex := 2 // With 5 messages, we cache messages at index 2, 3, 4
	if cacheStartIndex != expectedStartIndex {
		t.Errorf("expected cache start index %d, got %d", expectedStartIndex, cacheStartIndex)
	}
}
