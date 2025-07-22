package openrouter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	providers "github.com/nathants/nina/providers"
	util "github.com/nathants/nina/util"
	"os"
	"strings"
)

func init() {
	providers.InitAllHTTPClients()
}

type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

type ContentPart struct {
	Type     string   `json:"type"`
	Text     string   `json:"text,omitempty"`
	ImageURL ImageURL `json:"image_url"`
}

type Message struct {
	Role       string `json:"role"`
	Content    any    `json:"content"`
	Name       string `json:"name,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

type ToolFunction struct {
	Description string `json:"description,omitempty"`
	Name        string `json:"name"`
	Parameters  any    `json:"parameters"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type Reasoning struct {
	Effort    string `json:"effort,omitempty"`
	MaxTokens int    `json:"max_tokens,omitempty"`
	Exclude   bool   `json:"exclude,omitempty"`
}

type Provider struct {
	Order             []string       `json:"order,omitempty"`
	AllowFallbacks    bool           `json:"allow_fallbacks,omitempty"`
	RequireParameters bool           `json:"require_parameters,omitempty"`
	DataCollection    string         `json:"data_collection,omitempty"`
	Only              []string       `json:"only,omitempty"`
	Ignore            []string       `json:"ignore,omitempty"`
	Quantizations     []string       `json:"quantizations,omitempty"`
	Sort              string         `json:"sort,omitempty"`
	MaxPrice          map[string]any `json:"max_price,omitempty"`
}

type Request struct {
	Messages          []Message         `json:"messages"`
	Model             string            `json:"model,omitempty"`
	Provider          *Provider         `json:"provider,omitempty"`
	ResponseFormat    map[string]string `json:"response_format,omitempty"`
	Stop              any               `json:"stop,omitempty"`
	Stream            bool              `json:"stream,omitempty"`
	MaxTokens         int               `json:"max_tokens,omitempty"`
	Temperature       float64           `json:"temperature,omitempty"`
	Tools             []Tool            `json:"tools,omitempty"`
	ToolChoice        any               `json:"tool_choice,omitempty"`
	Seed              int               `json:"seed,omitempty"`
	TopP              float64           `json:"top_p,omitempty"`
	TopK              int               `json:"top_k,omitempty"`
	FrequencyPenalty  float64           `json:"frequency_penalty,omitempty"`
	PresencePenalty   float64           `json:"presence_penalty,omitempty"`
	RepetitionPenalty float64           `json:"repetition_penalty,omitempty"`
	LogitBias         map[int]float64   `json:"logit_bias,omitempty"`
	TopLogprobs       int               `json:"top_logprobs,omitempty"`
	MinP              float64           `json:"min_p,omitempty"`
	TopA              float64           `json:"top_a,omitempty"`
	Transforms        []string          `json:"transforms,omitempty"`
	Models            []string          `json:"models,omitempty"`
	Route             string            `json:"route,omitempty"`
	Reasoning         *Reasoning        `json:"reasoning,omitempty"`
}

type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

type ChoiceMessage struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

type ChoiceDelta struct {
	Role      string     `json:"role,omitempty"`
	Content   string     `json:"content"`
	Reasoning string     `json:"reasoning,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

type Choice struct {
	FinishReason       string         `json:"finish_reason"`
	NativeFinishReason string         `json:"native_finish_reason"`
	Message            *ChoiceMessage `json:"message,omitempty"`
	Delta              *ChoiceDelta   `json:"delta,omitempty"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type Response struct {
	ID                string   `json:"id"`
	Choices           []Choice `json:"choices"`
	Created           int64    `json:"created"`
	Model             string   `json:"model"`
	Object            string   `json:"object"`
	SystemFingerprint string   `json:"system_fingerprint,omitempty"`
	Usage             *Usage   `json:"usage,omitempty"`
}

func HandleOpenRouterChat(ctx context.Context, req Request, reasoningCallback func(data string)) (string, error) {

	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("json marshal error: %w", err)
	}

	outReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://openrouter.ai/api/v1/chat/completions", bytes.NewBuffer(body))
	if err != nil {
		return "", fmt.Errorf("request creation error: %w", err)
	}

	outReq.Header.Set("Content-Type", "application/json")
	outReq.Header.Set("Authorization", "Bearer "+os.Getenv("OPENROUTER_KEY"))
	outReq.Header.Set("HTTP-Referer", "https://ninabot.com")
	outReq.Header.Set("X-Title", "NinaBot")
	if req.Stream {
		outReq.Header.Set("Accept", "text/event-stream")
	}

	client := providers.LongTimeoutClient
	resp, err := client.Do(outReq)
	if err != nil {
		return "", fmt.Errorf("do request error: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		resBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("api error: %s", string(resBody))
	}

	if !req.Stream {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("panic in goroutine: %v", r)
				}
			}()
			<-ctx.Done()
			_ = resp.Body.Close()
		}()

		resBody, err := io.ReadAll(resp.Body)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", fmt.Errorf("error reading response: %w", err)
		}

		var or Response
		err = json.Unmarshal(resBody, &or)
		if err != nil {
			return "", fmt.Errorf("failed to unmarshal json: %w", err)
		}

		if or.Usage != nil {
			fmt.Println(util.Pformat(or.Usage))
		}

		if len(or.Choices) > 0 {
			return or.Choices[0].Message.Content, nil
		}
		return "", fmt.Errorf("no choices returned")
	}

	reader := bufio.NewReader(resp.Body)
	var answerBuilder strings.Builder
	var reasoningBuilder strings.Builder
	var eventData strings.Builder

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", fmt.Errorf("stream read error: %w", err)
		}

		line = strings.TrimSpace(line)

		if strings.HasPrefix(line, ":") {
			continue
		}

		if strings.HasPrefix(line, "data:") {
			eventData.WriteString(strings.TrimSpace(line[5:]))
			continue
		}

		if line == "" && eventData.Len() > 0 {
			eventJSON := eventData.String()
			eventData.Reset()

			if eventJSON == "[DONE]" {
				break
			}

			var chunk Response
			if err := json.Unmarshal([]byte(eventJSON), &chunk); err != nil {
				fmt.Println("unmarshal event error:", err)
				continue
			}

			if len(chunk.Choices) > 0 {
				choice := chunk.Choices[0]

				if choice.Delta.Reasoning != "" {
					reasoningBuilder.WriteString(choice.Delta.Reasoning)

					if strings.Contains(reasoningBuilder.String(), "\n\n") {
						if ctx.Err() == nil && reasoningCallback != nil {
							parts := strings.SplitN(reasoningBuilder.String(), "\n\n", 2)
							reasoningCallback(parts[0])
							reasoningBuilder.Reset()
							if len(parts) > 1 {
								reasoningBuilder.WriteString(parts[1])
							}
						}
					}
				}

				if choice.Delta.Content != "" {
					answerBuilder.WriteString(choice.Delta.Content)
				}

				if choice.FinishReason != "" && choice.FinishReason != "null" {
					if reasoningBuilder.Len() > 0 {
						if ctx.Err() == nil && reasoningCallback != nil {
							reasoningCallback(reasoningBuilder.String())
						}
						reasoningBuilder.Reset()
					}

					if choice.FinishReason == "stop" && chunk.Usage != nil {
						fmt.Println(util.Pformat(chunk.Usage))
					}
				}
			}
		}
	}

	return answerBuilder.String(), nil
}
