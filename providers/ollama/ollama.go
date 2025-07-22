// ollama provides integration with local Ollama server for LLM inference with
// streaming support and full parameter control. Automatically selects the latest
// model when none specified, supports all parameters, and streams responses.
package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
	
	providers "github.com/nathants/nina/providers"
)

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaRequest struct {
	Model            string          `json:"model"`
	Messages         []ollamaMessage `json:"messages"`
	Stream           bool            `json:"stream"`
	Temperature      *float64        `json:"temperature,omitempty"`
	TopP             *float64        `json:"top_p,omitempty"`
	TopK             *int            `json:"top_k,omitempty"`
	Seed             *int            `json:"seed,omitempty"`
	NumPredict       *int            `json:"num_predict,omitempty"`
	NumCtx           *int            `json:"num_ctx,omitempty"`
	RepeatPenalty    *float64        `json:"repeat_penalty,omitempty"`
	RepeatLastN      *int            `json:"repeat_last_n,omitempty"`
	Stop             []string        `json:"stop,omitempty"`
	TfsZ             *float64        `json:"tfs_z,omitempty"`
	TypicalP         *float64        `json:"typical_p,omitempty"`
	PresencePenalty  *float64        `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float64        `json:"frequency_penalty,omitempty"`
}

type ollamaResponse struct {
	Message            ollamaMessage `json:"message"`
	Done               bool          `json:"done"`
	Model              string        `json:"model,omitempty"`
	CreatedAt          string        `json:"created_at,omitempty"`
	DoneReason         string        `json:"done_reason,omitempty"`
	TotalDuration      int64         `json:"total_duration,omitempty"`
	LoadDuration       int64         `json:"load_duration,omitempty"`
	PromptEvalCount    int           `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64         `json:"prompt_eval_duration,omitempty"`
	EvalCount          int           `json:"eval_count,omitempty"`
	EvalDuration       int64         `json:"eval_duration,omitempty"`
}

type ollamaModelInfo struct {
	Name       string    `json:"name"`
	ModifiedAt time.Time `json:"modified_at"`
	Size       int64     `json:"size"`
	Digest     string    `json:"digest"`
	Details    struct {
		Format            string   `json:"format"`
		Family            string   `json:"family"`
		Families          []string `json:"families"`
		ParameterSize     string   `json:"parameter_size"`
		QuantizationLevel string   `json:"quantization_level"`
	} `json:"details"`
}

type ollamaModelsResponse struct {
	Models []ollamaModelInfo `json:"models"`
}

// OllamaConfig holds all configuration parameters for Ollama requests including
// model selection, sampling parameters, and generation constraints. All fields
// are optional except Model which defaults if not specified.
type OllamaConfig struct {
	Model             string
	Temperature       *float64
	TopP              *float64
	TopK              *int
	Seed              *int
	NumPredict        *int
	NumCtx            *int
	RepeatPenalty     *float64
	RepeatLastN       *int
	Stop              []string
	TfsZ              *float64
	TypicalP          *float64
	PresencePenalty   *float64
	FrequencyPenalty  *float64
	Stream            bool
	ReasoningCallback func(string)
}

var (
	ollamaClient *http.Client
)

func init() {
	providers.InitAllHTTPClients()
	ollamaClient = providers.LongTimeoutClient
}

// getOllamaURL returns the Ollama server URL from OLLAMA_URL environment
// variable or defaults to http://localhost:11434 if not set.
func getOllamaURL() string {
	url := os.Getenv("OLLAMA_URL")
	if url == "" {
		return "http://localhost:11434"
	}
	return url
}

// getLatestOllamaModel queries the Ollama server for available models and returns
// the name of the most recently modified model. Returns an error if no models
// are available or if the server cannot be reached.
func getLatestOllamaModel(ctx context.Context) (string, error) {
	url := getOllamaURL() + "/api/tags"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	resp, err := ollamaClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch models: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama error: %s", string(body))
	}

	var modelsResp ollamaModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&modelsResp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if len(modelsResp.Models) == 0 {
		return "", fmt.Errorf("no models available")
	}

	// Find the most recently modified model
	latestModel := modelsResp.Models[0]
	for _, model := range modelsResp.Models[1:] {
		if model.ModifiedAt.After(latestModel.ModifiedAt) {
			latestModel = model
		}
	}

	return latestModel.Name, nil
}

// HandleOllamaChat sends the prompt to an Ollama server (configured via OLLAMA_URL
// env var, defaults to http://localhost:11434) and returns the assistant's reply.
// Streaming is not used – the whole answer is returned in one go which is then
// wrapped in a single "assistant" SSE event by the caller.
func HandleOllamaChat(ctx context.Context, prompt string, model string) (string, error) {
	config := OllamaConfig{
		Model:  model,
		Stream: false,
	}
	return HandleOllamaChatWithConfig(ctx, prompt, config)
}

// HandleOllamaChatWithConfig sends the prompt to a local Ollama server with full
// parameter control. When streaming is enabled, output is sent to the reasoning
// callback and the complete response is returned. System prompts should be
// included in the prompt parameter.
func HandleOllamaChatWithConfig(ctx context.Context, prompt string, config OllamaConfig) (string, error) {
	modelName := config.Model
	if modelName == "ollama" || modelName == "" {
		latestModel, err := getLatestOllamaModel(ctx)
		if err != nil {
			return "", fmt.Errorf("get latest model: %w", err)
		}
		// fmt.Fprintln(os.Stderr, "ollama model:", latestModel)
		modelName = latestModel
	}

	reqBody, err := json.Marshal(ollamaRequest{
		Model:            modelName,
		Messages:         []ollamaMessage{{Role: "user", Content: prompt}},
		Stream:           config.Stream,
		Temperature:      config.Temperature,
		TopP:             config.TopP,
		TopK:             config.TopK,
		Seed:             config.Seed,
		NumPredict:       config.NumPredict,
		NumCtx:           config.NumCtx,
		RepeatPenalty:    config.RepeatPenalty,
		RepeatLastN:      config.RepeatLastN,
		Stop:             config.Stop,
		TfsZ:             config.TfsZ,
		TypicalP:         config.TypicalP,
		PresencePenalty:  config.PresencePenalty,
		FrequencyPenalty: config.FrequencyPenalty,
	})
	if err != nil {
		return "", err
	}

	url := getOllamaURL() + "/api/chat"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := ollamaClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama error: %s", string(body))
	}

	if config.Stream {
		return handleOllamaStream(resp.Body, config.ReasoningCallback)
	}

	var respObj ollamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&respObj); err != nil {
		return "", err
	}

	return respObj.Message.Content, nil
}

// handleOllamaStream processes streaming responses from Ollama, calling the
// reasoning callback with each chunk and accumulating the full response to
// return when complete.
func handleOllamaStream(body io.ReadCloser, reasoningCallback func(string)) (string, error) {
	var fullResponse strings.Builder
	scanner := bufio.NewScanner(body)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var chunk ollamaResponse
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			return "", fmt.Errorf("failed to parse stream chunk: %w", err)
		}

		if chunk.Message.Content != "" {
			content := chunk.Message.Content
			fullResponse.WriteString(content)

			if reasoningCallback != nil {
				reasoningCallback(content)
			}
		}

		if chunk.Done {
			break
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("stream reading error: %w", err)
	}

	return fullResponse.String(), nil
}
