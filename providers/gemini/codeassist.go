package gemini

// Code Assist API client for Gemini OAuth authentication. This client makes
// direct HTTP requests to the Code Assist API endpoint using OAuth tokens,
// similar to how gemini-cli handles OAuth authentication.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"google.golang.org/genai"
	providers "github.com/nathants/nina/providers"
)

const (
	// Try using regular Gemini API with OAuth instead of Code Assist
	codeAssistEndpoint   = "https://generativelanguage.googleapis.com"
	codeAssistAPIVersion = "v1beta"
)

// codeAssistClient handles requests to the Code Assist API using OAuth tokens
type codeAssistClient struct {
	token      string
	httpClient *http.Client
}

// newCodeAssistClient creates a new Code Assist API client with the given OAuth token
func newCodeAssistClient(token string) *codeAssistClient {
	return &codeAssistClient{
		token:      token,
		httpClient: providers.ShortTimeoutClient,
	}
}


// vertexGenerateContentRequest represents the Vertex AI format request
type vertexGenerateContentRequest struct {
	Contents          []*genai.Content        `json:"contents"`
	SystemInstruction *genai.Content          `json:"systemInstruction,omitempty"`
	GenerationConfig  *vertexGenerationConfig `json:"generationConfig,omitempty"`
}

// vertexGenerationConfig represents generation configuration
type vertexGenerationConfig struct {
	Temperature    *float32        `json:"temperature,omitempty"`
	ThinkingConfig *thinkingConfig `json:"thinkingConfig,omitempty"`
}

// thinkingConfig represents thinking configuration
type thinkingConfig struct {
	IncludeThoughts bool   `json:"includeThoughts"`
	ThinkingBudget  *int32 `json:"thinkingBudget,omitempty"`
}

// generateContentResponse represents the response from Code Assist API
type generateContentResponse struct {
	Response vertexGenerateContentResponse `json:"response"`
}

// vertexGenerateContentResponse represents the Vertex AI format response
type vertexGenerateContentResponse struct {
	Candidates    []candidate    `json:"candidates"`
	UsageMetadata *usageMetadata `json:"usageMetadata,omitempty"`
}

// candidate represents a response candidate
type candidate struct {
	Content      *genai.Content `json:"content"`
	FinishReason string         `json:"finishReason,omitempty"`
}

// usageMetadata represents token usage information
type usageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

// generateContentStream makes a streaming request to the Code Assist API
func (c *codeAssistClient) generateContentStream(ctx context.Context, model string, contents []*genai.Content, cfg *genai.GenerateContentConfig) (*codeAssistStreamIterator, error) {
	// Use regular Gemini API request format instead of Code Assist format
	req := vertexGenerateContentRequest{
		Contents:          contents,
		SystemInstruction: cfg.SystemInstruction,
		GenerationConfig: &vertexGenerationConfig{
			Temperature: cfg.Temperature,
		},
	}

	if cfg.ThinkingConfig != nil {
		req.GenerationConfig.ThinkingConfig = &thinkingConfig{
			IncludeThoughts: cfg.ThinkingConfig.IncludeThoughts,
			ThinkingBudget:  cfg.ThinkingConfig.ThinkingBudget,
		}
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// Use regular Gemini API format: /v1beta/models/{model}:streamGenerateContent
	url := fmt.Sprintf("%s/%s/models/%s:streamGenerateContent?alt=sse", codeAssistEndpoint, codeAssistAPIVersion, model)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.token)

	// Use the existing httpClient from the struct
	providers.InitAllHTTPClients()
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, fmt.Errorf("code assist api error: status %d: %s", resp.StatusCode, string(body))
	}

	return &codeAssistStreamIterator{
		reader: resp.Body,
	}, nil
}

// codeAssistStreamIterator handles streaming responses from Code Assist API
type codeAssistStreamIterator struct {
	reader io.ReadCloser
	err    error
}

// Next returns the next chunk in the stream
func (it *codeAssistStreamIterator) Next() (*genai.GenerateContentResponse, error) {
	if it.err != nil {
		return nil, it.err
	}

	// Read SSE data
	var line string
	var data strings.Builder

	buf := make([]byte, 1)
	for {
		n, err := it.reader.Read(buf)
		if err != nil {
			if err == io.EOF {
				it.err = io.EOF
				_ = it.reader.Close()
				return nil, io.EOF
			}
			it.err = err
			_ = it.reader.Close()
			return nil, err
		}
		if n == 0 {
			continue
		}

		if buf[0] == '\n' {
			if line == "" && data.Len() > 0 {
				// Empty line means end of event
				break
			}
			if after, found := strings.CutPrefix(line, "data: "); found {
				data.WriteString(after)
				data.WriteString("\n")
			}
			line = ""
		} else {
			line += string(buf[0])
		}
	}

	if data.Len() == 0 {
		return it.Next() // Skip empty events
	}

	// Parse the JSON data
	var caResp generateContentResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(data.String())), &caResp); err != nil {
		it.err = fmt.Errorf("decode response: %w", err)
		_ = it.reader.Close()
		return nil, it.err
	}

	// Convert to genai.GenerateContentResponse
	genaiResp := &genai.GenerateContentResponse{}
	for _, cand := range caResp.Response.Candidates {
		genaiCand := &genai.Candidate{
			Content:      cand.Content,
			FinishReason: genai.FinishReason(cand.FinishReason),
		}
		genaiResp.Candidates = append(genaiResp.Candidates, genaiCand)
	}

	if caResp.Response.UsageMetadata != nil {
		genaiResp.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     int32(caResp.Response.UsageMetadata.PromptTokenCount),
			CandidatesTokenCount: int32(caResp.Response.UsageMetadata.CandidatesTokenCount),
			TotalTokenCount:      int32(caResp.Response.UsageMetadata.TotalTokenCount),
		}
	}

	return genaiResp, nil
}

// Close closes the stream
func (it *codeAssistStreamIterator) Close() error {
	return it.reader.Close()
}


// loadCodeAssist loads Code Assist configuration
func (c *codeAssistClient) loadCodeAssist(_ context.Context) error {
	// Regular Gemini API doesn't have a loadCodeAssist endpoint
	// Skip this call when using generativelanguage API
	return nil
}
