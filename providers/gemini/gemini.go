package gemini

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"google.golang.org/genai"
	providers "github.com/nathants/nina/providers"
	oauth "github.com/nathants/nina/providers/oauth"
)

func init() {
	providers.InitAllHTTPClients()
}

var (
	defaultTemperature = float32(0.7)
	onceClient         sync.Once
	cli                *genai.Client
	cliErr             error
)

func getClient(ctx context.Context) (*genai.Client, error) {
	onceClient.Do(func() {
		// Prefer OAuth access token if available.
		token := os.Getenv("GEMINI_OAUTH_TOKEN")

		apiKey := ""

		if token == "" {
			apiKey = os.Getenv("GOOGLE_AISTUDIO_TOKEN")
		}

		// Check if we have valid credentials
		if token != "" && apiKey == "" {
			// OAuth token is available, but we can't use it with genai.Client
			// We'll handle OAuth separately in the Handle function
			// oncePrint.Do(func() { fmt.Fprintln(os.Stderr, "gemini using OAuth") })
			cliErr = fmt.Errorf("oauth-mode")
			return
		} else if apiKey != "" {
			// oncePrint.Do(func() { fmt.Fprintln(os.Stderr, "gemini using API key") })
		} else {
			cliErr = fmt.Errorf("no gemini credentials: login with ask-login -p gemini or set GOOGLE_AISTUDIO_TOKEN")
			return
		}

		cfg := &genai.ClientConfig{
			APIKey:  apiKey,
			Backend: genai.BackendGeminiAPI,
		}
		cli, cliErr = genai.NewClient(ctx, cfg)
	})
	return cli, cliErr
}

// authTransport adds an Authorization Bearer header to each request.
func inlineURL(ctx context.Context, rawURL string) (*genai.Part, error) {

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := providers.ShortTimeoutClient.Do(req)
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = http.DetectContentType(data)
	}
	if !strings.HasPrefix(ct, "image/") {
		return nil, fmt.Errorf("not an image: %s", ct)
	}
	part := genai.NewPartFromBytes(data, ct)
	return part, nil
}

func fileToBlob(path string) (*genai.Part, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	ext := filepath.Ext(path)
	mimeType := mime.TypeByExtension(ext)
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	return genai.NewPartFromBytes(data, mimeType), nil
}

type ChatMessage struct {
	Role    genai.Role `json:"role"`
	Content string     `json:"content"`
}

var logModelOnce sync.Once

func Handle(ctx context.Context, model string, system string, messages []string, imageUrls []string, reasoningCallback func(string), noThoughts bool, thinkingBudget int) (string, error) {

	logModelOnce.Do(func() {
		thinking := ""
		if thinkingBudget > 0 {
			thinking = fmt.Sprintf("thinking=%d", thinkingBudget)
		}
		fmt.Fprintln(os.Stderr, "model="+model, thinking)
	})

	// Check if we should use OAuth with Code Assist API
	token, _ := oauth.GeminiAccess()
	if token != "" {
		// Prefer OAuth over API key
		return handleWithCodeAssist(ctx, token, model, system, messages, imageUrls, reasoningCallback, noThoughts, thinkingBudget)
	}

	client, err := getClient(ctx)
	if err != nil && err.Error() != "oauth-mode" {
		return "", err
	}

	contents := []*genai.Content{}
	for _, msg := range messages {
		contents = append(contents, genai.NewContentFromText(msg, genai.RoleUser))
	}

	for _, u := range imageUrls {
		var part *genai.Part
		var convErr error
		if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
			part, convErr = inlineURL(ctx, u)
		} else if strings.HasPrefix(u, "data:") {
			comma := strings.Index(u, ",")
			if comma == -1 {
				convErr = fmt.Errorf("invalid data url")
			} else {
				meta := u[5:comma]
				mimeType := meta
				if semi := strings.Index(meta, ";"); semi != -1 {
					mimeType = meta[:semi]
				}
				decoded, decErr := base64.StdEncoding.DecodeString(u[comma+1:])
				if decErr != nil {
					convErr = decErr
				} else {
					part = genai.NewPartFromBytes(decoded, mimeType)
				}
			}
		} else {
			part, convErr = fileToBlob(u)
		}
		if convErr != nil {
			// fmt.Println("gemini: skip image:", convErr)
			continue
		}
		contents = append(contents, genai.NewContentFromParts([]*genai.Part{part}, ""))
	}

	budget := int32(24000)
	cfg := &genai.GenerateContentConfig{
		SystemInstruction: genai.NewContentFromText(system, genai.RoleUser),
		Temperature:       genai.Ptr(defaultTemperature),
		ThinkingConfig: &genai.ThinkingConfig{
			IncludeThoughts: !noThoughts,
			ThinkingBudget:  &budget,
		},
	}
	if thinkingBudget != 0 {
		budget := int32(thinkingBudget)
		cfg.ThinkingConfig.ThinkingBudget = &budget
	}

	// fmt.Println("gemini send")

	stream := client.Models.GenerateContentStream(ctx, model, contents, cfg)

	var answerBuilder strings.Builder
	// var usage *genai.GenerateContentResponseUsageMetadata

	// start := time.Now()
	for chunk, err := range stream {
		if err != nil {
			return "", err
		}

		if chunk == nil {
			continue
		}

		if chunk.UsageMetadata != nil {
			// usage = chunk.UsageMetadata
		}

		for _, cand := range chunk.Candidates {
			if cand.Content == nil {
				continue
			}
			for _, part := range cand.Content.Parts {
				if part.Thought && reasoningCallback != nil {
					reasoningCallback(strings.TrimSpace(part.Text))
				} else if part.Text != "" {
					answerBuilder.WriteString(part.Text)
				}
			}
		}
	}
	// fmt.Printf("Gemini finished in %.1fs\n", time.Since(start).Seconds())

	// if usage != nil {
	// 	fmt.Println("Gemini usage:", lib.Pformat(usage))
	// }

	return answerBuilder.String(), nil
}

// handleWithCodeAssist handles requests using OAuth with the Code Assist API
func handleWithCodeAssist(ctx context.Context, token, model, system string, messages []string, imageUrls []string, reasoningCallback func(string), noThoughts bool, thinkingBudget int) (string, error) {
	// Create Code Assist client
	client := newCodeAssistClient(token)

	// Try to load Code Assist (this may help with permissions)
	if err := client.loadCodeAssist(ctx); err != nil {
		// Log but don't fail - the actual request might still work
		fmt.Fprintf(os.Stderr, "Warning: loadCodeAssist failed: %v\n", err)
	}

	// Build contents
	contents := []*genai.Content{}
	for _, msg := range messages {
		contents = append(contents, genai.NewContentFromText(msg, genai.RoleUser))
	}

	// Handle images
	for _, u := range imageUrls {
		var part *genai.Part
		var convErr error
		if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
			part, convErr = inlineURL(ctx, u)
		} else if strings.HasPrefix(u, "data:") {
			comma := strings.Index(u, ",")
			if comma == -1 {
				convErr = fmt.Errorf("invalid data url")
			} else {
				meta := u[5:comma]
				mimeType := meta
				if semi := strings.Index(meta, ";"); semi != -1 {
					mimeType = meta[:semi]
				}
				decoded, decErr := base64.StdEncoding.DecodeString(u[comma+1:])
				if decErr != nil {
					convErr = decErr
				} else {
					part = genai.NewPartFromBytes(decoded, mimeType)
				}
			}
		} else {
			part, convErr = fileToBlob(u)
		}
		if convErr != nil {
			continue
		}
		contents = append(contents, genai.NewContentFromParts([]*genai.Part{part}, ""))
	}

	// Build configuration
	budget := int32(24000)
	cfg := &genai.GenerateContentConfig{
		SystemInstruction: genai.NewContentFromText(system, genai.RoleUser),
		Temperature:       genai.Ptr(defaultTemperature),
		ThinkingConfig: &genai.ThinkingConfig{
			IncludeThoughts: !noThoughts,
			ThinkingBudget:  &budget,
		},
	}
	if thinkingBudget != 0 {
		budget := int32(thinkingBudget)
		cfg.ThinkingConfig.ThinkingBudget = &budget
	}

	// Make streaming request
	stream, err := client.generateContentStream(ctx, model, contents, cfg)
	if err != nil {
		return "", err
	}
	defer func() { _ = stream.Close() }()

	var answerBuilder strings.Builder

	// Process stream
	for {
		chunk, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}

		for _, cand := range chunk.Candidates {
			if cand.Content == nil {
				continue
			}
			for _, part := range cand.Content.Parts {
				if part.Thought && reasoningCallback != nil {
					reasoningCallback(strings.TrimSpace(part.Text))
				} else if part.Text != "" {
					answerBuilder.WriteString(part.Text)
				}
			}
		}
	}

	return answerBuilder.String(), nil
}
