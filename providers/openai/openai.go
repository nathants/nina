// CRITICAL: we only use the responses api, we do NOT use the completions api
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"

	providers "nina/providers"
	util "nina/util"
	"os"
	"strings"
	"sync"
	"time"
)

func init() {
	providers.InitAllHTTPClients()
}

// --- authentication helpers -----------------------------------------------

// var authOnce sync.Once

// getAuthToken chooses between OAuth-generated API key and OPENAI_KEY.
func getAuthToken() string {
	token := os.Getenv("OPENAI_OAUTH_API_KEY")
	if token != "" {
		// authOnce.Do(func() { fmt.Fprintln(os.Stderr, "Using OAuth authentication for OpenAI API") })
		return token
	}
	// authOnce.Do(func() { fmt.Fprintln(os.Stderr, "Using API key authentication for OpenAI API") })
	return os.Getenv("OPENAI_KEY")
}

type Response struct {
	CreatedAt          int64          `json:"created_at"`
	Error              any            `json:"error"`
	ID                 string         `json:"id"`
	IncompleteDetails  any            `json:"incomplete_details"`
	Instructions       any            `json:"instructions"`
	MaxOutputTokens    any            `json:"max_output_tokens"`
	Metadata           map[string]any `json:"metadata"`
	Model              string         `json:"model"`
	Object             string         `json:"object"`
	Output             []Output       `json:"output"`
	ParallelToolCalls  bool           `json:"parallel_tool_calls"`
	PreviousResponseID any            `json:"previous_response_id"`
	Reasoning          Reasoning      `json:"reasoning"`
	ServiceTier        string         `json:"service_tier"`
	Status             string         `json:"status"`
	Store              bool           `json:"store"`
	Temperature        float64        `json:"temperature"`
	Text               Text           `json:"text"`
	ToolChoice         string         `json:"tool_choice"`
	Tools              []any          `json:"tools"`
	TopP               float64        `json:"top_p"`
	Truncation         string         `json:"truncation"`
	Usage              Usage          `json:"usage"`
	User               any            `json:"user"`
}

// Output represents each element in the "output" array.
type Output struct {
	Content []Content `json:"content"`
	ID      string    `json:"id"`
	Role    string    `json:"role"`
	Status  string    `json:"status"`
	Type    string    `json:"type"`
}

// Content represents each item in an Output's "content" array.
type Content struct {
	Annotations []any  `json:"annotations"`
	Text        string `json:"text"`
	Type        string `json:"type"`
}

// Text wraps the formatting details.
type Text struct {
	Format Format `json:"format"`
}

// Format describes the format type.
type Format struct {
	Type string `json:"type"`
}

// Usage captures token usage statistics.
type Usage struct {
	InputTokens         int                 `json:"input_tokens"`
	InputTokensDetails  InputTokensDetails  `json:"input_tokens_details"`
	OutputTokens        int                 `json:"output_tokens"`
	OutputTokensDetails OutputTokensDetails `json:"output_tokens_details"`
	TotalTokens         int                 `json:"total_tokens"`
}

// InputTokensDetails holds details about input tokens.
type InputTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

// OutputTokensDetails holds details about output tokens.
type OutputTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

// Reasoning holds any reasoning metadata.
type Reasoning struct {
	Effort  any `json:"effort"`
	Summary any `json:"summary"`
}

type SummaryTextPart struct {
	Text string `json:"text"`
	Type string `json:"type"`
}

type ResponseReasoningSummaryPartDone struct {
	ItemID       string          `json:"item_id"`
	OutputIndex  int             `json:"output_index"`
	Part         SummaryTextPart `json:"part"`
	SummaryIndex int             `json:"summary_index"`
	Type         string          `json:"type"`
}

type ContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

type ChatMessage struct {
	Type    string        `json:"type"` // always "message"
	Role    string        `json:"role"`
	Content []ContentPart `json:"content"`
}

type ReasoningRequest struct {
	Summary string `json:"summary"`
	Effort  string `json:"effort"`
}

type Request struct {
	Model           string            `json:"model"`
	ServiceTier     string            `json:"service_tier,omitempty"`
	Input           []ChatMessage     `json:"input"`
	Instructions    string            `json:"instructions,omitempty"`
	Reasoning       *ReasoningRequest `json:"reasoning,omitempty"`
	Temperature     *float64          `json:"temperature,omitempty"`
	MaxOutputTokens *int              `json:"max_output_tokens,omitempty"`
	Stream          bool              `json:"stream"`
	Store           bool              `json:"store"`
	User            string            `json:"user"`
	PreviousID      string            `json:"previous_response_id,omitempty"`
}

/*
   Top-level structs for unmarshalling the “response.completed” event
   (no nested anonymous structs as requested).
*/

type ResponsePayload struct {
	Usage Usage `json:"usage"`
}

type ResponseCompletedEvent struct {
	Type     string          `json:"type"`
	Response ResponsePayload `json:"response"`
}

// HandleResponse holds the response data from the Handle function including
// the response text, usage statistics, and response ID for conversation state.
type HandleResponse struct {
	Text       string
	Usage      *Usage
	ResponseID string
}

var logModelOnce sync.Once

func Handle(ctx context.Context, req Request, reasoningCallback func(data string)) (*HandleResponse, error) {
	logModelOnce.Do(func() {
		effort := ""
		if req.Reasoning != nil {
			effort = "effort=" + req.Reasoning.Effort
		}
		fmt.Fprintln(os.Stderr, "model="+ req.Model, "serviceTier="+ req.ServiceTier, effort)
	})

	// fmt.Println("calling openai", req.Model)
	if req.Temperature != nil {
		// fmt.Println("temp:", *req.Temperature)
	}
	if req.Reasoning != nil {
		// fmt.Println("reasoning:", req.Reasoning.Effort)
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("json marshal error: %w", err)
	}

	outReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/responses", bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("request creation error: %w", err)
	}

	outReq.Header.Set("Content-Type", "application/json")
	outReq.Header.Set("Authorization", "Bearer "+getAuthToken())
	if req.Stream {
		outReq.Header.Set("Accept", "text/event-stream")
	}

	// ---------------------------------------------------------------------
	// Execute the request
	// ---------------------------------------------------------------------

	client := providers.LongTimeoutClient
	resp, err := client.Do(outReq)
	if err != nil {
		return nil, fmt.Errorf("do request error: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		resBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("api error: %s", string(resBody))
	}

	if !req.Stream {
		go func() {
			// defer func() {}()
			<-ctx.Done()
			_ = resp.Body.Close()
		}()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, err
		}

		var val Response
		err = json.Unmarshal(data, &val)
		if err != nil {
			return nil, err
		}
		for _, output := range val.Output {
			if output.Type == "message" {
				res := &HandleResponse{
					Text:       output.Content[0].Text,
					Usage:      &val.Usage,
					ResponseID: val.ID,
				}
				return res, nil
			}
		}

		return nil, fmt.Errorf("no message output returned")
	}

	var answerBuilder strings.Builder
	var reasoningSummaryBuilder strings.Builder
	var eventData strings.Builder
	var raw map[string]any
	var responseID string

	reader := bufio.NewReader(resp.Body)

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("stream read error: %w", err)
		}

		if strings.HasPrefix(line, "data:") {
			eventData.WriteString(strings.TrimSpace(line[5:]))
			continue
		}
		if line != "\n" && line != "\r\n" {
			continue
		}

		eventJSON := strings.TrimSpace(eventData.String())
		eventData.Reset()

		if len(eventJSON) == 0 || eventJSON == "[DONE]" {
			continue
		}

		val := map[string]any{}
		if err := json.Unmarshal([]byte(eventJSON), &val); err != nil {
			fmt.Println("unmarshal event error:", err)
			continue
		}

		evtType, _ := val["type"].(string)

		switch evtType {

		case "response.created":
			// Extract response ID from the first streamed event
			if response, ok := val["response"].(map[string]any); ok {
				if id, ok := response["id"].(string); ok {
					responseID = id
				}
			}

		case "response.reasoning_summary_text.delta":
			delta, ok := val["delta"].(string)
			if ok {
				reasoningSummaryBuilder.WriteString(delta)
			}

		case "response.reasoning_summary_text.done":
			if ctx.Err() == nil {
				reasoningCallback(reasoningSummaryBuilder.String())
			}
			reasoningSummaryBuilder.Reset()

		case "response.output_text.delta":
			delta, ok := val["delta"].(string)
			if ok {
				answerBuilder.WriteString(delta)
			}

		case "response.completed":
			raw = val

		case "error", "response.failed", "response.incomplete":
			return nil, fmt.Errorf("api stream error: %s", util.Pformat(val))

		default:

		}
	}

	var val ResponseCompletedEvent
	data, err := json.Marshal(raw)
	if err != nil {
		panic(err)
	}
	err = json.Unmarshal(data, &val)
	if err != nil {
		panic(err)
	}

	return &HandleResponse{
		Text:       answerBuilder.String(),
		Usage:      &val.Response.Usage,
		ResponseID: responseID,
	}, nil
}

// -----------------------------------------------------------------------------
// Batch API types (mirrors the Claude batch helper types for a unified interface)
// -----------------------------------------------------------------------------

// BatchRequestItem wraps a single request that will become one JSONL line
// in the uploaded batch file.
type BatchRequestItem struct {
	CustomID string  `json:"custom_id"`
	Params   Request `json:"params"`
}

// BatchRequestCounts matches the “request_counts” object in the batch
// status responses.
type BatchRequestCounts struct {
	Total     int `json:"total"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
}

// BatchResponse is a pared-down representation of the OpenAI batch
// object. Only the fields needed by the helper are kept.
type BatchResponse struct {
	ID            string             `json:"id"`
	Object        string             `json:"object"`
	Endpoint      string             `json:"endpoint"`
	Status        string             `json:"status"`
	OutputFileID  *string            `json:"output_file_id"`
	ErrorFileID   *string            `json:"error_file_id"`
	RequestCounts BatchRequestCounts `json:"request_counts"`
}

// BatchIndividualResult matches one line of the batch output file.
type BatchIndividualResult struct {
	ID       string         `json:"id"`
	CustomID string         `json:"custom_id"`
	Response map[string]any `json:"response,omitempty"`
	Error    map[string]any `json:"error,omitempty"`
}

// -----------------------------------------------------------------------------
// HandleOpenAIBatch – high-level helper
// -----------------------------------------------------------------------------

// HandleBatch sends multiple requests to OpenAI using the Batch API and
// returns all individual results once the batch has finished processing. The
// function follows the same calling convention as HandleClaudeBatch so callers
// can switch providers without changing code.
func HandleBatch(ctx context.Context, requests []BatchRequestItem) ([]BatchIndividualResult, error) {

	// ---------------------------------------------------------------------
	// Build the JSONL file content (one request per line)
	// ---------------------------------------------------------------------
	var jsonlLines []string
	for _, item := range requests {
		lineObj := struct {
			CustomID string  `json:"custom_id"`
			Method   string  `json:"method"`
			URL      string  `json:"url"`
			Body     Request `json:"body"`
		}{
			CustomID: item.CustomID,
			Method:   "POST",
			URL:      "/v1/responses",
			Body:     item.Params,
		}

		b, err := json.Marshal(lineObj)
		if err != nil {
			return nil, fmt.Errorf("json marshal input line: %v", err)
		}
		jsonlLines = append(jsonlLines, string(b))
	}
	fileContent := strings.Join(jsonlLines, "\n")

	// ---------------------------------------------------------------------
	// Upload the file (multipart/form-data, purpose “batch”)
	// ---------------------------------------------------------------------
	var buf bytes.Buffer

	mw := multipart.NewWriter(&buf)

	if err := mw.WriteField("purpose", "batch"); err != nil {
		return nil, fmt.Errorf("write purpose field: %v", err)
	}

	part, err := mw.CreateFormFile("file", "batch.jsonl")
	if err != nil {
		return nil, fmt.Errorf("create form file: %v", err)
	}
	if _, err := part.Write([]byte(fileContent)); err != nil {
		return nil, fmt.Errorf("write file content: %v", err)
	}
	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("close multipart writer: %v", err)
	}

	uploadReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/files", &buf)
	if err != nil {
		return nil, fmt.Errorf("file upload request creation: %v", err)
	}
	uploadReq.Header.Set("Authorization", "Bearer "+getAuthToken())
	uploadReq.Header.Set("Content-Type", mw.FormDataContentType())

	uploadResp, err := providers.LongTimeoutClient.Do(uploadReq)
	if err != nil {
		return nil, fmt.Errorf("file upload request error: %v", err)
	}
	defer func() { _ = uploadResp.Body.Close() }()

	if uploadResp.StatusCode != http.StatusOK {
		resBody, _ := io.ReadAll(uploadResp.Body)
		return nil, fmt.Errorf("file upload api error: %s", string(resBody))
	}

	var uploadData struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(uploadResp.Body).Decode(&uploadData); err != nil {
		return nil, fmt.Errorf("decode upload response: %v", err)
	}

	// ---------------------------------------------------------------------
	// Create batch
	// ---------------------------------------------------------------------
	createBody, err := json.Marshal(map[string]any{
		"input_file_id":     uploadData.ID,
		"endpoint":          "/v1/responses",
		"completion_window": "24h",
	})
	if err != nil {
		return nil, fmt.Errorf("marshal batch create body: %v", err)
	}

	createReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/batches", bytes.NewBuffer(createBody))
	if err != nil {
		return nil, fmt.Errorf("batch create request creation: %v", err)
	}
	createReq.Header.Set("Authorization", "Bearer "+getAuthToken())
	createReq.Header.Set("Content-Type", "application/json")

	createResp, err := providers.LongTimeoutClient.Do(createReq)
	if err != nil {
		return nil, fmt.Errorf("batch create request error: %v", err)
	}
	defer func() { _ = createResp.Body.Close() }()

	if createResp.StatusCode != http.StatusOK {
		resBody, _ := io.ReadAll(createResp.Body)
		return nil, fmt.Errorf("batch create api error: %s", string(resBody))
	}

	var batchResp BatchResponse
	if err := json.NewDecoder(createResp.Body).Decode(&batchResp); err != nil {
		return nil, fmt.Errorf("decode batch create response: %v", err)
	}

	start := time.Now()
	fmt.Printf("Created OpenAI batch %s with %d requests\n", batchResp.ID, len(requests))

	// ---------------------------------------------------------------------
	// Poll for completion
	// ---------------------------------------------------------------------
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		if batchResp.Status == "completed" || batchResp.Status == "failed" || batchResp.Status == "cancelled" {
			break
		}

		elapsed := time.Since(start).Seconds()
		fmt.Printf("Batch %s status: %s (completed: %d, failed: %d) %.1f seconds\n",
			batchResp.ID,
			batchResp.Status,
			batchResp.RequestCounts.Completed,
			batchResp.RequestCounts.Failed,
			elapsed)

		time.Sleep(5 * time.Second)

		statusReq, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://api.openai.com/v1/batches/%s", batchResp.ID), nil)
		if err != nil {
			return nil, fmt.Errorf("status request creation: %v", err)
		}
		statusReq.Header.Set("Authorization", "Bearer "+getAuthToken())

		statusResp, err := providers.LongTimeoutClient.Do(statusReq)
		if err != nil {
			return nil, fmt.Errorf("status request error: %v", err)
		}

		if statusResp.StatusCode != http.StatusOK {
			resBody, _ := io.ReadAll(statusResp.Body)
			_ = statusResp.Body.Close()
			return nil, fmt.Errorf("status api error: %s", string(resBody))
		}

		if err := json.NewDecoder(statusResp.Body).Decode(&batchResp); err != nil {
			_ = statusResp.Body.Close()
			return nil, fmt.Errorf("decode status response: %v", err)
		}
		_ = statusResp.Body.Close()
	}

	fmt.Printf("Batch %s completed with status %s\n", batchResp.ID, batchResp.Status)

	if batchResp.OutputFileID == nil {
		return nil, fmt.Errorf("no output file ID available")
	}

	// ---------------------------------------------------------------------
	// Download output file and parse JSONL results
	// ---------------------------------------------------------------------
	outputReq, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://api.openai.com/v1/files/%s/content", *batchResp.OutputFileID), nil)
	if err != nil {
		return nil, fmt.Errorf("output file request creation: %v", err)
	}
	outputReq.Header.Set("Authorization", "Bearer "+getAuthToken())

	outputResp, err := providers.LongTimeoutClient.Do(outputReq)
	if err != nil {
		return nil, fmt.Errorf("output file request error: %v", err)
	}
	defer func() { _ = outputResp.Body.Close() }()

	if outputResp.StatusCode != http.StatusOK {
		resBody, _ := io.ReadAll(outputResp.Body)
		return nil, fmt.Errorf("output file api error: %s", string(resBody))
	}

	var results []BatchIndividualResult
	scanner := bufio.NewScanner(outputResp.Body)

	val := make([]byte, 0, 1024*1024) // 1 MB initial buffer
	scanner.Buffer(val, 10*1024*1024) // allow lines up to 10 MB
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var res BatchIndividualResult
		if err := json.Unmarshal([]byte(line), &res); err != nil {
			return nil, fmt.Errorf("unmarshal result line: %v", err)
		}
		results = append(results, res)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read output file: %v", err)
	}

	return results, nil
}
